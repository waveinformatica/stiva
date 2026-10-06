package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
)

// ssoProvider is one configured single-sign-on entry: a known preset or a
// generic mechanism with resolved endpoints. It serves the browser
// authorization-code flow and doubles as a Realm, so IdP-issued tokens also
// authenticate API and docker clients directly. CAS has no bearer tokens and
// stays browser-only.
type ssoProvider struct {
	cfg      *resolvedProvider
	key      string // preset key, e.g. "github" (drives provider quirks like orgs)
	client   *http.Client
	authURL  string
	tokenURL string
	// oidc only: verifies ID tokens.
	verifier *oidc.IDTokenVerifier
}

func (p *ssoProvider) Name() string { return "sso:" + p.cfg.ID }

func (p *ssoProvider) AuthenticatePassword(ctx context.Context, username, password string) (*User, error) {
	return nil, ErrNoMatch
}

func (p *ssoProvider) AuthenticateToken(ctx context.Context, token string) (*User, error) {
	switch p.cfg.Kind {
	case SSOKindOIDC:
		if p.verifier == nil {
			return nil, ErrNoMatch
		}
		idt, err := p.verifier.Verify(ctx, token)
		if err != nil {
			return nil, ErrNoMatch
		}
		var raw map[string]any
		if err := idt.Claims(&raw); err != nil {
			return nil, ErrNoMatch
		}
		return p.mapOIDCUser(raw, idt.Subject), nil
	case SSOKindOAuth2:
		data, err := p.fetchUserinfo(ctx, token)
		if err != nil {
			return nil, ErrNoMatch
		}
		return p.mapOAuthUser(ctx, token, data), nil
	default:
		return nil, ErrNoMatch
	}
}

// NewSSOProvider resolves one SSO entry and prepares its runtime. OIDC
// providers run discovery at startup so a misconfigured issuer fails loudly
// here instead of on the first user login.
func NewSSOProvider(ctx context.Context, cfg *SSOConfig) (*ssoProvider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("auth: sso: nil config")
	}
	r, err := resolveProvider(*cfg)
	if err != nil {
		return nil, err
	}
	p := &ssoProvider{
		cfg:    r,
		key:    strings.ToLower(strings.TrimSpace(cfg.Provider)),
		client: &http.Client{Timeout: time.Minute},
	}
	switch r.Kind {
	case SSOKindOIDC:
		if err := p.initOIDC(ctx); err != nil {
			return nil, err
		}
	case SSOKindOAuth2:
		p.authURL, p.tokenURL = r.Authorize, r.Token
	case SSOKindCAS:
		if _, err := url.ParseRequestURI(r.BaseURL); err != nil {
			return nil, fmt.Errorf("auth: sso %q: invalid cas base url: %w", r.ID, err)
		}
	}
	return p, nil
}

// oidcDiscovery is the subset of the discovery document the login flow needs.
type oidcDiscovery struct {
	Issuer      string `json:"issuer"`
	AuthURL     string `json:"authorization_endpoint"`
	TokenURL    string `json:"token_endpoint"`
	UserinfoURL string `json:"userinfo_endpoint"`
	JWKSURL     string `json:"jwks_uri"`
}

func (p *ssoProvider) initOIDC(ctx context.Context) error {
	r := p.cfg
	docURL := trimURL(r.Issuer) + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, docURL, nil)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("auth: sso %q: oidc discovery failed: %w", r.ID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("auth: sso %q: oidc discovery returned %d", r.ID, resp.StatusCode)
	}
	var doc oidcDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("auth: sso %q: oidc discovery is not JSON: %w", r.ID, err)
	}
	if doc.Issuer != "" && doc.Issuer != r.Issuer && trimURL(doc.Issuer) != trimURL(r.Issuer) {
		return fmt.Errorf("auth: sso %q: oidc issuer mismatch (configured %q, discovered %q)", r.ID, r.Issuer, doc.Issuer)
	}
	if doc.JWKSURL == "" || doc.TokenURL == "" {
		return fmt.Errorf("auth: sso %q: oidc discovery misses endpoints", r.ID)
	}
	p.authURL = r.Authorize
	if p.authURL == "" {
		p.authURL = doc.AuthURL
	}
	p.tokenURL = r.Token
	if p.tokenURL == "" {
		p.tokenURL = doc.TokenURL
	}
	if p.authURL == "" || p.tokenURL == "" {
		return fmt.Errorf("auth: sso %q: no authorization/token endpoints", r.ID)
	}
	aud := r.Audience
	if aud == "" {
		aud = r.ClientID
	}
	p.verifier = oidc.NewVerifier(r.Issuer, oidc.NewRemoteKeySet(ctx, doc.JWKSURL), &oidc.Config{ClientID: aud})
	return nil
}

// ---- browser flow ----

// SSOPublic describes one login button. It carries no secrets.
type SSOPublic struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

// callbackURL is the absolute URL the provider returns the user to. It is
// derived from the incoming request, so it tracks the public address through
// any TLS-terminating ingress without extra configuration.
func ssoCallbackURL(c *gin.Context, id string) string {
	return RequestScheme(c) + "://" + c.Request.Host + "/auth/sso/" + id + "/callback"
}

// pkceVerifier makes a fresh code verifier (43-128 base64url chars).
func pkceVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// pkceChallenge derives the S256 code challenge for a verifier.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ssoState issues the login request's state: a short-lived HMAC token binding
// the provider to the PKCE verifier. Stateless, so any replica answers the
// callback; the token secret signs it, so it cannot be forged or retargeted.
func ssoState(secret []byte, providerID, verifier string) (string, error) {
	now := time.Now()
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	payload := map[string]any{
		"typ": "sso-state",
		"pid": providerID,
		"pkc": verifier,
		"exp": now.Add(10 * time.Minute).Unix(),
		"iat": now.Unix(),
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	pb, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	signingInput := b64(hb) + "." + b64(pb)
	return signingInput + "." + hmacSum(secret, signingInput), nil
}

// parseSSOState validates a state token and returns its provider and PKCE
// verifier. Anything unsigned, expired, or of another type is rejected.
func parseSSOState(secret []byte, token string) (providerID, verifier string, err error) {
	claims, err := verifyToken(secret, token)
	if err != nil {
		return "", "", fmt.Errorf("auth: sso: invalid state: %w", err)
	}
	if typ, _ := claims["typ"].(string); typ != "sso-state" {
		return "", "", fmt.Errorf("auth: sso: invalid state type")
	}
	pid, _ := claims["pid"].(string)
	pkc, _ := claims["pkc"].(string)
	if pid == "" || pkc == "" {
		return "", "", fmt.Errorf("auth: sso: malformed state")
	}
	return pid, pkc, nil
}

// authorizeURL completes the code-flow authorization URL once the state token
// (carrying the PKCE verifier) exists.
func (p *ssoProvider) authorizeURL(callback, state, verifier string) string {
	q := url.Values{}
	q.Set("client_id", p.cfg.ClientID)
	q.Set("redirect_uri", callback)
	q.Set("response_type", "code")
	if p.cfg.Scope != "" {
		q.Set("scope", p.cfg.Scope)
	}
	q.Set("state", state)
	q.Set("code_challenge", pkceChallenge(verifier))
	q.Set("code_challenge_method", "S256")
	return p.authURL + "?" + q.Encode()
}

// tokenResponse is the access-token endpoint subset the flows read.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	Token       string `json:"token"`
	IDToken     string `json:"id_token"`
	ExpiresIn   int    `json:"expires_in"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

// exchangeCode trades an authorization code for tokens (PKCE confidential-client
// flow). GitHub answers urlencoded unless asked, hence the JSON accept header.
func (p *ssoProvider) exchangeCode(ctx context.Context, callback, code, verifier string) (*tokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", callback)
	form.Set("client_id", p.cfg.ClientID)
	if p.cfg.Secret != "" {
		form.Set("client_secret", p.cfg.Secret)
	}
	form.Set("code_verifier", verifier)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth: sso %q: token exchange failed: %w", p.cfg.ID, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth: sso %q: token endpoint returned %d", p.cfg.ID, resp.StatusCode)
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("auth: sso %q: token endpoint is not JSON: %w", p.cfg.ID, err)
	}
	if tr.Error != "" {
		desc := tr.ErrorDesc
		if desc == "" {
			desc = tr.Error
		}
		return nil, fmt.Errorf("auth: sso %q: token endpoint: %s", p.cfg.ID, desc)
	}
	if tr.AccessToken == "" {
		tr.AccessToken = tr.Token
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("auth: sso %q: token endpoint returned no access token", p.cfg.ID)
	}
	return &tr, nil
}

// fetchUserinfo GETs the provider userinfo endpoint with a bearer token.
func (p *ssoProvider) fetchUserinfo(ctx context.Context, token string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.UserInfo, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth: sso %q: userinfo returned %d", p.cfg.ID, resp.StatusCode)
	}
	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	return data, nil
}

// githubOrgs lists the organizations the token's user belongs to (returned as
// plain logins, usable as group names). Only meaningful for the github preset.
func (p *ssoProvider) githubOrgs(ctx context.Context, token string) []string {
	base := strings.TrimSuffix(p.cfg.UserInfo, "/user")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/user/orgs", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()
	var orgs []struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&orgs); err != nil {
		return nil
	}
	out := make([]string, 0, len(orgs))
	for _, o := range orgs {
		if o.Login != "" {
			out = append(out, o.Login)
		}
	}
	return out
}

// ---- HTTP surface (wired in main.go alongside /auth/token) ----

// SSOProviders lists the configured login buttons. Secrets never leave the
// server: only the handle, label and mechanism are exposed.
func (m *Manager) SSOProviders() []SSOPublic {
	m.ssoMu.RLock()
	defer m.ssoMu.RUnlock()
	out := make([]SSOPublic, 0, len(m.sso))
	for _, p := range m.sso {
		out = append(out, SSOPublic{ID: p.cfg.ID, Label: p.cfg.Label, Kind: p.cfg.Kind})
	}
	return out
}

func (m *Manager) findSSO(id string) *ssoProvider {
	m.ssoMu.RLock()
	defer m.ssoMu.RUnlock()
	for _, p := range m.sso {
		if p.cfg.ID == id {
			return p
		}
	}
	return nil
}

// SSOListHandler serves GET /auth/sso.
func (m *Manager) SSOListHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"providers": m.SSOProviders()})
}

// SSOLoginHandler serves GET /auth/sso/:id/login by redirecting the browser
// to the provider (authorization endpoint, or the CAS login page).
func (m *Manager) SSOLoginHandler(c *gin.Context) {
	p := m.findSSO(c.Param("id"))
	if p == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown sso provider"})
		return
	}
	callback := ssoCallbackURL(c, p.cfg.ID)
	if p.cfg.Kind == SSOKindCAS {
		q := url.Values{}
		q.Set("service", callback)
		c.Redirect(http.StatusFound, trimURL(p.cfg.BaseURL)+"/login?"+q.Encode())
		return
	}
	verifier, err := pkceVerifier()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot start login"})
		return
	}
	state, err := ssoState(m.secret, p.cfg.ID, verifier)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot start login"})
		return
	}
	c.Redirect(http.StatusFound, p.authorizeURL(callback, state, verifier))
}

// SSOCallbackHandler serves GET /auth/sso/:id/callback: it completes the
// code exchange (or CAS ticket validation), maps the identity, issues a
// registry session token and hands it to the SPA through the URL fragment,
// which browsers never send to any server.
func (m *Manager) SSOCallbackHandler(c *gin.Context) {
	fail := func(msg string) {
		c.Redirect(http.StatusFound, "/#sso_error="+url.QueryEscape(msg))
	}
	p := m.findSSO(c.Param("id"))
	if p == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown sso provider"})
		return
	}
	if e := c.Query("error"); e != "" {
		desc := c.Query("error_description")
		if desc == "" {
			desc = e
		}
		fail(desc)
		return
	}
	callback := ssoCallbackURL(c, p.cfg.ID)
	ctx := c.Request.Context()
	var u *User
	if p.cfg.Kind == SSOKindCAS {
		ticket := c.Query("ticket")
		if ticket == "" {
			fail("missing cas ticket")
			return
		}
		name, attrs, err := validateCASTicket(ctx, p.client, p.cfg.BaseURL, callback, ticket)
		if err != nil {
			fail("ticket validation failed")
			return
		}
		u = p.mapCASUser(name, attrs)
	} else {
		code, stateTok := c.Query("code"), c.Query("state")
		if code == "" || stateTok == "" {
			fail("missing code or state")
			return
		}
		pid, verifier, err := parseSSOState(m.secret, stateTok)
		if err != nil || pid != p.cfg.ID {
			fail("invalid state")
			return
		}
		tr, err := p.exchangeCode(ctx, callback, code, verifier)
		if err != nil {
			fail("code exchange failed")
			return
		}
		if p.cfg.Kind == SSOKindOIDC {
			if tr.IDToken == "" {
				fail("no identity token returned")
				return
			}
			idt, err := p.verifier.Verify(ctx, tr.IDToken)
			if err != nil {
				fail("invalid identity token")
				return
			}
			var raw map[string]any
			if err := idt.Claims(&raw); err != nil {
				fail("invalid identity token")
				return
			}
			u = p.mapOIDCUser(raw, idt.Subject)
		} else {
			data, err := p.fetchUserinfo(ctx, tr.AccessToken)
			if err != nil {
				fail("cannot read user profile")
				return
			}
			u = p.mapOAuthUser(ctx, tr.AccessToken, data)
		}
	}
	if u == nil || strings.TrimSpace(u.Name) == "" {
		fail("empty username from provider")
		return
	}
	tok, err := issueToken(m.secret, u, m.ttl, "")
	if err != nil {
		fail("cannot issue session")
		return
	}
	c.Redirect(http.StatusFound, "/#sso_token="+tok)
}

// firstString returns the first non-empty string found under the given keys.
func firstString(data map[string]any, keys ...string) string {
	for _, k := range keys {
		if k == "" {
			continue
		}
		if s, ok := data[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// mapOIDCUser maps verified ID-token claims to a registry user.
func (p *ssoProvider) mapOIDCUser(raw map[string]any, subject string) *User {
	name := firstString(raw, p.cfg.Username, "preferred_username", "email", "name", "sub")
	if name == "" {
		name = subject
	}
	u := &User{Name: name, Groups: extractGroups(raw[p.cfg.Groups]), Source: p.Name()}
	p.applyAdmin(u)
	return u
}

// mapOAuthUser maps a userinfo document to a registry user.
func (p *ssoProvider) mapOAuthUser(ctx context.Context, token string, data map[string]any) *User {
	name := firstString(data, p.cfg.Username, "login", "username", "email", "name")
	var groups []string
	if p.key == "github" && p.cfg.Groups == "orgs" {
		groups = p.githubOrgs(ctx, token)
	} else {
		groups = extractGroups(data[p.cfg.Groups])
	}
	u := &User{Name: name, Groups: groups, Source: p.Name()}
	p.applyAdmin(u)
	return u
}

// mapCASUser maps a validated CAS ticket (username + attributes) to a user.
// An explicit username_claim names the attribute to use instead of cas:user;
// groups come from the groups_claim attribute (memberOf by convention).
func (p *ssoProvider) mapCASUser(user string, attrs map[string][]string) *User {
	name := user
	if p.cfg.Username != "" {
		if v := attrs[p.cfg.Username]; len(v) > 0 && strings.TrimSpace(v[0]) != "" {
			name = strings.TrimSpace(v[0])
		}
	}
	u := &User{Name: name, Groups: attrs[p.cfg.Groups], Source: p.Name()}
	p.applyAdmin(u)
	return u
}

func (p *ssoProvider) applyAdmin(u *User) {
	if p.cfg.AdminGroup == "" || u == nil {
		return
	}
	for _, g := range u.Groups {
		if g == p.cfg.AdminGroup {
			u.Admin = true
			return
		}
	}
}

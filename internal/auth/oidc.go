package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/coreos/go-oidc/v3/oidc"
)

// OIDCRealm validates OpenID Connect access tokens (JWT bearer) presented by
// clients. It verifies signature, expiry and audience using the IdP metadata.
type OIDCRealm struct {
	cfg      *OIDCConfig
	verifier *oidc.IDTokenVerifier
}

func NewOIDCRealm(ctx context.Context, cfg *OIDCConfig) (*OIDCRealm, error) {
	if cfg == nil || cfg.Issuer == "" {
		return nil, errors.New("auth: oidc requires issuer")
	}
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, err
	}
	aud := cfg.Audience
	if aud == "" {
		aud = cfg.ClientID
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: aud})
	return &OIDCRealm{cfg: cfg, verifier: verifier}, nil
}

func (r *OIDCRealm) Name() string { return "oidc" }

func (r *OIDCRealm) AuthenticatePassword(ctx context.Context, username, password string) (*User, error) {
	return nil, ErrNoMatch
}

func (r *OIDCRealm) AuthenticateToken(ctx context.Context, token string) (*User, error) {
	idt, err := r.verifier.Verify(ctx, token)
	if err != nil {
		return nil, ErrNoMatch
	}
	var raw map[string]any
	if err := idt.Claims(&raw); err != nil {
		return nil, ErrNoMatch
	}
	usernameClaim := r.cfg.UsernameClaim
	if usernameClaim == "" {
		usernameClaim = "sub"
	}
	name, _ := raw[usernameClaim].(string)
	if name == "" {
		name = idt.Subject
	}
	groupsClaim := r.cfg.GroupsClaim
	if groupsClaim == "" {
		groupsClaim = "groups"
	}
	groups := extractGroups(raw[groupsClaim])
	u := &User{Name: name, Groups: groups, Source: "oidc"}
	if r.cfg.AdminGroup != "" {
		for _, g := range groups {
			if g == r.cfg.AdminGroup {
				u.Admin = true
				break
			}
		}
	}
	return u, nil
}

func extractGroups(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// OAuthRealm validates OAuth2 access tokens via the provider's userinfo endpoint.
type OAuthRealm struct {
	cfg *OAuthConfig
}

func NewOAuthRealm(cfg *OAuthConfig) *OAuthRealm { return &OAuthRealm{cfg: cfg} }

func (r *OAuthRealm) Name() string { return "oauth" }

func (r *OAuthRealm) AuthenticatePassword(ctx context.Context, username, password string) (*User, error) {
	return nil, ErrNoMatch
}

func (r *OAuthRealm) AuthenticateToken(ctx context.Context, token string) (*User, error) {
	if r.cfg == nil || r.cfg.UserInfoURL == "" {
		return nil, ErrNoMatch
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.cfg.UserInfoURL, nil)
	if err != nil {
		return nil, ErrNoMatch
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return nil, ErrNoMatch
	}
	defer resp.Body.Close()

	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, ErrNoMatch
	}
	field := r.cfg.MapUsername
	if field == "" {
		field = "login"
	}
	name, _ := data[field].(string)
	if name == "" {
		return nil, ErrNoMatch
	}
	groups := extractGroups(data[r.cfg.MapGroups])
	return &User{Name: name, Groups: groups, Source: "oauth"}, nil
}

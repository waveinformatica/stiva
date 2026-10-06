package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

var ssoTestSecret = []byte("test-secret-0123456789abcdef")

func TestSSOStateRoundTrip(t *testing.T) {
	st, err := ssoState(ssoTestSecret, "gh", "verifier-123")
	if err != nil {
		t.Fatal(err)
	}
	pid, v, err := parseSSOState(ssoTestSecret, st)
	if err != nil || pid != "gh" || v != "verifier-123" {
		t.Fatalf("round trip = %q %q %v", pid, v, err)
	}
	if _, _, err := parseSSOState(ssoTestSecret, st+"tampered"); err == nil {
		t.Fatal("tampered state must fail")
	}
	if _, _, err := parseSSOState([]byte("other-secret"), st); err == nil {
		t.Fatal("wrong secret must fail")
	}
	// A session token is not a state token.
	sess, err := issueToken(ssoTestSecret, &User{Name: "x"}, time.Hour, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := parseSSOState(ssoTestSecret, sess); err == nil {
		t.Fatal("session token must not pass as state")
	}
	// Expired state fails closed.
	past := map[string]any{"typ": "sso-state", "pid": "gh", "pkc": "v", "exp": time.Now().Add(-time.Hour).Unix(), "iat": time.Now().Unix()}
	hb, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	pb, _ := json.Marshal(past)
	in := b64(hb) + "." + b64(pb)
	expired := in + "." + hmacSum(ssoTestSecret, in)
	if _, _, err := parseSSOState(ssoTestSecret, expired); err == nil {
		t.Fatal("expired state must fail")
	}
}

func TestPKCEVector(t *testing.T) {
	// RFC 7636 appendix B.
	if got := pkceChallenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("challenge = %q", got)
	}
	if v, err := pkceVerifier(); err != nil || len(v) < 43 {
		t.Fatalf("verifier = %q %v", v, err)
	}
}

// ssoTestManager builds a Manager holding exactly the given providers.
func ssoTestManager(t *testing.T, cfgs ...*SSOConfig) *Manager {
	t.Helper()
	m := &Manager{secret: ssoTestSecret, ttl: time.Hour}
	for _, c := range cfgs {
		p, err := NewSSOProvider(t.Context(), c)
		if err != nil {
			t.Fatal(err)
		}
		m.sso = append(m.sso, p)
		m.realms = append(m.realms, p)
	}
	return m
}

func ssoTestContext(method, target, host, id string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	c.Request.Host = host
	c.Params = gin.Params{{Key: "id", Value: id}}
	return c, w
}

func TestSSOLoginRedirect(t *testing.T) {
	m := ssoTestManager(t, &SSOConfig{
		ID: "gh", Provider: "oauth2",
		Authorize: "https://idp.example.com/authorize", Token: "https://idp.example.com/token",
		UserInfo: "https://idp.example.com/me", ClientID: "cid",
	})
	c, w := ssoTestContext("GET", "/auth/sso/gh/login", "registry.example.com", "gh")
	m.SSOLoginHandler(c)
	if w.Code != http.StatusFound {
		t.Fatalf("code = %d", w.Code)
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Scheme+"://"+loc.Host != "https://idp.example.com" || loc.Path != "/authorize" {
		t.Fatalf("location = %q", w.Header().Get("Location"))
	}
	q := loc.Query()
	if q.Get("client_id") != "cid" || q.Get("response_type") != "code" ||
		q.Get("redirect_uri") != "http://registry.example.com/auth/sso/gh/callback" ||
		q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" {
		t.Fatalf("authorize params = %v", q)
	}
	if _, _, err := parseSSOState(ssoTestSecret, q.Get("state")); err != nil {
		t.Fatalf("state in redirect is not ours: %v", err)
	}

	cas := ssoTestManager(t, &SSOConfig{ID: "cas", Provider: "cas", BaseURL: "https://cas.example.com"})
	c, w = ssoTestContext("GET", "/auth/sso/cas/login", "registry.example.com", "cas")
	cas.SSOLoginHandler(c)
	loc, _ = url.Parse(w.Header().Get("Location"))
	if loc.Scheme+"://"+loc.Host+loc.Path != "https://cas.example.com/login" ||
		loc.Query().Get("service") != "http://registry.example.com/auth/sso/cas/callback" {
		t.Fatalf("cas login = %q", w.Header().Get("Location"))
	}

	c, w = ssoTestContext("GET", "/auth/sso/nope/login", "registry.example.com", "nope")
	m.SSOLoginHandler(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown provider code = %d", w.Code)
	}
}

// TestSSOCallbackOAuth2 drives the whole code flow against a fake IdP.
func TestSSOCallbackOAuth2(t *testing.T) {
	var tokenHits int
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		tokenHits++
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.PostForm.Get("grant_type") != "authorization_code" ||
			r.PostForm.Get("code") != "authcode-1" ||
			r.PostForm.Get("code_verifier") != "verifier-1" ||
			r.PostForm.Get("redirect_uri") != "http://registry.example.com/auth/sso/o2/callback" {
			t.Errorf("token form = %v", r.PostForm)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"tok-1","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"login":"octocat","teams":["devs","admins"]}`))
	})
	idp := httptest.NewServer(mux)
	defer idp.Close()

	m := ssoTestManager(t, &SSOConfig{
		ID: "o2", Provider: "oauth2",
		Authorize: idp.URL + "/authorize", Token: idp.URL + "/token", UserInfo: idp.URL + "/me",
		ClientID: "cid", Secret: "sec", Username: "login", Groups: "teams", AdminGroup: "admins",
	})
	st, err := ssoState(ssoTestSecret, "o2", "verifier-1")
	if err != nil {
		t.Fatal(err)
	}
	c, w := ssoTestContext("GET", "/auth/sso/o2/callback?code=authcode-1&state="+url.QueryEscape(st), "registry.example.com", "o2")
	m.SSOCallbackHandler(c)
	if w.Code != http.StatusFound {
		t.Fatalf("code = %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/#sso_token=") {
		t.Fatalf("location = %q", loc)
	}
	claims, err := verifyToken(ssoTestSecret, strings.TrimPrefix(loc, "/#sso_token="))
	if err != nil {
		t.Fatalf("session token invalid: %v", err)
	}
	if claims["sub"] != "octocat" || claims["adm"] != true {
		t.Fatalf("claims = %v", claims)
	}
	got := map[string]bool{}
	for _, g := range claims["groups"].([]any) {
		got[g.(string)] = true
	}
	if !got["devs"] || !got["admins"] {
		t.Fatalf("groups = %v", claims["groups"])
	}
	if tokenHits != 1 {
		t.Fatalf("token endpoint hits = %d", tokenHits)
	}

	// Tampered state must not issue anything.
	c, w = ssoTestContext("GET", "/auth/sso/o2/callback?code=authcode-1&state=tampered", "registry.example.com", "o2")
	m.SSOCallbackHandler(c)
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/#sso_error=") {
		t.Fatalf("tampered state location = %q", loc)
	}
}

// TestSSOCallbackGitHubOrgs covers the orgs-as-groups quirk of the preset.
func TestSSOCallbackGitHubOrgs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"tok-2","token_type":"bearer"}`))
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"login":"octocat"}`))
	})
	mux.HandleFunc("/user/orgs", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-2" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"login":"acme"},{"login":"labs"}]`))
	})
	idp := httptest.NewServer(mux)
	defer idp.Close()

	m := ssoTestManager(t, &SSOConfig{
		ID: "gh", Provider: "github", ClientID: "cid", Secret: "sec",
		Authorize: idp.URL + "/authorize", Token: idp.URL + "/token", UserInfo: idp.URL + "/user",
	})
	st, _ := ssoState(ssoTestSecret, "gh", "v")
	c, w := ssoTestContext("GET", "/auth/sso/gh/callback?code=c&state="+url.QueryEscape(st), "registry.example.com", "gh")
	m.SSOCallbackHandler(c)
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/#sso_token=") {
		t.Fatalf("location = %q", loc)
	}
	claims, err := verifyToken(ssoTestSecret, strings.TrimPrefix(loc, "/#sso_token="))
	if err != nil {
		t.Fatal(err)
	}
	if claims["sub"] != "octocat" {
		t.Fatalf("claims = %v", claims)
	}
	got := []string{}
	for _, g := range claims["groups"].([]any) {
		got = append(got, g.(string))
	}
	if len(got) != 2 || got[0] != "acme" || got[1] != "labs" {
		t.Fatalf("groups = %v", got)
	}
}

// TestSSOCallbackCAS drives ticket validation against a fake CAS server.
func TestSSOCallbackCAS(t *testing.T) {
	cas := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Query().Get("ticket"), "ST-") ||
			r.URL.Query().Get("service") != "http://registry.example.com/auth/sso/cas/callback" {
			t.Errorf("validate query = %v", r.URL.Query())
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(casSuccessDoc))
	}))
	defer cas.Close()

	m := ssoTestManager(t, &SSOConfig{
		ID: "cas", Provider: "cas", BaseURL: cas.URL, Groups: "memberOf", AdminGroup: "admins",
	})
	c, w := ssoTestContext("GET", "/auth/sso/cas/callback?ticket=ST-1", "registry.example.com", "cas")
	m.SSOCallbackHandler(c)
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/#sso_token=") {
		t.Fatalf("location = %q", loc)
	}
	claims, err := verifyToken(ssoTestSecret, strings.TrimPrefix(loc, "/#sso_token="))
	if err != nil {
		t.Fatal(err)
	}
	if claims["sub"] != "alice" || claims["adm"] != true {
		t.Fatalf("claims = %v", claims)
	}
}

func TestMapOIDCUser(t *testing.T) {
	p := &ssoProvider{cfg: &resolvedProvider{SSOConfig: SSOConfig{
		ID: "e", Username: "preferred_username", Groups: "groups", AdminGroup: "registry-admins",
	}}}
	u := p.mapOIDCUser(map[string]any{
		"sub":                "123",
		"preferred_username": "jdoe@example.com",
		"groups":             []any{"devs", "registry-admins"},
	}, "123")
	if u.Name != "jdoe@example.com" || !u.Admin || len(u.Groups) != 2 || u.Source != "sso:e" {
		t.Fatalf("user = %+v", u)
	}
	u = p.mapOIDCUser(map[string]any{"sub": "456"}, "456")
	if u.Name != "456" || u.Admin || len(u.Groups) != 0 {
		t.Fatalf("fallback user = %+v", u)
	}
}

func TestSSOListHandler(t *testing.T) {
	// OIDC presets need live discovery, so the list test sticks to offline kinds.
	m := ssoTestManager(t,
		&SSOConfig{ID: "gh", Provider: "github", ClientID: "c", Secret: "s"},
		&SSOConfig{ID: "cas", Provider: "cas", BaseURL: "https://cas.example.com"},
	)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/auth/sso", nil)
	m.SSOListHandler(c)
	var out struct {
		Providers []SSOPublic `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Providers) != 2 || out.Providers[0].Label != "GitHub" || out.Providers[1].Kind != SSOKindCAS {
		t.Fatalf("providers = %+v", out.Providers)
	}
}

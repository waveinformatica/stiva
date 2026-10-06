package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"registry/internal/storage"
	"registry/internal/vault"
)

// Realm authenticates credentials of one kind.
type Realm interface {
	Name() string
	AuthenticatePassword(ctx context.Context, username, password string) (*User, error)
	AuthenticateToken(ctx context.Context, token string) (*User, error)
}

// Manager orchestrates realms and issues/verifies session tokens.
type Manager struct {
	realms []Realm
	secret []byte
	// trustedProxies is how many X-Forwarded-For hops were appended by
	// infrastructure we control. Zero means the header is ignored, which is the
	// safe default: it cannot then be used to claim an address.
	trustedProxies int
	ttl            time.Duration
	// allowAnonymous gates whether unauthenticated requests are mapped to the
	// "anonymous" subject. It is a persisted global setting (default: false),
	// loaded from the database and mutable from the admin UI.
	anonMu         sync.RWMutex
	allowAnonymous bool
	local          *LocalRealm
	// sso holds the UI-managed login providers. Unlike realms it changes at
	// runtime (admin CRUD reloads it), so readers take ssoMu.
	ssoMu    sync.RWMutex
	sso      []*ssoProvider
	ssoMeta  storage.MetadataStore
	ssoVault *vault.Vault
}

// NewManager builds an authentication manager from configuration. Authentication
// is always active so the registry stays administrable; only anonymous access is
// optional and is controlled by a persisted global setting. Realms are tried in
// order.
func NewManager(cfg *AuthConfig, defaultDSN string) *Manager {
	m := &Manager{ttl: 24 * time.Hour}
	if cfg == nil {
		cfg = &AuthConfig{Realms: []string{"local"}, Local: &LocalConfig{}}
	}

	m.trustedProxies = cfg.TrustedProxies
	if cfg.TokenTTL != "" {
		if d, err := time.ParseDuration(cfg.TokenTTL); err == nil {
			m.ttl = d
		}
	}
	if cfg.TokenSecret != "" {
		m.secret = []byte(cfg.TokenSecret)
	} else {
		// No configured secret: generate an ephemeral one. This is only viable
		// for a single instance — every restart invalidates all sessions, and
		// with more than one replica a token issued by one is rejected by the
		// others, producing intermittent auth failures that are very hard to
		// diagnose. Warn loudly rather than fail, so single-node and dev runs
		// still work out of the box.
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			// Without a secret every token would be forgeable; refuse to run.
			log.Fatalf("auth: cannot generate a token secret: %v", err)
		}
		m.secret = b
		log.Printf("WARNING: no token secret configured (REGISTRY_AUTH_SECRET is empty); " +
			"generated an ephemeral one. Sessions will not survive a restart and " +
			"MUST NOT be used with more than one replica.")
	}

	ctx := context.Background()
	for _, name := range cfg.Realms {
		switch strings.ToLower(name) {
		case "local":
			dsn := defaultDSN
			if cfg.Local != nil && cfg.Local.PostgresDSN != "" {
				dsn = cfg.Local.PostgresDSN
			}
			if dsn == "" {
				log.Printf("auth: local realm requested but no postgres DSN configured; skipping")
				continue
			}
			lr, err := NewLocalRealm(dsn)
			if err != nil {
				log.Printf("auth: local realm init failed: %v", err)
				continue
			}
			m.local = lr
			m.realms = append(m.realms, lr)
		case "ldap":
			if cfg.LDAP != nil {
				m.realms = append(m.realms, NewLDAPRealm(cfg.LDAP))
			}
		case "oidc":
			if cfg.OIDC != nil {
				or, err := NewOIDCRealm(ctx, cfg.OIDC)
				if err != nil {
					log.Printf("auth: oidc realm init failed (IdP unreachable?): %v", err)
					continue
				}
				m.realms = append(m.realms, or)
			}
		case "oauth":
			if cfg.OAuth != nil {
				m.realms = append(m.realms, NewOAuthRealm(cfg.OAuth))
			}
		}
	}

	// Anonymous access is OFF by default; the admin UI turns it on. When on,
	// unauthenticated requests become the "anonymous" subject and per-registry
	// permissions decide what they may do.
	if m.local != nil {
		if v, err := m.local.GetSetting(ctx, "allow_anonymous"); err == nil {
			m.allowAnonymous = v == "true"
		}
	}
	return m
}

// AllowAnonymous reports whether unauthenticated requests are mapped to the
// "anonymous" subject.
func (m *Manager) AllowAnonymous() bool {
	m.anonMu.RLock()
	defer m.anonMu.RUnlock()
	return m.allowAnonymous
}

// SetAllowAnonymous persists and applies the anonymous-access setting.
func (m *Manager) SetAllowAnonymous(ctx context.Context, v bool) error {
	if m.local != nil {
		if err := m.local.SetSetting(ctx, "allow_anonymous", boolToString(v)); err != nil {
			return err
		}
	}
	m.anonMu.Lock()
	m.allowAnonymous = v
	m.anonMu.Unlock()
	return nil
}

func boolToString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// Local returns the local realm (may be nil if local auth is not configured).
func (m *Manager) Local() *LocalRealm { return m.local }

// BootstrapAdmin creates the initial admin account if using the local realm.
func (m *Manager) BootstrapAdmin(name, password string) error {
	if m.local == nil || name == "" {
		return nil
	}
	return m.local.BootstrapAdmin(name, password)
}

// AuthenticateRequest resolves the principal from an HTTP request.
func (m *Manager) AuthenticateRequest(c *gin.Context) (*User, error) {
	auth := c.GetHeader("Authorization")
	if auth == "" {
		if !m.AllowAnonymous() {
			return nil, ErrUnauthorized
		}
		// Which anonymous identity, if any, recognises this caller. The named
		// identities carry their own grants, so "pull without credentials from
		// inside the cluster" is expressed by one of them rather than by a
		// global switch that is open to everybody.
		if m.local != nil {
			ip := ClientIP(c.Request, m.trustedProxies)
			if u := m.local.ResolveAnonymous(c.Request.Context(), ip); u != nil {
				return u, nil
			}
			return nil, ErrUnauthorized
		}
		return &User{Name: "anonymous", Anonymous: true, Source: "anonymous"}, nil
	}
	switch {
	case strings.HasPrefix(auth, "Basic "):
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(auth, "Basic "))
		if err != nil {
			return nil, ErrUnauthorized
		}
		parts := strings.SplitN(string(raw), ":", 2)
		if len(parts) != 2 {
			return nil, ErrUnauthorized
		}
		return m.tryPassword(c.Request.Context(), parts[0], parts[1])
	case strings.HasPrefix(auth, "Bearer "):
		token := strings.TrimPrefix(auth, "Bearer ")
		// Our own session tokens first.
		if claims, err := verifyToken(m.secret, token); err == nil {
			if sub, ok := claims["sub"].(string); ok && sub != "" {
				// Sessions minted from an API key stay bound to it: the key
				// is re-resolved on every request, so revocation and grant
				// changes apply immediately instead of lingering until expiry.
				if kh, ok := claims["key"].(string); ok && kh != "" {
					if m.local == nil {
						return nil, ErrUnauthorized
					}
					return m.local.authenticateKeyHash(c.Request.Context(), "", kh, false)
				}
				u := &User{Name: sub, Source: "session"}
				if adm, ok := claims["adm"].(bool); ok {
					u.Admin = adm
				}
				if pcr, ok := claims["pcr"].(bool); ok {
					u.PasswordChangeRequired = pcr
				}
				if g, ok := claims["groups"].([]any); ok {
					for _, x := range g {
						if s, ok := x.(string); ok {
							u.Groups = append(u.Groups, s)
						}
					}
				}
				return u, nil
			}
		}
		// Then external realms (API keys, OIDC access tokens, OAuth tokens).
		return m.tryToken(c.Request.Context(), token)
	default:
		return nil, ErrUnauthorized
	}
}

func (m *Manager) tryPassword(ctx context.Context, user, pass string) (*User, error) {
	for _, r := range m.realms {
		u, err := r.AuthenticatePassword(ctx, user, pass)
		if err == nil && u != nil {
			return u, nil
		}
	}
	return nil, ErrUnauthorized
}

func (m *Manager) tryToken(ctx context.Context, token string) (*User, error) {
	for _, r := range m.realms {
		u, err := r.AuthenticateToken(ctx, token)
		if err == nil && u != nil {
			return u, nil
		}
	}
	// UI-managed SSO providers validate IdP-issued tokens too (GitLab CI JWTs,
	// GitHub tokens), so API and docker clients work without a browser.
	m.ssoMu.RLock()
	providers := m.sso
	m.ssoMu.RUnlock()
	for _, sp := range providers {
		u, err := sp.AuthenticateToken(ctx, token)
		if err == nil && u != nil {
			return u, nil
		}
	}
	return nil, ErrUnauthorized
}

// realmURL returns the token endpoint for the WWW-Authenticate challenge.
func (m *Manager) realmURL(c *gin.Context) string {
	return RequestScheme(c) + "://" + c.Request.Host + "/auth/token"
}

// Challenge builds the WWW-Authenticate header value.
func (m *Manager) Challenge(c *gin.Context) string {
	return `Bearer realm="` + m.realmURL(c) + `",service="registry"`
}

// Middleware enforces authentication on protected routes.
func (m *Manager) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, err := m.AuthenticateRequest(c)
		if err != nil {
			c.Header("WWW-Authenticate", m.Challenge(c))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"errors": []gin.H{{"code": "UNAUTHORIZED", "message": "authentication required"}},
			})
			return
		}
		c.Set("user", user)
		c.Next()
	}
}

// TrustedProxies reports how many X-Forwarded-For hops are trusted. The admin
// UI shows it, because an address filter means nothing when it is zero and the
// registry sits behind a proxy.
func (m *Manager) TrustedProxies() int { return m.trustedProxies }

// TokenHandler implements the OCI/Docker token endpoint (/auth/token).
func (m *Manager) TokenHandler(c *gin.Context) {
	var username, password string
	if ah := c.GetHeader("Authorization"); strings.HasPrefix(ah, "Basic ") {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ah, "Basic "))
		if err == nil {
			parts := strings.SplitN(string(raw), ":", 2)
			if len(parts) == 2 {
				username, password = parts[0], parts[1]
			}
		}
	} else {
		username = c.PostForm("username")
		password = c.PostForm("password")
	}

	user, err := m.tryPassword(c.Request.Context(), username, password)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	// Sessions minted from an API key stay bound to it (see AuthenticateRequest).
	keyHash := ""
	if user.Source == "token" {
		keyHash = hashAPIKey(password)
	}
	tok, err := issueToken(m.secret, user, m.ttl, keyHash)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "token issuance failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token":        tok,
		"access_token": tok,
		"expires_in":   int(m.ttl.Seconds()),
	})
}

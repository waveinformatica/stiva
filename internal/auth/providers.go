package auth

import (
	"fmt"
	"strings"
)

// SSO provider kinds: how the browser login flow and token validation work.
const (
	SSOKindOIDC   = "oidc"
	SSOKindOAuth2 = "oauth2"
	SSOKindCAS    = "cas"
)

// SSOConfig declares one single-sign-on provider. The Provider selects a
// known preset (microsoft, google, github, gitlab, linkedin) or a generic
// mechanism (oidc, oauth2, cas). Presets fill every endpoint and mapping, so
// only the credentials the provider actually needs stay mandatory:
//
//	microsoft: client_id + client_secret + tenant
//	google:    client_id (+ client_secret for the code flow)
//	github:    client_id + client_secret
//	gitlab:    client_id (+ client_secret), base_url for self-hosted instances
//	linkedin:  client_id + client_secret
//
// Every other field overrides the preset default.
type SSOConfig struct {
	ID         string `json:"id"`       // unique handle, used in /auth/sso/:id/...
	Label      string `json:"label"`    // button text; defaults to the provider's display name
	Provider   string `json:"provider"` // microsoft | google | github | gitlab | linkedin | oidc | oauth2 | cas
	Enabled    bool   `json:"enabled"`  // disabled entries stay stored but never serve logins
	ClientID   string `json:"client_id"`
	Secret     string `json:"client_secret"` // write-only: accepted on create/update, never returned
	Tenant     string `json:"tenant"`        // microsoft: directory tenant ("common" allows any Microsoft account)
	BaseURL    string `json:"base_url"`      // gitlab: self-hosted base (default https://gitlab.com); cas: server base
	Issuer     string `json:"issuer"`        // generic oidc: issuer URL (overrides preset)
	Authorize  string `json:"authorize_url"`
	Token      string `json:"token_url"`
	UserInfo   string `json:"userinfo_url"`
	Scope      string `json:"scope"`          // space-separated; defaults to the preset scopes
	Username   string `json:"username_claim"` // claim/field used as username (preset default, overridable)
	Groups     string `json:"groups_claim"`   // claim/field carrying group membership (empty = none)
	Audience   string `json:"audience"`       // OIDC audience check; defaults to ClientID
	AdminGroup string `json:"admin_group"`    // group whose members become admins
}

// resolvedProvider is an SSOConfig with every preset default applied.
type resolvedProvider struct {
	SSOConfig
	Kind        string
	DisplayName string
}

// providerPreset holds the baked-in knowledge for one known provider.
type providerPreset struct {
	kind        string
	displayName string
	issuer      func(c *SSOConfig) string
	authorize   func(c *SSOConfig) string
	token       func(c *SSOConfig) string
	userinfo    func(c *SSOConfig) string
	scopes      string
	username    string
	groups      string
	require     []string // config keys that must be set (client_id, secret, tenant)
	fields      []string // editable keys in display order, driving the admin form
}

// SSOPresetInfo describes one preset to the admin UI so the form only asks
// for the fields that preset actually uses.
type SSOPresetInfo struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"`
	Required []string `json:"required"`
	Fields   []string `json:"fields"`
}

// SSOPresets lists every known preset in a stable order.
func SSOPresets() []SSOPresetInfo {
	keys := []string{"microsoft", "google", "github", "gitlab", "linkedin", "oidc", "oauth2", "cas"}
	out := make([]SSOPresetInfo, 0, len(keys))
	for _, k := range keys {
		p := providerPresets[k]
		out = append(out, SSOPresetInfo{Key: k, Label: p.displayName, Kind: p.kind, Required: p.require, Fields: p.fields})
	}
	return out
}

func trimURL(s string) string { return strings.TrimRight(strings.TrimSpace(s), "/") }

var providerPresets = map[string]providerPreset{
	"microsoft": {
		kind:        SSOKindOIDC,
		displayName: "Microsoft 365",
		issuer:      func(c *SSOConfig) string { return "https://login.microsoftonline.com/" + c.Tenant + "/v2.0" },
		authorize: func(c *SSOConfig) string {
			return "https://login.microsoftonline.com/" + c.Tenant + "/oauth2/v2.0/authorize"
		},
		token: func(c *SSOConfig) string {
			return "https://login.microsoftonline.com/" + c.Tenant + "/oauth2/v2.0/token"
		},
		scopes:   "openid profile email",
		username: "preferred_username",
		groups:   "groups",
		require:  []string{"client_id", "secret", "tenant"},
		fields:   []string{"client_id", "secret", "tenant", "scope", "username_claim", "groups_claim", "audience", "admin_group"},
	},
	"google": {
		kind:        SSOKindOIDC,
		displayName: "Google",
		issuer:      func(c *SSOConfig) string { return "https://accounts.google.com" },
		scopes:      "openid email profile",
		username:    "email",
		groups:      "groups",
		require:     []string{"client_id"},
		fields:      []string{"client_id", "secret", "scope", "username_claim", "admin_group", "audience"},
	},
	"github": {
		kind:        SSOKindOAuth2,
		displayName: "GitHub",
		authorize:   func(c *SSOConfig) string { return "https://github.com/login/oauth/authorize" },
		token:       func(c *SSOConfig) string { return "https://github.com/login/oauth/access_token" },
		userinfo:    func(c *SSOConfig) string { return "https://api.github.com/user" },
		scopes:      "read:user read:org",
		username:    "login",
		groups:      "orgs",
		require:     []string{"client_id", "secret"},
		fields:      []string{"client_id", "secret", "scope", "username_claim", "groups_claim", "admin_group"},
	},
	"gitlab": {
		kind:        SSOKindOIDC,
		displayName: "GitLab",
		issuer:      func(c *SSOConfig) string { return gitlabBase(c) },
		scopes:      "openid email profile",
		username:    "preferred_username",
		groups:      "groups",
		require:     []string{"client_id"},
		fields:      []string{"client_id", "secret", "base_url", "scope", "username_claim", "groups_claim", "audience", "admin_group"},
	},
	"linkedin": {
		kind:        SSOKindOAuth2,
		displayName: "LinkedIn",
		authorize:   func(c *SSOConfig) string { return "https://www.linkedin.com/oauth/v2/authorization" },
		token:       func(c *SSOConfig) string { return "https://www.linkedin.com/oauth/v2/accessToken" },
		userinfo:    func(c *SSOConfig) string { return "https://api.linkedin.com/v2/userinfo" },
		scopes:      "openid profile email",
		username:    "email",
		groups:      "groups",
		require:     []string{"client_id", "secret"},
		fields:      []string{"client_id", "secret", "scope", "username_claim", "admin_group"},
	},
	"oidc": {
		kind:        SSOKindOIDC,
		displayName: "OpenID Connect",
		scopes:      "openid email profile",
		username:    "preferred_username",
		groups:      "groups",
		require:     []string{"issuer", "client_id"},
		fields:      []string{"issuer", "client_id", "secret", "scope", "username_claim", "groups_claim", "audience", "admin_group"},
	},
	"oauth2": {
		kind:        SSOKindOAuth2,
		displayName: "OAuth2",
		username:    "login",
		groups:      "groups",
		require:     []string{"authorize", "token", "userinfo"},
		fields:      []string{"authorize_url", "token_url", "userinfo_url", "client_id", "secret", "scope", "username_claim", "groups_claim", "admin_group"},
	},
	"cas": {
		kind:        SSOKindCAS,
		displayName: "CAS",
		groups:      "memberOf",
		require:     []string{"base_url"},
		fields:      []string{"base_url", "username_claim", "groups_claim", "admin_group"},
	},
}

func gitlabBase(c *SSOConfig) string {
	if c.BaseURL != "" {
		return trimURL(c.BaseURL)
	}
	return "https://gitlab.com"
}

// resolveProvider applies preset defaults under explicit settings and checks
// the required fields. Unknown providers and missing credentials are errors,
// so a typo fails loudly at startup instead of serving a dead login button.
func resolveProvider(c SSOConfig) (*resolvedProvider, error) {
	key := strings.ToLower(strings.TrimSpace(c.Provider))
	p, ok := providerPresets[key]
	if !ok {
		return nil, fmt.Errorf("auth: sso %q: unknown provider %q (microsoft, google, github, gitlab, linkedin, oidc, oauth2, cas)", c.ID, c.Provider)
	}
	if c.ID == "" {
		return nil, fmt.Errorf("auth: sso provider %q needs an id", c.Provider)
	}
	missing := func() string {
		for _, k := range p.require {
			var v string
			switch k {
			case "client_id":
				v = c.ClientID
			case "secret":
				v = c.Secret
			case "tenant":
				v = c.Tenant
			case "issuer":
				v = c.Issuer
			case "authorize":
				v = c.Authorize
			case "token":
				v = c.Token
			case "userinfo":
				v = c.UserInfo
			case "base_url":
				v = c.BaseURL
			}
			if strings.TrimSpace(v) == "" {
				return k
			}
		}
		return ""
	}()
	if missing != "" {
		return nil, fmt.Errorf("auth: sso %q (%s) requires %s", c.ID, key, missing)
	}
	r := &resolvedProvider{SSOConfig: c, Kind: p.kind, DisplayName: p.displayName}
	if r.Label == "" {
		r.Label = p.displayName
	}
	if r.Scope == "" {
		r.Scope = p.scopes
	}
	if r.Username == "" {
		r.Username = p.username
	}
	if c.Groups != "" {
		r.Groups = c.Groups
	} else {
		r.Groups = p.groups
	}
	if p.issuer != nil && r.Issuer == "" {
		r.Issuer = p.issuer(&c)
	}
	if p.authorize != nil && r.Authorize == "" {
		r.Authorize = p.authorize(&c)
	}
	if p.token != nil && r.Token == "" {
		r.Token = p.token(&c)
	}
	if p.userinfo != nil && r.UserInfo == "" {
		r.UserInfo = p.userinfo(&c)
	}
	if r.Kind == SSOKindCAS {
		r.BaseURL = trimURL(c.BaseURL)
	}
	return r, nil
}

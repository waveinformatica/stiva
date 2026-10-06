package auth

// AuthConfig configures the authentication subsystem. Authentication is always
// active (the registry cannot be administered otherwise); only anonymous access
// is optional and is controlled by a persisted global setting (default: off),
// not by this struct. Realms are tried in order.
type AuthConfig struct {
	// TokenSecret signs/verifies issued session tokens. Auto-generated if empty.
	TokenSecret string `json:"-"`
	// TokenTTL is the session token lifetime.
	TokenTTL string `json:"token_ttl"`
	// TrustedProxies is how many X-Forwarded-For entries were appended by
	// infrastructure under our control. Address-based anonymous identities are
	// only as trustworthy as this number: set it wrong and a caller can claim
	// any address by writing the header themselves. Zero ignores the header.
	TrustedProxies int `json:"trusted_proxies"`
	// Realms lists the enabled authentication methods, in priority order.
	// Values: "local", "ldap", "oidc", "oauth".
	Realms []string `json:"realms"`

	Local *LocalConfig `json:"local"`
	LDAP  *LDAPConfig  `json:"ldap"`
	OIDC  *OIDCConfig  `json:"oidc"`
	OAuth *OAuthConfig `json:"oauth"`
}

// LocalConfig configures local username/password + API key authentication.
type LocalConfig struct {
	// PostgresDSN stores users and API keys. Defaults to the registry DSN.
	PostgresDSN string `json:"postgres_dsn"`
}

// LDAPConfig configures LDAP / Active Directory authentication.
type LDAPConfig struct {
	URL                string `json:"url"`
	BindDN             string `json:"bind_dn"`       // service account for search (optional)
	BindPassword       string `json:"bind_password"` // service account password (optional)
	UserBaseDN         string `json:"user_base_dn"`
	UserFilter         string `json:"user_filter"`    // e.g. "(uid=%s)" or "(sAMAccountName=%s)"
	UserNameAttr       string `json:"user_name_attr"` // attribute returned as username (default "cn")
	StartTLS           bool   `json:"start_tls"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify"`
	GroupFilter        string `json:"group_filter"` // optional, for group membership
	AdminGroup         string `json:"admin_group"`  // group whose members become admins
}

// OIDCConfig configures OpenID Connect token (bearer JWT) validation.
type OIDCConfig struct {
	Issuer        string `json:"issuer"`
	ClientID      string `json:"client_id"`
	Audience      string `json:"audience"` // expected aud; if empty, ClientID is used
	JWKSURL       string `json:"jwks_url"`
	GroupsClaim   string `json:"groups_claim"`   // claim carrying group membership
	UsernameClaim string `json:"username_claim"` // default "sub"
	AdminGroup    string `json:"admin_group"`    // group whose members become admins
}

// OAuthConfig configures generic OAuth2 (GitHub / Google / custom) userinfo validation.
type OAuthConfig struct {
	Provider    string `json:"provider"` // "github" | "google" | "generic"
	UserInfoURL string `json:"userinfo_url"`
	// MapUsername is a JSON field in the userinfo response used as the username.
	MapUsername string `json:"map_username"` // e.g. "login" for GitHub, "email" for Google
	// MapGroups is an optional JSON field carrying group membership.
	MapGroups string `json:"map_groups"`
}

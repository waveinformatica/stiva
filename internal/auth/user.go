package auth

import "registry/internal/authz"

// User represents an authenticated principal.
type User struct {
	Name      string   `json:"name"`
	Groups    []string `json:"groups,omitempty"`
	Admin     bool     `json:"admin,omitempty"`
	Anonymous bool     `json:"-"`
	// PasswordChangeRequired is set when the account must change its password
	// before being allowed to proceed (e.g. the seeded first-login admin).
	PasswordChangeRequired bool `json:"password_change_required,omitempty"`
	// Source records which realm authenticated the user (local, ldap, oidc, oauth, token).
	Source string `json:"-"`
	// KeyLabel names the API key the session runs on, if any. The registry
	// recognizes the key as its owner for identity and audit; authorization
	// additionally passes the key's own grants (see KeyGrants).
	KeyLabel string `json:"key_label,omitempty"`
	// KeyGrants restricts an API-key session to the listed (role, scope)
	// pairs, evaluated as the owner on top of the owner's own grants. Empty
	// means full owner power. Only key authentication sets this.
	KeyGrants []authz.KeyGrant `json:"-"`
}

// IsAnonymous reports whether the user is the anonymous guest.
func (u *User) IsAnonymous() bool { return u.Anonymous }

// IsAdmin reports whether the user has administrator privileges.
func (u *User) IsAdmin() bool { return u.Admin }

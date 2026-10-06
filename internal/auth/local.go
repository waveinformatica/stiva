package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"registry/internal/authz"
	"registry/internal/digest"
)

// UserView is the admin-facing representation of a user.
type UserView struct {
	Name                   string `json:"name"`
	Admin                  bool   `json:"admin"`
	Disabled               bool   `json:"disabled"`
	PasswordChangeRequired bool   `json:"password_change_required"`
}

// APIKeyView is the admin-facing representation of a service account (API key).
type APIKeyView struct {
	Key      string           `json:"key,omitempty"` // only present on creation
	Masked   string           `json:"masked"`
	Username string           `json:"username"`
	Label    string           `json:"label"`
	Grants   []authz.KeyGrant `json:"grants,omitempty"`
}

// RoleView is the admin-facing representation of an RBAC role.
type RoleView struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

// LocalRealm authenticates against users and API keys stored in PostgreSQL and
// provides the administrative operations (user/role/key management).
type LocalRealm struct {
	anon anonCache
	pool *pgxpool.Pool
}

// NewLocalRealm opens its own PostgreSQL pool for credential storage. The schema
// (auth_users, auth_api_keys, auth_roles, auth_user_roles) is created by the
// golang-migrate migrations at startup, so this constructor only opens the pool.
func NewLocalRealm(dsn string) (*LocalRealm, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &LocalRealm{pool: pool}, nil
}

// Pool exposes the underlying pool (used by the admin API handlers).
func (r *LocalRealm) Pool() *pgxpool.Pool { return r.pool }

func (r *LocalRealm) Name() string { return "local" }

func (r *LocalRealm) AuthenticatePassword(ctx context.Context, username, password string) (*User, error) {
	var hash string
	var admin, pcr bool
	err := r.pool.QueryRow(ctx,
		`SELECT password_hash, admin, password_change_required FROM auth_users WHERE name = $1 AND disabled = false AND kind = 'local'`, username).Scan(&hash, &admin, &pcr)
	if err == nil && bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil {
		return &User{
			Name:                   username,
			Admin:                  admin,
			Source:                 "local",
			PasswordChangeRequired: pcr,
			Groups:                 r.groupsOf(ctx, username),
		}, nil
	}
	// No password matched: the credential may be an API key used as a client
	// password (docker login and friends only speak Basic). The key only
	// authenticates under its owner's name, and never confers the admin bit:
	// administrative power for keys flows exclusively through key grants that
	// the owner's own bindings also satisfy.
	return r.authenticateKey(ctx, username, password, true)
}

// authenticateKeyHash validates an API key by its stored hash, without a
// authenticateKey validates an API key, optionally requiring it to be presented
// under its owner's name (Basic clients must name an owner; bearer tokens
// carry no name to check).
func (r *LocalRealm) authenticateKey(ctx context.Context, username, password string, matchOwner bool) (*User, error) {
	if password == "" {
		return nil, ErrNoMatch
	}
	return r.authenticateKeyHash(ctx, username, hashAPIKey(password), matchOwner)
}

// authenticateKeyHash validates an API key by its stored hash. The JWT session
// restorer uses it after the token signature already authenticated the call,
// so no owner name is checked here.
func (r *LocalRealm) authenticateKeyHash(ctx context.Context, username, keyHash string, matchOwner bool) (*User, error) {
	var owner, label string
	var grantsRaw []byte
	var admin bool
	// Local accounts disabled after the key was issued stop working with it;
	// federated owners have no local row and resolve through the LEFT JOIN.
	// The admin bit mirrors the owner's only for display (/me): authorization
	// never reads it, key power flows exclusively through grants.
	err := r.pool.QueryRow(ctx,
		`SELECT k.username, k.label, k.grants, COALESCE(u.admin, false) FROM auth_api_keys k
		 LEFT JOIN auth_users u ON u.name = k.username AND u.kind = 'local'
		 WHERE k.key_hash = $1 AND (u.name IS NULL OR u.disabled = false)`,
		keyHash).Scan(&owner, &label, &grantsRaw, &admin)
	if err != nil {
		return nil, ErrNoMatch
	}
	if matchOwner && owner != username {
		return nil, ErrNoMatch
	}
	grants, err := decodeKeyGrants(grantsRaw)
	if err != nil {
		return nil, ErrNoMatch
	}
	return &User{
		Name:      owner,
		Admin:     admin,
		Source:    "token",
		KeyLabel:  label,
		KeyGrants: grants,
		Groups:    r.groupsOf(ctx, owner),
	}, nil
}

// decodeKeyGrants parses the stored grants. Corrupt data fails the
// authentication outright rather than falling back to full owner power;
// individually malformed entries are dropped while valid ones stand.
func decodeKeyGrants(raw []byte) ([]authz.KeyGrant, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out []authz.KeyGrant
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	kept := out[:0]
	for _, g := range out {
		if g.Valid() {
			kept = append(kept, g)
		}
	}
	return kept, nil
}

// groupsOf loads a local account's group memberships.
//
// Without this, "group:x" in a grant could only ever match an identity that
// arrived with group claims — LDAP or OIDC. A local user placed in a group
// gained precisely nothing, which is why the whole roles screen was decorative.
// Failures are swallowed on purpose: losing a group means losing access, never
// gaining it, so a transient database error must not fail the login open.
func (r *LocalRealm) groupsOf(ctx context.Context, username string) []string {
	rows, err := r.pool.Query(ctx,
		`SELECT group_name FROM auth_group_members WHERE username=$1 ORDER BY group_name`, username)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return out
		}
		out = append(out, g)
	}
	return out
}

func (r *LocalRealm) AuthenticateToken(ctx context.Context, token string) (*User, error) {
	return r.authenticateKey(ctx, "", token, false)
}

// BootstrapAdmin creates the given admin account if no users exist yet.
func (r *LocalRealm) BootstrapAdmin(name, password string) error {
	if name == "" {
		return nil
	}
	ctx := context.Background()
	var n int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM auth_users`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO auth_users(name, password_hash, disabled, admin) VALUES($1,$2,false,true)`, name, string(hash))
	return err
}

// GetSetting returns a global auth setting value. It returns an error if the key
// does not exist (callers decide the default).
func (r *LocalRealm) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := r.pool.QueryRow(ctx, `SELECT value FROM auth_settings WHERE key=$1`, key).Scan(&v)
	if err != nil {
		return "", err
	}
	return v, nil
}

// SetSetting upserts a global auth setting value.
func (r *LocalRealm) SetSetting(ctx context.Context, key, value string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO auth_settings (key, value, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		key, value)
	return err
}

// ChangePassword verifies the current password and replaces it with newPassword,
// clearing the password_change_required flag. It is the server-side handler for
// both the forced first-login change and the voluntary change in the UI.
func (r *LocalRealm) ChangePassword(ctx context.Context, username, current, newPassword string) error {
	var hash string
	var disabled bool
	if err := r.pool.QueryRow(ctx,
		`SELECT password_hash, disabled FROM auth_users WHERE name = $1 AND kind = 'local'`, username).Scan(&hash, &disabled); err != nil {
		return ErrNoMatch
	}
	if disabled {
		return ErrNoMatch
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
		return ErrNoMatch
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx,
		`UPDATE auth_users SET password_hash = $2, password_change_required = false WHERE name = $1`,
		username, string(newHash))
	return err
}

// --- User management ---

func (r *LocalRealm) ListUsers(ctx context.Context) ([]UserView, error) {
	rows, err := r.pool.Query(ctx, `SELECT name, admin, disabled, password_change_required FROM auth_users WHERE kind = 'local' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// See GetUserRoles: collections must marshal as [] rather than null.
	out := []UserView{}
	for rows.Next() {
		var u UserView
		if err := rows.Scan(&u.Name, &u.Admin, &u.Disabled, &u.PasswordChangeRequired); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *LocalRealm) CreateUser(ctx context.Context, name, password string, admin bool) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO auth_users(name, password_hash, disabled, admin, created_at)
		 VALUES($1,$2,false,$3,$4)`, name, string(hash), admin, time.Now().UnixNano())
	return err
}

func (r *LocalRealm) UpdateUser(ctx context.Context, name, password string, disabled, admin *bool) error {
	if password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		if _, err := r.pool.Exec(ctx, `UPDATE auth_users SET password_hash=$2 WHERE name=$1`, name, string(hash)); err != nil {
			return err
		}
	}
	if disabled != nil {
		if _, err := r.pool.Exec(ctx, `UPDATE auth_users SET disabled=$2 WHERE name=$1`, name, *disabled); err != nil {
			return err
		}
	}
	if admin != nil {
		if _, err := r.pool.Exec(ctx, `UPDATE auth_users SET admin=$2 WHERE name=$1`, name, *admin); err != nil {
			return err
		}
	}
	return nil
}

func (r *LocalRealm) DeleteUser(ctx context.Context, name string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM auth_users WHERE name=$1 AND kind = 'local'`, name)
	return err
}

// --- API keys (service accounts) ---

// ErrKeyNotFound is returned when revoking a key that does not exist, so a
// revocation that matched nothing is reported instead of silently succeeding.
var ErrKeyNotFound = errors.New("auth: api key not found")

// apiKeyPrefixLen is how much of a key is kept in cleartext to identify it in
// the admin UI: "rk_" plus 8 hex characters. The remaining 40 hex characters
// (160 bits) stay secret, so the prefix is not a meaningful disclosure.
const apiKeyPrefixLen = 11

// hashAPIKey returns the stored form of an API key. Keys carry 192 bits of
// entropy, so a single SHA-256 pass is enough — there is no low-entropy secret
// to protect against brute force, and this runs on every token request.
func hashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// apiKeyPrefix returns the non-secret identifying prefix of a key.
func apiKeyPrefix(key string) string {
	if len(key) < apiKeyPrefixLen {
		return key
	}
	return key[:apiKeyPrefixLen]
}

func (r *LocalRealm) CreateAPIKey(ctx context.Context, username, label string, grants []authz.KeyGrant) (string, error) {
	key := "rk_" + digest.EncodeHex(randBytes(24))
	grantsJSON := []byte("[]")
	if len(grants) > 0 {
		var err error
		if grantsJSON, err = json.Marshal(grants); err != nil {
			return "", err
		}
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO auth_api_keys(key_hash, key_prefix, username, label, grants, created_at)
		 VALUES($1,$2,$3,$4,$5,$6)`,
		hashAPIKey(key), apiKeyPrefix(key), username, label, string(grantsJSON), time.Now().UnixNano())
	if err != nil {
		return "", err
	}
	// The cleartext key is returned exactly once, here; it is not recoverable
	// from storage afterwards.
	return key, nil
}

func (r *LocalRealm) ListAPIKeys(ctx context.Context) ([]APIKeyView, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT key_prefix, username, label, grants FROM auth_api_keys ORDER BY username, label`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKeyView{}
	for rows.Next() {
		var pfx, u, l string
		var grantsRaw []byte
		if err := rows.Scan(&pfx, &u, &l, &grantsRaw); err != nil {
			return nil, err
		}
		grants, _ := decodeKeyGrants(grantsRaw)
		out = append(out, APIKeyView{Masked: maskKey(pfx), Username: u, Label: l, Grants: grants})
	}
	return out, rows.Err()
}

// RevokeAPIKey deletes a key identified either by its cleartext value or by the
// masked form shown in the admin UI (the only identifier an operator still has
// once the key has been issued). It reports ErrKeyNotFound when nothing matched,
// so a revocation that hits nothing is not mistaken for success.
func (r *LocalRealm) RevokeAPIKey(ctx context.Context, key string) error {
	var tag pgconn.CommandTag
	var err error
	if strings.Contains(key, "*") {
		tag, err = r.pool.Exec(ctx,
			`DELETE FROM auth_api_keys WHERE key_prefix=$1`, maskedToPrefix(key))
	} else {
		tag, err = r.pool.Exec(ctx,
			`DELETE FROM auth_api_keys WHERE key_hash=$1`, hashAPIKey(key))
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrKeyNotFound
	}
	return nil
}

// RevokeOwnAPIKey deletes a key only when it belongs to the given owner, for
// self-service revocation. Anything else reports ErrKeyNotFound, so one user
// can neither revoke nor probe another's keys.
func (r *LocalRealm) RevokeOwnAPIKey(ctx context.Context, username, key string) error {
	var tag pgconn.CommandTag
	var err error
	if strings.Contains(key, "*") {
		tag, err = r.pool.Exec(ctx,
			`DELETE FROM auth_api_keys WHERE key_prefix=$1 AND username=$2`, maskedToPrefix(key), username)
	} else {
		tag, err = r.pool.Exec(ctx,
			`DELETE FROM auth_api_keys WHERE key_hash=$1 AND username=$2`, hashAPIKey(key), username)
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrKeyNotFound
	}
	return nil
}

// maskedToPrefix recovers the stored prefix from the masked form produced by
// maskKey ("rk_xxxxxxxx****").
func maskedToPrefix(masked string) string {
	if i := strings.IndexByte(masked, '*'); i >= 0 {
		return masked[:i]
	}
	return masked
}

// --- Roles (RBAC) ---

func (r *LocalRealm) ListRoles(ctx context.Context) ([]RoleView, error) {
	rows, err := r.pool.Query(ctx, `SELECT name, description, permissions FROM auth_roles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RoleView{}
	for rows.Next() {
		var name, desc, perms string
		if err := rows.Scan(&name, &desc, &perms); err != nil {
			return nil, err
		}
		out = append(out, RoleView{Name: name, Description: desc, Permissions: splitPerms(perms)})
	}
	return out, rows.Err()
}

func (r *LocalRealm) UpsertRole(ctx context.Context, name, description string, permissions []string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO auth_roles(name, description, permissions) VALUES($1,$2,$3)
		 ON CONFLICT (name) DO UPDATE SET description = EXCLUDED.description, permissions = EXCLUDED.permissions`,
		name, description, joinPerms(permissions))
	return err
}

func (r *LocalRealm) DeleteRole(ctx context.Context, name string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM auth_roles WHERE name=$1`, name)
	return err
}

func (r *LocalRealm) SetUserRoles(ctx context.Context, username string, roles []string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM auth_user_roles WHERE username=$1`, username); err != nil {
		return err
	}
	for _, role := range roles {
		if _, err := tx.Exec(ctx, `INSERT INTO auth_user_roles(username, role) VALUES($1,$2) ON CONFLICT DO NOTHING`, username, role); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *LocalRealm) GetUserRoles(ctx context.Context, username string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT role FROM auth_user_roles WHERE username=$1 ORDER BY role`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// Empty, not nil: a nil slice marshals to JSON null and the UI reads
	// .length off it. Collections in responses are always arrays.
	out := []string{}
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, err
		}
		out = append(out, role)
	}
	return out, rows.Err()
}

// maskKey renders the stored, non-secret key prefix for display. It is also the
// identifier the admin UI sends back to revoke the key, so its shape must stay
// in sync with maskedToPrefix.
func maskKey(prefix string) string {
	if prefix == "" {
		return "****"
	}
	return prefix + "****"
}

func splitPerms(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	cur := ""
	for _, c := range s {
		if c == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(c)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func joinPerms(p []string) string {
	out := ""
	for i, x := range p {
		if i > 0 {
			out += ","
		}
		out += x
	}
	return out
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		for i := range b {
			b[i] = byte(i)
		}
	}
	return b
}

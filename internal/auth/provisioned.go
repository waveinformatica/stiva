package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"registry/internal/authz"
)

// ProvisionedLabel marks the API keys of declared service accounts. Those
// keys are owned by the declaration: every startup makes the database match
// it, so a key revoked from the UI comes back and an account removed from the
// declaration loses its key and its grants.
const ProvisionedLabel = "provisioned"

// minProvisionedKeyLen is the shortest key accepted from a declaration: "rk_"
// and at least 32 characters, as much entropy as a generated key needs to be
// stored with a single SHA-256 pass.
const minProvisionedKeyLen = 35

// ServiceAccount is a service account declared in the configuration: its name,
// the one role it holds within one scope, and its API key, read from a file
// (a mounted Kubernetes Secret) so the same Secret can be given to the
// workload that uses the account.
type ServiceAccount struct {
	Name    string      `json:"name"`
	Role    string      `json:"role"`
	Scope   authz.Scope `json:"scope"`
	KeyFile string      `json:"key_file"`
}

// provisioned is a declared account with its key read.
type provisioned struct {
	ServiceAccount
	key string
}

// readServiceAccounts validates the declaration and reads the keys.
func readServiceAccounts(accounts []ServiceAccount, readFile func(string) ([]byte, error)) ([]provisioned, error) {
	seen := map[string]bool{}
	out := make([]provisioned, 0, len(accounts))
	for _, a := range accounts {
		switch {
		case a.Name == "" || a.Role == "" || a.KeyFile == "":
			return nil, fmt.Errorf("service account %q: name, role and key_file are required", a.Name)
		case seen[a.Name]:
			return nil, fmt.Errorf("service account %q declared twice", a.Name)
		case a.Role == authz.SystemAdmin:
			return nil, fmt.Errorf("service account %q: %s cannot be given to a declared account", a.Name, authz.SystemAdmin)
		case !a.Scope.Valid():
			return nil, fmt.Errorf("service account %q: invalid scope %q", a.Name, a.Scope)
		}
		seen[a.Name] = true
		b, err := readFile(a.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("service account %q: %w", a.Name, err)
		}
		key := strings.TrimSpace(string(b))
		if !strings.HasPrefix(key, "rk_") || len(key) < minProvisionedKeyLen {
			return nil, fmt.Errorf("service account %q: the key must start with rk_ and be at least %d characters long", a.Name, minProvisionedKeyLen)
		}
		out = append(out, provisioned{ServiceAccount: a, key: key})
	}
	return out, nil
}

// ProvisionServiceAccounts makes the database match the declared service
// accounts, in one transaction: each account holds exactly its declared grant
// and exactly one provisioned key, the one read from its file; accounts no
// longer declared lose their provisioned keys and their grants. The caller
// reloads the authorization engine afterwards.
func (r *LocalRealm) ProvisionServiceAccounts(ctx context.Context, accounts []ServiceAccount, readFile func(string) ([]byte, error)) error {
	declared, err := readServiceAccounts(accounts, readFile)
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	names := make([]string, 0, len(declared))
	for _, a := range declared {
		names = append(names, a.Name)
		// A declared account is not a person: a local user of the same name
		// would silently gain the account's grants.
		var users int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM auth_users WHERE name = $1`, a.Name).Scan(&users); err != nil {
			return err
		}
		if users > 0 {
			return fmt.Errorf("service account %q: a user with that name exists", a.Name)
		}
		var roles int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM auth_roles WHERE name = $1`, a.Role).Scan(&roles); err != nil {
			return err
		}
		if roles == 0 {
			return fmt.Errorf("service account %q: no role %q", a.Name, a.Role)
		}

		hash := hashAPIKey(a.key)
		var owner string
		err := tx.QueryRow(ctx, `SELECT username FROM auth_api_keys WHERE key_hash = $1`, hash).Scan(&owner)
		switch {
		case err == nil && owner != a.Name:
			return fmt.Errorf("service account %q: its key is already the key of %q", a.Name, owner)
		case err != nil && !isNoRows(err):
			return err
		}
		grants, err := json.Marshal([]authz.KeyGrant{{Role: a.Role, Scope: a.Scope}})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO auth_api_keys(key_hash, key_prefix, username, label, grants, created_at)
			 VALUES($1,$2,$3,$4,$5,$6)
			 ON CONFLICT (key_hash) DO UPDATE SET label = EXCLUDED.label, grants = EXCLUDED.grants`,
			hash, apiKeyPrefix(a.key), a.Name, ProvisionedLabel, string(grants), time.Now().UnixNano()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM auth_api_keys WHERE username = $1 AND key_hash <> $2`, a.Name, hash); err != nil {
			return err
		}
		subject := "user:" + a.Name
		if _, err := tx.Exec(ctx,
			`INSERT INTO auth_bindings(subject, role, scope) VALUES($1,$2,$3)
			 ON CONFLICT (subject, role, scope) DO NOTHING`, subject, a.Role, string(a.Scope)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM auth_bindings WHERE subject = $1 AND NOT (role = $2 AND scope = $3)`,
			subject, a.Role, string(a.Scope)); err != nil {
			return err
		}
	}

	// Accounts provisioned before and no longer declared.
	rows, err := tx.Query(ctx,
		`SELECT DISTINCT username FROM auth_api_keys WHERE label = $1 AND NOT (username = ANY($2))`,
		ProvisionedLabel, names)
	if err != nil {
		return err
	}
	var gone []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		gone = append(gone, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, n := range gone {
		if _, err := tx.Exec(ctx, `DELETE FROM auth_api_keys WHERE username = $1`, n); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM auth_bindings WHERE subject = $1`, "user:"+n); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

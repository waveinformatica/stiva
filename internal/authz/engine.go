package authz

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrLastAdmin   = errors.New("authz: this would leave nobody able to administer the registry")
	ErrSystemRole  = errors.New("authz: the built-in system:admin role cannot be modified or deleted")
	ErrBadScope    = errors.New("authz: malformed scope")
	ErrBadPerm     = errors.New("authz: unknown permission")
	ErrAnonAdmin   = errors.New("authz: an anonymous principal cannot hold administrative permissions")
	ErrRoleUnknown = errors.New("authz: unknown role")
)

// Principal is who is asking. It is deliberately not the auth package's User:
// authorization should not depend on how identity was established.
type Principal struct {
	Name      string
	Groups    []string
	Anonymous bool
	// KeyGrants restricts an API-key session to the listed (role, scope)
	// pairs, evaluated as the owner. A request must pass them AND the
	// owner's own bindings, so a key can never exceed its owner however it
	// is scoped; an empty set means full owner power (the legacy keys).
	KeyGrants []KeyGrant
}

// KeyGrant is one (role, scope) pair attached to an API key. The subject is
// always the key owner, so group narrowing happens through scopes, and the
// owner's live group memberships keep applying on the owner side of the
// intersection.
type KeyGrant struct {
	Role  string `json:"role"`
	Scope Scope  `json:"scope"`
}

// Valid reports whether the grant is well-formed: a role name and a
// well-formed scope. Role existence is checked against the role table at
// write time, where the engine snapshot is at hand.
func (g KeyGrant) Valid() bool {
	return g.Role != "" && g.Scope.Valid()
}

// Subjects expands a principal into the binding subjects that apply to it.
//
// This is the piece that used to be missing for local accounts: group subjects
// only ever matched claims carried by federated identities, so a local user
// could be put in a group and gain nothing from it.
func (p *Principal) Subjects() []string {
	if p == nil {
		return nil
	}
	if p.Anonymous {
		return []string{"anonymous", "user:" + p.Name}
	}
	out := []string{"authenticated", "user:" + p.Name}
	for _, g := range p.Groups {
		out = append(out, "group:"+g)
	}
	return out
}

// Binding grants a role to a subject within a scope.
type Binding struct {
	ID      int64  `json:"id"`
	Subject string `json:"subject"`
	Role    string `json:"role"`
	Scope   Scope  `json:"scope"`
}

// Role is a named set of permissions.
type Role struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Permissions []Permission `json:"permissions"`
	BuiltIn     bool         `json:"built_in"`
}

// Group is a local group. Federated identities bring their own groups as
// claims; these are for local accounts, so both kinds resolve the same way.
type Group struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Members     []string `json:"members"`
}

type snapshot struct {
	rolePerms map[string]map[Permission]bool
	bindings  []Binding
}

// Engine answers authorization questions from an in-memory snapshot, refreshed
// whenever the rules change. Permission checks sit on the hot path of every
// pull, so they must not become a database round trip each.
type Engine struct {
	pool *pgxpool.Pool

	mu   sync.RWMutex
	snap snapshot
}

func NewEngine(pool *pgxpool.Pool) *Engine {
	return &Engine{pool: pool, snap: snapshot{rolePerms: map[string]map[Permission]bool{}}}
}

// NewInMemory builds an engine from a fixed set of rules, with no database
// behind it. Authorization is pure logic over a snapshot, so it can be
// exercised — and reasoned about — without one.
func NewInMemory(rolePerms map[string][]Permission, bindings []Binding) *Engine {
	e := &Engine{snap: snapshot{rolePerms: map[string]map[Permission]bool{}}}
	for role, ps := range rolePerms {
		m := map[Permission]bool{}
		for _, p := range ps {
			m[p] = true
		}
		e.snap.rolePerms[role] = m
	}
	all := map[Permission]bool{}
	for _, p := range All {
		all[p] = true
	}
	e.snap.rolePerms[SystemAdmin] = all
	e.snap.bindings = bindings
	return e
}

// Reload rebuilds the snapshot from the database.
func (e *Engine) Reload(ctx context.Context) error {
	perms := map[string]map[Permission]bool{}
	rows, err := e.pool.Query(ctx, `SELECT role_name, permission FROM auth_role_permissions`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var role, perm string
		if err := rows.Scan(&role, &perm); err != nil {
			rows.Close()
			return err
		}
		if perms[role] == nil {
			perms[role] = map[Permission]bool{}
		}
		perms[role][Permission(perm)] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// The built-in role is defined as "every permission", so a permission added
	// in a later release is covered without touching data.
	all := map[Permission]bool{}
	for _, p := range All {
		all[p] = true
	}
	perms[SystemAdmin] = all

	var bindings []Binding
	brows, err := e.pool.Query(ctx, `SELECT id, subject, role, scope FROM auth_bindings`)
	if err != nil {
		return err
	}
	defer brows.Close()
	for brows.Next() {
		var b Binding
		if err := brows.Scan(&b.ID, &b.Subject, &b.Role, &b.Scope); err != nil {
			return err
		}
		bindings = append(bindings, b)
	}
	if err := brows.Err(); err != nil {
		return err
	}

	e.mu.Lock()
	e.snap = snapshot{rolePerms: perms, bindings: bindings}
	e.mu.Unlock()
	return nil
}

// Allows reports whether the principal holds a permission over a repository of
// a given format and registry. Pass an empty repo to ask about the registry as
// a whole, and empty format/registry for a global (administrative) question.
func (e *Engine) Allows(p *Principal, perm Permission, format, registry, repo string) bool {
	if p == nil {
		return false
	}
	// Administrative power is global by nature; a scope that narrows it would
	// look like a restriction without being one.
	if perm.IsAdmin() && p.Anonymous {
		return false
	}

	subjects := map[string]bool{}
	for _, s := range p.Subjects() {
		subjects[s] = true
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	// API-key sessions pass their own grants first, as the owner. Whatever
	// survives still has to pass the owner's bindings below, which is what
	// keeps a key from ever exceeding its owner.
	if len(p.KeyGrants) > 0 {
		keyBindings := make([]Binding, 0, len(p.KeyGrants))
		for _, kg := range p.KeyGrants {
			keyBindings = append(keyBindings, Binding{Subject: "user:" + p.Name, Role: kg.Role, Scope: kg.Scope})
		}
		if !e.match(keyBindings, subjects, perm, format, registry, repo) {
			return false
		}
	}

	return e.match(e.snap.bindings, subjects, perm, format, registry, repo)
}

// match reports whether any binding grants the permission. Callers hold the
// read lock when the bindings come from the snapshot.
func (e *Engine) match(bindings []Binding, subjects map[string]bool, perm Permission, format, registry, repo string) bool {
	for _, b := range bindings {
		if !subjects[b.Subject] {
			continue
		}
		if !e.snap.rolePerms[b.Role][perm] {
			continue
		}
		if perm.IsAdmin() {
			if b.Scope == ScopeAll {
				return true
			}
			continue
		}
		if b.Scope.Matches(format, registry, repo) {
			return true
		}
	}
	return false
}

// IsAdmin reports whether the principal can administer anything at all. It is
// the replacement for the old boolean column on the user row.
func (e *Engine) IsAdmin(p *Principal) bool {
	for _, perm := range All {
		if perm.IsAdmin() && e.Allows(p, perm, "", "", "") {
			return true
		}
	}
	return false
}

// --- roles ---

// HasRole reports whether a role exists in the snapshot. Key-grant validation
// uses it so a typo fails at write time instead of becoming a grant that can
// never match.
func (e *Engine) HasRole(name string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, ok := e.snap.rolePerms[name]
	return ok
}

func (e *Engine) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := e.pool.Query(ctx, `SELECT name, description FROM auth_roles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Role{}
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.Name, &r.Description); err != nil {
			return nil, err
		}
		r.BuiltIn = r.Name == SystemAdmin
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	e.mu.RLock()
	defer e.mu.RUnlock()
	for i := range out {
		for p := range e.snap.rolePerms[out[i].Name] {
			out[i].Permissions = append(out[i].Permissions, p)
		}
		sort.Slice(out[i].Permissions, func(a, b int) bool {
			return out[i].Permissions[a] < out[i].Permissions[b]
		})
	}
	return out, nil
}

// UpsertRole stores a role and its permissions.
func (e *Engine) UpsertRole(ctx context.Context, r Role) error {
	if r.Name == SystemAdmin {
		return ErrSystemRole
	}
	if strings.TrimSpace(r.Name) == "" {
		return errors.New("authz: a role needs a name")
	}
	for _, p := range r.Permissions {
		if !p.Valid() {
			return fmt.Errorf("%w: %q", ErrBadPerm, p)
		}
	}
	return e.mutate(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO auth_roles(name, description, permissions) VALUES($1,$2,'')
			 ON CONFLICT (name) DO UPDATE SET description = EXCLUDED.description`,
			r.Name, r.Description); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM auth_role_permissions WHERE role_name=$1`, r.Name); err != nil {
			return err
		}
		for _, p := range r.Permissions {
			if _, err := tx.Exec(ctx,
				`INSERT INTO auth_role_permissions(role_name, permission) VALUES($1,$2)
				 ON CONFLICT DO NOTHING`, r.Name, string(p)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (e *Engine) DeleteRole(ctx context.Context, name string) error {
	if name == SystemAdmin {
		return ErrSystemRole
	}
	return e.mutate(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM auth_bindings WHERE role=$1`, name); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM auth_role_permissions WHERE role_name=$1`, name); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM auth_roles WHERE name=$1`, name)
		return err
	})
}

// --- bindings ---

func (e *Engine) ListBindings(ctx context.Context) ([]Binding, error) {
	rows, err := e.pool.Query(ctx, `SELECT id, subject, role, scope FROM auth_bindings ORDER BY subject, role, scope`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Binding{}
	for rows.Next() {
		var b Binding
		if err := rows.Scan(&b.ID, &b.Subject, &b.Role, &b.Scope); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (e *Engine) AddBinding(ctx context.Context, b Binding) error {
	if !b.Scope.Valid() {
		return fmt.Errorf("%w: %q", ErrBadScope, b.Scope)
	}
	if b.Subject == "" {
		return errors.New("authz: a grant needs a subject")
	}
	var exists bool
	if err := e.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM auth_roles WHERE name=$1)`, b.Role).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: %q", ErrRoleUnknown, b.Role)
	}
	// An anonymous principal must never be able to administer. This is the kind
	// of mistake that is made once and noticed much later.
	if b.Subject == "anonymous" && e.roleHasAdmin(b.Role) {
		return ErrAnonAdmin
	}
	return e.mutate(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO auth_bindings(subject, role, scope) VALUES($1,$2,$3)
			 ON CONFLICT (subject, role, scope) DO NOTHING`,
			b.Subject, b.Role, string(b.Scope))
		return err
	})
}

// UpdateBinding changes an existing grant in place, so narrowing a scope or
// swapping a role does not mean deleting and re-creating — which would drop the
// grant for a moment, and could be refused halfway by the anti-lockout guard.
func (e *Engine) UpdateBinding(ctx context.Context, id int64, b Binding) error {
	if !b.Scope.Valid() {
		return fmt.Errorf("%w: %q", ErrBadScope, b.Scope)
	}
	if b.Subject == "" {
		return errors.New("authz: a grant needs a subject")
	}
	var exists bool
	if err := e.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM auth_roles WHERE name=$1)`, b.Role).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: %q", ErrRoleUnknown, b.Role)
	}
	if b.Subject == "anonymous" && e.roleHasAdmin(b.Role) {
		return ErrAnonAdmin
	}
	return e.mutate(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE auth_bindings SET subject=$1, role=$2, scope=$3 WHERE id=$4`,
			b.Subject, b.Role, string(b.Scope), id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errors.New("authz: grant not found")
		}
		return nil
	})
}

func (e *Engine) DeleteBinding(ctx context.Context, id int64) error {
	return e.mutate(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM auth_bindings WHERE id=$1`, id)
		return err
	})
}

func (e *Engine) roleHasAdmin(role string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if role == SystemAdmin {
		return true
	}
	for p := range e.snap.rolePerms[role] {
		if p.IsAdmin() {
			return true
		}
	}
	return false
}

// mutate applies a change to the authorization rules inside a transaction and
// refuses to commit it when the resulting state would leave nobody able to
// administer the registry.
//
// The check has to run against the transaction, not after the fact: an earlier
// version wrote first and validated afterwards, so a refused deletion still
// removed the row — the caller was told "denied" while the grant was already
// gone. Evaluating inside the transaction means the refusal and the rollback
// are the same decision.
func (e *Engine) mutate(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := fn(tx); err != nil {
		return err
	}
	ok, err := hasAdminTx(ctx, tx)
	if err != nil {
		return err
	}
	if !ok {
		return ErrLastAdmin // the deferred rollback undoes the change
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return e.Reload(ctx)
}

// hasAdminTx reports, from inside a transaction, whether at least one enabled
// user would still hold administrative power.
func hasAdminTx(ctx context.Context, tx pgx.Tx) (bool, error) {
	rolePerms := map[string]map[Permission]bool{}
	rows, err := tx.Query(ctx, `SELECT role_name, permission FROM auth_role_permissions`)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var role, perm string
		if err := rows.Scan(&role, &perm); err != nil {
			rows.Close()
			return false, err
		}
		if rolePerms[role] == nil {
			rolePerms[role] = map[Permission]bool{}
		}
		rolePerms[role][Permission(perm)] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	all := map[Permission]bool{}
	for _, p := range All {
		all[p] = true
	}
	rolePerms[SystemAdmin] = all

	// Subjects that still carry administrative power at global scope.
	adminSubjects := map[string]bool{}
	brows, err := tx.Query(ctx, `SELECT subject, role, scope FROM auth_bindings`)
	if err != nil {
		return false, err
	}
	for brows.Next() {
		var subject, role, scope string
		if err := brows.Scan(&subject, &role, &scope); err != nil {
			brows.Close()
			return false, err
		}
		if Scope(scope) != ScopeAll {
			continue
		}
		for p := range rolePerms[role] {
			if p.IsAdmin() {
				adminSubjects[subject] = true
				break
			}
		}
	}
	brows.Close()
	if err := brows.Err(); err != nil {
		return false, err
	}
	if len(adminSubjects) == 0 {
		return false, nil
	}

	// An enabled account must actually reach one of those subjects. A grant to
	// a group nobody belongs to, or to a disabled user, is not an administrator.
	urows, err := tx.Query(ctx, `
		SELECT u.name, COALESCE(m.group_name, '')
		  FROM auth_users u
		  LEFT JOIN auth_group_members m ON m.username = u.name
		 WHERE u.disabled = false`)
	if err != nil {
		return false, err
	}
	defer urows.Close()
	groups := map[string][]string{}
	seen := map[string]bool{}
	for urows.Next() {
		var name, group string
		if err := urows.Scan(&name, &group); err != nil {
			return false, err
		}
		seen[name] = true
		if group != "" {
			groups[name] = append(groups[name], group)
		}
	}
	if err := urows.Err(); err != nil {
		return false, err
	}
	for name := range seen {
		p := &Principal{Name: name, Groups: groups[name]}
		for _, sub := range p.Subjects() {
			if adminSubjects[sub] {
				return true, nil
			}
		}
	}
	return false, nil
}

// --- groups ---

func (e *Engine) UserGroups(ctx context.Context, username string) ([]string, error) {
	rows, err := e.pool.Query(ctx,
		`SELECT group_name FROM auth_group_members WHERE username=$1 ORDER BY group_name`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (e *Engine) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := e.pool.Query(ctx, `SELECT name, description FROM auth_groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.Name, &g.Description); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		m, err := e.GroupMembers(ctx, out[i].Name)
		if err != nil {
			return nil, err
		}
		out[i].Members = m
	}
	return out, nil
}

func (e *Engine) GroupMembers(ctx context.Context, group string) ([]string, error) {
	rows, err := e.pool.Query(ctx,
		`SELECT username FROM auth_group_members WHERE group_name=$1 ORDER BY username`, group)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (e *Engine) UpsertGroup(ctx context.Context, g Group) error {
	if strings.TrimSpace(g.Name) == "" {
		return errors.New("authz: a group needs a name")
	}
	// Group membership feeds bindings, so removing someone from a group can
	// remove their last route to administering.
	return e.mutate(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO auth_groups(name, description) VALUES($1,$2)
			 ON CONFLICT (name) DO UPDATE SET description = EXCLUDED.description`,
			g.Name, g.Description); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM auth_group_members WHERE group_name=$1`, g.Name); err != nil {
			return err
		}
		for _, m := range g.Members {
			if _, err := tx.Exec(ctx,
				`INSERT INTO auth_group_members(group_name, username) VALUES($1,$2) ON CONFLICT DO NOTHING`,
				g.Name, m); err != nil {
				return err
			}
		}
		return nil
	})
}

func (e *Engine) DeleteGroup(ctx context.Context, name string) error {
	return e.mutate(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM auth_bindings WHERE subject=$1`, "group:"+name); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM auth_groups WHERE name=$1`, name)
		return err
	})
}

// EnsureAdmin re-asserts that a named user administers everything. It is the
// way back in when the last grant is lost: set REGISTRY_ADMIN_USER and restart.
func (e *Engine) EnsureAdmin(ctx context.Context, username string) error {
	if username == "" {
		return nil
	}
	_, err := e.pool.Exec(ctx,
		`INSERT INTO auth_bindings(subject, role, scope) VALUES($1,$2,'*')
		 ON CONFLICT (subject, role, scope) DO NOTHING`,
		"user:"+username, SystemAdmin)
	if err != nil {
		return err
	}
	return e.Reload(ctx)
}

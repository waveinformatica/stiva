package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// AnonymousIdentity is a caller with no credentials that the registry is
// nonetheless willing to recognise.
//
// It exists so that "allow unauthenticated pulls, but only from the cluster"
// can be stated once and enforced, instead of being a global switch that is
// either open to everyone or closed to everyone. Once matched, an anonymous
// identity is an ordinary principal: what it may do comes from the grants made
// to it, exactly as for a signed-in account.
type AnonymousIdentity struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Disabled    bool   `json:"disabled"`
	// CIDRs the caller's address must fall inside. Empty means "any address",
	// which is the catch-all rule.
	CIDRs []string `json:"cidrs"`
}

// Filtered reports whether the identity restricts by address at all.
func (a AnonymousIdentity) Filtered() bool { return len(a.CIDRs) > 0 }

// Matches reports whether a caller's address falls within the identity's rules.
func (a AnonymousIdentity) Matches(ip net.IP) bool {
	if a.Disabled {
		return false
	}
	if len(a.CIDRs) == 0 {
		return true // catch-all
	}
	if ip == nil {
		return false
	}
	for _, c := range a.CIDRs {
		_, n, err := net.ParseCIDR(strings.TrimSpace(c))
		if err != nil {
			continue
		}
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientIP resolves the caller's address.
//
// X-Forwarded-For is only consulted for as many hops as are declared trusted,
// counting from the right — the entries a trusted proxy appended itself. Anyone
// can put anything in that header, so reading the leftmost value would let a
// caller claim to be inside the cluster and pick up whatever an
// address-restricted identity grants. A filter that reads a forgeable header is
// worse than no filter, because it looks like one.
//
// With trustedProxies at 0 the header is ignored entirely, which is the safe
// default: the address seen is the one the connection came from.
func ClientIP(r *http.Request, trustedProxies int) net.IP {
	if trustedProxies > 0 {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			// The rightmost entry was added by the nearest proxy; step back one
			// per trusted hop to reach the address that proxy actually saw.
			idx := len(parts) - trustedProxies
			if idx < 0 {
				idx = 0
			}
			if idx < len(parts) {
				if ip := net.ParseIP(strings.TrimSpace(parts[idx])); ip != nil {
					return ip
				}
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

// anonCache holds the identities between changes. Resolution runs on every
// unauthenticated request — that is every image pull — so it must not be a
// database round trip each time.
type anonCache struct {
	mu    sync.RWMutex
	list  []AnonymousIdentity
	valid bool
}

func (c *anonCache) get() ([]AnonymousIdentity, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.list, c.valid
}

func (c *anonCache) set(l []AnonymousIdentity) {
	c.mu.Lock()
	c.list, c.valid = l, true
	c.mu.Unlock()
}

func (c *anonCache) invalidate() {
	c.mu.Lock()
	c.valid = false
	c.mu.Unlock()
}

// ListAnonymous returns the anonymous identities, ordered the way they are
// evaluated: address-restricted ones first, catch-alls last. A catch-all placed
// before a narrower rule would swallow every caller and make the narrower one
// unreachable, so the order is derived rather than left to chance.
func (r *LocalRealm) ListAnonymous(ctx context.Context) ([]AnonymousIdentity, error) {
	if l, ok := r.anon.get(); ok {
		return l, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT name, COALESCE(description, ''), disabled, match
		   FROM auth_users WHERE kind = 'anonymous' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AnonymousIdentity{}
	for rows.Next() {
		var a AnonymousIdentity
		var raw []byte
		if err := rows.Scan(&a.Name, &a.Description, &a.Disabled, &raw); err != nil {
			return nil, err
		}
		var m struct {
			CIDRs []string `json:"cidrs"`
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &m)
		}
		a.CIDRs = m.CIDRs
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Filtered() && !out[j].Filtered() })
	r.anon.set(out)
	return out, nil
}

// UpsertAnonymous stores an anonymous identity.
func (r *LocalRealm) UpsertAnonymous(ctx context.Context, a AnonymousIdentity) error {
	for _, c := range a.CIDRs {
		if _, _, err := net.ParseCIDR(strings.TrimSpace(c)); err != nil {
			return err
		}
	}
	m, err := json.Marshal(map[string]any{"cidrs": a.CIDRs})
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx,
		`INSERT INTO auth_users(name, password_hash, disabled, admin, password_change_required, created_at, kind, description, match)
		 VALUES($1,'',$2,false,false,extract(epoch from now())::bigint,'anonymous',$3,$4)
		 ON CONFLICT (name) DO UPDATE
		   SET disabled = EXCLUDED.disabled,
		       description = EXCLUDED.description,
		       match = EXCLUDED.match
		 WHERE auth_users.kind = 'anonymous'`,
		a.Name, a.Disabled, a.Description, string(m))
	if err != nil {
		return err
	}
	// The conflict clause skips rows that are not anonymous, so a name already
	// taken by a real account writes nothing. Say so instead of reporting a
	// success that did not happen.
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("auth: %q is already a user account", a.Name)
	}
	r.anon.invalidate()
	return nil
}

// DeleteAnonymous removes an anonymous identity. Grants made to it are left
// behind deliberately: they become inert, and reappear if the identity is
// recreated with the same name.
func (r *LocalRealm) DeleteAnonymous(ctx context.Context, name string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM auth_users WHERE name=$1 AND kind='anonymous'`, name)
	r.anon.invalidate()
	return err
}

// ResolveAnonymous returns the first anonymous identity that recognises the
// caller, or nil when none does — in which case the request is unauthenticated
// and gets a challenge.
func (r *LocalRealm) ResolveAnonymous(ctx context.Context, ip net.IP) *User {
	list, err := r.ListAnonymous(ctx)
	if err != nil {
		return nil
	}
	for _, a := range list {
		if a.Matches(ip) {
			return &User{Name: a.Name, Anonymous: true, Source: "anonymous"}
		}
	}
	return nil
}

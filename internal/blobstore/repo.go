package blobstore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"registry/internal/vault"
)

// Repo persists blob store definitions.
type Repo struct {
	pool  *pgxpool.Pool
	vault *vault.Vault
}

func NewRepo(pool *pgxpool.Pool, v *vault.Vault) *Repo { return &Repo{pool: pool, vault: v} }

// List returns every store, credentials still as references.
func (r *Repo) List(ctx context.Context) ([]*Store, error) {
	rows, err := r.pool.Query(ctx, `SELECT name, kind, description, config FROM blob_stores ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Store{}
	for rows.Next() {
		var name, kind, desc string
		var cfg []byte
		if err := rows.Scan(&name, &kind, &desc, &cfg); err != nil {
			return nil, err
		}
		s := &Store{}
		if err := json.Unmarshal(cfg, s); err != nil {
			return nil, fmt.Errorf("blobstore %q: %w", name, err)
		}
		s.Name, s.Kind, s.Description = name, Kind(kind), desc
		out = append(out, s)
	}
	return out, rows.Err()
}

// Get returns a single store.
func (r *Repo) Get(ctx context.Context, name string) (*Store, error) {
	var kind, desc string
	var cfg []byte
	err := r.pool.QueryRow(ctx,
		`SELECT kind, description, config FROM blob_stores WHERE name=$1`, name).Scan(&kind, &desc, &cfg)
	if err != nil {
		return nil, ErrNotFound
	}
	s := &Store{}
	if err := json.Unmarshal(cfg, s); err != nil {
		return nil, err
	}
	s.Name, s.Kind, s.Description = name, Kind(kind), desc
	return s, nil
}

// Upsert validates and stores a definition.
func (r *Repo) Upsert(ctx context.Context, s *Store) error {
	if err := s.Validate(); err != nil {
		return err
	}
	// Referenced credentials must exist, or the store is broken the moment it
	// is used and the failure surfaces far from the mistake.
	for _, ref := range s.SecretRefs() {
		if _, err := r.vault.Get(ctx, ref.Key()); err != nil {
			return fmt.Errorf("blobstore %q: credenziale %q inesistente", s.Name, ref.Key())
		}
	}
	cfg, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO blob_stores(name, kind, description, config) VALUES($1,$2,$3,$4)
		 ON CONFLICT (name) DO UPDATE
		   SET kind=EXCLUDED.kind, description=EXCLUDED.description,
		       config=EXCLUDED.config, updated_at=now()`,
		s.Name, string(s.Kind), s.Description, string(cfg))
	return err
}

// Delete removes a store, refusing while a registry still points at it.
func (r *Repo) Delete(ctx context.Context, name string) error {
	users, err := r.RegistriesUsing(ctx, name)
	if err != nil {
		return err
	}
	if len(users) > 0 {
		return fmt.Errorf("%w: %v", ErrInUse, users)
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM blob_stores WHERE name=$1`, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RegistriesUsing lists the registries referencing a store.
func (r *Repo) RegistriesUsing(ctx context.Context, name string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT name FROM registries_meta WHERE blob_store=$1 ORDER BY name`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// StoresUsingSecret lists the stores referencing a vault entry. It is what lets
// the vault refuse to delete a credential that is still wired to something,
// instead of breaking a store silently.
func (r *Repo) StoresUsingSecret(ctx context.Context, key string) ([]string, error) {
	all, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, s := range all {
		for _, ref := range s.SecretRefs() {
			if ref.Key() == key {
				out = append(out, s.Name)
				break
			}
		}
	}
	return out, nil
}

// SetRegistryStore points a registry at a store, under a key prefix that keeps
// its objects apart from the other registries sharing that store.
//
// The prefix is written once, when the link is first made, and never changed
// afterwards: moving it would leave every object already written under the old
// one unreachable.
func (r *Repo) SetRegistryStore(ctx context.Context, registry, store, prefix string) error {
	var val any
	if store != "" {
		val = store
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE registries_meta
		    SET blob_store = $1,
		        blob_prefix = CASE WHEN blob_store IS NULL OR blob_store = '' THEN $2 ELSE blob_prefix END
		  WHERE name = $3`,
		val, prefix, registry)
	return err
}

// RegistryStore returns the store a registry points at, or "" when it has none.
func (r *Repo) RegistryStore(ctx context.Context, registry string) (string, error) {
	store, _, err := r.RegistryLink(ctx, registry)
	return store, err
}

// RegistryLink returns the store a registry uses and the prefix its objects
// live under inside that store.
func (r *Repo) RegistryLink(ctx context.Context, registry string) (store, prefix string, err error) {
	var s *string
	if err := r.pool.QueryRow(ctx,
		`SELECT blob_store, blob_prefix FROM registries_meta WHERE name=$1`, registry).Scan(&s, &prefix); err != nil {
		return "", "", err
	}
	if s == nil {
		return "", prefix, nil
	}
	return *s, prefix, nil
}

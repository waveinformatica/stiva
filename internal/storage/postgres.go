package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"registry/internal/digest"
)

// PostgresMetadataStore implements MetadataStore on top of PostgreSQL.
type PostgresMetadataStore struct {
	pool *pgxpool.Pool
}

// OpenMetadataStore connects to PostgreSQL and prepares the schema.
func OpenMetadataStore(dsn string) (MetadataStore, error) {
	if dsn == "" {
		return nil, errors.New("storage: postgres DSN is required (no local metadata store is supported)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: connect postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("storage: ping postgres: %w", err)
	}
	if err := Migrate(dsn); err != nil {
		pool.Close()
		return nil, err
	}
	m := &PostgresMetadataStore{pool: pool}
	return m, nil
}

func (m *PostgresMetadataStore) Close() error { m.pool.Close(); return nil }

func (m *PostgresMetadataStore) now() int64 { return time.Now().UnixNano() }

func (m *PostgresMetadataStore) CreateRepo(registry, name string) error {
	_, err := m.pool.Exec(context.Background(),
		`INSERT INTO repositories(registry, name, created_at) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`,
		registry, name, m.now())
	return err
}

func (m *PostgresMetadataStore) RepoExists(registry, name string) (bool, error) {
	var n int
	err := m.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM repositories WHERE registry = $1 AND name = $2`, registry, name).Scan(&n)
	return n > 0, err
}

func (m *PostgresMetadataStore) ListRepos(registry string) ([]string, error) {
	rows, err := m.pool.Query(context.Background(),
		`SELECT name FROM repositories WHERE registry = $1 ORDER BY name`, registry)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// DeleteRepo removes a repository and all of its manifests, tags and links.
func (m *PostgresMetadataStore) DeleteRepo(registry, name string) error {
	ctx := context.Background()
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM manifest_blobs WHERE registry = $1 AND repo = $2`, registry, name); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM tags WHERE registry = $1 AND repo = $2`, registry, name); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM manifests WHERE registry = $1 AND repo = $2`, registry, name); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM repositories WHERE registry = $1 AND name = $2`, registry, name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *PostgresMetadataStore) RecordBlob(registry string, d digest.Digest, size int64) error {
	_, err := m.pool.Exec(context.Background(),
		`INSERT INTO blobs(registry, digest, size, created_at) VALUES($1,$2,$3,$4)
		 ON CONFLICT (registry, digest) DO UPDATE SET size = EXCLUDED.size`,
		registry, d.String(), size, m.now())
	return err
}

func (m *PostgresMetadataStore) BlobSize(registry string, d digest.Digest) (int64, error) {
	var sz int64
	err := m.pool.QueryRow(context.Background(),
		`SELECT size FROM blobs WHERE registry = $1 AND digest = $2`, registry, d.String()).Scan(&sz)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return sz, err
}

func (m *PostgresMetadataStore) BlobRefCount(registry string, d digest.Digest) (int, error) {
	var n int
	err := m.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM manifest_blobs WHERE registry = $1 AND blob_digest = $2`,
		registry, d.String()).Scan(&n)
	return n, err
}

func (m *PostgresMetadataStore) DeleteBlobMeta(registry string, d digest.Digest) error {
	_, err := m.pool.Exec(context.Background(),
		`DELETE FROM blobs WHERE registry = $1 AND digest = $2`, registry, d.String())
	return err
}

func (m *PostgresMetadataStore) PutManifest(registry, repo string, d digest.Digest, mediaType string, content []byte) error {
	if _, err := m.pool.Exec(context.Background(),
		`INSERT INTO manifests(registry, repo, digest, media_type, content, created_at)
		 VALUES($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (registry, repo, digest) DO UPDATE SET media_type = EXCLUDED.media_type, content = EXCLUDED.content`,
		registry, repo, d.String(), mediaType, content, m.now()); err != nil {
		return err
	}
	return m.CreateRepo(registry, repo)
}

func (m *PostgresMetadataStore) GetManifest(registry, repo string, d digest.Digest) ([]byte, string, error) {
	var content []byte
	var mt string
	err := m.pool.QueryRow(context.Background(),
		`SELECT content, media_type FROM manifests WHERE registry = $1 AND repo = $2 AND digest = $3`,
		registry, repo, d.String()).Scan(&content, &mt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	return content, mt, nil
}

func (m *PostgresMetadataStore) ManifestExists(registry, repo string, d digest.Digest) (bool, error) {
	var n int
	err := m.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM manifests WHERE registry = $1 AND repo = $2 AND digest = $3`,
		registry, repo, d.String()).Scan(&n)
	return n > 0, err
}

func (m *PostgresMetadataStore) DeleteManifest(registry, repo string, d digest.Digest) error {
	ctx := context.Background()
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`DELETE FROM manifest_blobs WHERE registry = $1 AND repo = $2 AND manifest_digest = $3`,
		registry, repo, d.String()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM tags WHERE registry = $1 AND repo = $2 AND digest = $3`, registry, repo, d.String()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM manifests WHERE registry = $1 AND repo = $2 AND digest = $3`, registry, repo, d.String()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *PostgresMetadataStore) ListManifests(registry, repo string) ([]digest.Digest, error) {
	rows, err := m.pool.Query(context.Background(),
		`SELECT digest FROM manifests WHERE registry = $1 AND repo = $2 ORDER BY created_at DESC`,
		registry, repo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []digest.Digest
	for rows.Next() {
		var ds string
		if err := rows.Scan(&ds); err != nil {
			return nil, err
		}
		out = append(out, digest.Digest(ds))
	}
	return out, rows.Err()
}

func (m *PostgresMetadataStore) ResolveTag(registry, repo, tag string) (digest.Digest, error) {
	var ds string
	err := m.pool.QueryRow(context.Background(),
		`SELECT digest FROM tags WHERE registry = $1 AND repo = $2 AND tag = $3`,
		registry, repo, tag).Scan(&ds)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return digest.Digest(ds), nil
}

func (m *PostgresMetadataStore) SetTag(registry, repo, tag string, d digest.Digest) error {
	if _, err := m.pool.Exec(context.Background(),
		`INSERT INTO tags(registry, repo, tag, digest) VALUES($1,$2,$3,$4)
		 ON CONFLICT (registry, repo, tag) DO UPDATE SET digest = EXCLUDED.digest`,
		registry, repo, tag, d.String()); err != nil {
		return err
	}
	return m.CreateRepo(registry, repo)
}

func (m *PostgresMetadataStore) ListTags(registry, repo string) ([]string, error) {
	rows, err := m.pool.Query(context.Background(),
		`SELECT tag FROM tags WHERE registry = $1 AND repo = $2 ORDER BY tag`,
		registry, repo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (m *PostgresMetadataStore) LinkBlob(registry, repo string, manifest, blob digest.Digest) error {
	_, err := m.pool.Exec(context.Background(),
		`INSERT INTO manifest_blobs(registry, repo, manifest_digest, blob_digest) VALUES($1,$2,$3,$4)
		 ON CONFLICT DO NOTHING`,
		registry, repo, manifest.String(), blob.String())
	return err
}

func (m *PostgresMetadataStore) BlobsForManifest(registry, repo string, manifest digest.Digest) ([]digest.Digest, error) {
	rows, err := m.pool.Query(context.Background(),
		`SELECT blob_digest FROM manifest_blobs WHERE registry = $1 AND repo = $2 AND manifest_digest = $3`,
		registry, repo, manifest.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []digest.Digest
	for rows.Next() {
		var ds string
		if err := rows.Scan(&ds); err != nil {
			return nil, err
		}
		out = append(out, digest.Digest(ds))
	}
	return out, rows.Err()
}

// ---- Registry definitions ----

func (m *PostgresMetadataStore) UpsertRegistry(r RegistryRecord) error {
	_, err := m.pool.Exec(context.Background(),
		`INSERT INTO registries_meta(name, format, type, config, created_at)
		 VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT (name) DO UPDATE SET format = EXCLUDED.format, type = EXCLUDED.type, config = EXCLUDED.config`,
		r.Name, r.Format, r.Type, r.Config, m.now())
	return err
}

func (m *PostgresMetadataStore) GetRegistry(name string) (*RegistryRecord, error) {
	var rec RegistryRecord
	err := m.pool.QueryRow(context.Background(),
		`SELECT name, format, type, config FROM registries_meta WHERE name = $1`, name).
		Scan(&rec.Name, &rec.Format, &rec.Type, &rec.Config)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func (m *PostgresMetadataStore) ListRegistries() ([]RegistryRecord, error) {
	rows, err := m.pool.Query(context.Background(),
		`SELECT name, format, type, config FROM registries_meta ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RegistryRecord
	for rows.Next() {
		var rec RegistryRecord
		if err := rows.Scan(&rec.Name, &rec.Format, &rec.Type, &rec.Config); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (m *PostgresMetadataStore) DeleteRegistry(name string) error {
	_, err := m.pool.Exec(context.Background(),
		`DELETE FROM registries_meta WHERE name = $1`, name)
	return err
}

// ---- SSO providers (browser login, UI-managed) ----

func (m *PostgresMetadataStore) ListSSOProviders() ([]SSOProviderRecord, error) {
	rows, err := m.pool.Query(context.Background(),
		`SELECT id, label, provider, client_id, tenant, base_url, issuer,
		        authorize_url, token_url, userinfo_url, scope, username_claim,
		        groups_claim, audience, admin_group, enabled
		   FROM sso_providers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SSOProviderRecord{}
	for rows.Next() {
		var rec SSOProviderRecord
		if err := rows.Scan(&rec.ID, &rec.Label, &rec.Provider, &rec.ClientID,
			&rec.Tenant, &rec.BaseURL, &rec.Issuer, &rec.AuthorizeURL, &rec.TokenURL,
			&rec.UserinfoURL, &rec.Scope, &rec.Username, &rec.Groups, &rec.Audience,
			&rec.AdminGroup, &rec.Enabled); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (m *PostgresMetadataStore) UpsertSSOProvider(r SSOProviderRecord) error {
	_, err := m.pool.Exec(context.Background(),
		`INSERT INTO sso_providers(id, label, provider, client_id, tenant, base_url,
		                           issuer, authorize_url, token_url, userinfo_url, scope,
		                           username_claim, groups_claim, audience, admin_group,
		                           enabled, updated_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,now())
		 ON CONFLICT (id) DO UPDATE SET label = EXCLUDED.label, provider = EXCLUDED.provider,
		   client_id = EXCLUDED.client_id, tenant = EXCLUDED.tenant, base_url = EXCLUDED.base_url,
		   issuer = EXCLUDED.issuer, authorize_url = EXCLUDED.authorize_url,
		   token_url = EXCLUDED.token_url, userinfo_url = EXCLUDED.userinfo_url,
		   scope = EXCLUDED.scope, username_claim = EXCLUDED.username_claim,
		   groups_claim = EXCLUDED.groups_claim, audience = EXCLUDED.audience,
		   admin_group = EXCLUDED.admin_group, enabled = EXCLUDED.enabled, updated_at = now()`,
		r.ID, r.Label, r.Provider, r.ClientID, r.Tenant, r.BaseURL, r.Issuer,
		r.AuthorizeURL, r.TokenURL, r.UserinfoURL, r.Scope, r.Username, r.Groups,
		r.Audience, r.AdminGroup, r.Enabled)
	return err
}

func (m *PostgresMetadataStore) DeleteSSOProvider(id string) error {
	_, err := m.pool.Exec(context.Background(),
		`DELETE FROM sso_providers WHERE id = $1`, id)
	return err
}

// ---- Objects (path-based artifacts) ----

func (m *PostgresMetadataStore) RecordObject(registry, p string, d digest.Digest, size int64, contentType string) error {
	_, err := m.pool.Exec(context.Background(),
		`INSERT INTO objects(registry, path, digest, size, content_type, created_at)
		 VALUES($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (registry, path) DO UPDATE SET
		   digest = EXCLUDED.digest, size = EXCLUDED.size,
		   content_type = EXCLUDED.content_type, created_at = EXCLUDED.created_at`,
		registry, p, d.String(), size, contentType, m.now())
	return err
}

func (m *PostgresMetadataStore) GetObjectMeta(registry, p string) (digest.Digest, int64, string, error) {
	var ds, ct string
	var sz int64
	err := m.pool.QueryRow(context.Background(),
		`SELECT digest, size, content_type FROM objects WHERE registry = $1 AND path = $2`,
		registry, p).Scan(&ds, &sz, &ct)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, "", ErrNotFound
	}
	if err != nil {
		return "", 0, "", err
	}
	return digest.Digest(ds), sz, ct, nil
}

func (m *PostgresMetadataStore) DeleteObject(registry, p string) error {
	_, err := m.pool.Exec(context.Background(),
		`DELETE FROM objects WHERE registry = $1 AND path = $2`, registry, p)
	return err
}

func (m *PostgresMetadataStore) ListObjects(registry, prefix string) ([]string, error) {
	rows, err := m.pool.Query(context.Background(),
		`SELECT path FROM objects WHERE registry = $1 AND path LIKE $2 ORDER BY path`,
		registry, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

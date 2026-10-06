-- Baseline schema for the registry metadata store.
-- Uses IF NOT EXISTS so this migration is safe to apply on a database already
-- bootstrapped by the legacy idempotent startup DDL; golang-migrate then tracks
-- the version in its own schema_migrations table.
CREATE TABLE IF NOT EXISTS registries_meta (
	name TEXT PRIMARY KEY,
	format TEXT NOT NULL,
	type TEXT NOT NULL,
	config JSONB NOT NULL,
	created_at BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS repositories (
	registry TEXT NOT NULL,
	name TEXT NOT NULL,
	created_at BIGINT NOT NULL,
	PRIMARY KEY (registry, name)
);

CREATE TABLE IF NOT EXISTS blobs (
	registry TEXT NOT NULL,
	digest TEXT NOT NULL,
	size BIGINT NOT NULL,
	created_at BIGINT NOT NULL,
	PRIMARY KEY (registry, digest)
);

CREATE TABLE IF NOT EXISTS manifests (
	registry TEXT NOT NULL,
	repo TEXT NOT NULL,
	digest TEXT NOT NULL,
	media_type TEXT NOT NULL,
	content BYTEA NOT NULL,
	created_at BIGINT NOT NULL,
	PRIMARY KEY (registry, repo, digest)
);

CREATE TABLE IF NOT EXISTS tags (
	registry TEXT NOT NULL,
	repo TEXT NOT NULL,
	tag TEXT NOT NULL,
	digest TEXT NOT NULL,
	PRIMARY KEY (registry, repo, tag)
);

CREATE TABLE IF NOT EXISTS manifest_blobs (
	registry TEXT NOT NULL,
	repo TEXT NOT NULL,
	manifest_digest TEXT NOT NULL,
	blob_digest TEXT NOT NULL,
	PRIMARY KEY (registry, repo, manifest_digest, blob_digest)
);

CREATE TABLE IF NOT EXISTS objects (
	registry TEXT NOT NULL,
	path TEXT NOT NULL,
	digest TEXT NOT NULL,
	size BIGINT NOT NULL,
	content_type TEXT NOT NULL DEFAULT '',
	created_at BIGINT NOT NULL,
	PRIMARY KEY (registry, path)
);

CREATE INDEX IF NOT EXISTS idx_manifests_reg_repo ON manifests(registry, repo);
CREATE INDEX IF NOT EXISTS idx_tags_reg_repo ON tags(registry, repo);
CREATE INDEX IF NOT EXISTS idx_mb_reg_manifest ON manifest_blobs(registry, manifest_digest);
CREATE INDEX IF NOT EXISTS idx_objects_reg_path ON objects(registry, path);

-- Authentication store (users, API keys / service accounts, RBAC roles). These
-- are created here (not at runtime) so the schema has a single source of truth
-- via golang-migrate. IF NOT EXISTS keeps this safe on databases already
-- bootstrapped by the legacy runtime DDL.
CREATE TABLE IF NOT EXISTS auth_users (
	name TEXT PRIMARY KEY,
	password_hash TEXT NOT NULL,
	disabled BOOLEAN NOT NULL DEFAULT false,
	admin BOOLEAN NOT NULL DEFAULT false,
	created_at BIGINT
);
CREATE TABLE IF NOT EXISTS auth_api_keys (
	key TEXT PRIMARY KEY,
	username TEXT NOT NULL,
	label TEXT,
	created_at BIGINT
);
CREATE INDEX IF NOT EXISTS idx_api_keys_user ON auth_api_keys(username);
CREATE TABLE IF NOT EXISTS auth_roles (
	name TEXT PRIMARY KEY,
	description TEXT,
	permissions TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS auth_user_roles (
	username TEXT NOT NULL,
	role TEXT NOT NULL,
	PRIMARY KEY (username, role)
);

-- Application catalog: the apps shown on the home page and the artifacts that
-- make each one up. Timestamps are UnixNano.
CREATE TABLE IF NOT EXISTS catalog_apps (
	id          BIGSERIAL PRIMARY KEY,
	slug        TEXT    NOT NULL UNIQUE,
	name        TEXT    NOT NULL,
	tagline     TEXT    NOT NULL DEFAULT '',
	description TEXT    NOT NULL DEFAULT '',
	icon        TEXT    NOT NULL DEFAULT '', -- https URL or data:image/... URI
	category    TEXT    NOT NULL DEFAULT '',
	homepage    TEXT    NOT NULL DEFAULT '',
	featured    BOOLEAN NOT NULL DEFAULT FALSE,
	position    INT     NOT NULL DEFAULT 0,
	created_at  BIGINT  NOT NULL,
	updated_at  BIGINT  NOT NULL
);

CREATE TABLE IF NOT EXISTS catalog_artifacts (
	id       BIGSERIAL PRIMARY KEY,
	app_id   BIGINT NOT NULL REFERENCES catalog_apps(id) ON DELETE CASCADE,
	registry TEXT   NOT NULL,
	ref      TEXT   NOT NULL,            -- OCI repository, or artifact path / prefix
	tag      TEXT   NOT NULL DEFAULT '', -- OCI: pin one tag (empty = show recent tags)
	label    TEXT   NOT NULL DEFAULT '',
	position INT    NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_catalog_artifacts_app ON catalog_artifacts(app_id, position, id);

-- Screenshots of a catalog application. The image is a data URI, served through
-- an authenticated endpoint so it follows the visibility of the application.
CREATE TABLE IF NOT EXISTS catalog_screenshots (
	id       BIGSERIAL PRIMARY KEY,
	app_id   BIGINT NOT NULL REFERENCES catalog_apps(id) ON DELETE CASCADE,
	image    TEXT   NOT NULL,
	caption  TEXT   NOT NULL DEFAULT '',
	position INT    NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_catalog_screenshots_app ON catalog_screenshots(app_id, position, id);

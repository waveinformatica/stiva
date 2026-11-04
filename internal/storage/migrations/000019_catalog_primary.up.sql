-- Which file of a version is "the" download: a glob matched against the file
-- name (empty = the largest non-checksum file).
ALTER TABLE catalog_artifacts ADD COLUMN IF NOT EXISTS primary_pattern TEXT NOT NULL DEFAULT '';

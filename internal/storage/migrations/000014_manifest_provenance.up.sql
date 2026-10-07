-- Provenance recorded at push time. author and image_created come from the
-- OCI/Docker image config referenced by the manifest, when it is available
-- locally (empty otherwise, e.g. proxied manifests whose config was never
-- stored). tags.pushed_at records the last push of the tag itself, so
-- re-tagging an old digest still shows as a fresh upload.
ALTER TABLE manifests ADD COLUMN IF NOT EXISTS author TEXT NOT NULL DEFAULT '';
ALTER TABLE manifests ADD COLUMN IF NOT EXISTS image_created TEXT NOT NULL DEFAULT '';
ALTER TABLE tags ADD COLUMN IF NOT EXISTS pushed_at BIGINT NOT NULL DEFAULT 0;
-- Backfill: a tag first seen pointing at a digest inherits that manifest's
-- first-seen time, so pre-existing tags show their original push, not zero.
UPDATE tags SET pushed_at = COALESCE((
	SELECT created_at FROM manifests
	WHERE manifests.registry = tags.registry
	  AND manifests.repo = tags.repo
	  AND manifests.digest = tags.digest
), 0) WHERE pushed_at = 0;

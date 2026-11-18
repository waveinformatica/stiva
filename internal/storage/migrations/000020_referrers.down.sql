DROP INDEX IF EXISTS idx_manifests_image;
DROP INDEX IF EXISTS idx_manifests_subject;
ALTER TABLE manifests DROP COLUMN IF EXISTS image;
ALTER TABLE manifests DROP COLUMN IF EXISTS artifact_type;
ALTER TABLE manifests DROP COLUMN IF EXISTS subject;

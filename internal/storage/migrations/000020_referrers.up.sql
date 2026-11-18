-- What a manifest says about itself, recorded at push time.
--   subject:       OCI 1.1 referrers. A manifest with a "subject" descriptor
--                  refers to another manifest of the same repository (an SBOM,
--                  a signature, an attestation).
--   artifact_type: the manifest's artifactType, or its config media type when
--                  artifactType is absent (the Distribution spec's rule).
--   image:         a single-platform manifest whose layers are all filesystem
--                  layers: a container image that can be scanned. Signatures,
--                  SBOMs and other artifacts are not images.
ALTER TABLE manifests ADD COLUMN IF NOT EXISTS subject TEXT NOT NULL DEFAULT '';
ALTER TABLE manifests ADD COLUMN IF NOT EXISTS artifact_type TEXT NOT NULL DEFAULT '';
ALTER TABLE manifests ADD COLUMN IF NOT EXISTS image BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX IF NOT EXISTS idx_manifests_subject ON manifests(registry, repo, subject) WHERE subject <> '';
CREATE INDEX IF NOT EXISTS idx_manifests_image ON manifests(created_at, registry, repo, digest) WHERE image;

-- Backfill the manifests stored before these columns existed, with the same
-- rules the Go code applies at push time. A row whose content is not JSON is
-- left as it is: it carries neither a subject nor layers.
DO $$
DECLARE
	r RECORD;
	j JSONB;
BEGIN
	FOR r IN SELECT registry, repo, digest, media_type, content FROM manifests LOOP
		BEGIN
			j := convert_from(r.content, 'UTF8')::jsonb;
		EXCEPTION WHEN others THEN
			CONTINUE;
		END;
		IF jsonb_typeof(j) <> 'object' THEN
			CONTINUE;
		END IF;
		UPDATE manifests
		   SET subject = COALESCE(j->'subject'->>'digest', ''),
		       artifact_type = COALESCE(NULLIF(j->>'artifactType', ''), j->'config'->>'mediaType', ''),
		       image = r.media_type IN ('application/vnd.oci.image.manifest.v1+json',
		                                'application/vnd.docker.distribution.manifest.v2+json')
		           AND jsonb_typeof(j->'layers') = 'array'
		           AND jsonb_array_length(j->'layers') > 0
		           AND NOT EXISTS (
		               SELECT 1 FROM jsonb_array_elements(j->'layers') l
		                WHERE COALESCE(l->>'mediaType', '') NOT IN (
		                    'application/vnd.oci.image.layer.v1.tar',
		                    'application/vnd.oci.image.layer.v1.tar+gzip',
		                    'application/vnd.oci.image.layer.v1.tar+zstd',
		                    'application/vnd.docker.image.rootfs.diff.tar.gzip',
		                    'application/vnd.oci.image.layer.nondistributable.v1.tar+gzip',
		                    'application/vnd.docker.image.rootfs.foreign.diff.tar.gzip'))
		 WHERE registry = r.registry AND repo = r.repo AND digest = r.digest;
	END LOOP;
END $$;

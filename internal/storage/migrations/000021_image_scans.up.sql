-- Image scanning (docs/scanning.md). Packages are kept once: an image lists
-- the packages it contains, a package carries its vulnerabilities, and the
-- daily refresh matches each distinct package once, whatever the number of
-- images that contain it.

-- One row per scanned image manifest. It goes away with its manifest.
CREATE TABLE IF NOT EXISTS image_scans (
	registry    TEXT   NOT NULL,
	repo        TEXT   NOT NULL,
	digest      TEXT   NOT NULL,
	-- Analyser of the full scan ("trivy/v0.76.2"): a different analyser makes
	-- the image pending again.
	analyzer    TEXT   NOT NULL,
	-- How the findings were filtered (ignore-unfixed, ignore policy digest).
	options     JSONB  NOT NULL,
	-- The SBOM artifact (a referrer of the image) and its document blob.
	sbom_manifest TEXT NOT NULL,
	sbom_blob     TEXT NOT NULL,
	-- Secrets are in the layers, which never change for a digest: they are
	-- found by the full scan only.
	secrets       JSONB NOT NULL,
	secret_counts JSONB NOT NULL,
	scanned_at    BIGINT NOT NULL,
	PRIMARY KEY (registry, repo, digest),
	FOREIGN KEY (registry, repo, digest) REFERENCES manifests(registry, repo, digest) ON DELETE CASCADE
);

-- Distinct packages. key = sha256 of the canonical JSON of {context, package}:
-- context is what the vulnerability match depends on besides the package (the
-- OS for OS packages, the ecosystem for libraries); package is the analyser's
-- package document without what depends on where it sits in an image.
CREATE TABLE IF NOT EXISTS scan_packages (
	key                        TEXT    PRIMARY KEY,
	context                    JSONB   NOT NULL,
	package                    JSONB   NOT NULL,
	purl                       TEXT    NOT NULL,
	-- The database and options the vulnerabilities below were matched with.
	database_version           INTEGER NOT NULL,
	database_updated_at        BIGINT  NOT NULL,
	options                    JSONB   NOT NULL,
	vulnerabilities_scanned_at BIGINT  NOT NULL,
	created_at                 BIGINT  NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_scan_packages_purl ON scan_packages(purl);

CREATE TABLE IF NOT EXISTS package_vulnerabilities (
	package_key      TEXT NOT NULL REFERENCES scan_packages(key) ON DELETE CASCADE,
	vulnerability_id TEXT NOT NULL,
	severity         TEXT NOT NULL,
	fixed            TEXT NOT NULL,
	title            TEXT NOT NULL,
	PRIMARY KEY (package_key, vulnerability_id)
);
CREATE INDEX IF NOT EXISTS idx_package_vulnerabilities_id ON package_vulnerabilities(vulnerability_id);

-- Which packages an image contains, per target (an OS, a lock file, a jar...)
-- and how many times: the analyser reports a vulnerability per occurrence, and
-- so does the registry.
CREATE TABLE IF NOT EXISTS image_packages (
	registry    TEXT    NOT NULL,
	repo        TEXT    NOT NULL,
	digest      TEXT    NOT NULL,
	target      TEXT    NOT NULL,
	package_key TEXT    NOT NULL REFERENCES scan_packages(key),
	occurrences INTEGER NOT NULL CHECK (occurrences > 0),
	PRIMARY KEY (registry, repo, digest, target, package_key),
	FOREIGN KEY (registry, repo, digest) REFERENCES image_scans(registry, repo, digest) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_image_packages_package ON image_packages(package_key);

-- The scanner's role: read images and submit their results.
INSERT INTO auth_roles (name, description, permissions) VALUES
  ('scanner', 'Read images and submit their scan results', '')
ON CONFLICT (name) DO NOTHING;
INSERT INTO auth_role_permissions (role_name, permission) VALUES
  ('scanner', 'registry:read'),
  ('scanner', 'registry:scan')
ON CONFLICT DO NOTHING;

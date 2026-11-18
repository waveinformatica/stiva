package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"registry/internal/digest"
)

// ScanTarget names one image manifest.
type ScanTarget struct {
	Registry string        `json:"registry"`
	Repo     string        `json:"repo"`
	Digest   digest.Digest `json:"digest"`
}

// ScanDatabase identifies the vulnerability database findings were matched
// against.
type ScanDatabase struct {
	Version   int
	UpdatedAt time.Time
}

// PackageVulnerability is one vulnerability of a package.
type PackageVulnerability struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Fixed    string `json:"fixed"`
	Title    string `json:"title"`
}

// ScanPackage is one distinct package (docs/scanning.md): Key is the sha256 of
// the canonical JSON of {context, package}.
type ScanPackage struct {
	Key     string          `json:"key"`
	Context json.RawMessage `json:"context"`
	Package json.RawMessage `json:"package"`
	PURL    string          `json:"purl"`
}

// PackageResult is a package with its vulnerabilities.
type PackageResult struct {
	ScanPackage
	Vulnerabilities []PackageVulnerability
}

// PackageOccurrence says that an image contains a package in a target, Count
// times.
type PackageOccurrence struct {
	Target     string
	PackageKey string
	Count      int
}

// FullScan is the result of the full scan of one image.
type FullScan struct {
	ScanTarget
	Analyzer     string
	Options      json.RawMessage
	SBOMManifest digest.Digest
	SBOMBlob     digest.Digest
	Secrets      json.RawMessage
	SecretCounts map[string]int
	ScannedAt    time.Time
	// The packages of the image and their vulnerabilities. A package already
	// known keeps the vulnerabilities matched against the newest database.
	Database    ScanDatabase
	Packages    []PackageResult
	Occurrences []PackageOccurrence
}

// ImageScan is the stored full-scan record of one image.
type ImageScan struct {
	ScanTarget
	Analyzer  string
	SBOMBlob  digest.Digest
	ScannedAt time.Time
}

// PackageRefresh is the daily refresh of some packages' vulnerabilities.
type PackageRefresh struct {
	Database  ScanDatabase
	Options   json.RawMessage
	ScannedAt time.Time
	Packages  []PackageVulnerabilities
}

// PackageVulnerabilities are the vulnerabilities of the package with Key.
type PackageVulnerabilities struct {
	Key             string
	Vulnerabilities []PackageVulnerability
}

// ScanSummary is the per-severity summary of one scanned image.
type ScanSummary struct {
	ScanTarget
	VulnerabilityCounts map[string]int
	SecretCounts        map[string]int
	ScannedAt           time.Time
	// VulnerabilitiesScannedAt is the oldest vulnerability match among the
	// image's packages (the scan time for an image without packages).
	VulnerabilitiesScannedAt time.Time
}

// ImageVulnerability is one vulnerability of an image: a package occurrence
// joined with the package's vulnerability.
type ImageVulnerability struct {
	Target      string `json:"target"`
	Occurrences int    `json:"occurrences"`
	PURL        string `json:"purl"`
	Package     string `json:"package"`
	Version     string `json:"version"`
	PackageVulnerability
}

// ScanStore persists image scan results (docs/scanning.md).
type ScanStore interface {
	// PendingScans lists the image manifests of the given registries that have
	// no scan, or whose full scan was made by another analyzer, oldest first.
	PendingScans(registries []string, analyzer string, limit int) ([]ScanTarget, error)
	GetImageScan(t ScanTarget) (*ImageScan, error)
	// PutFullScan writes the result of a full scan, replacing any previous one
	// of the same image, in one transaction.
	PutFullScan(s FullScan) error
	// ListScanPackages pages through the packages some image contains, in key
	// order, after the given key.
	ListScanPackages(afterKey string, limit int) ([]ScanPackage, error)
	// RefreshPackages replaces the vulnerabilities of the given packages,
	// unless they were matched against a newer database already. It returns
	// the keys it does not know.
	RefreshPackages(r PackageRefresh) (updated int, unknown []string, err error)
	// PruneScanPackages deletes the packages no image contains any more.
	PruneScanPackages() (int64, error)
	ScanSummaries(registries []string) ([]ScanSummary, error)
	ImageVulnerabilities(t ScanTarget) ([]ImageVulnerability, error)
}

func (m *PostgresMetadataStore) PendingScans(registries []string, analyzer string, limit int) ([]ScanTarget, error) {
	rows, err := m.pool.Query(context.Background(),
		`SELECT m.registry, m.repo, m.digest
		   FROM manifests m
		   LEFT JOIN image_scans s
		     ON s.registry = m.registry AND s.repo = m.repo AND s.digest = m.digest
		  WHERE m.image AND m.registry = ANY($1)
		    AND (s.digest IS NULL OR s.analyzer <> $2)
		  ORDER BY m.created_at, m.registry, m.repo, m.digest
		  LIMIT $3`,
		registries, analyzer, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScanTarget{}
	for rows.Next() {
		var t ScanTarget
		var d string
		if err := rows.Scan(&t.Registry, &t.Repo, &d); err != nil {
			return nil, err
		}
		t.Digest = digest.Digest(d)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (m *PostgresMetadataStore) GetImageScan(t ScanTarget) (*ImageScan, error) {
	s := ImageScan{ScanTarget: t}
	var blob string
	var at int64
	err := m.pool.QueryRow(context.Background(),
		`SELECT analyzer, sbom_blob, scanned_at FROM image_scans WHERE registry = $1 AND repo = $2 AND digest = $3`,
		t.Registry, t.Repo, t.Digest.String()).Scan(&s.Analyzer, &blob, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.SBOMBlob = digest.Digest(blob)
	s.ScannedAt = time.Unix(0, at).UTC()
	return &s, nil
}

// replaceVulnerabilities queues the replacement of a package's vulnerabilities
// and of the database they were matched against, unless the package already
// holds a match against a newer database.
func replaceVulnerabilities(b *pgx.Batch, key string, db ScanDatabase, options json.RawMessage, at time.Time, vulns []PackageVulnerability) {
	b.Queue(`DELETE FROM package_vulnerabilities v USING scan_packages p
	          WHERE v.package_key = p.key AND p.key = $1 AND p.database_updated_at <= $2`,
		key, db.UpdatedAt.UnixNano())
	for _, v := range vulns {
		b.Queue(`INSERT INTO package_vulnerabilities(package_key, vulnerability_id, severity, fixed, title)
		         SELECT $1, $2, $3, $4, $5 FROM scan_packages WHERE key = $1 AND database_updated_at <= $6`,
			key, v.ID, v.Severity, v.Fixed, v.Title, db.UpdatedAt.UnixNano())
	}
	// Last: the guards above compare with the database recorded before this
	// match.
	b.Queue(`UPDATE scan_packages SET database_version = $2, database_updated_at = $3, options = $4,
	                vulnerabilities_scanned_at = $5
	          WHERE key = $1 AND database_updated_at <= $3`,
		key, db.Version, db.UpdatedAt.UnixNano(), options, at.UnixNano())
}

func (m *PostgresMetadataStore) PutFullScan(s FullScan) error {
	ctx := context.Background()
	sc, err := json.Marshal(s.SecretCounts)
	if err != nil {
		return err
	}
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	b := &pgx.Batch{}
	b.Queue(`INSERT INTO image_scans(registry, repo, digest, analyzer, options, sbom_manifest, sbom_blob,
	                                 secrets, secret_counts, scanned_at)
	         VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	         ON CONFLICT (registry, repo, digest) DO UPDATE SET
	                analyzer = EXCLUDED.analyzer, options = EXCLUDED.options,
	                sbom_manifest = EXCLUDED.sbom_manifest, sbom_blob = EXCLUDED.sbom_blob,
	                secrets = EXCLUDED.secrets, secret_counts = EXCLUDED.secret_counts,
	                scanned_at = EXCLUDED.scanned_at`,
		s.Registry, s.Repo, s.Digest.String(), s.Analyzer, s.Options, s.SBOMManifest.String(), s.SBOMBlob.String(),
		s.Secrets, sc, s.ScannedAt.UnixNano())
	for _, p := range s.Packages {
		// A new package starts with no match (database time 0), so the match
		// of this scan is always applied to it.
		b.Queue(`INSERT INTO scan_packages(key, context, package, purl, database_version, database_updated_at,
		                                   options, vulnerabilities_scanned_at, created_at)
		         VALUES($1,$2,$3,$4,0,0,$5,0,$6) ON CONFLICT (key) DO NOTHING`,
			p.Key, p.Context, p.Package, p.PURL, s.Options, s.ScannedAt.UnixNano())
		replaceVulnerabilities(b, p.Key, s.Database, s.Options, s.ScannedAt, p.Vulnerabilities)
	}
	b.Queue(`DELETE FROM image_packages WHERE registry = $1 AND repo = $2 AND digest = $3`,
		s.Registry, s.Repo, s.Digest.String())
	for _, o := range s.Occurrences {
		b.Queue(`INSERT INTO image_packages(registry, repo, digest, target, package_key, occurrences)
		         VALUES($1,$2,$3,$4,$5,$6)`,
			s.Registry, s.Repo, s.Digest.String(), o.Target, o.PackageKey, o.Count)
	}
	if err := tx.SendBatch(ctx, b).Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *PostgresMetadataStore) ListScanPackages(afterKey string, limit int) ([]ScanPackage, error) {
	rows, err := m.pool.Query(context.Background(),
		`SELECT p.key, p.context, p.package, p.purl FROM scan_packages p
		  WHERE p.key > $1 AND EXISTS (SELECT 1 FROM image_packages i WHERE i.package_key = p.key)
		  ORDER BY p.key LIMIT $2`,
		afterKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScanPackage{}
	for rows.Next() {
		var p ScanPackage
		if err := rows.Scan(&p.Key, &p.Context, &p.Package, &p.PURL); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (m *PostgresMetadataStore) RefreshPackages(r PackageRefresh) (int, []string, error) {
	ctx := context.Background()
	keys := make([]string, 0, len(r.Packages))
	for _, p := range r.Packages {
		keys = append(keys, p.Key)
	}
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT key, database_updated_at FROM scan_packages WHERE key = ANY($1) FOR UPDATE`, keys)
	if err != nil {
		return 0, nil, err
	}
	stored := map[string]int64{}
	for rows.Next() {
		var k string
		var at int64
		if err := rows.Scan(&k, &at); err != nil {
			rows.Close()
			return 0, nil, err
		}
		stored[k] = at
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}
	unknown := []string{}
	updated := 0
	b := &pgx.Batch{}
	for _, p := range r.Packages {
		at, ok := stored[p.Key]
		if !ok {
			unknown = append(unknown, p.Key)
			continue
		}
		if at > r.Database.UpdatedAt.UnixNano() {
			continue
		}
		replaceVulnerabilities(b, p.Key, r.Database, r.Options, r.ScannedAt, p.Vulnerabilities)
		updated++
	}
	if err := tx.SendBatch(ctx, b).Close(); err != nil {
		return 0, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, nil, err
	}
	return updated, unknown, nil
}

func (m *PostgresMetadataStore) PruneScanPackages() (int64, error) {
	tag, err := m.pool.Exec(context.Background(),
		`DELETE FROM scan_packages p WHERE NOT EXISTS (SELECT 1 FROM image_packages i WHERE i.package_key = p.key)`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (m *PostgresMetadataStore) ScanSummaries(registries []string) ([]ScanSummary, error) {
	rows, err := m.pool.Query(context.Background(),
		`WITH v AS (
		     SELECT i.registry, i.repo, i.digest, pv.severity, SUM(i.occurrences)::int AS n
		       FROM image_packages i JOIN package_vulnerabilities pv ON pv.package_key = i.package_key
		      WHERE i.registry = ANY($1)
		      GROUP BY i.registry, i.repo, i.digest, pv.severity),
		  counts AS (
		     SELECT registry, repo, digest, jsonb_object_agg(severity, n) AS c FROM v
		      GROUP BY registry, repo, digest),
		  fresh AS (
		     SELECT i.registry, i.repo, i.digest, MIN(p.vulnerabilities_scanned_at) AS at
		       FROM image_packages i JOIN scan_packages p ON p.key = i.package_key
		      WHERE i.registry = ANY($1)
		      GROUP BY i.registry, i.repo, i.digest)
		 SELECT s.registry, s.repo, s.digest, COALESCE(counts.c, '{}'::jsonb), s.secret_counts,
		        s.scanned_at, COALESCE(fresh.at, s.scanned_at)
		   FROM image_scans s
		   LEFT JOIN counts ON counts.registry = s.registry AND counts.repo = s.repo AND counts.digest = s.digest
		   LEFT JOIN fresh ON fresh.registry = s.registry AND fresh.repo = s.repo AND fresh.digest = s.digest
		  WHERE s.registry = ANY($1)
		  ORDER BY s.registry, s.repo, s.digest`,
		registries)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScanSummary{}
	for rows.Next() {
		var s ScanSummary
		var d string
		var vc, sc []byte
		var at, vat int64
		if err := rows.Scan(&s.Registry, &s.Repo, &d, &vc, &sc, &at, &vat); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(vc, &s.VulnerabilityCounts); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(sc, &s.SecretCounts); err != nil {
			return nil, err
		}
		s.Digest = digest.Digest(d)
		s.ScannedAt = time.Unix(0, at).UTC()
		s.VulnerabilitiesScannedAt = time.Unix(0, vat).UTC()
		out = append(out, s)
	}
	return out, rows.Err()
}

func (m *PostgresMetadataStore) ImageVulnerabilities(t ScanTarget) ([]ImageVulnerability, error) {
	rows, err := m.pool.Query(context.Background(),
		`SELECT i.target, i.occurrences, p.purl, COALESCE(p.package->>'Name', ''), COALESCE(p.package->>'Version', ''),
		        v.vulnerability_id, v.severity, v.fixed, v.title
		   FROM image_packages i
		   JOIN scan_packages p ON p.key = i.package_key
		   JOIN package_vulnerabilities v ON v.package_key = i.package_key
		  WHERE i.registry = $1 AND i.repo = $2 AND i.digest = $3
		  ORDER BY i.target, p.purl, v.vulnerability_id`,
		t.Registry, t.Repo, t.Digest.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ImageVulnerability{}
	for rows.Next() {
		var v ImageVulnerability
		if err := rows.Scan(&v.Target, &v.Occurrences, &v.PURL, &v.Package, &v.Version,
			&v.ID, &v.Severity, &v.Fixed, &v.Title); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

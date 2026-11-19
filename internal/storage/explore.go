package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"registry/internal/digest"
)

// What people browse in the interface: packages, vulnerabilities and the
// findings of one image, always limited to the registries the caller can read
// (docs/scanning.md, "Browsing findings").

// PackageSummary is a distinct package with its vulnerabilities by severity
// and where it is.
type PackageSummary struct {
	Key             string         `json:"key"`
	Name            string         `json:"name"`
	Version         string         `json:"version"`
	PURL            string         `json:"purl"`
	Type            string         `json:"type"` // ecosystem or OS family (debian, npm, gobinary...)
	Vulnerabilities map[string]int `json:"vulnerabilities"`
	Images          int            `json:"images"`     // image manifests containing it
	Containers      int            `json:"containers"` // running containers whose image contains it
}

// VulnerabilitySummary is a vulnerability with how far it reaches.
type VulnerabilitySummary struct {
	ID         string `json:"id"`
	Severity   string `json:"severity"`
	Title      string `json:"title"`
	Packages   int    `json:"packages"`
	Images     int    `json:"images"`
	Containers int    `json:"containers"`
}

// FindingImage is an image manifest, with the tags that name it (its own, or
// those of an index holding it) and where it runs.
type FindingImage struct {
	ScanTarget
	Tags       []string    `json:"tags"`
	Placements []Placement `json:"placements"`
}

// AffectedPackage is a package carrying a vulnerability.
type AffectedPackage struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Version string `json:"version"`
	PURL    string `json:"purl"`
	Type    string `json:"type"`
	Fixed   string `json:"fixed"`
}

// VulnerabilityDetail is one vulnerability, the packages that carry it and the
// images containing them (at most ImageLimit, the running ones first).
type VulnerabilityDetail struct {
	ID         string            `json:"id"`
	Severity   string            `json:"severity"`
	Title      string            `json:"title"`
	Packages   []AffectedPackage `json:"packages"`
	ImageCount int               `json:"image_count"`
	ImagesOf   []FindingImage    `json:"images_of"`
	ImageLimit int               `json:"image_limit"`
}

// PackageDetail is one package, its vulnerabilities and the images containing
// it (at most ImageLimit, the running ones first).
type PackageDetail struct {
	PackageSummary
	Context json.RawMessage        `json:"context"`
	Matched []PackageVulnerability `json:"matched"`
	// When and against which vulnerability database the package was last
	// matched.
	MatchedAt         time.Time      `json:"matched_at"`
	DatabaseVersion   int            `json:"database_version"`
	DatabaseUpdatedAt time.Time      `json:"database_updated_at"`
	ImagesOf          []FindingImage `json:"images_of"`
	ImageLimit        int            `json:"image_limit"`
}

// ImagePackage is a package of an image, where it sits and its
// vulnerabilities.
type ImagePackage struct {
	Key             string                 `json:"key"`
	Name            string                 `json:"name"`
	Version         string                 `json:"version"`
	PURL            string                 `json:"purl"`
	Type            string                 `json:"type"`
	Targets         []string               `json:"targets"`
	Vulnerabilities []PackageVulnerability `json:"vulnerabilities"`
}

// ImageFindings is everything known of one image manifest. Scanned is false
// when it has no full scan (Failure says why, when it failed).
type ImageFindings struct {
	ScanTarget
	Scanned      bool            `json:"scanned"`
	Analyzer     string          `json:"analyzer,omitempty"`
	ScannedAt    *time.Time      `json:"scanned_at,omitempty"`
	SBOMManifest string          `json:"sbom_manifest,omitempty"`
	Counts       map[string]int  `json:"vulnerability_counts"`
	SecretCounts map[string]int  `json:"secret_counts"`
	Packages     []ImagePackage  `json:"packages"`
	Secrets      json.RawMessage `json:"secrets"`
	Placements   []Placement     `json:"placements"`
	Failure      *ScanFailure    `json:"failure,omitempty"`
}

// ExploreStore answers the interface's questions on findings.
type ExploreStore interface {
	SearchPackages(registries []string, query string, limit int, freshAfter time.Duration) ([]PackageSummary, error)
	SearchVulnerabilities(registries []string, query, severity string, limit int, freshAfter time.Duration) ([]VulnerabilitySummary, error)
	// GetPackage and GetVulnerability return ErrNotFound when no image of
	// the registries contains it.
	GetPackage(key string, registries []string, imageLimit int, freshAfter time.Duration) (*PackageDetail, error)
	GetVulnerability(id string, registries []string, imageLimit int, freshAfter time.Duration) (*VulnerabilityDetail, error)
	GetImageFindings(t ScanTarget, freshAfter time.Duration) (*ImageFindings, error)
}

// likePattern makes a substring ILIKE pattern of user input.
func likePattern(q string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q) + "%"
}

const indexMediaTypes = `('application/vnd.oci.image.index.v1+json', 'application/vnd.docker.distribution.manifest.list.v2+json')`

// findingImagesSQL lists the images of imgs (registry, repo, digest) with
// their tags and placements, running first; it expects resolved.
const findingImagesSQL = `
SELECT im.registry, im.repo, im.digest,
       COALESCE((SELECT array_agg(DISTINCT t.tag ORDER BY t.tag)
                   FROM tags t JOIN manifests m ON m.registry = t.registry AND m.repo = t.repo AND m.digest = t.digest
                  WHERE t.registry = im.registry AND t.repo = im.repo
                    AND (t.digest = im.digest
                         OR (m.media_type IN ` + indexMediaTypes + `
                             AND position(im.digest IN convert_from(m.content, 'UTF8')) > 0))), '{}'),
       COALESCE((SELECT json_agg(json_build_object('node', x.node, 'namespace', x.namespace, 'pod', x.pod,
                                                   'container', x.container, 'image', x.image, 'digest', x.pulled,
                                                   'platform', x.platform)
                                 ORDER BY x.namespace, x.pod, x.container)
                   FROM resolved x WHERE x.registry = im.registry AND x.repo = im.repo AND x.digest = im.digest), '[]')
  FROM imgs im
 ORDER BY (SELECT count(*) FROM resolved x WHERE x.registry = im.registry AND x.repo = im.repo AND x.digest = im.digest) DESC,
          im.registry, im.repo, im.digest
 LIMIT `

func scanFindingImages(rows pgx.Rows) ([]FindingImage, error) {
	defer rows.Close()
	out := []FindingImage{}
	for rows.Next() {
		var f FindingImage
		var dg string
		var pj []byte
		if err := rows.Scan(&f.Registry, &f.Repo, &dg, &f.Tags, &pj); err != nil {
			return nil, err
		}
		f.Digest = digest.Digest(dg)
		if err := json.Unmarshal(pj, &f.Placements); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func fresh(d time.Duration) int64 { return time.Now().Add(-d).UnixNano() }

func (m *PostgresMetadataStore) SearchPackages(registries []string, query string, limit int, freshAfter time.Duration) ([]PackageSummary, error) {
	rows, err := m.pool.Query(context.Background(), `
WITH `+resolvedCTE("$1", "$2")+`,
pk AS (
	SELECT p.key, p.purl, COALESCE(p.package->>'Name', '') AS name, COALESCE(p.package->>'Version', '') AS version,
	       COALESCE(p.context->>'type', '') AS type
	  FROM scan_packages p
	 WHERE (p.package->>'Name' ILIKE $3 OR p.purl ILIKE $3)
	   AND EXISTS (SELECT 1 FROM image_packages i WHERE i.package_key = p.key AND i.registry = ANY($1))
),
counted AS (
	SELECT pk.*,
	       COALESCE((SELECT jsonb_object_agg(severity, n) FROM
	                   (SELECT severity, count(*) AS n FROM package_vulnerabilities v WHERE v.package_key = pk.key GROUP BY severity) s), '{}') AS vulns,
	       (SELECT count(DISTINCT (i.registry, i.repo, i.digest)) FROM image_packages i
	         WHERE i.package_key = pk.key AND i.registry = ANY($1)) AS images,
	       (SELECT count(DISTINCT (x.node, x.namespace, x.pod, x.container)) FROM image_packages i
	          JOIN resolved x ON x.registry = i.registry AND x.repo = i.repo AND x.digest = i.digest
	         WHERE i.package_key = pk.key) AS containers
	  FROM pk
)
SELECT key, name, version, purl, type, vulns, images, containers FROM counted
 ORDER BY containers DESC, images DESC, name, version
 LIMIT $4`, registries, fresh(freshAfter), likePattern(query), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PackageSummary{}
	for rows.Next() {
		var p PackageSummary
		var vj []byte
		if err := rows.Scan(&p.Key, &p.Name, &p.Version, &p.PURL, &p.Type, &vj, &p.Images, &p.Containers); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(vj, &p.Vulnerabilities); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (m *PostgresMetadataStore) SearchVulnerabilities(registries []string, query, severity string, limit int, freshAfter time.Duration) ([]VulnerabilitySummary, error) {
	// The containers are counted for every match at once, from the running
	// images; all the images containing a vulnerability only for the rows
	// shown.
	rows, err := m.pool.Query(context.Background(), `
WITH `+resolvedCTE("$1", "$2")+`,
vs AS (
	SELECT v.vulnerability_id AS id, v.severity, max(v.title) AS title, array_agg(DISTINCT v.package_key) AS keys
	  FROM package_vulnerabilities v
	 WHERE ($3 = '%%' OR v.vulnerability_id ILIKE $3 OR v.title ILIKE $3)
	   AND ($4 = '' OR v.severity = $4)
	   AND EXISTS (SELECT 1 FROM image_packages i WHERE i.package_key = v.package_key AND i.registry = ANY($1))
	 GROUP BY v.vulnerability_id, v.severity
),
rv AS (
	SELECT v.vulnerability_id AS id, v.severity, count(DISTINCT (x.node, x.namespace, x.pod, x.container)) AS containers
	  FROM resolved x
	  JOIN image_packages i ON i.registry = x.registry AND i.repo = x.repo AND i.digest = x.digest
	  JOIN package_vulnerabilities v ON v.package_key = i.package_key
	 WHERE ($3 = '%%' OR v.vulnerability_id ILIKE $3 OR v.title ILIKE $3)
	   AND ($4 = '' OR v.severity = $4)
	 GROUP BY v.vulnerability_id, v.severity
),
top AS (
	SELECT vs.id, vs.severity, vs.title, vs.keys, COALESCE(rv.containers, 0) AS containers
	  FROM vs LEFT JOIN rv ON rv.id = vs.id AND rv.severity = vs.severity
	 ORDER BY 5 DESC, vs.id
	 LIMIT $5
)
SELECT id, severity, title, cardinality(keys),
       (SELECT count(DISTINCT (i.registry, i.repo, i.digest)) FROM image_packages i
         WHERE i.package_key = ANY(top.keys) AND i.registry = ANY($1)),
       containers
  FROM top
 ORDER BY containers DESC, id`, registries, fresh(freshAfter), likePattern(query), severity, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VulnerabilitySummary{}
	for rows.Next() {
		var v VulnerabilitySummary
		if err := rows.Scan(&v.ID, &v.Severity, &v.Title, &v.Packages, &v.Images, &v.Containers); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (m *PostgresMetadataStore) GetVulnerability(id string, registries []string, imageLimit int, freshAfter time.Duration) (*VulnerabilityDetail, error) {
	ctx := context.Background()
	rows, err := m.pool.Query(ctx, `
SELECT p.key, COALESCE(p.package->>'Name', ''), COALESCE(p.package->>'Version', ''), p.purl,
       COALESCE(p.context->>'type', ''), v.fixed, v.severity, v.title
  FROM package_vulnerabilities v JOIN scan_packages p ON p.key = v.package_key
 WHERE v.vulnerability_id = $1
   AND EXISTS (SELECT 1 FROM image_packages i WHERE i.package_key = p.key AND i.registry = ANY($2))
 ORDER BY 2, 3, 1`, id, registries)
	if err != nil {
		return nil, err
	}
	d := &VulnerabilityDetail{ID: id, Packages: []AffectedPackage{}, ImageLimit: imageLimit}
	keys := []string{}
	rank := map[string]int{"Critical": 5, "High": 4, "Medium": 3, "Low": 2, "Unknown": 1}
	for rows.Next() {
		var p AffectedPackage
		var sev, title string
		if err := rows.Scan(&p.Key, &p.Name, &p.Version, &p.PURL, &p.Type, &p.Fixed, &sev, &title); err != nil {
			rows.Close()
			return nil, err
		}
		// A vulnerability can be rated differently by the sources of
		// different packages: the worst rating stands for it.
		if rank[sev] > rank[d.Severity] {
			d.Severity = sev
		}
		if len(title) > len(d.Title) {
			d.Title = title
		}
		d.Packages = append(d.Packages, p)
		keys = append(keys, p.Key)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, ErrNotFound
	}
	if err := m.pool.QueryRow(ctx, `SELECT count(DISTINCT (registry, repo, digest)) FROM image_packages
	                                  WHERE package_key = ANY($1) AND registry = ANY($2)`, keys, registries).Scan(&d.ImageCount); err != nil {
		return nil, err
	}
	irows, err := m.pool.Query(ctx, `
WITH `+resolvedCTE("$1", "$2")+`,
imgs AS (SELECT DISTINCT registry, repo, digest FROM image_packages WHERE package_key = ANY($3) AND registry = ANY($1))
`+findingImagesSQL+`$4`, registries, fresh(freshAfter), keys, imageLimit)
	if err != nil {
		return nil, err
	}
	if d.ImagesOf, err = scanFindingImages(irows); err != nil {
		return nil, err
	}
	return d, nil
}

func (m *PostgresMetadataStore) GetPackage(key string, registries []string, imageLimit int, freshAfter time.Duration) (*PackageDetail, error) {
	ctx := context.Background()
	d := &PackageDetail{ImageLimit: imageLimit}
	var dbAt, matched int64
	err := m.pool.QueryRow(ctx, `
SELECT p.key, COALESCE(p.package->>'Name', ''), COALESCE(p.package->>'Version', ''), p.purl,
       COALESCE(p.context->>'type', ''), p.context, p.database_version, p.database_updated_at, p.vulnerabilities_scanned_at,
       (SELECT count(DISTINCT (i.registry, i.repo, i.digest)) FROM image_packages i
         WHERE i.package_key = p.key AND i.registry = ANY($2))
  FROM scan_packages p WHERE p.key = $1`, key, registries).Scan(
		&d.Key, &d.Name, &d.Version, &d.PURL, &d.Type, &d.Context, &d.DatabaseVersion, &dbAt, &matched, &d.Images)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if d.Images == 0 {
		return nil, ErrNotFound
	}
	d.DatabaseUpdatedAt = time.Unix(0, dbAt).UTC()
	d.MatchedAt = time.Unix(0, matched).UTC()
	vrows, err := m.pool.Query(ctx, `
SELECT vulnerability_id, severity, fixed, title FROM package_vulnerabilities WHERE package_key = $1
 ORDER BY CASE severity WHEN 'Critical' THEN 0 WHEN 'High' THEN 1 WHEN 'Medium' THEN 2 WHEN 'Low' THEN 3 ELSE 4 END, vulnerability_id`, key)
	if err != nil {
		return nil, err
	}
	d.Matched = []PackageVulnerability{}
	d.Vulnerabilities = map[string]int{}
	for vrows.Next() {
		var v PackageVulnerability
		if err := vrows.Scan(&v.ID, &v.Severity, &v.Fixed, &v.Title); err != nil {
			vrows.Close()
			return nil, err
		}
		d.Matched = append(d.Matched, v)
		d.Vulnerabilities[v.Severity]++
	}
	vrows.Close()
	if err := vrows.Err(); err != nil {
		return nil, err
	}
	irows, err := m.pool.Query(ctx, `
WITH `+resolvedCTE("$1", "$2")+`,
imgs AS (SELECT DISTINCT registry, repo, digest FROM image_packages WHERE package_key = $3 AND registry = ANY($1))
`+findingImagesSQL+`$4`, registries, fresh(freshAfter), key, imageLimit)
	if err != nil {
		return nil, err
	}
	if d.ImagesOf, err = scanFindingImages(irows); err != nil {
		return nil, err
	}
	for _, img := range d.ImagesOf {
		d.Containers += len(img.Placements)
	}
	return d, nil
}

func (m *PostgresMetadataStore) GetImageFindings(t ScanTarget, freshAfter time.Duration) (*ImageFindings, error) {
	ctx := context.Background()
	f := &ImageFindings{ScanTarget: t, Counts: map[string]int{}, SecretCounts: map[string]int{},
		Packages: []ImagePackage{}, Secrets: json.RawMessage("[]"), Placements: []Placement{}}

	var analyzer, sbom string
	var secrets, secretCounts []byte
	var at int64
	err := m.pool.QueryRow(ctx, `SELECT analyzer, sbom_manifest, secrets, secret_counts, scanned_at FROM image_scans
	                              WHERE registry = $1 AND repo = $2 AND digest = $3`,
		t.Registry, t.Repo, t.Digest.String()).Scan(&analyzer, &sbom, &secrets, &secretCounts, &at)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		var fl ScanFailure
		var failedAt int64
		ferr := m.pool.QueryRow(ctx, `SELECT analyzer, error, failed_at FROM image_scan_failures
		                               WHERE registry = $1 AND repo = $2 AND digest = $3`,
			t.Registry, t.Repo, t.Digest.String()).Scan(&fl.Analyzer, &fl.Error, &failedAt)
		if ferr == nil {
			fl.ScanTarget = t
			fl.FailedAt = time.Unix(0, failedAt).UTC()
			f.Failure = &fl
		} else if !errors.Is(ferr, pgx.ErrNoRows) {
			return nil, ferr
		}
	case err != nil:
		return nil, err
	default:
		f.Scanned = true
		f.Analyzer = analyzer
		ts := time.Unix(0, at).UTC()
		f.ScannedAt = &ts
		f.SBOMManifest = sbom
		f.Secrets = secrets
		if err := json.Unmarshal(secretCounts, &f.SecretCounts); err != nil {
			return nil, err
		}
		rows, err := m.pool.Query(ctx, `
SELECT p.key, COALESCE(p.package->>'Name', ''), COALESCE(p.package->>'Version', ''), p.purl,
       COALESCE(p.context->>'type', ''), array_agg(DISTINCT i.target ORDER BY i.target),
       COALESCE((SELECT json_agg(json_build_object('id', v.vulnerability_id, 'severity', v.severity, 'fixed', v.fixed, 'title', v.title)
                                 ORDER BY v.vulnerability_id)
                   FROM package_vulnerabilities v WHERE v.package_key = p.key), '[]')
  FROM image_packages i JOIN scan_packages p ON p.key = i.package_key
 WHERE i.registry = $1 AND i.repo = $2 AND i.digest = $3
 GROUP BY p.key
 ORDER BY 2, 3`, t.Registry, t.Repo, t.Digest.String())
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var p ImagePackage
			var vj []byte
			if err := rows.Scan(&p.Key, &p.Name, &p.Version, &p.PURL, &p.Type, &p.Targets, &vj); err != nil {
				rows.Close()
				return nil, err
			}
			if err := json.Unmarshal(vj, &p.Vulnerabilities); err != nil {
				rows.Close()
				return nil, err
			}
			for _, v := range p.Vulnerabilities {
				f.Counts[v.Severity]++
			}
			f.Packages = append(f.Packages, p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	prows, err := m.pool.Query(ctx, `
WITH `+resolvedCTE("$1", "$2")+`
SELECT node, namespace, pod, container, image, pulled, platform FROM resolved
 WHERE registry = $3 AND repo = $4 AND digest = $5
 ORDER BY namespace, pod, container`, []string{t.Registry}, fresh(freshAfter), t.Registry, t.Repo, t.Digest.String())
	if err != nil {
		return nil, err
	}
	defer prows.Close()
	for prows.Next() {
		var p Placement
		var pulled string
		if err := prows.Scan(&p.Node, &p.Namespace, &p.Pod, &p.Container, &p.Image, &pulled, &p.Platform); err != nil {
			return nil, err
		}
		p.Digest = digest.Digest(pulled)
		f.Placements = append(f.Placements, p)
	}
	return f, prows.Err()
}

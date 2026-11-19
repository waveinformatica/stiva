package storage

import (
	"context"
	"encoding/json"
	"time"

	"registry/internal/digest"
)

// Placement is a container running an image, as the pre-warm agent of its
// node reports it. Digest is what the node resolved (an index digest for a
// multi-platform tag); Platform says which entry of an index runs.
type Placement struct {
	Node      string        `json:"node"`
	Namespace string        `json:"namespace"`
	Pod       string        `json:"pod"`
	Container string        `json:"container"`
	Image     string        `json:"image"`
	Digest    digest.Digest `json:"digest"`
	Platform  string        `json:"platform"`
}

// VulnerabilitySet is a set of findings of one severity shared by running
// images: the same (package, vulnerability) pairs. Images built from the same
// base with the same vulnerabilities share a set; it is the unit of alerting.
type VulnerabilitySet struct {
	Set             string   `json:"set"`
	Severity        string   `json:"severity"`
	Packages        []string `json:"packages"` // package names, sorted
	Vulnerabilities int      `json:"vulnerabilities"`
	Images          int      `json:"images"`     // distinct image manifests running it
	Containers      int      `json:"containers"` // running containers
	Namespaces      []string `json:"namespaces"`
}

// SetPackage is a package of a set with the set's vulnerabilities on it.
type SetPackage struct {
	PURL            string                 `json:"purl"`
	Name            string                 `json:"name"`
	Version         string                 `json:"version"`
	Vulnerabilities []PackageVulnerability `json:"vulnerabilities"`
}

// SetImage is an image manifest that carries a set, with where it runs.
type SetImage struct {
	ScanTarget
	Placements []Placement `json:"placements"`
}

// VulnerabilitySetDetail is one set in full.
type VulnerabilitySetDetail struct {
	VulnerabilitySet
	Detail   []SetPackage `json:"detail"`
	ImagesOf []SetImage   `json:"images_of"`
}

// FindingsStore answers where images run and which vulnerability sets they
// share (docs/scanning.md, "Vulnerability sets").
type FindingsStore interface {
	// ReplacePlacements replaces what a node runs.
	ReplacePlacements(node string, ps []Placement, at time.Time) error
	// VulnerabilitySets lists the sets of the given severities that run, in
	// the given registries; placements older than freshAfter are ignored.
	VulnerabilitySets(registries, severities []string, freshAfter time.Duration) ([]VulnerabilitySet, error)
	// GetVulnerabilitySet returns one set, ErrNotFound when nothing runs it.
	GetVulnerabilitySet(set, severity string, registries []string, freshAfter time.Duration) (*VulnerabilitySetDetail, error)
}

func (m *PostgresMetadataStore) ReplacePlacements(node string, ps []Placement, at time.Time) error {
	ctx := context.Background()
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM running_placements WHERE node = $1`, node); err != nil {
		return err
	}
	for _, p := range ps {
		if _, err := tx.Exec(ctx,
			`INSERT INTO running_placements(node, namespace, pod, container, image, digest, platform, seen_at)
			 VALUES($1,$2,$3,$4,$5,$6,$7,$8)
			 ON CONFLICT (node, namespace, pod, container) DO UPDATE SET
			        image = EXCLUDED.image, digest = EXCLUDED.digest, platform = EXCLUDED.platform, seen_at = EXCLUDED.seen_at`,
			node, p.Namespace, p.Pod, p.Container, p.Image, p.Digest.String(), p.Platform, at.UnixNano()); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// setsCTE resolves running containers onto scanned image manifests and the
// sets those carry. $1 registries, $2 severities, $3 oldest seen_at.
//   - running: fresh placements.
//   - resolved: each placement onto the image manifest(s) it runs: the digest
//     itself when it is an image, the index entry of the node's platform when
//     it is an index (in every registry and repository holding it).
//   - image_sets: per scanned image and severity, the md5 of the sorted
//     (package key, vulnerability) pairs: the set.
const setsCTE = `
WITH running AS (
	SELECT * FROM running_placements WHERE seen_at >= $3
),
resolved AS (
	SELECT r.node, r.namespace, r.pod, r.container, r.image, r.digest AS pulled, r.platform,
	       m.registry, m.repo, m.digest
	  FROM running r JOIN manifests m ON m.digest = r.digest AND m.image
	 WHERE m.registry = ANY($1)
	UNION
	SELECT r.node, r.namespace, r.pod, r.container, r.image, r.digest, r.platform,
	       m.registry, m.repo, e->>'digest'
	  FROM running r
	  JOIN manifests m ON m.digest = r.digest
	       AND m.media_type IN ('application/vnd.oci.image.index.v1+json',
	                            'application/vnd.docker.distribution.manifest.list.v2+json')
	 CROSS JOIN LATERAL jsonb_array_elements(convert_from(m.content, 'UTF8')::jsonb->'manifests') e
	 WHERE m.registry = ANY($1)
	   AND concat_ws('/', e->'platform'->>'os', e->'platform'->>'architecture') = r.platform
),
image_sets AS (
	SELECT i.registry, i.repo, i.digest, v.severity,
	       md5(string_agg(DISTINCT i.package_key || ' ' || v.vulnerability_id, ',' ORDER BY i.package_key || ' ' || v.vulnerability_id)) AS set
	  FROM image_packages i JOIN package_vulnerabilities v ON v.package_key = i.package_key
	 WHERE i.registry = ANY($1) AND v.severity = ANY($2)
	   AND EXISTS (SELECT 1 FROM resolved x WHERE x.registry = i.registry AND x.repo = i.repo AND x.digest = i.digest)
	 GROUP BY i.registry, i.repo, i.digest, v.severity
),
running_sets AS (
	SELECT s.set, s.severity, s.registry, s.repo, s.digest, x.node, x.namespace, x.pod, x.container, x.image, x.pulled, x.platform
	  FROM image_sets s JOIN resolved x ON x.registry = s.registry AND x.repo = s.repo AND x.digest = s.digest
)`

func (m *PostgresMetadataStore) VulnerabilitySets(registries, severities []string, freshAfter time.Duration) ([]VulnerabilitySet, error) {
	rows, err := m.pool.Query(context.Background(), setsCTE+`,
set_packages AS (
	SELECT DISTINCT s.set, s.severity, p.package->>'Name' AS name, v.vulnerability_id
	  FROM (SELECT DISTINCT set, severity, registry, repo, digest FROM running_sets) s
	  JOIN image_packages i ON i.registry = s.registry AND i.repo = s.repo AND i.digest = s.digest
	  JOIN package_vulnerabilities v ON v.package_key = i.package_key AND v.severity = s.severity
	  JOIN scan_packages p ON p.key = i.package_key
)
SELECT r.set, r.severity,
       (SELECT array_agg(DISTINCT name ORDER BY name) FROM set_packages sp WHERE sp.set = r.set AND sp.severity = r.severity),
       (SELECT count(DISTINCT vulnerability_id) FROM set_packages sp WHERE sp.set = r.set AND sp.severity = r.severity),
       count(DISTINCT (r.registry, r.repo, r.digest)),
       count(DISTINCT (r.node, r.namespace, r.pod, r.container)),
       array_agg(DISTINCT r.namespace ORDER BY r.namespace)
  FROM running_sets r
 GROUP BY r.set, r.severity
 ORDER BY r.severity, r.set`,
		registries, severities, time.Now().Add(-freshAfter).UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VulnerabilitySet{}
	for rows.Next() {
		var s VulnerabilitySet
		if err := rows.Scan(&s.Set, &s.Severity, &s.Packages, &s.Vulnerabilities, &s.Images, &s.Containers, &s.Namespaces); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (m *PostgresMetadataStore) GetVulnerabilitySet(set, severity string, registries []string, freshAfter time.Duration) (*VulnerabilitySetDetail, error) {
	sets, err := m.VulnerabilitySets(registries, []string{severity}, freshAfter)
	if err != nil {
		return nil, err
	}
	var d *VulnerabilitySetDetail
	for _, s := range sets {
		if s.Set == set {
			d = &VulnerabilitySetDetail{VulnerabilitySet: s}
		}
	}
	if d == nil {
		return nil, ErrNotFound
	}
	ctx := context.Background()
	fresh := time.Now().Add(-freshAfter).UnixNano()
	// Images and where they run.
	rows, err := m.pool.Query(ctx, setsCTE+`
SELECT registry, repo, digest, node, namespace, pod, container, image, pulled, platform
  FROM running_sets WHERE set = $4 AND severity = $5
 ORDER BY registry, repo, digest, namespace, pod, container`,
		registries, []string{severity}, fresh, set, severity)
	if err != nil {
		return nil, err
	}
	byImage := map[ScanTarget]int{}
	for rows.Next() {
		var t ScanTarget
		var p Placement
		var dg, pulled string
		if err := rows.Scan(&t.Registry, &t.Repo, &dg, &p.Node, &p.Namespace, &p.Pod, &p.Container, &p.Image, &pulled, &p.Platform); err != nil {
			rows.Close()
			return nil, err
		}
		t.Digest = digest.Digest(dg)
		p.Digest = digest.Digest(pulled)
		i, ok := byImage[t]
		if !ok {
			i = len(d.ImagesOf)
			byImage[t] = i
			d.ImagesOf = append(d.ImagesOf, SetImage{ScanTarget: t})
		}
		d.ImagesOf[i].Placements = append(d.ImagesOf[i].Placements, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(d.ImagesOf) == 0 {
		return nil, ErrNotFound
	}
	// The packages and vulnerabilities, from one of the images: they all
	// carry the same set.
	first := d.ImagesOf[0].ScanTarget
	prow, err := m.pool.Query(ctx,
		`SELECT p.purl, COALESCE(p.package->>'Name', ''), COALESCE(p.package->>'Version', ''),
		        json_agg(json_build_object('id', v.vulnerability_id, 'severity', v.severity, 'fixed', v.fixed, 'title', v.title)
		                 ORDER BY v.vulnerability_id)
		   FROM image_packages i
		   JOIN scan_packages p ON p.key = i.package_key
		   JOIN package_vulnerabilities v ON v.package_key = i.package_key AND v.severity = $4
		  WHERE i.registry = $1 AND i.repo = $2 AND i.digest = $3
		  GROUP BY p.key, p.purl, p.package->>'Name', p.package->>'Version'
		  ORDER BY 2, 3`,
		first.Registry, first.Repo, first.Digest.String(), severity)
	if err != nil {
		return nil, err
	}
	defer prow.Close()
	for prow.Next() {
		var sp SetPackage
		var vj []byte
		if err := prow.Scan(&sp.PURL, &sp.Name, &sp.Version, &vj); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(vj, &sp.Vulnerabilities); err != nil {
			return nil, err
		}
		d.Detail = append(d.Detail, sp)
	}
	return d, prow.Err()
}

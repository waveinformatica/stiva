package storage

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
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

// SetSeverities are the severities that make a package part of an image's
// vulnerability set: what alerts are raised on.
var SetSeverities = []string{"Critical", "High"}

// VulnerabilitySet is the set of packages (each at its version) of an image
// that have Critical or High vulnerabilities. Images built from the same base
// with the same vulnerable packages share it: it is the unit of alerting, and
// a new vulnerability on the same packages does not change it. Severity is
// the highest of its vulnerabilities; Counts has the number of distinct
// vulnerabilities per severity.
type VulnerabilitySet struct {
	Set             string         `json:"set"`
	Severity        string         `json:"severity"`
	Packages        []string       `json:"packages"` // package names, sorted
	Vulnerabilities int            `json:"vulnerabilities"`
	Counts          map[string]int `json:"counts"`
	Images          int            `json:"images"`     // distinct image manifests running it
	Containers      int            `json:"containers"` // running containers
	Namespaces      []string       `json:"namespaces"`
}

// SetPackage is a package of a set with its Critical and High vulnerabilities.
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
	// VulnerabilitySets lists the sets that run, in the given registries;
	// placements older than freshAfter are ignored.
	VulnerabilitySets(registries []string, freshAfter time.Duration) ([]VulnerabilitySet, error)
	// GetVulnerabilitySet returns one set, ErrNotFound when nothing runs it.
	GetVulnerabilitySet(set string, registries []string, freshAfter time.Duration) (*VulnerabilitySetDetail, error)
	// What the interface browses (explore.go).
	ExploreStore
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
// sets those carry. $1 registries, $2 SetSeverities, $3 oldest seen_at.
//   - running: fresh placements.
//   - resolved: each placement onto the image manifest(s) it runs: the digest
//     itself when it is an image, the index entry of the node's platform when
//     it is an index (in every registry and repository holding it).
//   - image_sets: per scanned image, the md5 of the sorted keys of its
//     packages with a Critical or High vulnerability (the set), and the
//     highest severity among them.
//
// resolvedCTE defines running and resolved (above), with the registries and
// the freshness bound as the given query parameters.
func resolvedCTE(registries, fresh string) string {
	return `running AS (
	SELECT * FROM running_placements WHERE seen_at >= ` + fresh + `
),
resolved AS MATERIALIZED (
	SELECT r.node, r.namespace, r.pod, r.container, r.image, r.digest AS pulled, r.platform,
	       m.registry, m.repo, m.digest
	  FROM running r JOIN manifests m ON m.digest = r.digest AND m.image
	 WHERE m.registry = ANY(` + registries + `)
	UNION
	SELECT r.node, r.namespace, r.pod, r.container, r.image, r.digest, r.platform,
	       m.registry, m.repo, e->>'digest'
	  FROM running r
	  JOIN manifests m ON m.digest = r.digest
	       AND m.media_type IN ('application/vnd.oci.image.index.v1+json',
	                            'application/vnd.docker.distribution.manifest.list.v2+json')
	 CROSS JOIN LATERAL jsonb_array_elements(convert_from(m.content, 'UTF8')::jsonb->'manifests') e
	 WHERE m.registry = ANY(` + registries + `)
	   AND concat_ws('/', e->'platform'->>'os', e->'platform'->>'architecture') = r.platform
)`
}

var setsCTE = `
WITH ` + resolvedCTE("$1", "$3") + `,
image_sets AS (
	SELECT i.registry, i.repo, i.digest,
	       md5(string_agg(DISTINCT i.package_key, ',' ORDER BY i.package_key)) AS set,
	       CASE WHEN bool_or(v.severity = 'Critical') THEN 'Critical' ELSE 'High' END AS severity
	  FROM image_packages i JOIN package_vulnerabilities v ON v.package_key = i.package_key
	 WHERE i.registry = ANY($1) AND v.severity = ANY($2)
	   AND EXISTS (SELECT 1 FROM resolved x WHERE x.registry = i.registry AND x.repo = i.repo AND x.digest = i.digest)
	 GROUP BY i.registry, i.repo, i.digest
),
running_sets AS (
	SELECT s.set, s.severity, s.registry, s.repo, s.digest, x.node, x.namespace, x.pod, x.container, x.image, x.pulled, x.platform
	  FROM image_sets s JOIN resolved x ON x.registry = s.registry AND x.repo = s.repo AND x.digest = s.digest
)`

func (m *PostgresMetadataStore) VulnerabilitySets(registries []string, freshAfter time.Duration) ([]VulnerabilitySet, error) {
	// The packages and vulnerabilities of a set are those of any one of its
	// images: they all have the same vulnerable packages.
	rows, err := m.pool.Query(context.Background(), setsCTE+`,
one_image AS (
	SELECT DISTINCT ON (set) set, registry, repo, digest FROM running_sets ORDER BY set, registry, repo, digest
),
set_vulns AS (
	SELECT DISTINCT o.set, p.package->>'Name' AS name, v.vulnerability_id, v.severity
	  FROM one_image o
	  JOIN image_packages i ON i.registry = o.registry AND i.repo = o.repo AND i.digest = o.digest
	  JOIN package_vulnerabilities v ON v.package_key = i.package_key AND v.severity = ANY($2)
	  JOIN scan_packages p ON p.key = i.package_key
)
SELECT r.set, min(r.severity),
       (SELECT array_agg(DISTINCT name ORDER BY name) FROM set_vulns sv WHERE sv.set = r.set),
       (SELECT count(DISTINCT vulnerability_id) FROM set_vulns sv WHERE sv.set = r.set),
       (SELECT jsonb_object_agg(severity, n) FROM
          (SELECT severity, count(DISTINCT vulnerability_id) AS n FROM set_vulns sv WHERE sv.set = r.set GROUP BY severity) c),
       count(DISTINCT (r.registry, r.repo, r.digest)),
       count(DISTINCT (r.node, r.namespace, r.pod, r.container)),
       array_agg(DISTINCT r.namespace ORDER BY r.namespace)
  FROM running_sets r
 GROUP BY r.set
 ORDER BY r.set`,
		registries, SetSeverities, time.Now().Add(-freshAfter).UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VulnerabilitySet{}
	for rows.Next() {
		var s VulnerabilitySet
		var counts []byte
		if err := rows.Scan(&s.Set, &s.Severity, &s.Packages, &s.Vulnerabilities, &counts, &s.Images, &s.Containers, &s.Namespaces); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(counts, &s.Counts); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (m *PostgresMetadataStore) GetVulnerabilitySet(set string, registries []string, freshAfter time.Duration) (*VulnerabilitySetDetail, error) {
	// One set only: the summary is counted from its own rows, not from the
	// list of every set (which costs as much as all of them).
	d := &VulnerabilitySetDetail{VulnerabilitySet: VulnerabilitySet{Set: set, Counts: map[string]int{}}}
	ctx := context.Background()
	fresh := time.Now().Add(-freshAfter).UnixNano()
	// Images and where they run.
	rows, err := m.pool.Query(ctx, setsCTE+`
SELECT registry, repo, digest, node, namespace, pod, container, image, pulled, platform
  FROM running_sets WHERE set = $4
 ORDER BY registry, repo, digest, namespace, pod, container`,
		registries, SetSeverities, fresh, set)
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
	namespaces := map[string]bool{}
	for _, img := range d.ImagesOf {
		d.Containers += len(img.Placements)
		for _, p := range img.Placements {
			namespaces[p.Namespace] = true
		}
	}
	d.Images = len(d.ImagesOf)
	for ns := range namespaces {
		d.Namespaces = append(d.Namespaces, ns)
	}
	sort.Strings(d.Namespaces)
	// The packages and vulnerabilities, from one of the images: they all
	// carry the same set.
	first := d.ImagesOf[0].ScanTarget
	prow, err := m.pool.Query(ctx,
		`SELECT p.purl, COALESCE(p.package->>'Name', ''), COALESCE(p.package->>'Version', ''),
		        json_agg(json_build_object('id', v.vulnerability_id, 'severity', v.severity, 'fixed', v.fixed, 'title', v.title)
		                 ORDER BY v.vulnerability_id)
		   FROM (SELECT DISTINCT package_key FROM image_packages
		          WHERE registry = $1 AND repo = $2 AND digest = $3) i
		   JOIN scan_packages p ON p.key = i.package_key
		   JOIN package_vulnerabilities v ON v.package_key = i.package_key AND v.severity = ANY($4)
		  GROUP BY p.key, p.purl, p.package->>'Name', p.package->>'Version'
		  ORDER BY 2, 3`,
		first.Registry, first.Repo, first.Digest.String(), SetSeverities)
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
	if err := prow.Err(); err != nil {
		return nil, err
	}
	names := map[string]bool{}
	ids := map[string]bool{}
	bySeverity := map[string]map[string]bool{}
	for _, p := range d.Detail {
		names[p.Name] = true
		for _, v := range p.Vulnerabilities {
			ids[v.ID] = true
			if bySeverity[v.Severity] == nil {
				bySeverity[v.Severity] = map[string]bool{}
			}
			bySeverity[v.Severity][v.ID] = true
		}
	}
	for sev, set := range bySeverity {
		d.Counts[sev] = len(set)
	}
	d.Severity = "High"
	if d.Counts["Critical"] > 0 {
		d.Severity = "Critical"
	}
	for n := range names {
		d.Packages = append(d.Packages, n)
	}
	// As the list orders them (the database collation ignores case).
	sort.Slice(d.Packages, func(i, j int) bool { return strings.ToLower(d.Packages[i]) < strings.ToLower(d.Packages[j]) })
	d.Vulnerabilities = len(ids)
	return d, nil
}

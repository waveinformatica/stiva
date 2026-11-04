package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"registry/internal/digest"
	"registry/internal/storage"
)

// CleanupPolicy is a per-registry retention policy for OCI tags.
//
// A tag is kept when ANY rule keeps it: it matches a Protect pattern, it is
// among the KeepLast most recently pushed tags of its repository, or it was
// pushed within the last MaxAgeDays days. Everything else is deleted. With
// neither KeepLast nor MaxAgeDays set the policy keeps everything, so an empty
// policy can never delete by accident.
type CleanupPolicy struct {
	Enabled    bool     `json:"enabled"`
	KeepLast   int      `json:"keep_last"`    // newest N tags per repository (0 = rule off)
	MaxAgeDays int      `json:"max_age_days"` // keep tags pushed within N days (0 = rule off)
	Protect    []string `json:"protect"`      // glob patterns of tags never deleted (path.Match)
}

// Validate rejects values that could not mean what the operator intended.
func (p *CleanupPolicy) Validate() error {
	if p == nil {
		return nil
	}
	if p.KeepLast < 0 || p.MaxAgeDays < 0 {
		return fmt.Errorf("cleanup: keep_last and max_age_days must not be negative")
	}
	for _, pat := range p.Protect {
		if _, err := path.Match(pat, ""); err != nil {
			return fmt.Errorf("cleanup: invalid protect pattern %q: %w", pat, err)
		}
	}
	return nil
}

// Active reports whether the policy can delete anything at all.
func (p *CleanupPolicy) Active() bool {
	return p != nil && p.Enabled && (p.KeepLast > 0 || p.MaxAgeDays > 0)
}

// attachmentTag matches cosign/OCI attachment tags (sha256-<hex>.sig, .att,
// .sbom). They are tied to their subject image, so retention leaves them alone.
func attachmentTag(tag string) bool {
	if !strings.HasPrefix(tag, "sha256-") {
		return false
	}
	for _, suf := range []string{".sig", ".att", ".sbom"} {
		if strings.HasSuffix(tag, suf) {
			return true
		}
	}
	return false
}

func (p *CleanupPolicy) protectedAny(names ...string) bool {
	for _, n := range names {
		for _, pat := range p.Protect {
			if ok, _ := path.Match(pat, n); ok {
				return true
			}
		}
	}
	return false
}

func (p *CleanupPolicy) protected(tag string) bool {
	if attachmentTag(tag) {
		return true
	}
	for _, pat := range p.Protect {
		if ok, _ := path.Match(pat, tag); ok {
			return true
		}
	}
	return false
}

// retentionItem is one candidate (a tag, or a package version) for retention.
type retentionItem struct {
	pushedAt  int64
	protected bool
}

// planRetention applies the policy to the items of ONE group (a repository's
// tags, one package's versions) and returns the indexes to delete. Pure.
// Items with an unknown time (0) count as recent for the age rule so legacy
// rows are never deleted for lack of data, and rank last for keep-last.
func planRetention(items []retentionItem, p *CleanupPolicy, now time.Time) []int {
	if !p.Active() {
		return nil
	}
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return items[order[a]].pushedAt > items[order[b]].pushedAt })
	var cutoff int64
	if p.MaxAgeDays > 0 {
		cutoff = now.Add(-time.Duration(p.MaxAgeDays) * 24 * time.Hour).UnixNano()
	}
	var del []int
	rank := 0
	for _, i := range order {
		it := items[i]
		if it.protected {
			continue
		}
		rank++
		if p.KeepLast > 0 && rank <= p.KeepLast {
			continue
		}
		if p.MaxAgeDays > 0 && (it.pushedAt == 0 || it.pushedAt >= cutoff) {
			continue
		}
		del = append(del, i)
	}
	return del
}

// PlanCleanup returns the tags of one repository the policy would delete.
func PlanCleanup(tags []storage.TagInfo, p *CleanupPolicy, now time.Time) []storage.TagInfo {
	items := make([]retentionItem, len(tags))
	for i, t := range tags {
		items[i] = retentionItem{pushedAt: t.PushedAt, protected: p != nil && p.protected(t.Tag)}
	}
	var out []storage.TagInfo
	for _, i := range planRetention(items, p, now) {
		out = append(out, tags[i])
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].PushedAt > out[b].PushedAt })
	return out
}

// ArtifactUnit is one version of one package: every stored file that belongs
// to it, so a version is kept or removed as a whole.
type ArtifactUnit struct {
	Package   string
	Version   string
	Paths     []string
	Size      int64
	CreatedAt int64 // newest file of the version (UnixNano)
}

// GroupArtifacts folds stored objects into package versions. Index and
// metadata files are left out: they are never candidates.
func GroupArtifacts(format Format, objs []storage.ObjectInfo) []ArtifactUnit {
	idx := map[string]*ArtifactUnit{}
	var order []string
	for _, o := range objs {
		pkg, ver, ok := ParsePackagePath(format, o.Path)
		if !ok {
			continue
		}
		k := pkg + "\x00" + ver
		u := idx[k]
		if u == nil {
			u = &ArtifactUnit{Package: pkg, Version: ver}
			idx[k] = u
			order = append(order, k)
		}
		u.Paths = append(u.Paths, o.Path)
		u.Size += o.Size
		if o.CreatedAt > u.CreatedAt {
			u.CreatedAt = o.CreatedAt
		}
	}
	out := make([]ArtifactUnit, 0, len(order))
	for _, k := range order {
		out = append(out, *idx[k])
	}
	return out
}

// PlanArtifactCleanup returns the versions the policy would delete, deciding
// separately for each package. A Protect pattern matches a version ("1.*") or
// "package@version".
func PlanArtifactCleanup(units []ArtifactUnit, p *CleanupPolicy, now time.Time) []ArtifactUnit {
	if !p.Active() {
		return nil
	}
	byPkg := map[string][]ArtifactUnit{}
	var pkgs []string
	for _, u := range units {
		if _, ok := byPkg[u.Package]; !ok {
			pkgs = append(pkgs, u.Package)
		}
		byPkg[u.Package] = append(byPkg[u.Package], u)
	}
	sort.Strings(pkgs)
	var out []ArtifactUnit
	for _, pkg := range pkgs {
		vs := byPkg[pkg]
		items := make([]retentionItem, len(vs))
		for i, u := range vs {
			items[i] = retentionItem{
				pushedAt:  u.CreatedAt,
				protected: p.protectedAny(u.Version, u.Package+"@"+u.Version),
			}
		}
		for _, i := range planRetention(items, p, now) {
			out = append(out, vs[i])
		}
	}
	return out
}

// CleanupRepoReport is the outcome for one repository.
type CleanupRepoReport struct {
	Repo      string   `json:"repo"`
	Kept      int      `json:"kept"`
	Tags      []string `json:"tags"`            // tags deleted (or to delete on a dry run)
	Manifests int      `json:"manifests"`       // manifests pruned after losing their last tag
	Files     int      `json:"files,omitempty"` // stored files removed (non-OCI formats)
	Bytes     int64    `json:"bytes,omitempty"` // their total size
}

// CleanupReport is the outcome of a cleanup run for one registry.
type CleanupReport struct {
	Registry  string              `json:"registry"`
	DryRun    bool                `json:"dry_run"`
	Repos     []CleanupRepoReport `json:"repos"`
	Unit      string              `json:"unit"`      // "tags" (OCI) or "versions" (other formats)
	TagCount  int                 `json:"tag_count"` // tags or versions deleted
	Manifests int                 `json:"manifest_count"`
	Files     int                 `json:"file_count,omitempty"`
	Bytes     int64               `json:"bytes,omitempty"`
	Errors    []string            `json:"errors,omitempty"`
}

// Cleanup applies the registry's policy. Deleting a tag never deletes content
// still in use: a manifest is pruned only when no tag points at it and no
// remaining manifest of the repository lists it as a child (multi-arch
// indexes, attestations). Blobs freed this way are reclaimed by garbage
// collection, which stays a separate, explicit step.
func (m *Manager) Cleanup(ctx context.Context, name string, dryRun bool) (*CleanupReport, error) {
	reg, ok := m.Get(name)
	if !ok {
		return nil, fmt.Errorf("registry %q not found", name)
	}
	if err := CleanupSupported(reg); err != nil {
		return nil, err
	}
	if !reg.Cleanup.Active() {
		return nil, fmt.Errorf("no active cleanup policy: enable it and set keep_last or max_age_days")
	}
	if reg.Format != string(FormatDocker) {
		return m.cleanupArtifacts(ctx, reg, dryRun)
	}
	name = reg.Name
	report := &CleanupReport{Registry: name, DryRun: dryRun, Unit: "tags", Repos: []CleanupRepoReport{}}
	repos, err := m.meta.ListRepos(name)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for _, repo := range repos {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		infos, err := m.meta.ListTagInfos(name, repo)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("%s: %v", repo, err))
			continue
		}
		plan := PlanCleanup(infos, reg.Cleanup, now)
		if len(plan) == 0 {
			continue
		}
		rr := CleanupRepoReport{Repo: repo, Kept: len(infos) - len(plan), Tags: []string{}}
		deleted := map[string]bool{}
		var candidates []string
		for _, t := range plan {
			if !dryRun {
				if err := m.meta.DeleteTag(name, repo, t.Tag); err != nil {
					report.Errors = append(report.Errors, fmt.Sprintf("%s:%s: %v", repo, t.Tag, err))
					continue
				}
			}
			deleted[t.Tag] = true
			rr.Tags = append(rr.Tags, t.Tag)
			candidates = append(candidates, t.Digest)
		}
		rr.Manifests = m.pruneManifests(name, repo, candidates, deleted, dryRun, report)
		report.Repos = append(report.Repos, rr)
		report.TagCount += len(rr.Tags)
		report.Manifests += rr.Manifests
	}
	return report, nil
}

// pruneManifests deletes the candidate manifests that lost their last tag and
// are not a child of a surviving manifest. On a dry run, deleted holds the
// tags that would go, so the answer matches what a real run would do.
func (m *Manager) pruneManifests(name, repo string, candidates []string, deleted map[string]bool, dryRun bool, report *CleanupReport) int {
	remaining, err := m.meta.ListTagInfos(name, repo)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("%s: %v", repo, err))
		return 0
	}
	tagged := map[string]bool{}
	for _, t := range remaining {
		if !deleted[t.Tag] {
			tagged[t.Digest] = true
		}
	}
	// Digests that must stay: tagged ones and everything reachable from them.
	keep := map[string]bool{}
	var mark func(d string, depth int)
	mark = func(d string, depth int) {
		if keep[d] || depth > 4 {
			return
		}
		keep[d] = true
		dg, err := digest.Parse(d)
		if err != nil {
			return
		}
		content, _, err := m.meta.GetManifest(name, repo, dg)
		if err != nil {
			return
		}
		var idx struct {
			Manifests []struct {
				Digest string `json:"digest"`
			} `json:"manifests"`
		}
		if json.Unmarshal(content, &idx) == nil {
			for _, c := range idx.Manifests {
				mark(c.Digest, depth+1)
			}
		}
	}
	for d := range tagged {
		mark(d, 0)
	}
	// Only digests that just lost their tag are pruned, plus the children an
	// index loses with them (platform manifests, attestations), so their blobs
	// become collectable. Manifests pushed by digest are never touched.
	children := func(d string) []string {
		dg, err := digest.Parse(d)
		if err != nil {
			return nil
		}
		content, _, err := m.meta.GetManifest(name, repo, dg)
		if err != nil {
			return nil
		}
		var idx struct {
			Manifests []struct {
				Digest string `json:"digest"`
			} `json:"manifests"`
		}
		var out []string
		if json.Unmarshal(content, &idx) == nil {
			for _, c := range idx.Manifests {
				out = append(out, c.Digest)
			}
		}
		return out
	}
	expanded := append([]string(nil), candidates...)
	for i := 0; i < len(expanded) && i < 10000; i++ {
		expanded = append(expanded, children(expanded[i])...)
	}
	candidates = expanded
	pruned := 0
	seen := map[string]bool{}
	for _, d := range candidates {
		if seen[d] || keep[d] {
			continue
		}
		seen[d] = true
		dg, err := digest.Parse(d)
		if err != nil {
			continue
		}
		if !dryRun {
			if err := m.meta.DeleteManifest(name, repo, dg); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("%s@%s: %v", repo, d, err))
				continue
			}
		}
		pruned++
	}
	return pruned
}

// CleanupSupported reports whether a retention policy can apply to the registry:
// hosted OCI registries (tags) and hosted or caching registries of every other
// format (package versions). Groups hold no content of their own.
func CleanupSupported(reg *Registry) error {
	switch Type(reg.Type) {
	case TypeHosted:
		return nil
	case TypeProxy, TypeCache:
		if reg.Format != string(FormatDocker) {
			return nil
		}
	}
	return fmt.Errorf("cleanup policies apply to hosted registries, and to proxy registries of non-OCI formats")
}

// cleanupArtifacts applies the policy to a path-addressed registry: package
// versions, newest first by last write. Files go; the blobs they pointed at
// are reclaimed by garbage collection, since content may be shared.
func (m *Manager) cleanupArtifacts(ctx context.Context, reg *Registry, dryRun bool) (*CleanupReport, error) {
	st := m.StoreFor(reg.Name)
	if st == nil {
		return nil, fmt.Errorf("registry %q has no local store", reg.Name)
	}
	objs, err := st.ListObjectInfos("")
	if err != nil {
		return nil, err
	}
	report := &CleanupReport{Registry: reg.Name, DryRun: dryRun, Unit: "versions", Repos: []CleanupRepoReport{}}
	plan := PlanArtifactCleanup(GroupArtifacts(Format(reg.Format), objs), reg.Cleanup, time.Now())
	// Report per package, in plan order.
	byPkg := map[string]*CleanupRepoReport{}
	var order []string
	for _, u := range plan {
		if ctx.Err() != nil {
			break
		}
		removed := 0
		if !dryRun {
			for _, p := range u.Paths {
				if err := st.DeleteObject(p); err != nil {
					report.Errors = append(report.Errors, fmt.Sprintf("%s: %v", p, err))
					continue
				}
				removed++
			}
			if removed == 0 {
				continue
			}
		} else {
			removed = len(u.Paths)
		}
		rr := byPkg[u.Package]
		if rr == nil {
			rr = &CleanupRepoReport{Repo: u.Package, Tags: []string{}}
			byPkg[u.Package] = rr
			order = append(order, u.Package)
		}
		rr.Tags = append(rr.Tags, u.Version)
		rr.Files += removed
		rr.Bytes += u.Size
		report.TagCount++
		report.Files += removed
		report.Bytes += u.Size
	}
	total := map[string]int{}
	for _, u := range GroupArtifacts(Format(reg.Format), objs) {
		total[u.Package]++
	}
	for _, pkg := range order {
		rr := byPkg[pkg]
		rr.Kept = total[pkg] - len(rr.Tags)
		report.Repos = append(report.Repos, *rr)
	}
	if ctx.Err() != nil {
		return report, ctx.Err()
	}
	return report, nil
}

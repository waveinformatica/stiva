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

// PlanCleanup returns the tags of one repository the policy would delete.
// Pure: no I/O, unit-tested. Tags with an unknown push time (0) count as
// recent for the age rule so legacy rows are never deleted for lack of data.
func PlanCleanup(tags []storage.TagInfo, p *CleanupPolicy, now time.Time) []storage.TagInfo {
	if !p.Active() {
		return nil
	}
	ordered := append([]storage.TagInfo(nil), tags...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].PushedAt != ordered[j].PushedAt {
			return ordered[i].PushedAt > ordered[j].PushedAt // newest first
		}
		return ordered[i].Tag < ordered[j].Tag
	})
	var cutoff int64
	if p.MaxAgeDays > 0 {
		cutoff = now.Add(-time.Duration(p.MaxAgeDays) * 24 * time.Hour).UnixNano()
	}
	var del []storage.TagInfo
	rank := 0
	for _, t := range ordered {
		if p.protected(t.Tag) {
			continue
		}
		rank++
		if p.KeepLast > 0 && rank <= p.KeepLast {
			continue
		}
		if p.MaxAgeDays > 0 && (t.PushedAt == 0 || t.PushedAt >= cutoff) {
			continue
		}
		del = append(del, t)
	}
	return del
}

// CleanupRepoReport is the outcome for one repository.
type CleanupRepoReport struct {
	Repo      string   `json:"repo"`
	Kept      int      `json:"kept"`
	Tags      []string `json:"tags"`      // tags deleted (or to delete on a dry run)
	Manifests int      `json:"manifests"` // manifests pruned after losing their last tag
}

// CleanupReport is the outcome of a cleanup run for one registry.
type CleanupReport struct {
	Registry  string              `json:"registry"`
	DryRun    bool                `json:"dry_run"`
	Repos     []CleanupRepoReport `json:"repos"`
	TagCount  int                 `json:"tag_count"`
	Manifests int                 `json:"manifest_count"`
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
	if reg.Format != string(FormatDocker) || reg.Type != string(TypeHosted) {
		return nil, fmt.Errorf("cleanup policies apply to hosted OCI registries only")
	}
	if !reg.Cleanup.Active() {
		return nil, fmt.Errorf("no active cleanup policy: enable it and set keep_last or max_age_days")
	}
	report := &CleanupReport{Registry: name, DryRun: dryRun, Repos: []CleanupRepoReport{}}
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

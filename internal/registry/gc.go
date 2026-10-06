package registry

import (
	"context"
	"fmt"
	"time"

	"registry/internal/digest"
	"registry/internal/storage"
)

// maxListedBlobs caps the per-registry blob sample in a report. Counts and
// byte totals always cover everything; only the listing truncates.
const maxListedBlobs = 200

// GCRegistryReport is the garbage-collection outcome for one registry.
type GCRegistryReport struct {
	Registry     string             `json:"registry"`
	Orphans      int                `json:"orphans"`
	OrphanBytes  int64              `json:"orphan_bytes"`
	Deleted      int                `json:"deleted"`
	DeletedBytes int64              `json:"deleted_bytes"`
	Blobs        []storage.BlobInfo `json:"blobs"`
	Truncated    bool               `json:"truncated"`
	Skipped      string             `json:"skipped,omitempty"`
	Errors       []string           `json:"errors,omitempty"`
}

// GCReport is the outcome of a collection run.
type GCReport struct {
	DryRun     bool               `json:"dry_run"`
	OlderThan  string             `json:"older_than"`
	Registries []GCRegistryReport `json:"registries"`
}

// blobDeleter deletes one blob with a reference check. storage.Store satisfies
// it; tests stub it.
type blobDeleter interface {
	DeleteBlob(digest.Digest) error
}

// SweepBlobs deletes orphan blobs one by one, best effort: a blob that fails
// (re-linked between listing and delete, failing backend) is reported and the
// sweep continues with the rest. Dry runs delete nothing.
func SweepBlobs(del blobDeleter, orphans []storage.BlobInfo, dryRun bool) (deleted int, deletedBytes int64, errs []string) {
	for _, b := range orphans {
		if dryRun {
			continue
		}
		if err := del.DeleteBlob(b.Digest); err != nil {
			if len(errs) < 20 {
				errs = append(errs, fmt.Sprintf("%s: %v", b.Digest, err))
			}
			continue
		}
		deleted++
		deletedBytes += b.Size
	}
	return deleted, deletedBytes, errs
}

// GC collects orphan blobs — content no manifest links and no artifact object
// embeds — for one registry (name != "") or every registry with a store.
// Groups have no store and report as skipped. cut is the creation-time cutoff;
// blobs younger than the grace period are never candidates, which is what
// keeps blobs of pushes still in flight out of the sweep.
func (m *Manager) GC(ctx context.Context, name string, olderThan time.Duration, dryRun bool) (*GCReport, error) {
	cutoff := time.Now().Add(-olderThan)
	report := &GCReport{DryRun: dryRun, OlderThan: olderThan.String(), Registries: []GCRegistryReport{}}
	targets := []string{}
	if name != "" {
		if _, ok := m.Get(name); !ok {
			return nil, fmt.Errorf("registry %q not found", name)
		}
		targets = append(targets, name)
	} else {
		for _, r := range m.List() {
			targets = append(targets, r.Name)
		}
	}
	for _, t := range targets {
		st := m.StoreFor(t)
		if st == nil {
			report.Registries = append(report.Registries, GCRegistryReport{Registry: t, Skipped: "no local store"})
			continue
		}
		orphans, err := m.meta.OrphanBlobs(t, cutoff)
		if err != nil {
			report.Registries = append(report.Registries, GCRegistryReport{
				Registry: t, Errors: []string{err.Error()},
			})
			continue
		}
		var orphanBytes int64
		for _, b := range orphans {
			orphanBytes += b.Size
		}
		deleted, deletedBytes, errs := SweepBlobs(st, orphans, dryRun)
		listed := orphans
		truncated := false
		if len(listed) > maxListedBlobs {
			listed = listed[:maxListedBlobs]
			truncated = true
		}
		if listed == nil {
			listed = []storage.BlobInfo{}
		}
		report.Registries = append(report.Registries, GCRegistryReport{
			Registry: t, Orphans: len(orphans), OrphanBytes: orphanBytes,
			Deleted: deleted, DeletedBytes: deletedBytes,
			Blobs: listed, Truncated: truncated, Errors: errs,
		})
	}
	return report, nil
}

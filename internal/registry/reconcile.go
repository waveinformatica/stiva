package registry

import (
	"context"
	"fmt"

	"registry/internal/digest"
	"registry/internal/storage"
)

// ReconcileReport is the outcome of repairing or rebalancing one registry's
// blob store.
type ReconcileReport struct {
	Registry string   `json:"registry"`
	DryRun   bool     `json:"dry_run"`
	Checked  int      `json:"checked"`
	Changed  int      `json:"changed"` // blobs that needed (or, on a dry run, would need) action
	Failed   int      `json:"failed"`
	Errors   []string `json:"errors,omitempty"`
}

const reconcilePage = 500

// Reconcile walks every blob recorded for a registry and brings it to where its
// replicated or sharded blob store wants it: copied to the replicas that miss
// it, or moved to its owning shard. It is what makes a returned-from-outage
// replica or a newly added shard catch up. Blobs are processed one at a time
// and failures are collected, not fatal.
func (m *Manager) Reconcile(ctx context.Context, name string, dryRun bool) (*ReconcileReport, error) {
	if _, ok := m.Get(name); !ok {
		return nil, fmt.Errorf("registry %q not found", name)
	}
	st := m.StoreFor(name)
	if st == nil {
		return nil, fmt.Errorf("registry %q has no local store", name)
	}
	if !st.Composite() {
		return nil, storage.ErrNotComposite
	}
	rep := &ReconcileReport{Registry: name, DryRun: dryRun}
	var after digest.Digest
	for {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		page, err := st.ListBlobs(after, reconcilePage)
		if err != nil {
			return rep, err
		}
		if len(page) == 0 {
			return rep, nil
		}
		for _, b := range page {
			rep.Checked++
			changed, err := st.Reconcile(b.Digest, dryRun)
			if changed {
				rep.Changed++
			}
			if err != nil {
				rep.Failed++
				if len(rep.Errors) < 20 {
					rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", b.Digest, err))
				}
			}
			after = b.Digest
		}
	}
}

// ReconcilableBlobs reports whether the registry's blob store is replicated or
// sharded, i.e. whether a reconcile run means anything for it.
func (m *Manager) ReconcilableBlobs(name string) bool {
	st := m.StoreFor(name)
	return st != nil && st.Composite()
}

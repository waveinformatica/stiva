package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type scanner struct {
	stiva    *stiva
	trivy    *trivy
	db       database
	opts     options
	work     string
	pageSize int
}

// full scans every pending image. An image that fails is reported and skipped
// for the rest of the run (it stays pending for the next one); the run then
// exits with an error so the CronJob shows the failure.
func (s *scanner) full(ctx context.Context) error {
	analyzer, err := analyzerVersion()
	if err != nil {
		return err
	}
	p, err := startProxy(s.stiva)
	if err != nil {
		return err
	}
	defer p.close(context.Background())

	failed := map[target]bool{}
	scanned := 0
	for {
		// Failed images stay pending and come back first: ask for enough to
		// get past them.
		page, err := s.stiva.pending(ctx, analyzer, min(s.pageSize+len(failed), 500))
		if err != nil {
			return err
		}
		var todo []target
		for _, t := range page {
			if !failed[t] {
				todo = append(todo, t)
			}
		}
		if len(todo) == 0 {
			break
		}
		for _, t := range todo {
			if err := ctx.Err(); err != nil {
				return err
			}
			start := time.Now()
			if err := s.scanImage(ctx, p, analyzer, t); err != nil {
				logger.Printf("FAILED %s: %v", t, err)
				failed[t] = true
				continue
			}
			scanned++
			logger.Printf("scanned %s in %s", t, time.Since(start).Round(time.Second))
		}
	}
	logger.Printf("full scan: %d images scanned, %d failed (%s, database v%d of %s)",
		scanned, len(failed), analyzer, s.db.Version, s.db.UpdatedAt.Format(time.RFC3339))
	if len(failed) > 0 {
		return fmt.Errorf("%d images could not be scanned", len(failed))
	}
	return nil
}

func (s *scanner) scanImage(ctx context.Context, p *proxy, analyzer string, t target) error {
	dir, err := os.MkdirTemp(s.work, "image-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	ref := p.ref(t)
	doc, err := s.trivy.imageSBOM(ctx, ref, dir)
	if err != nil {
		return err
	}
	rep, err := s.trivy.imageFindings(ctx, ref, dir)
	if err != nil {
		return err
	}
	pkgs, occ, err := imagePackages(rep)
	if err != nil {
		return err
	}
	secrets, err := secretsOf(rep)
	if err != nil {
		return err
	}
	return s.stiva.putResults(ctx, t, results{
		Analyzer: analyzer, Database: s.db, Options: s.opts,
		SBOM:     sbomDocument{Format: "cyclonedx", Document: json.RawMessage(doc)},
		Packages: pkgs, Occurrences: occ, Secrets: secrets,
	})
}

// refreshBatch is how many packages a refresh request carries.
const refreshBatch = 500

// refresh re-matches every package of the archive against the database
// downloaded for this run, one Trivy run per context (an OS release, an
// ecosystem) instead of one per image. Images are not read.
func (s *scanner) refresh(ctx context.Context) error {
	pruned, err := s.stiva.prune(ctx)
	if err != nil {
		return err
	}
	// The archive grouped by context. Contexts are canonical JSON, so equal
	// contexts are equal strings.
	byContext := map[string][]archivedPackage{}
	total := 0
	after := ""
	for {
		page, err := s.stiva.packages(ctx, after, s.pageSize)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			break
		}
		for _, p := range page {
			byContext[string(p.Context)] = append(byContext[string(p.Context)], p)
		}
		total += len(page)
		after = page[len(page)-1].Key
	}
	contexts := make([]string, 0, len(byContext))
	for c := range byContext {
		contexts = append(contexts, c)
	}
	sort.Strings(contexts)

	refreshed, failures, gone := 0, 0, 0
	for _, c := range contexts {
		if err := ctx.Err(); err != nil {
			return err
		}
		pkgs := byContext[c]
		n, unknown, err := s.refreshContext(ctx, json.RawMessage(c), pkgs)
		if err != nil {
			logger.Printf("FAILED context %s (%d packages): %v", c, len(pkgs), err)
			failures++
			continue
		}
		refreshed += n
		gone += unknown
	}
	logger.Printf("vulnerability refresh: %d packages in %d contexts, %d refreshed, %d gone meanwhile, %d contexts failed, %d unused packages pruned (database v%d of %s)",
		total, len(contexts), refreshed, gone, failures, pruned, s.db.Version, s.db.UpdatedAt.Format(time.RFC3339))
	if failures > 0 {
		return fmt.Errorf("%d contexts could not be refreshed", failures)
	}
	return nil
}

func (s *scanner) refreshContext(ctx context.Context, c json.RawMessage, pkgs []archivedPackage) (int, int, error) {
	dir, err := os.MkdirTemp(s.work, "context-")
	if err != nil {
		return 0, 0, err
	}
	defer os.RemoveAll(dir)

	doc, err := contextSBOM(ctx, c, pkgs)
	if err != nil {
		return 0, 0, err
	}
	path := filepath.Join(dir, "packages.cdx.json")
	if err := os.WriteFile(path, doc, 0o600); err != nil {
		return 0, 0, err
	}
	rep, err := s.trivy.sbomVulnerabilities(ctx, path, dir)
	if err != nil {
		return 0, 0, err
	}
	vulns, err := contextVulnerabilities(rep, pkgs)
	if err != nil {
		return 0, 0, err
	}
	refreshed, gone := 0, 0
	for start := 0; start < len(pkgs); start += refreshBatch {
		end := min(start+refreshBatch, len(pkgs))
		batch := packageVulnerabilities{Database: s.db, Options: s.opts}
		for _, p := range pkgs[start:end] {
			batch.Packages = append(batch.Packages, keyVulnerabilities{Key: p.Key, Vulnerabilities: vulns[p.Key]})
		}
		unknown, err := s.stiva.putPackageVulnerabilities(ctx, batch)
		if err != nil {
			return refreshed, gone, err
		}
		refreshed += len(batch.Packages) - len(unknown)
		gone += len(unknown)
	}
	return refreshed, gone, nil
}

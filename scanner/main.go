// Command stiva-scanner analyses the images held by a Stiva registry and sends
// the results back to it: CycloneDX SBOMs, vulnerabilities and exposed secrets
// (docs/scanning.md). Trivy is built in: every analysis runs Trivy's own
// command line, so the findings are those of the trivy command with the same
// options. It is meant to run as a Kubernetes CronJob; each run does one mode
// and exits.
//
//	stiva-scanner -mode full             scan the images never scanned by this Trivy
//	stiva-scanner -mode vulnerabilities  re-match every stored SBOM against today's database
//	stiva-scanner trivy <args>           Trivy's own command line
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
)

// logger writes to stderr directly. Trivy installs its own slog default
// handler, and since Go 1.21 the standard log package writes through it: the
// package-level log functions would follow Trivy's quiet mode.
var logger = log.New(os.Stderr, "", log.LstdFlags)

const (
	modeFull            = "full"
	modeVulnerabilities = "vulnerabilities"
)

type config struct {
	stivaURL      string
	tokenFile     string
	mode          string
	cacheDir      string
	dbRepository  string
	javaDBRepo    string
	ignoreUnfixed bool
	ignoreFile    string
	pageSize      int
	timeout       time.Duration
}

func parseFlags(args []string) (config, error) {
	var c config
	fs := flag.NewFlagSet("stiva-scanner", flag.ContinueOnError)
	fs.StringVar(&c.stivaURL, "stiva-url", "", "base URL of the Stiva registry (required)")
	fs.StringVar(&c.tokenFile, "token-file", "", "file holding the API key of an account with the scanner role (required)")
	fs.StringVar(&c.mode, "mode", "", "full | vulnerabilities (required)")
	fs.StringVar(&c.cacheDir, "cache-dir", "", "Trivy cache directory: databases and layer analyses; keep it on a volume (required)")
	fs.StringVar(&c.dbRepository, "db-repository", "", "OCI repository of the vulnerability database, e.g. mirror.gcr.io/aquasec/trivy-db (required)")
	fs.StringVar(&c.javaDBRepo, "java-db-repository", "", "OCI repository of the Java database, e.g. mirror.gcr.io/aquasec/trivy-java-db (required)")
	fs.BoolVar(&c.ignoreUnfixed, "ignore-unfixed", false, "leave out vulnerabilities that have no fixed version")
	fs.StringVar(&c.ignoreFile, "ignore-file", "", "Trivy ignore policy (.trivyignore.yaml) applied to the findings")
	fs.IntVar(&c.pageSize, "page-size", 100, "images asked from the registry at a time")
	fs.DurationVar(&c.timeout, "timeout", 15*time.Minute, "longest a single analysis may take")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	var missing []string
	for name, v := range map[string]string{
		"stiva-url": c.stivaURL, "token-file": c.tokenFile, "mode": c.mode, "cache-dir": c.cacheDir,
		"db-repository": c.dbRepository, "java-db-repository": c.javaDBRepo,
	} {
		if v == "" {
			missing = append(missing, "-"+name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return c, fmt.Errorf("required: %s", strings.Join(missing, ", "))
	}
	if c.mode != modeFull && c.mode != modeVulnerabilities {
		return c, fmt.Errorf("-mode must be %q or %q", modeFull, modeVulnerabilities)
	}
	if c.pageSize < 1 || c.pageSize > 500 {
		return c, errors.New("-page-size must be between 1 and 500")
	}
	return c, nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "trivy" {
		os.Exit(runTrivyCLI(os.Args[2:]))
	}
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		logger.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg); err != nil {
		logger.Fatal(err)
	}
}

func run(ctx context.Context, cfg config) error {
	token, err := os.ReadFile(cfg.tokenFile)
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	tr := &trivy{
		self: self, cacheDir: cfg.cacheDir, dbRepository: cfg.dbRepository, javaDBRepo: cfg.javaDBRepo,
		ignoreUnfixed: cfg.ignoreUnfixed, ignoreFile: cfg.ignoreFile, timeout: cfg.timeout,
	}
	st := newStiva(cfg.stivaURL, strings.TrimSpace(string(token)))

	if err := tr.downloadDatabases(ctx); err != nil {
		return fmt.Errorf("downloading the databases: %w", err)
	}
	db, err := tr.database()
	if err != nil {
		return fmt.Errorf("reading the database metadata: %w", err)
	}
	opts, err := tr.options()
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "stiva-scan-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	s := &scanner{stiva: st, trivy: tr, db: db, opts: opts, work: work, pageSize: cfg.pageSize}
	if cfg.mode == modeFull {
		return s.full(ctx)
	}
	return s.refresh(ctx)
}

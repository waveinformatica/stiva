package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/aquasecurity/trivy/pkg/commands"
	"github.com/aquasecurity/trivy/pkg/result"
	"github.com/aquasecurity/trivy/pkg/types"

	_ "modernc.org/sqlite" // sqlite driver for the RPM database and the Java DB, as in Trivy's own main
)

const trivyModule = "github.com/aquasecurity/trivy"

// runTrivyCLI runs Trivy's own command line in this process: it is what
// "stiva-scanner trivy <args>" does. The scanner runs every analysis that way,
// in a child process, so each one goes through exactly the path of the trivy
// command, with fresh global state.
func runTrivyCLI(args []string) int {
	defer commands.Cleanup()
	app := commands.NewApp()
	app.SetArgs(args)
	if err := app.ExecuteContext(commands.NotifyContext(context.Background())); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// analyzerVersion is "trivy/<module version>" of the Trivy built into this
// binary: the version Stiva records with every full scan.
func analyzerVersion() (string, error) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "", fmt.Errorf("no build information in the binary")
	}
	for _, d := range bi.Deps {
		if d.Path == trivyModule {
			return "trivy/" + d.Version, nil
		}
	}
	return "", fmt.Errorf("%s is not a dependency of this binary", trivyModule)
}

// trivy runs analyses with a fixed configuration.
type trivy struct {
	self          string // this executable
	cacheDir      string
	dbRepository  string
	javaDBRepo    string
	ignoreUnfixed bool
	ignoreFile    string
	timeout       time.Duration
}

func (tr *trivy) run(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, tr.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, tr.self, append([]string{"trivy"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("trivy %s: %w\n%s", args[0], err, tail(stderr.String(), 4000))
	}
	return nil
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// common flags of every analysis. The databases are downloaded once per run
// (downloadDatabases), so every analysis of a run matches against the same one.
func (tr *trivy) common() []string {
	return []string{"--cache-dir", tr.cacheDir, "--skip-db-update", "--skip-java-db-update", "--quiet"}
}

// findingFlags are the options that filter findings, the same for a full scan
// and a refresh.
func (tr *trivy) findingFlags() []string {
	var f []string
	if tr.ignoreUnfixed {
		f = append(f, "--ignore-unfixed")
	}
	if tr.ignoreFile != "" {
		f = append(f, "--ignorefile", tr.ignoreFile)
	}
	return f
}

// downloadDatabases fetches the vulnerability and Java databases.
func (tr *trivy) downloadDatabases(ctx context.Context) error {
	if err := tr.run(ctx, "image", "--download-db-only", "--cache-dir", tr.cacheDir, "--db-repository", tr.dbRepository, "--quiet"); err != nil {
		return err
	}
	return tr.run(ctx, "image", "--download-java-db-only", "--cache-dir", tr.cacheDir, "--java-db-repository", tr.javaDBRepo, "--quiet")
}

// database reads the metadata of the downloaded vulnerability database.
func (tr *trivy) database() (database, error) {
	b, err := os.ReadFile(filepath.Join(tr.cacheDir, "db", "metadata.json"))
	if err != nil {
		return database{}, err
	}
	var m struct {
		Version   int
		UpdatedAt time.Time
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return database{}, err
	}
	return database{Version: m.Version, UpdatedAt: m.UpdatedAt}, nil
}

// options describes the filtering applied to findings.
func (tr *trivy) options() (options, error) {
	o := options{IgnoreUnfixed: tr.ignoreUnfixed}
	if tr.ignoreFile != "" {
		// Vulnerabilities belong to packages, matched once for every image
		// that contains them: an exception limited to some paths cannot be
		// applied there, so the policy is refused rather than half applied.
		conf, err := result.ParseIgnoreFile(context.Background(), tr.ignoreFile)
		if err != nil {
			return options{}, err
		}
		for _, f := range conf.Vulnerabilities {
			if len(f.Paths) > 0 {
				return options{}, fmt.Errorf("ignore policy: vulnerability %s is limited to paths; use purls or no restriction", f.ID)
			}
		}
		b, err := os.ReadFile(tr.ignoreFile)
		if err != nil {
			return options{}, err
		}
		sum := sha256.Sum256(b)
		o.IgnorePolicy = "sha256:" + hex.EncodeToString(sum[:])
	}
	return o, nil
}

// imageSBOM produces the CycloneDX SBOM of an image: exactly
// "trivy image --format cyclonedx".
func (tr *trivy) imageSBOM(ctx context.Context, ref, dir string) ([]byte, error) {
	out := filepath.Join(dir, "sbom.cdx.json")
	args := append([]string{"image", "--image-src", "remote", "--insecure", "--format", "cyclonedx", "--output", out}, tr.common()...)
	if err := tr.run(ctx, append(args, ref)...); err != nil {
		return nil, err
	}
	return os.ReadFile(out)
}

// imageFindings scans an image for vulnerabilities and secrets, listing every
// package (the registry keeps them).
func (tr *trivy) imageFindings(ctx context.Context, ref, dir string) (*types.Report, error) {
	out := filepath.Join(dir, "image.json")
	args := append([]string{"image", "--image-src", "remote", "--insecure", "--format", "json", "--scanners", "vuln,secret",
		"--list-all-pkgs", "--output", out}, tr.common()...)
	args = append(args, tr.findingFlags()...)
	if err := tr.run(ctx, append(args, ref)...); err != nil {
		return nil, err
	}
	return readReport(out)
}

// sbomVulnerabilities matches an SBOM against the current database.
func (tr *trivy) sbomVulnerabilities(ctx context.Context, sbomPath, dir string) (*types.Report, error) {
	out := filepath.Join(dir, "sbom.json")
	args := append([]string{"sbom", "--format", "json", "--scanners", "vuln", "--output", out}, tr.common()...)
	args = append(args, tr.findingFlags()...)
	if err := tr.run(ctx, append(args, sbomPath)...); err != nil {
		return nil, err
	}
	return readReport(out)
}

func readReport(path string) (*types.Report, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r types.Report
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("trivy report: %w", err)
	}
	return &r, nil
}

// severities maps Trivy's severities onto the registry's spelling.
var severities = map[string]string{
	"CRITICAL": "Critical", "HIGH": "High", "MEDIUM": "Medium", "LOW": "Low", "UNKNOWN": "Unknown",
}

func severity(s string) (string, error) {
	v, ok := severities[s]
	if !ok {
		return "", fmt.Errorf("unknown severity %q", s)
	}
	return v, nil
}

func secretsOf(r *types.Report) ([]exposedSecret, error) {
	out := []exposedSecret{}
	for _, res := range r.Results {
		for _, s := range res.Secrets {
			sev, err := severity(s.Severity)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", s.RuleID, err)
			}
			out = append(out, exposedSecret{
				RuleID: s.RuleID, Category: string(s.Category), Severity: sev, Title: s.Title,
				Target: res.Target, Line: s.StartLine,
			})
		}
	}
	return out, nil
}

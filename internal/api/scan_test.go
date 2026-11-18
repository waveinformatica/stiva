package api

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"registry/internal/digest"
	"registry/internal/storage"
)

func fullResults() scanResults {
	return scanResults{
		Analyzer: "trivy/v0.76.2",
		Database: scanDatabase{Version: 2, UpdatedAt: time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)},
		Options:  scanOptions{IgnoreUnfixed: true},
		SBOM:     scanSBOMDocument{Format: sbomFormatCycloneDX, Document: json.RawMessage(`{"bomFormat":"CycloneDX","specVersion":"1.6"}`)},
		Packages: []scanPackage{
			{
				Context: json.RawMessage(`{"class":"os-pkgs","type":"debian","os":{"Family":"debian","Name":"12.8"}}`),
				Package: json.RawMessage(`{"Name":"openssl","Version":"3.0.15","Identifier":{"PURL":"pkg:deb/debian/openssl@3.0.15"}}`),
				Vulnerabilities: []scanVulnerability{
					{ID: "CVE-2026-1", Severity: "Critical", Fixed: "3.0.16"},
					{ID: "CVE-2026-2", Severity: "Low"},
				},
			},
			{
				Context:         json.RawMessage(`{"class":"lang-pkgs","type":"npm"}`),
				Package:         json.RawMessage(`{"Name":"lodash","Version":"4.17.20"}`),
				Vulnerabilities: []scanVulnerability{},
			},
		},
		Occurrences: []scanOccurrence{
			{Target: "debian 12.8", Package: 0, Count: 1},
			{Target: "app/package-lock.json", Package: 1, Count: 2},
		},
		Secrets: []scanSecret{{RuleID: "aws-access-key-id", Severity: "Critical", Target: "/app/.env"}},
	}
}

func TestScanResultsResolve(t *testing.T) {
	r := fullResults()
	pkgs, occ, err := r.resolve()
	if err != nil {
		t.Fatalf("scansione valida: %v", err)
	}
	if len(pkgs) != 2 || len(occ) != 2 {
		t.Fatalf("pacchetti %d, occorrenze %d", len(pkgs), len(occ))
	}
	if pkgs[0].PURL != "pkg:deb/debian/openssl@3.0.15" || pkgs[1].PURL != "" {
		t.Errorf("purl: %q %q", pkgs[0].PURL, pkgs[1].PURL)
	}
	if occ[1].PackageKey != pkgs[1].Key || occ[1].Count != 2 {
		t.Errorf("occorrenza: %+v", occ[1])
	}
	if len(pkgs[0].Vulnerabilities) != 2 || pkgs[0].Vulnerabilities[0].Fixed != "3.0.16" {
		t.Errorf("vulnerabilita': %+v", pkgs[0].Vulnerabilities)
	}

	cases := map[string]func(*scanResults){
		"senza analyzer":          func(r *scanResults) { r.Analyzer = "" },
		"senza database":          func(r *scanResults) { r.Database = scanDatabase{} },
		"sbom non cyclonedx":      func(r *scanResults) { r.SBOM.Format = "spdx" },
		"documento non BOM":       func(r *scanResults) { r.SBOM.Document = json.RawMessage(`{"spdxVersion":"2.3"}`) },
		"senza packages":          func(r *scanResults) { r.Packages = nil },
		"senza occurrences":       func(r *scanResults) { r.Occurrences = nil },
		"senza secrets":           func(r *scanResults) { r.Secrets = nil },
		"contesto vuoto":          func(r *scanResults) { r.Packages[0].Context = json.RawMessage(`{}`) },
		"pacchetto non oggetto":   func(r *scanResults) { r.Packages[0].Package = json.RawMessage(`[1]`) },
		"vulnerabilities assente": func(r *scanResults) { r.Packages[1].Vulnerabilities = nil },
		"severita' sconosciuta":   func(r *scanResults) { r.Packages[0].Vulnerabilities[0].Severity = "CRITICAL" },
		"vulnerabilita' doppia":   func(r *scanResults) { r.Packages[0].Vulnerabilities[1].ID = "CVE-2026-1" },
		"pacchetto doppio":        func(r *scanResults) { r.Packages[1] = r.Packages[0] },
		"occorrenza fuori range":  func(r *scanResults) { r.Occurrences[0].Package = 5 },
		"occorrenza senza target": func(r *scanResults) { r.Occurrences[0].Target = "" },
		"occorrenza a zero":       func(r *scanResults) { r.Occurrences[0].Count = 0 },
		"occorrenza doppia":       func(r *scanResults) { r.Occurrences[1] = r.Occurrences[0] },
		"pacchetto mai presente":  func(r *scanResults) { r.Occurrences = r.Occurrences[:1] },
		"segreto senza regola":    func(r *scanResults) { r.Secrets[0].RuleID = "" },
	}
	for name, mutate := range cases {
		r := fullResults()
		mutate(&r)
		if _, _, err := r.resolve(); err == nil {
			t.Errorf("%s: atteso un errore", name)
		}
	}
}

// La chiave di un pacchetto non dipende dall'ordine dei campi ne' dagli spazi:
// lo stesso pacchetto visto in due immagini e' un pacchetto solo.
func TestPackageKeyIsCanonical(t *testing.T) {
	a, err := canonicalJSON(json.RawMessage(`{"Name":"openssl","Version":"3.0.15","Epoch":1}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonicalJSON(json.RawMessage(`{ "Epoch": 1, "Version": "3.0.15", "Name": "openssl" }`))
	if err != nil {
		t.Fatal(err)
	}
	ctx, _ := canonicalJSON(json.RawMessage(`{"class":"os-pkgs","type":"debian"}`))
	if packageKey(ctx, a) != packageKey(ctx, b) {
		t.Error("stesso pacchetto, chiavi diverse")
	}
	other, _ := canonicalJSON(json.RawMessage(`{"class":"os-pkgs","type":"ubuntu"}`))
	if packageKey(ctx, a) == packageKey(other, a) {
		t.Error("contesti diversi, stessa chiave")
	}
	if _, err := digest.Parse(packageKey(ctx, a)); err != nil {
		t.Errorf("la chiave non e' un digest: %v", err)
	}
}

func TestSecretCounts(t *testing.T) {
	r := fullResults()
	if sc := secretCounts(r.Secrets); sc["Critical"] != 1 || len(sc) != 1 {
		t.Errorf("conteggi segreti: %v", sc)
	}
}

func TestWriteScanMetrics(t *testing.T) {
	d := digest.FromBytes([]byte("image"))
	at := time.Unix(1795000000, 0).UTC()
	var b bytes.Buffer
	writeScanMetrics(&b, []storage.ScanSummary{{
		ScanTarget:               storage.ScanTarget{Registry: "cache", Repo: "library/nginx", Digest: d},
		VulnerabilityCounts:      map[string]int{"Critical": 2, "Low": 1, "High": 0},
		SecretCounts:             map[string]int{},
		ScannedAt:                at,
		VulnerabilitiesScannedAt: at.Add(time.Hour),
	}})
	out := b.String()
	lbl := `registry="cache",repository="library/nginx",image_digest="` + d.String() + `"`
	for _, want := range []string{
		"# TYPE stiva_image_vulnerabilities gauge\n",
		"stiva_image_vulnerabilities{" + lbl + `,severity="Critical"} 2` + "\n",
		"stiva_image_vulnerabilities{" + lbl + `,severity="Low"} 1` + "\n",
		"# TYPE stiva_image_exposedsecrets gauge\n",
		"stiva_image_scan_timestamp_seconds{" + lbl + "} 1795000000\n",
		"stiva_image_vulnerabilities_timestamp_seconds{" + lbl + "} 1795003600\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("manca %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, `severity="High"`) || strings.Contains(out, "stiva_image_exposedsecrets{") {
		t.Errorf("conteggi nulli esportati:\n%s", out)
	}
	if strings.Index(out, `severity="Critical"`) > strings.Index(out, `severity="Low"`) {
		t.Error("ordine delle severita' non deterministico")
	}
}

func TestPromEscape(t *testing.T) {
	if got := promEscape(`a"b\c` + "\n"); got != `a\"b\\c\n` {
		t.Errorf("escape: %q", got)
	}
}

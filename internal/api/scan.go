package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"registry/internal/authz"
	"registry/internal/digest"
	"registry/internal/registry"
	"registry/internal/storage"
)

// Image scanning (docs/scanning.md): the registry stores and serves scan
// results; the analysis itself is done by stiva-scanner, which talks to these
// endpoints.

const (
	sbomFormatCycloneDX    = "cyclonedx"
	mediaTypeCycloneDX     = "application/vnd.cyclonedx+json"
	mediaTypeEmptyJSON     = "application/vnd.oci.empty.v1+json"
	annotationCreated      = "org.opencontainers.image.created"
	annotationScanAnalyzer = "dev.stiva.scan.analyzer"

	maxScanResultsBytes = 64 << 20
	maxScanPageSize     = 500
)

// Severities, in the order metrics list them. The spelling is trivy-operator's,
// so existing dashboards and rules keep their label values.
var scanSeverities = []string{"Critical", "High", "Medium", "Low", "Unknown"}

func validSeverity(s string) bool {
	for _, x := range scanSeverities {
		if x == s {
			return true
		}
	}
	return false
}

// SetScans wires the scan result store.
func (h *Handler) SetScans(s storage.ScanStore) { h.scans = s }

func (h *Handler) scansReady(c *gin.Context) bool {
	if h.scans == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "image scanning not configured"})
		return false
	}
	return true
}

// registerScan mounts the scanner's endpoints under /api/v1/scan.
func (h *Handler) registerScan(v1 gin.IRouter) {
	s := v1.Group("/scan")
	s.GET("/pending", h.scanPending)
	s.GET("/manifest", h.scanManifest)
	s.GET("/blob", h.scanBlob)
	s.GET("/sbom", h.scanSBOM)
	s.GET("/vulnerabilities", h.scanImageVulnerabilities)
	s.PUT("/results", h.scanPutResults)
	s.GET("/packages", h.scanPackages)
	s.PUT("/packages/vulnerabilities", h.scanPutPackageVulnerabilities)
	s.POST("/packages/prune", h.scanPrunePackages)
}

// RegisterMetrics mounts GET /metrics (Prometheus text format).
func (h *Handler) RegisterMetrics(r gin.IRouter) {
	r.GET("/metrics", h.metrics)
}

// scanRegistries lists the OCI registries on which the caller holds every one
// of perms over the whole registry, in name order.
func (h *Handler) scanRegistries(c *gin.Context, perms ...authz.Permission) []string {
	p := principal(h.currentUser(c))
	out := []string{}
	if h.authz == nil {
		return out
	}
	for _, reg := range h.mgr.List() {
		if registry.Format(reg.Format) != registry.FormatDocker {
			continue
		}
		ok := true
		for _, perm := range perms {
			if !h.authz.Allows(p, perm, reg.Format, reg.Name, "") {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, reg.Name)
		}
	}
	return out
}

func pageSize(c *gin.Context) (int, bool) {
	limit := 100
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxScanPageSize {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("limit must be between 1 and %d", maxScanPageSize)})
			return 0, false
		}
		limit = n
	}
	return limit, true
}

// scanPending handles GET /api/v1/scan/pending?analyzer=&limit=.
func (h *Handler) scanPending(c *gin.Context) {
	if !h.scansReady(c) {
		return
	}
	analyzer := c.Query("analyzer")
	if analyzer == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "analyzer is required"})
		return
	}
	limit, ok := pageSize(c)
	if !ok {
		return
	}
	regs := h.scanRegistries(c, authz.RegistryRead, authz.RegistryScan)
	targets, err := h.scans.PendingScans(regs, analyzer, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"images": targets})
}

// scanTarget reads registry/repo/digest from the query and checks perm on it.
// The registry is named, not routed by host: the scanner does not need to
// know how clients reach each registry.
func (h *Handler) scanTarget(c *gin.Context, perm authz.Permission) (*registry.Registry, storage.ScanTarget, bool) {
	t := storage.ScanTarget{Registry: c.Query("registry"), Repo: c.Query("repo")}
	reg, found := h.mgr.Get(t.Registry)
	if !found || registry.Format(reg.Format) != registry.FormatDocker {
		c.JSON(http.StatusNotFound, gin.H{"error": "no such OCI registry"})
		return nil, t, false
	}
	if !validRepo(t.Repo) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repository name"})
		return nil, t, false
	}
	d, err := digest.Parse(c.Query("digest"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid digest"})
		return nil, t, false
	}
	t.Digest = d
	if h.authz == nil || !h.authz.Allows(principal(h.currentUser(c)), perm, reg.Format, reg.Name, t.Repo) {
		c.JSON(http.StatusForbidden, gin.H{"error": "permission required", "permission": string(perm)})
		return nil, t, false
	}
	return reg, t, true
}

// scanManifest handles GET /api/v1/scan/manifest?registry=&repo=&digest=.
func (h *Handler) scanManifest(c *gin.Context) {
	reg, t, ok := h.scanTarget(c, authz.RegistryRead)
	if !ok {
		return
	}
	be, err := h.mgr.BackendFor(reg.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	content, mt, err := be.GetManifest(t.Repo, t.Digest.String())
	if errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "manifest not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, mt, content)
}

// scanBlob handles GET /api/v1/scan/blob?registry=&repo=&digest=.
func (h *Handler) scanBlob(c *gin.Context) {
	reg, t, ok := h.scanTarget(c, authz.RegistryRead)
	if !ok {
		return
	}
	be, err := h.mgr.BackendFor(reg.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	rc, size, err := be.GetBlob(t.Repo, t.Digest)
	if errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "blob not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rc.Close()
	c.DataFromReader(http.StatusOK, size, "application/octet-stream", rc, nil)
}

// scanSBOM handles GET /api/v1/scan/sbom?registry=&repo=&digest=: the SBOM of
// a scanned image.
func (h *Handler) scanSBOM(c *gin.Context) {
	if !h.scansReady(c) {
		return
	}
	reg, t, ok := h.scanTarget(c, authz.RegistryRead)
	if !ok {
		return
	}
	s, err := h.scans.GetImageScan(t)
	if errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "image not scanned"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	rc, size, err := h.mgr.StoreFor(reg.Name).GetBlob(t.Repo, s.SBOMBlob)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "SBOM document: " + err.Error()})
		return
	}
	defer rc.Close()
	c.DataFromReader(http.StatusOK, size, mediaTypeCycloneDX, rc, nil)
}

// scanAllRegistries reports whether the caller holds read and scan on every
// OCI registry. The package archive is shared by all of them: listing or
// changing it is for an account that sees everything it summarises.
func (h *Handler) scanAllRegistries(c *gin.Context) bool {
	all := 0
	for _, reg := range h.mgr.List() {
		if registry.Format(reg.Format) == registry.FormatDocker {
			all++
		}
	}
	if len(h.scanRegistries(c, authz.RegistryRead, authz.RegistryScan)) != all {
		c.JSON(http.StatusForbidden, gin.H{"error": "registry:read and registry:scan on every OCI registry are required"})
		return false
	}
	return true
}

// scanVulnerability is one vulnerability of a package.
type scanVulnerability struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Fixed    string `json:"fixed"`
	Title    string `json:"title"`
}

// scanSecret is one secret found in the image's layers.
type scanSecret struct {
	RuleID   string `json:"rule_id"`
	Category string `json:"category"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Target   string `json:"target"`
	Line     int    `json:"line"`
}

type scanOptions struct {
	IgnoreUnfixed bool   `json:"ignore_unfixed"`
	IgnorePolicy  string `json:"ignore_policy"` // digest of the ignore file, "" when none
}

type scanDatabase struct {
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (d scanDatabase) validate() error {
	if d.Version < 1 || d.UpdatedAt.IsZero() {
		return errors.New("database version and updated_at are required")
	}
	return nil
}

type scanSBOMDocument struct {
	Format   string          `json:"format"`
	Document json.RawMessage `json:"document"`
}

// scanPackage is a package of an image as the scanner reports it.
type scanPackage struct {
	Context         json.RawMessage     `json:"context"`
	Package         json.RawMessage     `json:"package"`
	Vulnerabilities []scanVulnerability `json:"vulnerabilities"`
}

// scanOccurrence: the image contains packages[Package] in Target, Count times.
type scanOccurrence struct {
	Target  string `json:"target"`
	Package int    `json:"package"`
	Count   int    `json:"count"`
}

// scanResults is the body of PUT /api/v1/scan/results: a full scan.
type scanResults struct {
	Analyzer    string           `json:"analyzer"`
	Database    scanDatabase     `json:"database"`
	Options     scanOptions      `json:"options"`
	SBOM        scanSBOMDocument `json:"sbom"`
	Packages    []scanPackage    `json:"packages"`
	Occurrences []scanOccurrence `json:"occurrences"`
	Secrets     []scanSecret     `json:"secrets"`
}

func validateVulnerabilities(vs []scanVulnerability, where string) error {
	if vs == nil {
		return fmt.Errorf("%s: vulnerabilities is required (an empty list when there are none)", where)
	}
	seen := map[string]bool{}
	for i, v := range vs {
		if v.ID == "" || !validSeverity(v.Severity) {
			return fmt.Errorf("%s.vulnerabilities[%d]: id and a valid severity are required", where, i)
		}
		if seen[v.ID] {
			return fmt.Errorf("%s.vulnerabilities[%d]: %s listed twice", where, i, v.ID)
		}
		seen[v.ID] = true
	}
	return nil
}

// canonicalJSON re-encodes a JSON object with sorted keys and no spacing, so
// the same content always has the same bytes.
func canonicalJSON(raw json.RawMessage) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v map[string]any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if len(v) == 0 {
		return nil, errors.New("an empty object")
	}
	return json.Marshal(v)
}

// packageKey is the identity of a package in the archive: the sha256 of the
// canonical {context, package}.
func packageKey(context, pkg json.RawMessage) string {
	b, _ := json.Marshal(map[string]json.RawMessage{"context": context, "package": pkg})
	return digest.FromBytes(b).String()
}

// packagePURL reads the package URL the analyser assigned, "" when none.
func packagePURL(pkg json.RawMessage) string {
	var p struct {
		Identifier struct {
			PURL string `json:"PURL"`
		} `json:"Identifier"`
	}
	if err := json.Unmarshal(pkg, &p); err != nil {
		return ""
	}
	return p.Identifier.PURL
}

// resolve validates a full scan and turns it into the stored form: packages
// keyed by content, occurrences pointing at the keys.
func (r *scanResults) resolve() ([]storage.PackageResult, []storage.PackageOccurrence, error) {
	if r.Analyzer == "" {
		return nil, nil, errors.New("analyzer is required")
	}
	if err := r.Database.validate(); err != nil {
		return nil, nil, err
	}
	if r.SBOM.Format != sbomFormatCycloneDX {
		return nil, nil, fmt.Errorf("sbom format must be %q", sbomFormatCycloneDX)
	}
	var doc map[string]any
	if err := json.Unmarshal(r.SBOM.Document, &doc); err != nil || doc["bomFormat"] != "CycloneDX" {
		return nil, nil, errors.New("sbom document is not a CycloneDX BOM")
	}
	if r.Packages == nil || r.Occurrences == nil || r.Secrets == nil {
		return nil, nil, errors.New("packages, occurrences and secrets are required (empty lists when there are none)")
	}
	pkgs := make([]storage.PackageResult, len(r.Packages))
	keys := map[string]int{}
	for i, p := range r.Packages {
		where := fmt.Sprintf("packages[%d]", i)
		ctx, err := canonicalJSON(p.Context)
		if err != nil {
			return nil, nil, fmt.Errorf("%s.context: %v", where, err)
		}
		pkg, err := canonicalJSON(p.Package)
		if err != nil {
			return nil, nil, fmt.Errorf("%s.package: %v", where, err)
		}
		if err := validateVulnerabilities(p.Vulnerabilities, where); err != nil {
			return nil, nil, err
		}
		key := packageKey(ctx, pkg)
		if j, dup := keys[key]; dup {
			return nil, nil, fmt.Errorf("%s is packages[%d] again", where, j)
		}
		keys[key] = i
		vulns := make([]storage.PackageVulnerability, len(p.Vulnerabilities))
		for k, v := range p.Vulnerabilities {
			vulns[k] = storage.PackageVulnerability{ID: v.ID, Severity: v.Severity, Fixed: v.Fixed, Title: v.Title}
		}
		pkgs[i] = storage.PackageResult{
			ScanPackage:     storage.ScanPackage{Key: key, Context: ctx, Package: pkg, PURL: packagePURL(pkg)},
			Vulnerabilities: vulns,
		}
	}
	occ := make([]storage.PackageOccurrence, len(r.Occurrences))
	used := make([]bool, len(r.Packages))
	seen := map[string]bool{}
	for i, o := range r.Occurrences {
		if o.Target == "" || o.Package < 0 || o.Package >= len(r.Packages) || o.Count < 1 {
			return nil, nil, fmt.Errorf("occurrences[%d]: a target, a package index and a count of at least 1 are required", i)
		}
		id := o.Target + "\x00" + strconv.Itoa(o.Package)
		if seen[id] {
			return nil, nil, fmt.Errorf("occurrences[%d]: package %d in %q listed twice", i, o.Package, o.Target)
		}
		seen[id] = true
		used[o.Package] = true
		occ[i] = storage.PackageOccurrence{Target: o.Target, PackageKey: pkgs[o.Package].Key, Count: o.Count}
	}
	for i, u := range used {
		if !u {
			return nil, nil, fmt.Errorf("packages[%d] occurs nowhere in the image", i)
		}
	}
	for i, s := range r.Secrets {
		if s.RuleID == "" || s.Target == "" || !validSeverity(s.Severity) {
			return nil, nil, fmt.Errorf("secrets[%d]: rule_id, target and a valid severity are required", i)
		}
	}
	return pkgs, occ, nil
}

func secretCounts(ss []scanSecret) map[string]int {
	out := map[string]int{}
	for _, s := range ss {
		out[s.Severity]++
	}
	return out
}

// decodeStrict decodes a size-limited JSON body, refusing unknown fields.
func decodeStrict(c *gin.Context, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, maxScanResultsBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "document: " + err.Error()})
		return false
	}
	return true
}

// scanPutResults handles PUT /api/v1/scan/results?registry=&repo=&digest=: the
// result of a full scan.
func (h *Handler) scanPutResults(c *gin.Context) {
	if !h.scansReady(c) {
		return
	}
	reg, t, ok := h.scanTarget(c, authz.RegistryScan)
	if !ok {
		return
	}
	var res scanResults
	if !decodeStrict(c, &res) {
		return
	}
	pkgs, occ, err := res.resolve()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	store := h.mgr.StoreFor(reg.Name)
	content, mt, err := store.GetManifest(t.Repo, t.Digest.String())
	if errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "manifest not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	info, err := storage.ParseManifestInfo(mt, content)
	if err != nil || !info.Image {
		c.JSON(http.StatusBadRequest, gin.H{"error": "not an image manifest"})
		return
	}
	options, err := json.Marshal(res.Options)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	secrets, err := json.Marshal(res.Secrets)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	now := time.Now().UTC()
	sbomManifest, sbomBlob, err := storeSBOM(store, t, mt, int64(len(content)), res.SBOM.Document, res.Analyzer, now)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "storing the SBOM: " + err.Error()})
		return
	}
	if err := h.scans.PutFullScan(storage.FullScan{
		ScanTarget: t, Analyzer: res.Analyzer, Options: options,
		SBOMManifest: sbomManifest, SBOMBlob: sbomBlob,
		Secrets: secrets, SecretCounts: secretCounts(res.Secrets), ScannedAt: now,
		Database:    storage.ScanDatabase{Version: res.Database.Version, UpdatedAt: res.Database.UpdatedAt},
		Packages:    pkgs,
		Occurrences: occ,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Only now that the new SBOM is recorded, remove the ones earlier scans
	// attached. Done after the write so a failure leaves an extra SBOM, never
	// none; a repeated submission removes the leftovers.
	if err := removeOtherSBOMs(store, t, sbomManifest); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "removing the previous SBOM: " + err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// scanImageVulnerabilities handles GET /api/v1/scan/vulnerabilities?registry=&repo=&digest=.
func (h *Handler) scanImageVulnerabilities(c *gin.Context) {
	if !h.scansReady(c) {
		return
	}
	_, t, ok := h.scanTarget(c, authz.RegistryRead)
	if !ok {
		return
	}
	s, err := h.scans.GetImageScan(t)
	if errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "image not scanned"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	vulns, err := h.scans.ImageVulnerabilities(t)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"analyzer": s.Analyzer, "scanned_at": s.ScannedAt, "vulnerabilities": vulns})
}

// scanPackages handles GET /api/v1/scan/packages?after=&limit=: the packages
// some image contains, for the daily refresh.
func (h *Handler) scanPackages(c *gin.Context) {
	if !h.scansReady(c) || !h.scanAllRegistries(c) {
		return
	}
	limit, ok := pageSize(c)
	if !ok {
		return
	}
	pkgs, err := h.scans.ListScanPackages(c.Query("after"), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"packages": pkgs})
}

// packageVulnerabilities is the body of PUT /api/v1/scan/packages/vulnerabilities.
type packageVulnerabilities struct {
	Database scanDatabase `json:"database"`
	Options  scanOptions  `json:"options"`
	Packages []struct {
		Key             string              `json:"key"`
		Vulnerabilities []scanVulnerability `json:"vulnerabilities"`
	} `json:"packages"`
}

// scanPutPackageVulnerabilities handles PUT /api/v1/scan/packages/vulnerabilities:
// the refreshed vulnerabilities of some packages. A package matched against a
// newer database already keeps that match.
func (h *Handler) scanPutPackageVulnerabilities(c *gin.Context) {
	if !h.scansReady(c) || !h.scanAllRegistries(c) {
		return
	}
	var body packageVulnerabilities
	if !decodeStrict(c, &body) {
		return
	}
	if err := body.Database.validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(body.Packages) == 0 || len(body.Packages) > maxScanPageSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("between 1 and %d packages per request", maxScanPageSize)})
		return
	}
	refresh := storage.PackageRefresh{
		Database:  storage.ScanDatabase{Version: body.Database.Version, UpdatedAt: body.Database.UpdatedAt},
		ScannedAt: time.Now().UTC(),
	}
	seen := map[string]bool{}
	for i, p := range body.Packages {
		where := fmt.Sprintf("packages[%d]", i)
		if _, err := digest.Parse(p.Key); err != nil {
			return400(c, where+": invalid key")
			return
		}
		if seen[p.Key] {
			return400(c, where+": key listed twice")
			return
		}
		seen[p.Key] = true
		if err := validateVulnerabilities(p.Vulnerabilities, where); err != nil {
			return400(c, err.Error())
			return
		}
		vulns := make([]storage.PackageVulnerability, len(p.Vulnerabilities))
		for k, v := range p.Vulnerabilities {
			vulns[k] = storage.PackageVulnerability{ID: v.ID, Severity: v.Severity, Fixed: v.Fixed, Title: v.Title}
		}
		refresh.Packages = append(refresh.Packages, storage.PackageVulnerabilities{Key: p.Key, Vulnerabilities: vulns})
	}
	options, err := json.Marshal(body.Options)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	refresh.Options = options
	updated, unknown, err := h.scans.RefreshPackages(refresh)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// A package can leave the archive while the refresh runs (its last image
	// was deleted): it is reported, not an error.
	c.JSON(http.StatusOK, gin.H{"updated": updated, "unknown": unknown})
}

func return400(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": msg})
}

// scanPrunePackages handles POST /api/v1/scan/packages/prune: it removes the
// packages no image contains any more.
func (h *Handler) scanPrunePackages(c *gin.Context) {
	if !h.scansReady(c) || !h.scanAllRegistries(c) {
		return
	}
	n, err := h.scans.PruneScanPackages()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": n})
}

// ociDescriptor is a content descriptor inside a manifest.
type ociDescriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Data      string `json:"data,omitempty"`
}

type ociArtifactManifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	ArtifactType  string            `json:"artifactType"`
	Config        ociDescriptor     `json:"config"`
	Layers        []ociDescriptor   `json:"layers"`
	Subject       ociDescriptor     `json:"subject"`
	Annotations   map[string]string `json:"annotations"`
}

// storeSBOM attaches the SBOM document to the image as an OCI 1.1 referrer:
// the document as a blob, an empty config, and a manifest whose subject is the
// image. It returns the manifest and the document digests.
func storeSBOM(store *storage.Store, t storage.ScanTarget, imageMediaType string, imageSize int64,
	document []byte, analyzer string, now time.Time) (digest.Digest, digest.Digest, error) {
	docDigest := digest.FromBytes(document)
	if err := store.PutBlob(t.Repo, docDigest, bytes.NewReader(document)); err != nil {
		return "", "", err
	}
	empty := []byte("{}")
	emptyDigest := digest.FromBytes(empty)
	if err := store.PutBlob(t.Repo, emptyDigest, bytes.NewReader(empty)); err != nil {
		return "", "", err
	}
	m, err := json.Marshal(ociArtifactManifest{
		SchemaVersion: 2,
		MediaType:     storage.MediaTypeOCIManifest,
		ArtifactType:  mediaTypeCycloneDX,
		Config:        ociDescriptor{MediaType: mediaTypeEmptyJSON, Digest: emptyDigest.String(), Size: int64(len(empty)), Data: "e30="},
		Layers:        []ociDescriptor{{MediaType: mediaTypeCycloneDX, Digest: docDigest.String(), Size: int64(len(document))}},
		Subject:       ociDescriptor{MediaType: imageMediaType, Digest: t.Digest.String(), Size: imageSize},
		Annotations: map[string]string{
			annotationCreated:      now.Format(time.RFC3339),
			annotationScanAnalyzer: analyzer,
		},
	})
	if err != nil {
		return "", "", err
	}
	md, err := store.PutManifest(t.Repo, digest.FromBytes(m).String(), storage.MediaTypeOCIManifest, m)
	if err != nil {
		return "", "", err
	}
	for _, b := range []digest.Digest{emptyDigest, docDigest} {
		if err := store.LinkManifestBlob(t.Repo, md, b); err != nil {
			return "", "", err
		}
	}
	return md, docDigest, nil
}

// removeOtherSBOMs deletes the SBOMs earlier scans attached to the image. Only
// SBOMs the registry made (annotated with the analyzer) are touched: an SBOM a
// build pushed next to its image stays.
func removeOtherSBOMs(store *storage.Store, t storage.ScanTarget, keep digest.Digest) error {
	refs, err := store.Referrers(t.Repo, t.Digest)
	if err != nil {
		return err
	}
	for _, r := range refs {
		if r.Digest == keep || r.ArtifactType != mediaTypeCycloneDX {
			continue
		}
		if _, ours := r.Annotations[annotationScanAnalyzer]; !ours {
			continue
		}
		if err := store.DeleteManifest(t.Repo, r.Digest.String()); err != nil {
			return err
		}
	}
	return nil
}

// metrics handles GET /metrics: the scan findings of the registries the caller
// can read, per image digest (docs/scanning.md, "Metrics and alerts").
func (h *Handler) metrics(c *gin.Context) {
	if !h.scansReady(c) {
		return
	}
	regs := h.scanRegistries(c, authz.RegistryRead)
	sums, err := h.scans.ScanSummaries(regs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var b bytes.Buffer
	writeScanMetrics(&b, sums)
	c.Data(http.StatusOK, "text/plain; version=0.0.4; charset=utf-8", b.Bytes())
}

func writeScanMetrics(w io.Writer, sums []storage.ScanSummary) {
	labels := func(s storage.ScanSummary) string {
		return fmt.Sprintf(`registry="%s",repository="%s",image_digest="%s"`,
			promEscape(s.Registry), promEscape(s.Repo), promEscape(s.Digest.String()))
	}
	counts := func(name, help string, pick func(storage.ScanSummary) map[string]int) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
		for _, s := range sums {
			m := pick(s)
			for _, sev := range scanSeverities {
				if n := m[sev]; n > 0 {
					fmt.Fprintf(w, "%s{%s,severity=\"%s\"} %d\n", name, labels(s), sev, n)
				}
			}
		}
	}
	counts("stiva_image_vulnerabilities", "Vulnerabilities of a scanned image, per severity.",
		func(s storage.ScanSummary) map[string]int { return s.VulnerabilityCounts })
	counts("stiva_image_exposedsecrets", "Secrets exposed in a scanned image's layers, per severity.",
		func(s storage.ScanSummary) map[string]int { return s.SecretCounts })
	gauge := func(name, help string, pick func(storage.ScanSummary) time.Time) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
		for _, s := range sums {
			fmt.Fprintf(w, "%s{%s} %d\n", name, labels(s), pick(s).Unix())
		}
	}
	gauge("stiva_image_scan_timestamp_seconds", "Time of the full scan of an image.",
		func(s storage.ScanSummary) time.Time { return s.ScannedAt })
	gauge("stiva_image_vulnerabilities_timestamp_seconds", "Oldest vulnerability match among an image's packages.",
		func(s storage.ScanSummary) time.Time { return s.VulnerabilitiesScannedAt })
}

// promEscape escapes a label value for the Prometheus text format.
func promEscape(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

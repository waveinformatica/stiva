package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// target names one image manifest in Stiva.
type target struct {
	Registry string `json:"registry"`
	Repo     string `json:"repo"`
	Digest   string `json:"digest"`
}

func (t target) String() string { return t.Registry + "/" + t.Repo + "@" + t.Digest }

func (t target) query() url.Values {
	return url.Values{"registry": {t.Registry}, "repo": {t.Repo}, "digest": {t.Digest}}
}

// stiva is the client of the registry's scan API (docs/scanning.md).
type stiva struct {
	base  string
	token string
	http  *http.Client
}

func newStiva(base, token string) *stiva {
	return &stiva{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{Timeout: 10 * time.Minute}}
}

func (s *stiva) do(ctx context.Context, method, path string, q url.Values, body io.Reader) (*http.Response, error) {
	u := s.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return s.http.Do(req)
}

// expect turns any status other than want into an error carrying the body.
func expect(resp *http.Response, want int) error {
	if resp.StatusCode == want {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("%s %s: %s: %s", resp.Request.Method, resp.Request.URL.Path, resp.Status, strings.TrimSpace(string(b)))
}

func (s *stiva) list(ctx context.Context, path string, q url.Values) ([]target, error) {
	resp, err := s.do(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := expect(resp, http.StatusOK); err != nil {
		return nil, err
	}
	var page struct {
		Images []target `json:"images"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, err
	}
	return page.Images, nil
}

// pending lists images that need a full scan by this analyzer.
func (s *stiva) pending(ctx context.Context, analyzer string, limit int) ([]target, error) {
	return s.list(ctx, "/api/v1/scan/pending", url.Values{"analyzer": {analyzer}, "limit": {strconv.Itoa(limit)}})
}

// packages pages through the package archive, after the given key.
func (s *stiva) packages(ctx context.Context, after string, limit int) ([]archivedPackage, error) {
	resp, err := s.do(ctx, http.MethodGet, "/api/v1/scan/packages", url.Values{"after": {after}, "limit": {strconv.Itoa(limit)}}, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := expect(resp, http.StatusOK); err != nil {
		return nil, err
	}
	var page struct {
		Packages []archivedPackage `json:"packages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, err
	}
	return page.Packages, nil
}

// prune removes the packages no image contains any more.
func (s *stiva) prune(ctx context.Context) (int64, error) {
	resp, err := s.do(ctx, http.MethodPost, "/api/v1/scan/packages/prune", nil, nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if err := expect(resp, http.StatusOK); err != nil {
		return 0, err
	}
	var r struct {
		Deleted int64 `json:"deleted"`
	}
	return r.Deleted, json.NewDecoder(resp.Body).Decode(&r)
}

// putPackageVulnerabilities submits refreshed vulnerabilities; it returns the
// keys the registry no longer has.
func (s *stiva) putPackageVulnerabilities(ctx context.Context, doc packageVulnerabilities) ([]string, error) {
	body, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	resp, err := s.do(ctx, http.MethodPut, "/api/v1/scan/packages/vulnerabilities", nil, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := expect(resp, http.StatusOK); err != nil {
		return nil, err
	}
	var r struct {
		Unknown []string `json:"unknown"`
	}
	return r.Unknown, json.NewDecoder(resp.Body).Decode(&r)
}

// putResults submits a results document.
func (s *stiva) putResults(ctx context.Context, t target, doc results) error {
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	resp, err := s.do(ctx, http.MethodPut, "/api/v1/scan/results", t.query(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return expect(resp, http.StatusNoContent)
}

// putFailure records that the image could not be scanned; the registry offers
// it again after a day, or to another analyzer.
func (s *stiva) putFailure(ctx context.Context, t target, analyzer string, cause error) error {
	body, err := json.Marshal(map[string]string{"analyzer": analyzer, "error": cause.Error()})
	if err != nil {
		return err
	}
	resp, err := s.do(ctx, http.MethodPut, "/api/v1/scan/failures", t.query(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return expect(resp, http.StatusNoContent)
}

// content fetches a manifest or a blob of an image through the scan API, which
// addresses registries by name rather than by the host clients use.
func (s *stiva) content(ctx context.Context, kind string, t target) (*http.Response, error) {
	return s.do(ctx, http.MethodGet, "/api/v1/scan/"+kind, t.query(), nil)
}

// results is the body of PUT /api/v1/scan/results: a full scan.
type results struct {
	Analyzer    string          `json:"analyzer"`
	Database    database        `json:"database"`
	Options     options         `json:"options"`
	SBOM        sbomDocument    `json:"sbom"`
	Packages    []scanPackage   `json:"packages"`
	Occurrences []occurrence    `json:"occurrences"`
	Secrets     []exposedSecret `json:"secrets"`
}

type database struct {
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

type options struct {
	IgnoreUnfixed bool   `json:"ignore_unfixed"`
	IgnorePolicy  string `json:"ignore_policy"`
}

type sbomDocument struct {
	Format   string          `json:"format"`
	Document json.RawMessage `json:"document"`
}

// scanPackage is a package of an image: its context, the package document and
// its vulnerabilities.
type scanPackage struct {
	Context         json.RawMessage `json:"context"`
	Package         json.RawMessage `json:"package"`
	Vulnerabilities []vulnerability `json:"vulnerabilities"`
}

// occurrence: the image contains packages[Package] in Target, Count times.
type occurrence struct {
	Target  string `json:"target"`
	Package int    `json:"package"`
	Count   int    `json:"count"`
}

type vulnerability struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Fixed    string `json:"fixed"`
	Title    string `json:"title"`
}

type exposedSecret struct {
	RuleID   string `json:"rule_id"`
	Category string `json:"category"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Target   string `json:"target"`
	Line     int    `json:"line"`
}

// archivedPackage is a package of the registry's archive.
type archivedPackage struct {
	Key     string          `json:"key"`
	Context json.RawMessage `json:"context"`
	Package json.RawMessage `json:"package"`
	PURL    string          `json:"purl"`
}

// packageVulnerabilities is the body of PUT /api/v1/scan/packages/vulnerabilities.
type packageVulnerabilities struct {
	Database database             `json:"database"`
	Options  options              `json:"options"`
	Packages []keyVulnerabilities `json:"packages"`
}

type keyVulnerabilities struct {
	Key             string          `json:"key"`
	Vulnerabilities []vulnerability `json:"vulnerabilities"`
}

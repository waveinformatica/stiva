package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"registry/internal/digest"
)

// Upstream speaks the OCI Distribution API against a remote registry. It is used
// by proxy registries to fetch (and, when write-through is enabled, to push)
// content from/to an upstream source.
//
// Authentication follows the OCI distribution handshake: explicitly configured
// credentials (static token, token URL or basic auth) are sent up front, and a
// 401 carrying a Bearer challenge is answered by fetching a token from the
// challenge realm and retrying once. Registries such as Docker Hub mandate this
// flow even for public images, answering every anonymous request with 401.
type Upstream struct {
	base     string // e.g. https://registry-1.docker.io
	user     string
	pass     string
	token    string
	tokenURL string

	client      *http.Client
	mu          sync.Mutex
	cachedToken string
	cachedUntil time.Time
	bearers     map[string]cachedBearer // challenge tokens, keyed by realm/service/scope
}

// bearerChallenge is a parsed WWW-Authenticate Bearer challenge.
type bearerChallenge struct {
	realm   string
	service string
	scope   string
}

// cachedBearer is a challenge token with its expiry.
type cachedBearer struct {
	token string
	until time.Time
}

// NewUpstream builds an upstream client for a proxy registry.
func NewUpstream(remoteURL, user, pass, token, tokenURL string) *Upstream {
	return &Upstream{
		base:     strings.TrimRight(remoteURL, "/"),
		user:     user,
		pass:     pass,
		token:    token,
		tokenURL: tokenURL,
		client:   &http.Client{Timeout: 5 * time.Minute},
	}
}

// upstreamToken resolves a bearer token for the upstream when a token URL is set.
func (u *Upstream) upstreamToken(ctx context.Context) (string, error) {
	if u.token != "" {
		return u.token, nil
	}
	if u.tokenURL == "" {
		return "", nil
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.cachedToken != "" && time.Now().Before(u.cachedUntil) {
		return u.cachedToken, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.tokenURL, nil)
	if err != nil {
		return "", err
	}
	if u.user != "" {
		req.SetBasicAuth(u.user, u.pass)
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("upstream token endpoint returned %d", resp.StatusCode)
	}
	var tr struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", err
	}
	tok := tr.Token
	if tok == "" {
		tok = tr.AccessToken
	}
	u.cachedToken = tok
	u.cachedUntil = time.Now().Add(4 * time.Minute)
	return tok, nil
}

// auth applies the appropriate authorization to a request.
func (u *Upstream) auth(ctx context.Context, req *http.Request) error {
	if u.token != "" {
		req.Header.Set("Authorization", "Bearer "+u.token)
		return nil
	}
	if u.tokenURL != "" {
		tok, err := u.upstreamToken(ctx)
		if err != nil {
			return err
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
			return nil
		}
	}
	if u.user != "" {
		req.SetBasicAuth(u.user, u.pass)
	}
	return nil
}

func (u *Upstream) do(ctx context.Context, method, path string, body io.Reader, hdr http.Header) (*http.Response, error) {
	resp, err := u.roundTrip(ctx, method, path, body, hdr, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	ch := parseBearerChallenge(resp.Header.Get("Www-Authenticate"))
	if ch == nil {
		return resp, nil
	}
	tok, terr := u.bearerToken(ctx, ch)
	if terr != nil {
		resp.Body.Close()
		return nil, terr
	}
	resp.Body.Close()
	if body != nil {
		rs, ok := body.(io.Seeker)
		if !ok {
			return nil, fmt.Errorf("upstream %s %s: unauthorized (request body cannot be replayed)", method, path)
		}
		if _, serr := rs.Seek(0, io.SeekStart); serr != nil {
			return nil, fmt.Errorf("upstream %s %s: unauthorized (request body cannot be replayed)", method, path)
		}
	}
	return u.roundTrip(ctx, method, path, body, hdr, tok)
}

func (u *Upstream) roundTrip(ctx context.Context, method, path string, body io.Reader, hdr http.Header, bearer string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.base+path, body)
	if err != nil {
		return nil, err
	}
	if hdr != nil {
		for k, vs := range hdr {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
	}
	req.Header.Set("Docker-Distribution-Api-Version", "registry/2.0")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	} else if err := u.auth(ctx, req); err != nil {
		return nil, err
	}
	return u.client.Do(req)
}

// bearerParam matches the key="value" pairs of a Bearer challenge, e.g.
// Bearer realm="https://auth.docker.io/token",service="registry.docker.io".
var bearerParam = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9_-]*)\s*=\s*"([^"]*)"`)

// parseBearerChallenge extracts realm, service and scope from a
// WWW-Authenticate header. It returns nil when the header carries no Bearer
// challenge with a realm to ask for a token.
func parseBearerChallenge(header string) *bearerChallenge {
	i := strings.Index(strings.ToLower(header), "bearer")
	if i < 0 {
		return nil
	}
	ch := &bearerChallenge{}
	for _, m := range bearerParam.FindAllStringSubmatch(header[i:], -1) {
		switch strings.ToLower(m[1]) {
		case "realm":
			ch.realm = m[2]
		case "service":
			ch.service = m[2]
		case "scope":
			ch.scope = m[2]
		}
	}
	if ch.realm == "" {
		return nil
	}
	return ch
}

// bearerToken returns a cached challenge token for realm/service/scope, fetching
// a fresh one when none is cached or the cached one expired. Concurrent callers
// may fetch twice; the last write wins and both tokens are valid.
func (u *Upstream) bearerToken(ctx context.Context, ch *bearerChallenge) (string, error) {
	key := ch.realm + "\x00" + ch.service + "\x00" + ch.scope
	u.mu.Lock()
	cb, ok := u.bearers[key]
	u.mu.Unlock()
	if ok && time.Now().Before(cb.until) {
		return cb.token, nil
	}
	tok, ttl, err := fetchBearerToken(ctx, u.client, ch, u.user, u.pass)
	if err != nil {
		return "", err
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	skew := 30 * time.Second
	if ttl < 2*skew {
		skew = ttl / 2
	}
	u.mu.Lock()
	if u.bearers == nil {
		u.bearers = make(map[string]cachedBearer)
	}
	u.bearers[key] = cachedBearer{token: tok, until: time.Now().Add(ttl - skew)}
	u.mu.Unlock()
	return tok, nil
}

// fetchBearerToken asks the challenge realm for a token. When the proxy carries
// upstream credentials they are sent as basic auth, which is how registries
// grant higher rate limits or access to private repositories.
func fetchBearerToken(ctx context.Context, client *http.Client, ch *bearerChallenge, user, pass string) (string, time.Duration, error) {
	q := url.Values{}
	if ch.service != "" {
		q.Set("service", ch.service)
	}
	if ch.scope != "" {
		q.Set("scope", ch.scope)
	}
	target := ch.realm
	if strings.Contains(target, "?") {
		target += "&" + q.Encode()
	} else {
		target += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", 0, err
	}
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("upstream token endpoint returned %d", resp.StatusCode)
	}
	var tr struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", 0, err
	}
	tok := tr.Token
	if tok == "" {
		tok = tr.AccessToken
	}
	if tok == "" {
		return "", 0, fmt.Errorf("upstream token endpoint returned no token")
	}
	return tok, time.Duration(tr.ExpiresIn) * time.Second, nil
}

// GetBlobReader fetches a blob from the upstream.
func (u *Upstream) GetBlobReader(repo string, d digest.Digest) (io.ReadCloser, int64, error) {
	ctx := context.Background()
	resp, err := u.do(ctx, http.MethodGet, "/v2/"+repo+"/blobs/"+d.String(), nil, nil)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, 0, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("upstream get blob: %d", resp.StatusCode)
	}
	return resp.Body, resp.ContentLength, nil
}

// StatBlob returns the size of an upstream blob via HEAD.
func (u *Upstream) StatBlob(repo string, d digest.Digest) (int64, error) {
	ctx := context.Background()
	resp, err := u.do(ctx, http.MethodHead, "/v2/"+repo+"/blobs/"+d.String(), nil, nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return 0, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("upstream stat blob: %d", resp.StatusCode)
	}
	return strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
}

// GetManifest fetches a manifest from the upstream.
func (u *Upstream) GetManifest(repo, reference string) ([]byte, string, error) {
	ctx := context.Background()
	resp, err := u.do(ctx, http.MethodGet, "/v2/"+repo+"/manifests/"+reference, nil, http.Header{
		"Accept": {manifestAccept},
	})
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("upstream get manifest: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// ListTags returns the tag list for an upstream repo.
func (u *Upstream) ListTags(repo string) ([]string, error) {
	ctx := context.Background()
	resp, err := u.do(ctx, http.MethodGet, "/v2/"+repo+"/tags/list", nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream list tags: %d", resp.StatusCode)
	}
	var out struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Tags == nil {
		return []string{}, nil
	}
	return out.Tags, nil
}

// NewUpload starts a blob upload session on the upstream and returns its session URL.
func (u *Upstream) NewUpload(repo string) (string, error) {
	ctx := context.Background()
	resp, err := u.do(ctx, http.MethodPost, "/v2/"+repo+"/blobs/uploads", nil, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("upstream start upload: %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("upstream start upload: missing Location")
	}
	return resolveUpstreamURL(u.base, loc), nil
}

// PatchUpload appends bytes to an upstream upload session.
func (u *Upstream) PatchUpload(sessionURL string, r io.Reader) error {
	ctx := context.Background()
	resp, err := u.do(ctx, http.MethodPatch, sessionURL, r, http.Header{
		"Content-Type": {"application/octet-stream"},
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("upstream patch upload: %d", resp.StatusCode)
	}
	return nil
}

// UploadOffset returns the current offset of an upstream upload session.
func (u *Upstream) UploadOffset(sessionURL string) (int64, error) {
	ctx := context.Background()
	resp, err := u.do(ctx, http.MethodGet, sessionURL, nil, nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return 0, fmt.Errorf("upstream upload offset: %d", resp.StatusCode)
	}
	r := resp.Header.Get("Range") // "0-123"
	if r == "" {
		return 0, nil
	}
	parts := strings.Split(r, "-")
	if len(parts) != 2 {
		return 0, nil
	}
	return strconv.ParseInt(parts[1], 10, 64)
}

// CommitUpload completes an upstream upload session.
func (u *Upstream) CommitUpload(sessionURL string, d digest.Digest) error {
	ctx := context.Background()
	sep := "?"
	if strings.Contains(sessionURL, "?") {
		sep = "&"
	}
	resp, err := u.do(ctx, http.MethodPut, sessionURL+sep+"digest="+url.QueryEscape(d.String()), nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upstream commit upload: %d", resp.StatusCode)
	}
	return nil
}

// CancelUpload deletes an upstream upload session.
func (u *Upstream) CancelUpload(sessionURL string) error {
	ctx := context.Background()
	resp, err := u.do(ctx, http.MethodDelete, sessionURL, nil, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// PatchUploadMonolithic performs a single-POST blob upload (?digest=) to the upstream.
func (u *Upstream) PatchUploadMonolithic(repo string, d digest.Digest, r io.Reader) error {
	ctx := context.Background()
	resp, err := u.do(ctx, http.MethodPost, "/v2/"+repo+"/blobs/uploads?digest="+url.QueryEscape(d.String()), r, http.Header{
		"Content-Type": {"application/octet-stream"},
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upstream monolithic upload: %d", resp.StatusCode)
	}
	return nil
}

// PutManifest pushes a manifest to the upstream.
func (u *Upstream) PutManifest(repo, reference, mediaType string, content []byte) error {
	ctx := context.Background()
	resp, err := u.do(ctx, http.MethodPut, "/v2/"+repo+"/manifests/"+reference, bytes.NewReader(content), http.Header{
		"Content-Type": {mediaType},
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upstream put manifest: %d", resp.StatusCode)
	}
	return nil
}

// resolveUpstreamURL makes an upstream Location absolute against the base URL.
func resolveUpstreamURL(base, loc string) string {
	if strings.HasPrefix(loc, "http://") || strings.HasPrefix(loc, "https://") {
		return loc
	}
	return strings.TrimRight(base, "/") + loc
}

// GetObject fetches an arbitrary artifact by path from a plain-HTTP upstream
// (maven/npm/helm repositories). The path is appended to the upstream base URL.
func (u *Upstream) GetObject(p string) (io.ReadCloser, int64, string, error) {
	ctx := context.Background()
	resp, err := u.do(ctx, http.MethodGet, "/"+strings.TrimLeft(p, "/"), nil, nil)
	if err != nil {
		return nil, 0, "", err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, 0, "", ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, 0, "", fmt.Errorf("upstream get object: %d", resp.StatusCode)
	}
	return resp.Body, resp.ContentLength, resp.Header.Get("Content-Type"), nil
}

// ObjectHead returns size and content type for an upstream artifact.
func (u *Upstream) ObjectHead(p string) (int64, string, error) {
	ctx := context.Background()
	resp, err := u.do(ctx, http.MethodHead, "/"+strings.TrimLeft(p, "/"), nil, nil)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return 0, "", ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("upstream head object: %d", resp.StatusCode)
	}
	return resp.ContentLength, resp.Header.Get("Content-Type"), nil
}

const manifestAccept = "application/vnd.docker.distribution.manifest.v2+json,application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.oci.image.index.v1+json"

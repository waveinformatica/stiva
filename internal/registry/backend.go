package registry

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"sync"

	"registry/internal/digest"
	"registry/internal/storage"
)

func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte("fallback"))
	}
	return hex.EncodeToString(b)
}

// ErrNotFound is returned when content is not present locally or upstream.
var ErrNotFound = storage.ErrNotFound

// Backend is the uniform OCI surface the API handlers talk to. Each registry
// type (hosted, proxy, group) provides its own implementation, so the handlers
// contain no type-specific logic.
type Backend interface {
	// Reads
	GetBlob(repo string, d digest.Digest) (io.ReadCloser, int64, error)
	StatBlob(repo string, d digest.Digest) (int64, error)
	GetManifest(repo, reference string) ([]byte, string, error)

	// Writes
	PutBlob(repo string, d digest.Digest, r io.Reader) error
	PutManifest(repo, reference, mediaType string, content []byte) (digest.Digest, error)
	DeleteManifest(repo, reference string) error
	DeleteBlob(d digest.Digest) error
	ListTags(repo string) ([]string, error)
	LinkManifestBlob(repo string, manifest, blob digest.Digest) error

	// Upload sessions
	NewUpload(repo string) (*storage.Upload, error)
	GetUpload(id string) (*storage.Upload, error)
	AppendUpload(u *storage.Upload, r io.Reader) (int64, error)
	UploadOffset(u *storage.Upload) (int64, error)
	CommitUpload(u *storage.Upload, d digest.Digest) error
	CancelUpload(u *storage.Upload) error
}

// ---- hosted: a plain per-registry store ----

type hostedBackend struct {
	*storage.Store
}

// ---- proxy: cache locally, fall back to and write through an upstream ----

type proxyBackend struct {
	*storage.Store
	up *Upstream

	mu       sync.Mutex
	sessions map[string]string // local upload id -> upstream session URL
}

func newProxyBackend(cache *storage.Store, up *Upstream) *proxyBackend {
	return &proxyBackend{Store: cache, up: up, sessions: make(map[string]string)}
}

// urepo maps a client-side repo path onto the name this proxy's upstream
// expects (Docker Hub wants "library/ubuntu" for "ubuntu"). The local cache
// keeps keying on the client-side name, so one repo has exactly one identity
// no matter which upstream naming it maps to.
func (p *proxyBackend) urepo(repo string) string {
	return upstreamRepoName(p.up.base, repo)
}

func (p *proxyBackend) GetBlob(repo string, d digest.Digest) (io.ReadCloser, int64, error) {
	rc, sz, err := p.Store.GetBlob(repo, d)
	if err == nil {
		return rc, sz, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return nil, 0, err
	}
	// Cache miss: pull from upstream and cache on close.
	urc, usz, uerr := p.up.GetBlobReader(p.urepo(repo), d)
	if uerr != nil {
		return nil, 0, uerr
	}
	tmp, terr := os.CreateTemp("", "pxblob-")
	if terr != nil {
		urc.Close()
		return nil, 0, terr
	}
	if _, cerr := io.Copy(tmp, urc); cerr != nil {
		urc.Close()
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, 0, cerr
	}
	urc.Close()
	tmp.Close()
	// Commit to local cache.
	if f, oerr := os.Open(tmp.Name()); oerr == nil {
		_ = p.Store.PutBlob(repo, d, f)
		f.Close()
	}
	f, rerr := os.Open(tmp.Name())
	if rerr != nil {
		os.Remove(tmp.Name())
		return nil, 0, rerr
	}
	return &tempFileReader{f: f, path: tmp.Name()}, usz, nil
}

// tempFileReader serves a temp file to the client and deletes it afterwards.
type tempFileReader struct {
	f    *os.File
	path string
	done bool
}

func (t *tempFileReader) Read(p []byte) (int, error) { return t.f.Read(p) }
func (t *tempFileReader) Close() error {
	err := t.f.Close()
	if !t.done {
		t.done = true
		os.Remove(t.path)
	}
	return err
}

func (p *proxyBackend) StatBlob(repo string, d digest.Digest) (int64, error) {
	sz, err := p.Store.StatBlob(repo, d)
	if err == nil {
		return sz, nil
	}
	return p.up.StatBlob(p.urepo(repo), d)
}

func (p *proxyBackend) GetManifest(repo, reference string) ([]byte, string, error) {
	content, mt, err := p.Store.GetManifest(repo, reference)
	if err == nil {
		return content, mt, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return nil, "", err
	}
	uc, umt, uerr := p.up.GetManifest(p.urepo(repo), reference)
	if uerr != nil {
		return nil, "", uerr
	}
	// Cache locally for next time.
	_, _ = p.Store.PutManifest(repo, reference, umt, uc)
	return uc, umt, nil
}

func (p *proxyBackend) PutManifest(repo, reference, mediaType string, content []byte) (digest.Digest, error) {
	if err := p.up.PutManifest(p.urepo(repo), reference, mediaType, content); err != nil {
		return "", err
	}
	return p.Store.PutManifest(repo, reference, mediaType, content)
}

func (p *proxyBackend) PutBlob(repo string, d digest.Digest, r io.Reader) error {
	if err := p.up.PatchUploadMonolithic(p.urepo(repo), d, r); err != nil {
		return err
	}
	return p.Store.PutBlob(repo, d, r)
}

func (p *proxyBackend) DeleteManifest(repo, reference string) error {
	// Best-effort local cache invalidation; upstreams often forbid deletes.
	return p.Store.DeleteManifest(repo, reference)
}

func (p *proxyBackend) DeleteBlob(d digest.Digest) error {
	return p.Store.DeleteBlob(d)
}

func (p *proxyBackend) NewUpload(repo string) (*storage.Upload, error) {
	url, err := p.up.NewUpload(p.urepo(repo))
	if err != nil {
		return nil, err
	}
	id := newSessionID()
	p.mu.Lock()
	p.sessions[id] = url
	p.mu.Unlock()
	return &storage.Upload{ID: id, Repo: repo, Path: url}, nil
}

func (p *proxyBackend) GetUpload(id string) (*storage.Upload, error) {
	p.mu.Lock()
	url, ok := p.sessions[id]
	p.mu.Unlock()
	if !ok {
		return nil, ErrNotFound
	}
	return &storage.Upload{ID: id, Path: url}, nil
}

func (p *proxyBackend) AppendUpload(u *storage.Upload, r io.Reader) (int64, error) {
	if err := p.up.PatchUpload(u.Path, r); err != nil {
		return 0, err
	}
	off, err := p.up.UploadOffset(u.Path)
	if err != nil {
		return 0, err
	}
	return off + 1, nil
}

func (p *proxyBackend) UploadOffset(u *storage.Upload) (int64, error) {
	off, err := p.up.UploadOffset(u.Path)
	if err != nil {
		return 0, err
	}
	return off + 1, nil
}

func (p *proxyBackend) CommitUpload(u *storage.Upload, d digest.Digest) error {
	return p.up.CommitUpload(u.Path, d)
}

func (p *proxyBackend) CancelUpload(u *storage.Upload) error {
	err := p.up.CancelUpload(u.Path)
	p.mu.Lock()
	delete(p.sessions, u.ID)
	p.mu.Unlock()
	return err
}

// ---- group: aggregate reads across members, write to one member ----

type groupBackend struct {
	members []Backend
	write   Backend
}

func (g *groupBackend) GetBlob(repo string, d digest.Digest) (io.ReadCloser, int64, error) {
	for _, m := range g.members {
		rc, sz, err := m.GetBlob(repo, d)
		if err == nil {
			return rc, sz, nil
		}
	}
	return nil, 0, ErrNotFound
}

func (g *groupBackend) StatBlob(repo string, d digest.Digest) (int64, error) {
	for _, m := range g.members {
		if sz, err := m.StatBlob(repo, d); err == nil {
			return sz, nil
		}
	}
	return 0, ErrNotFound
}

func (g *groupBackend) GetManifest(repo, reference string) ([]byte, string, error) {
	for _, m := range g.members {
		c, mt, err := m.GetManifest(repo, reference)
		if err == nil {
			return c, mt, nil
		}
	}
	return nil, "", ErrNotFound
}

func (g *groupBackend) PutBlob(repo string, d digest.Digest, r io.Reader) error {
	return g.write.PutBlob(repo, d, r)
}

func (g *groupBackend) PutManifest(repo, reference, mediaType string, content []byte) (digest.Digest, error) {
	return g.write.PutManifest(repo, reference, mediaType, content)
}

func (g *groupBackend) DeleteManifest(repo, reference string) error {
	return g.write.DeleteManifest(repo, reference)
}

func (g *groupBackend) DeleteBlob(d digest.Digest) error {
	return g.write.DeleteBlob(d)
}

func (g *groupBackend) ListTags(repo string) ([]string, error) {
	for _, m := range g.members {
		if tags, err := m.ListTags(repo); err == nil && len(tags) > 0 {
			return tags, nil
		}
	}
	return []string{}, nil
}

func (g *groupBackend) LinkManifestBlob(repo string, manifest, blob digest.Digest) error {
	return g.write.LinkManifestBlob(repo, manifest, blob)
}

func (g *groupBackend) NewUpload(repo string) (*storage.Upload, error) {
	return g.write.NewUpload(repo)
}
func (g *groupBackend) GetUpload(id string) (*storage.Upload, error) { return g.write.GetUpload(id) }
func (g *groupBackend) AppendUpload(u *storage.Upload, r io.Reader) (int64, error) {
	return g.write.AppendUpload(u, r)
}
func (g *groupBackend) UploadOffset(u *storage.Upload) (int64, error) { return g.write.UploadOffset(u) }
func (g *groupBackend) CommitUpload(u *storage.Upload, d digest.Digest) error {
	return g.write.CommitUpload(u, d)
}
func (g *groupBackend) CancelUpload(u *storage.Upload) error { return g.write.CancelUpload(u) }

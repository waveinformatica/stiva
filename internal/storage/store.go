package storage

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"registry/internal/digest"
)

func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Extremely unlikely; fall back to timestamp-based id.
		return hex.EncodeToString([]byte(fmt.Sprintf("%d", os.Getpid())))
	}
	return hex.EncodeToString(b)
}

// Store is the composed persistence layer for a single registry: blob content +
// metadata + uploads. All metadata is partitioned by the registry name.
type Store struct {
	registry string
	blobs    BlobStore
	meta     MetadataStore
	uploads  string

	mu       sync.Mutex
	uploadsM map[string]*Upload
}

// Upload represents an in-progress blob upload session.
type Upload struct {
	ID     string
	Repo   string
	Path   string
	Digest digest.Digest
}

// NewStoreForRegistry constructs a per-registry Store. Uploads are staged under
// uploadsDir/<registry> so concurrent uploads for different registries never collide.
func NewStoreForRegistry(meta MetadataStore, blobs BlobStore, uploadsDir, registry string) (*Store, error) {
	dir := filepath.Join(uploadsDir, registry)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{
		registry: registry,
		blobs:    blobs,
		meta:     meta,
		uploads:  dir,
		uploadsM: make(map[string]*Upload),
	}, nil
}

// Registry returns the registry name this store is scoped to.
func (s *Store) Registry() string { return s.registry }

// ---- Blob operations ----

func (s *Store) GetBlob(repo string, d digest.Digest) (io.ReadCloser, int64, error) {
	return s.blobs.Get(d)
}

func (s *Store) StatBlob(repo string, d digest.Digest) (int64, error) {
	sz, err := s.blobs.Stat(d)
	if err == nil {
		return sz, nil
	}
	// Fall back to metadata (e.g. blob stored externally later).
	return s.meta.BlobSize(s.registry, d)
}

func (s *Store) PutBlob(repo string, d digest.Digest, r io.Reader) error {
	if s.blobs.Exists(d) {
		// Already present; still record metadata.
		if sz, err := s.blobs.Stat(d); err == nil {
			return s.meta.RecordBlob(s.registry, d, sz)
		}
	}
	if err := s.blobs.Put(d, r); err != nil {
		return err
	}
	sz, err := s.blobs.Stat(d)
	if err != nil {
		return err
	}
	return s.meta.RecordBlob(s.registry, d, sz)
}

func (s *Store) DeleteBlob(d digest.Digest) error {
	refs, err := s.meta.BlobRefCount(s.registry, d)
	if err != nil {
		return err
	}
	if refs > 0 {
		return fmt.Errorf("registry/storage: blob %s is referenced by %d manifest(s)", d, refs)
	}
	if s.blobs.Exists(d) {
		if err := s.blobs.Delete(d); err != nil && err != ErrNotFound {
			return err
		}
	}
	return s.meta.DeleteBlobMeta(s.registry, d)
}

// ---- Manifest operations ----

// ResolveReference resolves a tag or digest reference to a concrete digest.
func (s *Store) ResolveReference(repo, reference string) (digest.Digest, error) {
	if d, err := digest.Parse(reference); err == nil {
		return d, nil
	}
	return s.meta.ResolveTag(s.registry, repo, reference)
}

func (s *Store) GetManifest(repo, reference string) ([]byte, string, error) {
	d, err := s.ResolveReference(repo, reference)
	if err != nil {
		return nil, "", err
	}
	exists, err := s.meta.ManifestExists(s.registry, repo, d)
	if err != nil {
		return nil, "", err
	}
	if !exists {
		return nil, "", ErrNotFound
	}
	return s.meta.GetManifest(s.registry, repo, d)
}

func (s *Store) PutManifest(repo, reference, mediaType string, content []byte) (digest.Digest, error) {
	d := digest.FromBytes(content)
	author, created := s.provenanceFromConfig(content)
	if err := s.meta.PutManifest(s.registry, repo, d, mediaType, author, created, content); err != nil {
		return "", err
	}
	// If reference is a tag (not a digest), record the tag.
	if _, err := digest.Parse(reference); err != nil {
		if err := s.meta.SetTag(s.registry, repo, reference, d); err != nil {
			return "", err
		}
	}
	return d, nil
}

// provenanceFromConfig extracts the image author and creation time from the
// image config referenced by a single manifest. Indexes reference no config,
// and the config blob may simply not be here (e.g. proxied manifests): both
// yield empty strings, never an error.
func (s *Store) provenanceFromConfig(content []byte) (string, string) {
	var m struct {
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
	}
	if err := json.Unmarshal(content, &m); err != nil || m.Config.Digest == "" {
		return "", ""
	}
	d, err := digest.Parse(m.Config.Digest)
	if err != nil {
		return "", ""
	}
	rc, _, err := s.blobs.Get(d)
	if err != nil {
		return "", ""
	}
	defer rc.Close()
	// Configs are small JSON documents; cap the read defensively.
	cfg, err := io.ReadAll(io.LimitReader(rc, 4<<20))
	if err != nil {
		return "", ""
	}
	var c struct {
		Author  string `json:"author"`
		Created string `json:"created"`
	}
	if err := json.Unmarshal(cfg, &c); err != nil {
		return "", ""
	}
	return c.Author, c.Created
}

// EnsureProvenance returns the recorded author/creation time for a manifest,
// filling the row from the config blob when it was pushed before provenance
// was recorded. Old images heal on first view; new pushes record at PUT.
func (s *Store) EnsureProvenance(repo, ref string) (string, string) {
	d, err := s.ResolveReference(repo, ref)
	if err != nil {
		return "", ""
	}
	if author, created, err := s.meta.ManifestProvenance(s.registry, repo, d); err == nil && (author != "" || created != "") {
		return author, created
	}
	content, _, err := s.meta.GetManifest(s.registry, repo, d)
	if err != nil {
		return "", ""
	}
	author, created := s.provenanceFromConfig(content)
	if author != "" || created != "" {
		_ = s.meta.SetManifestProvenance(s.registry, repo, d, author, created)
	}
	return author, created
}

func (s *Store) DeleteManifest(repo, reference string) error {
	d, err := s.ResolveReference(repo, reference)
	if err != nil {
		return err
	}
	return s.meta.DeleteManifest(s.registry, repo, d)
}

func (s *Store) ListTags(repo string) ([]string, error) {
	return s.meta.ListTags(s.registry, repo)
}

func (s *Store) ListTagInfos(repo string) ([]TagInfo, error) {
	return s.meta.ListTagInfos(s.registry, repo)
}

// TagsForBlob lists the tags in repo whose image has the blob as its TOP
// layer (last entry of the manifest's layer list): the image the layer was
// built for, not every image inheriting it as a base. Resolution walks one
// index level (tag -> index -> child manifests).
func (s *Store) TagsForBlob(repo string, blob digest.Digest) ([]TagInfo, error) {
	infos, err := s.meta.ListTagInfos(s.registry, repo)
	if err != nil {
		return nil, err
	}
	var out []TagInfo
	for _, ti := range infos {
		d, err := digest.Parse(ti.Digest)
		if err != nil {
			continue
		}
		content, _, err := s.meta.GetManifest(s.registry, repo, d)
		if err != nil {
			continue
		}
		if manifestToppedBy(s, repo, content, blob) {
			out = append(out, ti)
		}
	}
	return out, nil
}

// manifestToppedBy reports whether blob is the last layer of the manifest,
// descending one index level into children.
func manifestToppedBy(s *Store, repo string, content []byte, blob digest.Digest) bool {
	if isTopLayer(content, blob.String()) {
		return true
	}
	var m struct {
		Manifests []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
	}
	if json.Unmarshal(content, &m) != nil {
		return false
	}
	for _, c := range m.Manifests {
		cd, err := digest.Parse(c.Digest)
		if err != nil {
			continue
		}
		cc, _, err := s.meta.GetManifest(s.registry, repo, cd)
		if err != nil {
			continue
		}
		if isTopLayer(cc, blob.String()) {
			return true
		}
	}
	return false
}

// isTopLayer reports whether blobDigest is the last entry of a single
// manifest's layer list. Pure: unit-tested.
func isTopLayer(content []byte, blobDigest string) bool {
	var m struct {
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
	}
	if json.Unmarshal(content, &m) != nil || len(m.Layers) == 0 {
		return false
	}
	return m.Layers[len(m.Layers)-1].Digest == blobDigest
}

func (s *Store) ListRepos() ([]string, error) {
	return s.meta.ListRepos(s.registry)
}

func (s *Store) CreateRepo(repo string) error {
	return s.meta.CreateRepo(s.registry, repo)
}

// DeleteRepo removes a repository and all of its metadata.
func (s *Store) DeleteRepo(name string) error {
	return s.meta.DeleteRepo(s.registry, name)
}

// LinkManifestBlob records that a manifest references a blob (layer/config).
func (s *Store) LinkManifestBlob(repo string, manifest, blob digest.Digest) error {
	return s.meta.LinkBlob(s.registry, repo, manifest, blob)
}

// ---- Upload sessions ----

func (s *Store) NewUpload(repo string) (*Upload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := newUUID()
	u := &Upload{
		ID:   id,
		Repo: repo,
		Path: filepath.Join(s.uploads, id),
	}
	f, err := os.Create(u.Path)
	if err != nil {
		return nil, err
	}
	f.Close()
	s.uploadsM[id] = u
	return u, nil
}

func (s *Store) GetUpload(id string) (*Upload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.uploadsM[id]
	if !ok {
		// Recover from disk if the file exists (survives restart).
		p := filepath.Join(s.uploads, id)
		if _, err := os.Stat(p); err != nil {
			return nil, ErrNotFound
		}
		u = &Upload{ID: id, Path: p}
		s.uploadsM[id] = u
	}
	return u, nil
}

func (s *Store) AppendUpload(u *Upload, r io.Reader) (int64, error) {
	f, err := os.OpenFile(u.Path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return 0, err
	}
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (s *Store) UploadOffset(u *Upload) (int64, error) {
	info, err := os.Stat(u.Path)
	if err != nil {
		return 0, ErrNotFound
	}
	return info.Size(), nil
}

// CommitUpload finalizes an upload by verifying the digest and promoting the blob.
func (s *Store) CommitUpload(u *Upload, d digest.Digest) error {
	f, err := os.Open(u.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := s.PutBlob(u.Repo, d, f); err != nil && !errors.Is(err, ErrBlobExists) {
		return err
	}
	s.removeUpload(u)
	return nil
}

func (s *Store) CancelUpload(u *Upload) error {
	s.removeUpload(u)
	return os.Remove(u.Path)
}

func (s *Store) removeUpload(u *Upload) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.uploadsM, u.ID)
}

// ---- Objects (path-based artifacts: helm charts, maven/npm packages) ----

// PutObject stores an arbitrary artifact addressed by its path, computing the
// content digest and recording path->digest metadata. Content is deduplicated
// at the blob layer by digest.
func (s *Store) PutObject(p, contentType string, r io.Reader) error {
	tmp, err := os.CreateTemp("", "obj-")
	if err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(tmp, io.TeeReader(r, h)); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	d := digest.Digest("sha256:" + digest.EncodeHex(h.Sum(nil)))
	if f, err := os.Open(tmp.Name()); err == nil {
		if perr := s.PutBlob(s.registry, d, f); perr != nil && perr != ErrBlobExists {
			f.Close()
			os.Remove(tmp.Name())
			return perr
		}
		f.Close()
	}
	os.Remove(tmp.Name())
	sz, err := s.blobs.Stat(d)
	if err != nil {
		return err
	}
	return s.meta.RecordObject(s.registry, p, d, sz, contentType)
}

// GetObject returns the artifact stored at path.
func (s *Store) GetObject(p string) (io.ReadCloser, int64, string, error) {
	d, sz, ct, err := s.meta.GetObjectMeta(s.registry, p)
	if err != nil {
		return nil, 0, "", err
	}
	rc, _, err := s.blobs.Get(d)
	if err != nil {
		return nil, 0, "", err
	}
	return rc, sz, ct, nil
}

// StatObject returns size and content type for a stored artifact.
func (s *Store) StatObject(p string) (int64, string, error) {
	_, sz, ct, err := s.meta.GetObjectMeta(s.registry, p)
	return sz, ct, err
}

// DeleteObject removes the path->digest mapping (the underlying blob is left for
// the GC to reclaim; it may be shared by other paths).
func (s *Store) DeleteObject(p string) error {
	return s.meta.DeleteObject(s.registry, p)
}

// ListObjects returns all stored paths under a prefix.
func (s *Store) ListObjects(prefix string) ([]string, error) {
	return s.meta.ListObjects(s.registry, prefix)
}

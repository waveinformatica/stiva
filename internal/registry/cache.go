package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"registry/internal/digest"
	"registry/internal/storage"
)

// Warmer is implemented by backends that can proactively pull content into their
// local store. A cache registry uses it to pre-warm images discovered in a
// Kubernetes cluster (see the pre-warm controller). It is intentionally not part
// of the core Backend interface: hosted/proxy/group registries are not warmers.
type Warmer interface {
	WarmImage(ctx context.Context, repo, reference string) error
}

var errCacheReadOnly = fmt.Errorf("cache registry is read-only")

// cacheBackend is a transparent multi-upstream pull-through cache. Every read is
// served from the local store and, on a miss, fetched from the upstream that the
// repo path resolves to (host-less repos => Docker Hub, anything with a registry
// host segment => that host). Writes are rejected: a cache is populated by pulls
// and by WarmImage, never by client pushes.
type cacheBackend struct {
	*storage.Store
	resolve func(repo string) (*Upstream, string, error)
}

func newCacheBackend(cache *storage.Store, resolve func(repo string) (*Upstream, string, error)) *cacheBackend {
	return &cacheBackend{Store: cache, resolve: resolve}
}

func (c *cacheBackend) GetBlob(repo string, d digest.Digest) (io.ReadCloser, int64, error) {
	rc, sz, err := c.Store.GetBlob(repo, d)
	if err == nil {
		return rc, sz, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return nil, 0, err
	}
	up, urepo, rerr := c.resolve(repo)
	if rerr != nil {
		return nil, 0, rerr
	}
	urc, usz, uerr := up.GetBlobReader(urepo, d)
	if uerr != nil {
		return nil, 0, uerr
	}
	tmp, terr := os.CreateTemp("", "cacheblob-")
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
	if f, oerr := os.Open(tmp.Name()); oerr == nil {
		_ = c.Store.PutBlob(repo, d, f)
		f.Close()
	}
	f, rerr2 := os.Open(tmp.Name())
	if rerr2 != nil {
		os.Remove(tmp.Name())
		return nil, 0, rerr2
	}
	return &tempFileReader{f: f, path: tmp.Name()}, usz, nil
}

func (c *cacheBackend) StatBlob(repo string, d digest.Digest) (int64, error) {
	sz, err := c.Store.StatBlob(repo, d)
	if err == nil {
		return sz, nil
	}
	up, urepo, rerr := c.resolve(repo)
	if rerr != nil {
		return 0, rerr
	}
	return up.StatBlob(urepo, d)
}

func (c *cacheBackend) GetManifest(repo, reference string) ([]byte, string, error) {
	content, mt, err := c.Store.GetManifest(repo, reference)
	if err == nil {
		return content, mt, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return nil, "", err
	}
	up, urepo, rerr := c.resolve(repo)
	if rerr != nil {
		return nil, "", rerr
	}
	uc, umt, uerr := up.GetManifest(urepo, reference)
	if uerr != nil {
		return nil, "", uerr
	}
	_, _ = c.Store.PutManifest(repo, reference, umt, uc)
	return uc, umt, nil
}

func (c *cacheBackend) ListTags(repo string) ([]string, error) {
	up, urepo, rerr := c.resolve(repo)
	if rerr != nil {
		return nil, rerr
	}
	return up.ListTags(urepo)
}

// ---- read-only writes ----

func (c *cacheBackend) PutBlob(repo string, d digest.Digest, r io.Reader) error {
	return errCacheReadOnly
}
func (c *cacheBackend) PutManifest(repo, reference, mediaType string, content []byte) (digest.Digest, error) {
	return "", errCacheReadOnly
}
func (c *cacheBackend) DeleteManifest(repo, reference string) error { return errCacheReadOnly }
func (c *cacheBackend) DeleteBlob(d digest.Digest) error            { return errCacheReadOnly }
func (c *cacheBackend) NewUpload(repo string) (*storage.Upload, error) {
	return nil, errCacheReadOnly
}
func (c *cacheBackend) GetUpload(id string) (*storage.Upload, error) { return nil, errCacheReadOnly }
func (c *cacheBackend) AppendUpload(u *storage.Upload, r io.Reader) (int64, error) {
	return 0, errCacheReadOnly
}
func (c *cacheBackend) UploadOffset(u *storage.Upload) (int64, error) { return 0, errCacheReadOnly }
func (c *cacheBackend) CommitUpload(u *storage.Upload, d digest.Digest) error {
	return errCacheReadOnly
}
func (c *cacheBackend) CancelUpload(u *storage.Upload) error { return errCacheReadOnly }
func (c *cacheBackend) LinkManifestBlob(repo string, manifest, blob digest.Digest) error {
	return nil
}

// ---- pre-warm ----

// WarmImage pulls a manifest (and, for image manifests, its config + layers;
// for indexes, every referenced platform manifest recursively) into the local
// store so a later pull is served entirely from cache.
func (c *cacheBackend) WarmImage(ctx context.Context, repo, reference string) error {
	return c.warmManifest(ctx, repo, reference, map[digest.Digest]bool{})
}

func (c *cacheBackend) warmManifest(ctx context.Context, repo, reference string, seen map[digest.Digest]bool) error {
	up, urepo, err := c.resolve(repo)
	if err != nil {
		return err
	}
	content, mt, err := up.GetManifest(urepo, reference)
	if err != nil {
		return err
	}
	if _, serr := c.Store.PutManifest(repo, reference, mt, content); serr != nil {
		return serr
	}
	var m struct {
		MediaType string `json:"mediaType"`
		Config    struct {
			Digest string `json:"digest"`
		} `json:"config"`
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
		Manifests []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(content, &m); err != nil {
		return err
	}
	if strings.Contains(m.MediaType, "manifest.list") || strings.Contains(m.MediaType, "image.index") {
		for _, child := range m.Manifests {
			d := digest.Digest(child.Digest)
			if d == "" || seen[d] {
				continue
			}
			seen[d] = true
			if err := c.warmManifest(ctx, repo, d.String(), seen); err != nil {
				return err
			}
		}
		return nil
	}
	if m.Config.Digest != "" {
		if err := c.warmBlob(ctx, repo, digest.Digest(m.Config.Digest)); err != nil {
			return err
		}
	}
	for _, l := range m.Layers {
		if err := c.warmBlob(ctx, repo, digest.Digest(l.Digest)); err != nil {
			return err
		}
	}
	return nil
}

func (c *cacheBackend) warmBlob(ctx context.Context, repo string, d digest.Digest) error {
	if _, err := c.Store.StatBlob(repo, d); err == nil {
		return nil
	}
	up, urepo, err := c.resolve(repo)
	if err != nil {
		return err
	}
	rc, _, err := up.GetBlobReader(urepo, d)
	if err != nil {
		return err
	}
	defer rc.Close()
	return c.Store.PutBlob(repo, d, rc)
}

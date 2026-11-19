package registry

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"registry/internal/digest"
	"registry/internal/storage"
)

// ErrTagConflict is returned when a warm push names a tag the cache already
// holds for another digest: the cache's tags follow the origin (see
// GetManifest), a node's copy of a tag may be older and never moves them.
var ErrTagConflict = errors.New("tag already points to another manifest")

// cacheBackend is a transparent multi-upstream pull-through cache. Every read is
// served from the local store and, on a miss, fetched from the upstream that the
// repo path resolves to (host-less repos => Docker Hub, anything with a registry
// host segment => that host). It is populated by pulls and by warm pushes: the
// pre-warm agent pushes the images the nodes already hold (the API allows that
// only to registry:warm). Clients cannot delete.
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
		return nil, 0, notHere(repo, d.String(), uerr)
	}
	tmp, terr := os.CreateTemp("", "cacheblob-")
	if terr != nil {
		urc.Close()
		return nil, 0, terr
	}
	if cerr := copyVerifyDigest(tmp, urc, d); cerr != nil {
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
	sz, uerr := up.StatBlob(urepo, d)
	if uerr != nil {
		return 0, notHere(repo, d.String(), uerr)
	}
	return sz, nil
}

// notHere is what a cache answers when it does not hold content and cannot get
// it from the origin: not found. A mirror client (containerd) then asks the
// origin itself, with its own credentials (a private image the cache has no
// key for, a node with imagePullSecrets); any other error would stop the pull.
// The reason is logged.
func notHere(repo, ref string, cause error) error {
	if !errors.Is(cause, storage.ErrNotFound) {
		log.Printf("cache: %s %s: not held and the origin did not provide it (%v)", repo, ref, cause)
	}
	return storage.ErrNotFound
}

// GetManifest serves a manifest. A digest names immutable content: the local
// copy is the answer. A tag is the origin's to move: when the cache holds it,
// the origin is asked for the tag's current digest with a HEAD (Docker Hub does
// not count those against the pull limit); the same digest serves the local
// copy, a new one is fetched. When the origin cannot answer, a cache serves
// what it holds: that is what a cache is for, and the reason is logged.
func (c *cacheBackend) GetManifest(repo, reference string) ([]byte, string, error) {
	content, mt, err := c.Store.GetManifest(repo, reference)
	cached := err == nil
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return nil, "", err
	}
	if _, derr := digest.Parse(reference); derr == nil && cached {
		return content, mt, nil
	}
	up, urepo, rerr := c.resolve(repo)
	if rerr != nil {
		return nil, "", rerr
	}
	if cached {
		current, herr := up.HeadManifest(urepo, reference)
		if herr != nil {
			log.Printf("cache: %s:%s: origin did not answer (%v), serving the cached copy", repo, reference, herr)
			return content, mt, nil
		}
		if current == digest.FromBytes(content) {
			return content, mt, nil
		}
		// The tag moved at the origin. The new manifest may already be here
		// (pulled by digest, or pushed by a node).
		if nc, nmt, nerr := c.Store.GetManifest(repo, current.String()); nerr == nil {
			if _, perr := c.Store.PutManifest(repo, reference, nmt, nc); perr != nil {
				return nil, "", perr
			}
			return nc, nmt, nil
		}
	}
	uc, umt, uerr := up.GetManifest(urepo, reference)
	if uerr != nil {
		if cached {
			log.Printf("cache: %s:%s: fetching the new manifest failed (%v), serving the cached copy", repo, reference, uerr)
			return content, mt, nil
		}
		return nil, "", notHere(repo, reference, uerr)
	}
	if verr := verifyManifestDigest(reference, uc); verr != nil {
		return nil, "", verr
	}
	if _, perr := c.Store.PutManifest(repo, reference, umt, uc); perr != nil {
		return nil, "", perr
	}
	return uc, umt, nil
}

func (c *cacheBackend) ListTags(repo string) ([]string, error) {
	up, urepo, rerr := c.resolve(repo)
	if rerr != nil {
		return nil, rerr
	}
	return up.ListTags(urepo)
}

// ---- warm pushes ----
// The API lets only registry:warm write here (the pre-warm agent pushing what
// the nodes hold); digests are verified by the store like any push.

func (c *cacheBackend) PutBlob(repo string, d digest.Digest, r io.Reader) error {
	return c.Store.PutBlob(repo, d, r)
}

// PutManifest stores a pushed manifest. By digest, always; by tag, only a tag
// the cache does not hold yet, or one already pointing at this manifest.
func (c *cacheBackend) PutManifest(repo, reference, mediaType string, content []byte) (digest.Digest, error) {
	if _, err := digest.Parse(reference); err != nil {
		held, herr := c.Store.ResolveReference(repo, reference)
		switch {
		case herr == nil && held != digest.FromBytes(content):
			return "", fmt.Errorf("%s:%s: %w", repo, reference, ErrTagConflict)
		case herr != nil && !errors.Is(herr, storage.ErrNotFound):
			return "", herr
		}
	}
	return c.Store.PutManifest(repo, reference, mediaType, content)
}

var errCacheNoDelete = fmt.Errorf("cache registry content cannot be deleted by clients")

func (c *cacheBackend) DeleteManifest(repo, reference string) error { return errCacheNoDelete }
func (c *cacheBackend) DeleteBlob(d digest.Digest) error            { return errCacheNoDelete }

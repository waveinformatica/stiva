package registry

import (
	"fmt"
	"io"
	"os"
	"strings"

	"registry/internal/storage"
)

// ArtifactBackend is the path-based surface the API handlers use for non-OCI
// formats (helm/maven/npm). Unlike the OCI Backend, content is addressed by an
// arbitrary path rather than by content digest, so each registry is a single
// flat namespace of artifacts.
type ArtifactBackend interface {
	// Name returns the registry name this backend serves.
	Name() string
	// Get returns the artifact at path (no leading slash), its size and type.
	Get(path string) (io.ReadCloser, int64, string, error)
	// Head returns size and content type without the body.
	Head(path string) (int64, string, error)
	// Put stores an artifact at path (hosted/proxy write-through).
	Put(path, contentType string, r io.Reader) error
	// Delete removes an artifact.
	Delete(path string) error
	// List returns stored paths under a prefix.
	List(prefix string) ([]string, error)
	Close() error
}

// ---- hosted: a plain per-registry object store ----

type hostedArtifactBackend struct {
	*storage.Store
}

func (b *hostedArtifactBackend) Name() string { return b.Store.Registry() }

func (b *hostedArtifactBackend) Get(p string) (io.ReadCloser, int64, string, error) {
	rc, sz, ct, err := b.Store.GetObject(p)
	if err == storage.ErrNotFound {
		return nil, 0, "", ErrNotFound
	}
	return rc, sz, ct, err
}

func (b *hostedArtifactBackend) Head(p string) (int64, string, error) {
	sz, ct, err := b.Store.StatObject(p)
	if err == storage.ErrNotFound {
		return 0, "", ErrNotFound
	}
	return sz, ct, err
}

func (b *hostedArtifactBackend) Put(p, ct string, r io.Reader) error {
	return b.Store.PutObject(p, ct, r)
}

func (b *hostedArtifactBackend) Delete(p string) error { return b.Store.DeleteObject(p) }

func (b *hostedArtifactBackend) List(prefix string) ([]string, error) {
	return b.Store.ListObjects(prefix)
}

func (b *hostedArtifactBackend) Close() error { return nil }

// ---- proxy: cache locally, fall back to an upstream on miss ----

type proxyArtifactBackend struct {
	*storage.Store
	up         *Upstream
	allowWrite bool
}

func (b *proxyArtifactBackend) Name() string { return b.Store.Registry() }

func (b *proxyArtifactBackend) Get(p string) (io.ReadCloser, int64, string, error) {
	rc, sz, ct, err := b.Store.GetObject(p)
	if err == nil {
		return rc, sz, ct, nil
	}
	if err != storage.ErrNotFound {
		return nil, 0, "", err
	}
	urc, _, uct, uerr := b.up.GetObject(p)
	if uerr != nil {
		return nil, 0, "", uerr
	}
	tmp, terr := os.CreateTemp("", "pxobj-")
	if terr != nil {
		urc.Close()
		return nil, 0, "", terr
	}
	// Report the size actually received, not the one the upstream advertised:
	// the two differ if the upstream lies or the transfer is truncated, and a
	// Content-Length that disagrees with the body breaks clients.
	written, cerr := io.Copy(tmp, urc)
	if cerr != nil {
		urc.Close()
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, 0, "", cerr
	}
	urc.Close()
	tmp.Close()
	if f, oerr := os.Open(tmp.Name()); oerr == nil {
		_ = b.Store.PutObject(p, uct, f)
		f.Close()
	}
	rf, rerr := os.Open(tmp.Name())
	if rerr != nil {
		os.Remove(tmp.Name())
		return nil, 0, "", rerr
	}
	return &tempFileReader{f: rf, path: tmp.Name()}, written, uct, nil
}

func (b *proxyArtifactBackend) Head(p string) (int64, string, error) {
	sz, ct, err := b.Store.StatObject(p)
	if err == nil {
		return sz, ct, nil
	}
	if err != storage.ErrNotFound {
		return 0, "", err
	}
	return b.up.ObjectHead(p)
}

func (b *proxyArtifactBackend) Put(p, ct string, r io.Reader) error {
	if !b.allowWrite {
		return fmt.Errorf("proxy registry does not allow writes")
	}
	// Write-through stores locally (upstreams for these formats are read-only mirrors).
	return b.Store.PutObject(p, ct, r)
}

func (b *proxyArtifactBackend) Delete(p string) error { return b.Store.DeleteObject(p) }

func (b *proxyArtifactBackend) List(prefix string) ([]string, error) {
	return b.Store.ListObjects(prefix)
}

func (b *proxyArtifactBackend) Close() error { return nil }

// ---- group: aggregate reads across members, write to one member ----

type groupArtifactBackend struct {
	members []ArtifactBackend
	write   ArtifactBackend
}

func (g *groupArtifactBackend) Name() string {
	if len(g.members) > 0 {
		return g.members[0].Name()
	}
	return ""
}

func (g *groupArtifactBackend) Get(p string) (io.ReadCloser, int64, string, error) {
	for _, m := range g.members {
		rc, sz, ct, err := m.Get(p)
		if err == nil {
			return rc, sz, ct, nil
		}
	}
	return nil, 0, "", ErrNotFound
}

func (g *groupArtifactBackend) Head(p string) (int64, string, error) {
	for _, m := range g.members {
		sz, ct, err := m.Head(p)
		if err == nil {
			return sz, ct, nil
		}
	}
	return 0, "", ErrNotFound
}

func (g *groupArtifactBackend) Put(p, ct string, r io.Reader) error { return g.write.Put(p, ct, r) }

func (g *groupArtifactBackend) Delete(p string) error { return g.write.Delete(p) }

func (g *groupArtifactBackend) List(prefix string) ([]string, error) {
	seen := make(map[string]struct{})
	var out []string
	for _, m := range g.members {
		objs, err := m.List(prefix)
		if err != nil {
			continue
		}
		for _, o := range objs {
			if _, ok := seen[o]; ok {
				continue
			}
			seen[o] = struct{}{}
			out = append(out, o)
		}
	}
	return out, nil
}

func (g *groupArtifactBackend) Close() error {
	for _, m := range g.members {
		_ = m.Close()
	}
	return nil
}

// ContentTypeForPath derives a sensible Content-Type from the artifact extension.
func ContentTypeForPath(p string) string {
	switch {
	case strings.HasSuffix(p, ".tgz"), strings.HasSuffix(p, ".tar.gz"):
		return "application/gzip"
	case strings.HasSuffix(p, ".tar"):
		return "application/x-tar"
	case strings.HasSuffix(p, ".jar"), strings.HasSuffix(p, ".war"), strings.HasSuffix(p, ".ear"):
		return "application/java-archive"
	case strings.HasSuffix(p, ".pom"), strings.HasSuffix(p, ".xml"):
		return "application/xml"
	case strings.HasSuffix(p, ".json"):
		return "application/json"
	case strings.HasSuffix(p, ".yaml"), strings.HasSuffix(p, ".yml"):
		return "application/yaml"
	case strings.HasSuffix(p, ".sha1"), strings.HasSuffix(p, ".md5"), strings.HasSuffix(p, ".sha256"):
		return "text/plain"
	case strings.HasSuffix(p, ".html"), strings.HasSuffix(p, ".htm"):
		return "text/html"
	case strings.HasSuffix(p, ".txt"):
		return "text/plain"
	default:
		return "application/octet-stream"
	}
}

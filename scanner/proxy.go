package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
)

// proxy is a read-only OCI registry on the loopback interface that Trivy pulls
// from. It serves /v2/<registry>/<repo>/manifests/<digest> and
// /v2/<registry>/<repo>/blobs/<digest> from Stiva's scan API, so Trivy reads
// images exactly as it reads any registry (streaming, layer by layer) without
// the scanner knowing how clients reach each Stiva registry.
type proxy struct {
	stiva *stiva
	srv   *http.Server
	addr  string
}

func startProxy(s *stiva) (*proxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &proxy{stiva: s, addr: ln.Addr().String()}
	p.srv = &http.Server{Handler: http.HandlerFunc(p.serve)}
	go p.srv.Serve(ln)
	return p, nil
}

func (p *proxy) close(ctx context.Context) error { return p.srv.Shutdown(ctx) }

// ref is the reference Trivy pulls for an image.
func (p *proxy) ref(t target) string {
	return p.addr + "/" + t.Registry + "/" + t.Repo + "@" + t.Digest
}

// splitPath parses "<registry>/<repo...>/<kind>/<digest>". Only digests are
// served: the scanner always pulls by digest.
func splitPath(path string) (t target, kind string, ok bool) {
	segs := strings.Split(path, "/")
	n := len(segs)
	if n < 4 || segs[n-1] == "" {
		return target{}, "", false
	}
	switch segs[n-2] {
	case "manifests":
		kind = "manifest"
	case "blobs":
		kind = "blob"
	default:
		return target{}, "", false
	}
	if !strings.HasPrefix(segs[n-1], "sha256:") {
		return target{}, "", false
	}
	return target{Registry: segs[0], Repo: strings.Join(segs[1:n-2], "/"), Digest: segs[n-1]}, kind, true
}

func (p *proxy) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "read-only", http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v2/")
	if path == r.URL.Path {
		http.NotFound(w, r)
		return
	}
	if path == "" {
		w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
		w.WriteHeader(http.StatusOK)
		return
	}
	t, kind, ok := splitPath(path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	resp, err := p.stiva.content(r.Context(), kind, t)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("Docker-Content-Digest", t.Digest)
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		w.Header().Set("Content-Length", cl)
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		io.Copy(w, resp.Body)
	}
}

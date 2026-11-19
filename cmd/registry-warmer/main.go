// Command registry-warmer is the pre-warm agent of a cache registry. It runs on
// every node (a DaemonSet) and copies into the cache the images that node is
// running, reading them from containerd's content store: the node already has
// them, so nothing is asked of their origin (no pull limit, no credentials for
// private registries), and the cache gets exactly the digests and the platform
// the cluster runs.
//
// For each running container of its node it pushes, with the standard OCI push
// protocol and an API key with registry:warm on the cache:
//   - the blobs the cache does not have yet (config and layers), then the
//     manifest, by digest;
//   - for a multi-platform image, the platform manifests the node holds and then
//     the index, by digest;
//   - the tag, which the cache accepts only when it does not hold that tag for
//     another digest: the origin moves tags, a node's copy may be older.
//
// Images of the registries listed in -exclude (the registry itself) are
// skipped. A round runs every -interval; what failed is retried by the next.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"registry/internal/digest"
	"registry/internal/registry"
)

type config struct {
	cacheURL   string
	token      string
	node       string
	contentDir string
	exclude    map[string]bool
	interval   time.Duration
}

func main() {
	cacheURL := flag.String("cache-url", os.Getenv("REGISTRY_WARMER_CACHE_URL"),
		"base URL of the cache registry's own listener, e.g. http://registry:5001 (required)")
	node := flag.String("node", os.Getenv("REGISTRY_WARMER_NODE"), "name of the node this agent runs on (required)")
	contentDir := flag.String("content-dir", os.Getenv("REGISTRY_WARMER_CONTENT_DIR"),
		"containerd content store, e.g. /var/lib/containerd/io.containerd.content.v1.content (required)")
	exclude := flag.String("exclude", os.Getenv("REGISTRY_WARMER_EXCLUDE"),
		"comma-separated registry hosts whose images are not warmed (the registry itself)")
	interval := flag.Duration("interval", 10*time.Minute, "time between two rounds")
	flag.Parse()

	cfg := config{
		cacheURL: strings.TrimRight(*cacheURL, "/"), token: os.Getenv("REGISTRY_WARMER_TOKEN"),
		node: *node, contentDir: *contentDir, exclude: map[string]bool{}, interval: *interval,
	}
	var missing []string
	for name, v := range map[string]string{"-cache-url": cfg.cacheURL, "-node": cfg.node, "-content-dir": cfg.contentDir, "REGISTRY_WARMER_TOKEN": cfg.token} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		log.Fatalf("required: %s", strings.Join(missing, ", "))
	}
	for _, h := range strings.Split(*exclude, ",") {
		if h = strings.TrimSpace(h); h != "" {
			cfg.exclude[h] = true
		}
	}

	kcfg, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("kube config: %v", err)
	}
	cs, err := kubernetes.NewForConfig(kcfg)
	if err != nil {
		log.Fatalf("kube client: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	w := &warmer{cfg: cfg, kube: cs, http: &http.Client{Timeout: 30 * time.Minute}, done: map[string]bool{}}
	log.Printf("registry-warmer: node %s, cache %s, excluding %v", cfg.node, cfg.cacheURL, sortedKeys(cfg.exclude))
	for {
		w.round(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(cfg.interval):
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type warmer struct {
	cfg  config
	kube kubernetes.Interface
	http *http.Client
	// done holds the images already in the cache ("repo@digest"): a running
	// image is pushed once per agent lifetime.
	done map[string]bool
}

// image is a running image of the node: the cache repository, the reference
// the pod names (a tag or a digest) and the digest the node resolved.
type image struct {
	repo, ref string
	digest    digest.Digest
}

func (im image) String() string { return im.repo + ":" + im.ref + "@" + string(im.digest) }

// running lists the images of the node's running containers, deduplicated.
func (w *warmer) running(ctx context.Context) ([]image, error) {
	pods, err := w.kube.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + w.cfg.node})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []image
	for _, p := range pods.Items {
		statuses := append(append([]corev1.ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...)
		for _, st := range statuses {
			im, ok, err := w.imageOf(st)
			if err != nil {
				log.Printf("%s/%s %s: %v", p.Namespace, p.Name, st.Image, err)
				continue
			}
			if ok && !seen[im.String()] {
				seen[im.String()] = true
				out = append(out, im)
			}
		}
	}
	return out, nil
}

// imageOf reads a container status. Not an image to warm: no resolved digest
// yet, an image named by its ID only, or an excluded registry.
func (w *warmer) imageOf(st corev1.ContainerStatus) (image, bool, error) {
	i := strings.LastIndex(st.ImageID, "@")
	if i < 0 || strings.HasPrefix(st.Image, "sha256:") {
		return image{}, false, nil
	}
	d, err := digest.Parse(st.ImageID[i+1:])
	if err != nil {
		return image{}, false, err
	}
	repo, ref, err := registry.NormalizeImage(st.Image)
	if err != nil {
		return image{}, false, err
	}
	host := "docker.io"
	if first := strings.SplitN(repo, "/", 2)[0]; strings.ContainsAny(first, ".:") || first == "localhost" {
		host = first
	}
	if w.cfg.exclude[host] {
		return image{}, false, nil
	}
	return image{repo: repo, ref: ref, digest: d}, true, nil
}

func (w *warmer) round(ctx context.Context) {
	images, err := w.running(ctx)
	if err != nil {
		log.Printf("listing the node's pods: %v", err)
		return
	}
	pushed, failed := 0, 0
	for _, im := range images {
		if ctx.Err() != nil {
			return
		}
		key := im.repo + "@" + string(im.digest)
		if w.done[key] {
			continue
		}
		if err := w.push(ctx, im); err != nil {
			log.Printf("FAILED %s: %v", im, err)
			failed++
			continue
		}
		w.done[key] = true
		pushed++
	}
	if pushed > 0 || failed > 0 {
		log.Printf("round: %d images warmed, %d failed, %d running images known", pushed, failed, len(w.done))
	}
}

// blob reads a blob of the node's content store.
func (w *warmer) blobPath(d digest.Digest) (string, error) {
	hexPart := strings.TrimPrefix(string(d), "sha256:")
	if hexPart == string(d) {
		return "", fmt.Errorf("unsupported digest %s", d)
	}
	return filepath.Join(w.cfg.contentDir, "blobs", "sha256", hexPart), nil
}

func (w *warmer) readBlob(d digest.Digest) ([]byte, error) {
	p, err := w.blobPath(d)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	if digest.FromBytes(b) != d {
		return nil, fmt.Errorf("content store: %s does not match its digest", d)
	}
	return b, nil
}

func (w *warmer) hasBlobLocally(d digest.Digest) bool {
	p, err := w.blobPath(d)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// manifestDoc is what the agent reads of a manifest or an index.
type manifestDoc struct {
	MediaType string `json:"mediaType"`
	Config    *struct {
		Digest digest.Digest `json:"digest"`
	} `json:"config"`
	Layers []struct {
		Digest digest.Digest `json:"digest"`
	} `json:"layers"`
	Manifests []struct {
		Digest digest.Digest `json:"digest"`
	} `json:"manifests"`
}

const (
	mediaTypeOCIIndex    = "application/vnd.oci.image.index.v1+json"
	mediaTypeOCIManifest = "application/vnd.oci.image.manifest.v1+json"
)

// mediaType is the manifest's declared media type, or the OCI one its shape
// names when it declares none (the OCI spec makes the field optional).
func (m manifestDoc) mediaType() string {
	switch {
	case m.MediaType != "":
		return m.MediaType
	case m.Manifests != nil:
		return mediaTypeOCIIndex
	default:
		return mediaTypeOCIManifest
	}
}

func (m manifestDoc) isIndex() bool {
	mt := m.mediaType()
	return strings.Contains(mt, "image.index") || strings.Contains(mt, "manifest.list")
}

// push copies a running image into the cache.
func (w *warmer) push(ctx context.Context, im image) error {
	top, err := w.readBlob(im.digest)
	if err != nil {
		return err
	}
	var doc manifestDoc
	if err := json.Unmarshal(top, &doc); err != nil {
		return fmt.Errorf("manifest %s: %w", im.digest, err)
	}
	if doc.isIndex() {
		// The platforms the node pulled; the others are not here and are not
		// needed: the cache serves them on demand like any pull-through.
		children := 0
		for _, m := range doc.Manifests {
			if !w.hasBlobLocally(m.Digest) {
				continue
			}
			if err := w.pushImageManifest(ctx, im.repo, m.Digest); err != nil {
				return err
			}
			children++
		}
		if children == 0 {
			return fmt.Errorf("index %s: no platform manifest in the content store", im.digest)
		}
	} else if err := w.pushImageManifest(ctx, im.repo, im.digest); err != nil {
		return err
	}
	if err := w.putManifest(ctx, im.repo, string(im.digest), doc.mediaType(), top); err != nil {
		return err
	}
	if _, err := digest.Parse(im.ref); err == nil {
		return nil
	}
	err = w.putManifest(ctx, im.repo, im.ref, doc.mediaType(), top)
	if errors.Is(err, errTagHeld) {
		// The cache follows the origin for this tag; the node's copy is older
		// or newer, either way not the node's to move.
		return nil
	}
	return err
}

// pushImageManifest pushes a single-platform manifest: its blobs, then itself.
func (w *warmer) pushImageManifest(ctx context.Context, repo string, d digest.Digest) error {
	content, err := w.readBlob(d)
	if err != nil {
		return err
	}
	var doc manifestDoc
	if err := json.Unmarshal(content, &doc); err != nil {
		return fmt.Errorf("manifest %s: %w", d, err)
	}
	if doc.Config == nil {
		return fmt.Errorf("manifest %s: no config", d)
	}
	blobs := []digest.Digest{doc.Config.Digest}
	for _, l := range doc.Layers {
		blobs = append(blobs, l.Digest)
	}
	for _, b := range blobs {
		if err := w.pushBlob(ctx, repo, b); err != nil {
			return fmt.Errorf("blob %s: %w", b, err)
		}
	}
	return w.putManifest(ctx, repo, string(d), doc.mediaType(), content)
}

func (w *warmer) request(ctx context.Context, method, path string, q url.Values, body io.Reader, size int64, contentType string) (*http.Response, error) {
	u := w.cfg.cacheURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+w.cfg.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if body != nil {
		req.ContentLength = size
	}
	return w.http.Do(req)
}

func errorOf(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return fmt.Errorf("%s %s: %s %s", resp.Request.Method, resp.Request.URL.Path, resp.Status, strings.TrimSpace(string(b)))
}

// pushBlob uploads a blob the cache does not hold. It asks with a
// cross-repository mount, which the registry answers from its own store (201:
// already there); otherwise the answer opens an upload session, completed in
// one request with the whole blob.
func (w *warmer) pushBlob(ctx context.Context, repo string, d digest.Digest) error {
	resp, err := w.request(ctx, http.MethodPost, "/v2/"+repo+"/blobs/uploads/", url.Values{"mount": {string(d)}, "from": {repo}}, nil, 0, "")
	if err != nil {
		return err
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusCreated:
		return nil
	case http.StatusAccepted:
	default:
		return errorOf(resp)
	}
	session, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || session.Path == "" {
		return fmt.Errorf("upload session without a location")
	}
	p, err := w.blobPath(d)
	if err != nil {
		return err
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	q := session.Query()
	q.Set("digest", string(d))
	resp, err = w.request(ctx, http.MethodPut, session.Path, q, f, st.Size(), "application/octet-stream")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return errorOf(resp)
	}
	return nil
}

var errTagHeld = errors.New("the cache holds this tag for another manifest")

func (w *warmer) putManifest(ctx context.Context, repo, ref, mediaType string, content []byte) error {
	resp, err := w.request(ctx, http.MethodPut, "/v2/"+repo+"/manifests/"+ref, nil, bytes.NewReader(content), int64(len(content)), mediaType)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusCreated:
		return nil
	case http.StatusConflict:
		return errTagHeld
	default:
		return errorOf(resp)
	}
}

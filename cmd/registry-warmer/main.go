// Command registry-warmer is the Kubernetes pre-warm controller for cache
// (pull-through) registries. It discovers every container image referenced by
// workloads in the cluster and asks the cache registry to pull it into its local
// store, so node pulls are served entirely from the cache.
//
// It is deterministic: each discovered image is normalized exactly once by the
// registry's NormalizeImage and warmed via POST /api/v1/admin/registries/:name/warm.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	cacheURL := flag.String("cache-url", envOr("REGISTRY_WARMER_CACHE_URL", "http://registry.registry.svc:8080"),
		"base URL of the cache registry HTTP endpoint")
	registry := flag.String("registry", envOr("REGISTRY_WARMER_NAME", "cache"),
		"name of the cache registry to warm")
	token := flag.String("token", envOr("REGISTRY_WARMER_TOKEN", ""),
		"admin bearer token for the warm endpoint (required)")
	kubeconfig := flag.String("kubeconfig", "", "path to kubeconfig (in-cluster if empty)")
	interval := flag.Duration("interval", envDuration("REGISTRY_WARMER_INTERVAL", 10*time.Minute),
		"full rescan interval for workload controllers")
	concurrency := flag.Int("concurrency", 8, "max concurrent warm requests")
	insecure := flag.Bool("insecure", envBool("REGISTRY_WARMER_INSECURE", false), "skip TLS verification for cache-url")
	flag.Parse()

	if *token == "" {
		log.Fatal("an admin token is required (-token or REGISTRY_WARMER_TOKEN)")
	}

	cfg, err := loadKubeConfig(*kubeconfig)
	if err != nil {
		log.Fatalf("load kube config: %v", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("build client: %v", err)
	}

	w := &warmer{
		cacheURL:    strings.TrimRight(*cacheURL, "/"),
		registry:    *registry,
		token:       *token,
		concurrency: *concurrency,
		client:      newHTTPClient(*insecure),
		seen:        make(map[string]struct{}),
	}

	// React immediately to running pods.
	factory := informers.NewSharedInformerFactory(cs, *interval)
	informer := factory.Core().V1().Pods().Informer()
	_, _ = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { w.enqueue(imagesOfPod(obj)) },
		UpdateFunc: func(_, obj interface{}) { w.enqueue(imagesOfPod(obj)) },
	})
	go informer.Run(context.Background().Done())

	// Periodic full rescan of workload controllers (covers scaled-to-zero /
	// not-yet-scheduled images that have no running pod yet).
	go w.rescanLoop(context.Background(), cs, *interval)

	log.Printf("registry-warmer: watching cluster, warming cache %q at %s", *registry, *cacheURL)
	select {}
}

type warmer struct {
	cacheURL    string
	registry    string
	token       string
	concurrency int
	client      *http.Client

	mu   sync.Mutex
	seen map[string]struct{}
}

func (w *warmer) enqueue(images []string) {
	for _, img := range images {
		if img == "" {
			continue
		}
		w.mu.Lock()
		_, known := w.seen[img]
		w.seen[img] = struct{}{}
		w.mu.Unlock()
		if !known {
			w.warm(img)
		}
	}
}

func (w *warmer) rescanLoop(ctx context.Context, cs kubernetes.Interface, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		w.rescan(ctx, cs)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (w *warmer) rescan(ctx context.Context, cs kubernetes.Interface) {
	opts := metav1.ListOptions{}
	collect := func(imgs []string) { w.enqueue(imgs) }

	if list, err := cs.CoreV1().Pods("").List(ctx, opts); err == nil {
		for _, p := range list.Items {
			collect(imagesOfPod(&p))
		}
	}
	if list, err := cs.AppsV1().Deployments("").List(ctx, opts); err == nil {
		for _, d := range list.Items {
			collect(imagesOfDeployment(&d))
		}
	}
	if list, err := cs.AppsV1().DaemonSets("").List(ctx, opts); err == nil {
		for _, d := range list.Items {
			collect(imagesOfDaemonSet(&d))
		}
	}
	if list, err := cs.AppsV1().StatefulSets("").List(ctx, opts); err == nil {
		for _, s := range list.Items {
			collect(imagesOfStatefulSet(&s))
		}
	}
	if list, err := cs.AppsV1().ReplicaSets("").List(ctx, opts); err == nil {
		for _, r := range list.Items {
			collect(imagesOfReplicaSet(&r))
		}
	}
	if list, err := cs.BatchV1().Jobs("").List(ctx, opts); err == nil {
		for _, j := range list.Items {
			collect(imagesOfJob(&j))
		}
	}
	if list, err := cs.BatchV1().CronJobs("").List(ctx, opts); err == nil {
		for _, c := range list.Items {
			collect(imagesOfCronJob(&c))
		}
	}
}

func (w *warmer) warm(img string) {
	body, _ := json.Marshal(map[string]string{"image": img})
	url := fmt.Sprintf("%s/api/v1/admin/registries/%s/warm", w.cacheURL, w.registry)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("warm %s: %v", img, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+w.token)
	resp, err := w.client.Do(req)
	if err != nil {
		log.Printf("warm %s: %v", img, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("warmed %s", img)
	} else {
		log.Printf("warm %s: unexpected status %d", img, resp.StatusCode)
	}
}

// ---- image extraction ----

func containers(imgs []string, cs []corev1.Container) []string {
	for _, c := range cs {
		imgs = append(imgs, c.Image)
	}
	return imgs
}

func imagesOfPod(obj interface{}) []string {
	p, ok := obj.(*corev1.Pod)
	if !ok {
		return nil
	}
	var imgs []string
	imgs = containers(imgs, p.Spec.InitContainers)
	imgs = containers(imgs, p.Spec.Containers)
	for _, e := range p.Spec.EphemeralContainers {
		imgs = append(imgs, e.Image)
	}
	return imgs
}

func imagesOfDeployment(d *appsv1.Deployment) []string {
	return containers(nil, d.Spec.Template.Spec.Containers)
}
func imagesOfDaemonSet(d *appsv1.DaemonSet) []string {
	return containers(nil, d.Spec.Template.Spec.Containers)
}
func imagesOfStatefulSet(s *appsv1.StatefulSet) []string {
	return containers(nil, s.Spec.Template.Spec.Containers)
}
func imagesOfReplicaSet(r *appsv1.ReplicaSet) []string {
	return containers(nil, r.Spec.Template.Spec.Containers)
}
func imagesOfJob(j *batchv1.Job) []string {
	return containers(nil, j.Spec.Template.Spec.Containers)
}
func imagesOfCronJob(c *batchv1.CronJob) []string {
	return containers(nil, c.Spec.JobTemplate.Spec.Template.Spec.Containers)
}

// ---- helpers ----

func loadKubeConfig(path string) (*rest.Config, error) {
	if path != "" {
		return clientcmd.BuildConfigFromFlags("", path)
	}
	return rest.InClusterConfig()
}

func newHTTPClient(insecure bool) *http.Client {
	if insecure {
		return &http.Client{Timeout: 5 * time.Minute, Transport: insecureTransport()}
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

func insecureTransport() *http.Transport {
	return &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v == "1" || v == "true"
}

func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	return def
}

package api

import "testing"

// Una richiesta di mirror di containerd ("?ns=<origine>") finisce sullo stesso
// repository della cache che il warmer usa per "<origine>/<repo>".
func TestNamespacedPath(t *testing.T) {
	ok := map[[2]string]string{
		{"docker.io", "library/nginx/manifests/1.25"}:                  "library/nginx/manifests/1.25",
		{"docker.io", "nginx/manifests/1.25"}:                          "library/nginx/manifests/1.25",
		{"docker.io", "vllm/vllm-openai/blobs/sha256:abc"}:             "vllm/vllm-openai/blobs/sha256:abc",
		{"quay.io", "calico/cni/manifests/v3.31.0"}:                    "quay.io/calico/cni/manifests/v3.31.0",
		{"registry.k8s.io", "pause/manifests/3.10"}:                    "registry.k8s.io/pause/manifests/3.10",
		{"ghcr.io", "open-telemetry/opentelemetry-operator/tags/list"}: "ghcr.io/open-telemetry/opentelemetry-operator/tags/list",
		{"myreg.local:5000", "team/app/manifests/sha256:0123"}:         "myreg.local:5000/team/app/manifests/sha256:0123",
		{"quay.io", "calico/cni/referrers/sha256:0123"}:                "quay.io/calico/cni/referrers/sha256:0123",
	}
	for in, want := range ok {
		got, err := namespacedPath(in[0], in[1])
		if err != nil || got != want {
			t.Errorf("ns=%s %s: %q %v, atteso %q", in[0], in[1], got, err, want)
		}
	}
	for _, in := range [][2]string{
		{"library", "nginx/manifests/1.25"},      // non e' un host
		{"quay.io/x", "calico/cni/manifests/v3"}, // host con percorso
		{"quay.io", "manifests/v3"},              // nessun repository
	} {
		if got, err := namespacedPath(in[0], in[1]); err == nil {
			t.Errorf("ns=%s %s accettato: %q", in[0], in[1], got)
		}
	}
}

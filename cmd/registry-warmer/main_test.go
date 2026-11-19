package main

import (
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestImageOf(t *testing.T) {
	w := &warmer{cfg: config{exclude: map[string]bool{"repo.intra.waveinformatica.com": true, "containers.waveinformatica.com": true}}}
	d := "sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cases := []struct {
		image, imageID string
		ok             bool
		repo, ref      string
	}{
		{"nginx:1.25", "docker.io/library/nginx@" + d, true, "library/nginx", "1.25"},
		{"docker.io/vllm/vllm-openai:latest", "docker.io/vllm/vllm-openai@" + d, true, "vllm/vllm-openai", "latest"},
		{"quay.io/calico/cni:v3.31.0", "quay.io/calico/cni@" + d, true, "quay.io/calico/cni", "v3.31.0"},
		{"registry.k8s.io/pause@" + d, "registry.k8s.io/pause@" + d, true, "registry.k8s.io/pause", d},
		// Esclusi: il registry stesso.
		{"repo.intra.waveinformatica.com/kosmos/kosmos-core:latest", "repo.intra.waveinformatica.com/kosmos/kosmos-core@" + d, false, "", ""},
		{"containers.waveinformatica.com/kosmos/x:1", "containers.waveinformatica.com/kosmos/x@" + d, false, "", ""},
		// Non ancora risolta, o nominata solo per ID.
		{"nginx:1.25", "", false, "", ""},
		{"sha256:" + d[7:], "docker.io/library/nginx@" + d, false, "", ""},
	}
	for _, c := range cases {
		im, ok, err := w.imageOf(corev1.ContainerStatus{Image: c.image, ImageID: c.imageID})
		if err != nil {
			t.Errorf("%s: %v", c.image, err)
			continue
		}
		if ok != c.ok || (ok && (im.repo != c.repo || im.ref != c.ref || string(im.digest) != d)) {
			t.Errorf("%s: %+v %v, atteso %s %s %v", c.image, im, ok, c.repo, c.ref, c.ok)
		}
	}
}

func TestManifestDocMediaType(t *testing.T) {
	cases := map[string]struct {
		mt    string
		index bool
	}{
		`{"mediaType":"application/vnd.docker.distribution.manifest.list.v2+json","manifests":[]}`: {"application/vnd.docker.distribution.manifest.list.v2+json", true},
		`{"mediaType":"application/vnd.docker.distribution.manifest.v2+json","config":{}}`:         {"application/vnd.docker.distribution.manifest.v2+json", false},
		// Senza mediaType (facoltativo per OCI): la forma decide.
		`{"manifests":[]}`: {mediaTypeOCIIndex, true},
		`{"config":{}}`:    {mediaTypeOCIManifest, false},
	}
	for in, want := range cases {
		var m manifestDoc
		if err := json.Unmarshal([]byte(in), &m); err != nil {
			t.Fatal(err)
		}
		if m.mediaType() != want.mt || m.isIndex() != want.index {
			t.Errorf("%s: %s %v", in, m.mediaType(), m.isIndex())
		}
	}
}

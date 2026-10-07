package storage

import "testing"

// TestIsTopLayer covers the tag-per-layer rule: a layer names the image it
// tops (last layer entry), not every image inheriting it as a base.
func TestIsTopLayer(t *testing.T) {
	manifest := `{"layers":[{"digest":"sha256:base"},{"digest":"sha256:top"}]}`
	if !isTopLayer([]byte(manifest), "sha256:top") {
		t.Error("ultimo layer non riconosciuto come top")
	}
	if isTopLayer([]byte(manifest), "sha256:base") {
		t.Error("layer di base riconosciuto come top")
	}
	if isTopLayer([]byte(manifest), "sha256:altro") {
		t.Error("digest assente riconosciuto come top")
	}
	if isTopLayer([]byte(`{"layers":[]}`), "sha256:top") {
		t.Error("lista vuota riconosciuta come top")
	}
	if isTopLayer([]byte(`{"manifests":[]}`), "sha256:top") {
		t.Error("indice senza layers riconosciuto come top")
	}
	if isTopLayer([]byte(`non json`), "sha256:top") {
		t.Error("json invalido riconosciuto come top")
	}
}

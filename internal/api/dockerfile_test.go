package api

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestDockerfileFromStatement(t *testing.T) {
	dockerfile := "FROM alpine:3.20\nRUN apk add curl\n"
	statement := map[string]any{
		"predicate": map[string]any{
			"buildDefinition": map[string]any{
				"definition": base64.StdEncoding.EncodeToString([]byte(dockerfile)),
			},
		},
	}
	stmtRaw, _ := json.Marshal(statement)
	// Bare statement.
	if got := dockerfileFromStatement(stmtRaw); got != dockerfile {
		t.Errorf("bare statement = %q, atteso %q", got, dockerfile)
	}
	// DSSE envelope around it.
	env, _ := json.Marshal(map[string]any{
		"payloadType": "application/vnd.in-toto+json",
		"payload":     base64.StdEncoding.EncodeToString(stmtRaw),
		"signatures":  []any{},
	})
	if got := dockerfileFromStatement(env); got != dockerfile {
		t.Errorf("dsse envelope = %q, atteso %q", got, dockerfile)
	}
	// Garbage in, empty out (never an error: the UI shows "unavailable").
	for _, raw := range []string{"", "{}", `{"payload":"!!!"}`, `{"predicate":{}}`} {
		if got := dockerfileFromStatement([]byte(raw)); got != "" {
			t.Errorf("dockerfileFromStatement(%q) = %q, atteso vuoto", raw, got)
		}
	}
}

func TestDockerfileFromHistory(t *testing.T) {
	cfg := `{"history":[
		{"createdBy":"ADD file:abc in / "},
		{"createdBy":"/bin/sh -c #(nop)  CMD [\"sh\"]"},
		{"createdBy":"/bin/sh -c apk add curl"},
		{"createdBy":""},
		{"createdBy":"WORKDIR /app"}
	]}`
	got := dockerfileFromHistory([]byte(cfg))
	want := []string{"ADD file:abc in /", `CMD ["sh"]`, "RUN apk add curl", "WORKDIR /app"}
	lines := strings.Split(got, "\n")
	if len(lines) != len(want) {
		t.Fatalf("history = %q, attese %d righe", got, len(want))
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("riga %d = %q, attesa %q", i, lines[i], w)
		}
	}
	if got := dockerfileFromHistory([]byte(`{"history":[]}`)); got != "" {
		t.Errorf("history vuota = %q, attesa vuota", got)
	}
}

func TestPlatformManifests(t *testing.T) {
	index := `{"manifests":[
		{"digest":"sha256:attest"},
		{"digest":"sha256:arm","platform":{"architecture":"arm64","os":"linux"}},
		{"digest":"sha256:amd","platform":{"architecture":"amd64","os":"linux"}}
	]}`
	got := platformManifests([]byte(index))
	want := []string{"sha256:amd", "sha256:arm", "sha256:attest"}
	if len(got) != len(want) {
		t.Fatalf("platformManifests = %v, atteso %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("posizione %d = %q, attesa %q", i, got[i], want[i])
		}
	}
	if got := platformManifests([]byte(`non json`)); len(got) != 0 {
		t.Errorf("json invalido = %v, atteso vuoto", got)
	}
}

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"registry/internal/digest"
	"registry/internal/registry"
	"registry/internal/storage"
)

// refBackend holds the referrers of one subject.
type refBackend struct {
	registry.Backend
	subject digest.Digest
	refs    []storage.Referrer
}

func (b *refBackend) Referrers(_ string, d digest.Digest) ([]storage.Referrer, error) {
	if d != b.subject {
		return []storage.Referrer{}, nil
	}
	return b.refs, nil
}

func serveReferrers(t *testing.T, be registry.Backend, target string) (*httptest.ResponseRecorder, referrersIndex) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v2/acme/app/referrers/"+target, nil)
	(&Handler{}).referrers(c, be, []string{"acme", "app"}, target)
	var idx referrersIndex
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &idx); err != nil {
			t.Fatal(err)
		}
	}
	return w, idx
}

func TestReferrersIndex(t *testing.T) {
	subject := digest.FromBytes([]byte("image"))
	sbom := digest.FromBytes([]byte("sbom"))
	sig := digest.FromBytes([]byte("sig"))
	be := &refBackend{subject: subject, refs: []storage.Referrer{
		{Digest: sbom, MediaType: "application/vnd.oci.image.manifest.v1+json", ArtifactType: "application/vnd.cyclonedx+json",
			Size: 10, Annotations: map[string]string{"org.opencontainers.image.created": "2026-11-18T00:00:00Z"}},
		{Digest: sig, MediaType: "application/vnd.oci.image.manifest.v1+json", ArtifactType: "application/vnd.dev.cosign.artifact.sig.v1+json", Size: 20},
	}}

	w, idx := serveReferrers(t, be, subject.String())
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != mediaTypeOCIIndex {
		t.Errorf("content type %q", ct)
	}
	if idx.SchemaVersion != 2 || idx.MediaType != mediaTypeOCIIndex || len(idx.Manifests) != 2 {
		t.Fatalf("indice: %+v", idx)
	}
	if idx.Manifests[0].Digest != sbom.String() || idx.Manifests[0].Annotations["org.opencontainers.image.created"] == "" {
		t.Errorf("primo referrer: %+v", idx.Manifests[0])
	}
	if w.Header().Get("OCI-Filters-Applied") != "" {
		t.Error("filtro dichiarato senza filtro")
	}

	// Filtro per artifactType.
	gin.SetMode(gin.TestMode)
	fw := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(fw)
	c.Request = httptest.NewRequest(http.MethodGet, "/v2/acme/app/referrers/"+subject.String()+"?artifactType=application/vnd.cyclonedx%2Bjson", nil)
	(&Handler{}).referrers(c, be, []string{"acme", "app"}, subject.String())
	var fidx referrersIndex
	if err := json.Unmarshal(fw.Body.Bytes(), &fidx); err != nil {
		t.Fatal(err)
	}
	if len(fidx.Manifests) != 1 || fidx.Manifests[0].Digest != sbom.String() {
		t.Errorf("filtrato: %+v", fidx)
	}
	if fw.Header().Get("OCI-Filters-Applied") != "artifactType" {
		t.Error("OCI-Filters-Applied mancante")
	}

	// Soggetto senza referrer: indice vuoto, non 404.
	w, idx = serveReferrers(t, be, digest.FromBytes([]byte("altro")).String())
	if w.Code != http.StatusOK || idx.Manifests == nil || len(idx.Manifests) != 0 {
		t.Errorf("soggetto sconosciuto: %d %+v", w.Code, idx)
	}

	// Digest non valido.
	if w, _ := serveReferrers(t, be, "sha256:corto"); w.Code != http.StatusBadRequest {
		t.Errorf("digest non valido: %d", w.Code)
	}
}

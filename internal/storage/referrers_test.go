package storage

import (
	"errors"
	"testing"

	"registry/internal/digest"
)

func TestParseManifestInfo(t *testing.T) {
	subject := digest.FromBytes([]byte("image"))

	info, err := ParseManifestInfo(MediaTypeOCIManifest, []byte(`{"artifactType":"application/vnd.cyclonedx+json",
		"config":{"mediaType":"application/vnd.oci.empty.v1+json"},
		"layers":[{"mediaType":"application/vnd.cyclonedx+json"}],
		"subject":{"digest":"`+subject.String()+`"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if info.Subject != subject || info.ArtifactType != "application/vnd.cyclonedx+json" || info.Image {
		t.Errorf("SBOM agganciato: %+v", info)
	}

	// Senza artifactType vale il media type della config (regola della specifica).
	info, err = ParseManifestInfo(MediaTypeOCIManifest, []byte(`{"config":{"mediaType":"application/vnd.oci.image.config.v1+json"},
		"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip"},{"mediaType":"application/vnd.oci.image.layer.v1.tar+zstd"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if info.Subject != "" || info.ArtifactType != "application/vnd.oci.image.config.v1+json" || !info.Image {
		t.Errorf("immagine OCI: %+v", info)
	}

	info, err = ParseManifestInfo(MediaTypeDockerManifest, []byte(`{"config":{"mediaType":"application/vnd.docker.container.image.v1+json"},
		"layers":[{"mediaType":"application/vnd.docker.image.rootfs.diff.tar.gzip"}]}`))
	if err != nil || !info.Image {
		t.Errorf("immagine Docker: %+v %v", info, err)
	}

	// Una firma cosign ha la config di un'immagine ma non layer di filesystem.
	info, err = ParseManifestInfo(MediaTypeOCIManifest, []byte(`{"config":{"mediaType":"application/vnd.oci.image.config.v1+json"},
		"layers":[{"mediaType":"application/vnd.dev.cosign.simplesigning.v1+json"}]}`))
	if err != nil || info.Image {
		t.Errorf("firma cosign: %+v %v", info, err)
	}

	// Un indice non e' un'immagine: si scansiona ciascuna piattaforma.
	info, err = ParseManifestInfo("application/vnd.oci.image.index.v1+json", []byte(`{"manifests":[]}`))
	if err != nil || info.ArtifactType != "" || info.Subject != "" || info.Image {
		t.Errorf("indice: %+v %v", info, err)
	}

	// Un manifest senza layer non e' un'immagine.
	info, err = ParseManifestInfo(MediaTypeOCIManifest, []byte(`{"config":{"mediaType":"application/vnd.oci.image.config.v1+json"},"layers":[]}`))
	if err != nil || info.Image {
		t.Errorf("senza layer: %+v %v", info, err)
	}

	for name, body := range map[string]string{
		"non json":       `non json`,
		"subject storto": `{"subject":{"digest":"sha256:corto"}}`,
		"subject vuoto":  `{"subject":{}}`,
	} {
		if _, err := ParseManifestInfo(MediaTypeOCIManifest, []byte(body)); !errors.Is(err, ErrManifestInvalid) {
			t.Errorf("%s: atteso ErrManifestInvalid, ottenuto %v", name, err)
		}
	}
}

package api

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"testing"

	"registry/internal/cosign"
	"registry/internal/digest"
	"registry/internal/registry"
)

// sigBackend is a Backend holding just enough for signature checks.
type sigBackend struct {
	registry.Backend
	manifests map[string][]byte
	blobs     map[digest.Digest][]byte
	tags      []string
}

func (b *sigBackend) GetManifest(_, ref string) ([]byte, string, error) {
	if m, ok := b.manifests[ref]; ok {
		return m, "application/vnd.oci.image.index.v1+json", nil
	}
	return nil, "", fmt.Errorf("not found")
}
func (b *sigBackend) GetBlob(_ string, d digest.Digest) (io.ReadCloser, int64, error) {
	if x, ok := b.blobs[d]; ok {
		return io.NopCloser(bytes.NewReader(x)), int64(len(x)), nil
	}
	return nil, 0, fmt.Errorf("not found")
}
func (b *sigBackend) ListTags(string) ([]string, error) { return b.tags, nil }

// addSignature stores what cosign would push for subject.
func (b *sigBackend) addSignature(t *testing.T, priv *ecdsa.PrivateKey, subject digest.Digest) {
	payload, _ := json.Marshal(map[string]any{"critical": map[string]any{
		"identity": map[string]string{"docker-reference": "x"},
		"image":    map[string]string{"docker-manifest-digest": string(subject)},
		"type":     "cosign container image signature",
	}})
	sum := sha256.Sum256(payload)
	sig, err := ecdsa.SignASN1(rand.Reader, priv, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	pd := digest.FromBytes(payload)
	m, _ := json.Marshal(map[string]any{"layers": []map[string]any{{
		"mediaType": "application/vnd.dev.cosign.simplesigning.v1+json", "digest": string(pd),
		"annotations": map[string]string{"dev.cosignproject.cosign/signature": base64.StdEncoding.EncodeToString(sig)},
	}}})
	b.manifests[cosign.SignatureTag(subject)] = m
	b.blobs[pd] = payload
}

func TestSignedIndexCoversItsPlatformManifests(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	keys, err := cosign.ParseKeys(string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})))
	if err != nil {
		t.Fatal(err)
	}

	child := digest.FromString("amd64 manifest")
	index, _ := json.Marshal(map[string]any{"manifests": []map[string]string{{"digest": string(child)}}})
	indexDigest := digest.FromBytes(index)
	be := &sigBackend{manifests: map[string][]byte{"v1": index}, blobs: map[digest.Digest][]byte{}, tags: []string{"v1"}}
	be.addSignature(t, priv, indexDigest)

	// The signed index verifies directly.
	if res, via := checkSignature(be, "app", indexDigest, keys, true); res.Status != cosign.StatusVerified || via {
		t.Errorf("index = (%+v, via=%v), want verified directly", res, via)
	}
	// Its unsigned child verifies through the index when pulled by digest...
	if res, via := checkSignature(be, "app", child, keys, true); res.Status != cosign.StatusVerified || !via {
		t.Errorf("child = (%+v, via=%v), want verified via index", res, via)
	}
	// ...but not when the index scan is off (a tag pull has no such parent).
	if res, _ := checkSignature(be, "app", child, keys, false); res.Status != cosign.StatusUnsigned {
		t.Errorf("child without scan = %+v, want unsigned", res)
	}
	// An unrelated digest stays unsigned.
	if res, _ := checkSignature(be, "app", digest.FromString("stranger"), keys, true); res.Status != cosign.StatusUnsigned {
		t.Errorf("stranger = %+v, want unsigned", res)
	}
}

func TestSigningPolicyValidation(t *testing.T) {
	enforceNoKeys := &registry.SigningPolicy{Mode: registry.SigningEnforce}
	if err := enforceNoKeys.Validate("oci"); err == nil {
		t.Error("enforce without keys accepted: it would refuse every image")
	}
	if err := (&registry.SigningPolicy{Mode: "bogus", Keys: "x"}).Validate("oci"); err == nil {
		t.Error("unknown mode accepted")
	}
	if err := (&registry.SigningPolicy{Mode: registry.SigningWarn, Keys: "not a key"}).Validate("oci"); err == nil {
		t.Error("garbage key accepted")
	}
	if err := (&registry.SigningPolicy{Mode: registry.SigningWarn}).Validate("maven"); err == nil {
		t.Error("signing accepted on a non-OCI registry")
	}
}

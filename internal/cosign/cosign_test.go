package cosign

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
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

	"registry/internal/digest"
)

type fakeRepo struct {
	manifests map[string][]byte
	blobs     map[digest.Digest][]byte
}

func (f *fakeRepo) GetManifest(_, ref string) ([]byte, string, error) {
	if b, ok := f.manifests[ref]; ok {
		return b, "application/vnd.oci.image.manifest.v1+json", nil
	}
	return nil, "", fmt.Errorf("not found")
}
func (f *fakeRepo) GetBlob(_ string, d digest.Digest) (io.ReadCloser, int64, error) {
	if b, ok := f.blobs[d]; ok {
		return io.NopCloser(bytes.NewReader(b)), int64(len(b)), nil
	}
	return nil, 0, fmt.Errorf("not found")
}

func pemOf(t *testing.T, pub crypto.PublicKey) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// sign builds the repository content cosign would push for subject.
func signECDSA(t *testing.T, priv *ecdsa.PrivateKey, subject digest.Digest, claimed digest.Digest) *fakeRepo {
	payload, _ := json.Marshal(map[string]any{
		"critical": map[string]any{
			"identity": map[string]string{"docker-reference": "example/app"},
			"image":    map[string]string{"docker-manifest-digest": string(claimed)},
			"type":     payloadType,
		},
		"optional": nil,
	})
	sum := sha256.Sum256(payload)
	sig, err := ecdsa.SignASN1(rand.Reader, priv, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	pd := digest.FromBytes(payload)
	manifest, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"layers": []map[string]any{{
			"mediaType":   payloadMediaType,
			"digest":      string(pd),
			"size":        len(payload),
			"annotations": map[string]string{signatureAnnotation: base64.StdEncoding.EncodeToString(sig)},
		}},
	})
	return &fakeRepo{
		manifests: map[string][]byte{SignatureTag(subject): manifest},
		blobs:     map[digest.Digest][]byte{pd: payload},
	}
}

func TestCheckVerifiesECDSASignature(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	subject := digest.FromString("the image manifest")
	repo := signECDSA(t, priv, subject, subject)

	keys, err := ParseKeys(pemOf(t, &priv.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	if r := Check(repo, "app", subject, keys); r.Status != StatusVerified || r.Verified != 1 {
		t.Errorf("trusted key = %+v, want verified", r)
	}

	wrong, _ := ParseKeys(pemOf(t, &other.PublicKey))
	if r := Check(repo, "app", subject, wrong); r.Status != StatusInvalid {
		t.Errorf("untrusted key = %+v, want invalid", r)
	}
	if r := Check(repo, "app", subject, nil); r.Status != StatusPresent {
		t.Errorf("no keys = %+v, want present", r)
	}
	if r := Check(repo, "app", digest.FromString("another image"), keys); r.Status != StatusUnsigned {
		t.Errorf("other digest = %+v, want unsigned", r)
	}
}

func TestCheckRejectsSignatureForAnotherDigest(t *testing.T) {
	// A valid signature over a payload that names a different image must not
	// vouch for this one (replaying a signature under another .sig tag).
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	victim := digest.FromString("victim")
	signedOther := digest.FromString("something I signed")
	repo := signECDSA(t, priv, victim, signedOther)
	keys, _ := ParseKeys(pemOf(t, &priv.PublicKey))
	if r := Check(repo, "app", victim, keys); r.Status != StatusInvalid {
		t.Errorf("replayed signature = %+v, want invalid", r)
	}
}

func TestCheckRejectsTamperedPayloadBlob(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	subject := digest.FromString("img")
	repo := signECDSA(t, priv, subject, subject)
	for d := range repo.blobs {
		repo.blobs[d] = append(repo.blobs[d], ' ') // no longer hashes to its digest
	}
	keys, _ := ParseKeys(pemOf(t, &priv.PublicKey))
	if r := Check(repo, "app", subject, keys); r.Status != StatusInvalid {
		t.Errorf("tampered blob = %+v, want invalid", r)
	}
}

func TestParseKeys(t *testing.T) {
	if _, err := ParseKeys(""); err == nil {
		t.Error("empty input accepted")
	}
	if _, err := ParseKeys("not pem"); err == nil {
		t.Error("garbage accepted")
	}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	ecPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	keys, err := ParseKeys(pemOf(t, pub) + "\n" + pemOf(t, &ecPriv.PublicKey))
	if err != nil || len(keys) != 2 {
		t.Errorf("two keys = (%d, %v)", len(keys), err)
	}
	priv := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: []byte("x")}))
	if _, err := ParseKeys(priv); err == nil {
		t.Error("a private key block must be refused")
	}
}

func TestAttachmentTags(t *testing.T) {
	for tag, want := range map[string]bool{
		"sha256-abc.sig": true, "sha256-abc.att": true, "sha256-abc.sbom": true,
		"latest": false, "v1.sig": false,
	} {
		if got := IsAttachmentTag(tag); got != want {
			t.Errorf("IsAttachmentTag(%q) = %v", tag, got)
		}
	}
}

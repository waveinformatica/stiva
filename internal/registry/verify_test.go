package registry

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"

	"registry/internal/digest"
)

func TestCopyVerifyDigest(t *testing.T) {
	data := []byte("layer bytes")
	good := digest.FromBytes(data)

	var out bytes.Buffer
	if err := copyVerifyDigest(&out, bytes.NewReader(data), good); err != nil {
		t.Fatalf("matching digest rejected: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Error("content not copied")
	}

	bad := digest.FromBytes([]byte("something else"))
	err := copyVerifyDigest(io.Discard, bytes.NewReader(data), bad)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("mismatch = %v, want ErrChecksumMismatch", err)
	}
}

func TestVerifyManifestDigest(t *testing.T) {
	content := []byte(`{"schemaVersion":2}`)
	if err := verifyManifestDigest("latest", content); err != nil {
		t.Errorf("a tag carries no expectation: %v", err)
	}
	if err := verifyManifestDigest(string(digest.FromBytes(content)), content); err != nil {
		t.Errorf("matching digest rejected: %v", err)
	}
	other := string(digest.FromBytes([]byte("other")))
	if err := verifyManifestDigest(other, content); !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("tampered manifest = %v, want ErrChecksumMismatch", err)
	}
}

func TestParseChecksumFile(t *testing.T) {
	cases := map[string]string{
		"ABCDEF0123":                "abcdef0123",
		"abcdef0123  lib-1.0.jar\n": "abcdef0123",
		"":                          "",
		"not hex at all":            "",
	}
	for in, want := range cases {
		if got := parseChecksumFile([]byte(in)); got != want {
			t.Errorf("parseChecksumFile(%q) = %q, want %q", in, got, want)
		}
	}
}

func sumsOf(data []byte) *objectSums {
	s := newObjectSums()
	_, _ = s.writer().Write(data)
	return s
}

func fetchFrom(files map[string]string) sidecarFetcher {
	return func(p string) ([]byte, error) {
		if v, ok := files[p]; ok {
			return []byte(v), nil
		}
		return nil, ErrNotFound
	}
}

func TestVerifyObjectSums(t *testing.T) {
	data := []byte("jar content")
	s256 := sha256.Sum256(data)
	s1 := sha1.Sum(data)
	h256, h1 := hex.EncodeToString(s256[:]), hex.EncodeToString(s1[:])

	if algo, err := verifyObjectSums("a/b.jar", sumsOf(data), fetchFrom(map[string]string{"a/b.jar.sha1": h1})); err != nil || algo != "sha1" {
		t.Errorf("sha1 sidecar = (%q, %v)", algo, err)
	}
	// The strongest published checksum wins.
	if algo, _ := verifyObjectSums("a/b.jar", sumsOf(data), fetchFrom(map[string]string{
		"a/b.jar.sha1": h1, "a/b.jar.sha256": h256 + "  b.jar",
	})); algo != "sha256" {
		t.Errorf("algo = %q, want sha256", algo)
	}
	// Tampered download.
	algo, err := verifyObjectSums("a/b.jar", sumsOf([]byte("evil")), fetchFrom(map[string]string{"a/b.jar.sha256": h256}))
	if !errors.Is(err, ErrChecksumMismatch) || algo != "sha256" {
		t.Errorf("tampered = (%q, %v)", algo, err)
	}
	// Nothing published: unverifiable, not failed.
	if algo, err := verifyObjectSums("a/b.jar", sumsOf(data), fetchFrom(nil)); err != nil || algo != "" {
		t.Errorf("no sidecar = (%q, %v)", algo, err)
	}
	// Garbage sidecar is ignored rather than failing the download.
	if _, err := verifyObjectSums("a/b.jar", sumsOf(data), fetchFrom(map[string]string{"a/b.jar.sha256": "<html>404</html>"})); err != nil {
		t.Errorf("garbage sidecar failed the download: %v", err)
	}
}

func TestChecksumApplies(t *testing.T) {
	for p, want := range map[string]bool{
		"org/x/1.0/x-1.0.jar":           true,
		"org/x/1.0/x-1.0.jar.sha1":      false,
		"org/x/1.0/x-1.0.jar.asc":       false,
		"org/x/maven-metadata.xml":      false,
		"index.yaml":                    false,
		"left-pad/-/left-pad-1.0.0.tgz": true,
	} {
		if got := checksumApplies(p); got != want {
			t.Errorf("checksumApplies(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestRegistryChecksumModeValidation(t *testing.T) {
	r := &Registry{Name: "x", Format: "maven", Type: "proxy", RemoteURL: "https://repo.example", Checksum: "bogus"}
	if err := r.Validate(); err == nil {
		t.Error("unknown checksum mode accepted")
	}
}

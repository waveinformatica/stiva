package api

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/crypto/openpgp"
	"golang.org/x/crypto/openpgp/clearsign"
)

func TestAPTKeyRoundTrip(t *testing.T) {
	priv, pub, fp, err := generateAPTKey("testrepo")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(priv, "PGP PRIVATE KEY BLOCK") || !strings.Contains(pub, "PGP PUBLIC KEY BLOCK") {
		t.Fatal("armored blocks missing")
	}
	if ok, _ := regexp.MatchString(`^([0-9A-F]{4} ){9}[0-9A-F]{4}$`, fp); !ok {
		t.Fatalf("fingerprint = %q", fp)
	}
	entity, err := parseAPTPrivateKey(priv)
	if err != nil {
		t.Fatalf("parse own key: %v", err)
	}
	if aptFingerprint(entity) != fp {
		t.Fatal("fingerprint not stable across parse")
	}
	for _, bad := range []string{"", "not a key", "-----BEGIN CERTIFICATE-----\nfoo"} {
		if _, err := parseAPTPrivateKey(bad); err == nil {
			t.Fatalf("%q should not parse", bad)
		}
	}
}

func aptTestEntity(t *testing.T) *openpgp.Entity {
	t.Helper()
	priv, _, _, err := generateAPTKey("test")
	if err != nil {
		t.Fatal(err)
	}
	entity, err := parseAPTPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return entity
}

func TestAPTDetachSignVerify(t *testing.T) {
	entity := aptTestEntity(t)
	data := []byte("SHA256:\n deadbeef 12 Packages\n")
	sig, err := detachSignAPT(entity, data)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	el := openpgp.EntityList{entity}
	if _, err := openpgp.CheckArmoredDetachedSignature(el, bytes.NewReader(data), bytes.NewReader(sig)); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, err := openpgp.CheckArmoredDetachedSignature(el, bytes.NewReader([]byte("tampered")), bytes.NewReader(sig)); err == nil {
		t.Fatal("tampered data must not verify")
	}
}

func TestAPTClearsignVerify(t *testing.T) {
	entity := aptTestEntity(t)
	data := []byte("SHA256:\n deadbeef 12 Packages\n")
	signed, err := clearsignAPT(entity, data)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !bytes.HasPrefix(signed, []byte("-----BEGIN PGP SIGNED MESSAGE-----")) {
		t.Fatal("not a clearsigned message")
	}
	block, _ := clearsign.Decode(signed)
	if block == nil {
		t.Fatal("clearsign decode failed")
	}
	if !bytes.Equal(block.Plaintext, data) {
		t.Fatal("clearsign round-trip changed the payload")
	}
	// Clearsign hashes the DARSH-canonical text: CRLF endings, no trailing
	// newline. apt and gpg agree on this form; it is what they will check.
	sigBytes, err := io.ReadAll(block.ArmoredSignature.Body)
	if err != nil {
		t.Fatal(err)
	}
	canonical := bytes.TrimSuffix(bytes.ReplaceAll(block.Plaintext, []byte("\n"), []byte("\r\n")), []byte("\r\n"))
	el := openpgp.EntityList{entity}
	if _, err := openpgp.CheckDetachedSignature(el, bytes.NewReader(canonical), bytes.NewReader(sigBytes)); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestAPTReleaseListsBothIndexes(t *testing.T) {
	deb, err := buildTestDeb()
	if err != nil {
		t.Fatal(err)
	}
	be := &fakeBackend{objs: map[string][]byte{"mypkg_1.0_amd64.deb": deb}}
	pkgs, _, ok := genAPT(be, "Packages")
	if !ok || len(pkgs) == 0 {
		t.Fatal("no Packages generated")
	}
	if !strings.Contains(string(pkgs), "Filename: mypkg_1.0_amd64.deb\n") {
		t.Fatalf("Filename must be relative:\n%s", pkgs)
	}
	gz, _, ok := genAPT(be, "Packages.gz")
	if !ok || len(gz) == 0 {
		t.Fatal("no Packages.gz generated")
	}
	rel, _, ok := genAPT(be, "Release")
	if !ok {
		t.Fatal("no Release generated")
	}
	for _, want := range []string{
		fmt.Sprintf("%x %d Packages", sha256.Sum256(pkgs), len(pkgs)),
		fmt.Sprintf("%x %d Packages.gz", sha256.Sum256(gz), len(gz)),
	} {
		if !strings.Contains(string(rel), want) {
			t.Fatalf("Release misses %q:\n%s", want, rel)
		}
	}
}

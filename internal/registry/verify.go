package registry

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log"
	"strings"

	"registry/internal/digest"
)

// ErrChecksumMismatch is returned (wrapped in *ChecksumError) when content
// fetched from an upstream does not match the checksum it was published with.
var ErrChecksumMismatch = errors.New("checksum mismatch")

// ChecksumError describes one failed verification.
type ChecksumError struct {
	What string // path or digest of the content
	Algo string
	Want string
	Got  string
}

func (e *ChecksumError) Error() string {
	return fmt.Sprintf("%s: %s mismatch (expected %s, got %s)", e.What, e.Algo, e.Want, e.Got)
}

func (e *ChecksumError) Is(target error) bool { return target == ErrChecksumMismatch }

// Checksum verification modes for proxy registries of path-addressed formats.
const (
	ChecksumOff     = ""        // do not verify
	ChecksumWarn    = "warn"    // log mismatches, still serve and cache
	ChecksumEnforce = "enforce" // refuse content that fails verification
)

// validChecksumMode reports whether m is a known mode.
func validChecksumMode(m string) bool {
	return m == ChecksumOff || m == ChecksumWarn || m == ChecksumEnforce
}

// copyVerifyDigest copies src to dst and checks the bytes against d. OCI
// content is addressed by digest, so a proxy must never hand a client (or its
// cache) bytes that do not hash to the digest they were requested by.
func copyVerifyDigest(dst io.Writer, src io.Reader, d digest.Digest) error {
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(dst, h), src); err != nil {
		return err
	}
	got := "sha256:" + hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, string(d)) {
		return &ChecksumError{What: string(d), Algo: "sha256", Want: string(d), Got: got}
	}
	return nil
}

// verifyManifestDigest checks manifest bytes fetched by digest reference. A
// tag reference carries no expectation and is accepted as is.
func verifyManifestDigest(reference string, content []byte) error {
	d, err := digest.Parse(reference)
	if err != nil {
		return nil // a tag
	}
	if got := digest.FromBytes(content); got != d {
		return &ChecksumError{What: reference, Algo: "sha256", Want: string(d), Got: string(got)}
	}
	return nil
}

// objectSums hashes a download with every algorithm a sidecar may use, in the
// same pass that stores it.
type objectSums struct {
	algos []string
	hs    map[string]hash.Hash
}

func newObjectSums() *objectSums {
	s := &objectSums{
		algos: []string{"sha512", "sha256", "sha1", "md5"}, // strongest first
		hs: map[string]hash.Hash{
			"sha512": sha512.New(), "sha256": sha256.New(), "sha1": sha1.New(), "md5": md5.New(),
		},
	}
	return s
}

func (s *objectSums) writer() io.Writer {
	ws := make([]io.Writer, 0, len(s.hs))
	for _, a := range s.algos {
		ws = append(ws, s.hs[a])
	}
	return io.MultiWriter(ws...)
}

func (s *objectSums) hex(algo string) string { return hex.EncodeToString(s.hs[algo].Sum(nil)) }

// parseChecksumFile extracts the hex digest from a sidecar: either the bare
// hash or "hash  filename" (sha256sum / Maven style).
func parseChecksumFile(b []byte) string {
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return ""
	}
	h := strings.ToLower(f[0])
	if _, err := hex.DecodeString(h); err != nil {
		return ""
	}
	return h
}

// sidecarFetcher returns the bytes of a small companion file from the upstream.
type sidecarFetcher func(path string) ([]byte, error)

// verifyObjectSums compares a download against the checksum the upstream
// publishes beside it (path + ".sha512|.sha256|.sha1|.md5"), strongest first.
// algo is "" when the upstream offers no checksum: unverifiable, not failed.
func verifyObjectSums(p string, sums *objectSums, fetch sidecarFetcher) (algo string, err error) {
	for _, a := range sums.algos {
		raw, ferr := fetch(p + "." + a)
		if ferr != nil {
			continue
		}
		want := parseChecksumFile(raw)
		if want == "" {
			continue
		}
		if got := sums.hex(a); got != want {
			return a, &ChecksumError{What: p, Algo: a, Want: want, Got: got}
		}
		return a, nil
	}
	return "", nil
}

// checksumApplies skips files that are checksums themselves, and generated
// or cached indexes that have no sidecar of their own.
func checksumApplies(p string) bool {
	if isIndexPath(p) {
		return false
	}
	for _, ext := range []string{".sha512", ".sha256", ".sha1", ".md5", ".asc", ".sig"} {
		if strings.HasSuffix(p, ext) {
			return false
		}
	}
	return true
}

func logChecksum(registry, p, algo string, err error) {
	switch {
	case err != nil:
		log.Printf("checksum: registry %q: %v", registry, err)
	case algo == "":
		// Nothing published to verify against; stay quiet, this is the common case.
	}
}

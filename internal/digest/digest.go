package digest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"regexp"
	"strings"
)

// NewHash returns a new hash.Hash for the algorithm. Only SHA256 is supported.
func (a Algorithm) NewHash() hash.Hash {
	switch a {
	case SHA256:
		return sha256.New()
	default:
		panic(fmt.Sprintf("registry/digest: unsupported algorithm %q", a))
	}
}

// String returns the algorithm name (e.g. "sha256").
func (a Algorithm) String() string { return string(a) }

// EncodeHex is a thin wrapper around hex.EncodeToString for convenience.
func EncodeHex(b []byte) string {
	return hex.EncodeToString(b)
}

// Algorithm identifies a digest algorithm.
type Algorithm string

const (
	SHA256 Algorithm = "sha256"
)

// Digest is a content-addressable identifier of the form "<algo>:<hex>".
type Digest string

var digestRegexp = regexp.MustCompile(`^[a-z0-9]+(?:[.+_-][a-z0-9]+)*:[a-fA-F0-9]+$`)

// FromBytes computes the canonical sha256 digest of b.
func FromBytes(b []byte) Digest {
	sum := sha256.Sum256(b)
	return Digest(fmt.Sprintf("sha256:%s", hex.EncodeToString(sum[:])))
}

// FromString computes the canonical sha256 digest of s.
func FromString(s string) Digest {
	return FromBytes([]byte(s))
}

// Parse validates and returns a Digest from its string form.
func Parse(s string) (Digest, error) {
	if !digestRegexp.MatchString(s) {
		return "", fmt.Errorf("registry/digest: invalid digest %q", s)
	}
	idx := strings.IndexByte(s, ':')
	algo := Algorithm(s[:idx])
	if algo != SHA256 {
		return "", fmt.Errorf("registry/digest: unsupported algorithm %q", algo)
	}
	hexStr := s[idx+1:]
	if len(hexStr) != 64 {
		return "", fmt.Errorf("registry/digest: invalid sha256 length %d", len(hexStr))
	}
	if _, err := hex.DecodeString(hexStr); err != nil {
		return "", fmt.Errorf("registry/digest: %w", err)
	}
	return Digest(s), nil
}

// MustParse is like Parse but panics on error.
func MustParse(s string) Digest {
	d, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return d
}

// String returns the canonical string representation.
func (d Digest) String() string { return string(d) }

// Algorithm returns the digest algorithm component.
func (d Digest) Algorithm() Algorithm {
	idx := strings.IndexByte(string(d), ':')
	if idx < 0 {
		return ""
	}
	return Algorithm(string(d)[:idx])
}

// Hex returns the hex-encoded hash component.
func (d Digest) Hex() string {
	idx := strings.IndexByte(string(d), ':')
	if idx < 0 {
		return ""
	}
	return string(d)[idx+1:]
}

// Valid reports whether the digest is well-formed.
func (d Digest) Valid() bool {
	_, err := Parse(string(d))
	return err == nil
}

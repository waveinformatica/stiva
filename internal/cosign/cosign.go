// Package cosign verifies cosign "simple signing" container image signatures
// against public keys.
//
// A cosign signature is stored in the same repository as a tag
// "sha256-<hex of the signed manifest digest>.sig". That tag's manifest lists
// one layer per signature; the layer blob is a small JSON payload naming the
// signed digest, and the signature itself rides in a layer annotation. This
// package checks both: the signature over the payload, and that the payload
// really names the digest being verified.
//
// Scope: key-based signatures (ECDSA, RSA, Ed25519 public keys). Keyless
// signing (Fulcio certificates, Rekor) and the newer bundle format are not
// handled here.
package cosign

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"strings"

	"registry/internal/digest"
)

const (
	payloadMediaType    = "application/vnd.dev.cosign.simplesigning.v1+json"
	signatureAnnotation = "dev.cosignproject.cosign/signature"
	payloadType         = "cosign container image signature"

	maxPayloadBytes = 64 << 10
	maxSignatures   = 16
)

// Statuses reported by Check.
const (
	StatusUnsigned = "unsigned" // no signature tag for this digest
	StatusPresent  = "present"  // signatures exist but no key was given to check them
	StatusVerified = "verified" // at least one signature verifies with a trusted key
	StatusInvalid  = "invalid"  // signatures exist but none verifies with a trusted key
)

// Result is the outcome of checking one digest.
type Result struct {
	Status     string `json:"status"`
	Signatures int    `json:"signatures"` // signature layers found
	Verified   int    `json:"verified"`   // of which verify with a trusted key
}

// Fetcher is the part of a registry backend the check needs.
type Fetcher interface {
	GetManifest(repo, reference string) ([]byte, string, error)
	GetBlob(repo string, d digest.Digest) (io.ReadCloser, int64, error)
}

// SignatureTag is the tag cosign stores the signatures of subject under.
func SignatureTag(subject digest.Digest) string {
	return strings.Replace(string(subject), ":", "-", 1) + ".sig"
}

// IsAttachmentTag reports whether tag is a cosign attachment tag (signature,
// attestation or SBOM): content that belongs to another image.
func IsAttachmentTag(tag string) bool {
	if !strings.HasPrefix(tag, "sha256-") {
		return false
	}
	for _, s := range []string{".sig", ".att", ".sbom"} {
		if strings.HasSuffix(tag, s) {
			return true
		}
	}
	return false
}

// ParseKeys reads every PEM public key (PKIX "PUBLIC KEY") in data. It fails
// if there is none, or any block is not a usable public key, so a typo is
// caught when the keys are saved and not when an image is refused.
func ParseKeys(data string) ([]crypto.PublicKey, error) {
	var keys []crypto.PublicKey
	rest := []byte(data)
	for {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		if blk.Type != "PUBLIC KEY" {
			return nil, fmt.Errorf("cosign: unsupported PEM block %q (expected a PUBLIC KEY)", blk.Type)
		}
		k, err := x509.ParsePKIXPublicKey(blk.Bytes)
		if err != nil {
			return nil, fmt.Errorf("cosign: invalid public key: %w", err)
		}
		switch k.(type) {
		case *ecdsa.PublicKey, *rsa.PublicKey, ed25519.PublicKey:
			keys = append(keys, k)
		default:
			return nil, fmt.Errorf("cosign: unsupported key type %T", k)
		}
	}
	if len(keys) == 0 {
		return nil, errors.New("cosign: no PEM public key found")
	}
	return keys, nil
}

// verifySig checks sig (as cosign encodes it) over payload with one key.
func verifySig(key crypto.PublicKey, payload, sig []byte) bool {
	switch k := key.(type) {
	case *ecdsa.PublicKey:
		sum := sha256.Sum256(payload)
		return ecdsa.VerifyASN1(k, sum[:], sig)
	case *rsa.PublicKey:
		sum := sha256.Sum256(payload)
		return rsa.VerifyPKCS1v15(k, crypto.SHA256, sum[:], sig) == nil
	case ed25519.PublicKey:
		return ed25519.Verify(k, payload, sig)
	}
	return false
}

// Check looks up the signatures of subject in repo and verifies them with keys.
// With no keys it only reports whether signatures exist.
func Check(f Fetcher, repo string, subject digest.Digest, keys []crypto.PublicKey) Result {
	content, _, err := f.GetManifest(repo, SignatureTag(subject))
	if err != nil {
		return Result{Status: StatusUnsigned}
	}
	var m struct {
		Layers []struct {
			MediaType   string            `json:"mediaType"`
			Digest      string            `json:"digest"`
			Annotations map[string]string `json:"annotations"`
		} `json:"layers"`
	}
	if json.Unmarshal(content, &m) != nil {
		return Result{Status: StatusUnsigned}
	}
	res := Result{}
	for _, l := range m.Layers {
		if l.MediaType != payloadMediaType || l.Annotations[signatureAnnotation] == "" {
			continue
		}
		if res.Signatures++; res.Signatures > maxSignatures {
			break
		}
		if len(keys) == 0 {
			continue
		}
		if verifyLayer(f, repo, subject, l.Digest, l.Annotations[signatureAnnotation], keys) {
			res.Verified++
		}
	}
	switch {
	case res.Signatures == 0:
		res.Status = StatusUnsigned
	case len(keys) == 0:
		res.Status = StatusPresent
	case res.Verified > 0:
		res.Status = StatusVerified
	default:
		res.Status = StatusInvalid
	}
	return res
}

func verifyLayer(f Fetcher, repo string, subject digest.Digest, layerDigest, sigB64 string, keys []crypto.PublicKey) bool {
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return false
	}
	d, err := digest.Parse(layerDigest)
	if err != nil {
		return false
	}
	rc, _, err := f.GetBlob(repo, d)
	if err != nil {
		return false
	}
	defer rc.Close()
	payload, err := io.ReadAll(io.LimitReader(rc, maxPayloadBytes+1))
	if err != nil || len(payload) > maxPayloadBytes {
		return false
	}
	// The blob must be the content its digest claims: a registry that serves
	// other bytes under a layer digest must not be able to vouch for them.
	if digest.FromBytes(payload) != d {
		return false
	}
	var p struct {
		Critical struct {
			Type  string `json:"type"`
			Image struct {
				Digest string `json:"docker-manifest-digest"`
			} `json:"image"`
		} `json:"critical"`
	}
	if json.Unmarshal(payload, &p) != nil || p.Critical.Type != payloadType ||
		p.Critical.Image.Digest != string(subject) {
		return false
	}
	for _, k := range keys {
		if verifySig(k, payload, sig) {
			return true
		}
	}
	return false
}

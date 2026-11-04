package api

import (
	"crypto"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"registry/internal/cosign"
	"registry/internal/digest"
	"registry/internal/registry"
)

// maxIndexScan bounds how many tags are examined when looking for a signed
// index that contains an unsigned platform manifest.
const maxIndexScan = 200

// signingKeys returns the registry's trusted keys, or nil when none are set.
func signingKeys(reg *registry.Registry) []crypto.PublicKey {
	if reg.Signing == nil || reg.Signing.Keys == "" {
		return nil
	}
	keys, err := cosign.ParseKeys(reg.Signing.Keys)
	if err != nil {
		return nil // validated on save; a corrupt record fails closed in enforce mode
	}
	return keys
}

// sigCacheTTL keeps a verdict for a short while: a pull makes several manifest
// requests, and a verdict may involve reading several manifests upstream.
const sigCacheTTL = 30 * time.Second

type sigVerdict struct {
	res cosign.Result
	exp time.Time
}

var sigCache sync.Map // key -> sigVerdict

func cachedCheck(reg *registry.Registry, be registry.Backend, repo string, subject digest.Digest, reference string) cosign.Result {
	_, parseErr := digest.Parse(reference)
	key := reg.Name + "|" + repo + "|" + string(subject) + "|" + string(digest.FromString(reg.Signing.Keys))
	if v, ok := sigCache.Load(key); ok && time.Now().Before(v.(sigVerdict).exp) {
		return v.(sigVerdict).res
	}
	res, _ := checkSignature(be, repo, subject, signingKeys(reg), parseErr == nil)
	sigCache.Store(key, sigVerdict{res: res, exp: time.Now().Add(sigCacheTTL)})
	return res
}

// checkSignature reports the signature state of the manifest with digest
// subject. A platform manifest carries no signature of its own when its
// multi-arch index was signed instead, so an unsigned digest also counts as
// verified when a tagged, signed index in the repository lists it.
func checkSignature(be registry.Backend, repo string, subject digest.Digest, keys []crypto.PublicKey, scanIndexes bool) (res cosign.Result, viaIndex bool) {
	res = cosign.Check(be, repo, subject, keys)
	if res.Status == cosign.StatusVerified || len(keys) == 0 || !scanIndexes {
		return res, false
	}
	tags, err := be.ListTags(repo)
	if err != nil {
		return res, false
	}
	for i, tag := range tags {
		if i >= maxIndexScan {
			break
		}
		if cosign.IsAttachmentTag(tag) {
			continue
		}
		content, _, err := be.GetManifest(repo, tag)
		if err != nil || !listsChild(content, subject) {
			continue
		}
		if cosign.Check(be, repo, digest.FromBytes(content), keys).Status == cosign.StatusVerified {
			return cosign.Result{Status: cosign.StatusVerified, Signatures: res.Signatures, Verified: 1}, true
		}
	}
	return res, false
}

// signatureGate applies the registry's signing policy to a manifest about to
// be served. It reports whether the request may proceed; when it may not, the
// 403 has been written. Signature and attestation tags are never gated: they
// belong to the image they describe.
func (h *Handler) signatureGate(c *gin.Context, be registry.Backend, repo, reference string, content []byte) bool {
	reg, ok := h.mgr.Get(c.GetString("registry"))
	if !ok || reg.Signing == nil || reg.Signing.Mode == registry.SigningOff {
		return true
	}
	if cosign.IsAttachmentTag(reference) {
		return true
	}
	res := cachedCheck(reg, be, repo, digest.FromBytes(content), reference)
	if res.Status == cosign.StatusVerified {
		c.Header("X-Stiva-Signature", "verified")
		return true
	}
	c.Header("X-Stiva-Signature", res.Status)
	if reg.Signing.Mode == registry.SigningEnforce {
		c.JSON(http.StatusForbidden, errorBody("DENIED",
			"image is not signed by a trusted key ("+res.Status+"): pull refused by this registry's signing policy"))
		return false
	}
	return true
}

// uiSignature serves GET /api/v1/signature?registry=&repo=&ref=: the cosign
// signature state of one image, for the browser.
func (h *Handler) uiSignature(c *gin.Context) {
	name := h.registryParam(c)
	reg, ok := h.mgr.Get(name)
	if !ok || !h.allowAccess(c, reg, false) {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}
	repo, ref := c.Query("repo"), c.Query("ref")
	if repo == "" || ref == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repo and ref are required"})
		return
	}
	if reg.Format != string(registry.FormatDocker) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "signatures apply to OCI registries"})
		return
	}
	be, err := h.mgr.BackendFor(name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	content, _, err := be.GetManifest(repo, ref)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "manifest not found"})
		return
	}
	subject := digest.FromBytes(content)
	_, refIsDigest := digest.Parse(ref)
	res, via := checkSignature(be, repo, subject, signingKeys(reg), refIsDigest == nil)
	c.JSON(http.StatusOK, gin.H{
		"digest": subject.String(), "status": res.Status,
		"signatures": res.Signatures, "verified": res.Verified,
		"via_index": via, "keys": signingKeys(reg) != nil,
	})
}

// listsChild reports whether the manifest is an index that lists child.
func listsChild(content []byte, child digest.Digest) bool {
	var idx struct {
		Manifests []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
	}
	if json.Unmarshal(content, &idx) != nil {
		return false
	}
	for _, m := range idx.Manifests {
		if m.Digest == string(child) {
			return true
		}
	}
	return false
}

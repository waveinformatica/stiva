package api

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"registry/internal/registry"
	"registry/internal/webhooks"
)

// Hosted npm registries speak the real npm protocol: `npm publish` sends a JSON
// document with the tarball inside, and `npm install` reads a metadata document
// that points at the tarballs. Tarballs are what is stored; the metadata is
// derived from them, so there is nothing to keep in step.

const maxPublishBytes = 512 << 20

// The version part of a tarball name: no path separators, whatever the scope.
var npmVersionTgz = regexp.MustCompile(`^[A-Za-z0-9._~+-]+\.tgz$`)

type npmAttachment struct {
	filename string
	data     []byte
}

// parseNpmPublish validates an npm publish document for package pkg and returns
// its tarballs. Each tarball must be named <name>-<version>.tgz and really
// contain that package at that version, so a client cannot plant files under
// another package's name.
func parseNpmPublish(body []byte, pkg string) ([]npmAttachment, error) {
	var doc struct {
		Name        string                     `json:"name"`
		Versions    map[string]json.RawMessage `json:"versions"`
		Attachments map[string]struct {
			Data string `json:"data"`
		} `json:"_attachments"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("invalid publish document: %w", err)
	}
	if doc.Name != pkg {
		return nil, fmt.Errorf("document is for %q but was sent to %q", doc.Name, pkg)
	}
	if len(doc.Attachments) == 0 {
		return nil, errors.New("publish document carries no tarball")
	}
	unscoped := path.Base(pkg)
	var out []npmAttachment
	for key, att := range doc.Attachments {
		// npm names the attachment <package>-<version>.tgz with the full
		// package name, scope included; the tarball URL uses the unscoped form.
		var rest string
		switch {
		case strings.HasPrefix(key, pkg+"-"):
			rest = strings.TrimPrefix(key, pkg+"-")
		case strings.HasPrefix(key, unscoped+"-"):
			rest = strings.TrimPrefix(key, unscoped+"-")
		default:
			return nil, fmt.Errorf("unexpected tarball name %q", key)
		}
		if !npmVersionTgz.MatchString(rest) {
			return nil, fmt.Errorf("unexpected tarball name %q", key)
		}
		version := strings.TrimSuffix(rest, ".tgz")
		filename := unscoped + "-" + rest
		if _, ok := doc.Versions[version]; !ok {
			return nil, fmt.Errorf("tarball %q has no matching version in the document", key)
		}
		data, err := base64.StdEncoding.DecodeString(att.Data)
		if err != nil || len(data) == 0 {
			return nil, fmt.Errorf("tarball %q is not valid base64", key)
		}
		pj, err := extractPackageJSON(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("tarball %q: %w", key, err)
		}
		if n, _ := pj["name"].(string); n != pkg {
			return nil, fmt.Errorf("tarball %q contains package %q, not %q", key, n, pkg)
		}
		if v, _ := pj["version"].(string); v != version {
			return nil, fmt.Errorf("tarball %q contains version %q, not %q", key, v, version)
		}
		out = append(out, npmAttachment{filename: filename, data: data})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].filename < out[j].filename })
	return out, nil
}

// npmPublish serves PUT /<package> from `npm publish`.
func (h *Handler) npmPublish(c *gin.Context, be registry.ArtifactBackend, reg *registry.Registry, pkg string) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxPublishBytes+1))
	if err != nil || len(body) > maxPublishBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "publish document too large"})
		return
	}
	atts, err := parseNpmPublish(body, pkg)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// A published version is immutable, as npm clients expect.
	for _, a := range atts {
		if _, _, err := be.Head(pkg + "/-/" + a.filename); err == nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "You cannot publish over the previously published versions: " + a.filename})
			return
		}
	}
	for _, a := range atts {
		p := pkg + "/-/" + a.filename
		if err := be.Put(p, "application/octet-stream", bytes.NewReader(a.data)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		h.emit(c, webhooks.EventPushArtifact, map[string]any{"path": p, "format": reg.Format, "content_type": "application/octet-stream"})
	}
	c.JSON(http.StatusCreated, gin.H{"ok": true, "id": pkg})
}

// generateNpmMetadata builds the package document `npm install` reads, from
// the tarballs stored under <pkg>/-/. Every version carries a dist block with
// the tarball URL and its checksums; "latest" is the highest stable version.
func generateNpmMetadata(be registry.ArtifactBackend, pkg, endpoint string) ([]byte, error) {
	prefix := pkg + "/-/"
	objs, err := be.List(prefix)
	if err != nil {
		return nil, err
	}
	versions := map[string]interface{}{}
	var names []string
	for _, o := range objs {
		if !strings.HasSuffix(o, ".tgz") || strings.Contains(strings.TrimPrefix(o, prefix), "/") {
			continue
		}
		rc, _, _, err := be.Get(o)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		pj, err := extractPackageJSON(bytes.NewReader(data))
		if err != nil {
			continue
		}
		ver, _ := pj["version"].(string)
		if ver == "" {
			continue
		}
		s1 := sha1.Sum(data)
		s512 := sha512.Sum512(data)
		pj["name"] = pkg
		pj["_id"] = pkg + "@" + ver
		pj["dist"] = map[string]string{
			"tarball":   endpoint + "/" + o,
			"shasum":    hex.EncodeToString(s1[:]),
			"integrity": "sha512-" + base64.StdEncoding.EncodeToString(s512[:]),
		}
		versions[ver] = pj
		names = append(names, ver)
	}
	doc := map[string]interface{}{
		"_id":       pkg,
		"name":      pkg,
		"versions":  versions,
		"dist-tags": map[string]string{},
	}
	if len(names) > 0 {
		doc["dist-tags"] = map[string]string{"latest": pickLatestTag(versionTags(names))}
	}
	return json.MarshalIndent(doc, "", "  ")
}

func versionTags(versions []string) []tagView {
	out := make([]tagView, len(versions))
	for i, v := range versions {
		out[i] = tagView{Tag: v}
	}
	return out
}

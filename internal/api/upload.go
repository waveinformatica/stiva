package api

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"registry/internal/auth"
	"registry/internal/registry"
	"registry/internal/webhooks"
)

// Upload from the web UI: PUT /api/v1/artifact?registry=&path=. The registry's
// own protocol endpoints sit behind virtual hosts, ports or base paths that the
// UI's origin need not reach, so the UI writes through this one instead. It
// applies the same rules as a direct push: signed in, write access, no cache
// registries, proxies only when they allow writes.

const maxWebUpload = 2 << 30

// cleanArtifactPath normalises a destination path and refuses anything that
// could climb out of the registry or hide in odd characters.
func cleanArtifactPath(p string) (string, error) {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	trailing := strings.HasSuffix(p, "/")
	var segs []string
	for _, s := range strings.Split(p, "/") {
		switch {
		case s == "":
			continue
		case s == "." || s == "..":
			return "", errors.New("the path must not contain . or .. segments")
		}
		for _, r := range s {
			if r < 0x20 || r == 0x7f {
				return "", errors.New("the path contains control characters")
			}
		}
		segs = append(segs, s)
	}
	out := strings.Join(segs, "/")
	if len(out) > 1024 {
		return "", errors.New("the path is too long")
	}
	if trailing && out != "" {
		out += "/"
	}
	return out, nil
}

// npmTarballTarget names where an npm tarball (from `npm pack`) belongs, from
// the package.json inside it: <name>/-/<unscoped>-<version>.tgz, which is where
// the install metadata looks for it.
func npmTarballTarget(data []byte) (string, error) {
	pj, err := extractPackageJSON(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("not an npm tarball: %w", err)
	}
	name, _ := pj["name"].(string)
	version, _ := pj["version"].(string)
	if name == "" || version == "" || strings.Contains(name, "..") || !npmVersionTgz.MatchString(version+".tgz") {
		return "", errors.New("the package.json has no usable name and version")
	}
	return name + "/-/" + path.Base(name) + "-" + version + ".tgz", nil
}

// webUploadTarget decides where an uploaded file is stored. dir is the
// destination folder the user typed ("" = the registry root); filename is the
// file's own name. Formats whose clients read a document or a fixed layout get
// the file placed for them, so an upload from the page behaves like a publish.
func webUploadTarget(format registry.Format, dir, filename string, data []byte) (string, error) {
	filename = path.Base(strings.ReplaceAll(filename, "\\", "/"))
	if filename == "" || filename == "." || filename == "/" {
		return "", errors.New("the file has no name")
	}
	switch format {
	case registry.FormatNpm:
		if !strings.HasSuffix(filename, ".tgz") {
			return "", errors.New("upload the tarball made by `npm pack` (.tgz)")
		}
		return npmTarballTarget(data)
	case registry.FormatPyPI:
		pkg, _, ok := registry.ParsePackagePath(registry.FormatPyPI, filename)
		if !ok {
			return "", errors.New("upload a wheel or source distribution (.whl, .tar.gz)")
		}
		return pypiTarget(pkg, filename)
	case registry.FormatConan:
		return "", errors.New("Conan packages are uploaded with `conan upload`")
	case registry.FormatDocker:
		return "", errors.New("images are pushed with a container client")
	}
	clean, err := cleanArtifactPath(dir)
	if err != nil {
		return "", err
	}
	clean = strings.TrimSuffix(clean, "/")
	if clean == "" {
		return filename, nil
	}
	return clean + "/" + filename, nil
}

// uiArtifactUpload serves PUT /api/v1/artifact?registry=&path=<dir>&name=<file>.
// The body is the file, raw. overwrite=1 replaces an existing file.
func (h *Handler) uiArtifactUpload(c *gin.Context) {
	name := c.Query("registry")
	reg, ok := h.mgr.Get(name)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}
	if u, _ := c.Get("user"); u != nil {
		if usr, ok := u.(*auth.User); ok && usr.IsAnonymous() {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "sign in to upload"})
			return
		}
	}
	if registry.Format(reg.Format) == registry.FormatDocker {
		c.JSON(http.StatusBadRequest, gin.H{"error": "images are pushed with a container client"})
		return
	}
	switch registry.Type(reg.Type) {
	case registry.TypeCache:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "this registry is read-only"})
		return
	case registry.TypeProxy:
		if !reg.ProxyAllowWrite {
			c.JSON(http.StatusForbidden, gin.H{"error": "this proxy registry does not allow writes"})
			return
		}
	}
	if !h.allowAccess(c, reg, true) {
		c.JSON(http.StatusForbidden, gin.H{"error": "you do not have write access to this registry"})
		return
	}
	be, err := h.mgr.ArtifactBackendFor(name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer be.Close()

	format := registry.Format(reg.Format)
	body := http.MaxBytesReader(c.Writer, c.Request.Body, maxWebUpload)
	var data []byte
	var reader io.Reader = body
	// npm needs to look inside the tarball to place it.
	if format == registry.FormatNpm {
		data, err = io.ReadAll(io.LimitReader(body, maxPublishBytes+1))
		if err != nil || len(data) > maxPublishBytes {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "tarball too large"})
			return
		}
		reader = bytes.NewReader(data)
	}
	target, err := webUploadTarget(format, c.Query("path"), c.Query("name"), data)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Package formats keep a published version immutable; plain files may be
	// replaced when the caller says so.
	immutable := format == registry.FormatNpm || format == registry.FormatPyPI
	if _, _, err := be.Head(target); err == nil && (immutable || c.Query("overwrite") != "1") {
		c.JSON(http.StatusConflict, gin.H{"error": "already exists: " + target})
		return
	}
	ct := c.GetHeader("Content-Type")
	if ct == "" || ct == "application/octet-stream" {
		ct = registry.ContentTypeForPath(target)
	}
	if err := be.Put(target, ct, reader); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Set("registry", reg.Name)
	h.emit(c, webhooks.EventPushArtifact, map[string]any{"path": target, "format": reg.Format, "content_type": ct})
	c.JSON(http.StatusCreated, gin.H{"path": target})
}

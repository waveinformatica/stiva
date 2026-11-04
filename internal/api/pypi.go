package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"registry/internal/registry"
	"registry/internal/webhooks"
)

// Hosted PyPI registries accept uploads the way twine sends them (the legacy
// upload API: a multipart POST with the file and its metadata) and serve them
// back through the simple index, so `twine upload` and `pip install` work as
// they do against PyPI.

const maxUploadBytes = 1 << 30

var (
	pypiSep      = regexp.MustCompile(`[-_.]+`)
	pypiFilename = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*\.(whl|tar\.gz|zip|tar\.bz2)$`)
)

// pypiNormalize is PEP 503 name normalisation: the form pip asks the simple
// index for, so it is also the directory a project is stored under.
func pypiNormalize(name string) string {
	return strings.ToLower(pypiSep.ReplaceAllString(name, "-"))
}

// pypiTarget decides where an uploaded file is stored, or why it is refused.
// The project the file name declares must be the project the form names, so an
// upload cannot place a file under another project.
func pypiTarget(name, filename string) (string, error) {
	if name == "" {
		return "", errors.New("the upload names no project")
	}
	if !pypiFilename.MatchString(filename) {
		return "", fmt.Errorf("unsupported file name %q", filename)
	}
	pkg, _, ok := registry.ParsePackagePath(registry.FormatPyPI, filename)
	if !ok {
		return "", fmt.Errorf("cannot read the project and version from %q", filename)
	}
	project := pypiNormalize(name)
	if pkg != project {
		return "", fmt.Errorf("file %q belongs to project %q, not %q", filename, pkg, project)
	}
	return project + "/" + filename, nil
}

// pypiUpload serves POST / from `twine upload`.
func (h *Handler) pypiUpload(c *gin.Context, be registry.ArtifactBackend, reg *registry.Registry) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBytes)
	if err := c.Request.ParseMultipartForm(64 << 20); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid upload: " + err.Error()})
		return
	}
	if a := c.Request.FormValue(":action"); a != "" && a != "file_upload" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported action " + a})
		return
	}
	f, hdr, err := c.Request.FormFile("content")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the upload has no file"})
		return
	}
	defer f.Close()
	target, err := pypiTarget(c.Request.FormValue("name"), hdr.Filename)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if _, _, err := be.Head(target); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "File already exists: " + hdr.Filename})
		return
	}
	if err := be.Put(target, "application/octet-stream", io.Reader(f)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.emit(c, webhooks.EventPushArtifact, map[string]any{"path": target, "format": reg.Format, "content_type": "application/octet-stream"})
	c.String(http.StatusOK, "OK")
}

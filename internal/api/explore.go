package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"registry/internal/authz"
	"registry/internal/storage"
)

// What the interface shows of the scans (docs/scanning.md, "Browsing
// findings"): packages, vulnerabilities and the findings of one image, in the
// registries the caller can read. Vulnerability sets are under /scan/sets.
const (
	findingsDefaultLimit = 100
	findingsMaxLimit     = 500
	// findingsImageLimit bounds the images listed with a package or a
	// vulnerability (the running ones first); the count is always given.
	findingsImageLimit = 200
	minSearchLength    = 2
)

// registerFindings mounts the read endpoints under /api/v1/findings.
func (h *Handler) registerFindings(v1 gin.IRouter) {
	f := v1.Group("/findings")
	f.GET("/packages", h.findingsPackages)
	f.GET("/packages/:key", h.findingsPackage)
	f.GET("/vulnerabilities", h.findingsVulnerabilities)
	f.GET("/vulnerabilities/:id", h.findingsVulnerability)
	f.GET("/image", h.findingsImage)
}

func findingsLimit(c *gin.Context) (int, bool) {
	limit := findingsDefaultLimit
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > findingsMaxLimit {
			return400(c, "limit must be between 1 and "+strconv.Itoa(findingsMaxLimit))
			return 0, false
		}
		limit = n
	}
	return limit, true
}

// findingsPackages handles GET /api/v1/findings/packages?q=&limit=: the
// packages whose name or purl contains q.
func (h *Handler) findingsPackages(c *gin.Context) {
	if !h.findingsReady(c) {
		return
	}
	q := strings.TrimSpace(c.Query("q"))
	if len(q) < minSearchLength {
		return400(c, "q must be at least 2 characters")
		return
	}
	limit, ok := findingsLimit(c)
	if !ok {
		return
	}
	pkgs, err := h.findings.SearchPackages(h.scanRegistries(c, authz.RegistryRead), q, limit, placementFreshness)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"packages": pkgs})
}

// findingsVulnerabilities handles GET
// /api/v1/findings/vulnerabilities?q=&severity=&limit=: the vulnerabilities
// whose identifier or title contains q, of one severity. One of the two is
// required.
func (h *Handler) findingsVulnerabilities(c *gin.Context) {
	if !h.findingsReady(c) {
		return
	}
	q := strings.TrimSpace(c.Query("q"))
	sev := c.Query("severity")
	if sev != "" && !validSeverity(sev) {
		return400(c, "unknown severity")
		return
	}
	if q != "" && len(q) < minSearchLength {
		return400(c, "q must be at least 2 characters")
		return
	}
	if q == "" && sev == "" {
		return400(c, "q or severity is required")
		return
	}
	limit, ok := findingsLimit(c)
	if !ok {
		return
	}
	vs, err := h.findings.SearchVulnerabilities(h.scanRegistries(c, authz.RegistryRead), q, sev, limit, placementFreshness)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"vulnerabilities": vs})
}

// findingsPackage handles GET /api/v1/findings/packages/:key.
func (h *Handler) findingsPackage(c *gin.Context) {
	if !h.findingsReady(c) {
		return
	}
	d, err := h.findings.GetPackage(c.Param("key"), h.scanRegistries(c, authz.RegistryRead), findingsImageLimit, placementFreshness)
	if errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "no image you can read contains this package"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, d)
}

// findingsVulnerability handles GET /api/v1/findings/vulnerabilities/:id.
func (h *Handler) findingsVulnerability(c *gin.Context) {
	if !h.findingsReady(c) {
		return
	}
	d, err := h.findings.GetVulnerability(c.Param("id"), h.scanRegistries(c, authz.RegistryRead), findingsImageLimit, placementFreshness)
	if errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "no image you can read carries this vulnerability"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, d)
}

// findingsImage handles GET /api/v1/findings/image?registry=&repo=&digest=:
// the packages, vulnerabilities and secrets of one image manifest and where it
// runs. An image without a scan answers scanned=false (with the failure, if
// its scan failed).
func (h *Handler) findingsImage(c *gin.Context) {
	if !h.findingsReady(c) {
		return
	}
	_, t, ok := h.scanTarget(c, authz.RegistryRead)
	if !ok {
		return
	}
	f, err := h.findings.GetImageFindings(t, placementFreshness)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, f)
}

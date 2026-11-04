package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"registry/internal/registry"
)

// adminGetCleanup serves GET /api/v1/admin/registries/:name/cleanup.
func (h *Handler) adminGetCleanup(c *gin.Context) {
	reg, ok := h.mgr.Get(c.Param("name"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}
	p := reg.Cleanup
	if p == nil {
		p = &registry.CleanupPolicy{Protect: []string{}}
	}
	if p.Protect == nil {
		p.Protect = []string{}
	}
	c.JSON(http.StatusOK, p)
}

// adminSetCleanup serves PUT /api/v1/admin/registries/:name/cleanup. It
// changes only the policy; the rest of the registry definition is untouched.
func (h *Handler) adminSetCleanup(c *gin.Context) {
	reg, ok := h.mgr.Get(c.Param("name"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}
	var p registry.CleanupPolicy
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := p.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	updated := *reg
	updated.Cleanup = &p
	if err := h.mgr.Update(&updated, nil); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, p)
}

// adminRunCleanup serves POST /api/v1/admin/registries/:name/cleanup/run.
// dry_run lists what the stored policy would delete without deleting.
func (h *Handler) adminRunCleanup(c *gin.Context) {
	var b struct {
		DryRun bool `json:"dry_run"`
	}
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	report, err := h.mgr.Cleanup(c.Request.Context(), c.Param("name"), b.DryRun)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, report)
}

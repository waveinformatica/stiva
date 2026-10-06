package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// gcBody configures a garbage-collection run. Registry empty means every
// registry with a local store; dry_run lists without deleting; older_than
// bounds the grace period protecting blobs of pushes still in flight.
type gcBody struct {
	Registry  string `json:"registry"`
	DryRun    bool   `json:"dry_run"`
	OlderThan string `json:"older_than"`
}

// adminGC serves POST /api/v1/admin/gc: reap orphan blobs (content no manifest
// links and no artifact object embeds). Dry runs are the expected first step:
// they report counts, bytes and the blob sample without deleting anything.
func (h *Handler) adminGC(c *gin.Context) {
	var b gcBody
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	olderThan := time.Hour
	if b.OlderThan != "" {
		d, err := time.ParseDuration(b.OlderThan)
		if err != nil || d < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "older_than must be a Go duration like 1h, 30m, 0s"})
			return
		}
		olderThan = d
	}
	report, err := h.mgr.GC(c.Request.Context(), b.Registry, olderThan, b.DryRun)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, report)
}

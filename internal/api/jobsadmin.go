package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"registry/internal/jobs"
	"registry/internal/storage"
)

// minScheduleInterval keeps a typo from turning a retention schedule into a
// tight loop over the whole registry.
const minScheduleInterval = 5 * time.Minute

// SetJobs wires the background job runner and its store.
func (h *Handler) SetJobs(r *jobs.Runner, s storage.JobStore) {
	h.jobRunner = r
	h.jobStore = s
}

func (h *Handler) jobsReady(c *gin.Context) bool {
	if h.jobRunner == nil || h.jobStore == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "background jobs not configured"})
		return false
	}
	return true
}

// adminListJobs serves GET /api/v1/admin/jobs?registry=&kind=&status=&limit=.
func (h *Handler) adminListJobs(c *gin.Context) {
	if !h.jobsReady(c) {
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	list, err := h.jobStore.ListJobs(c.Query("registry"), c.Query("kind"), c.Query("status"), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"jobs": list})
}

// adminGetJob serves GET /api/v1/admin/jobs/:id, including the result body.
func (h *Handler) adminGetJob(c *gin.Context) {
	if !h.jobsReady(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job id"})
		return
	}
	j, err := h.jobStore.GetJob(id)
	if errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, j)
}

type enqueueBody struct {
	Registry string          `json:"registry"`
	Kind     string          `json:"kind"`
	DryRun   bool            `json:"dry_run"`
	Params   json.RawMessage `json:"params"`
}

// adminEnqueueJob serves POST /api/v1/admin/jobs: queue a job to run in the background.
func (h *Handler) adminEnqueueJob(c *gin.Context) {
	if !h.jobsReady(c) {
		return
	}
	var b enqueueBody
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if _, ok := h.mgr.Get(b.Registry); !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}
	by := ""
	if u := h.currentUser(c); u != nil {
		by = u.Name
	}
	id, err := h.jobRunner.Enqueue(storage.Job{
		Registry: b.Registry, Kind: b.Kind, DryRun: b.DryRun, Params: b.Params, CreatedBy: by,
	})
	if errors.Is(err, storage.ErrJobActive) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"id": id})
}

// adminListSchedules serves GET /api/v1/admin/schedules?registry=.
func (h *Handler) adminListSchedules(c *gin.Context) {
	if !h.jobsReady(c) {
		return
	}
	list, err := h.jobStore.ListSchedules(c.Query("registry"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"schedules": list})
}

type scheduleBody struct {
	Enabled  bool            `json:"enabled"`
	Interval string          `json:"interval"` // Go duration, e.g. "24h"
	Params   json.RawMessage `json:"params"`
}

// adminSetSchedule serves PUT /api/v1/admin/schedules/:registry/:kind.
func (h *Handler) adminSetSchedule(c *gin.Context) {
	if !h.jobsReady(c) {
		return
	}
	name, kind := c.Param("registry"), c.Param("kind")
	if _, ok := h.mgr.Get(name); !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}
	known := false
	for _, k := range h.jobRunner.Kinds() {
		known = known || k == kind
	}
	if !known {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown job kind"})
		return
	}
	var b scheduleBody
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	d, err := time.ParseDuration(b.Interval)
	if err != nil || d < minScheduleInterval {
		c.JSON(http.StatusBadRequest, gin.H{"error": "interval must be a Go duration of at least 5m, like 6h or 24h"})
		return
	}
	s := storage.JobSchedule{
		Registry: name, Kind: kind, Enabled: b.Enabled, Interval: int64(d / time.Second), Params: b.Params,
	}
	if err := h.jobStore.SetSchedule(s); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	got, _ := h.jobStore.GetSchedule(name, kind)
	c.JSON(http.StatusOK, got)
}

// adminDeleteSchedule serves DELETE /api/v1/admin/schedules/:registry/:kind.
func (h *Handler) adminDeleteSchedule(c *gin.Context) {
	if !h.jobsReady(c) {
		return
	}
	if err := h.jobStore.DeleteSchedule(c.Param("registry"), c.Param("kind")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

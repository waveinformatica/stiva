package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"registry/internal/auth"
)

// ---- Single sign-on providers (admin UI-managed) ----

// adminListSSOPresets serves GET /api/v1/admin/sso/presets: the known
// providers with their required and editable fields, so the admin form only
// asks for what each preset actually uses.
func (h *Handler) adminListSSOPresets(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"presets": auth.SSOPresets()})
}

// adminListSSOProviders serves GET /api/v1/admin/sso/providers. Secrets never
// leave the server; HasSecret tells the form whether one is stored.
func (h *Handler) adminListSSOProviders(c *gin.Context) {
	list, err := h.authMgr.ListSSOProviders(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"providers": list})
}

// adminCreateSSOProvider serves POST /api/v1/admin/sso/providers.
func (h *Handler) adminCreateSSOProvider(c *gin.Context) {
	var b auth.SSOConfig
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	v, err := h.authMgr.UpsertSSOProvider(c.Request.Context(), b, true)
	if err != nil {
		c.JSON(ssoStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, v)
}

// adminUpdateSSOProvider serves PUT /api/v1/admin/sso/providers/:id.
func (h *Handler) adminUpdateSSOProvider(c *gin.Context) {
	var b auth.SSOConfig
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	b.ID = c.Param("id")
	v, err := h.authMgr.UpsertSSOProvider(c.Request.Context(), b, false)
	if err != nil {
		c.JSON(ssoStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, v)
}

// adminDeleteSSOProvider serves DELETE /api/v1/admin/sso/providers/:id.
func (h *Handler) adminDeleteSSOProvider(c *gin.Context) {
	if err := h.authMgr.DeleteSSOProvider(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(ssoStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func ssoStatus(err error) int {
	if errors.Is(err, auth.ErrSSOExists) {
		return http.StatusConflict
	}
	if errors.Is(err, auth.ErrSSONotFound) {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

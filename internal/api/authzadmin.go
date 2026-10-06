package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"registry/internal/auth"
	"registry/internal/authz"
)

// authzReady guards the endpoints that manage the authorization model.
func (h *Handler) authzReady(c *gin.Context) bool {
	if h.authz == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "authorization not configured"})
		return false
	}
	return true
}

// adminPermissions handles GET /api/v1/admin/permissions.
//
// The vocabulary is declared in code, so the UI can offer a real picker instead
// of a free-text field. Each entry carries a description: a permission name
// alone does not tell you whether "registry:delete" removes images or removes
// the registry.
func (h *Handler) adminPermissions(c *gin.Context) {
	type item struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Admin       bool   `json:"admin"`
	}
	out := make([]item, 0, len(authz.All))
	for _, p := range authz.All {
		out = append(out, item{Name: string(p), Description: authz.Describe[p], Admin: p.IsAdmin()})
	}
	c.JSON(http.StatusOK, gin.H{"permissions": out})
}

// --- roles ---

func (h *Handler) authzListRoles(c *gin.Context) {
	if !h.authzReady(c) {
		return
	}
	roles, err := h.authz.ListRoles(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"roles": roles})
}

func (h *Handler) authzUpsertRole(c *gin.Context) {
	if !h.authzReady(c) {
		return
	}
	var r authz.Role
	if err := c.ShouldBindJSON(&r); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if n := c.Param("name"); n != "" {
		r.Name = n
	}
	if err := h.authz.UpsertRole(c.Request.Context(), r); err != nil {
		c.JSON(statusForAuthzErr(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, r)
}

func (h *Handler) authzDeleteRole(c *gin.Context) {
	if !h.authzReady(c) {
		return
	}
	if err := h.authz.DeleteRole(c.Request.Context(), c.Param("name")); err != nil {
		c.JSON(statusForAuthzErr(err), gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// --- groups ---

func (h *Handler) authzListGroups(c *gin.Context) {
	if !h.authzReady(c) {
		return
	}
	groups, err := h.authz.ListGroups(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"groups": groups})
}

func (h *Handler) authzUpsertGroup(c *gin.Context) {
	if !h.authzReady(c) {
		return
	}
	var g authz.Group
	if err := c.ShouldBindJSON(&g); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if n := c.Param("name"); n != "" {
		g.Name = n
	}
	if err := h.authz.UpsertGroup(c.Request.Context(), g); err != nil {
		c.JSON(statusForAuthzErr(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, g)
}

func (h *Handler) authzDeleteGroup(c *gin.Context) {
	if !h.authzReady(c) {
		return
	}
	if err := h.authz.DeleteGroup(c.Request.Context(), c.Param("name")); err != nil {
		c.JSON(statusForAuthzErr(err), gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// --- grants ---

func (h *Handler) authzListBindings(c *gin.Context) {
	if !h.authzReady(c) {
		return
	}
	list, err := h.authz.ListBindings(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"grants": list})
}

func (h *Handler) authzAddBinding(c *gin.Context) {
	if !h.authzReady(c) {
		return
	}
	var b authz.Binding
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if b.Scope == "" {
		b.Scope = authz.ScopeAll
	}
	if err := h.authz.AddBinding(c.Request.Context(), b); err != nil {
		c.JSON(statusForAuthzErr(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, b)
}

func (h *Handler) authzUpdateBinding(c *gin.Context) {
	if !h.authzReady(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid grant id"})
		return
	}
	var b authz.Binding
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if b.Scope == "" {
		b.Scope = authz.ScopeAll
	}
	if err := h.authz.UpdateBinding(c.Request.Context(), id, b); err != nil {
		c.JSON(statusForAuthzErr(err), gin.H{"error": err.Error()})
		return
	}
	b.ID = id
	c.JSON(http.StatusOK, b)
}

// --- anonymous identities ---

// authzListAnonymous handles GET /api/v1/admin/anonymous.
//
// They are returned in evaluation order — address-restricted first, catch-alls
// last — because that order decides which one a caller lands on, and a list
// shown in any other order would misrepresent the behaviour.
func (h *Handler) authzListAnonymous(c *gin.Context) {
	lr := h.local()
	if lr == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "local auth not enabled"})
		return
	}
	list, err := lr.ListAnonymous(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"identities": list,
		// Whether address filters can be believed at all depends on this.
		"trusted_proxies": h.authMgr.TrustedProxies(),
	})
}

func (h *Handler) authzUpsertAnonymous(c *gin.Context) {
	lr := h.local()
	if lr == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "local auth not enabled"})
		return
	}
	var a auth.AnonymousIdentity
	if err := c.ShouldBindJSON(&a); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if n := c.Param("name"); n != "" {
		a.Name = n
	}
	if a.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "an identity needs a name"})
		return
	}
	if err := lr.UpsertAnonymous(c.Request.Context(), a); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, a)
}

func (h *Handler) authzDeleteAnonymous(c *gin.Context) {
	lr := h.local()
	if lr == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "local auth not enabled"})
		return
	}
	if err := lr.DeleteAnonymous(c.Request.Context(), c.Param("name")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) authzDeleteBinding(c *gin.Context) {
	if !h.authzReady(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid grant id"})
		return
	}
	if err := h.authz.DeleteBinding(c.Request.Context(), id); err != nil {
		c.JSON(statusForAuthzErr(err), gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// statusForAuthzErr maps the model's refusals onto status codes. The
// anti-lockout guard is a conflict rather than a bad request: the change is
// well-formed, it is the resulting state that is not allowed.
func statusForAuthzErr(err error) int {
	switch {
	case errors.Is(err, authz.ErrLastAdmin), errors.Is(err, authz.ErrSystemRole), errors.Is(err, authz.ErrAnonAdmin):
		return http.StatusConflict
	case errors.Is(err, authz.ErrBadScope), errors.Is(err, authz.ErrBadPerm), errors.Is(err, authz.ErrRoleUnknown):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

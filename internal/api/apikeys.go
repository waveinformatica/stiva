package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"registry/internal/auth"
	"registry/internal/authz"
)

// ---- Self-service API keys ----

// validateKeyGrants checks key grants for shape and known roles. Unknown
// registries are warnings, not errors: a key may legitimately precede the
// registry it scopes (automation creates both), and a scope that matches
// nothing simply never authorizes — the owner intersection keeps that safe.
// Unknown roles are always a bug (the vocabulary is closed and admin-managed)
// and fail the request.
func (h *Handler) validateKeyGrants(grants []authz.KeyGrant) ([]string, error) {
	var warnings []string
	for i, g := range grants {
		if !g.Valid() {
			return nil, fmt.Errorf("grant %d is malformed (need a role and a scope like docker:registry:repo/**)", i+1)
		}
		if h.authz == nil || !h.authz.HasRole(g.Role) {
			return nil, fmt.Errorf("grant %d: unknown role %q", i+1, g.Role)
		}
		if _, reg, _ := g.Scope.Parts(); reg != "*" {
			if h.mgr == nil {
				return nil, fmt.Errorf("grant %d: cannot check registry %q", i+1, reg)
			}
			if _, ok := h.mgr.Get(reg); !ok {
				warnings = append(warnings, fmt.Sprintf("grant %d: registry %q does not exist (yet)", i+1, reg))
			}
		}
	}
	return warnings, nil
}

// accountUser resolves the caller for self-service endpoints: authenticated,
// never anonymous (an anonymous identity has no name to own a key with).
func (h *Handler) accountUser(c *gin.Context) (*auth.User, bool) {
	u := h.currentUser(c)
	if u == nil || u.Anonymous || u.Name == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return nil, false
	}
	return u, true
}

type accountKeyBody struct {
	Label  string           `json:"label"`
	Grants []authz.KeyGrant `json:"grants"`
}

// accountListKeys serves GET /api/v1/account/keys: the caller's own keys.
func (h *Handler) accountListKeys(c *gin.Context) {
	u, ok := h.accountUser(c)
	if !ok {
		return
	}
	lr := h.local()
	if lr == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "local auth not enabled"})
		return
	}
	keys, err := lr.ListAPIKeys(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	mine := []auth.APIKeyView{}
	for _, k := range keys {
		if k.Username == u.Name {
			mine = append(mine, k)
		}
	}
	c.JSON(http.StatusOK, gin.H{"service_accounts": mine})
}

// accountCreateKey serves POST /api/v1/account/keys: mint a key for the
// caller, optionally scoped to the given grants.
func (h *Handler) accountCreateKey(c *gin.Context) {
	u, ok := h.accountUser(c)
	if !ok {
		return
	}
	lr := h.local()
	if lr == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "local auth not enabled"})
		return
	}
	var b accountKeyBody
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	warnings, err := h.validateKeyGrants(b.Grants)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	key, err := lr.CreateAPIKey(c.Request.Context(), u.Name, b.Label, b.Grants)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	resp := gin.H{"key": key, "username": u.Name, "label": b.Label}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}
	c.JSON(http.StatusCreated, resp)
}

// accountRevokeKey serves DELETE /api/v1/account/keys/:key: revoke one of the
// caller's own keys. Another user's key never matches, so this cannot touch
// what it must not.
func (h *Handler) accountRevokeKey(c *gin.Context) {
	u, ok := h.accountUser(c)
	if !ok {
		return
	}
	lr := h.local()
	if lr == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "local auth not enabled"})
		return
	}
	if err := lr.RevokeOwnAPIKey(c.Request.Context(), u.Name, c.Param("key")); err != nil {
		if errors.Is(err, auth.ErrKeyNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "api key not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// uiRolesList serves GET /api/v1/roles: the role catalog for grant pickers.
// Role names and their permissions are not secret; every authenticated caller
// needs them to scope API keys (and to read the grants that bind them).
func (h *Handler) uiRolesList(c *gin.Context) {
	if _, ok := h.accountUser(c); !ok {
		return
	}
	if h.authz == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "authorization not configured"})
		return
	}
	roles, err := h.authz.ListRoles(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"roles": roles})
}

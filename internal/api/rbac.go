package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"registry/internal/auth"
	"registry/internal/authz"
	"registry/internal/registry"
)

// principal converts the authenticated identity into the form the authorization
// engine works with. Keeping the two types apart means authorization does not
// depend on how the identity was established — local password, API key, LDAP or
// OIDC all arrive here the same shape.
func principal(u *auth.User) *authz.Principal {
	if u == nil {
		return nil
	}
	return &authz.Principal{
		Name:      u.Name,
		Groups:    u.Groups,
		Anonymous: u.IsAnonymous(),
		KeyGrants: u.KeyGrants,
	}
}

// allowAccess reports whether the current principal may read or write a
// registry as a whole.
//
// Everything goes through the grants now: no admin shortcut, and no
// per-registry access list. Those were two parallel mechanisms, and the one the
// UI let you edit — roles — never affected anything at all.
func (h *Handler) allowAccess(c *gin.Context, reg *registry.Registry, write bool) bool {
	return h.allowRepo(c, reg, "", write)
}

// allowRepo is allowAccess with the repository in hand, so a grant scoped to a
// path ("docker:kosmos/**") can actually narrow to it.
func (h *Handler) allowRepo(c *gin.Context, reg *registry.Registry, repo string, write bool) bool {
	if h.authz == nil || reg == nil {
		return false
	}
	perm := authz.RegistryRead
	if write {
		perm = authz.RegistryWrite
	}
	return h.authz.Allows(principal(h.currentUser(c)), perm, reg.Format, reg.Name, repo)
}

// allowDelete is separate from write on purpose: pushing a new tag and erasing
// an existing one are different powers, and plenty of accounts should have the
// first without the second.
func (h *Handler) allowDelete(c *gin.Context, reg *registry.Registry, repo string) bool {
	if h.authz == nil || reg == nil {
		return false
	}
	return h.authz.Allows(principal(h.currentUser(c)), authz.RegistryDelete, reg.Format, reg.Name, repo)
}

// isAdmin reports whether the caller can administer anything. It replaces the
// boolean column that used to sit on the user row and bypass every check.
func (h *Handler) isAdmin(c *gin.Context) bool {
	if h.authz == nil {
		return false
	}
	return h.authz.IsAdmin(principal(h.currentUser(c)))
}

// requirePerm gates a route on one administrative permission, so the admin API
// is not a single all-or-nothing door.
func (h *Handler) requirePerm(p authz.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.authz == nil {
			c.AbortWithStatusJSON(http.StatusNotImplemented, gin.H{"error": "authorization not configured"})
			return
		}
		if !h.authz.Allows(principal(h.currentUser(c)), p, "", "", "") {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":      "permission required",
				"permission": string(p),
			})
			return
		}
		c.Next()
	}
}

// requireAdmin gates routes any administrator may reach.
func (h *Handler) requireAdmin(c *gin.Context) {
	if !h.isAdmin(c) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "administrator privileges required"})
		return
	}
	c.Next()
}

// denyAccess ends a registry-protocol request the principal may not make,
// choosing between "you may not" and "who are you?".
//
// The difference matters more than it looks. An anonymous principal is resolved
// for every request that carries no credentials — a catch-all identity matches
// everyone — so a caller who simply has not authenticated yet arrives here
// looking like a known principal who is out of permissions. Answering 403 tells
// a client holding perfectly good credentials that it is not allowed, without
// ever asking for them: `docker push` opens with a HEAD on the blob, sees 403,
// and stops before it authenticates.
//
// So anything that did not authenticate gets a challenge, and 403 is kept for a
// caller that did identify itself and still may not proceed.
func (h *Handler) denyAccess(c *gin.Context, msg string) {
	if u := h.currentUser(c); u == nil || u.IsAnonymous() {
		c.Header("WWW-Authenticate", h.authMgr.Challenge(c))
		c.AbortWithStatusJSON(http.StatusUnauthorized, errorBody("UNAUTHORIZED", msg))
		return
	}
	c.AbortWithStatusJSON(http.StatusForbidden, errorBody("DENIED", msg))
}

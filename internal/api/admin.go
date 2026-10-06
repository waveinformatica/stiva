package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"registry/internal/auth"
	"registry/internal/authz"
	"registry/internal/registry"
)

// local returns the local realm (may be nil if local auth is not configured).
func (h *Handler) local() *auth.LocalRealm {
	if h.authMgr == nil {
		return nil
	}
	return h.authMgr.Local()
}

// adminListUsers handles GET /api/v1/admin/users.
func (h *Handler) adminListUsers(c *gin.Context) {
	lr := h.local()
	if lr == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "local auth not enabled"})
		return
	}
	users, err := lr.ListUsers(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}

type createUserBody struct {
	Name     string `json:"name"`
	Password string `json:"password"`
	Admin    bool   `json:"admin"`
}

// adminCreateUser handles POST /api/v1/admin/users.
func (h *Handler) adminCreateUser(c *gin.Context) {
	lr := h.local()
	if lr == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "local auth not enabled"})
		return
	}
	var b createUserBody
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if b.Name == "" || b.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name and password are required"})
		return
	}
	if err := lr.CreateUser(c.Request.Context(), b.Name, b.Password, b.Admin); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"name": b.Name})
}

type updateUserBody struct {
	Password *string `json:"password"`
	Disabled *bool   `json:"disabled"`
	Admin    *bool   `json:"admin"`
}

// adminUpdateUser handles PUT /api/v1/admin/users/:name.
func (h *Handler) adminUpdateUser(c *gin.Context) {
	lr := h.local()
	if lr == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "local auth not enabled"})
		return
	}
	name := c.Param("name")
	var b updateUserBody
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := lr.UpdateUser(c.Request.Context(), name, deref(b.Password), b.Disabled, b.Admin); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// adminDeleteUser handles DELETE /api/v1/admin/users/:name.
func (h *Handler) adminDeleteUser(c *gin.Context) {
	lr := h.local()
	if lr == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "local auth not enabled"})
		return
	}
	if err := lr.DeleteUser(c.Request.Context(), c.Param("name")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// adminGetUserRoles handles GET /api/v1/admin/users/:name/roles.
func (h *Handler) adminGetUserRoles(c *gin.Context) {
	lr := h.local()
	if lr == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "local auth not enabled"})
		return
	}
	roles, err := lr.GetUserRoles(c.Request.Context(), c.Param("name"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"roles": roles})
}

type setRolesBody struct {
	Roles []string `json:"roles"`
}

// adminSetUserRoles handles PUT /api/v1/admin/users/:name/roles.
func (h *Handler) adminSetUserRoles(c *gin.Context) {
	lr := h.local()
	if lr == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "local auth not enabled"})
		return
	}
	var b setRolesBody
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := lr.SetUserRoles(c.Request.Context(), c.Param("name"), b.Roles); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// adminListKeys handles GET /api/v1/admin/service-accounts.
func (h *Handler) adminListKeys(c *gin.Context) {
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
	c.JSON(http.StatusOK, gin.H{"service_accounts": keys})
}

type createKeyBody struct {
	Username string           `json:"username"`
	Label    string           `json:"label"`
	Grants   []authz.KeyGrant `json:"grants"`
}

// adminCreateKey handles POST /api/v1/admin/service-accounts.
func (h *Handler) adminCreateKey(c *gin.Context) {
	lr := h.local()
	if lr == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "local auth not enabled"})
		return
	}
	var b createKeyBody
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if b.Username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username is required"})
		return
	}
	warnings, err := h.validateKeyGrants(b.Grants)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	key, err := lr.CreateAPIKey(c.Request.Context(), b.Username, b.Label, b.Grants)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	resp := gin.H{"key": key, "masked": maskKey(key), "username": b.Username, "label": b.Label}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}
	c.JSON(http.StatusCreated, resp)
}

// adminRevokeKey handles DELETE /api/v1/admin/service-accounts/:key.
func (h *Handler) adminRevokeKey(c *gin.Context) {
	lr := h.local()
	if lr == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "local auth not enabled"})
		return
	}
	if err := lr.RevokeAPIKey(c.Request.Context(), c.Param("key")); err != nil {
		if errors.Is(err, auth.ErrKeyNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "api key not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// adminListRepos handles GET /api/v1/admin/repositories (scoped to a registry).
func (h *Handler) adminListRepos(c *gin.Context) {
	name := h.registryParam(c)
	st := h.mgr.StoreFor(name)
	if st == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}
	repos, err := st.ListRepos()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if repos == nil {
		repos = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"registry": name, "repositories": repos})
}

// adminDeleteRepo handles DELETE /api/v1/admin/repositories/:name (registry-scoped).
func (h *Handler) adminDeleteRepo(c *gin.Context) {
	name := h.registryParam(c)
	st := h.mgr.StoreFor(name)
	if st == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}
	if err := st.DeleteRepo(c.Param("name")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// ---- Registry definitions ----

// adminListRegistries handles GET /api/v1/admin/registries.
func (h *Handler) adminListRegistries(c *gin.Context) {
	list := h.mgr.List()
	// The blob store lives beside the definition rather than inside it, so it
	// is joined back on for the UI, which edits the two together.
	type view struct {
		*registry.Registry
		BlobStore string `json:"blob_store"`
	}
	out := make([]view, 0, len(list))
	for _, r := range list {
		v := view{Registry: r}
		if h.stores != nil {
			v.BlobStore, _ = h.stores.RegistryStore(c.Request.Context(), r.Name)
		}
		out = append(out, v)
	}
	c.JSON(http.StatusOK, gin.H{"registries": out})
}

// adminGetRegistry handles GET /api/v1/admin/registries/:name.
func (h *Handler) adminGetRegistry(c *gin.Context) {
	r, ok := h.mgr.Get(c.Param("name"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}
	store := ""
	if h.stores != nil {
		store, _ = h.stores.RegistryStore(c.Request.Context(), r.Name)
	}
	c.JSON(http.StatusOK, gin.H{"registry": r, "blob_store": store})
}

// adminCreateRegistry handles POST /api/v1/admin/registries.
func (h *Handler) adminCreateRegistry(c *gin.Context) {
	r, store, err := bindRegistry(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.mgr.Create(r, h.linkStore(c, store)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, r)
}

// adminUpdateRegistry handles PUT /api/v1/admin/registries/:name.
func (h *Handler) adminUpdateRegistry(c *gin.Context) {
	r, store, err := bindRegistry(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	r.Name = c.Param("name")
	if err := h.mgr.Update(r, h.linkStore(c, store)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, r)
}

// adminDeleteRegistry handles DELETE /api/v1/admin/registries/:name.
func (h *Handler) adminDeleteRegistry(c *gin.Context) {
	if err := h.mgr.Delete(c.Param("name")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// warmBody is the request body for the pre-warm endpoint.
type warmBody struct {
	Image string `json:"image"` // container image reference, e.g. gcr.io/x/y:v1
	Ref   string `json:"ref"`   // optional explicit reference; defaults to Image's tag/digest
}

// adminWarmRegistry handles POST /api/v1/admin/registries/:name/warm. It pulls
// the given image (manifest + config + layers, recursively for multi-arch
// indexes) into a cache registry so future pulls are served entirely from the
// local store. This is the hook the Kubernetes pre-warm controller calls.
func (h *Handler) adminWarmRegistry(c *gin.Context) {
	name := c.Param("name")
	be, err := h.mgr.BackendFor(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}
	warmer, ok := be.(registry.Warmer)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "registry type does not support warming"})
		return
	}
	var b warmBody
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if b.Image == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "image is required"})
		return
	}
	repo, ref, err := registry.NormalizeImage(b.Image)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if b.Ref != "" {
		ref = b.Ref
	}
	if err := warmer.WarmImage(c.Request.Context(), repo, ref); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"registry": name, "repo": repo, "reference": ref})
}

// adminListRoles handles GET /api/v1/admin/roles.
func (h *Handler) adminListRoles(c *gin.Context) {
	lr := h.local()
	if lr == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "local auth not enabled"})
		return
	}
	roles, err := lr.ListRoles(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"roles": roles})
}

type roleBody struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

// adminUpsertRole handles POST/PUT /api/v1/admin/roles[/:name].
func (h *Handler) adminUpsertRole(c *gin.Context) {
	lr := h.local()
	if lr == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "local auth not enabled"})
		return
	}
	var b roleBody
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	name := c.Param("name")
	if name == "" {
		name = b.Name
	}
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role name is required"})
		return
	}
	if err := lr.UpsertRole(c.Request.Context(), name, b.Description, b.Permissions); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"name": name})
}

// adminDeleteRole handles DELETE /api/v1/admin/roles/:name.
func (h *Handler) adminDeleteRole(c *gin.Context) {
	lr := h.local()
	if lr == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "local auth not enabled"})
		return
	}
	if err := lr.DeleteRole(c.Request.Context(), c.Param("name")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// adminGetSettings handles GET /api/v1/admin/settings. Anonymous access is a
// persisted global setting (default: off).
func (h *Handler) adminGetSettings(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"allow_anonymous": h.authMgr.AllowAnonymous()})
}

type settingsBody struct {
	AllowAnonymous *bool `json:"allow_anonymous"`
}

// adminSetSettings handles PUT /api/v1/admin/settings.
func (h *Handler) adminSetSettings(c *gin.Context) {
	var b settingsBody
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if b.AllowAnonymous == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "allow_anonymous is required"})
		return
	}
	if err := h.authMgr.SetAllowAnonymous(c.Request.Context(), *b.AllowAnonymous); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func maskKey(k string) string {
	if len(k) <= 8 {
		return "****"
	}
	return k[:4] + "****" + k[len(k)-4:]
}

// bindRegistry decodes a registry definition together with the name of the blob
// store it should use. The store is not part of the definition itself: it is a
// reference maintained alongside it, so that "which registries use this store"
// stays a single query instead of a scan over every definition.
func bindRegistry(c *gin.Context) (*registry.Registry, string, error) {
	var body struct {
		registry.Registry
		BlobStore string `json:"blob_store"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		return nil, "", err
	}
	r := body.Registry
	r.Format = registry.NormalizeFormat(r.Format)
	return &r, body.BlobStore, nil
}

// linkStore returns the callback that attaches a blob store to a registry
// between persisting the definition and reloading the manager. It returns nil
// when the request carried no store, leaving whatever link already exists.
func (h *Handler) linkStore(c *gin.Context, store string) func(string) error {
	if store == "" || h.stores == nil {
		return nil
	}
	return func(name string) error {
		// The prefix is derived, never asked for: each registry gets its own
		// namespace inside the store so several can share one backend without
		// deleting each other's blobs. Existing links keep the prefix they were
		// created with — SetRegistryStore only writes it on the first link.
		return h.stores.SetRegistryStore(c.Request.Context(), name, store, name)
	}
}

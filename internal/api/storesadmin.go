package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"registry/internal/authz"
	"registry/internal/blobstore"
	"registry/internal/vault"
)

// SetStorage wires the credential vault and the blob store repository into the
// admin API. Both are optional: without them the endpoints report 501 rather
// than half-working.
func (h *Handler) SetStorage(v *vault.Vault, stores *blobstore.Repo) {
	h.vault = v
	h.stores = stores
}

// SetAuthz wires the authorization engine. Without it every check denies:
// failing closed is the only safe default for a component whose absence would
// otherwise mean "allow".
func (h *Handler) SetAuthz(e *authz.Engine) { h.authz = e }

func (h *Handler) storageReady(c *gin.Context) bool {
	if h.vault == nil || h.stores == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "credential storage not configured"})
		return false
	}
	return true
}

// --- credentials ---

// adminListSecrets handles GET /api/v1/admin/secrets.
//
// Metadata only. There is deliberately no endpoint that returns a stored
// value: a credential goes in and is referenced, never read back.
func (h *Handler) adminListSecrets(c *gin.Context) {
	if !h.storageReady(c) {
		return
	}
	list, err := h.vault.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"secrets": list})
}

type secretBody struct {
	Key         string         `json:"key"`
	Value       string         `json:"value"`
	Description string         `json:"description"`
	Public      map[string]any `json:"public"`
}

// adminCreateSecret handles POST /api/v1/admin/secrets.
func (h *Handler) adminCreateSecret(c *gin.Context) {
	if !h.storageReady(c) {
		return
	}
	var b secretBody
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if b.Key == "" || b.Value == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key and value are required"})
		return
	}
	ref, err := h.vault.Create(c.Request.Context(), vault.ScopeCredentials, b.Key, b.Value, b.Description, b.Public)
	if errors.Is(err, vault.ErrExists) {
		c.JSON(http.StatusConflict, gin.H{"error": "a credential with this name already exists"})
		return
	}
	if errors.Is(err, vault.ErrNoMasterKey) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "vault master key not configured"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// The reference is what callers store; the value is never echoed back.
	c.JSON(http.StatusCreated, gin.H{"key": b.Key, "ref": ref})
}

// adminUpdateSecret handles PUT /api/v1/admin/secrets/:key — rotation.
// Everything referencing this credential picks up the new value, which is the
// point of naming credentials instead of copying them.
func (h *Handler) adminUpdateSecret(c *gin.Context) {
	if !h.storageReady(c) {
		return
	}
	key := c.Param("key")
	var b secretBody
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if b.Value == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "value is required"})
		return
	}
	if _, err := h.vault.Get(c.Request.Context(), key); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "credential not found"})
		return
	}
	if _, err := h.vault.Save(c.Request.Context(), vault.ScopeCredentials, key, b.Value, b.Description, b.Public); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// adminDeleteSecret handles DELETE /api/v1/admin/secrets/:key.
func (h *Handler) adminDeleteSecret(c *gin.Context) {
	if !h.storageReady(c) {
		return
	}
	key := c.Param("key")
	// Refuse while something still points at it, and say what, rather than
	// breaking a store that will only fail at the next pull.
	users, err := h.stores.StoresUsingSecret(c.Request.Context(), key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(users) > 0 {
		c.JSON(http.StatusConflict, gin.H{
			"error":     "credential is still in use",
			"used_by":   users,
			"remediate": "detach it from these stores first",
		})
		return
	}
	if err := h.vault.Delete(c.Request.Context(), key); err != nil {
		if errors.Is(err, vault.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "credential not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// --- blob stores ---

// adminListBlobStores handles GET /api/v1/admin/blob-stores.
func (h *Handler) adminListBlobStores(c *gin.Context) {
	if !h.storageReady(c) {
		return
	}
	list, err := h.stores.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Report who uses each store so the UI can show it before a delete is tried.
	type view struct {
		*blobstore.Store
		UsedBy []string `json:"used_by"`
	}
	out := make([]view, 0, len(list))
	for _, s := range list {
		users, err := h.stores.RegistriesUsing(c.Request.Context(), s.Name)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		out = append(out, view{Store: s, UsedBy: users})
	}
	c.JSON(http.StatusOK, gin.H{"blob_stores": out, "kinds": blobstore.Kinds})
}

// adminGetBlobStore handles GET /api/v1/admin/blob-stores/:name.
func (h *Handler) adminGetBlobStore(c *gin.Context) {
	if !h.storageReady(c) {
		return
	}
	s, err := h.stores.Get(c.Request.Context(), c.Param("name"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "blob store not found"})
		return
	}
	c.JSON(http.StatusOK, s)
}

// adminUpsertBlobStore handles POST /api/v1/admin/blob-stores and
// PUT /api/v1/admin/blob-stores/:name.
func (h *Handler) adminUpsertBlobStore(c *gin.Context) {
	if !h.storageReady(c) {
		return
	}
	var s blobstore.Store
	if err := c.ShouldBindJSON(&s); err != nil {
		// A plaintext credential in a *_ref field lands here: SecretRef refuses
		// to decode it, so it can never reach the stored definition.
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if n := c.Param("name"); n != "" {
		s.Name = n
	}
	if err := h.stores.Upsert(c.Request.Context(), &s); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, s)
}

// adminDeleteBlobStore handles DELETE /api/v1/admin/blob-stores/:name.
func (h *Handler) adminDeleteBlobStore(c *gin.Context) {
	if !h.storageReady(c) {
		return
	}
	err := h.stores.Delete(c.Request.Context(), c.Param("name"))
	switch {
	case errors.Is(err, blobstore.ErrInUse):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, blobstore.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "blob store not found"})
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	default:
		c.Status(http.StatusNoContent)
	}
}

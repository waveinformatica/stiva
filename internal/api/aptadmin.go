package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"registry/internal/registry"
	"registry/internal/vault"
)

// aptRegistry resolves a hosted APT registry by name, the only kind that
// carries a signing key. Proxied registries pass upstream signatures through;
// re-signing someone else's metadata would lie about its origin.
func (h *Handler) aptRegistry(name string) (*registry.Registry, error) {
	reg, ok := h.mgr.Get(name)
	if !ok {
		return nil, errNotFound("registry not found")
	}
	if registry.Type(reg.Type) != registry.TypeHosted || registry.Format(reg.Format) != registry.FormatAPT {
		return nil, errBadRequest("signing keys are for hosted APT registries")
	}
	return reg, nil
}

type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func errNotFound(msg string) error { return &apiError{status: http.StatusNotFound, msg: msg} }
func errBadRequest(msg string) error {
	return &apiError{status: http.StatusBadRequest, msg: msg}
}

func writeAPIError(c *gin.Context, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		c.JSON(ae.status, gin.H{"error": ae.msg})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

// adminGetAPTKey serves GET …/apt-key: fingerprint and public key, 404 when
// the registry has never generated one.
func (h *Handler) adminGetAPTKey(c *gin.Context) {
	reg, err := h.aptRegistry(c.Param("name"))
	if err != nil {
		writeAPIError(c, err)
		return
	}
	if h.vault == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "vault not configured"})
		return
	}
	sec, err := h.vault.Resolve(c.Request.Context(), vault.ScopeAPTSign, vault.NewRef(aptVaultKey(reg.Name)))
	if err != nil || sec == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "no signing key"})
		return
	}
	entity, err := parseAPTPrivateKey(sec)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "stored signing key is corrupt"})
		return
	}
	pub, err := aptPublicKey(c.Request.Context(), h.vault, reg.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"fingerprint": aptFingerprint(entity), "public_key": pub})
}

// adminCreateAPTKey serves POST …/apt-key: generates the key pair, seals the
// private half in the vault and returns the public half with its fingerprint.
// Refuses when a key already exists — rotation is delete-then-create, explicit.
func (h *Handler) adminCreateAPTKey(c *gin.Context) {
	reg, err := h.aptRegistry(c.Param("name"))
	if err != nil {
		writeAPIError(c, err)
		return
	}
	if h.vault == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "vault not configured (REGISTRY_VAULT_KEY)"})
		return
	}
	if _, err := h.vault.Get(c.Request.Context(), aptVaultKey(reg.Name)); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "signing key already exists — delete it first to rotate"})
		return
	}
	priv, pub, fp, err := generateAPTKey(reg.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if _, err := h.vault.Save(c.Request.Context(), vault.ScopeAPTSign, aptVaultKey(reg.Name), priv,
		"APT signing key for "+reg.Name, map[string]any{"registry": reg.Name}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"fingerprint": fp, "public_key": pub})
}

// adminDeleteAPTKey serves DELETE …/apt-key: drops the key. Already-signed
// metadata already served stays verifiable against distributed copies of the
// public key; new documents go back to unsigned.
func (h *Handler) adminDeleteAPTKey(c *gin.Context) {
	reg, err := h.aptRegistry(c.Param("name"))
	if err != nil {
		writeAPIError(c, err)
		return
	}
	if h.vault == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "vault not configured"})
		return
	}
	if err := h.vault.Delete(c.Request.Context(), aptVaultKey(reg.Name)); err != nil {
		if errors.Is(err, vault.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "no signing key"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

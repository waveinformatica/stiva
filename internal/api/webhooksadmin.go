package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"registry/internal/storage"
	"registry/internal/vault"
	"registry/internal/webhooks"
)

// SetWebhooks wires the webhook dispatcher and its store.
func (h *Handler) SetWebhooks(d *webhooks.Dispatcher, s storage.WebhookStore) {
	h.events = d
	h.whStore = s
}

// emit queues a webhook event for the registry named in the request context.
// It is a no-op when webhooks are not configured, and never fails the request.
func (h *Handler) emit(c *gin.Context, name string, data map[string]any) {
	if h.events == nil {
		return
	}
	actor := ""
	if u := h.currentUser(c); u != nil {
		actor = u.Name
	}
	h.events.Emit(webhooks.Event{Name: name, Registry: c.GetString("registry"), Actor: actor, Data: data})
}

func (h *Handler) webhooksReady(c *gin.Context) bool {
	if h.events == nil || h.whStore == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "webhooks not configured"})
		return false
	}
	return true
}

func pathID(c *gin.Context, key string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(key), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return 0, false
	}
	return id, true
}

// adminListWebhooks serves GET /api/v1/admin/webhooks.
func (h *Handler) adminListWebhooks(c *gin.Context) {
	if !h.webhooksReady(c) {
		return
	}
	list, err := h.whStore.ListWebhooks()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"webhooks": list, "events": webhooks.Catalog})
}

// adminUpsertWebhook serves POST /api/v1/admin/webhooks and PUT …/:id.
func (h *Handler) adminUpsertWebhook(c *gin.Context) {
	if !h.webhooksReady(c) {
		return
	}
	var w storage.Webhook
	if err := c.ShouldBindJSON(&w); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	w.ID = 0
	if c.Param("id") != "" {
		id, ok := pathID(c, "id")
		if !ok {
			return
		}
		w.ID = id
	}
	if err := webhooks.Validate(&w); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// The referenced credential must exist, or every delivery would fail later.
	if w.Secret != "" && h.vault != nil {
		if _, err := h.vault.Get(c.Request.Context(), vault.SecretRef(w.Secret).Key()); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "the secret refers to a credential that does not exist"})
			return
		}
	}
	id, err := h.whStore.UpsertWebhook(w)
	if errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "webhook not found"})
		return
	}
	if err != nil {
		// The name is unique; the likeliest failure is a duplicate.
		c.JSON(http.StatusBadRequest, gin.H{"error": "could not save webhook (is the name already used?): " + err.Error()})
		return
	}
	h.events.Invalidate()
	w.ID = id
	c.JSON(http.StatusOK, w)
}

// adminDeleteWebhook serves DELETE /api/v1/admin/webhooks/:id.
func (h *Handler) adminDeleteWebhook(c *gin.Context) {
	if !h.webhooksReady(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	if err := h.whStore.DeleteWebhook(id); errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "webhook not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.events.Invalidate()
	c.Status(http.StatusNoContent)
}

// adminTestWebhook serves POST /api/v1/admin/webhooks/:id/test: queue a ping.
func (h *Handler) adminTestWebhook(c *gin.Context) {
	if !h.webhooksReady(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	if _, err := h.whStore.GetWebhook(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "webhook not found"})
		return
	}
	actor := ""
	if u := h.currentUser(c); u != nil {
		actor = u.Name
	}
	did, err := h.events.EmitTo(id, webhooks.Event{Name: webhooks.EventPing, Actor: actor,
		Data: map[string]any{"message": "test delivery from Stiva", "at": time.Now().UTC().Format(time.RFC3339)}})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"delivery": did})
}

// adminListDeliveries serves GET /api/v1/admin/webhooks/:id/deliveries.
func (h *Handler) adminListDeliveries(c *gin.Context) {
	if !h.webhooksReady(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	list, err := h.whStore.ListDeliveries(id, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deliveries": list})
}

// adminGetDelivery serves GET /api/v1/admin/webhook-deliveries/:id (with payload).
func (h *Handler) adminGetDelivery(c *gin.Context) {
	if !h.webhooksReady(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	d, err := h.whStore.GetDelivery(id)
	if errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "delivery not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, d)
}

// adminRedeliver serves POST /api/v1/admin/webhook-deliveries/:id/redeliver.
func (h *Handler) adminRedeliver(c *gin.Context) {
	if !h.webhooksReady(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	if err := h.whStore.RetryDelivery(id, time.Now()); errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusConflict, gin.H{"error": "only finished deliveries can be redelivered"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusAccepted)
}

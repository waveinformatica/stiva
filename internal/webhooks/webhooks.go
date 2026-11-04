// Package webhooks delivers registry events to HTTP endpoints.
//
// Events are written to a PostgreSQL-backed queue first and sent afterwards, so
// a restart or an unreachable endpoint loses nothing: each delivery is retried
// with growing delays and its outcome is kept for inspection and redelivery.
// Payloads are JSON, signed with HMAC-SHA256 when the webhook has a secret.
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"registry/internal/storage"
	"registry/internal/vault"
)

// Event names. A webhook subscribes to names or to patterns such as "push.*".
const (
	EventPushManifest     = "push.manifest"
	EventDeleteManifest   = "delete.manifest"
	EventPushArtifact     = "push.artifact"
	EventDeleteArtifact   = "delete.artifact"
	EventJobFinished      = "job.finished"
	EventSignatureDenied  = "security.signature_denied"
	EventChecksumMismatch = "security.checksum_mismatch"
	EventPing             = "ping"
)

// Catalog lists every event a webhook can subscribe to, in display order.
var Catalog = []string{
	EventPushManifest, EventDeleteManifest, EventPushArtifact, EventDeleteArtifact,
	EventJobFinished, EventSignatureDenied, EventChecksumMismatch,
}

// Event is something that happened in a registry.
type Event struct {
	Name     string
	Registry string
	Actor    string
	Data     map[string]any
}

// Delivery parameters.
const (
	maxAttempts    = 6
	requestTimeout = 10 * time.Second
	keepDeliveries = 14 * 24 * time.Hour
	workers        = 4
)

// retryDelays[n] is the wait before attempt n+2 (after attempt n+1 failed).
var retryDelays = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, 6 * time.Hour}

// Matches reports whether w wants ev: it is enabled, ev is in its registry
// scope, and one of its event patterns matches.
func Matches(w storage.Webhook, ev Event) bool {
	if !w.Enabled {
		return false
	}
	if len(w.Registries) > 0 {
		in := false
		for _, r := range w.Registries {
			in = in || r == ev.Registry
		}
		if !in {
			return false
		}
	}
	for _, p := range w.Events {
		if matchPattern(p, ev.Name) {
			return true
		}
	}
	return false
}

func matchPattern(pattern, name string) bool {
	switch {
	case pattern == "*":
		return true
	case strings.HasSuffix(pattern, ".*"):
		return strings.HasPrefix(name, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == name
}

// Validate checks a webhook definition before it is saved.
func Validate(w *storage.Webhook) error {
	if strings.TrimSpace(w.Name) == "" {
		return errors.New("webhook name is required")
	}
	u, err := url.Parse(w.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("webhook url must be an http(s) URL")
	}
	if len(w.Events) == 0 {
		return errors.New("choose at least one event")
	}
	for _, p := range w.Events {
		ok := p == "*"
		if strings.HasSuffix(p, ".*") {
			for _, e := range Catalog {
				ok = ok || strings.HasPrefix(e, strings.TrimSuffix(p, "*"))
			}
		}
		for _, e := range Catalog {
			ok = ok || p == e
		}
		if !ok {
			return fmt.Errorf("unknown event %q", p)
		}
	}
	if w.Secret != "" && !vault.SecretRef(w.Secret).Valid() {
		return errors.New("secret must be a reference to a stored credential")
	}
	return nil
}

// Sign returns the signature header value for body: "sha256=<hex hmac>".
func Sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Dispatcher queues events for matching webhooks and delivers them.
type Dispatcher struct {
	store   storage.WebhookStore
	secret  func(ctx context.Context, ref string) (string, error)
	client  *http.Client
	pending chan Event

	mu       sync.Mutex
	cache    []storage.Webhook
	cachedAt time.Time

	PollEvery time.Duration
	wg        sync.WaitGroup
}

// NewDispatcher builds a dispatcher. secret resolves a webhook's credential
// reference to its value (the vault).
func NewDispatcher(store storage.WebhookStore, secret func(ctx context.Context, ref string) (string, error)) *Dispatcher {
	return &Dispatcher{
		store:   store,
		secret:  secret,
		pending: make(chan Event, 1024),
		client: &http.Client{
			Timeout: requestTimeout,
			// A redirect would send the signed payload somewhere the admin never chose.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		PollEvery: 5 * time.Second,
	}
}

// Invalidate drops the cached webhook list; call it after any change.
func (d *Dispatcher) Invalidate() {
	d.mu.Lock()
	d.cachedAt = time.Time{}
	d.mu.Unlock()
}

func (d *Dispatcher) hooks() []storage.Webhook {
	d.mu.Lock()
	defer d.mu.Unlock()
	if time.Since(d.cachedAt) > 15*time.Second {
		hs, err := d.store.ListWebhooks()
		if err != nil {
			log.Printf("webhooks: list: %v", err)
			return d.cache
		}
		d.cache, d.cachedAt = hs, time.Now()
	}
	return d.cache
}

// Emit hands an event to the dispatcher. It never blocks the caller: when the
// internal buffer is full the event is dropped and logged.
func (d *Dispatcher) Emit(ev Event) {
	select {
	case d.pending <- ev:
	default:
		log.Printf("webhooks: queue full, dropping %s", ev.Name)
	}
}

func (d *Dispatcher) payload(ev Event) []byte {
	b, _ := json.Marshal(map[string]any{
		"id": newID(), "event": ev.Name, "time": time.Now().UTC().Format(time.RFC3339),
		"registry": ev.Registry, "actor": ev.Actor, "data": ev.Data,
	})
	return b
}

// enqueue writes one delivery per matching webhook.
func (d *Dispatcher) enqueue(ev Event) {
	var body []byte
	for _, w := range d.hooks() {
		if !Matches(w, ev) {
			continue
		}
		if body == nil {
			body = d.payload(ev)
		}
		if _, err := d.store.EnqueueDelivery(w.ID, ev.Name, ev.Registry, body, time.Now()); err != nil {
			log.Printf("webhooks: enqueue for %q: %v", w.Name, err)
		}
	}
}

// EmitTo queues an event for one webhook regardless of its filters (test pings).
func (d *Dispatcher) EmitTo(webhookID int64, ev Event) (int64, error) {
	return d.store.EnqueueDelivery(webhookID, ev.Name, ev.Registry, d.payload(ev), time.Now())
}

// Start recovers interrupted deliveries and runs the queue until ctx ends.
func (d *Dispatcher) Start(ctx context.Context) {
	if n, err := d.store.RecoverDeliveries(2 * requestTimeout); err != nil {
		log.Printf("webhooks: recover: %v", err)
	} else if n > 0 {
		log.Printf("webhooks: requeued %d interrupted deliveries", n)
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-d.pending:
				d.enqueue(ev)
			}
		}
	}()
	for i := 0; i < workers; i++ {
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			t := time.NewTicker(d.PollEvery)
			defer t.Stop()
			for {
				d.Drain(ctx)
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		}()
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if _, err := d.store.PruneDeliveries(time.Now().Add(-keepDeliveries)); err != nil {
					log.Printf("webhooks: prune: %v", err)
				}
			}
		}
	}()
}

// Wait blocks until every goroutine has stopped.
func (d *Dispatcher) Wait() { d.wg.Wait() }

// Drain delivers every delivery that is due now.
func (d *Dispatcher) Drain(ctx context.Context) {
	for ctx.Err() == nil {
		del, err := d.store.ClaimDelivery(time.Now())
		if err != nil {
			log.Printf("webhooks: claim: %v", err)
			return
		}
		if del == nil {
			return
		}
		d.deliver(ctx, *del)
	}
}

func (d *Dispatcher) deliver(ctx context.Context, del storage.WebhookDelivery) {
	finish := func(ok bool, code int, msg string) {
		var retry time.Time
		if !ok && del.Attempts < maxAttempts {
			retry = time.Now().Add(retryDelays[min(del.Attempts-1, len(retryDelays)-1)])
		}
		if err := d.store.FinishDelivery(del.ID, ok, code, msg, retry); err != nil {
			log.Printf("webhooks: finish %d: %v", del.ID, err)
		}
	}
	w, err := d.store.GetWebhook(del.WebhookID)
	if err != nil {
		finish(false, 0, "webhook no longer exists")
		return
	}
	if !w.Enabled && del.Event != EventPing {
		finish(false, 0, "webhook disabled")
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(del.Payload))
	if err != nil {
		finish(false, 0, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Stiva-Webhook/1")
	req.Header.Set("X-Stiva-Event", del.Event)
	req.Header.Set("X-Stiva-Delivery", fmt.Sprint(del.ID))
	if w.Secret != "" {
		secret, err := d.secret(ctx, w.Secret)
		if err != nil {
			finish(false, 0, "cannot read secret: "+err.Error())
			return
		}
		req.Header.Set("X-Stiva-Signature", Sign(secret, del.Payload))
	}
	resp, err := d.client.Do(req)
	if err != nil {
		finish(false, 0, err.Error())
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		finish(true, resp.StatusCode, "")
		return
	}
	finish(false, resp.StatusCode, fmt.Sprintf("endpoint answered %d", resp.StatusCode))
}

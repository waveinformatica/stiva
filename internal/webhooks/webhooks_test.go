package webhooks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"registry/internal/storage"
)

// memStore is an in-memory WebhookStore.
type memStore struct {
	mu    sync.Mutex
	hooks []storage.Webhook
	dels  []*storage.WebhookDelivery
}

func (m *memStore) ListWebhooks() ([]storage.Webhook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]storage.Webhook(nil), m.hooks...), nil
}
func (m *memStore) GetWebhook(id int64) (*storage.Webhook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, h := range m.hooks {
		if h.ID == id {
			c := h
			return &c, nil
		}
	}
	return nil, storage.ErrNotFound
}
func (m *memStore) UpsertWebhook(storage.Webhook) (int64, error) { return 0, nil }
func (m *memStore) DeleteWebhook(int64) error                    { return nil }
func (m *memStore) WebhooksUsingSecret(string) ([]string, error) { return nil, nil }
func (m *memStore) EnqueueDelivery(wid int64, ev, reg string, p []byte, at time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := &storage.WebhookDelivery{ID: int64(len(m.dels) + 1), WebhookID: wid, Event: ev, Registry: reg,
		Payload: p, Status: "pending", NextAttemptAt: at.UnixNano()}
	m.dels = append(m.dels, d)
	return d.ID, nil
}
func (m *memStore) ClaimDelivery(now time.Time) (*storage.WebhookDelivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.dels {
		if d.Status == "pending" && d.NextAttemptAt <= now.UnixNano() {
			d.Status = "delivering"
			d.Attempts++
			c := *d
			return &c, nil
		}
	}
	return nil, nil
}
func (m *memStore) FinishDelivery(id int64, ok bool, code int, msg string, retryAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.dels[id-1]
	d.StatusCode, d.Error = code, msg
	switch {
	case ok:
		d.Status = "succeeded"
	case !retryAt.IsZero():
		d.Status, d.NextAttemptAt = "pending", time.Now().UnixNano() // retry immediately in tests
	default:
		d.Status = "failed"
	}
	return nil
}
func (m *memStore) RecoverDeliveries(time.Duration) (int64, error)               { return 0, nil }
func (m *memStore) GetDelivery(int64) (*storage.WebhookDelivery, error)          { return nil, nil }
func (m *memStore) ListDeliveries(int64, int) ([]storage.WebhookDelivery, error) { return nil, nil }
func (m *memStore) RetryDelivery(int64, time.Time) error                         { return nil }
func (m *memStore) PruneDeliveries(time.Time) (int64, error)                     { return 0, nil }

func (m *memStore) status(i int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dels[i].Status
}

func hook(url string) storage.Webhook {
	return storage.Webhook{ID: 1, Name: "ci", URL: url, Enabled: true, Events: []string{"push.*"}}
}

func TestMatches(t *testing.T) {
	ev := Event{Name: EventPushManifest, Registry: "docker"}
	cases := []struct {
		name string
		w    storage.Webhook
		want bool
	}{
		{"exact", storage.Webhook{Enabled: true, Events: []string{"push.manifest"}}, true},
		{"prefix pattern", storage.Webhook{Enabled: true, Events: []string{"push.*"}}, true},
		{"star", storage.Webhook{Enabled: true, Events: []string{"*"}}, true},
		{"other event", storage.Webhook{Enabled: true, Events: []string{"delete.*"}}, false},
		{"disabled", storage.Webhook{Enabled: false, Events: []string{"*"}}, false},
		{"registry in scope", storage.Webhook{Enabled: true, Events: []string{"*"}, Registries: []string{"docker", "maven"}}, true},
		{"registry out of scope", storage.Webhook{Enabled: true, Events: []string{"*"}, Registries: []string{"maven"}}, false},
		{"prefix is not a substring match", storage.Webhook{Enabled: true, Events: []string{"push"}}, false},
	}
	for _, c := range cases {
		if got := Matches(c.w, ev); got != c.want {
			t.Errorf("%s: Matches = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSignatureIsStableAndKeyed(t *testing.T) {
	body := []byte(`{"a":1}`)
	// Value computed independently (Python hmac), so receivers in other languages can check against it.
	if got, want := Sign("secret", body), "sha256=aa9e2e3575f5d7098b6caccd790888c36d5fdb63342a73bada2d6a51747a8494"; got != want {
		t.Errorf("Sign = %s, want %s", got, want)
	}
	if Sign("a", body) == Sign("b", body) {
		t.Error("different secrets must sign differently")
	}
	if Sign("a", body) != Sign("a", body) {
		t.Error("signature must be deterministic")
	}
}

func TestValidate(t *testing.T) {
	ok := storage.Webhook{Name: "ci", URL: "https://ci.example/hook", Events: []string{"push.*", "job.finished"}}
	if err := Validate(&ok); err != nil {
		t.Fatalf("valid webhook rejected: %v", err)
	}
	for name, mutate := range map[string]func(*storage.Webhook){
		"no name":          func(w *storage.Webhook) { w.Name = "" },
		"ftp url":          func(w *storage.Webhook) { w.URL = "ftp://x/y" },
		"no host":          func(w *storage.Webhook) { w.URL = "https://" },
		"no events":        func(w *storage.Webhook) { w.Events = nil },
		"unknown event":    func(w *storage.Webhook) { w.Events = []string{"nope"} },
		"bad pattern":      func(w *storage.Webhook) { w.Events = []string{"zzz.*"} },
		"plaintext secret": func(w *storage.Webhook) { w.Secret = "hunter2" },
	} {
		w := ok
		mutate(&w)
		if err := Validate(&w); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestDeliveryIsSignedAndRecorded(t *testing.T) {
	var gotSig, gotEvent atomic.Value
	var gotBody atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody.Store(b)
		gotSig.Store(r.Header.Get("X-Stiva-Signature"))
		gotEvent.Store(r.Header.Get("X-Stiva-Event"))
		w.WriteHeader(204)
	}))
	defer srv.Close()

	st := &memStore{hooks: []storage.Webhook{hook(srv.URL)}}
	st.hooks[0].Secret = "vault://k"
	d := NewDispatcher(st, func(context.Context, string) (string, error) { return "s3cr3t", nil })
	d.enqueue(Event{Name: EventPushManifest, Registry: "docker", Actor: "ivano",
		Data: map[string]any{"repo": "app", "reference": "v1"}})
	if len(st.dels) != 1 {
		t.Fatalf("%d deliveries queued, want 1", len(st.dels))
	}
	d.Drain(context.Background())

	if st.status(0) != "succeeded" {
		t.Fatalf("status = %s", st.status(0))
	}
	body := gotBody.Load().([]byte)
	if gotSig.Load() != Sign("s3cr3t", body) {
		t.Error("signature header does not match an HMAC of the delivered body")
	}
	if gotEvent.Load() != EventPushManifest {
		t.Errorf("event header = %v", gotEvent.Load())
	}
	var p struct {
		Event, Registry, Actor string
		Data                   map[string]any
	}
	if err := json.Unmarshal(body, &p); err != nil || p.Event != "push.manifest" || p.Registry != "docker" || p.Actor != "ivano" || p.Data["repo"] != "app" {
		t.Errorf("payload = %s (%v)", body, err)
	}
}

func TestOnlyMatchingWebhooksAreQueued(t *testing.T) {
	st := &memStore{hooks: []storage.Webhook{
		{ID: 1, Name: "pushes", URL: "http://x", Enabled: true, Events: []string{"push.*"}},
		{ID: 2, Name: "deletes", URL: "http://x", Enabled: true, Events: []string{"delete.*"}},
	}}
	d := NewDispatcher(st, nil)
	d.enqueue(Event{Name: EventPushManifest, Registry: "r"})
	if len(st.dels) != 1 || st.dels[0].WebhookID != 1 {
		t.Errorf("deliveries = %+v, want exactly one for webhook 1", st.dels)
	}
}

func TestFailedDeliveryIsRetriedThenGivesUp(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	st := &memStore{hooks: []storage.Webhook{hook(srv.URL)}}
	d := NewDispatcher(st, nil)
	d.enqueue(Event{Name: EventPushManifest})
	d.Drain(context.Background())
	if st.status(0) != "succeeded" || calls.Load() != 3 {
		t.Errorf("after flaky endpoint: status=%s calls=%d, want succeeded after 3", st.status(0), calls.Load())
	}

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer dead.Close()
	st2 := &memStore{hooks: []storage.Webhook{hook(dead.URL)}}
	d2 := NewDispatcher(st2, nil)
	d2.enqueue(Event{Name: EventPushManifest})
	d2.Drain(context.Background())
	if st2.status(0) != "failed" || st2.dels[0].Attempts != maxAttempts {
		t.Errorf("dead endpoint: status=%s attempts=%d, want failed after %d", st2.status(0), st2.dels[0].Attempts, maxAttempts)
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var hit atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit.Store(true) }))
	defer target.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redir.Close()
	st := &memStore{hooks: []storage.Webhook{hook(redir.URL)}}
	d := NewDispatcher(st, nil)
	d.enqueue(Event{Name: EventPushManifest})
	d.Drain(context.Background())
	if hit.Load() {
		t.Error("the signed payload followed a redirect to another host")
	}
	if st.status(0) == "succeeded" {
		t.Error("a 302 must not count as delivered")
	}
}

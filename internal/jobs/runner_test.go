package jobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"registry/internal/storage"
)

// memStore is an in-memory JobStore for the runner tests.
type memStore struct {
	mu    sync.Mutex
	jobs  []*storage.Job
	sched []storage.JobSchedule
	next  int64
}

func (m *memStore) EnqueueJob(j storage.Job) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.jobs {
		if x.Registry == j.Registry && x.Kind == j.Kind && (x.Status == "queued" || x.Status == "running") {
			return 0, storage.ErrJobActive
		}
	}
	m.next++
	j.ID, j.Status = m.next, "queued"
	m.jobs = append(m.jobs, &j)
	return j.ID, nil
}
func (m *memStore) ClaimJob() (*storage.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.jobs {
		if x.Status == "queued" {
			x.Status = "running"
			c := *x
			return &c, nil
		}
	}
	return nil, nil
}
func (m *memStore) FinishJob(id int64, status string, _ any, e string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.jobs {
		if x.ID == id {
			x.Status, x.Error = status, e
		}
	}
	return nil
}
func (m *memStore) FailInterrupted(time.Duration) (int64, error) { return 0, nil }
func (m *memStore) GetJob(int64) (*storage.Job, error)           { return nil, nil }
func (m *memStore) ListJobs(string, string, string, int) ([]storage.Job, error) {
	return nil, nil
}
func (m *memStore) PruneJobs(time.Time) (int64, error)                  { return 0, nil }
func (m *memStore) ListSchedules(string) ([]storage.JobSchedule, error) { return m.sched, nil }
func (m *memStore) GetSchedule(string, string) (*storage.JobSchedule, error) {
	return nil, nil
}
func (m *memStore) SetSchedule(storage.JobSchedule) error { return nil }
func (m *memStore) DeleteSchedule(string, string) error   { return nil }
func (m *memStore) DueSchedules(time.Time) ([]storage.JobSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]storage.JobSchedule(nil), m.sched...), nil
}
func (m *memStore) MarkScheduleRun(r, k string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.sched {
		if m.sched[i].Registry == r && m.sched[i].Kind == k {
			m.sched[i].LastRunAt = at.UnixNano()
			m.sched[i].Enabled = false // stop being "due" so the test is one-shot
		}
	}
	return nil
}

func (m *memStore) status(id int64) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.jobs {
		if x.ID == id {
			return x.Status
		}
	}
	return ""
}

func TestRunnerRunsAndRecordsOutcome(t *testing.T) {
	st := &memStore{}
	r := NewRunner(st)
	r.Handle("ok", func(context.Context, storage.Job) (any, error) { return map[string]int{"n": 1}, nil }, nil)
	r.Handle("bad", func(context.Context, storage.Job) (any, error) { return nil, errors.New("boom") }, nil)
	r.Handle("panic", func(context.Context, storage.Job) (any, error) { panic("oops") }, nil)

	a, _ := r.Enqueue(storage.Job{Registry: "r", Kind: "ok"})
	b, _ := r.Enqueue(storage.Job{Registry: "r", Kind: "bad"})
	c, _ := r.Enqueue(storage.Job{Registry: "r", Kind: "panic"})
	r.Tick(context.Background())

	if s := st.status(a); s != "succeeded" {
		t.Errorf("ok job = %s", s)
	}
	if s := st.status(b); s != "failed" {
		t.Errorf("bad job = %s", s)
	}
	if s := st.status(c); s != "failed" {
		t.Errorf("panicking job = %s (a panic must fail the job, not the server)", s)
	}
}

func TestRunnerRefusesDuplicateAndUnknown(t *testing.T) {
	st := &memStore{}
	r := NewRunner(st)
	r.Handle("ok", func(context.Context, storage.Job) (any, error) { return nil, nil }, nil)
	if _, err := r.Enqueue(storage.Job{Registry: "r", Kind: "ok"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Enqueue(storage.Job{Registry: "r", Kind: "ok"}); !errors.Is(err, storage.ErrJobActive) {
		t.Errorf("duplicate = %v, want ErrJobActive", err)
	}
	if _, err := r.Enqueue(storage.Job{Registry: "r", Kind: "nope"}); err == nil {
		t.Error("unknown kind accepted")
	}
}

func TestRunnerSchedulesRespectEligibility(t *testing.T) {
	st := &memStore{sched: []storage.JobSchedule{
		{Registry: "yes", Kind: "ok", Enabled: true, Interval: 1},
		{Registry: "no", Kind: "ok", Enabled: true, Interval: 1},
	}}
	r := NewRunner(st)
	ran := map[string]bool{}
	r.Handle("ok", func(_ context.Context, j storage.Job) (any, error) {
		ran[j.Registry] = true
		return nil, nil
	}, func(reg string) bool { return reg == "yes" })
	r.Tick(context.Background())
	if !ran["yes"] || ran["no"] {
		t.Errorf("ran = %v, want only 'yes'", ran)
	}
}

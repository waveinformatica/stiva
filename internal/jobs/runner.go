// Package jobs runs background tasks (retention cleanup, garbage collection)
// from a database-backed queue, on demand or on a schedule.
//
// The queue lives in PostgreSQL, so several server instances can share it: a
// job is claimed with SKIP LOCKED and a unique index keeps a registry from
// having two active jobs of the same kind.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"registry/internal/storage"
)

// Kinds of job.
const (
	KindCleanup = "cleanup"
	KindGC      = "gc"
)

// Handler runs one job and returns a JSON-serialisable result.
type Handler func(ctx context.Context, j storage.Job) (any, error)

// Runner claims queued jobs and runs them; it also turns due schedules into jobs.
type Runner struct {
	store    storage.JobStore
	handlers map[string]Handler
	// eligible lets a kind veto a scheduled run (e.g. no active policy). Nil
	// or a missing kind means always eligible.
	eligible map[string]func(registry string) bool

	PollEvery   time.Duration // how often to look for work
	StaleAfter  time.Duration // a "running" job older than this on startup is dead
	KeepHistory time.Duration // finished jobs older than this are pruned

	wg sync.WaitGroup
}

// NewRunner builds a runner with sensible defaults.
func NewRunner(store storage.JobStore) *Runner {
	return &Runner{
		store:       store,
		handlers:    map[string]Handler{},
		eligible:    map[string]func(string) bool{},
		PollEvery:   15 * time.Second,
		StaleAfter:  time.Minute,
		KeepHistory: 30 * 24 * time.Hour,
	}
}

// Handle registers the handler for a job kind. eligible may be nil.
func (r *Runner) Handle(kind string, h Handler, eligible func(registry string) bool) {
	r.handlers[kind] = h
	if eligible != nil {
		r.eligible[kind] = eligible
	}
}

// Kinds lists the registered job kinds.
func (r *Runner) Kinds() []string {
	out := make([]string, 0, len(r.handlers))
	for k := range r.handlers {
		out = append(out, k)
	}
	return out
}

// Enqueue queues a job of a registered kind. ErrJobActive means one is already
// queued or running for that registry and kind.
func (r *Runner) Enqueue(j storage.Job) (int64, error) {
	if _, ok := r.handlers[j.Kind]; !ok {
		return 0, fmt.Errorf("unknown job kind %q", j.Kind)
	}
	return r.store.EnqueueJob(j)
}

// Start recovers jobs a previous process left running, then polls until ctx ends.
func (r *Runner) Start(ctx context.Context) {
	if n, err := r.store.FailInterrupted(r.StaleAfter); err != nil {
		log.Printf("jobs: recover interrupted: %v", err)
	} else if n > 0 {
		log.Printf("jobs: marked %d interrupted job(s) as failed", n)
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		t := time.NewTicker(r.PollEvery)
		defer t.Stop()
		for {
			r.Tick(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// Wait blocks until the polling loop has stopped.
func (r *Runner) Wait() { r.wg.Wait() }

// Tick does one round: enqueue due schedules, run every queued job, prune history.
func (r *Runner) Tick(ctx context.Context) {
	r.enqueueDue(ctx, time.Now())
	for ctx.Err() == nil {
		j, err := r.store.ClaimJob()
		if err != nil {
			log.Printf("jobs: claim: %v", err)
			return
		}
		if j == nil {
			break
		}
		r.run(ctx, *j)
	}
	if _, err := r.store.PruneJobs(time.Now().Add(-r.KeepHistory)); err != nil {
		log.Printf("jobs: prune: %v", err)
	}
}

func (r *Runner) enqueueDue(ctx context.Context, now time.Time) {
	due, err := r.store.DueSchedules(now)
	if err != nil {
		log.Printf("jobs: schedules: %v", err)
		return
	}
	for _, s := range due {
		if ctx.Err() != nil {
			return
		}
		if ok := r.eligible[s.Kind]; ok != nil && !ok(s.Registry) {
			// Nothing to do now; look again after the next interval.
			_ = r.store.MarkScheduleRun(s.Registry, s.Kind, now)
			continue
		}
		_, err := r.store.EnqueueJob(storage.Job{
			Registry: s.Registry, Kind: s.Kind, Trigger: "schedule", Params: s.Params,
		})
		if err != nil && !errors.Is(err, storage.ErrJobActive) {
			log.Printf("jobs: enqueue %s/%s: %v", s.Registry, s.Kind, err)
			continue
		}
		_ = r.store.MarkScheduleRun(s.Registry, s.Kind, now)
	}
}

func (r *Runner) run(ctx context.Context, j storage.Job) {
	h, ok := r.handlers[j.Kind]
	if !ok {
		_ = r.store.FinishJob(j.ID, "failed", nil, fmt.Sprintf("no handler for kind %q", j.Kind))
		return
	}
	var (
		res any
		err error
	)
	func() {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("panic: %v", p)
			}
		}()
		res, err = h(ctx, j)
	}()
	if err != nil {
		if ferr := r.store.FinishJob(j.ID, "failed", res, err.Error()); ferr != nil {
			log.Printf("jobs: finish %d: %v", j.ID, ferr)
		}
		return
	}
	if ferr := r.store.FinishJob(j.ID, "succeeded", res, ""); ferr != nil {
		log.Printf("jobs: finish %d: %v", j.ID, ferr)
	}
}

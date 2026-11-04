package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Job is one background task run: queued, picked up by a runner, finished.
type Job struct {
	ID         int64           `json:"id"`
	Registry   string          `json:"registry"`
	Kind       string          `json:"kind"`
	Status     string          `json:"status"`
	DryRun     bool            `json:"dry_run"`
	Trigger    string          `json:"trigger"`
	Params     json.RawMessage `json:"params"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
	CreatedBy  string          `json:"created_by,omitempty"`
	CreatedAt  int64           `json:"created_at"`
	StartedAt  int64           `json:"started_at"`
	FinishedAt int64           `json:"finished_at"`
}

// JobSchedule runs a job kind for a registry every Interval.
type JobSchedule struct {
	Registry  string          `json:"registry"`
	Kind      string          `json:"kind"`
	Enabled   bool            `json:"enabled"`
	Interval  int64           `json:"interval_secs"`
	Params    json.RawMessage `json:"params"`
	LastRunAt int64           `json:"last_run_at"`
}

// ErrJobActive is returned when a job of the same registry and kind is
// already queued or running.
var ErrJobActive = errors.New("a job of this kind is already queued or running for this registry")

// JobStore persists jobs and schedules. PostgresMetadataStore implements it.
type JobStore interface {
	EnqueueJob(j Job) (int64, error)
	// ClaimJob atomically takes the oldest queued job and marks it running.
	// It returns nil when nothing is queued. Safe across several instances.
	ClaimJob() (*Job, error)
	FinishJob(id int64, status string, result any, errMsg string) error
	// FailInterrupted marks jobs left running by a dead instance as failed.
	FailInterrupted(olderThan time.Duration) (int64, error)
	GetJob(id int64) (*Job, error)
	ListJobs(registry, kind, status string, limit int) ([]Job, error)
	// PruneJobs deletes finished jobs older than the cutoff.
	PruneJobs(olderThan time.Time) (int64, error)

	ListSchedules(registry string) ([]JobSchedule, error)
	GetSchedule(registry, kind string) (*JobSchedule, error)
	SetSchedule(s JobSchedule) error
	DeleteSchedule(registry, kind string) error
	// DueSchedules lists enabled schedules whose last run is older than their interval.
	DueSchedules(now time.Time) ([]JobSchedule, error)
	MarkScheduleRun(registry, kind string, at time.Time) error
}

const jobCols = `id, registry, kind, status, dry_run, trigger, params, result, error, created_by, created_at, started_at, finished_at`

func scanJob(row pgx.Row) (*Job, error) {
	var j Job
	var params, result []byte
	if err := row.Scan(&j.ID, &j.Registry, &j.Kind, &j.Status, &j.DryRun, &j.Trigger,
		&params, &result, &j.Error, &j.CreatedBy, &j.CreatedAt, &j.StartedAt, &j.FinishedAt); err != nil {
		return nil, err
	}
	j.Params = params
	j.Result = result
	return &j, nil
}

func (m *PostgresMetadataStore) EnqueueJob(j Job) (int64, error) {
	params := j.Params
	if len(params) == 0 {
		params = []byte("{}")
	}
	if j.Trigger == "" {
		j.Trigger = "manual"
	}
	var id int64
	err := m.pool.QueryRow(context.Background(),
		`INSERT INTO jobs(registry, kind, dry_run, trigger, params, created_by, created_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (registry, kind) WHERE status IN ('queued','running') DO NOTHING
		 RETURNING id`,
		j.Registry, j.Kind, j.DryRun, j.Trigger, params, j.CreatedBy, m.now()).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrJobActive
	}
	return id, err
}

func (m *PostgresMetadataStore) ClaimJob() (*Job, error) {
	row := m.pool.QueryRow(context.Background(),
		`UPDATE jobs SET status = 'running', started_at = $1
		 WHERE id = (SELECT id FROM jobs WHERE status = 'queued'
		             ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1)
		 RETURNING `+jobCols, m.now())
	j, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return j, err
}

func (m *PostgresMetadataStore) FinishJob(id int64, status string, result any, errMsg string) error {
	var raw []byte
	if result != nil {
		b, err := json.Marshal(result)
		if err != nil {
			return err
		}
		raw = b
	}
	_, err := m.pool.Exec(context.Background(),
		`UPDATE jobs SET status = $2, result = $3, error = $4, finished_at = $5 WHERE id = $1`,
		id, status, raw, errMsg, m.now())
	return err
}

func (m *PostgresMetadataStore) FailInterrupted(olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan).UnixNano()
	tag, err := m.pool.Exec(context.Background(),
		`UPDATE jobs SET status = 'failed', error = 'interrupted (server restarted)', finished_at = $2
		 WHERE status = 'running' AND started_at < $1`, cutoff, m.now())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (m *PostgresMetadataStore) GetJob(id int64) (*Job, error) {
	j, err := scanJob(m.pool.QueryRow(context.Background(),
		`SELECT `+jobCols+` FROM jobs WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return j, err
}

func (m *PostgresMetadataStore) ListJobs(registry, kind, status string, limit int) ([]Job, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := m.pool.Query(context.Background(),
		`SELECT `+jobCols+` FROM jobs
		 WHERE ($1 = '' OR registry = $1) AND ($2 = '' OR kind = $2) AND ($3 = '' OR status = $3)
		 ORDER BY id DESC LIMIT $4`, registry, kind, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		// The list view never needs the (possibly large) result body.
		j.Result = nil
		out = append(out, *j)
	}
	return out, rows.Err()
}

func (m *PostgresMetadataStore) PruneJobs(olderThan time.Time) (int64, error) {
	tag, err := m.pool.Exec(context.Background(),
		`DELETE FROM jobs WHERE status IN ('succeeded','failed') AND finished_at < $1`, olderThan.UnixNano())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func scanSchedule(row pgx.Row) (*JobSchedule, error) {
	var s JobSchedule
	var params []byte
	if err := row.Scan(&s.Registry, &s.Kind, &s.Enabled, &s.Interval, &params, &s.LastRunAt); err != nil {
		return nil, err
	}
	s.Params = params
	return &s, nil
}

const schedCols = `registry, kind, enabled, interval_secs, params, last_run_at`

func (m *PostgresMetadataStore) ListSchedules(registry string) ([]JobSchedule, error) {
	rows, err := m.pool.Query(context.Background(),
		`SELECT `+schedCols+` FROM job_schedules WHERE ($1 = '' OR registry = $1) ORDER BY registry, kind`, registry)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JobSchedule{}
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func (m *PostgresMetadataStore) GetSchedule(registry, kind string) (*JobSchedule, error) {
	s, err := scanSchedule(m.pool.QueryRow(context.Background(),
		`SELECT `+schedCols+` FROM job_schedules WHERE registry = $1 AND kind = $2`, registry, kind))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return s, err
}

func (m *PostgresMetadataStore) SetSchedule(s JobSchedule) error {
	params := s.Params
	if len(params) == 0 {
		params = []byte("{}")
	}
	_, err := m.pool.Exec(context.Background(),
		`INSERT INTO job_schedules(registry, kind, enabled, interval_secs, params, last_run_at)
		 VALUES($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (registry, kind) DO UPDATE SET
		   enabled = EXCLUDED.enabled, interval_secs = EXCLUDED.interval_secs, params = EXCLUDED.params`,
		s.Registry, s.Kind, s.Enabled, s.Interval, params, m.now())
	return err
}

func (m *PostgresMetadataStore) DeleteSchedule(registry, kind string) error {
	_, err := m.pool.Exec(context.Background(),
		`DELETE FROM job_schedules WHERE registry = $1 AND kind = $2`, registry, kind)
	return err
}

func (m *PostgresMetadataStore) DueSchedules(now time.Time) ([]JobSchedule, error) {
	rows, err := m.pool.Query(context.Background(),
		`SELECT `+schedCols+` FROM job_schedules
		 WHERE enabled AND last_run_at + interval_secs * 1000000000 <= $1`, now.UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JobSchedule{}
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func (m *PostgresMetadataStore) MarkScheduleRun(registry, kind string, at time.Time) error {
	_, err := m.pool.Exec(context.Background(),
		`UPDATE job_schedules SET last_run_at = $3 WHERE registry = $1 AND kind = $2`,
		registry, kind, at.UnixNano())
	return err
}

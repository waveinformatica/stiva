-- Background jobs (cleanup, garbage collection) and their schedules.
-- Timestamps are UnixNano like every created_at in this store; 0 = unset.
CREATE TABLE IF NOT EXISTS jobs (
	id          BIGSERIAL PRIMARY KEY,
	registry    TEXT    NOT NULL,
	kind        TEXT    NOT NULL,
	status      TEXT    NOT NULL DEFAULT 'queued', -- queued | running | succeeded | failed
	dry_run     BOOLEAN NOT NULL DEFAULT FALSE,
	trigger     TEXT    NOT NULL DEFAULT 'manual', -- manual | schedule
	params      JSONB   NOT NULL DEFAULT '{}',
	result      JSONB,
	error       TEXT    NOT NULL DEFAULT '',
	created_by  TEXT    NOT NULL DEFAULT '',
	created_at  BIGINT  NOT NULL,
	started_at  BIGINT  NOT NULL DEFAULT 0,
	finished_at BIGINT  NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_jobs_created ON jobs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
-- At most one active job per registry and kind: overlapping runs of the same
-- task are refused at the database, whichever instance tries to start them.
CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_active ON jobs(registry, kind)
	WHERE status IN ('queued', 'running');

CREATE TABLE IF NOT EXISTS job_schedules (
	registry      TEXT    NOT NULL,
	kind          TEXT    NOT NULL,
	enabled       BOOLEAN NOT NULL DEFAULT TRUE,
	interval_secs BIGINT  NOT NULL,
	params        JSONB   NOT NULL DEFAULT '{}',
	last_run_at   BIGINT  NOT NULL DEFAULT 0,
	PRIMARY KEY (registry, kind)
);

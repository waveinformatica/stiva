-- Outgoing webhooks and their delivery log. Timestamps are UnixNano; 0 = unset.
CREATE TABLE IF NOT EXISTS webhooks (
	id          BIGSERIAL PRIMARY KEY,
	name        TEXT    NOT NULL UNIQUE,
	url         TEXT    NOT NULL,
	secret      TEXT    NOT NULL DEFAULT '', -- vault reference, never the secret itself
	events      TEXT[]  NOT NULL DEFAULT '{}',
	registries  TEXT[]  NOT NULL DEFAULT '{}', -- empty = every registry
	enabled     BOOLEAN NOT NULL DEFAULT TRUE,
	description TEXT    NOT NULL DEFAULT '',
	created_at  BIGINT  NOT NULL
);

CREATE TABLE IF NOT EXISTS webhook_deliveries (
	id              BIGSERIAL PRIMARY KEY,
	webhook_id      BIGINT NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
	event           TEXT   NOT NULL,
	registry        TEXT   NOT NULL DEFAULT '',
	payload         JSONB  NOT NULL,
	status          TEXT   NOT NULL DEFAULT 'pending', -- pending | delivering | succeeded | failed
	attempts        INT    NOT NULL DEFAULT 0,
	next_attempt_at BIGINT NOT NULL,
	status_code     INT    NOT NULL DEFAULT 0,
	error           TEXT   NOT NULL DEFAULT '',
	created_at      BIGINT NOT NULL,
	delivered_at    BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_wd_due ON webhook_deliveries(next_attempt_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_wd_hook ON webhook_deliveries(webhook_id, id DESC);

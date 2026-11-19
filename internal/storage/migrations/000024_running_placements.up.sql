-- Where images run: each pre-warm agent reports, every round, the containers of
-- its node and the digest each one runs. A node's report replaces its previous
-- one; rows not renewed for a while (a node gone) are ignored by the queries.
CREATE TABLE IF NOT EXISTS running_placements (
	node      TEXT   NOT NULL,
	namespace TEXT   NOT NULL,
	pod       TEXT   NOT NULL,
	container TEXT   NOT NULL,
	image     TEXT   NOT NULL,
	digest    TEXT   NOT NULL,
	-- The node's os/architecture: which entry of a multi-platform index runs.
	platform  TEXT   NOT NULL,
	seen_at   BIGINT NOT NULL,
	PRIMARY KEY (node, namespace, pod, container)
);
CREATE INDEX IF NOT EXISTS idx_running_placements_digest ON running_placements(digest);

-- Images the analyser could not scan (a file it cannot read, a blob the
-- registry does not hold). Recorded so a failure is visible and is retried
-- once a day rather than at every run; a successful full scan removes it, and
-- so does the manifest going away.
CREATE TABLE IF NOT EXISTS image_scan_failures (
	registry  TEXT   NOT NULL,
	repo      TEXT   NOT NULL,
	digest    TEXT   NOT NULL,
	analyzer  TEXT   NOT NULL,
	error     TEXT   NOT NULL,
	failed_at BIGINT NOT NULL,
	PRIMARY KEY (registry, repo, digest),
	FOREIGN KEY (registry, repo, digest) REFERENCES manifests(registry, repo, digest) ON DELETE CASCADE
);

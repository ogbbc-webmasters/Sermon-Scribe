CREATE TABLE jobs (
    id           TEXT PRIMARY KEY,
    sermon_id    TEXT NOT NULL REFERENCES sermons(id) ON DELETE CASCADE,
    type         TEXT NOT NULL,
    stage        TEXT NOT NULL,
    state        TEXT NOT NULL CHECK (state IN ('queued', 'running', 'done', 'failed')),
    attempts     INTEGER NOT NULL DEFAULT 0,
    progress     INTEGER NOT NULL DEFAULT 0 CHECK (progress BETWEEN -1 AND 100),
    checkpoint   TEXT,
    parameters   TEXT NOT NULL DEFAULT '{}',
    last_error   TEXT,
    available_at TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
) STRICT;

CREATE INDEX jobs_ready_idx ON jobs (state, available_at, created_at);
CREATE INDEX jobs_sermon_idx ON jobs (sermon_id, created_at DESC);

CREATE TABLE job_errors (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id     TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    attempt    INTEGER NOT NULL,
    error      TEXT NOT NULL,
    created_at TEXT NOT NULL
) STRICT;

CREATE INDEX job_errors_job_idx ON job_errors (job_id, created_at);

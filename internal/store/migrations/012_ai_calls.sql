CREATE TABLE ai_calls (
    id INTEGER PRIMARY KEY,
    sermon_id TEXT NOT NULL REFERENCES sermons(id) ON DELETE CASCADE,
    job_id TEXT NOT NULL,
    task TEXT NOT NULL,
    model TEXT NOT NULL,
    request_id TEXT NOT NULL,
    cost_usd REAL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX ai_calls_sermon ON ai_calls(sermon_id);

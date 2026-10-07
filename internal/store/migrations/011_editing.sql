CREATE TABLE editing (
 sermon_id TEXT PRIMARY KEY REFERENCES sermons(id) ON DELETE CASCADE,
 draft TEXT NOT NULL,
 revision INTEGER NOT NULL,
 source_text TEXT NOT NULL,
 source_metadata TEXT NOT NULL,
 playback TEXT NOT NULL DEFAULT '',
 active INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE source_transcriptions (
 sermon_id TEXT PRIMARY KEY REFERENCES sermons(id) ON DELETE CASCADE,
 text TEXT NOT NULL,
 metadata TEXT NOT NULL
);

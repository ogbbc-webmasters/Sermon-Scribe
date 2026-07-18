CREATE TABLE sermons (
    id                TEXT PRIMARY KEY,
    original_filename TEXT NOT NULL,
    uploaded_at       TEXT NOT NULL,
    uploaded_by       TEXT,
    stage             TEXT NOT NULL,
    status            TEXT NOT NULL
) STRICT;

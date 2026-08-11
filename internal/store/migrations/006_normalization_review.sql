ALTER TABLE sermons
ADD COLUMN normalization_reviewed INTEGER NOT NULL DEFAULT 0
CHECK (normalization_reviewed IN (0, 1));

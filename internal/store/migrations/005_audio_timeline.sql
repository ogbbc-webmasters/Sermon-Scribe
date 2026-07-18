ALTER TABLE sermons ADD COLUMN applied_regions TEXT;
ALTER TABLE sermons ADD COLUMN edit_approved INTEGER NOT NULL DEFAULT 0 CHECK (edit_approved IN (0, 1));

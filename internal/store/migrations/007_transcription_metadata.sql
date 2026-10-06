ALTER TABLE sermons ADD COLUMN transcript TEXT;
ALTER TABLE sermons ADD COLUMN title TEXT;
ALTER TABLE sermons ADD COLUMN title_generated INTEGER CHECK (title_generated IN (0, 1));
ALTER TABLE sermons ADD COLUMN title_reasoning TEXT;
ALTER TABLE sermons ADD COLUMN speaker TEXT;
ALTER TABLE sermons ADD COLUMN scriptures TEXT;
ALTER TABLE sermons ADD COLUMN topics TEXT;
ALTER TABLE sermons ADD COLUMN topics_reasoning TEXT;
ALTER TABLE sermons ADD COLUMN topic_scores TEXT;

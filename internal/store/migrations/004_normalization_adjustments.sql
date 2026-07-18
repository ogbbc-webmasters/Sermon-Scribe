ALTER TABLE sermons
ADD COLUMN normalization_gate_adjustment INTEGER NOT NULL DEFAULT 0;

ALTER TABLE sermons
ADD COLUMN normalization_volume_adjustment INTEGER NOT NULL DEFAULT 0;

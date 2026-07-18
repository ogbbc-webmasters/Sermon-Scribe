INSERT INTO jobs (
    id, sermon_id, type, stage, state, attempts, progress,
    parameters, available_at, created_at, updated_at
)
SELECT
    'normalize-' || s.id,
    s.id,
    'normalize',
    'normalization',
    'queued',
    0,
    0,
    '{}',
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
FROM sermons s
WHERE s.stage = 'upload'
  AND s.status = 'done'
  AND NOT EXISTS (
      SELECT 1 FROM jobs j WHERE j.sermon_id = s.id
  );

UPDATE sermons
SET stage = 'normalization', status = 'pending'
WHERE stage = 'upload'
  AND status = 'done'
  AND EXISTS (
      SELECT 1 FROM jobs j
      WHERE j.sermon_id = sermons.id
        AND j.type = 'normalize'
        AND j.stage = 'normalization'
  );

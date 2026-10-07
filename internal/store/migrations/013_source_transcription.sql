-- Source transcripts are canonical; retain legacy columns without reading them
-- when a full source is available.
INSERT INTO source_transcriptions(sermon_id,text,metadata)
SELECT sermon_id,source_text,source_metadata FROM editing WHERE true
ON CONFLICT(sermon_id) DO NOTHING;
INSERT INTO source_transcriptions(sermon_id,text,metadata)
SELECT id,transcript,COALESCE(transcription_metadata,'{}') FROM sermons WHERE transcript IS NOT NULL
ON CONFLICT(sermon_id) DO NOTHING;

ALTER TABLE editing ADD COLUMN applied_draft TEXT;
-- The saved draft may have changed since the last render. Recover the exact
-- rendered snapshot from the job that owns the playback file when possible.
UPDATE editing SET applied_draft=COALESCE(
 (SELECT json_extract(j.parameters,'$.draft') FROM jobs j
  WHERE j.sermon_id=editing.sermon_id AND j.type='render_edit'
    AND editing.playback='edit-' || j.id || '/edited.mp3'),
 draft
) WHERE active;

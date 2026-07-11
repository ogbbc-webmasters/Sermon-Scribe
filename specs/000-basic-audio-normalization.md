---
status: draft
author: Addison Emig
creation_date: 2026-01-17
---

# Basic Audio Normalization

## Problem

Currently, sermon audio must be manually edited in Audacity before upload:

1. Import raw audio
2. Convert to mono
3. Resample to 44100 Hz
4. Apply noise gate and normalization
5. Cut out: intro, opening/closing, singing, extended silence, mic noises
6. Export as MP3 (mono, 32kbps)

This is tedious and requires specialized software. The goal is to eliminate Audacity entirely by handling the full workflow in the web app: **raw audio → edited audio → transcription → metadata**.

## Proposed Solution

Add server-side audio normalization with a manual editing step:

1. Upload raw audio → creates sermon
2. Server normalizes (mono, resample, noise gate, normalize) → `normalized.mp3`
3. Job pauses at `awaiting_edit`
4. User downloads `normalized.mp3`
5. User edits in Audacity (cuts singing, silence, etc.)
6. User uploads edited file back to same sermon → `final.mp3`
7. Job resumes → transcription → metadata

[Spec 001](001-automatic-audio-timeline.md) will later replace steps 4-6 with an in-browser timeline UI.

## Normalization Pipeline

```bash
ffmpeg -i original.wav \
  -ac 1 \
  -ar 44100 \
  -af "agate=threshold=TBD,loudnorm" \
  -b:a 128k \
  normalized.mp3
```

Converts to mono, resamples to 44100 Hz, applies noise gate and loudness normalization. Output is 128kbps for editing quality; `final.mp3` will be 32kbps.

**Supported input formats**: Any format FFmpeg can decode.

**Noise gate threshold**: TBD - needs testing with actual sermon recordings to find optimal value.

## File Structure

```
uploads/{sermon_id}/
  original.*          # Raw upload (any format: wav, mp3, m4a, etc.)
  normalized.mp3      # After normalization (128kbps, for editing)
  final.mp3           # After user edits (32kbps, for transcription)
  chunks/chunk_N.mp3  # For transcription (from final.mp3)
```

## API Changes

### New Endpoints

- `GET /api/sermons/{id}/audio/{type}` - Download audio file where `type` is `original`, `normalized`, or `final`
- `POST /api/sermons/{id}/upload-edited` - Upload edited file as `final.mp3`, resumes job

### Job Status Changes

New statuses replace the generic `processing` status:

- `normalizing` - FFmpeg normalization in progress
- `awaiting_edit` - Paused, waiting for user to upload edited file
- `transcribing` - Sending audio to OpenAI for transcription
- `extracting` - Extracting metadata from transcript

Full flow: `pending` → `normalizing` → `awaiting_edit` → `transcribing` → `extracting` → `complete`

## UI Changes

When job status is `awaiting_edit`, show:

1. Download button for `normalized.mp3`
2. Upload form for edited file
3. Instructions: "Download, edit in Audacity, then upload your edited file"

## Backward Compatibility

- **Completed sermons**: Unaffected
- **In-progress jobs**: On startup, `ResetStaleJobs()` already resets `processing` jobs to `pending`. These will fall back to `original.*` for transcription since they have no `final.mp3`.

## Authentication

New endpoints use the existing exe.dev proxy auth (`X-Exedev-Userid` header), same as other protected endpoints.

## Task List

### Add New Job Statuses

- [ ] Add `normalizing` status - FFmpeg processing in progress
- [ ] Add `awaiting_edit` status - Paused for user input
- [ ] Add `transcribing` status - OpenAI transcription in progress  
- [ ] Add `extracting` status - Metadata extraction in progress
- [ ] Update worker to use specific statuses instead of generic `processing`
- [ ] Update frontend to display new status names

### Add Normalization to Upload Pipeline

- [ ] After saving `original.*`, run FFmpeg normalization pipeline
- [ ] Save output as `normalized.mp3` (mono, 44100Hz, noise gate, normalize, 128kbps)
- [ ] Update job status to `normalizing` during this step
- [ ] Update job progress during normalization

### Add `awaiting_edit` Job Stage

- [ ] Worker pauses job after normalization completes
- [ ] Set job status to `awaiting_edit`
- [ ] Update checkpointing to handle new stage
- [ ] Job stays in `awaiting_edit` until user uploads edited file

### Add Download Endpoint

- [ ] Create `GET /api/sermons/{id}/audio/{type}` endpoint
- [ ] Support `type` values: `original`, `normalized`, `final`
- [ ] Return appropriate file as download
- [ ] Require auth (same as other protected endpoints)
- [ ] Return 404 if file doesn't exist

### Add Upload-Edited Endpoint

- [ ] Create `POST /api/sermons/{id}/upload-edited` endpoint
- [ ] Accept audio file upload
- [ ] Validate job is in `awaiting_edit` status
- [ ] Save as `final.mp3` (transcode to 32kbps mono if needed)
- [ ] Set job status to `pending` to resume processing
- [ ] Worker picks up job and continues to transcription

### Update Frontend for Edit Workflow

- [ ] Detect `awaiting_edit` status in sermon detail view
- [ ] Show download button for normalized audio
- [ ] Show upload form for edited file
- [ ] Show brief instructions for the workflow
- [ ] After upload, show transcription progress as before

### Update Transcription to Use Final Audio

- [ ] Modify worker to use `final.mp3` for transcription
- [ ] Fall back to `original.*` if `final.mp3` doesn't exist (backward compatibility with old sermons)

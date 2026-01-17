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

## Implementation Phases

### Phase 1: Manual Editing Workflow (this spec)

Add server-side audio normalization with a manual editing step:

1. Upload raw audio → creates sermon
2. Server normalizes (mono, resample, noise gate, normalize) → `normalized.mp3`
3. Job pauses at `awaiting_edit`
4. User downloads `normalized.mp3`
5. User edits in Audacity (cuts singing, silence, etc.)
6. User uploads edited file back to same sermon → `final.mp3`
7. Job resumes → transcription → metadata

This validates the `awaiting_edit` job stage and file structure before building the complex UI.

### Phase 2: In-Browser Editing

See [Spec 001](001-automatic-audio-timeline.md) - replaces steps 4-6 with a timeline UI.

## Normalization Pipeline

FFmpeg processes the uploaded audio:

1. **Convert to mono** - Mix stereo down to single channel
2. **Resample to 44100 Hz** - Standard sample rate
3. **Noise gate** - Remove low-level background noise
4. **Normalize volume** - Consistent loudness

```bash
ffmpeg -i original.wav \
  -ac 1 \
  -ar 44100 \
  -af "agate=threshold=-30dB,loudnorm" \
  -b:a 128k \
  normalized.mp3
```

Output is 128kbps for good editing quality. Final export after user edits will be 32kbps.

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

- `GET /api/sermons/{id}/download` - Download `normalized.mp3` for editing
- `POST /api/sermons/{id}/upload-edited` - Upload edited file as `final.mp3`, resumes job

### Job Status Changes

Current statuses: `pending`, `processing`, `complete`, `error`

New statuses:
- `normalizing` - FFmpeg normalization in progress
- `awaiting_edit` - Paused, waiting for user to upload edited file
- `transcribing` - Sending audio to OpenAI for transcription
- `extracting` - Extracting metadata from transcript

Full flow: `pending` → `normalizing` → `awaiting_edit` → `transcribing` → `extracting` → `complete`

The generic `processing` status is replaced with specific stages.

## UI Changes

When job status is `awaiting_edit`, show:

1. Download button for `normalized.mp3`
2. Upload form for edited file
3. Instructions: "Download, edit in Audacity, then upload your edited file"

## Task List

### Add Normalization to Upload Pipeline

- [ ] After saving `original.*`, run FFmpeg normalization pipeline
- [ ] Save output as `normalized.mp3` (mono, 44100Hz, noise gate, normalize, 128kbps)
- [ ] Add `normalizing` job status
- [ ] Update job progress during normalization

### Add `awaiting_edit` Job Stage

- [ ] Add `awaiting_edit` job status
- [ ] Worker pauses job after normalization completes
- [ ] Update checkpointing to handle new stage
- [ ] Job stays in `awaiting_edit` until user uploads edited file

### Add Download Endpoint

- [ ] Create `GET /api/sermons/{id}/download` endpoint
- [ ] Return `normalized.mp3` as file download
- [ ] Require auth (same as other protected endpoints)
- [ ] Return 404 if file doesn't exist or job not in `awaiting_edit`

### Add Upload-Edited Endpoint

- [ ] Create `POST /api/sermons/{id}/upload-edited` endpoint
- [ ] Accept audio file upload
- [ ] Validate job is in `awaiting_edit` status
- [ ] Save as `final.mp3` (transcode to 32kbps mono if needed)
- [ ] Update job status to resume processing
- [ ] Worker picks up job and continues to transcription

### Update Frontend for Edit Workflow

- [ ] Detect `awaiting_edit` status in sermon detail view
- [ ] Show download button for normalized audio
- [ ] Show upload form for edited file
- [ ] Show brief instructions for the workflow
- [ ] After upload, show transcription progress as before

### Update Transcription to Use Final Audio

- [ ] Modify worker to use `final.mp3` for transcription (instead of `original.*`)
- [ ] Fall back to `original.*` if `final.mp3` doesn't exist (backward compatibility)

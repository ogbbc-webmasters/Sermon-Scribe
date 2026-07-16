---
status: draft
author: Addison Emig
creation_date: 2026-01-17
---

# Audio Normalization

Server-side audio normalization with a manual editing step, for the [Basic Webapp](specs/000-basic-webapp/SPEC.md). Raw sermon recordings need cleanup before transcription and publishing: mono conversion, resampling, noise gating, loudness normalization, and removal of unwanted sections (intro, singing, extended silence). This spec covers the automated normalization and the manual edit round-trip; [Automatic Audio Timeline](specs/001-automatic-audio-timeline/SPEC.md) later replaces the manual edit with an in-browser timeline editor.

## Workflow

1. Upload raw audio → creates sermon (see [Project Setup](specs/000-basic-webapp/000-project-setup/SPEC.md))
2. `normalize` job converts and normalizes → `normalized.mp3`
3. Sermon reaches `normalized`
4. User downloads `normalized.mp3`, edits in Audacity (cuts singing, silence, etc.)
5. User uploads edited file back to the same sermon → `final.mp3`
6. Pipeline resumes → transcription → metadata (see [Processing Pipeline](specs/000-basic-webapp/001-processing-pipeline/SPEC.md))

## Design Decisions

### Normalization Pipeline

```bash
ffmpeg -i original.wav \
  -ac 1 \
  -ar 44100 \
  -af "agate=threshold=TBD,loudnorm" \
  -b:a 128k \
  normalized.mp3
```

Converts to mono, resamples to 44100 Hz, applies noise gate then loudness normalization. Output is 128kbps for editing quality; `final.mp3` is 32kbps for transcription and publishing.

- **Supported input formats**: any format FFmpeg can decode
- **Noise gate threshold**: TBD - needs testing with actual sermon recordings
- **loudnorm**: single-pass; sufficient for speech and avoids a second FFmpeg run

### File Structure

```text
uploads/{sermon_id}/
  original.*          # Raw upload (any format: wav, mp3, m4a, etc.)
  normalized.mp3      # After normalization (128kbps, for editing)
  final.mp3           # After user edits (32kbps, for transcription)
  chunks/chunk_N.mp3  # For transcription (from final.mp3)
```

## API

- `GET /api/sermons/{id}/audio/{type}` - Download audio file where `type` is `original`, `normalized`, or `final`
- `POST /api/sermons/{id}/upload-edited` - Upload edited file; server re-encodes to `final.mp3` (mono, 32kbps); repeatable to replace a previous upload

Both require exe.dev proxy auth, as defined in [Project Setup](specs/000-basic-webapp/000-project-setup/SPEC.md).

## UI

When sermon stage is `normalized`, show:

1. Download button for `normalized.mp3`
2. Upload form for the edited file
3. Instructions: "Download, edit in Audacity, then upload your edited file"

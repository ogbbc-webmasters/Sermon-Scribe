---
status: draft
author: Shelley
creation_date: 2026-01-17
---

# Audio Editor

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

Add an audio editing stage between upload and transcription with:

1. **Automatic audio normalization** - Convert to mono, resample, apply noise gate and normalization using FFmpeg
2. **Automatic region detection** - Analyze the waveform to detect three region types:
   - `silence` - Extended periods of low amplitude
   - `speaking` - Normal speech patterns
   - `singing` - Sustained notes, regular amplitude (easily distinguishable from speech)
3. **Timeline UI** - Visual waveform display with color-coded regions where users can:
   - Adjust region boundaries (drag edges)
   - Mark regions as keep/delete
   - Preview audio playback synced to timeline
4. **Apply edits** - Generate final edited audio, then proceed to existing transcription pipeline

## File Structure

```
uploads/{sermon_id}/
  original.*          # Raw upload (any format: wav, mp3, m4a, etc.)
  normalized.mp3      # Transcoded for browser playback (64-128kbps)
  final.mp3           # After user edits applied (32kbps)
  chunks/chunk_N.mp3  # For transcription (from final.mp3)
```

## API Additions

- `POST /api/sermons/{id}/analyze` - Runs FFmpeg analysis, returns detected regions
- `GET /api/sermons/{id}/waveform` - Returns pre-rendered waveform data (JSON amplitude samples or SVG)
- `POST /api/sermons/{id}/edit` - Accepts final region boundaries, applies cuts, produces edited audio
- `GET /uploads/{id}/normalized.mp3` - Serve normalized audio for browser playback

## Region Data Structure

```json
{
  "duration": 5400.0,
  "regions": [
    {"start": 0, "end": 45.2, "type": "silence"},
    {"start": 45.2, "end": 120.5, "type": "speaking"},
    {"start": 120.5, "end": 185.0, "type": "singing"},
    {"start": 185.0, "end": 187.5, "type": "silence"},
    {"start": 187.5, "end": 2400.0, "type": "speaking"}
  ]
}
```

Regions are contiguous with no gaps or overlaps - the entire file is covered.

## Timeline UI

- Waveform display rendered from server-generated data
- Color-coded regions: speaking=green, singing=red, silence=gray
- Each region has a keep/delete toggle
- Dragging a boundary adjusts both adjacent regions
- HTML5 `<audio>` element for playback, synced to timeline position
- "Apply & Transcribe" button to proceed

## Job Stages

The job pipeline becomes:

1. `pending` → `analyzing` → `awaiting_edit` (pauses for user input)
2. User reviews regions, adjusts as needed, submits edits
3. `editing` → `transcribing` → `extracting` → `complete`

## Design Decisions

### Waveform rendering: server-side vs client-side

**Chosen: Server-side**

- Server already has FFmpeg
- Better performance (don't send full audio to browser just for visualization)
- Generate amplitude data or SVG on the server

### Audio preview: full file streaming vs snippets

**Chosen: Full file streaming**

- Simpler implementation: serve `normalized.mp3` directly
- Use HTML5 `<audio>` element with `currentTime` seeking
- No extra server logic for extracting snippets

### Region detection approach

**Chosen: Server-side FFmpeg + custom analysis**

- Silence detection via FFmpeg `silencedetect` filter
- Singing detection via waveform characteristics (sustained energy, less amplitude variance, pitch stability)
- All processing happens server-side

### Default region actions

- `speaking` → keep
- `singing` → delete
- `silence` → delete (or auto-trim to ~1 second gap)

## Task List

### Backend: Audio Normalization

- [ ] On upload, save original file with original extension
- [ ] Add FFmpeg pipeline to normalize audio (mono, resample 44100Hz, noise gate, normalize)
- [ ] Save normalized output as `normalized.mp3` (64-128kbps)
- [ ] Serve normalized file at `/uploads/{id}/normalized.mp3`

### Backend: Region Detection

- [ ] Implement silence detection using FFmpeg `silencedetect`
- [ ] Implement singing detection via waveform analysis
- [ ] Create `POST /api/sermons/{id}/analyze` endpoint
- [ ] Return regions as JSON with start, end, type

### Backend: Waveform Generation

- [ ] Generate waveform amplitude data from normalized audio
- [ ] Create `GET /api/sermons/{id}/waveform` endpoint
- [ ] Return data suitable for frontend rendering (JSON array or SVG)

### Backend: Edit Application

- [ ] Create `POST /api/sermons/{id}/edit` endpoint
- [ ] Accept region boundaries with keep/delete flags
- [ ] Apply cuts using FFmpeg
- [ ] Save result as `final.mp3` (32kbps)
- [ ] Trigger transcription pipeline on `final.mp3`

### Backend: Job Stage Updates

- [ ] Add new job statuses: `analyzing`, `awaiting_edit`, `editing`
- [ ] Update worker to pause at `awaiting_edit` for user input
- [ ] Update checkpointing to handle new stages

### Frontend: Timeline UI

- [ ] Create timeline component with waveform display
- [ ] Render color-coded region overlays (green/red/gray)
- [ ] Implement boundary dragging to adjust regions
- [ ] Add keep/delete toggle for each region
- [ ] Integrate HTML5 audio player synced to timeline
- [ ] Add playhead indicator that follows audio position
- [ ] Add "Apply & Transcribe" button

### Frontend: Workflow Integration

- [ ] Update upload flow to show editing stage
- [ ] Show analysis progress while detecting regions
- [ ] Transition to timeline UI when `awaiting_edit`
- [ ] Submit edits and show transcription progress
- [ ] Handle errors gracefully at each stage

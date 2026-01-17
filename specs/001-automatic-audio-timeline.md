---
status: draft
author: Addison Emig
creation_date: 2026-01-17
---

# Automatic Audio Timeline

## Problem

With [Spec 000](000-basic-audio-normalization.md), users still need Audacity to cut out singing, silence, and unwanted sections. This spec eliminates that manual step with an in-browser timeline editor.

## Proposed Solution

Replace the download/edit/re-upload workflow with:

1. **Automatic region detection** - Analyze the waveform to detect three region types:
   - `silence` - Extended periods of low amplitude
   - `speaking` - Normal speech patterns  
   - `singing` - Sustained notes, regular amplitude (easily distinguishable from speech)

2. **Timeline UI** - Visual waveform display with color-coded regions where users can:
   - Adjust region boundaries (drag edges)
   - Mark regions as keep/delete
   - Preview audio playback synced to timeline

3. **Apply edits** - Server applies cuts based on user selections, produces `final.mp3`

## Prerequisites

- [Spec 000](000-basic-audio-normalization.md) must be complete
- Job already pauses at `awaiting_edit` with `normalized.mp3` ready

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

## API Additions

- `GET /api/sermons/{id}/waveform` - Returns pre-rendered waveform data (JSON amplitude samples)
- `POST /api/sermons/{id}/analyze` - Runs waveform analysis, returns detected regions
- `POST /api/sermons/{id}/apply-edits` - Accepts regions with keep/delete flags, applies cuts, produces `final.mp3`

Audio files are served via `GET /api/sermons/{id}/audio/{type}` (from Spec 000):
- `normalized` - For editing preview
- `final` - For confirmation before transcription

## Timeline UI

- Waveform display rendered from server-generated amplitude data
- Color-coded regions: speaking=green, singing=red, silence=gray
- Each region has a keep/delete toggle
- Dragging a boundary adjusts both adjacent regions
- HTML5 `<audio>` element for playback, synced to timeline position
- Playhead indicator that follows audio position
- "Apply Edits" button to generate `final.mp3`
- "Confirm & Transcribe" button to proceed after preview

## Default Region Actions

- `speaking` → keep
- `singing` → delete  
- `silence` → delete (or auto-trim to ~1 second gap)

## Design Decisions

### Waveform rendering: server-side vs client-side

**Chosen: Server-side**

- Server already has FFmpeg
- Better performance (don't send full audio to browser just for visualization)
- Generate amplitude data on the server, render in browser canvas

### Audio preview during editing

**Chosen: Client-side virtual preview**

- Browser uses Web Audio API to skip deleted regions during playback
- Instant feedback without server round-trip
- Plays from `normalized.mp3` but respects keep/delete selections

### Audio preview after applying edits

**Chosen: Server-generated final.mp3**

- After "Apply Edits", server generates `final.mp3` with FFmpeg
- User can preview the actual final audio before transcription
- Served via `GET /api/sermons/{id}/audio/final`

### Singing detection approach

**Chosen: Waveform analysis**

- Singing has distinct characteristics: sustained energy, regular amplitude, smoother envelope
- Speech has irregular amplitude, transients, gaps between words
- Can detect without ML using basic signal analysis

## Task List

### Backend: Waveform Generation

- [ ] Generate waveform amplitude data from `normalized.mp3`
- [ ] Create `GET /api/sermons/{id}/waveform` endpoint
- [ ] Return JSON array of amplitude samples suitable for canvas rendering

### Backend: Region Detection

- [ ] Implement silence detection using FFmpeg `silencedetect` or amplitude analysis
- [ ] Implement singing detection via waveform characteristics
- [ ] Create `POST /api/sermons/{id}/analyze` endpoint
- [ ] Return regions as JSON with start, end, type

### Backend: Apply Edits

- [ ] Create `POST /api/sermons/{id}/apply-edits` endpoint
- [ ] Accept regions with keep/delete flags
- [ ] Apply cuts using FFmpeg (keep only "keep" regions)
- [ ] Save result as `final.mp3` (32kbps)
- [ ] Return success (does not start transcription yet)

### Frontend: Timeline Component

- [ ] Create timeline component with canvas waveform display
- [ ] Fetch and render waveform data from server
- [ ] Render color-coded region overlays (green/red/gray)
- [ ] Add playhead indicator

### Frontend: Region Editing

- [ ] Implement boundary dragging to adjust regions
- [ ] Add keep/delete toggle for each region
- [ ] Sync changes to region data structure

### Frontend: Audio Playback

- [ ] Integrate HTML5 audio player with `normalized.mp3`
- [ ] Sync playhead to audio position
- [ ] Click on timeline to seek
- [ ] Use Web Audio API to skip deleted regions during preview

### Frontend: Workflow Integration

- [ ] Replace download/upload UI with timeline when job is `awaiting_edit`
- [ ] Call analyze endpoint on load to get regions
- [ ] "Apply Edits" button calls apply-edits endpoint, then loads `final.mp3` for preview
- [ ] "Confirm & Transcribe" button resumes job (calls existing upload-edited or new confirm endpoint)
- [ ] Show transcription progress after confirmation

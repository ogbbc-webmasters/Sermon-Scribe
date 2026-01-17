---
status: draft
author: Addison Emig
creation_date: 2026-01-17
---

# Automatic Audio Timeline

## Problem

With spec 000, users still need Audacity to cut out singing, silence, and unwanted sections. This spec eliminates that manual step with an in-browser timeline editor.

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

- Spec 000 (Basic Audio Normalization) must be complete
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

- `POST /api/sermons/{id}/analyze` - Runs waveform analysis, returns detected regions
- `GET /api/sermons/{id}/waveform` - Returns pre-rendered waveform data (JSON amplitude samples)
- `POST /api/sermons/{id}/edit` - Accepts regions with keep/delete flags, applies cuts, produces `final.mp3`

## Timeline UI

- Waveform display rendered from server-generated amplitude data
- Color-coded regions: speaking=green, singing=red, silence=gray
- Each region has a keep/delete toggle
- Dragging a boundary adjusts both adjacent regions
- HTML5 `<audio>` element for playback, synced to timeline position
- Playhead indicator that follows audio position
- "Apply & Transcribe" button to proceed

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

### Audio preview: full file streaming vs snippets

**Chosen: Full file streaming**

- Simpler implementation: serve `normalized.mp3` directly
- Use HTML5 `<audio>` element with `currentTime` seeking
- No extra server logic for extracting snippets

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

- [ ] Create `POST /api/sermons/{id}/edit` endpoint
- [ ] Accept regions with keep/delete flags
- [ ] Apply cuts using FFmpeg (keep only "keep" regions)
- [ ] Save result as `final.mp3` (32kbps)
- [ ] Resume job to continue to transcription

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

- [ ] Integrate HTML5 audio player
- [ ] Sync playhead to audio position
- [ ] Click on timeline to seek

### Frontend: Workflow Integration

- [ ] Replace download/upload UI with timeline when job is `awaiting_edit`
- [ ] Call analyze endpoint on load to get regions
- [ ] "Apply & Transcribe" button calls edit endpoint
- [ ] Show transcription progress after edits applied

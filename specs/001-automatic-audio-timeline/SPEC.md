---
status: draft
author: Addison Emig
creation_date: 2026-01-17
---

# Automatic Audio Timeline

## Problem

With [Audio Normalization](specs/000-basic-webapp/002-audio-normalization/SPEC.md), users still need Audacity to cut out singing, silence, and unwanted sections. This spec eliminates that manual step with an in-browser timeline editor.

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

Builds on [Audio Normalization](specs/000-basic-webapp/002-audio-normalization/SPEC.md) - sermon reaches `normalized` with `normalized.mp3` ready. This spec adds the `edited` status when the timeline editor produces `final.mp3`.

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
- `POST /api/sermons/{id}/analyze` - Runs region detection, returns detected regions (async, may take 30s-2min for long audio)
- `POST /api/sermons/{id}/apply-edits` - Accepts regions with keep/delete flags, applies cuts, produces `final.mp3`
- `POST /api/sermons/{id}/confirm` - Resumes job to start transcription

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
- `silence` → delete, with 1 second gaps between kept regions; leading/trailing silence trimmed to zero

## Singing Detection Algorithm

Singing is visually distinct from speech in a waveform - congregational singing shows as dense, sustained amplitude with no gaps, while speech has irregular spikes with pauses between phrases.

Detection uses two signals analyzed over sliding windows (1-2 seconds):

1. **Amplitude variance** - Low variance = singing (sustained energy), high variance = speech (spiky)
2. **Gap ratio** - No gaps = singing, frequent gaps = speech

Consecutive windows matching singing characteristics are merged into singing regions. This is deterministic signal processing with tunable thresholds, not ML.

**Future enhancement**: Pitch stability analysis could improve accuracy if needed, but adds complexity. The two-signal approach should be sufficient given the stark visual difference between singing and speech.

## Design Decisions

**Waveform generation**: Server-side during normalization step (not lazy). User sees timeline instantly when opening editor.

**Waveform resolution**: Fixed samples per second (e.g., 10-20 samples/sec). Consistent detail regardless of duration. Frontend scales to canvas width.

**Audio preview during editing**: Client-side via Web Audio API, skipping deleted regions. Instant feedback without server round-trip.

**Audio preview after applying edits**: Server generates `final.mp3`, user previews before confirming transcription.

**Edit persistence**: User adjustments saved to browser localStorage (keyed by sermon ID). On load, restore from localStorage if available, otherwise call analyze endpoint. Clear localStorage after "Apply Edits" succeeds. Multiple tabs editing the same sermon: last write wins (not worth adding complexity for this edge case).

---
status: draft
author: Addison Emig
creation_date: 2026-01-17
---

# Audio Normalization

Server-side audio normalization for the [Basic Webapp](specs/000-basic-webapp/SPEC.md). Raw sermon recordings need cleanup before editing and publishing: mono conversion, resampling, noise gating, loudness normalization. This spec covers automated normalization ending in a downloadable `normalized.mp3`; editing out unwanted sections (intro, singing, extended silence) happens outside the system until the [Automatic Audio Timeline](specs/001-automatic-audio-timeline/SPEC.md) brings it in-browser.

## Workflow

1. Upload raw audio → creates sermon (see [Project Setup](specs/000-basic-webapp/000-project-setup/SPEC.md))
2. `normalize` job converts and normalizes → `normalized.mp3` (see [Processing Pipeline](specs/000-basic-webapp/001-processing-pipeline/SPEC.md))
3. Sermon reaches `normalization`/`done`
4. User listens to / downloads `normalized.mp3`; if the result is unsatisfying, re-runs normalization with different parameters (see below)
5. User continues in Audacity (cuts singing, silence, etc.)

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

Converts to mono, resamples to 44100 Hz, applies noise gate then loudness normalization. Output is 128kbps to preserve editing quality; later stages produce lower-bitrate publishing output.

- **Supported input formats**: any format FFmpeg can decode
- **Noise gate threshold**: TBD - needs testing with actual sermon recordings
- **loudnorm**: single-pass; sufficient for speech and avoids a second FFmpeg run

### Re-running with Different Parameters

- Chosen: normalization can be re-run on demand with a different parameter preset; each run overwrites `normalized.mp3` and the sermon's stored preset is updated
  - Presets are a small fixed set of named variations of the FFmpeg filter chain, e.g. **Standard** (default), **Stronger noise gate** (noisier recordings), **No noise gate** (quiet speakers whose soft passages get clipped by the gate), **Louder** (higher loudnorm target)
  - Exact preset parameters TBD alongside the gate threshold, from testing on real recordings
  - A re-run is an ordinary `normalize` job carrying the preset, so queueing, progress, and retry come for free
- Considered: exposing raw FFmpeg parameters in the UI
  - Rejected: the target audience is non-technical; a few labeled buttons match how they'd describe the problem ("too quiet", "still noisy")

### File Structure

```text
uploads/{sermon_id}/
  original.*          # Raw upload (any format: wav, mp3, m4a, etc.)
  normalized.mp3      # After normalization (128kbps, for editing)
```

Later specs add derived files alongside these (e.g., `final.mp3` from the timeline editor, transcription chunks).

## API

- `GET /api/sermons/{id}/audio/{type}` - Download audio file where `type` is `original` or `normalized`

Requires exe.dev proxy auth, as defined in [Project Setup](specs/000-basic-webapp/000-project-setup/SPEC.md).

## UI

When sermon stage is `normalization`/`done`, show:

1. **Download** button for `normalized.mp3` - the main action; editing continues externally in this spec (no built-in editor yet)
2. An audio player to listen to the result before downloading
3. Re-run buttons for the other normalization presets, for when the output isn't satisfying

---
status: in-progress
author: Addison Emig
creation_date: 2026-01-17
approved_by: Addison Emig
approval_date: 2026-07-18
---

# Audio Normalization

Server-side audio normalization for the [Basic Webapp](specs/000-basic-webapp/SPEC.md). Raw sermon recordings need cleanup before editing and publishing: mono conversion, resampling, noise gating, loudness normalization. This spec produces a lossless normalized master plus a browser-friendly proxy; editing out unwanted sections (intro, singing, extended silence) arrives with the [Automatic Audio Timeline](specs/001-automatic-audio-timeline/SPEC.md).

## Workflow

1. Upload raw audio → creates sermon (see [Project Setup](specs/000-basic-webapp/000-project-setup/SPEC.md))
2. `normalize` job converts and normalizes → `normalized.flac` + `normalized.mp3` (see [Processing Pipeline](specs/000-basic-webapp/001-processing-pipeline/SPEC.md))
3. Sermon reaches `normalization`/`done`
4. User listens to the result; if it is unsatisfying, re-runs normalization with a different preset (see below)

## Design Decisions

### Normalization Pipeline

- Chosen: one FFmpeg pass over the original producing two outputs - a lossless FLAC master and a 128 kbps MP3 proxy
  - Every derived artifact (notably spec 1's `final.mp3`) renders from the FLAC master, so the published audio goes through exactly **one** lossy encode
  - The proxy exists for the browser: fast-loading playback now, and waveform/scrubbing in spec 1's editor (~86 MB for 90 min vs ~300 MB FLAC)
  - Both outputs come from the same render, so the proxy is timing-identical to the master: cut timestamps chosen against the proxy apply 1:1 to the FLAC
  - FLAC is ~50-60% of WAV size and decodes anywhere FFmpeg runs
- Considered: a single 128 kbps MP3 as the normalized artifact
  - Rejected: applying cuts to an MP3 requires decode + re-encode anyway (frame granularity, bit-reservoir corruption at splice points), forcing a second lossy generation into the published output
- Considered: WAV master
  - Rejected: ~2x FLAC size for zero quality gain

```bash
ffmpeg -i original.wav \
  -ac 1 \
  -ar 44100 \
  -af "agate=threshold=TBD,loudnorm" \
  -map 0:a -c:a flac normalized.flac \
  -map 0:a -b:a 128k normalized.mp3
```

Converts to mono, resamples to 44100 Hz, applies noise gate then loudness normalization; writes both outputs in the one pass. (Exact output-mapping syntax to be settled at implementation time; the requirement is a single decode/filter run emitting both files.)

- **Supported input formats**: any format FFmpeg can decode
- **Noise gate threshold**: TBD - needs testing with actual sermon recordings
- **loudnorm**: single-pass; sufficient for speech and avoids a second FFmpeg run

### Re-running with Different Parameters

- Chosen: normalization can be re-run on demand with a different parameter preset; each run overwrites both normalized outputs and the sermon's stored preset is updated
  - Presets are a small fixed set of named variations of the FFmpeg filter chain: **Standard** (default), **Stronger noise gate** (noisier recordings), **No noise gate** (quiet speakers whose soft passages get clipped by the gate), **Louder** (higher loudnorm target), and **Quieter** (lower loudnorm target)
  - Exact preset parameters TBD alongside the gate threshold, from testing on real recordings
  - A re-run is an ordinary `normalize` job carrying the preset, so queueing, progress, and retry come for free
- Considered: exposing raw FFmpeg parameters in the UI
  - Rejected: the target audience is non-technical; a few labeled buttons match how they'd describe the problem ("too quiet", "still noisy")

### File Structure

```text
uploads/{sermon_id}/
  original.*          # Raw upload (any format: wav, mp3, m4a, etc.)
  normalized.flac     # Lossless normalized master - source for all later rendering
  normalized.mp3      # 128 kbps mono proxy - browser playback and editor preview
```

Later specs add derived files alongside these (e.g., `final.mp3` rendered from the FLAC master by the timeline editor).

## API

- `GET /api/sermons/{id}/audio/{type}` - Download audio file where `type` is `original`, `normalized` (the FLAC master), or `proxy` (the MP3)

Requires exe.dev proxy auth, as defined in [Project Setup](specs/000-basic-webapp/000-project-setup/SPEC.md).

## UI

When sermon stage is `normalization`/`done`, show:

1. An audio player streaming `normalized.mp3` to check the result
2. Five plain-language outcome actions:
   - **Continue** - downloads the MP3 proxy until the in-browser editor arrives
   - **I hear too much background noise** - re-run with Stronger noise gate
   - **Some words sound cut off** - re-run with No noise gate
   - **The recording is too quiet** - re-run with Louder
   - **The recording is too loud** - re-run with Quieter

Continue is the escape hatch until the in-browser editor arrives, not the primary long-term flow; specs 0-2 are built as one continuous effort.

---
status: draft
author: Addison Emig
creation_date: 2026-07-16
---

# Processing Pipeline

Event-based background processing for the [Basic Webapp](specs/000-basic-webapp/SPEC.md). Each stage of the audio pipeline runs as a discrete job on a queue; the frontend receives live progress over SSE.

## Goals

- Each pipeline stage (normalize, transcribe, extract metadata) is an independent, resumable job
- Jobs from different sermons can process concurrently
- Progress streams live to the frontend without polling
- Jobs survive server restarts; no stage re-runs work it already completed

## Design Decisions

### Job Queue

- Chosen: SQLite-backed job queue, one row per job, with a worker pool capped at 2 concurrent jobs
  - Event-based: completing a stage enqueues the next stage's job
  - Cap of 2 keeps FFmpeg and upload bandwidth from starving the VM while still allowing overlap across sermons
  - SQLite persistence means the queue survives restarts; on startup, jobs marked running are reset to queued
- Considered: external queue (Redis, etc.)
  - Rejected: another service to run; SQLite is already present and the load is tiny

### Pipeline Stages

Each stage is a job type. A sermon flows through:

1. `normalize` - FFmpeg normalization (see [Audio Normalization](specs/000-basic-webapp/002-audio-normalization/SPEC.md)); pipeline pauses at `awaiting_edit` until the user uploads an edited file
2. `transcribe` - transcription of `final.mp3`
3. `extract_metadata` - title, scripture references, and topics from the transcript

Sermon statuses mirror the stages: `pending` → `normalizing` → `awaiting_edit` → `transcribing` → `extracting` → `complete`, plus `failed` with an error message.

### Transcription

- Chosen: OpenRouter dedicated transcription endpoint (`/api/v1/audio/transcriptions`) with `microsoft/mai-transcribe-1.5`; model slug in config
  - Dedicated endpoint is purpose-built for speech-to-text: faster and cheaper than routing audio through chat completions
  - MAI-Transcribe 1.5 (June 2026): ~2.4% WER (top-3 on Artificial Analysis), strong on noisy/far-field real-world audio, automatic punctuation, ~276x real-time on long files, duration-based billing (~$0.36/hr)
  - Supports keyword biasing (up to 200 keywords) - feed it biblical book names and congregation-specific vocabulary, pending verification that OpenRouter passes the parameter through
- Considered: `qwen/qwen3-asr-flash` - strong benchmarks and contextual biasing
  - Rejected: avoiding Alibaba as a provider
- Considered: `openai/whisper-large-v3-turbo` - 9x cheaper ($0.04/hr), portable
  - Rejected as primary: two generations older, weaker on far-field audio, known hallucination on silence/music; kept as configured fallback
- Considered: single multimodal chat model doing transcription + metadata in one call (prior prototype used `google/gemini-3-flash-preview` via chat completions)
  - Rejected: fights the per-stage job design; base64 chat plumbing; one failure loses both outputs

### Chunking

- Chosen: split `final.mp3` into fixed-duration chunks, transcribe each as its own resumable unit, join in order
  - Per-request audio duration limits vary by model and are not documented on OpenRouter; chunking makes limits a non-issue
  - Chunk duration in config; verify MAI-Transcribe's actual limit at implementation time - if it accepts full-length files, chunk count is simply 1

### Metadata Extraction

- Chosen: separate job calling a text model (Gemini Flash, current stable slug, via OpenRouter chat completions) with the full transcript, returning structured JSON: title, scripture references, topics
  - Separate stage means improved prompts can re-run without re-transcribing
  - Also responsible for normalizing scripture references the transcription may have misheard

### Progress Streaming

- Chosen: single SSE endpoint broadcasting job events (stage started, progress percent, stage completed, failed) for all sermons; the Elm app subscribes once and updates its model
  - Use `-1` for indeterminate progress, `0-100` for determinate
- Considered: polling
  - Rejected: SSE is simple in Go and matches the event-based design

### Checkpointing

- Chosen: per-job checkpoint state in SQLite (e.g., which chunks are already transcribed, partial transcripts)
  - A restart mid-transcription resumes at the first untranscribed chunk

### Rate Limiting

- Chosen: 20 sermons/day site-wide, enforced at upload
  - Bounds worst-case API spend; trivial to raise

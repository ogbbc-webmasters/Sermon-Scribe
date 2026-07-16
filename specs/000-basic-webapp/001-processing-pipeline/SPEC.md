---
status: draft
author: Addison Emig
creation_date: 2026-07-16
---

# Processing Pipeline

Event-based background processing for the [Basic Webapp](specs/000-basic-webapp/SPEC.md). Each stage of the audio pipeline runs as a discrete job on a queue; the frontend receives live progress over SSE.

## Goals

- Each pipeline stage is an independent, resumable job
- Jobs from different sermons can process concurrently
- Progress streams live to the frontend without polling
- Jobs survive server restarts; no stage re-runs work it already completed
- New stage types can be added without changing the queue machinery (later specs add transcription and metadata stages)

## Design Decisions

### Job Queue

- Chosen: SQLite-backed job queue, one row per job, with a worker pool capped at 2 concurrent jobs
  - Event-based: completing a stage enqueues the next stage's job
  - Cap of 2 keeps FFmpeg and upload bandwidth from starving the VM while still allowing overlap across sermons
  - SQLite persistence means the queue survives restarts; on startup, jobs marked running are reset to queued
- Considered: external queue (Redis, etc.)
  - Rejected: another service to run; SQLite is already present and the load is tiny

### Pipeline Stages

Within the scope of the Basic Webapp there is one job type:

1. `normalize` - FFmpeg normalization (see [Audio Normalization](specs/000-basic-webapp/002-audio-normalization/SPEC.md)); the pipeline then pauses at `awaiting_edit` until the user uploads an edited file, which completes the pipeline

Sermon statuses: `pending` → `normalizing` → `awaiting_edit` → `complete`, plus `failed` with an error message.

Later specs extend the pipeline with more stages after the editing step: [Automatic Audio Timeline](specs/001-automatic-audio-timeline/SPEC.md) and [Transcription and Metadata](specs/002-transcription-and-metadata/SPEC.md). The stage/status vocabulary is designed to be extended, not replaced.

### Progress Streaming

- Chosen: single SSE endpoint broadcasting job events (stage started, progress percent, stage completed, failed) for all sermons; the Elm app subscribes once and updates its model
  - Use `-1` for indeterminate progress, `0-100` for determinate
- Considered: polling
  - Rejected: SSE is simple in Go and matches the event-based design

### Checkpointing

- Chosen: per-job checkpoint state in SQLite
  - A restart mid-stage resumes from the last checkpoint rather than restarting the stage from scratch
  - For `normalize` the checkpoint is coarse (re-run FFmpeg); the mechanism exists so later multi-part stages (e.g., chunked transcription) can resume mid-stage

### Rate Limiting

- Chosen: 20 sermons/day site-wide, enforced at upload
  - Bounds worst-case processing and (in later specs) API spend; trivial to raise

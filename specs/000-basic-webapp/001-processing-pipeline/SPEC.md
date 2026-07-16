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

### Sermon Stages and Job Status

- Chosen: a sermon's `stage` records the last completed milestone (always past-tense); what is *currently happening* is derived from the job queue, never duplicated into the stage
  - Stages only advance on job success, so there are no transient "-ing" stages to get stuck in, fewer writes, and restart recovery needs no stage repair
  - The UI composes the two: stage `uploaded` + running normalize job displays as "Normalizing… 40%"
- Considered: encoding in-progress states as stages (`normalizing`, `transcribing`, …)
  - Rejected: duplicates what the job table already knows and mixes two kinds of state in one enum

Within the scope of the Basic Webapp there is one job type, `normalize` (see [Audio Normalization](specs/000-basic-webapp/002-audio-normalization/SPEC.md)), and two stages:

`uploaded` → `normalized`

- `uploaded` - original stored; `normalize` job enqueued
- `normalized` - `normalized.mp3` exists; the manual edit round-trip (download, edit externally, upload `final.mp3`) is available and repeatable without a stage change

Later specs append stages rather than redefining these: the [Automatic Audio Timeline](specs/001-automatic-audio-timeline/SPEC.md) adds `edited` when its editor produces `final.mp3`, and [Transcription and Metadata](specs/002-transcription-and-metadata/SPEC.md) continues through `complete`.

### Failure and Retry

- Chosen: failure lives on the job, not the sermon - there is no failed stage. A job records its status (`queued`/`running`/`done`/`failed`), attempt count, error message, and checkpoint
  - Transient errors auto-retry with backoff, up to 3 attempts, resuming from the checkpoint
  - When attempts are exhausted the job is marked `failed`; the sermon stays at its last milestone and the UI shows the error with a Retry button that re-enqueues the job
  - Retrying is uniform for every current and future job type: re-enqueue, resume from checkpoint
- Considered: a generic `failed` sermon stage
  - Rejected: it discards which stage failed and where to resume, and transitioning out of it on retry requires restoring the prior stage anyway
- Considered: per-stage failure stages (`normalize_failed`, …)
  - Rejected: doubles the stage vocabulary with every new spec to encode what the job row already knows

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

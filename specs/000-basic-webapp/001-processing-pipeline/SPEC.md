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

### Sermon Stage and Status

- Chosen: a sermon's pipeline position is two fields - `stage` (which phase of the pipeline) and `status` (progress within that stage: `pending` / `running` / `done` / `failed`)
  - The full stage vocabulary across all planned specs: `upload` → `normalization` → `edit` → `transcription` → `extraction` → `review`
  - Completing a stage advances the sermon to the next stage at `pending`; the terminal state is the final stage at `done`
  - Machine stages are driven by jobs; human stages (later specs: `edit`, `review`) complete by user action, never enter `running` or `failed`, and have no job
  - Display state is direct: `normalization`/`running` renders as "Normalizing… 40%", `transcription`/`failed` as an error with Retry
- Considered: a single status enum mixing phases and progress (`normalizing`, `normalized`, `failed`, …)
  - Rejected: conflates two dimensions, produces inconsistent tense, and a generic `failed` loses which stage failed

Within the scope of the Basic Webapp there are two stages and one job type:

- `upload` - `pending` when the record is created, `running` while receiving, `done` when `original.*` is stored, which enqueues the `normalize` job
- `normalization` - mirrors the `normalize` job (see [Audio Normalization](specs/000-basic-webapp/002-audio-normalization/SPEC.md)); at `done`, `normalized.mp3` is available for download - the terminal state of this spec

Later specs append stages rather than redefining these: the [Automatic Audio Timeline](specs/001-automatic-audio-timeline/SPEC.md) adds `edit`, and [Transcription and Metadata](specs/002-transcription-and-metadata/SPEC.md) adds `transcription`, `extraction`, and `review`.

### Failure and Retry

- Chosen: a failed job sets its sermon's status to `failed` at the current stage; execution detail (attempt count, error message, checkpoint) lives on the job row
  - Transient errors auto-retry with backoff, up to 3 attempts, resuming from the checkpoint
  - When attempts are exhausted the job is marked failed and the sermon shows `failed` at that stage with the error and a Retry button; Retry re-enqueues the job, which resumes from its checkpoint and returns the stage to `running`
  - Retrying is uniform for every current and future job type
- Considered: keeping failure only on the job row with no sermon-visible status
  - Rejected: the sermon list would need a job-table join just to show that something needs attention

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

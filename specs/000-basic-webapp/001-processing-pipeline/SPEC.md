---
status: completed
author: Addison Emig
creation_date: 2026-07-16
approved_by: Addison Emig
approval_date: 2026-07-18
---

# Processing Pipeline

Event-based background processing for the [Basic Webapp](specs/000-basic-webapp/SPEC.md). Each stage of the audio pipeline runs as a discrete job on a queue; the frontend receives live progress over SSE.

## Goals

- Each pipeline stage is an independent, resumable job
- Jobs from different sermons can process concurrently
- Progress streams live to the frontend without polling
- Jobs survive server restarts; successfully committed work is not repeated, and interrupted work resumes from a checkpoint when the stage supports it
- New stage types can be added without changing the queue machinery (later specs add transcription and metadata stages)

## Design Decisions

### Job Queue

- Chosen: SQLite-backed job queue, one row per job, with a worker pool capped at 2 concurrent jobs
  - Event-based: completing a stage enqueues the next stage's job
  - Cap of 2 keeps FFmpeg and upload bandwidth from starving the VM while still allowing overlap across sermons
  - A worker atomically claims one queued job in a SQLite transaction; claiming marks the job and sermon running and increments the attempt count
  - The deployment has one active application instance. On startup, jobs left running by a stopped process are reset to queued
  - Worker errors and panics return the job to the retry flow rather than leaving it running
- Considered: leases and worker heartbeats
  - Rejected: they add machinery needed for multi-instance processing; startup recovery is sufficient for the single-instance deployment
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
- `normalization` - mirrors the `normalize` job (see [Audio Normalization](specs/000-basic-webapp/002-audio-normalization/SPEC.md)); at `done`, the normalized master and proxy exist and can be played back - the terminal state of this spec

Later specs append stages rather than redefining these: the [Automatic Audio Timeline](specs/001-automatic-audio-timeline/SPEC.md) adds `edit`, and [Transcription and Metadata](specs/002-transcription-and-metadata/SPEC.md) adds `transcription`, `extraction`, and `review`.

### Failure and Retry

- Chosen: a failed job sets its sermon's status to `failed` at the current stage; execution detail (attempt count, error message, checkpoint) lives on the job row
  - Transient errors auto-retry with backoff, with 3 total attempts per execution cycle, resuming from the checkpoint
  - When attempts are exhausted the job is marked failed and the sermon shows `failed` at that stage with the error and a Retry button
  - Retry re-enqueues the same job, resets its attempt count, and starts a fresh 3-attempt cycle; prior errors remain available in logs
  - Retrying is uniform for every current and future job type
- Considered: keeping failure only on the job row with no sermon-visible status
  - Rejected: the sermon list would need a job-table join just to show that something needs attention

### Progress Streaming

- Chosen: single SSE endpoint broadcasting job events (stage started, progress percent, stage completed, failed) for all sermons; the Elm app subscribes once and updates its model
  - Every connection begins with a snapshot of current sermon and job state, followed by live events; events that occur while preparing the snapshot must be delivered after it
  - Events are best-effort and are not retained for `Last-Event-ID` replay; SQLite is the source of truth after reconnecting
  - The latest progress value is persisted in SQLite so reconnect snapshots include it
  - Use `-1` for indeterminate progress, `0-100` for determinate
- Considered: a durable event log and replay
  - Rejected: the initial snapshot restores current state without retaining transient event history
- Considered: polling
  - Rejected: SSE is simple in Go and matches the event-based design

### Checkpointing and Completion

- Chosen: per-job checkpoint state in SQLite
  - Interrupted jobs resume from their latest checkpoint when the stage supports it
  - Normalization has no mid-FFmpeg checkpoint, so an interrupted FFmpeg process restarts from the beginning
  - The checkpoint mechanism exists so later multi-part stages can resume within a stage
- Chosen: a filesystem completion marker makes artifact creation idempotent across a crash between filesystem and SQLite updates
  - A job writes artifacts to job-specific temporary paths, validates them, and renames them to their final paths before atomically publishing the marker
  - The marker identifies the job, its preset or parameters, and the pipeline version; it is the commit record that all expected artifacts were produced successfully
  - If a retry finds a matching marker and valid outputs, it skips processing and completes the remaining SQLite transition
  - Outputs without a matching marker are not considered committed and may be regenerated
  - A later user-requested re-run has a new job ID, so an earlier marker cannot satisfy it
- Considered: relying only on the job row to record artifact completion
  - Rejected: SQLite and filesystem writes cannot share one transaction, so a crash after writing artifacts but before updating the row would repeat completed processing

### Rate Limiting

- Chosen: none - access is limited to trusted users behind the exe.dev proxy, so upload volume is self-limiting
- Considered: a site-wide sermons/day cap
  - Rejected: adds a failure mode for legitimate use (e.g., backfilling a sermon archive) to defend against abuse that authentication already prevents

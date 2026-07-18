# Processing Pipeline Plan

Implement [Processing Pipeline](specs/000-basic-webapp/001-processing-pipeline/SPEC.md) in focused commits on one reviewable pull request.

## Pull Request Plan

### PR 1: Persistent event-driven processing pipeline

- Add the SQLite job schema and transactional queue lifecycle, including startup recovery, progress persistence, retry history, and sermon state transitions.
- Add a two-worker processing runtime with typed handlers, bounded concurrency, panic recovery, backoff, and wakeups.
- Add reusable filesystem completion-marker support for stage handlers.
- Enqueue normalization after upload and expose failed-job retry through the HTTP API.
- Add snapshot-first SSE delivery with buffered best-effort live events.
- Connect the Elm application to SSE, display live progress and failures, and provide Retry controls.
- Validate the complete workflow with store, worker, server, and browser-level checks.

## Implementation Notes

- The queue runtime only claims job types that have registered handlers. Spec 0.2 will register the production `normalize` handler; this implementation enqueues that job type without inventing the draft normalization parameters.
- Use the existing `stage` and `status` fields as the user-visible state; job execution details remain in the jobs tables.
- Preserve the single-process deployment assumption and SQLite as the source of truth.
- Treat each bullet group above as a focused commit boundary where practical, and commit every verified working chunk before starting the next one.

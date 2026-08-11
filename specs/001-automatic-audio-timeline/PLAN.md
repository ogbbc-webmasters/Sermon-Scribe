# Automatic Audio Timeline Implementation Plan

## Waveform and Region Analysis

- Generate fixed-resolution waveform peak samples as part of normalization and publish them atomically with the normalized audio.
- Add deterministic silence, speaking, and singing classification over contiguous windows, with unit tests for classification, merging, and default keep/delete behavior.
- Expose waveform and analyzed regions through authenticated sermon endpoints.

## Durable Edit Rendering

- Extend sermon storage with the last applied region plan and edit approval state.
- Validate contiguous, complete, bounded regions before accepting an edit render.
- Render kept FLAC segments into a temporary 32 kbps mono MP3 using the durable queue, then atomically publish `final.mp3` and the applied region plan.
- Serve final audio for audition and allow repeatable renders until approval.

## Approval and Cleanup

- Require a successfully rendered final artifact before approval.
- Atomically mark the edit approved while staging source artifacts for deletion, with rollback on database failure.
- Keep `final.mp3` and remove the original, normalized master, proxy, waveform, and normalization marker.

## Elm Editor

- Add a focused editor module with explicit loading, editing, rendering, failure, and approval states.
- Render the waveform and draggable region boundaries through a small canvas port while Elm owns and validates all editor state.
- Provide large region keep/delete and boundary-adjustment controls in addition to dragging.
- Preview deleted-region skipping against the normalized proxy and audition the rendered final artifact before approval.
- Persist unapplied changes in localStorage and restore either the local draft or the last server-applied plan.

## Verification

- Add focused Go tests for waveform generation, region analysis, edit validation/rendering, queue transitions, API state checks, artifact serving, and approval cleanup.
- Compile optimized Elm and run the full Go test suite.
- Exercise the complete browser flow with a representative audio fixture when available.

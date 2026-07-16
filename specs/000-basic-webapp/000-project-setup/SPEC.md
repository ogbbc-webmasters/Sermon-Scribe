---
status: draft
author: Addison Emig
creation_date: 2026-07-16
---

# Project Setup

Scaffolding for the [Basic Webapp](specs/000-basic-webapp/SPEC.md): tech stack, data model, authentication, and the upload flow that creates sermon records. This establishes the foundation the other children build on.

## Goals

- A running web server with a simple upload UI
- Uploading an audio file creates a persistent sermon record
- Uploaded files stored on disk in a predictable per-sermon layout
- Access restricted to authenticated users

## Design Decisions

### Backend Stack

- Chosen: Go backend with SQLite database
  - Small deployment surface: one binary, one DB file
  - Proven fit in a prior prototype of this app
- Considered: Node/Python backends
  - Rejected: more runtime dependencies for no benefit at this scale

### Frontend

- Chosen: Elm 0.19.2, compiled to a single `elm.js` embedded in the Go binary via `//go:embed`
  - Recently released Elm version; strong static guarantees for an event-heavy UI (SSE-driven state updates)
  - `elm make` only - no bundler, preserving the one-binary deployment
- Considered: single hand-written HTML/JS file (prior prototype approach)
  - Rejected: the event-based UI (live job progress across multiple sermons) benefits from Elm's architecture
- Considered: JS framework (React/Vite)
  - Rejected: adds a bundler toolchain without Elm's correctness benefits

### Authentication

- Chosen: authentication and authorization are provided entirely by the exe.dev private proxy; the app trusts all requests it receives
  - The VM's share list is the authorization boundary (`ssh exe.dev share add/remove`); unauthenticated visitors are redirected to exe.dev login by the proxy and never reach the app
  - No in-app auth checks and no login/logout UI; zero auth code to maintain, and local dev needs no header injection
  - Optionally, the `X-Exedev-Email` header (present on all proxied requests) may be recorded as a nullable `uploaded_by` on the sermon record for attribution - display only, never enforcement
- Considered: application-level accounts
  - Rejected: unnecessary for a small trusted user base
- Considered: in-app presence check on the `X-Exedev-Userid` header as defense-in-depth
  - Rejected: only guards against an accidental `share set-public`, whose blast radius here is small; not worth complicating local development

### Deployment

- Chosen: systemd service on the exe.dev VM, serving on the default port behind the exe.dev proxy

### File Storage

- Chosen: per-sermon directory on disk

```text
uploads/{sermon_id}/
  original.*          # Raw upload, extension preserved (wav, mp3, m4a, etc.)
```

The normalization stage adds `normalized.flac` (lossless master) and `normalized.mp3` (browser proxy) alongside the original; later specs add further derived files. See [Audio Normalization](specs/000-basic-webapp/002-audio-normalization/SPEC.md).

### Data Model

- Chosen: a `sermons` table holding id, original filename, upload timestamp, stage, and status
- Stage and status vocabulary is defined by the [Processing Pipeline](specs/000-basic-webapp/001-processing-pipeline/SPEC.md); later specs add metadata fields (title, scripture references, topics, transcript)

### Sermon Identity

- Chosen: sermons are identified in the list by original filename plus upload date; no title field at upload
  - Keeps upload to a single step (pick a file), serving the minimal-steps UX goal
  - Weekly cadence means the date alone usually identifies a sermon
- Considered: an optional free-text title at upload
  - Rejected: [Transcription and Metadata](specs/002-transcription-and-metadata/SPEC.md) extracts a real title from the transcript, so a manual field would be redundant shortly after

### Deletion

- Chosen: hard delete, behind a confirmation dialog - removes the sermon row and its entire `uploads/{sermon_id}/` directory
  - Wrong-file and duplicate uploads are the most likely user errors in a minimal upload flow
  - Each sermon is ~1.5-2 GB across artifacts, so reclaiming disk matters on a VM
  - Low-stakes: the source recording still exists on the uploader's device
- Considered: soft delete
  - Rejected: leaves disk usage growing and requires an eventual purge story

### Upload Mechanics

- Chosen: a single `multipart/form-data` POST, streamed to disk by the upload handler
  - Simplest possible flow on both ends; a dropped connection means re-uploading, which is acceptable for trusted users on stable connections
  - Verify before relying on it that the exe.dev proxy tolerates a ~1-2 GB request body (test with a real large upload)
- Considered: chunked/resumable upload
  - Rejected: substantially more code on both ends (and fiddly file slicing in Elm) to solve a problem our users rarely have

### Upload Constraints

- Accept any format FFmpeg can decode; validation happens at normalization time, not upload time
- Max upload size: 2 GB - recordings are at most 2 hours, and 2 h of 44.1 kHz/16-bit stereo WAV is ~1.27 GB, leaving comfortable headroom (even 24-bit stereo fits); the cap is a disk/memory guardrail on the upload handler, not a quota

## UI

- Single-page Elm app: upload form plus a list of sermons with their stage/status (including progress or errors)
- Target audience is 50+ years old: large fonts, high contrast, minimal steps

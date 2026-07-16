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

- Chosen: exe.dev proxy auth via the `X-Exedev-Userid` header; all routes require it
  - Zero auth code to maintain; the proxy authenticates users
- Considered: application-level accounts
  - Rejected: unnecessary for a small trusted user base

### Deployment

- Chosen: systemd service on the exe.dev VM, serving on the default port behind the exe.dev proxy

### File Storage

- Chosen: per-sermon directory on disk

```text
uploads/{sermon_id}/
  original.*          # Raw upload, extension preserved (wav, mp3, m4a, etc.)
```

The normalization stage adds `normalized.mp3` alongside the original; later specs add further derived files. See [Audio Normalization](specs/000-basic-webapp/002-audio-normalization/SPEC.md).

### Data Model

- Chosen: a `sermons` table holding id, original filename, upload timestamp, stage, and status
- Stage and status vocabulary is defined by the [Processing Pipeline](specs/000-basic-webapp/001-processing-pipeline/SPEC.md); later specs add metadata fields (title, scripture references, topics, transcript)

### Upload Constraints

- Accept any format FFmpeg can decode; validation happens at normalization time, not upload time
- Max upload size: 2 GB - recordings are at most 2 hours, and 2 h of 44.1 kHz/16-bit stereo WAV is ~1.27 GB, leaving comfortable headroom (even 24-bit stereo fits); the cap is a disk/memory guardrail on the upload handler, not a quota

## UI

- Single-page Elm app: upload form plus a list of sermons with their stage/status (including progress or errors)
- Target audience is 50+ years old: large fonts, high contrast, minimal steps

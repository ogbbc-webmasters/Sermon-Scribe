---
status: completed
author: Addison Emig
creation_date: 2026-01-17
---

# Basic Webapp

A web application that begins turning raw sermon recordings into publish-ready audio. Today the workflow is entirely manual. This first spec tree covers a simple webapp handling the beginning of the flow: **upload raw audio → normalize**.

## Goals

- Accept raw audio uploads in any common format
- Produce normalized audio, ready for editing, without manual processing steps
- Simple UX suitable for non-technical users aged 50+ (large fonts, minimal steps)
- Resilient background processing that survives restarts
- A foundation (stack, auth, job queue, stage model) that later specs extend without rework

## Children

- [Project Setup](specs/000-basic-webapp/000-project-setup/SPEC.md) - Tech stack, project scaffolding, auth, upload, and sermon records
- [Processing Pipeline](specs/000-basic-webapp/001-processing-pipeline/SPEC.md) - Background jobs, the stage/status model, and progress streaming
- [Audio Normalization](specs/000-basic-webapp/002-audio-normalization/SPEC.md) - Server-side FFmpeg normalization producing a lossless `normalized.flac` master plus a browser proxy

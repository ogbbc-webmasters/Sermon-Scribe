---
status: draft
author: Addison Emig
creation_date: 2026-01-17
---

# Basic Webapp

A web application for turning raw sermon recordings into published-ready audio and metadata. Today the workflow requires manually editing audio in Audacity and running separate transcription tooling. This spec tree covers a fresh-start webapp that handles the full flow: **upload raw audio → normalize → edit → transcribe → extract metadata**.

This is a greenfield build; it does not reference or extend any prior codebase.

## Goals

- Eliminate Audacity from the sermon publishing workflow
- Accept raw audio uploads in any common format
- Produce a final, normalized, edited MP3 suitable for publishing
- Produce a transcript and metadata (title, scripture references, topics)
- Simple UX suitable for non-technical users aged 50+ (large fonts, minimal steps)
- Resilient background processing that survives restarts

## Children

- [Project Setup](specs/000-basic-webapp/000-project-setup/SPEC.md) - Tech stack, project scaffolding, auth, upload, and sermon records
- [Processing Pipeline](specs/000-basic-webapp/001-processing-pipeline/SPEC.md) - Background jobs, statuses, progress streaming, transcription, and metadata extraction
- [Audio Normalization](specs/000-basic-webapp/002-audio-normalization/SPEC.md) - Server-side FFmpeg normalization and the manual edit round-trip

[Automatic Audio Timeline](specs/001-automatic-audio-timeline/SPEC.md) later replaces the manual edit round-trip with an in-browser timeline editor.

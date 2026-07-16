---
status: draft
author: Addison Emig
creation_date: 2026-07-16
---

# Transcription and Metadata

Once the [Basic Webapp](specs/000-basic-webapp/SPEC.md) produces an edited `final.mp3` and the [Automatic Audio Timeline](specs/001-automatic-audio-timeline/SPEC.md) makes editing effortless, the remaining manual work is transcription and metadata entry. This spec adds pipeline stages that transcribe the final audio and extract publishing metadata (title, scripture references, topics).

## Goals

- Accurate transcript of `final.mp3` with no manual steps
- Extracted metadata: title, scripture references, topics
- Stages run as resumable jobs on the queue from the [Processing Pipeline](specs/000-basic-webapp/001-processing-pipeline/SPEC.md)
- Re-running metadata extraction (e.g., after a prompt improvement) must not require re-transcription

## Pipeline Stages

Two new job types extend the pipeline after the editing step:

1. `transcribe` - transcription of `final.mp3`
2. `extract_metadata` - title, scripture references, and topics from the transcript

Sermon statuses extend to: `... → transcribing → extracting → complete`.

## Design Decisions

### Transcription

- Chosen: OpenRouter dedicated transcription endpoint (`/api/v1/audio/transcriptions`) with `microsoft/mai-transcribe-1.5`; model slug in config
  - Dedicated endpoint is purpose-built for speech-to-text: faster and cheaper than routing audio through chat completions
  - MAI-Transcribe 1.5 (June 2026): ~2.4% WER (top-3 on Artificial Analysis), strong on noisy/far-field real-world audio, automatic punctuation, ~276x real-time on long files, duration-based billing (~$0.36/hr)
  - Supports keyword biasing (up to 200 keywords) - feed it biblical book names and congregation-specific vocabulary, pending verification that OpenRouter passes the parameter through
- Considered: `qwen/qwen3-asr-flash` - strong benchmarks and contextual biasing
  - Rejected: avoiding Alibaba as a provider
- Considered: `openai/whisper-large-v3-turbo` - 9x cheaper ($0.04/hr), portable
  - Rejected as primary: two generations older, weaker on far-field audio, known hallucination on silence/music; kept as configured fallback
- Considered: single multimodal chat model doing transcription + metadata in one call (prior prototype used `google/gemini-3-flash-preview` via chat completions)
  - Rejected: fights the per-stage job design; base64 chat plumbing; one failure loses both outputs

### Chunking

- Chosen: split `final.mp3` into fixed-duration chunks (`uploads/{sermon_id}/chunks/chunk_N.mp3`), transcribe each as its own resumable unit, join in order
  - Per-request audio duration limits vary by model and are not documented on OpenRouter; chunking makes limits a non-issue
  - Chunk duration in config; verify MAI-Transcribe's actual limit at implementation time - if it accepts full-length files, chunk count is simply 1
  - Checkpoint state records which chunks are transcribed; a restart resumes at the first untranscribed chunk

### Metadata Extraction

- Chosen: separate job calling a text model (Gemini Flash, current stable slug, via OpenRouter chat completions) with the full transcript, returning structured JSON: title, scripture references, topics
  - Separate stage means improved prompts can re-run without re-transcribing
  - Also responsible for normalizing scripture references the transcription may have misheard

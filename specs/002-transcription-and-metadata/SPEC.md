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
- A human reviews the metadata and can push back and regenerate before anything is final

## Pipeline Stages

Two new job types extend the pipeline after the editing step:

1. `transcribe` - transcription of `final.mp3`
2. `extract_metadata` - title, scripture references, and topics from the transcript

Sermon stages (per the [Processing Pipeline](specs/000-basic-webapp/001-processing-pipeline/SPEC.md) stage/status model) extend past `edit`:

`… → edit → transcription → extraction → review`

- `transcription` - mirrors the `transcribe` job, enqueued when `edit` completes
- `extraction` - mirrors the `extract_metadata` job
- `review` - a human stage: metadata is presented for review; `done` when a human accepts it. `review`/`done` is publish-ready and the pipeline's terminal state - it is never reached automatically

A Revise regeneration enqueues a new `extract_metadata` job on a sermon at `review`/`pending`; the stage does not move backward while it runs.

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

- Chosen: separate job calling a text model (Gemini Flash, current stable slug, via OpenRouter chat completions) with the full transcript, returning structured JSON
  - Separate stage means improved prompts can re-run without re-transcribing
  - Also responsible for normalizing scripture references the transcription may have misheard

Extracted fields:

- `title` - see Title Selection below
- `title_reasoning` - where in the sermon the quote came from and why it was chosen
- `speaker` - the preacher's name, if mentioned
- `scriptures` - all Bible references mentioned, with normalization rules: deduplicate (keep the more specific of "Jeremiah 2" vs "Jeremiah 2:1-37"), combine contiguous verses ("Revelation 2:1, 2:2, 2:3" → "Revelation 2:1-3"), ordered by first mention
- `topics` - 2 to 5 entries from the topics taxonomy
- `topics_reasoning` - per-topic explanation of what sermon content led to the selection

The reasoning fields make the model's choices auditable by the person publishing the sermon.

### Title Selection

- Chosen: the title must be a verbatim quote from the sermon, enforced programmatically - after extraction, the server verifies the title appears in the transcript (normalized for case, punctuation, and whitespace); on failure, retry the extraction, and if it still fails, flag the title for user attention rather than accepting it
  - The prior prototype instructed the model to use only the speaker's exact words, but prompt instructions alone were insufficient: it regularly hallucinated titles. Verbatim quotes are mechanically checkable, so enforcement belongs in code, not the prompt
  - If the speaker explicitly states a title, use that; otherwise select a quote that serves well as a title
- Considered: allowing the model to compose a title freely
  - Rejected: hallucinated titles were a recurring failure in the prior prototype, and a composed title cannot be validated against the transcript

### Topics Taxonomy

- Chosen: topics come from the curated list in [topics.md](specs/002-transcription-and-metadata/topics.md) (79 topics with descriptions, carried over from the prior prototype); the model selects from this list only, and the server rejects any topic not on it
  - The descriptions are congregation-specific domain knowledge and are included in the extraction prompt
  - Versioned in the repo as a data file the application loads, so the list can be edited without touching code

### Review and Regeneration

The prior prototype wrote extracted metadata straight to the sermon record with no way to revise; correcting a bad result meant reprocessing from scratch. This spec makes human review a required stage: nothing is publish-ready until a human accepts.

- Chosen: at the `review` stage, the UI presents the metadata with its reasoning fields and offers three actions:
  - **Accept** - marks `review`/`done`; required before the sermon is publish-ready
  - **Revise** - a free-text feedback box (e.g., "that's not the title, he stated it near the end"); the server enqueues a new `extract_metadata` job whose prompt includes the transcript, the previous metadata, and the user's feedback. Regeneration is text-only and cheap - no re-transcription - and can loop as many times as needed
  - **Edit** - inline manual edits to individual fields (fix the speaker's name, toggle a topic) for corrections that are simpler to make directly than to explain to a model
- Chosen: keep a full revision history - each generation's metadata, the feedback that prompted it, who gave it, and when - stored with the sermon and visible in the review UI
  - Multiple people may edit the same sermon; the history lets any editor see what feedback was already given and how the metadata evolved before adding their own
  - Also aids debugging prompt quality over time
- Considered: last-write-wins with no history
  - Rejected: invisible prior feedback would cause editors to repeat or contradict each other
- Considered: auto-accepting metadata with revision available after the fact
  - Rejected: publishing correct metadata is the point of the app; requiring an explicit accept builds trust and catches errors before they are published
- Considered: per-field regeneration (regenerate only the title or only the topics)
  - Rejected: a single feedback box plus inline manual edits covers the same cases with much less UI

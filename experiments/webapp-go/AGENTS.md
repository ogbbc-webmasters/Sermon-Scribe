# AGENTS.md

Go backend for Sermon Scribe.

## Structure

- `cmd/srv/main.go` - Entry point, reads OPENAI_API_KEY from env
- `srv/server.go` - HTTP handlers
- `srv/worker.go` - Background job processing
- `srv/db.go` - SQLite database
- `srv/models.go` - Data models
- `srv/index.html` - Embedded frontend (single HTML file)
- `.env` - Environment variables (not in git)
- `sermon-scribe.service` - Systemd service file

## Running the Server

**ALWAYS use the systemd service:**

```bash
sudo systemctl restart sermon-scribe
sudo systemctl status sermon-scribe
journalctl -u sermon-scribe -f
```

**NEVER run the binary directly or hardcode secrets in commands.**

The service uses `EnvironmentFile` to load `.env` safely.

## Key Details

- Uses native FFmpeg for audio processing (must be installed)
- Splits audio into 10-minute chunks for parallel transcription
- Max 3 concurrent transcription requests to OpenAI
- Frontend is embedded in binary via `//go:embed`
- SQLite database for sermon/job persistence
- Background worker processes jobs independently of HTTP requests
- SSE streaming for real-time progress updates

## Job Checkpointing

Jobs save progress at each stage so they can resume after server restart:

1. **Splitting** → saves chunk count, stores chunks in `uploads/{sermon_id}/chunks/`
2. **Transcribing** → saves each chunk's transcript as it completes
3. **Extracting metadata** → saves full transcript before calling OpenAI

On restart, stale "processing" jobs are reset to "pending" and resume from their last checkpoint.

## File Storage

```
uploads/
  {sermon_id}/
    original.mp3      # Original uploaded audio
    chunks/
      chunk_0.mp3     # 10-minute segments for transcription
      chunk_1.mp3
      ...
```

## API Endpoints

- `GET /api/sermons` - List all sermons with latest job status
- `POST /api/sermons` - Upload audio, create sermon + processing job
- `GET /api/sermons/{id}` - Get sermon details with all jobs
- `DELETE /api/sermons/{id}` - Delete sermon and files
- `GET /api/jobs/{id}` - Get job status
- `GET /api/jobs/{id}/stream` - SSE stream for real-time progress
- `GET /api/usage` - Get daily usage stats (requires auth)

## OpenAI Models

- Transcription: `gpt-4o-transcribe`
- Metadata extraction: `gpt-5-mini`

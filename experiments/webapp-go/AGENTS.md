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

- Uses OpenRouter API with Gemini 3 Flash for transcription + metadata extraction
- Single API call processes entire sermon (no chunking needed)
- FFmpeg used to prepare audio (mono, 16kHz, 64kbps MP3)
- Frontend is embedded in binary via `//go:embed`
- SQLite database for sermon/job persistence
- Background worker processes jobs independently of HTTP requests
- SSE streaming for real-time progress updates

## Job Checkpointing

Jobs save progress so they can resume after server restart:

1. **Processing** → sends audio to Gemini for transcription + metadata
2. **Extracting metadata** → if transcript exists, only extracts metadata (for resume)

On restart, stale "processing" jobs are reset to "pending" and resume from their last checkpoint.

## File Storage

```
uploads/
  {sermon_id}/
    original.mp3      # Original uploaded audio
    processed.mp3     # Converted for API (mono, 16kHz, 64kbps)
```

## API Endpoints

- `GET /api/sermons` - List all sermons with latest job status
- `POST /api/sermons` - Upload audio, create sermon + processing job
- `GET /api/sermons/{id}` - Get sermon details with all jobs
- `DELETE /api/sermons/{id}` - Delete sermon and files
- `GET /api/jobs/{id}` - Get job status
- `GET /api/jobs/{id}/stream` - SSE stream for real-time progress
- `GET /api/usage` - Get daily usage stats (requires auth)

## API Configuration

- **Provider**: OpenRouter
- **Model**: `google/gemini-3-flash-preview`
- **Single call**: Transcription + metadata extraction combined
- **Environment**: `OPENROUTER_API_KEY` in `.env`

# AGENTS.md

Go backend for Sermon Scribe.

## Structure

- `cmd/srv/main.go` - Entry point, reads OPENROUTER_API_KEY from env
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

- Uses OpenRouter API with `google/gemini-3-flash-preview` for transcription
- Audio split into 10-minute chunks for reliable API calls (don't try large single requests)
- Max 3 concurrent transcription requests
- FFmpeg used to prepare audio chunks (mono, 16kHz, 64kbps MP3)
- Frontend is embedded in binary via `//go:embed`
- SQLite database for sermon/job persistence
- Background worker processes jobs independently of HTTP requests
- SSE streaming for real-time progress updates
- Always restart server after build - changes don't take effect until restart

## Job Checkpointing

Jobs save progress at each stage so they can resume after server restart:

1. **Splitting** → saves chunk count, stores chunks in `uploads/{sermon_id}/chunks/`
2. **Transcribing** → saves each chunk's transcript as it completes
3. **Extracting metadata** → saves full transcript before calling API

On restart, stale "processing" jobs are reset to "pending" and resume from their last checkpoint.

Failed jobs can be retried via UI button (`POST /api/jobs/{id}/retry`).

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
- `POST /api/jobs/{id}/retry` - Retry a failed job (resumes from checkpoint)
- `GET /api/usage` - Get daily usage stats (requires auth)

## API Configuration

- **Provider**: OpenRouter (`https://openrouter.ai/api/v1/chat/completions`)
- **Model**: `google/gemini-3-flash-preview`
- **Environment**: `OPENROUTER_API_KEY` in `.env`

## UI Guidelines

- Target audience is 50+ years old - large fonts, simple UX, plain language ("AI" not "LLM")
- Sermon detail view must fit on desktop without scrolling - keep UI compact
- Never show raw API errors to users - log to console, show friendly message
- Progress bars should have animation so users know it's not frozen
- Provide retry buttons for failed operations - don't require manual intervention
- Use context-appropriate icons (sermons are spoken word: 🔊 not 🎵)
- Prefer CSS classes over inline styles - consolidate duplicate styles into reusable classes

## Subagent Coordination

When delegating tasks to subagents:

1. **Partition files carefully** - Assign each subagent to non-overlapping files
2. **Subagents don't build** - They make code changes only, report when done
3. **Main agent builds** - After subagent reports done, main agent builds and tests
4. **Sequential for overlapping files** - If tasks touch same files, run subagents one at a time
5. **One subagent for related changes** - If a feature spans multiple files, give it all to one subagent
6. **Delegate more, flow less** - Pause before doing work yourself and ask "could a subagent do this?"

Example workflow:
```
Me: "subagent-1, update worker.go lines 470-500. Don't build."
Me: "subagent-2, add search UI to index.html. Don't build."
[wait for both to finish]
Me: go build, test, fix issues
```

## Code Style

- Remove unused code entirely - don't neutralize with empty values or "none"
- Keep documentation precise - "OpenRouter" not "Gemini"; small inaccuracies compound
- Ask clarifying questions early - don't assume preferences for architecture, error handling, etc.

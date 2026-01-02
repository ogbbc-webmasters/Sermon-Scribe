# AGENT.md

Go backend for Sermon Scribe.

## Structure

- `cmd/srv/main.go` - Entry point, reads OPENAI_API_KEY from env
- `srv/server.go` - HTTP handlers and OpenAI API calls
- `srv/index.html` - Embedded frontend (single HTML file)
- `.env` - Environment variables (not in git)

## Key Details

- Uses native FFmpeg for audio processing (must be installed)
- Splits audio into 10-minute chunks for parallel transcription
- Max 3 concurrent transcription requests to OpenAI
- Frontend is embedded in binary via `//go:embed`

# AGENTS.md

Guidance for AI agents working on this project.

## Project Overview

Sermon Scribe is a tool for extracting metadata from sermon audio files. The project contains multiple experimental implementations.

## Repository

- **GitHub**: https://github.com/ogbbc-webmasters/Sermon-Scribe
- **Branch**: `main`
- **Commits**: Use conventional commits (e.g., `feat:`, `fix:`, `refactor:`, `docs:`, `chore:`)
- **Push access**: Configured via `gh auth setup-git` with personal access token

## File Organization

```
Sermon-Scribe/
├── README.md                    # Project overview and setup instructions
├── AGENTS.md                    # This file - guidance for AI agents
├── .gitignore                   # Git ignore patterns
└── experiments/
    ├── python-assemblyai/       # Python experiment using AssemblyAI
    │   ├── requirements.txt     # Python dependencies
    │   └── src/transcribe/      # Transcription scripts
    └── webapp-go/               # Go backend with embedded frontend
        ├── cmd/srv/main.go      # Entry point
        ├── srv/server.go        # HTTP handlers, OpenAI API calls
        ├── srv/index.html       # Embedded frontend (single HTML file)
        ├── sermon-scribe.service # systemd service
        ├── .env                 # API key (not in git)
        └── .gitignore
```

## Experiments

### webapp-go (Active)

Go backend with embedded HTML frontend.

- **Transcription**: OpenAI `gpt-4o-transcribe`
- **Metadata extraction**: OpenAI `gpt-5-mini`
- **Audio processing**: Server-side FFmpeg (splits into 10-min chunks)
- **Rate limit**: 20 sermons/day site-wide
- **Auth**: Requires exe.dev login (checks `X-Exedev-Userid` header)
- **Live Demo**: https://sermon-scribe.exe.xyz/
- **Service**: `sudo systemctl status sermon-scribe`

### python-assemblyai (Experimental)

Python script using AssemblyAI for transcription.

- Requires `.env` file with `API_KEY` and `FILE_URL`
- Not currently deployed

## Development Notes

- Frontend is embedded in Go binary via `//go:embed`
- API key stored in `.env` file (never commit, never read)
- Target audience is 50+ years old - prioritize large fonts and simple UX
- FFmpeg required on server for audio processing

## Development Workflow

1. Make changes to code
2. Build: `go build -o sermon-scribe ./cmd/srv`
3. Restart: `sudo systemctl restart sermon-scribe`
4. Test at https://sermon-scribe.exe.xyz/
5. Check logs: `journalctl -u sermon-scribe -f`
6. Commit often with conventional commits
7. Push to main when stable

## API Notes

- `gpt-5-mini` does not support custom `temperature` parameter
- Transcription uses parallel chunk processing (max 3 concurrent)
- Progress updates streamed via SSE to frontend
- Use `-1` for indeterminate progress percentage, `0-100` for determinate

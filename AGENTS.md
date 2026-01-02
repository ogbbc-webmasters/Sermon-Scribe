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
- **Metadata extraction**: OpenAI `gpt-4o-mini`
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
- API key stored in `.env` file (never commit)
- Target audience is 50+ years old - prioritize large fonts and simple UX
- FFmpeg required on server for audio processing

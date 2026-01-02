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
    │       ├── __init__.py      # Main transcription script
    │       ├── scriptures.sh    # Scripture extraction
    │       ├── title.sh         # Title extraction
    │       ├── topics.sh        # Topic extraction
    │       └── topics.txt       # Topic reference data
    └── webapp-openai/           # Browser webapp using OpenAI APIs
        ├── index.html           # Single-page webapp (HTML/CSS/JS)
        └── sermon-scribe.service # systemd service for hosting
```

## Experiments

### webapp-openai (Active)

Browser-based webapp with no backend required.

- **Transcription**: OpenAI `gpt-4o-transcribe`
- **Metadata extraction**: OpenAI `gpt-5-mini`
- **Hosting**: Served via Python http.server on port 8000
- **Live Demo**: https://sermon-scribe.exe.xyz/
- **Service**: `sudo systemctl status sermon-scribe`

### python-assemblyai (Experimental)

Python script using AssemblyAI for transcription.

- Requires `.env` file with `API_KEY` and `FILE_URL`
- Not currently deployed

## Development Notes

- The webapp is pure HTML/JS - no build step required
- API keys are entered by the user at runtime (never stored)
- Target audience is 50+ years old - prioritize large fonts and simple UX
- OpenAI transcription API has ~25MB file size limit

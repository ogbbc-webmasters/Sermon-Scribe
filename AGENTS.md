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

- **Transcription + Metadata**: OpenRouter with `google/gemini-3-flash-preview`
- **Audio processing**: Server-side FFmpeg (prepares audio for API)
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
6. **Commit after every logical change** - don't batch unrelated changes
7. Push to main when stable

## Commit Guidelines

- **Commit frequently** - after each working change, not at the end of a session
- Use conventional commits: `feat:`, `fix:`, `refactor:`, `docs:`, `chore:`
- Keep commits small and focused
- Don't wait until asked to commit

## API Notes

- Single API call via OpenRouter (using Gemini 3 Flash) handles both transcription and metadata extraction
- Progress updates streamed via SSE to frontend
- Use `-1` for indeterminate progress percentage, `0-100` for determinate

## Specture System

This project uses the Specture System for managing specifications, design records, and implementation plans. When the user asks about planned features, architectural decisions, or implementation details, use `specture list` to find the relevant spec, then read its `SPEC.md` and any sibling `PLAN.md`.

Specs live in numbered directories such as `specs/002-feature-name/SPEC.md`. `SPEC.md` contains durable design rationale, decisions, requirements, and acceptance criteria. Optional `PLAN.md` files contain disposable execution handoffs and temporary implementation notes.

The `specs/` directory also contains `README.md` with complete guidelines on how the spec system works.

### Implementation Workflow

Before implementing a spec, read its `SPEC.md` and any relevant `PLAN.md`. Commit each focused, verified implementation chunk before starting the next chunk. Keep implementation progress out of `SPEC.md`; update `PLAN.md` only when execution details need to change. Do not modify durable design decisions without explicit user permission.

### CLI Usage for AI Agents

Use the Specture CLI for deterministic discovery, creation, and validation:

- `specture list` (find active specs)
- `specture list -d all` (include the full spec tree)
- `specture new --title "Spec Title"` (create a top-level spec)
- `specture new --title "Child Title" --parent 2` (create a child spec)
- `specture validate` (validate the full tree after spec or plan edits)

Run `specture --help` and `specture <command> --help` to learn about all available options. See `specs/README.md` for the repository workflow.

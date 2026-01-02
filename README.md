# Sermon Scribe

Tools for extracting metadata from sermon audio files.

**Live Demo**: https://sermon-scribe.exe.xyz/

## Experiments

### `/experiments/webapp-go` (Active)
Go backend with embedded HTML frontend:
- **gpt-4o-transcribe** for audio transcription
- **gpt-4o-mini** for metadata extraction
- Server-side FFmpeg for fast audio processing
- Parallel chunk transcription for long sermons
- Extracts: title, speaker, scripture references, topics
- Export results as YAML
- Rate limited: 20 sermons/day site-wide
- Requires exe.dev authentication

### `/experiments/python-assemblyai`
A Python script using AssemblyAI:
- Uses AssemblyAI for transcription and summarization
- Shell scripts for extracting specific metadata

## Getting Started

### Go Webapp
1. `cd experiments/webapp-go`
2. Create `.env` with `OPENAI_API_KEY=sk-...`
3. `go build -o sermon-scribe ./cmd/srv`
4. `./sermon-scribe`
5. Open http://localhost:8000

### Python AssemblyAI
1. `cd experiments/python-assemblyai`
2. `pip install -r requirements.txt`
3. Create `.env` with `API_KEY` and `FILE_URL`
4. Run `python src/transcribe/__init__.py`

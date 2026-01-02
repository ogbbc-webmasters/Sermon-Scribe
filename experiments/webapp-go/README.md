# Sermon Scribe - Go Backend

A Go server that provides sermon transcription and metadata extraction using OpenAI APIs.

## Features

- **Server-side audio processing** with native FFmpeg (much faster than browser WASM)
- **Parallel chunk transcription** for long sermons
- **OpenAI APIs**: `gpt-4o-transcribe` for transcription, `gpt-4o-mini` for metadata
- **Single binary** with embedded frontend

## Setup

1. Create `.env` file with your OpenAI API key:
   ```
   OPENAI_API_KEY=sk-...
   ```

2. Build and run:
   ```bash
   go build -o sermon-scribe ./cmd/srv
   ./sermon-scribe
   ```

3. Open http://localhost:8000

## Deployment

Install the systemd service:
```bash
sudo cp srv.service /etc/systemd/system/sermon-scribe.service
sudo systemctl daemon-reload
sudo systemctl enable sermon-scribe
sudo systemctl start sermon-scribe
```

## API Endpoints

- `GET /` - Serves the frontend
- `POST /api/transcribe` - Upload audio file, returns transcript
- `POST /api/extract-metadata` - Extract metadata from transcript

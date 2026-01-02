# Sermon Scribe

Tools for extracting metadata from sermon audio files.

## Experiments

### `/experiments/webapp-openai`
A browser-based webapp using OpenAI APIs:
- **gpt-4o-transcribe** for audio transcription
- **gpt-5-mini** for metadata extraction
- Single HTML/JS file, no backend required
- Extracts: title, speaker, scripture references, topics
- Export results as YAML

### `/experiments/python-assemblyai`
A Python script using AssemblyAI:
- Uses AssemblyAI for transcription and summarization
- Shell scripts for extracting specific metadata

## Getting Started

### OpenAI Webapp
1. Open `experiments/webapp-openai/index.html` in a browser, or host it on a web server
2. Enter your OpenAI API key
3. Upload a sermon audio file (MP3 or WAV)
4. View and export extracted metadata

### Python AssemblyAI
1. `cd experiments/python-assemblyai`
2. `pip install -r requirements.txt`
3. Create `.env` with `API_KEY` and `FILE_URL`
4. Run `python src/transcribe/__init__.py`

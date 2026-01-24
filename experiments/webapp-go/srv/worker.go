package srv

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	openRouterURL     = "https://openrouter.ai/api/v1/chat/completions"
	geminiModel       = "google/gemini-3-flash-preview"
	openRouterAppURL  = "https://sermon-scribe.exe.xyz"
	openRouterAppName = "Sermon Scribe"
)

// Worker processes jobs in the background
type Worker struct {
	db     *DB
	apiKey string
	stop   chan struct{}
	wg     sync.WaitGroup

	// subscribers for job progress updates
	subsMu sync.RWMutex
	subs   map[string][]chan *Job // jobID -> channels
}

// NewWorker creates a new background worker
func NewWorker(db *DB, apiKey string) *Worker {
	return &Worker{
		db:     db,
		apiKey: apiKey,
		stop:   make(chan struct{}),
		subs:   make(map[string][]chan *Job),
	}
}

// Start begins processing jobs
func (w *Worker) Start() {
	w.wg.Add(1)
	go w.run()
}

// Stop gracefully stops the worker
func (w *Worker) Stop() {
	close(w.stop)
	w.wg.Wait()
}

// Subscribe returns a channel that receives job updates
func (w *Worker) Subscribe(jobID string) chan *Job {
	ch := make(chan *Job, 10)
	w.subsMu.Lock()
	w.subs[jobID] = append(w.subs[jobID], ch)
	w.subsMu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel
func (w *Worker) Unsubscribe(jobID string, ch chan *Job) {
	w.subsMu.Lock()
	defer w.subsMu.Unlock()

	chans := w.subs[jobID]
	for i, c := range chans {
		if c == ch {
			w.subs[jobID] = append(chans[:i], chans[i+1:]...)
			close(ch)
			break
		}
	}
	if len(w.subs[jobID]) == 0 {
		delete(w.subs, jobID)
	}
}

// notify sends job updates to all subscribers
func (w *Worker) notify(job *Job) {
	w.subsMu.RLock()
	defer w.subsMu.RUnlock()

	for _, ch := range w.subs[job.ID] {
		select {
		case ch <- job:
		default:
			// Channel full, skip
		}
	}
}

func (w *Worker) run() {
	defer w.wg.Done()

	for {
		select {
		case <-w.stop:
			return
		default:
		}

		job, checkpoint, err := w.db.GetPendingJobWithCheckpoint()
		if err != nil {
			slog.Error("failed to get pending job", "error", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if job == nil {
			// No pending jobs, wait a bit
			time.Sleep(1 * time.Second)
			continue
		}

		slog.Info("processing job", "jobID", job.ID, "sermonID", job.SermonID, "type", job.Type, "checkpoint_stage", checkpoint.Stage)
		w.processJob(job, checkpoint)
	}
}

func (w *Worker) processJob(job *Job, checkpoint *Checkpoint) {
	ctx := context.Background()

	// Get sermon info
	sermon, err := w.db.GetSermon(job.SermonID)
	if err != nil || sermon == nil {
		w.failJob(job, "Sermon not found")
		return
	}

	uploadsDir := filepath.Join("uploads", sermon.ID)

	// Find the audio file
	files, err := filepath.Glob(filepath.Join(uploadsDir, "original.*"))
	if err != nil || len(files) == 0 {
		w.failJob(job, "Audio file not found")
		return
	}
	inputPath := files[0]

	// Check if we already have transcript from checkpoint
	var transcript string
	var metadata *MetadataResponse

	if checkpoint.Stage == "extracting_metadata" && checkpoint.FullTranscript != "" {
		// Resume from transcript - just need metadata
		transcript = checkpoint.FullTranscript
		slog.Info("resuming at metadata extraction", "transcript_len", len(transcript))
		w.updateProgress(job, "Extracting metadata...", 80)

		metadata, err = w.extractMetadataOnly(ctx, transcript)
		if err != nil {
			w.failJob(job, "Metadata extraction failed: "+err.Error())
			return
		}
	} else {
		// Need to process audio - convert to optimal format first
		w.updateProgress(job, "Preparing audio...", 5)

		processedPath := filepath.Join(uploadsDir, "processed.mp3")
		if err := w.prepareAudio(inputPath, processedPath); err != nil {
			slog.Error("audio preparation failed", "error", err)
			w.failJob(job, "Failed to prepare audio: "+err.Error())
			return
		}

		w.updateProgress(job, "Reading audio file...", 10)

		// Read and base64 encode the audio
		audioData, err := os.ReadFile(processedPath)
		if err != nil {
			w.failJob(job, "Failed to read audio file: "+err.Error())
			return
		}

		slog.Info("audio file read", "size_mb", len(audioData)/(1024*1024), "path", processedPath)

		w.updateProgress(job, "Transcribing and analyzing sermon...", 20)

		// Single API call for transcription + metadata
		transcript, metadata, err = w.transcribeAndExtract(ctx, audioData, "mp3")
		if err != nil {
			slog.Error("transcription failed", "error", err)
			w.failJob(job, "Transcription failed: "+err.Error())
			return
		}

		// Save checkpoint with transcript in case metadata extraction needs retry
		checkpoint.Stage = "extracting_metadata"
		checkpoint.FullTranscript = transcript
		w.db.SaveCheckpoint(job.ID, checkpoint)
	}

	w.updateProgress(job, "Saving results...", 95)

	// Save results
	err = w.db.UpdateSermonMetadata(
		sermon.ID,
		metadata.Title,
		metadata.TitleGenerated,
		metadata.Speaker,
		metadata.Scriptures,
		metadata.Topics,
		transcript,
	)
	if err != nil {
		w.failJob(job, "Failed to save results: "+err.Error())
		return
	}

	// Mark complete
	w.db.CompleteJob(job.ID)
	job.Status = JobStatusComplete
	job.Progress = "Complete"
	job.Percent = 100
	w.notify(job)

	slog.Info("job completed", "jobID", job.ID, "title", metadata.Title)
}

func (w *Worker) updateProgress(job *Job, progress string, percent int) {
	w.db.UpdateJobProgress(job.ID, progress, percent)
	job.Progress = progress
	job.Percent = percent
	w.notify(job)
}

func (w *Worker) failJob(job *Job, errMsg string) {
	w.db.FailJob(job.ID, errMsg)
	job.Status = JobStatusError
	job.Error = errMsg
	w.notify(job)
	slog.Error("job failed", "jobID", job.ID, "error", errMsg)
}

// prepareAudio converts audio to optimal format for API (mono, 16kHz, 64kbps MP3)
func (w *Worker) prepareAudio(inputPath, outputPath string) error {
	cmd := exec.Command("ffmpeg", "-y",
		"-i", inputPath,
		"-acodec", "libmp3lame",
		"-b:a", "64k",
		"-ar", "16000",
		"-ac", "1",
		outputPath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg failed: %w, output: %s", err, string(output))
	}
	return nil
}

// transcribeAndExtract sends audio to Gemini and gets transcript + metadata in one call
func (w *Worker) transcribeAndExtract(ctx context.Context, audioData []byte, format string) (string, *MetadataResponse, error) {
	base64Audio := base64.StdEncoding.EncodeToString(audioData)

	slog.Info("sending audio to Gemini",
		"audio_size_mb", len(audioData)/(1024*1024),
		"base64_size_mb", len(base64Audio)/(1024*1024),
		"model", geminiModel)

	prompt := fmt.Sprintf(`You are analyzing a sermon audio recording. Please:

1. **Transcribe** the entire sermon word-for-word.

2. **Extract metadata** from the content:
   - **title**: The sermon title or main theme. If explicitly mentioned, use that. Otherwise, create a concise, descriptive title.
   - **title_generated**: false if title was explicitly stated, true if you inferred it.
   - **speaker**: The pastor/preacher's name if mentioned.
   - **scriptures**: All Bible references mentioned (e.g., "John 3:16", "Psalm 23:1-6").
   - **topics**: 2-5 topics from this predefined list that best match the sermon:

%s

Respond with JSON in this exact format:
{
  "transcript": "full word-for-word transcript here...",
  "metadata": {
    "title": "string",
    "title_generated": boolean,
    "speaker": "string or null",
    "scriptures": ["string", ...],
    "topics": ["string", ...]
  }
}`, TopicsForPrompt())

	requestBody := map[string]any{
		"model": geminiModel,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "text",
						"text": prompt,
					},
					{
						"type": "input_audio",
						"input_audio": map[string]string{
							"data":   base64Audio,
							"format": format,
						},
					},
				},
			},
		},
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	slog.Info("sending request to OpenRouter", "body_size_mb", len(jsonBody)/(1024*1024))

	req, err := http.NewRequestWithContext(ctx, "POST", openRouterURL, bytes.NewReader(jsonBody))
	if err != nil {
		return "", nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+w.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", openRouterAppURL)
	req.Header.Set("X-Title", openRouterAppName)

	client := &http.Client{Timeout: 10 * time.Minute} // Long timeout for audio processing
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read response: %w", err)
	}

	slog.Info("received response from OpenRouter", "status", resp.StatusCode, "body_size", len(respBody))

	if resp.StatusCode != http.StatusOK {
		slog.Error("OpenRouter error", "status", resp.StatusCode, "body", string(respBody))
		return "", nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content any `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", nil, fmt.Errorf("no choices in response")
	}

	// Handle content which could be string or array
	var content string
	switch c := chatResp.Choices[0].Message.Content.(type) {
	case string:
		content = c
	case []any:
		// Multimodal response - find text block
		for _, block := range c {
			if m, ok := block.(map[string]any); ok {
				if t, ok := m["type"].(string); ok && (t == "text" || t == "output_text") {
					if text, ok := m["text"].(string); ok {
						content = text
						break
					}
				}
			}
		}
	}

	if content == "" {
		return "", nil, fmt.Errorf("no content in response")
	}

	slog.Info("parsing response content", "content_len", len(content))

	// Strip markdown code blocks if present
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	// Parse combined response
	var result struct {
		Transcript string `json:"transcript"`
		Metadata   struct {
			Title          string   `json:"title"`
			TitleGenerated bool     `json:"title_generated"`
			Speaker        string   `json:"speaker"`
			Scriptures     []string `json:"scriptures"`
			Topics         []string `json:"topics"`
		} `json:"metadata"`
	}

	if err := json.Unmarshal([]byte(content), &result); err != nil {
		slog.Error("failed to parse JSON response", "error", err, "content_preview", content[:min(500, len(content))])
		return "", nil, fmt.Errorf("failed to parse response JSON: %w", err)
	}

	metadata := &MetadataResponse{
		Title:          result.Metadata.Title,
		TitleGenerated: result.Metadata.TitleGenerated,
		Speaker:        result.Metadata.Speaker,
		Scriptures:     result.Metadata.Scriptures,
		Topics:         result.Metadata.Topics,
	}

	slog.Info("transcription and extraction complete",
		"transcript_len", len(result.Transcript),
		"title", metadata.Title,
		"speaker", metadata.Speaker)

	return result.Transcript, metadata, nil
}

// extractMetadataOnly extracts metadata from an existing transcript (for checkpoint resume)
func (w *Worker) extractMetadataOnly(ctx context.Context, transcript string) (*MetadataResponse, error) {
	slog.Info("extracting metadata from transcript", "transcript_len", len(transcript))

	prompt := fmt.Sprintf(`You are analyzing a sermon transcript. Extract the following metadata and return as JSON:

1. **title**: The sermon title or main theme. If explicitly mentioned, use that. Otherwise, create a concise, descriptive title.
2. **title_generated**: false if title was explicitly stated, true if you inferred it.
3. **speaker**: The pastor/preacher's name if mentioned.
4. **scriptures**: All Bible references mentioned (e.g., "John 3:16", "Psalm 23:1-6").
5. **topics**: 2-5 topics from this predefined list that best match:

%s

Respond ONLY with JSON:
{
  "title": "string",
  "title_generated": boolean,
  "speaker": "string or null",
  "scriptures": ["string", ...],
  "topics": ["string", ...]
}

Transcript:
%s`, TopicsForPrompt(), transcript)

	requestBody := map[string]any{
		"model": geminiModel,
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
	}

	jsonBody, _ := json.Marshal(requestBody)

	req, err := http.NewRequestWithContext(ctx, "POST", openRouterURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+w.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", openRouterAppURL)
	req.Header.Set("X-Title", openRouterAppName)

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no response from API")
	}

	content := chatResp.Choices[0].Message.Content
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var metadata MetadataResponse
	if err := json.Unmarshal([]byte(content), &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

	return &metadata, nil
}

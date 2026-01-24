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
	chunksDir := filepath.Join(uploadsDir, "chunks")

	var chunks []string
	var transcript string

	// Check if we can resume from checkpoint
	if checkpoint.Stage == "transcribing" || checkpoint.Stage == "extracting_metadata" {
		// We have chunks already, find them
		for i := 0; i < checkpoint.ChunkCount; i++ {
			chunkPath := filepath.Join(chunksDir, fmt.Sprintf("chunk_%d.mp3", i))
			if _, err := os.Stat(chunkPath); err == nil {
				chunks = append(chunks, chunkPath)
			}
		}
		slog.Info("resuming from checkpoint", "stage", checkpoint.Stage, "chunks_found", len(chunks), "transcripts_done", len(checkpoint.Transcripts))
	}

	if checkpoint.Stage == "extracting_metadata" && checkpoint.FullTranscript != "" {
		// We already have the full transcript, skip to metadata
		transcript = checkpoint.FullTranscript
		slog.Info("resuming at metadata extraction", "transcript_len", len(transcript))
	} else {
		// Need to do splitting and/or transcription
		if len(chunks) == 0 {
			// Need to split audio
			files, err := filepath.Glob(filepath.Join(uploadsDir, "original.*"))
			if err != nil || len(files) == 0 {
				w.failJob(job, "Audio file not found")
				return
			}
			inputPath := files[0]

			w.updateProgress(job, "Processing audio with FFmpeg...", -1)

			// Create chunks directory
			if err := os.MkdirAll(chunksDir, 0755); err != nil {
				w.failJob(job, "Failed to create chunks directory")
				return
			}

			chunks, err = w.splitAudio(chunksDir, inputPath)
			if err != nil {
				slog.Error("ffmpeg split failed", "error", err)
				w.failJob(job, "Failed to process audio: "+err.Error())
				return
			}

			slog.Info("split audio into chunks", "count", len(chunks))

			// Save checkpoint after splitting
			checkpoint.Stage = "transcribing"
			checkpoint.ChunkCount = len(chunks)
			checkpoint.Transcripts = make([]string, len(chunks))
			w.db.SaveCheckpoint(job.ID, checkpoint)
		}

		w.updateProgress(job, fmt.Sprintf("Processed audio into %d chunks", len(chunks)), -1)

		// Transcribe chunks (with resume support)
		w.updateProgress(job, fmt.Sprintf("Transcribing %d chunks...", len(chunks)), 0)
		transcript, err = w.transcribeChunksWithCheckpoint(ctx, job, chunks, checkpoint)
		if err != nil {
			w.failJob(job, "Transcription failed: "+err.Error())
			return
		}

		// Save checkpoint with full transcript
		checkpoint.Stage = "extracting_metadata"
		checkpoint.FullTranscript = transcript
		w.db.SaveCheckpoint(job.ID, checkpoint)
	}

	w.updateProgress(job, "Extracting metadata...", 90)

	// Extract metadata
	metadata, err := w.extractMetadata(ctx, transcript)
	if err != nil {
		slog.Error("metadata extraction failed", "error", err)
		// Still save transcript even if metadata fails
		if saveErr := w.db.UpdateSermonMetadata(sermon.ID, "Unknown", true, "", "", nil, nil, nil, transcript); saveErr != nil {
			slog.Error("failed to save transcript", "error", saveErr)
		}
		w.failJob(job, "Metadata extraction failed: "+err.Error())
		return
	}

	// Save results
	err = w.db.UpdateSermonMetadata(
		sermon.ID,
		metadata.Title,
		metadata.TitleGenerated,
		metadata.TitleReasoning,
		metadata.Speaker,
		metadata.Scriptures,
		metadata.Topics,
		metadata.TopicsReasoning,
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

func (w *Worker) splitAudio(chunksDir, inputPath string) ([]string, error) {
	// Get duration
	durationCmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", inputPath)
	output, err := durationCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	var duration float64
	fmt.Sscanf(strings.TrimSpace(string(output)), "%f", &duration)
	slog.Info("audio duration", "seconds", duration)

	// Split into 10-minute chunks
	chunkDuration := 600.0
	var chunks []string

	for i := 0; float64(i)*chunkDuration < duration; i++ {
		start := float64(i) * chunkDuration
		outputPath := filepath.Join(chunksDir, fmt.Sprintf("chunk_%d.mp3", i))

		cmd := exec.Command("ffmpeg", "-y",
			"-i", inputPath,
			"-ss", fmt.Sprintf("%.0f", start),
			"-t", fmt.Sprintf("%.0f", chunkDuration),
			"-acodec", "libmp3lame",
			"-b:a", "64k",
			"-ar", "16000",
			"-ac", "1",
			outputPath,
		)
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("ffmpeg chunk %d failed: %w", i, err)
		}

		// Check if file has content
		info, err := os.Stat(outputPath)
		if err == nil && info.Size() > 1000 {
			chunks = append(chunks, outputPath)
		}
	}

	return chunks, nil
}

func (w *Worker) transcribeChunksWithCheckpoint(ctx context.Context, job *Job, chunks []string, checkpoint *Checkpoint) (string, error) {
	if len(chunks) == 0 {
		return "", fmt.Errorf("no audio chunks to transcribe")
	}

	// Ensure transcripts slice is the right size
	if len(checkpoint.Transcripts) != len(chunks) {
		checkpoint.Transcripts = make([]string, len(chunks))
	}

	// Find chunks that still need transcription
	var pendingChunks []int
	for i, t := range checkpoint.Transcripts {
		if t == "" {
			pendingChunks = append(pendingChunks, i)
		}
	}

	completed := len(chunks) - len(pendingChunks)
	slog.Info("transcription status", "total", len(chunks), "completed", completed, "pending", len(pendingChunks))

	if len(pendingChunks) > 0 {
		type result struct {
			index int
			text  string
			err   error
		}

		resultsCh := make(chan result, len(pendingChunks))
		var wg sync.WaitGroup
		sem := make(chan struct{}, 3) // Limit concurrency

		for _, idx := range pendingChunks {
			wg.Add(1)
			go func(chunkIdx int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				slog.Info("transcribing chunk", "index", chunkIdx, "path", chunks[chunkIdx])
				text, err := w.transcribeChunk(ctx, chunks[chunkIdx])
				resultsCh <- result{index: chunkIdx, text: text, err: err}
			}(idx)
		}

		// Collect results as they come in and save checkpoints
		go func() {
			wg.Wait()
			close(resultsCh)
		}()

		for r := range resultsCh {
			if r.err != nil {
				return "", fmt.Errorf("chunk %d failed: %w", r.index, r.err)
			}

			// Save transcript and checkpoint
			checkpoint.Transcripts[r.index] = r.text
			if err := w.db.SaveCheckpoint(job.ID, checkpoint); err != nil {
				slog.Error("failed to save checkpoint", "error", err)
			}

			completed++
			percent := (completed * 80) / len(chunks) // 0-80% for transcription
			w.updateProgress(job, fmt.Sprintf("Transcribing chunk %d of %d...", completed, len(chunks)), percent)
		}
	}

	// Combine all transcripts in order
	return strings.Join(checkpoint.Transcripts, "\n\n"), nil
}

// transcribeChunk sends a single audio chunk to OpenRouter for transcription
func (w *Worker) transcribeChunk(ctx context.Context, chunkPath string) (string, error) {
	// Read audio file
	audioData, err := os.ReadFile(chunkPath)
	if err != nil {
		return "", fmt.Errorf("failed to read audio: %w", err)
	}

	base64Audio := base64.StdEncoding.EncodeToString(audioData)

	slog.Info("sending chunk to Gemini", "chunk", chunkPath, "size_kb", len(audioData)/1024)

	requestBody := map[string]any{
		"model": geminiModel,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "text",
						"text": "Transcribe this audio exactly as spoken. Output only the transcript text, nothing else.",
					},
					{
						"type": "input_audio",
						"input_audio": map[string]string{
							"data":   base64Audio,
							"format": "mp3",
						},
					},
				},
			},
		},
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", openRouterURL, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+w.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", openRouterAppURL)
	req.Header.Set("X-Title", openRouterAppName)

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		slog.Error("OpenRouter error", "status", resp.StatusCode, "body", string(respBody))
		return "", w.parseAPIError(resp.StatusCode, respBody)
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content any `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return w.extractTextContent(chatResp.Choices[0].Message.Content)
}

// extractMetadata calls OpenRouter to extract metadata from transcript
func (w *Worker) extractMetadata(ctx context.Context, transcript string) (*MetadataResponse, error) {
	slog.Info("extracting metadata", "transcript_len", len(transcript))

	prompt := fmt.Sprintf(`You are analyzing a sermon transcript. Extract the following metadata and return as JSON:

1. **title**: The sermon title or main theme. If explicitly mentioned, use that. Otherwise, create a concise, descriptive title.
2. **title_generated**: false if title was explicitly stated, true if you inferred it.
3. **title_reasoning**: Explain how the title was determined:
   - If found directly in the transcript, quote the exact phrase where it was stated (e.g., "The speaker said 'Today's sermon is titled Walking in Faith'")
   - If generated, explain your reasoning (e.g., "Generated based on the main theme of forgiveness discussed throughout")
4. **speaker**: The pastor/preacher's name if mentioned.
5. **scriptures**: All Bible references mentioned (e.g., "John 3:16", "Psalm 23:1-6").
   - Deduplicate: if both "Jeremiah 2" and "Jeremiah 2:1-37" appear, keep only the more specific one
   - Combine contiguous verses: "Revelation 2:1, Revelation 2:2, Revelation 2:3" becomes "Revelation 2:1-3"
   - Keep in order of first mention in the sermon
6. **topics**: 2-5 topics from this predefined list that best match:

%s

7. **topics_reasoning**: An object mapping each selected topic to its reasoning. For each topic, briefly explain what content in the sermon led to its selection.

Respond ONLY with JSON:
{
  "title": "string",
  "title_generated": boolean,
  "title_reasoning": "string",
  "speaker": "string or null",
  "scriptures": ["string", ...],
  "topics": ["string", ...],
  "topics_reasoning": {"Topic Name": "reason for selection", ...}
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

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, w.parseAPIError(resp.StatusCode, respBody)
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no response from API")
	}

	finishReason := chatResp.Choices[0].FinishReason
	if finishReason != "stop" && finishReason != "end_turn" {
		slog.Warn("metadata response may be truncated", "finish_reason", finishReason)
	}

	content := chatResp.Choices[0].Message.Content
	if content == "" {
		slog.Error("empty content from API", "finish_reason", finishReason, "response_body", string(respBody))
		return nil, fmt.Errorf("empty response from API (finish_reason: %s)", finishReason)
	}

	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var metadata MetadataResponse
	if err := json.Unmarshal([]byte(content), &metadata); err != nil {
		slog.Error("failed to parse metadata JSON", "error", err, "content_preview", truncateForLog(content, 500))
		return nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

	slog.Info("metadata extracted", "title", metadata.Title, "speaker", metadata.Speaker)
	return &metadata, nil
}

// parseAPIError extracts a clean error message from API responses
func (w *Worker) parseAPIError(statusCode int, body []byte) error {
	switch statusCode {
	case 503:
		return fmt.Errorf("service temporarily unavailable, please retry")
	case 401:
		return fmt.Errorf("authentication failed, check API key")
	case 429:
		return fmt.Errorf("rate limit exceeded, please wait and retry")
	default:
		var errResp struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
			return fmt.Errorf("%s", errResp.Error.Message)
		}
		return fmt.Errorf("API error %d", statusCode)
	}
}

// truncateForLog returns a truncated string for logging, avoiding huge log entries
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...[truncated]"
}

// extractTextContent handles both string and array content formats from OpenRouter
func (w *Worker) extractTextContent(content any) (string, error) {
	switch c := content.(type) {
	case string:
		return c, nil
	case []any:
		for _, block := range c {
			if m, ok := block.(map[string]any); ok {
				if t, ok := m["type"].(string); ok && (t == "text" || t == "output_text") {
					if text, ok := m["text"].(string); ok {
						return text, nil
					}
				}
			}
		}
	}
	return "", fmt.Errorf("no text content found in response")
}

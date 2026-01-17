package srv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
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

			// Create chunks directory (persistent, not temp)
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

	w.updateProgress(job, "Extracting metadata...", -1)

	// Extract metadata
	metadata, err := w.extractMetadata(ctx, transcript)
	if err != nil {
		slog.Error("metadata extraction failed", "error", err)
		errMsg := err.Error()
		// Still save transcript even if metadata fails
		if saveErr := w.db.UpdateSermonMetadata(sermon.ID, "Unknown", true, "", nil, nil, transcript); saveErr != nil {
			slog.Error("failed to save transcript", "error", saveErr)
		}
		w.failJob(job, "Metadata extraction failed: "+errMsg)
		return
	}

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

func (w *Worker) splitAudio(tmpDir, inputPath string) ([]string, error) {
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
		outputPath := filepath.Join(tmpDir, fmt.Sprintf("chunk_%d.mp3", i))

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
				text, err := w.transcribeSingle(ctx, chunks[chunkIdx])
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
			percent := (completed * 100) / len(chunks)
			w.updateProgress(job, fmt.Sprintf("Transcribing chunk %d of %d...", completed, len(chunks)), percent)
		}
	}

	// Combine all transcripts in order
	return strings.Join(checkpoint.Transcripts, "\n\n"), nil
}

func (w *Worker) transcribeSingle(ctx context.Context, audioPath string) (string, error) {
	file, err := os.Open(audioPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return "", err
	}
	io.Copy(part, file)

	writer.WriteField("model", "gpt-4o-transcribe")
	writer.WriteField("language", "en")
	writer.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/audio/transcriptions", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+w.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OpenAI API error %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.Text, nil
}

func (w *Worker) extractMetadata(ctx context.Context, transcript string) (*MetadataResponse, error) {
	slog.Info("extractMetadata called", "transcriptLen", len(transcript))

	prompt := fmt.Sprintf(`You are analyzing a sermon transcript. Extract the following metadata from the transcript and return it as JSON:

1. **title**: The sermon title or main theme. If explicitly mentioned, use that. Otherwise, create a concise, descriptive title based on the main message.

2. **title_generated**: A boolean. Set to false if the title was explicitly stated in the sermon, true if you generated/inferred it.

3. **speaker**: The name of the pastor/preacher if mentioned. Look for introductions like "Pastor John" or "Reverend Smith" or self-references.

4. **scriptures**: An array of all Bible references mentioned (e.g., "John 3:16", "Psalm 23:1-6", "Romans 8"). Include chapter and verse when available.

5. **topics**: An array of 2-5 topics from the PREDEFINED LIST below that best match the sermon content. Use ONLY topics from this list, using the exact topic names provided. Select topics that are central themes, not just briefly mentioned.

PREDEFINED TOPICS:
%s

Return ONLY valid JSON in this exact format:
{
  "title": "string",
  "title_generated": boolean,
  "speaker": "string or null if not found",
  "scriptures": ["string", ...],
  "topics": ["string", ...]
}

Transcript:
%s`, TopicsForPrompt(), transcript)

	requestBody := map[string]any{
		"model": "gpt-5-mini",
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	jsonBody, _ := json.Marshal(requestBody)
	slog.Info("calling OpenAI chat completions", "model", "gpt-5-mini", "requestLen", len(jsonBody))

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		slog.Error("failed to create request", "error", err)
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+w.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		slog.Error("OpenAI request failed", "error", err)
		return nil, fmt.Errorf("failed to call OpenAI: %w", err)
	}
	defer resp.Body.Close()

	slog.Info("OpenAI response received", "status", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		slog.Error("OpenAI error response", "status", resp.StatusCode, "body", string(body))
		return nil, fmt.Errorf("OpenAI error %d: %s", resp.StatusCode, string(body))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("failed to read response body", "error", err)
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	slog.Info("OpenAI response body", "len", len(respBody))

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		slog.Error("failed to parse OpenAI response", "error", err, "body", string(respBody[:min(500, len(respBody))]))
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		slog.Error("no choices in OpenAI response", "body", string(respBody[:min(500, len(respBody))]))
		return nil, fmt.Errorf("no response from OpenAI")
	}

	content := chatResp.Choices[0].Message.Content
	slog.Info("OpenAI content received", "contentLen", len(content), "preview", content[:min(200, len(content))])

	// Strip markdown code blocks if present
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	slog.Info("parsing metadata JSON", "content", content[:min(500, len(content))])

	var metadata MetadataResponse
	if err := json.Unmarshal([]byte(content), &metadata); err != nil {
		slog.Error("failed to parse metadata JSON", "error", err, "content", content)
		return nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

	slog.Info("metadata extracted", "title", metadata.Title, "speaker", metadata.Speaker, "scriptures", len(metadata.Scriptures), "topics", len(metadata.Topics))
	return &metadata, nil
}

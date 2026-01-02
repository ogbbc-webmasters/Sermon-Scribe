package srv

import (
	"bytes"
	"context"
	"embed"
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

//go:embed index.html
var staticFiles embed.FS

const DailyLimit = 20

type Server struct {
	APIKey string

	mu         sync.Mutex
	usageDate  string // YYYY-MM-DD
	usageCount int    // total requests today
}

func New(apiKey string) *Server {
	return &Server{
		APIKey: apiKey,
	}
}

func (s *Server) Serve(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("POST /api/transcribe", s.requireAuth(s.handleTranscribe))
	mux.HandleFunc("POST /api/extract-metadata", s.requireAuth(s.handleExtractMetadata))
	mux.HandleFunc("GET /api/usage", s.requireAuth(s.handleUsage))
	slog.Info("starting server", "addr", addr, "dailyLimit", DailyLimit)
	return http.ListenAndServe(addr, mux)
}

// requireAuth wraps a handler to require exe.dev authentication
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("X-Exedev-Userid")
		if userID == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Login required"})
			return
		}
		next(w, r)
	}
}

// checkAndIncrementUsage returns true if site is within daily limit
func (s *Server) checkAndIncrementUsage() (current int, allowed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	
	// Reset count if it's a new day
	if s.usageDate != today {
		s.usageDate = today
		s.usageCount = 0
		slog.Info("reset daily usage counter", "date", today)
	}

	if s.usageCount >= DailyLimit {
		return s.usageCount, false
	}

	s.usageCount++
	return s.usageCount, true
}

// getUsage returns current site-wide usage
func (s *Server) getUsage() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	if s.usageDate != today {
		return 0
	}
	return s.usageCount
}

// handleUsage returns the site-wide usage
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"used":  s.getUsage(),
		"limit": DailyLimit,
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	data, _ := staticFiles.ReadFile("index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

// TranscribeResponse is the JSON response from transcription
type TranscribeResponse struct {
	Transcript string `json:"transcript"`
	Error      string `json:"error,omitempty"`
}

// ProgressEvent is sent via SSE during processing
type ProgressEvent struct {
	Type    string `json:"type"`    // "progress", "complete", "error"
	Step    string `json:"step"`    // Current step description
	Percent int    `json:"percent"` // 0-100
	Data    any    `json:"data,omitempty"`
}

func sendSSE(w http.ResponseWriter, event ProgressEvent) {
	data, _ := json.Marshal(event)
	fmt.Fprintf(w, "data: %s\n\n", data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	// Set up SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Check rate limit
	used, allowed := s.checkAndIncrementUsage()
	if !allowed {
		userID := r.Header.Get("X-Exedev-Userid")
		slog.Warn("rate limit exceeded", "userID", userID, "used", used)
		sendSSE(w, ProgressEvent{Type: "error", Step: fmt.Sprintf("Daily site limit reached (%d/%d). Try again tomorrow.", used, DailyLimit)})
		return
	}
	userID := r.Header.Get("X-Exedev-Userid")
	slog.Info("processing request", "userID", userID, "usage", fmt.Sprintf("%d/%d", used, DailyLimit))

	sendSSE(w, ProgressEvent{Type: "progress", Step: "Receiving audio file...", Percent: 5})

	// Parse multipart form (max 500MB)
	if err := r.ParseMultipartForm(500 << 20); err != nil {
		sendSSE(w, ProgressEvent{Type: "error", Step: "Failed to parse upload: " + err.Error()})
		return
	}

	file, header, err := r.FormFile("audio")
	if err != nil {
		sendSSE(w, ProgressEvent{Type: "error", Step: "No audio file provided"})
		return
	}
	defer file.Close()

	slog.Info("received audio file", "name", header.Filename, "size", header.Size)
	sendSSE(w, ProgressEvent{Type: "progress", Step: fmt.Sprintf("Received %s (%.1f MB)", header.Filename, float64(header.Size)/(1024*1024)), Percent: 10})

	// Save to temp file
	tmpDir, err := os.MkdirTemp("", "sermon-*")
	if err != nil {
		sendSSE(w, ProgressEvent{Type: "error", Step: "Failed to create temp dir"})
		return
	}
	defer os.RemoveAll(tmpDir)

	ext := filepath.Ext(header.Filename)
	if ext == "" {
		ext = ".mp3"
	}
	inputPath := filepath.Join(tmpDir, "input"+ext)

	f, err := os.Create(inputPath)
	if err != nil {
		sendSSE(w, ProgressEvent{Type: "error", Step: "Failed to save file"})
		return
	}
	io.Copy(f, file)
	f.Close()

	sendSSE(w, ProgressEvent{Type: "progress", Step: "Processing audio with FFmpeg...", Percent: 15})

	// Convert to optimized MP3 chunks using ffmpeg
	chunks, err := splitAudio(tmpDir, inputPath)
	if err != nil {
		slog.Error("ffmpeg split failed", "error", err)
		sendSSE(w, ProgressEvent{Type: "error", Step: "Failed to process audio: " + err.Error()})
		return
	}

	slog.Info("split audio into chunks", "count", len(chunks))
	sendSSE(w, ProgressEvent{Type: "progress", Step: fmt.Sprintf("Split into %d chunks for transcription", len(chunks)), Percent: 20})

	// Transcribe chunks with progress callback
	transcript, err := s.transcribeChunksWithProgress(r.Context(), chunks, func(completed, total int) {
		percent := 20 + (completed*70)/total
		sendSSE(w, ProgressEvent{Type: "progress", Step: fmt.Sprintf("Transcribing chunk %d of %d...", completed, total), Percent: percent})
	})
	if err != nil {
		sendSSE(w, ProgressEvent{Type: "error", Step: "Transcription failed: " + err.Error()})
		return
	}

	sendSSE(w, ProgressEvent{Type: "progress", Step: "Extracting metadata...", Percent: 92})

	// Extract metadata
	metadata, err := s.extractMetadata(r.Context(), transcript)
	if err != nil {
		slog.Error("metadata extraction failed", "error", err)
		// Still return transcript even if metadata fails
		sendSSE(w, ProgressEvent{Type: "complete", Step: "Complete (metadata extraction failed)", Percent: 100, Data: map[string]any{
			"transcript": transcript,
			"title":      "Unknown",
			"speaker":    "",
			"scriptures": []string{},
			"topics":     []string{},
		}})
		return
	}

	sendSSE(w, ProgressEvent{Type: "complete", Step: "Complete!", Percent: 100, Data: map[string]any{
		"transcript": transcript,
		"title":      metadata.Title,
		"speaker":    metadata.Speaker,
		"scriptures": metadata.Scriptures,
		"topics":     metadata.Topics,
	}})
}

func splitAudio(tmpDir, inputPath string) ([]string, error) {
	// First, get duration
	durationCmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", inputPath)
	output, err := durationCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	var duration float64
	fmt.Sscanf(strings.TrimSpace(string(output)), "%f", &duration)
	slog.Info("audio duration", "seconds", duration)

	// Split into 10-minute chunks
	chunkDuration := 600.0 // 10 minutes
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

func (s *Server) transcribeChunksWithProgress(ctx context.Context, chunks []string, onProgress func(completed, total int)) (string, error) {
	if len(chunks) == 0 {
		return "", fmt.Errorf("no audio chunks to transcribe")
	}

	// Transcribe in parallel (max 3 concurrent)
	type result struct {
		index int
		text  string
		err   error
	}

	results := make([]result, len(chunks))
	var wg sync.WaitGroup
	var completedMu sync.Mutex
	completed := 0
	sem := make(chan struct{}, 3) // Limit concurrency

	for i, chunk := range chunks {
		wg.Add(1)
		go func(idx int, path string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			slog.Info("transcribing chunk", "index", idx, "path", path)
			text, err := s.transcribeSingle(ctx, path)
			results[idx] = result{index: idx, text: text, err: err}

			completedMu.Lock()
			completed++
			if onProgress != nil {
				onProgress(completed, len(chunks))
			}
			completedMu.Unlock()
		}(i, chunk)
	}

	wg.Wait()

	// Combine results in order
	var transcripts []string
	for _, r := range results {
		if r.err != nil {
			return "", fmt.Errorf("chunk %d failed: %w", r.index, r.err)
		}
		transcripts = append(transcripts, r.text)
	}

	return strings.Join(transcripts, "\n\n"), nil
}

func (s *Server) transcribeSingle(ctx context.Context, audioPath string) (string, error) {
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
	req.Header.Set("Authorization", "Bearer "+s.APIKey)
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

// MetadataRequest is the request body for metadata extraction
type MetadataRequest struct {
	Transcript string `json:"transcript"`
}

// MetadataResponse is the response from metadata extraction
type MetadataResponse struct {
	Title      string   `json:"title"`
	Speaker    string   `json:"speaker"`
	Scriptures []string `json:"scriptures"`
	Topics     []string `json:"topics"`
	Error      string   `json:"error,omitempty"`
}

// extractMetadata calls OpenAI to extract metadata from transcript
func (s *Server) extractMetadata(ctx context.Context, transcript string) (*MetadataResponse, error) {
	// Truncate transcript if too long
	if len(transcript) > 30000 {
		transcript = transcript[:30000]
	}

	prompt := fmt.Sprintf(`You are analyzing a sermon transcript. Extract the following metadata from the transcript and return it as JSON:

1. **title**: The sermon title or main theme. If explicitly mentioned, use that. Otherwise, create a concise, descriptive title based on the main message.

2. **speaker**: The name of the pastor/preacher if mentioned. Look for introductions like "Pastor John" or "Reverend Smith" or self-references.

3. **scriptures**: An array of all Bible references mentioned (e.g., "John 3:16", "Psalm 23:1-6", "Romans 8"). Include chapter and verse when available.

4. **topics**: An array of 3-7 main topics or themes discussed in the sermon (e.g., "faith", "forgiveness", "prayer", "salvation").

Return ONLY valid JSON in this exact format:
{
  "title": "string",
  "speaker": "string or null if not found",
  "scriptures": ["string", ...],
  "topics": ["string", ...]
}

Transcript:
%s`, transcript)

	requestBody := map[string]any{
		"model": "gpt-5-mini",
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0.3,
	}

	jsonBody, _ := json.Marshal(requestBody)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(jsonBody))
	httpReq.Header.Set("Authorization", "Bearer "+s.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to call OpenAI: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenAI error %d: %s", resp.StatusCode, string(body))
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
		return nil, fmt.Errorf("no response from OpenAI")
	}

	content := chatResp.Choices[0].Message.Content
	// Strip markdown code blocks if present
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

func (s *Server) handleExtractMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req MetadataRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(MetadataResponse{Error: "Invalid request"})
		return
	}

	if req.Transcript == "" {
		json.NewEncoder(w).Encode(MetadataResponse{Error: "No transcript provided"})
		return
	}

	metadata, err := s.extractMetadata(r.Context(), req.Transcript)
	if err != nil {
		json.NewEncoder(w).Encode(MetadataResponse{Error: err.Error()})
		return
	}

	json.NewEncoder(w).Encode(metadata)
}

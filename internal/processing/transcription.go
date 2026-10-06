package processing

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

const openRouterURL = "https://openrouter.ai/api/v1"
const openRouterDecisionsURL = "https://openrouter.ai/api/alpha/decisions"

type AIConfig struct {
	APIKey             string
	TranscriptionModel string
	MetadataModel      string
	TopicModel         string
}

type TranscriptionHandler struct {
	store      *store.Store
	uploadsDir string
	config     AIConfig
	client     *http.Client
}

func NewTranscriptionHandler(st *store.Store, uploadsDir string, config AIConfig) *TranscriptionHandler {
	if config.TranscriptionModel == "" {
		config.TranscriptionModel = "microsoft/mai-transcribe-1.5"
	}
	return &TranscriptionHandler{store: st, uploadsDir: uploadsDir, config: config, client: &http.Client{Timeout: 5 * time.Minute}}
}

func (h *TranscriptionHandler) Run(ctx context.Context, job store.Job, reporter Reporter) (Result, error) {
	if h.config.APIKey == "" {
		return Result{}, fmt.Errorf("OPENROUTER_API_KEY is not configured")
	}
	input, err := findOriginal(filepath.Join(h.uploadsDir, job.SermonID))
	if err != nil {
		return Result{}, err
	}
	file, err := os.Open(input)
	if err != nil {
		return Result{}, err
	}
	defer file.Close()

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", filepath.Base(input))
	if err != nil {
		return Result{}, err
	}
	if _, err := io.Copy(part, file); err != nil {
		return Result{}, err
	}
	if err := form.WriteField("model", h.config.TranscriptionModel); err != nil {
		return Result{}, err
	}
	contentType := form.FormDataContentType()
	if err := form.Close(); err != nil {
		return Result{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openRouterURL+"/audio/transcriptions", &body)
	if err != nil {
		return Result{}, err
	}
	setAIHeaders(req, h.config.APIKey, contentType)
	resp, err := h.client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Result{}, err
	}
	if resp.StatusCode/100 != 2 {
		return Result{}, apiError(resp.StatusCode, responseBody)
	}
	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return Result{}, fmt.Errorf("decode transcription response: %w", err)
	}
	if strings.TrimSpace(result.Text) == "" {
		return Result{}, fmt.Errorf("transcription response was empty")
	}
	if err := h.store.SaveTranscript(job.SermonID, result.Text); err != nil {
		return Result{}, err
	}
	return Result{
		Next: &store.NewJob{
			ID:         uuid.New().String(),
			Type:       "extract_metadata",
			Stage:      "metadata",
			Parameters: `{}`,
		},
	}, reporter.Progress(100, nil)
}

type MetadataHandler struct {
	store  *store.Store
	config AIConfig
	client *http.Client
}

func NewMetadataHandler(st *store.Store, config AIConfig) *MetadataHandler {
	if config.MetadataModel == "" {
		config.MetadataModel = "google/gemini-3-flash-preview"
	}
	if config.TopicModel == "" {
		config.TopicModel = "typesafe/jev-1.13"
	}
	return &MetadataHandler{store: st, config: config, client: &http.Client{Timeout: 5 * time.Minute}}
}

func (h *MetadataHandler) Run(ctx context.Context, job store.Job, reporter Reporter) (Result, error) {
	if h.config.APIKey == "" {
		return Result{}, fmt.Errorf("OPENROUTER_API_KEY is not configured")
	}
	sermon, err := h.store.GetSermon(job.SermonID)
	if err != nil {
		return Result{}, err
	}
	if sermon.Transcript == nil || strings.TrimSpace(*sermon.Transcript) == "" {
		return Result{}, fmt.Errorf("transcript is not available")
	}

	payload := map[string]any{
		"model": h.config.MetadataModel,
		"messages": []map[string]string{{
			"role":    "user",
			"content": metadataPrompt(*sermon.Transcript),
		}},
		"response_format": map[string]string{"type": "json_object"},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openRouterURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return Result{}, err
	}
	setAIHeaders(req, h.config.APIKey, "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Result{}, err
	}
	if resp.StatusCode/100 != 2 {
		return Result{}, apiError(resp.StatusCode, responseBody)
	}

	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil || len(envelope.Choices) == 0 {
		return Result{}, fmt.Errorf("invalid metadata response")
	}
	content := strings.TrimSpace(strings.Trim(envelope.Choices[0].Message.Content, "`"))
	content = strings.TrimSpace(strings.TrimPrefix(content, "json"))

	var result struct {
		Title          string   `json:"title"`
		TitleGenerated bool     `json:"title_generated"`
		TitleReasoning string   `json:"title_reasoning"`
		Speaker        string   `json:"speaker"`
		Scriptures     []string `json:"scriptures"`
		Topics         []string `json:"topics"`
	}
	rawMetadata := json.RawMessage(content)
	if len(rawMetadata) > 0 && rawMetadata[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(rawMetadata, &items); err != nil || len(items) == 0 {
			return Result{}, fmt.Errorf("parse metadata: expected a non-empty JSON array")
		}
		rawMetadata = items[0]
	}
	if err := json.Unmarshal(rawMetadata, &result); err != nil {
		return Result{}, fmt.Errorf("parse metadata: %w", err)
	}
	if result.Title == "" {
		return Result{}, fmt.Errorf("metadata response did not include a title")
	}
	scores, err := h.classifyTopics(ctx, *sermon.Transcript)
	if err != nil {
		return Result{}, err
	}
	if err := h.store.SaveMetadata(job.SermonID, result.Title, result.TitleGenerated, result.TitleReasoning, result.Speaker, result.Scriptures, result.Topics, scores); err != nil {
		return Result{}, err
	}
	return Result{}, reporter.Progress(100, nil)
}

func (h *MetadataHandler) classifyTopics(ctx context.Context, transcript string) (map[string]float64, error) {
	questions := make(map[string]any)
	for name, detail := range topicDetails() {
		key := strings.ToLower(strings.ReplaceAll(name, " ", "_"))
		questions[key] = map[string]any{
			"type":         "noul",
			"instructions": "Does this sermon substantially address the topic " + name + "?",
			"criteria": map[string]string{
				"true":  detail,
				"false": "The sermon does not substantially teach or focus on this topic.",
			},
		}
	}
	body, err := json.Marshal(map[string]any{
		"model":     h.config.TopicModel,
		"state":     map[string]string{"transcript": transcript},
		"questions": questions,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openRouterDecisionsURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	setAIHeaders(req, h.config.APIKey, "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, apiError(resp.StatusCode, data)
	}
	var response struct {
		Answers map[string]struct {
			Noul float64 `json:"noul"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode topic response: %w", err)
	}
	scores := make(map[string]float64, len(response.Answers))
	for name := range topicDetails() {
		key := strings.ToLower(strings.ReplaceAll(name, " ", "_"))
		if answer, ok := response.Answers[key]; ok {
			scores[name] = answer.Noul
		}
	}
	return scores, nil
}

func setAIHeaders(req *http.Request, key, contentType string) {
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("HTTP-Referer", "https://sermon-scribe.exe.xyz")
	req.Header.Set("X-Title", "Sermon Scribe")
}

func apiError(status int, body []byte) error {
	var response struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &response) == nil && response.Error.Message != "" {
		return fmt.Errorf("OpenRouter: %s", response.Error.Message)
	}
	return fmt.Errorf("OpenRouter returned HTTP %d", status)
}

//go:embed topics.md
var topicsTaxonomy string

func metadataPrompt(transcript string) string {
	return `You are analyzing a sermon transcript. Return only valid JSON.

Extract:
- title: an exact quote from the speaker that works as a sermon title
- title_generated: false only when the speaker explicitly announced the title
- title_reasoning: briefly explain the choice
- speaker: the preacher's name, or an empty string
- scriptures: normalized Bible references, deduplicated and kept in first-mention order
- topics: 2-5 labels from the taxonomy below

Topic taxonomy:
` + topicsTaxonomy + `

Transcript:
` + transcript
}

func topicDetails() map[string]string {
	details := make(map[string]string)
	var current string
	var description []string
	flush := func() {
		if current != "" {
			details[current] = strings.TrimSpace(strings.Join(description, " "))
		}
	}
	for _, line := range strings.Split(topicsTaxonomy, "\n") {
		if strings.HasPrefix(line, "## ") {
			flush()
			current = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			description = nil
		} else if current != "" && strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "# ") {
			description = append(description, strings.TrimSpace(line))
		}
	}
	flush()
	return details
}

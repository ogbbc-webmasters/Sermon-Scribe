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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

const openRouterURL = "https://openrouter.ai/api/v1"
const openRouterDecisionsURL = "https://openrouter.ai/api/alpha/decisions"
const maxTranscriptionRequestBytes = 25_000_000
const maxScriptureOptions = 6

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
		config.TranscriptionModel = "microsoft/mai-transcribe-2"
	}
	return &TranscriptionHandler{store: st, uploadsDir: uploadsDir, config: config, client: &http.Client{Timeout: 5 * time.Minute}}
}

func (h *TranscriptionHandler) Run(ctx context.Context, job store.Job, reporter Reporter) (Result, error) {
	if h.config.APIKey == "" {
		return Result{}, fmt.Errorf("OPENROUTER_API_KEY is not configured")
	}
	dir := filepath.Join(h.uploadsDir, job.SermonID)
	input := filepath.Join(dir, "normalized.mp3")
	if _, err := os.Stat(input); os.IsNotExist(err) {
		// Prepare legacy uploads on their first regeneration too. Never send
		// the original upload to OpenRouter, regardless of its size or format.
		normalize := NewNormalizeHandler(h.store, h.uploadsDir)
		_, err := normalize.Run(ctx, store.Job{
			ID: "normalize-" + job.ID, SermonID: job.SermonID,
			Parameters: `{"gate_adjustment":0,"volume_adjustment":0}`,
		}, reporter)
		if err != nil {
			return Result{}, err
		}
	} else if err != nil {
		return Result{}, err
	}
	info, err := os.Stat(input)
	if err != nil {
		return Result{}, err
	}
	reducedInput := ""
	if info.Size() > maxTranscriptionRequestBytes {
		reducedInput, err = makeTranscriptionAudio(ctx, dir, input)
		if err != nil {
			return Result{}, err
		}
		defer os.Remove(reducedInput)
		input = reducedInput
	}

	body, contentType, err := buildTranscriptionMultipart(input, h.config.TranscriptionModel)
	if err != nil {
		return Result{}, err
	}
	if body.Len() > maxTranscriptionRequestBytes && reducedInput == "" {
		reducedInput, err = makeTranscriptionAudio(ctx, dir, input)
		if err != nil {
			return Result{}, err
		}
		defer os.Remove(reducedInput)
		body, contentType, err = buildTranscriptionMultipart(reducedInput, h.config.TranscriptionModel)
		if err != nil {
			return Result{}, err
		}
	}
	if body.Len() > maxTranscriptionRequestBytes {
		return Result{}, fmt.Errorf("transcription request exceeds the 25 MB limit after reduced-rate encoding; split the recording")
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
		Text     string  `json:"text"`
		Language string  `json:"language"`
		Duration float64 `json:"duration"`
		Segments []struct {
			Start   float64 `json:"start"`
			End     float64 `json:"end"`
			Text    string  `json:"text"`
			Speaker *int    `json:"speaker"`
		} `json:"segments"`
		Words []struct {
			Word         string   `json:"word"`
			Start        float64  `json:"start"`
			End          float64  `json:"end"`
			Speaker      *int     `json:"speaker"`
			SpeakerLabel string   `json:"speaker_label"`
			Confidence   *float64 `json:"confidence"`
		} `json:"words"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return Result{}, fmt.Errorf("decode transcription response: %w", err)
	}
	if strings.TrimSpace(result.Text) == "" {
		return Result{}, fmt.Errorf("transcription response was empty")
	}
	metadata := store.TranscriptionMetadata{
		Language: result.Language,
		Duration: result.Duration,
		Segments: make([]store.TranscriptSegment, len(result.Segments)),
		Words:    make([]store.TranscriptWord, len(result.Words)),
	}
	for i, segment := range result.Segments {
		metadata.Segments[i] = store.TranscriptSegment{
			Start: segment.Start, End: segment.End, Text: segment.Text, Speaker: segment.Speaker,
		}
	}
	for i, word := range result.Words {
		metadata.Words[i] = store.TranscriptWord{
			Word: word.Word, Start: word.Start, End: word.End, Speaker: word.Speaker,
			SpeakerLabel: word.SpeakerLabel, Confidence: word.Confidence,
		}
	}
	if err := h.store.SaveTranscription(job.SermonID, result.Text, metadata); err != nil {
		return Result{}, err
	}
	return Result{
		Next: &store.NewJob{
			ID:         uuid.New().String(),
			Type:       "extract_metadata",
			Stage:      "metadata",
			Parameters: job.Parameters,
		},
	}, reporter.Progress(100, nil)
}

func makeTranscriptionAudio(ctx context.Context, dir, fallbackInput string) (string, error) {
	source := fallbackInput
	flac := filepath.Join(dir, "normalized.flac")
	if _, err := os.Stat(flac); err == nil {
		source = flac
	} else if !os.IsNotExist(err) {
		return "", err
	}

	file, err := os.CreateTemp(dir, ".transcription-*.mp3")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	if err := encodeTranscriptionAudio(ctx, source, path); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("encode reduced-rate transcription audio: %w", err)
	}
	return path, nil
}

func buildTranscriptionMultipart(input, model string) (bytes.Buffer, string, error) {
	file, err := os.Open(input)
	if err != nil {
		return bytes.Buffer{}, "", err
	}
	defer file.Close()

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", filepath.Base(input))
	if err != nil {
		return bytes.Buffer{}, "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return bytes.Buffer{}, "", err
	}
	if err := form.WriteField("model", model); err != nil {
		return bytes.Buffer{}, "", err
	}
	if err := form.WriteField("response_format", "verbose_json"); err != nil {
		return bytes.Buffer{}, "", err
	}
	for _, granularity := range []string{"segment", "word"} {
		if err := form.WriteField("timestamp_granularities[]", granularity); err != nil {
			return bytes.Buffer{}, "", err
		}
	}
	providerOptions, err := json.Marshal(map[string]any{
		"options": map[string]any{
			"azure": map[string]any{
				"diarization": map[string]bool{"enabled": true},
				"enhancedMode": map[string]any{
					"modelOptions": map[string]string{"transcribeStyle": "verbatim"},
				},
			},
		},
	})
	if err != nil {
		return bytes.Buffer{}, "", fmt.Errorf("encode transcription provider options: %w", err)
	}
	if err := form.WriteField("provider", string(providerOptions)); err != nil {
		return bytes.Buffer{}, "", err
	}
	contentType := form.FormDataContentType()
	if err := form.Close(); err != nil {
		return bytes.Buffer{}, "", err
	}
	return body, contentType, nil
}

type MetadataHandler struct {
	store  *store.Store
	config AIConfig
	client *http.Client
}

type titleCandidate struct {
	Title          string `json:"title"`
	TitleGenerated bool   `json:"title_generated"`
	TitleReasoning string `json:"title_reasoning"`
}

func NewMetadataHandler(st *store.Store, config AIConfig) *MetadataHandler {
	if config.MetadataModel == "" {
		config.MetadataModel = "openai/gpt-6-luna"
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
			"content": metadataPrompt(*sermon.Transcript, job.Type),
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
		TitleCandidates     []titleCandidate `json:"title_candidates"`
		Speaker             string           `json:"speaker"`
		OldTestamentReading string           `json:"old_testament_reading"`
		NewTestamentReading string           `json:"new_testament_reading"`
		Scriptures          []string         `json:"scriptures"`
		Topics              []string         `json:"topics"`
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
	if job.Type == "extract_topics" || job.Type == "extract_scriptures" {
		result.TitleCandidates = nil
	}
	if job.Type != "extract_topics" && job.Type != "extract_scriptures" && len(result.TitleCandidates) != 5 {
		return Result{}, fmt.Errorf("metadata response must include exactly five title candidates")
	}
	seen := make(map[string]bool)
	for i := range result.TitleCandidates {
		candidate := &result.TitleCandidates[i]
		candidate.Title = strings.TrimSpace(candidate.Title)
		key := strings.ToLower(candidate.Title)
		if key == "" || seen[key] {
			return Result{}, fmt.Errorf("metadata response must include five distinct non-empty title candidates")
		}
		seen[key] = true
	}
	if job.Type == "extract_title" || job.Type == "extract_topics" {
		result.OldTestamentReading = ""
		result.NewTestamentReading = ""
		result.Scriptures = nil
	}
	result.OldTestamentReading = strings.TrimSpace(result.OldTestamentReading)
	result.NewTestamentReading = strings.TrimSpace(result.NewTestamentReading)
	readings := make([]string, 0, 2)
	if result.OldTestamentReading != "" {
		readings = append(readings, result.OldTestamentReading)
	}
	if result.NewTestamentReading != "" {
		readings = append(readings, result.NewTestamentReading)
	}
	allScriptures := append(append([]string{}, readings...), result.Scriptures...)
	if err := validateScriptureVerseReferences(allScriptures); err != nil {
		return Result{}, err
	}
	if normalized := normalizeScriptures([]string{result.OldTestamentReading}); len(normalized) > 0 {
		result.OldTestamentReading = normalized[0]
	}
	if normalized := normalizeScriptures([]string{result.NewTestamentReading}); len(normalized) > 0 {
		result.NewTestamentReading = normalized[0]
	}
	readings = readings[:0]
	if result.OldTestamentReading != "" {
		readings = append(readings, result.OldTestamentReading)
	}
	if result.NewTestamentReading != "" {
		readings = append(readings, result.NewTestamentReading)
	}
	selectedReadings := normalizeScriptures(readings)
	scriptureOptions := append([]string{}, selectedReadings...)
	for _, candidate := range normalizeScriptures(result.Scriptures) {
		if !slices.Contains(scriptureOptions, candidate) {
			scriptureOptions = append(scriptureOptions, candidate)
		}
	}
	if len(scriptureOptions) > maxScriptureOptions {
		scriptureOptions = scriptureOptions[:maxScriptureOptions]
	}
	title, scores, err := h.classifyMetadata(ctx, *sermon.Transcript, result.TitleCandidates, job.Type != "extract_title" && job.Type != "extract_scriptures")
	if err != nil {
		return Result{}, err
	}
	switch job.Type {
	case "extract_title":
		err = h.store.SaveTitle(job.SermonID, title.Title, title.TitleGenerated, title.TitleReasoning)
	case "extract_topics":
		err = h.store.SaveTopics(job.SermonID, result.Topics, scores)
	case "extract_scriptures":
		err = h.store.SaveScriptureSelection(job.SermonID, result.OldTestamentReading, result.NewTestamentReading, selectedReadings, scriptureOptions)
	default:
		err = h.store.SaveMetadataWithScriptureOptions(job.SermonID, title.Title, title.TitleGenerated, title.TitleReasoning, result.Speaker, result.OldTestamentReading, result.NewTestamentReading, selectedReadings, scriptureOptions, result.Topics, scores)
	}
	if err != nil {
		return Result{}, err
	}
	return Result{}, reporter.Progress(100, nil)
}

func (h *MetadataHandler) classifyMetadata(ctx context.Context, transcript string, candidates []titleCandidate, includeTopics bool) (titleCandidate, map[string]float64, error) {
	questions := make(map[string]any)
	if len(candidates) == 0 && !includeTopics {
		return titleCandidate{}, nil, nil
	}
	titles := make([]string, len(candidates))
	for i, candidate := range candidates {
		titles[i] = candidate.Title
		questions[fmt.Sprintf("title_%d", i+1)] = map[string]any{
			"type":         "noul",
			"instructions": "How likely is " + strconv.Quote(candidate.Title) + " to be the best title for this sermon? Compare the five candidates using the transcript. Prefer an explicitly announced title; otherwise prefer a verbatim quote that best represents the sermon's central message.",
			"criteria": map[string]string{
				"true":  "This is the speaker's explicitly announced title, or, when no title was announced, the best representative quote for the sermon's central message.",
				"false": "This is not a verbatim quote, is an incidental remark, misrepresents the central message, or is less suitable than another candidate.",
			},
		}
	}
	if includeTopics {
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
	}
	body, err := json.Marshal(map[string]any{
		"model":     h.config.TopicModel,
		"state":     map[string]any{"transcript": transcript, "title_candidates": titles},
		"questions": questions,
	})
	if err != nil {
		return titleCandidate{}, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openRouterDecisionsURL, bytes.NewReader(body))
	if err != nil {
		return titleCandidate{}, nil, err
	}
	setAIHeaders(req, h.config.APIKey, "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return titleCandidate{}, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return titleCandidate{}, nil, err
	}
	if resp.StatusCode/100 != 2 {
		return titleCandidate{}, nil, apiError(resp.StatusCode, data)
	}
	var response struct {
		Answers map[string]struct {
			Noul *float64 `json:"noul"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return titleCandidate{}, nil, fmt.Errorf("decode decision response: %w", err)
	}
	for key := range questions {
		answer := response.Answers[key]
		if answer.Noul == nil || *answer.Noul < 0 || *answer.Noul > 1 {
			return titleCandidate{}, nil, fmt.Errorf("decision response missing or invalid score for %s", key)
		}
	}
	best, bestScore := 0, -1.0
	for i := range candidates {
		key := fmt.Sprintf("title_%d", i+1)
		answer := response.Answers[key]
		// Keep extraction order as the deterministic tie-breaker.
		if *answer.Noul > bestScore {
			best, bestScore = i, *answer.Noul
		}
	}
	scores := make(map[string]float64, len(response.Answers))
	if includeTopics {
		for name := range topicDetails() {
			key := strings.ToLower(strings.ReplaceAll(name, " ", "_"))
			scores[name] = *response.Answers[key].Noul
		}
	}
	if len(candidates) == 0 {
		return titleCandidate{}, scores, nil
	}
	return candidates[best], scores, nil
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

func metadataPrompt(transcript, jobType string) string {
	prompt := "You are analyzing a sermon transcript. Return only valid JSON.\n\nExtract:\n"
	if jobType == "extract_scriptures" {
		return prompt + scriptureExtractionInstructions() + "\n\nTranscript:\n" + transcript
	}
	if jobType != "extract_topics" {
		prompt += `- title_candidates: exactly five distinct candidate titles, each an object with:
  - title: an exact quote from the speaker that works as a sermon title; do not compose or paraphrase
  - title_generated: false only when the speaker explicitly announced this title
  - title_reasoning: briefly explain why this quote is a suitable title
  Include the explicitly announced title if present, then quotes that best represent the central message.
  A separate decision model will choose the most likely title from these five candidates.
`
	}
	if jobType != "extract_title" && jobType != "extract_topics" {
		prompt += `- speaker: the preacher's name, or an empty string
`
		prompt += scriptureExtractionInstructions()
	}
	if jobType != "extract_title" {
		prompt += "- topics: 2-5 labels from the taxonomy below\n\nTopic taxonomy:\n" + topicsTaxonomy
	}
	return prompt + "\n\nTranscript:\n" + transcript
}

func scriptureExtractionInstructions() string {
	return `- old_testament_reading: the identifiable formal Old Testament passage read at the opening of this sermon, if stated in the transcript; otherwise an empty string.
- new_testament_reading: the identifiable formal New Testament passage read at the opening of this sermon, if stated in the transcript; otherwise an empty string.
- scriptures: additional Scripture passages beyond those readings, with no more than six total references across the two readings and this list (up to four additional passages when both readings are present, or up to six when neither is present). Include only passages substantively developed and important to the sermon's main reasoning or application. Prioritize the governing text and the most central supporting passages. Do not include every chapter or passing citation: omit brief quotations, rapid supporting citations, incidental allusions, and uncertain references. These are optional candidates, not selected references.
	Use full Bible book names and always include verse numbers. Use standard Book C:V or Book C:V1-V2 references; never return a bare chapter. If the sermon treats a whole chapter as a primary passage, give its full verse range from verse 1 through the chapter's last verse (for example, Romans 8:1-39); do not shorten it to just the book and chapter. Do not invent a narrower verse range when the transcript does not support it.
  Deduplicate and combine overlapping or contiguous verses. Return normalized references as strings.
`
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

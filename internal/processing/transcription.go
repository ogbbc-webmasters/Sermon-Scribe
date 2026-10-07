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
	"strconv"
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
			Parameters: job.Parameters,
		},
	}, reporter.Progress(100, nil)
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
		TitleCandidates []titleCandidate `json:"title_candidates"`
		Speaker         string           `json:"speaker"`
		Scriptures      []string         `json:"scriptures"`
		Topics          []string         `json:"topics"`
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
		result.Scriptures = nil
	}
	result.Scriptures = normalizeScriptures(result.Scriptures)
	title, scores, classifications, err := h.classifyMetadata(ctx, *sermon.Transcript, result.TitleCandidates, job.Type != "extract_title" && job.Type != "extract_scriptures", result.Scriptures)
	if err != nil {
		return Result{}, err
	}
	switch job.Type {
	case "extract_title":
		err = h.store.SaveTitle(job.SermonID, title.Title, title.TitleGenerated, title.TitleReasoning)
	case "extract_topics":
		err = h.store.SaveTopics(job.SermonID, result.Topics, scores)
	case "extract_scriptures":
		err = h.store.SaveScriptures(job.SermonID, result.Scriptures, classifications)
	default:
		err = h.store.SaveMetadata(job.SermonID, title.Title, title.TitleGenerated, title.TitleReasoning, result.Speaker, result.Scriptures, result.Topics, scores, classifications)
	}
	if err != nil {
		return Result{}, err
	}
	return Result{}, reporter.Progress(100, nil)
}

func (h *MetadataHandler) classifyMetadata(ctx context.Context, transcript string, candidates []titleCandidate, includeTopics bool, scriptures []string) (titleCandidate, map[string]float64, map[string]string, error) {
	questions := make(map[string]any)
	if len(candidates) == 0 && !includeTopics && len(scriptures) == 0 {
		return titleCandidate{}, nil, map[string]string{}, nil
	}
	for i, reference := range scriptures {
		questions[fmt.Sprintf("scripture_%d", i+1)] = map[string]any{
			"type":         "choice",
			"instructions": "Classify the Bible reference " + strconv.Quote(reference) + " using only words actually spoken in the sermon transcript. Read requires evidence of a recognizable quotation of biblical wording from this specific passage, not merely discussion of it. Distinguish the speaker's commentary from quoted scripture. Announcing a reading or asking listeners to turn to a passage does not establish that it was read. Accept different Bible translations and minor transcription errors. A quotation may be recited from memory, need not name its reference, and may cover only part of a chapter or verse range. Do not borrow quoted words from a different passage or infer presence from related themes. Choose read over mentioned only when the quotation evidence is present.",
			"criteria": map[string]string{
				"read":      "The transcript contains an actual, recognizable quotation of biblical wording from this passage: the speaker reads it aloud or recites it. At least some verse text must be spoken. A reference alone, an announced intention to read, a paraphrase, a summary, an allusion, or a generic phrase is not sufficient.",
				"mentioned": "The speaker names this passage, asks listeners to turn to it, announces an intended reading, or clearly discusses, paraphrases, summarizes, or alludes to this specific passage, but no recognizable quotation of its biblical wording is actually spoken.",
				"absent":    "The transcript contains neither a recognizable quotation from this passage nor an identifiable reference or specific discussion of it. A shared theme or generic biblical phrase alone does not establish this passage's presence.",
			},
		}
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
		return titleCandidate{}, nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openRouterDecisionsURL, bytes.NewReader(body))
	if err != nil {
		return titleCandidate{}, nil, nil, err
	}
	setAIHeaders(req, h.config.APIKey, "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return titleCandidate{}, nil, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return titleCandidate{}, nil, nil, err
	}
	if resp.StatusCode/100 != 2 {
		return titleCandidate{}, nil, nil, apiError(resp.StatusCode, data)
	}
	var response struct {
		Answers map[string]struct {
			Noul   *float64 `json:"noul"`
			Choice string   `json:"choice"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return titleCandidate{}, nil, nil, fmt.Errorf("decode decision response: %w", err)
	}
	classifications := make(map[string]string, len(scriptures))
	for i, reference := range scriptures {
		key := fmt.Sprintf("scripture_%d", i+1)
		choice := response.Answers[key].Choice
		if choice != "read" && choice != "mentioned" && choice != "absent" {
			return titleCandidate{}, nil, nil, fmt.Errorf("decision response missing or invalid classification for %s", key)
		}
		classifications[reference] = choice
		delete(questions, key)
	}
	for key := range questions {
		answer := response.Answers[key]
		if answer.Noul == nil || *answer.Noul < 0 || *answer.Noul > 1 {
			return titleCandidate{}, nil, nil, fmt.Errorf("decision response missing or invalid score for %s", key)
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
		return titleCandidate{}, scores, classifications, nil
	}
	return candidates[best], scores, classifications, nil
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
		return prompt + "- scriptures: normalized Bible references, deduplicated and kept in first-mention order; return an empty array if none are present\n\nTranscript:\n" + transcript
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
- scriptures: normalized Bible references, deduplicated and kept in first-mention order
`
	}
	if jobType != "extract_title" {
		prompt += "- topics: 2-5 labels from the taxonomy below\n\nTopic taxonomy:\n" + topicsTaxonomy
	}
	return prompt + "\n\nTranscript:\n" + transcript
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

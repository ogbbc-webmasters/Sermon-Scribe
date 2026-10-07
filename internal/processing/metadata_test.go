package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

type metadataTransport func(*http.Request) (*http.Response, error)

func (f metadataTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestMetadataModelDefaults(t *testing.T) {
	h := NewMetadataHandler(nil, AIConfig{})
	if h.config.MetadataModel != "openai/gpt-6-luna" || h.config.TopicModel != "typesafe/jev-1.13" {
		t.Fatalf("unexpected models: extraction=%q decision=%q", h.config.MetadataModel, h.config.TopicModel)
	}
	h = NewMetadataHandler(nil, AIConfig{MetadataModel: "custom-extractor", TopicModel: "custom-decision"})
	if h.config.MetadataModel != "custom-extractor" || h.config.TopicModel != "custom-decision" {
		t.Fatal("explicit model configuration was ignored")
	}
}

func TestTranscriptionRetryChainsMetadataAndRetainsReturnState(t *testing.T) {
	st := processingTestStore(t)
	if err := st.CreateSermon(store.Sermon{ID: "transcription-retry", OriginalFilename: "source.mp3", UploadedAt: "2026-10-06T00:00:00Z", Stage: "transcription", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	uploads := t.TempDir()
	dir := filepath.Join(uploads, "transcription-retry")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "original.mp3"), []byte("source audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := NewTranscriptionHandler(st, uploads, AIConfig{APIKey: "test-key"})
	h.client.Transport = metadataTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != openRouterURL+"/audio/transcriptions" {
			t.Fatalf("unexpected URL %s", req.URL)
		}
		if err := req.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		defer req.MultipartForm.RemoveAll()
		if got := req.FormValue("model"); got != "microsoft/mai-transcribe-2" {
			t.Fatalf("unexpected transcription model %q", got)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"text":"New transcript"}`)), Header: make(http.Header)}, nil
	})
	const parameters = `{"return_stage":"normalization","return_status":"done"}`
	result, err := h.Run(context.Background(), store.Job{SermonID: "transcription-retry", Parameters: parameters}, &recordingReporter{})
	if err != nil {
		t.Fatal(err)
	}
	sm, err := st.GetSermon("transcription-retry")
	if err != nil {
		t.Fatal(err)
	}
	if sm.Transcript == nil || *sm.Transcript != "New transcript" || result.Next == nil || result.Next.Type != "extract_metadata" || result.Next.Stage != "metadata" || result.Next.Parameters != parameters {
		t.Fatalf("incorrect transcription retry result: %+v, %+v", sm, result)
	}
}

func TestMetadataHandlerSelectsTitleAndPreservesTopics(t *testing.T) {
	const transcript = "My subject is God gives generously. The gift of wisdom. Faith asks. Ask without doubting. Receive wisdom."
	tests := []struct {
		name            string
		candidates      string
		answers         string
		scriptureAnswer string
		wrapped         bool
		status          int
		wantTitle       string
		wantReason      string
		generated       bool
		wantError       string
	}{
		{name: "highest score is third", wantTitle: "God Gives Generously", wantReason: "Explicitly announced subject."},
		{name: "array wrapped metadata", wrapped: true, wantTitle: "God Gives Generously", wantReason: "Explicitly announced subject."},
		{name: "last candidate at one", answers: `{"title_1":{"noul":0.1},"title_2":{"noul":0.6},"title_3":{"noul":0.97},"title_4":{"noul":0.2},"title_5":{"noul":1}}`, wantTitle: "Receive Wisdom", wantReason: "Closing exhortation.", generated: true},
		{name: "ties keep extraction order", answers: `{"title_1":{"noul":0.1},"title_2":{"noul":0.97},"title_3":{"noul":0.97},"title_4":{"noul":0.2},"title_5":{"noul":0.7}}`, wantTitle: "Faith Asks", wantReason: "Describes faithful prayer.", generated: true},
		{name: "zero is a valid score", answers: `{"title_1":{"noul":0},"title_2":{"noul":0},"title_3":{"noul":0},"title_4":{"noul":0},"title_5":{"noul":0}}`, wantTitle: "The Gift of Wisdom", wantReason: "Describes God's gift.", generated: true},
		{name: "too few candidates", candidates: `[ {"title":"One"}, {"title":"Two"}, {"title":"Three"}, {"title":"Four"} ]`, wantError: "exactly five"},
		{name: "too many candidates", candidates: `[ {"title":"One"}, {"title":"Two"}, {"title":"Three"}, {"title":"Four"}, {"title":"Five"}, {"title":"Six"} ]`, wantError: "exactly five"},
		{name: "empty candidate", candidates: `[ {"title":"One"}, {"title":"Two"}, {"title":"Three"}, {"title":"Four"}, {"title":"   "} ]`, wantError: "distinct non-empty"},
		{name: "duplicate ignoring case and whitespace", candidates: `[ {"title":"One"}, {"title":"Two"}, {"title":"Three"}, {"title":"Four"}, {"title":" ONE "} ]`, wantError: "distinct non-empty"},
		{name: "missing last score", answers: `{"title_1":{"noul":0.1},"title_2":{"noul":0.6},"title_3":{"noul":0.97},"title_4":{"noul":0.2}}`, wantError: "score for title_5"},
		{name: "null last score", answers: `{"title_1":{"noul":0.1},"title_2":{"noul":0.6},"title_3":{"noul":0.97},"title_4":{"noul":0.2},"title_5":{"noul":null}}`, wantError: "score for title_5"},
		{name: "absent probability", answers: `{"title_1":{"noul":0.1},"title_2":{"noul":0.6},"title_3":{"noul":0.97},"title_4":{"noul":0.2},"title_5":{}}`, wantError: "score for title_5"},
		{name: "score above one", answers: `{"title_1":{"noul":0.1},"title_2":{"noul":0.6},"title_3":{"noul":0.97},"title_4":{"noul":0.2},"title_5":{"noul":1.01}}`, wantError: "score for title_5"},
		{name: "negative score", answers: `{"title_1":{"noul":0.1},"title_2":{"noul":0.6},"title_3":{"noul":0.97},"title_4":{"noul":0.2},"title_5":{"noul":-0.01}}`, wantError: "score for title_5"},
		{name: "decision API error", status: 502, wantError: "HTTP 502"},
		{name: "missing scripture choice", scriptureAnswer: `{}`, wantError: "classification for scripture_2"},
		{name: "null scripture choice", scriptureAnswer: `{"choice":null}`, wantError: "classification for scripture_2"},
		{name: "unknown scripture choice", scriptureAnswer: `{"choice":"quoted"}`, wantError: "classification for scripture_2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := processingTestStore(t)
			if err := st.CreateSermon(store.Sermon{ID: "metadata-test", OriginalFilename: "test.mp3", UploadedAt: "2026-10-06T00:00:00Z", Stage: "metadata", Status: "running"}); err != nil {
				t.Fatal(err)
			}
			if err := st.SaveTranscript("metadata-test", transcript); err != nil {
				t.Fatal(err)
			}
			if err := st.SaveMetadata("metadata-test", "Existing title", true, "Existing reason", "Existing speaker", []string{"James 1:1"}, []string{"Assurance"}, map[string]float64{"Assurance": 0.3}, nil); err != nil {
				t.Fatal(err)
			}
			h := NewMetadataHandler(st, AIConfig{APIKey: "test-key", MetadataModel: "extractor-test", TopicModel: "decision-test"})
			calls := 0
			h.client.Transport = metadataTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != http.MethodPost || req.Header.Get("Authorization") != "Bearer test-key" {
					t.Fatal("unexpected request method or authorization")
				}
				var body string
				status := 200
				switch req.URL.String() {
				case openRouterURL + "/chat/completions":
					var payload struct {
						Model    string                     `json:"model"`
						Messages []struct{ Content string } `json:"messages"`
					}
					if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					if payload.Model != "extractor-test" || len(payload.Messages) != 1 || !strings.Contains(payload.Messages[0].Content, "exactly five distinct") || !strings.Contains(payload.Messages[0].Content, transcript) || strings.Contains(payload.Messages[0].Content, "topics_reasoning") {
						t.Fatal("incorrect extraction model or prompt")
					}
					candidates := tt.candidates
					if candidates == "" {
						candidates = `[
							{"title":"The gift of wisdom","title_generated":true,"title_reasoning":"Describes God's gift."},
							{"title":"Faith asks","title_generated":true,"title_reasoning":"Describes faithful prayer."},
							{"title":"God gives generously","title_generated":false,"title_reasoning":"Explicitly announced subject."},
							{"title":"Ask without doubting","title_generated":true,"title_reasoning":"A practical instruction."},
							{"title":"Receive wisdom","title_generated":true,"title_reasoning":"Closing exhortation."}
						]`
					}
					metadata := `{"title_candidates":` + candidates + `,"speaker":"John Doe","scriptures":["James 1","James 1:5","James 1:6","James 1:5-6","John 3:16"],"topics":["Assurance","Atonement"],"topics_reasoning":{"Assurance":"Legacy reasoning must be ignored."}}`
					if tt.wrapped {
						metadata = "[" + metadata + "]"
					}
					content, err := json.Marshal(metadata)
					if err != nil {
						t.Fatal(err)
					}
					body = `{"choices":[{"message":{"content":` + string(content) + `}}]}`
				case openRouterDecisionsURL:
					var payload struct {
						Model string `json:"model"`
						State struct {
							Transcript string   `json:"transcript"`
							Titles     []string `json:"title_candidates"`
						} `json:"state"`
						Questions map[string]struct {
							Type         string            `json:"type"`
							Instructions string            `json:"instructions"`
							Criteria     map[string]string `json:"criteria"`
						} `json:"questions"`
					}
					if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					wantTitles := []string{"The gift of wisdom", "Faith asks", "God gives generously", "Ask without doubting", "Receive wisdom"}
					if payload.Model != "decision-test" || payload.State.Transcript != transcript || !reflect.DeepEqual(payload.State.Titles, wantTitles) || len(payload.Questions) != 86 {
						t.Fatal("incorrect decision model, state, or question count")
					}
					for i, title := range wantTitles {
						question := payload.Questions[fmt.Sprintf("title_%d", i+1)]
						if question.Type != "noul" || !strings.Contains(question.Instructions, title) || !strings.Contains(question.Instructions, "explicitly announced") {
							t.Fatalf("incorrect question for candidate %d", i+1)
						}
					}
					if payload.Questions["assurance"].Type != "noul" || payload.Questions["atonement"].Type != "noul" {
						t.Fatal("topic classification questions missing")
					}
					for i, reference := range []string{"James 1:5-6", "John 3:16"} {
						question := payload.Questions[fmt.Sprintf("scripture_%d", i+1)]
						if question.Type != "choice" || !strings.Contains(question.Instructions, reference) || len(question.Criteria) != 3 || question.Criteria["read"] == "" || question.Criteria["mentioned"] == "" || question.Criteria["absent"] == "" {
							t.Fatalf("incorrect scripture question for %s", reference)
						}
						if !strings.Contains(question.Instructions, "words actually spoken") || !strings.Contains(question.Instructions, "different passage") || !strings.Contains(question.Criteria["read"], "recognizable quotation") || !strings.Contains(question.Criteria["read"], "paraphrase") || !strings.Contains(question.Criteria["mentioned"], "intended reading") || !strings.Contains(question.Criteria["absent"], "shared theme") {
							t.Fatal("scripture criteria lost quotation evidence or read/mentioned/absent boundaries")
						}
					}
					answers := tt.answers
					if answers == "" {
						answers = `{"title_1":{"noul":0.1},"title_2":{"noul":0.6},"title_3":{"noul":0.97},"title_4":{"noul":0.2},"title_5":{"noul":0.7}}`
					}
					var scores map[string]any
					if err := json.Unmarshal([]byte(answers), &scores); err != nil {
						t.Fatal(err)
					}
					for name := range topicDetails() {
						key := strings.ToLower(strings.ReplaceAll(name, " ", "_"))
						scores[key] = map[string]float64{"noul": 0.1}
					}
					scores["assurance"] = map[string]float64{"noul": 0.81}
					scores["atonement"] = map[string]float64{"noul": 0.23}
					scores["scripture_1"] = map[string]string{"choice": "read"}
					scores["scripture_2"] = map[string]string{"choice": "absent"}
					if tt.scriptureAnswer != "" {
						var answer any
						if err := json.Unmarshal([]byte(tt.scriptureAnswer), &answer); err != nil {
							t.Fatal(err)
						}
						scores["scripture_2"] = answer
					}
					encoded, err := json.Marshal(map[string]any{"answers": scores})
					if err != nil {
						t.Fatal(err)
					}
					body = string(encoded)
					if tt.status != 0 {
						status = tt.status
					}
				default:
					t.Fatalf("unexpected URL %s", req.URL)
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			reporter := &recordingReporter{}
			_, err := h.Run(context.Background(), store.Job{SermonID: "metadata-test"}, reporter)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("error = %v, want %q", err, tt.wantError)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			sm, err := st.GetSermon("metadata-test")
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantError != "" {
				if sm.Title == nil || *sm.Title != "Existing Title" || sm.TitleReasoning == nil || *sm.TitleReasoning != "Existing reason" || sm.TopicScores["Assurance"] != 0.3 || len(reporter.progress) != 0 {
					t.Fatal("failed classification overwrote metadata or reported completion")
				}
				if tt.candidates != "" && calls != 1 {
					t.Fatal("invalid candidates reached the decision model")
				}
				return
			}
			if calls != 2 || !reflect.DeepEqual(reporter.progress, []int{100}) {
				t.Fatalf("calls = %d, progress = %v", calls, reporter.progress)
			}
			if sm.Title == nil || *sm.Title != tt.wantTitle || sm.TitleGenerated == nil || *sm.TitleGenerated != tt.generated || sm.TitleReasoning == nil || *sm.TitleReasoning != tt.wantReason {
				t.Fatalf("incorrect winning title metadata: %+v", sm)
			}
			if sm.Speaker == nil || *sm.Speaker != "John Doe" || !reflect.DeepEqual(sm.Scriptures, []string{"James 1:5-6", "John 3:16"}) || !reflect.DeepEqual(sm.ScriptureClassifications, map[string]string{"James 1:5-6": "read", "John 3:16": "absent"}) || !reflect.DeepEqual(sm.Topics, []string{"Assurance", "Atonement"}) || len(sm.TopicScores) != 79 || sm.TopicScores["Assurance"] != 0.81 || sm.TopicScores["Atonement"] != 0.23 {
				t.Fatalf("incorrect other metadata: %+v", sm)
			}
			encoded, err := json.Marshal(sm)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "topics_reasoning") {
				t.Fatal("topic reasoning still exposed by the API")
			}
		})
	}
}

func TestScriptureRegenerationPreservesOtherFields(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprintf("empty=%v", empty), func(t *testing.T) {
			st := processingTestStore(t)
			if err := st.CreateSermon(store.Sermon{ID: "scriptures", OriginalFilename: "source.mp3", UploadedAt: "2026-10-06T00:00:00Z", Stage: "scriptures", Status: "running"}); err != nil {
				t.Fatal(err)
			}
			if err := st.SaveTranscript("scriptures", "Existing transcript"); err != nil {
				t.Fatal(err)
			}
			if err := st.SaveMetadata("scriptures", "Existing title", true, "Existing reason", "Existing speaker", []string{"Old reference"}, []string{"Assurance"}, map[string]float64{"Assurance": 0.9}, map[string]string{"Old reference": "read"}); err != nil {
				t.Fatal(err)
			}
			h := NewMetadataHandler(st, AIConfig{APIKey: "test-key"})
			calls := 0
			h.client.Transport = metadataTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				body := ""
				if req.URL.String() == openRouterURL+"/chat/completions" {
					var payload struct {
						Messages []struct{ Content string } `json:"messages"`
					}
					if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					prompt := payload.Messages[0].Content
					if !strings.Contains(prompt, "- scriptures:") || strings.Contains(prompt, "title_candidates") || strings.Contains(prompt, "- topics:") || strings.Contains(prompt, "- speaker:") {
						t.Fatal("scripture regeneration requested unrelated fields")
					}
					metadata := `{"scriptures":["Romans 8","Romans 8:28","James 1:5","James 1:5","John 3:16"]}`
					if empty {
						metadata = `{"scriptures":[]}`
					}
					content, _ := json.Marshal(metadata)
					body = `{"choices":[{"message":{"content":` + string(content) + `}}]}`
				} else if req.URL.String() == openRouterDecisionsURL {
					var payload struct {
						Questions map[string]struct{ Type string } `json:"questions"`
					}
					if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					if empty || len(payload.Questions) != 3 {
						t.Fatal("incorrect scripture question count")
					}
					for _, question := range payload.Questions {
						if question.Type != "choice" {
							t.Fatal("scripture-only regeneration scored unrelated metadata")
						}
					}
					body = `{"answers":{"scripture_1":{"choice":"mentioned"},"scripture_2":{"choice":"read"},"scripture_3":{"choice":"absent"}}}`
				} else {
					t.Fatalf("unexpected URL %s", req.URL)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			if _, err := h.Run(context.Background(), store.Job{SermonID: "scriptures", Type: "extract_scriptures"}, &recordingReporter{}); err != nil {
				t.Fatal(err)
			}
			sm, err := st.GetSermon("scriptures")
			if err != nil {
				t.Fatal(err)
			}
			if *sm.Title != "Existing Title" || !*sm.TitleGenerated || *sm.TitleReasoning != "Existing reason" || *sm.Speaker != "Existing speaker" || *sm.Transcript != "Existing transcript" || !reflect.DeepEqual(sm.Topics, []string{"Assurance"}) || !reflect.DeepEqual(sm.TopicScores, map[string]float64{"Assurance": 0.9}) {
				t.Fatalf("scripture regeneration changed unrelated metadata: %+v", sm)
			}
			if empty {
				if calls != 1 || len(sm.Scriptures) != 0 || len(sm.ScriptureClassifications) != 0 {
					t.Fatalf("empty extraction left stale scriptures: %+v", sm)
				}
			} else if calls != 2 || !reflect.DeepEqual(sm.Scriptures, []string{"Romans 8:28", "James 1:5", "John 3:16"}) || !reflect.DeepEqual(sm.ScriptureClassifications, map[string]string{"Romans 8:28": "mentioned", "James 1:5": "read", "John 3:16": "absent"}) {
				t.Fatalf("incorrect regenerated scriptures: %+v", sm)
			}
		})
	}
}

func TestMetadataTargetedRetryPreservesOtherFields(t *testing.T) {
	for _, jobType := range []string{"extract_title", "extract_topics"} {
		t.Run(jobType, func(t *testing.T) {
			st := processingTestStore(t)
			if err := st.CreateSermon(store.Sermon{ID: "targeted", OriginalFilename: "source.mp3", UploadedAt: "2026-10-06T00:00:00Z", Stage: "metadata", Status: "done"}); err != nil {
				t.Fatal(err)
			}
			if err := st.SaveTranscript("targeted", "One. Two. Three. Four. Five."); err != nil {
				t.Fatal(err)
			}
			if err := st.SaveMetadata("targeted", "Existing title", true, "Existing reason", "Existing speaker", []string{"James 1:1"}, []string{"Assurance"}, map[string]float64{"Assurance": 0.3}, map[string]string{"James 1:1": "mentioned"}); err != nil {
				t.Fatal(err)
			}
			h := NewMetadataHandler(st, AIConfig{APIKey: "test-key"})
			calls := 0
			h.client.Transport = metadataTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				var body string
				if req.URL.String() == openRouterURL+"/chat/completions" {
					var payload struct {
						Messages []struct{ Content string } `json:"messages"`
					}
					if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					prompt := payload.Messages[0].Content
					if strings.Contains(prompt, "- speaker:") || strings.Contains(prompt, "- scriptures:") || strings.Contains(prompt, "topics_reasoning") || strings.Contains(prompt, "title_candidates:") != (jobType == "extract_title") || strings.Contains(prompt, "- topics:") != (jobType == "extract_topics") {
						t.Fatal("targeted prompt requested unrelated metadata")
					}
					metadata := `{"title_candidates":[{"title":"One","title_generated":true},{"title":"Two","title_generated":true},{"title":"Three","title_generated":false,"title_reasoning":"New reason"},{"title":"Four","title_generated":true},{"title":"Five","title_generated":true}],"speaker":"Do not save","scriptures":["Do not save"],"topics":["Atonement","Assurance"]}`
					if jobType == "extract_topics" {
						// Topics retries must work without title candidates.
						metadata = `{"topics":["Atonement","Assurance"]}`
					}
					content, err := json.Marshal(metadata)
					if err != nil {
						t.Fatal(err)
					}
					body = `{"choices":[{"message":{"content":` + string(content) + `}}]}`
				} else if req.URL.String() == openRouterDecisionsURL {
					var payload struct {
						Questions map[string]any `json:"questions"`
					}
					if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					wantCount := 5
					if jobType == "extract_topics" {
						wantCount = 79
					}
					if len(payload.Questions) != wantCount {
						t.Fatalf("questions = %d, want %d", len(payload.Questions), wantCount)
					}
					answers := make(map[string]any)
					for key := range payload.Questions {
						answers[key] = map[string]float64{"noul": 0.2}
					}
					if jobType == "extract_title" {
						answers["title_3"] = map[string]float64{"noul": 0.95}
					} else {
						answers["assurance"] = map[string]float64{"noul": 0.83}
					}
					data, err := json.Marshal(map[string]any{"answers": answers})
					if err != nil {
						t.Fatal(err)
					}
					body = string(data)
				} else {
					t.Fatalf("unexpected URL %s", req.URL)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			if _, err := h.Run(context.Background(), store.Job{SermonID: "targeted", Type: jobType}, &recordingReporter{}); err != nil {
				t.Fatal(err)
			}
			sm, err := st.GetSermon("targeted")
			if err != nil {
				t.Fatal(err)
			}
			if calls != 2 || sm.Transcript == nil || *sm.Transcript != "One. Two. Three. Four. Five." || sm.Speaker == nil || *sm.Speaker != "Existing speaker" || !reflect.DeepEqual(sm.Scriptures, []string{"James 1:1"}) || sm.ScriptureClassifications["James 1:1"] != "mentioned" {
				t.Fatalf("targeted retry changed unrelated fields: %+v", sm)
			}
			if jobType == "extract_title" {
				if *sm.Title != "Three" || *sm.TitleGenerated || *sm.TitleReasoning != "New reason" || !reflect.DeepEqual(sm.Topics, []string{"Assurance"}) || !reflect.DeepEqual(sm.TopicScores, map[string]float64{"Assurance": 0.3}) {
					t.Fatalf("incorrect title-only update: %+v", sm)
				}
			} else if *sm.Title != "Existing Title" || !*sm.TitleGenerated || *sm.TitleReasoning != "Existing reason" || !reflect.DeepEqual(sm.Topics, []string{"Atonement", "Assurance"}) || len(sm.TopicScores) != 79 || sm.TopicScores["Assurance"] != 0.83 || sm.TopicScores["Atonement"] != 0.2 {
				t.Fatalf("incorrect topics-only update: %+v", sm)
			}
		})
	}
}

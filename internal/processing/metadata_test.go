package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

type metadataTransport func(*http.Request) (*http.Response, error)

func (f metadataTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestMetadataHandlerSelectsTitleAndPreservesTopics(t *testing.T) {
	const transcript = "My subject is God gives generously. The gift of wisdom. Faith asks. Ask without doubting. Receive wisdom."
	tests := []struct {
		name       string
		candidates string
		answers    string
		wrapped    bool
		status     int
		wantTitle  string
		wantReason string
		generated  bool
		wantError  string
	}{
		{name: "highest score is third", wantTitle: "God gives generously", wantReason: "Explicitly announced subject."},
		{name: "array wrapped metadata", wrapped: true, wantTitle: "God gives generously", wantReason: "Explicitly announced subject."},
		{name: "last candidate at one", answers: `{"title_1":{"noul":0.1},"title_2":{"noul":0.6},"title_3":{"noul":0.97},"title_4":{"noul":0.2},"title_5":{"noul":1}}`, wantTitle: "Receive wisdom", wantReason: "Closing exhortation.", generated: true},
		{name: "ties keep extraction order", answers: `{"title_1":{"noul":0.1},"title_2":{"noul":0.97},"title_3":{"noul":0.97},"title_4":{"noul":0.2},"title_5":{"noul":0.7}}`, wantTitle: "Faith asks", wantReason: "Describes faithful prayer.", generated: true},
		{name: "zero is a valid score", answers: `{"title_1":{"noul":0},"title_2":{"noul":0},"title_3":{"noul":0},"title_4":{"noul":0},"title_5":{"noul":0}}`, wantTitle: "The gift of wisdom", wantReason: "Describes God's gift.", generated: true},
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
			if err := st.SaveMetadata("metadata-test", "Existing title", true, "Existing reason", "Existing speaker", []string{"James 1:1"}, []string{"Assurance"}, map[string]float64{"Assurance": 0.3}); err != nil {
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
					metadata := `{"title_candidates":` + candidates + `,"speaker":"John Doe","scriptures":["James 1:5","James 1:6"],"topics":["Assurance","Atonement"],"topics_reasoning":{"Assurance":"Legacy reasoning must be ignored."}}`
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
							Type         string `json:"type"`
							Instructions string `json:"instructions"`
						} `json:"questions"`
					}
					if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					wantTitles := []string{"The gift of wisdom", "Faith asks", "God gives generously", "Ask without doubting", "Receive wisdom"}
					if payload.Model != "decision-test" || payload.State.Transcript != transcript || !reflect.DeepEqual(payload.State.Titles, wantTitles) || len(payload.Questions) != 84 {
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
					answers := tt.answers
					if answers == "" {
						answers = `{"title_1":{"noul":0.1},"title_2":{"noul":0.6},"title_3":{"noul":0.97},"title_4":{"noul":0.2},"title_5":{"noul":0.7}}`
					}
					body = `{"answers":` + strings.TrimSuffix(answers, "}") + `,"assurance":{"noul":0.81},"atonement":{"noul":0.23}}}`
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
				if sm.Title == nil || *sm.Title != "Existing title" || sm.TitleReasoning == nil || *sm.TitleReasoning != "Existing reason" || sm.TopicScores["Assurance"] != 0.3 || len(reporter.progress) != 0 {
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
			if sm.Speaker == nil || *sm.Speaker != "John Doe" || !reflect.DeepEqual(sm.Scriptures, []string{"James 1:5", "James 1:6"}) || !reflect.DeepEqual(sm.Topics, []string{"Assurance", "Atonement"}) || !reflect.DeepEqual(sm.TopicScores, map[string]float64{"Assurance": 0.81, "Atonement": 0.23}) {
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

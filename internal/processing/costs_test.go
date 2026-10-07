package processing

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

func TestAICallsAccumulateAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "costs.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { st.Close() }()
	for _, id := range []string{"sermon", "other"} {
		if err := st.CreateSermon(store.Sermon{ID: id, OriginalFilename: "audio.mp3", UploadedAt: "2026-10-07", Stage: "metadata", Status: "done"}); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		task, model, body string
		status            int
		failure           bool
	}{
		{"transcribe", "audio/model", `{"usage":{"cost":0.12345}}`, 200, false},
		{"extract_title", "requested/model", `{"id":"first","model":"actual/model","usage":{"cost":0.0023},"choices":[]}`, 200, false},
		// Same job and model is another billed call, not an upsert.
		{"extract_title", "actual/model", `{"id":"second","usage":{"cost":0.0041}}`, 200, false},
		{"select_title", "decision/model", `{"usage":{"cost":0.0007},"error":{"message":"failed"}}`, 502, false},
		{"extract_title", "actual/model", `{"usage":{"cost":0}}`, 200, false},
		{"extract_title", "actual/model", `{}`, 200, false},
		{"extract_title", "actual/model", `broken JSON`, 200, false},
		{"extract_title", "actual/model", "", 0, true},
	}
	for _, tc := range cases {
		client := &http.Client{Transport: metadataTransport(func(*http.Request) (*http.Response, error) {
			if tc.failure {
				return nil, errors.New("network failure")
			}
			return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}
		req, _ := http.NewRequestWithContext(context.Background(), "POST", "https://example.test", nil)
		resp, err := doAIRequest(st, client, req, store.Job{ID: "job", SermonID: "sermon"}, tc.task, tc.model)
		if (err != nil) != tc.failure {
			t.Fatalf("request error = %v", err)
		}
		if resp != nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) != tc.body || resp.StatusCode != tc.status {
				t.Fatal("response changed by cost tracking")
			}
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sm, err := st.GetSermon("sermon")
	if err != nil {
		t.Fatal(err)
	}
	if len(sm.AICosts) != 3 {
		t.Fatalf("groups = %+v", sm.AICosts)
	}
	var total float64
	for _, cost := range sm.AICosts {
		total += cost.CostUSD
		if cost.Task == "extract_title" && (cost.Model != "actual/model" || cost.Calls != 6 || cost.UnknownCosts != 3 || math.Abs(cost.CostUSD-0.0064) > 1e-12) {
			t.Fatalf("regeneration breakdown = %+v", cost)
		}
	}
	if math.Abs(total-0.13055) > 1e-12 {
		t.Fatalf("total = %v", total)
	}
	sermons, err := st.ListSermons()
	if err != nil {
		t.Fatal(err)
	}
	for _, sm := range sermons {
		if sm.ID == "other" && len(sm.AICosts) != 0 {
			t.Fatal("costs leaked to another sermon")
		}
	}
}

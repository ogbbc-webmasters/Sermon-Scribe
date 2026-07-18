package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/processing"
)

func readyTimelineSermon(t *testing.T, srv *Server, ts *httptest.Server) string {
	t.Helper()
	_, sm := uploadFile(t, ts, "timeline.wav", []byte("original"), nil)
	job, err := srv.Store.ClaimNextJob(context.Background(), []string{"normalize"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.CompleteJob(job, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(srv.UploadsDir, sm.ID)
	waveform := processing.Waveform{Duration: 2, SamplesPerSecond: 20, Samples: make([]float64, 40)}
	data, _ := json.Marshal(waveform)
	if err := os.WriteFile(filepath.Join(dir, "waveform.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"normalized.flac", "normalized.mp3", ".normalization-complete.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return sm.ID
}

func TestTimelineAnalyzeApplyAndApproval(t *testing.T) {
	srv, ts := newTestServer(t)
	id := readyTimelineSermon(t, srv, ts)
	resp, err := http.Post(ts.URL+"/api/sermons/"+id+"/analyze", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("analyze = %d: %s", resp.StatusCode, body)
	}
	var analysis struct {
		Duration float64             `json:"duration"`
		Regions  []processing.Region `json:"regions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&analysis); err != nil {
		t.Fatal(err)
	}
	if analysis.Duration != 2 || len(analysis.Regions) == 0 {
		t.Fatalf("analysis = %+v", analysis)
	}

	plan := `{"regions":[{"start":0,"end":1,"type":"speaking","keep":true},{"start":1,"end":2,"type":"silence","keep":false}]}`
	resp, err = http.Post(ts.URL+"/api/sermons/"+id+"/apply-edits", "application/json", strings.NewReader(plan))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("apply = %d", resp.StatusCode)
	}
	resp, err = http.Post(ts.URL+"/api/sermons/"+id+"/apply-edits", "application/json", strings.NewReader(plan))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("concurrent apply = %d", resp.StatusCode)
	}

	job, err := srv.Store.ClaimNextJob(context.Background(), []string{"apply_edits"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	regions, _ := json.Marshal([]processing.Region{{Start: 0, End: 1, Type: "speaking", Keep: true}, {Start: 1, End: 2, Type: "silence"}})
	if err := srv.Store.SetAppliedRegions(id, regions); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.CompleteJob(job, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(srv.UploadsDir, id)
	if err := os.WriteFile(filepath.Join(dir, "final.mp3"), []byte("final"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, err = http.Post(ts.URL+"/api/sermons/"+id+"/approve-edit", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("approve = %d: %s", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "final.mp3")); err != nil {
		t.Fatal("final audio was deleted")
	}
	if _, err := os.Stat(filepath.Join(dir, "waveform.json")); !os.IsNotExist(err) {
		t.Fatal("waveform was not deleted")
	}
	resp, err = http.Post(ts.URL+"/api/sermons/"+id+"/approve-edit", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("repeat approval = %d", resp.StatusCode)
	}
}

func TestApplyRejectsAllDeleteAndMalformedPlans(t *testing.T) {
	srv, ts := newTestServer(t)
	id := readyTimelineSermon(t, srv, ts)
	for _, body := range []string{`{"regions":[{"start":0,"end":2,"type":"silence","keep":false}]}`, `{"regions":[{"start":0,"end":1,"type":"speaking","keep":true}]}`, `{"regions":[],"extra":1}`} {
		resp, err := http.Post(ts.URL+"/api/sermons/"+id+"/apply-edits", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %s status = %d", body, resp.StatusCode)
		}
	}
}

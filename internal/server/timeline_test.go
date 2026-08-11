package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/processing"
	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
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
	events, unsubscribe := srv.Events.subscribe()
	defer unsubscribe()
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
	select {
	case event := <-events:
		approved := event.Data.(store.Sermon)
		if !approved.EditApproved {
			t.Fatal("approval event did not contain durable approved state")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for approval event")
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

func TestWaveformUnavailableWhileNormalizationPending(t *testing.T) {
	srv, ts := newTestServer(t)
	_, sm := uploadFile(t, ts, "pending.wav", []byte("original"), nil)
	dir := filepath.Join(srv.UploadsDir, sm.ID)
	waveform := processing.Waveform{Duration: 1, SamplesPerSecond: 20, Samples: make([]float64, 20)}
	data, _ := json.Marshal(waveform)
	if err := os.WriteFile(filepath.Join(dir, "waveform.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, endpoint := range []string{"waveform", "analyze", "apply-edits"} {
		method := "GET"
		if endpoint != "waveform" {
			method = "POST"
		}
		req, _ := http.NewRequest(method, ts.URL+"/api/sermons/"+sm.ID+"/"+endpoint, strings.NewReader(`{"regions":[]}`))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusConflict {
			t.Errorf("%s during pending normalization = %d, want 409", endpoint, resp.StatusCode)
		}
	}
}

func TestFinalAudioUnavailableDuringRepeatRender(t *testing.T) {
	srv, ts := newTestServer(t)
	id := readyTimelineSermon(t, srv, ts)
	dir := filepath.Join(srv.UploadsDir, id)
	if err := os.WriteFile(filepath.Join(dir, "final.mp3"), []byte("old final"), 0o644); err != nil {
		t.Fatal(err)
	}
	regions := []processing.Region{{Start: 0, End: 2, Type: "speaking", Keep: true}}
	encoded, _ := json.Marshal(regions)
	if err := srv.Store.SetAppliedRegions(id, encoded); err != nil {
		t.Fatal(err)
	}
	plan := `{"regions":[{"start":0,"end":2,"type":"speaking","keep":true}]}`
	resp, err := http.Post(ts.URL+"/api/sermons/"+id+"/apply-edits", "application/json", strings.NewReader(plan))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp, err = http.Get(ts.URL + "/api/sermons/" + id + "/audio/final")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("final audio during pending repeat render = %d, want 409", resp.StatusCode)
	}
}

func TestApprovalReconcilesStaleStaging(t *testing.T) {
	srv, ts := newTestServer(t)
	id := readyTimelineSermon(t, srv, ts)
	dir := filepath.Join(srv.UploadsDir, id)
	staged := filepath.Join(dir, ".approval-staged")
	if err := os.Mkdir(staged, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "waveform.json"), filepath.Join(staged, "waveform.json")); err != nil {
		t.Fatal(err)
	}
	if err := reconcileApprovalStaging(dir, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "waveform.json")); err != nil {
		t.Fatalf("staged source was not restored: %v", err)
	}
	if _, err := os.Stat(staged); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging directory remains: %v", err)
	}
}

func TestTimelineArtifactReconciliationAtStartup(t *testing.T) {
	srv, ts := newTestServer(t)
	id := readyTimelineSermon(t, srv, ts)
	dir := filepath.Join(srv.UploadsDir, id)
	staged := filepath.Join(dir, ".approval-staged")
	if err := os.Mkdir(staged, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "waveform.json"), filepath.Join(staged, "waveform.json")); err != nil {
		t.Fatal(err)
	}
	if err := srv.ReconcileTimelineArtifacts(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "waveform.json")); err != nil {
		t.Fatalf("unapproved staged source was not restored: %v", err)
	}

	regions, _ := json.Marshal([]processing.Region{{Start: 0, End: 2, Type: "speaking", Keep: true}})
	if err := srv.Store.SetAppliedRegions(id, regions); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.EnqueueApplyEdits(id, "approval-job", `{"duration":2,"regions":[{"start":0,"end":2,"type":"speaking","keep":true}]}`, time.Now()); err != nil {
		t.Fatal(err)
	}
	job, err := srv.Store.ClaimNextJob(context.Background(), []string{"apply_edits"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.CompleteJob(job, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "final.mp3"), []byte("final"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.ApproveEdit(id); err != nil {
		t.Fatal(err)
	}
	if err := srv.ReconcileTimelineArtifacts(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "normalized.flac")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("approved source remains after startup reconciliation: %v", err)
	}
}

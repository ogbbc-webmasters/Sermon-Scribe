package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

func TestExportHTTPQueuesThenDownloadsOnlyCompletedOwnJob(t *testing.T) {
	srv, ts := newTestServer(t)
	if err := srv.Store.CreateSermon(store.Sermon{ID: "sermon", Stage: "editing", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	d := store.Editing{Duration: 8, Breakpoints: []store.Breakpoint{{ID: "start", Kind: "start"}, {ID: "end", Time: 8, Kind: "end"}}, Sections: []store.Section{{ID: "section", Keep: true}}}
	if err := srv.Store.PrepareEditing("sermon", d, "text", store.TranscriptionMetadata{Duration: 8}); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(ts.URL+"/api/sermons/sermon/export", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var queued exportResponse
	if err := json.NewDecoder(resp.Body).Decode(&queued); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 202 || queued.JobID == "" || queued.State != "queued" {
		t.Fatalf("queued: %d %+v", resp.StatusCode, queued)
	}
	resp, err = http.Post(ts.URL+"/api/sermons/sermon/export", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 409 {
		t.Fatalf("duplicate export status: %d", resp.StatusCode)
	}
	url := ts.URL + "/api/sermons/sermon/exports/" + queued.JobID
	check := func(url string, want int) []byte {
		t.Helper()
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != want {
			t.Fatalf("GET %s: %d %s, %v", url, resp.StatusCode, body, err)
		}
		if want == 200 && resp.Header.Get("Content-Type") == "audio/mpeg" && resp.Header.Get("Content-Disposition") != `attachment; filename="sermon.mp3"` {
			t.Fatalf("download headers: %v", resp.Header)
		}
		return body
	}
	check(url+"?download=1", 409)
	check(ts.URL+"/api/sermons/other/exports/"+queued.JobID, 404)
	job, err := srv.Store.ClaimNextJob(context.Background(), []string{"export"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Exhausted exports retry the same durable job through the Export button.
	if _, err := srv.Store.FailJob(job, "failed encode", time.Now()); err != nil {
		t.Fatal(err)
	}
	resp, err = http.Post(ts.URL+"/api/sermons/sermon/export", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var retry exportResponse
	if err := json.NewDecoder(resp.Body).Decode(&retry); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 202 || retry.JobID != job.ID {
		t.Fatalf("retry: %d %+v", resp.StatusCode, retry)
	}
	job, err = srv.Store.ClaimNextJob(context.Background(), []string{"export"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(srv.UploadsDir, "sermon", "export-"+job.ID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "export.mp3"), []byte("export audio"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.CompleteJob(job, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	var done exportResponse
	if err := json.Unmarshal(check(url, 200), &done); err != nil {
		t.Fatal(err)
	}
	if done.State != "done" || done.Progress != 100 {
		t.Fatalf("completed: %+v", done)
	}
	if body := check(url+"?download=1", 200); string(body) != "export audio" {
		t.Fatalf("download: %s", body)
	}
}

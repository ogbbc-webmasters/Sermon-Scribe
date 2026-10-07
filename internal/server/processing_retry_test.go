package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

func TestRetryProcessingParts(t *testing.T) {
	for _, part := range []string{"title", "topics", "transcription", "scriptures"} {
		t.Run(part, func(t *testing.T) {
			srv, ts := newTestServer(t)
			notifier := &countingNotifier{}
			srv.Queue = notifier
			if err := srv.Store.CreateSermon(store.Sermon{ID: "ready", OriginalFilename: "source.mp3", UploadedAt: "2026-10-06T00:00:00Z", Stage: "normalization", Status: "done"}); err != nil {
				t.Fatal(err)
			}
			if err := srv.Store.SaveTranscript("ready", "Existing transcript"); err != nil {
				t.Fatal(err)
			}
			if err := srv.Store.SaveMetadata("ready", "Existing title", true, "Existing reason", "Existing speaker", []string{"James 1:1"}, []string{"Assurance"}, map[string]float64{"Assurance": 0.9}, nil); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(srv.UploadsDir, "ready")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "original.mp3"), []byte("audio"), 0o644); err != nil {
				t.Fatal(err)
			}
			url := ts.URL + "/api/sermons/ready/retry/" + part
			resp, err := http.Post(url, "application/json", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", resp.StatusCode)
			}
			var sm store.Sermon
			if err := json.NewDecoder(resp.Body).Decode(&sm); err != nil {
				t.Fatal(err)
			}
			if sm.Stage != part || sm.Status != "pending" || *sm.Title != "Existing Title" || *sm.Transcript != "Existing transcript" || sm.TopicScores["Assurance"] != 0.9 || notifier.calls.Load() != 1 {
				t.Fatalf("unexpected queued sermon: %+v", sm)
			}
			duplicate, err := http.Post(url, "application/json", nil)
			if err != nil {
				t.Fatal(err)
			}
			duplicate.Body.Close()
			if duplicate.StatusCode != http.StatusConflict || notifier.calls.Load() != 1 {
				t.Fatal("duplicate retry was not rejected")
			}
			jobType := map[string]string{"title": "extract_title", "topics": "extract_topics", "transcription": "transcribe", "scriptures": "extract_scriptures"}[part]
			job, err := srv.Store.ClaimNextJob(context.Background(), []string{jobType}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			// A failed rerun followed by another explicit retry must still
			// restore the original normalization state, not title/topics.
			if _, err := srv.Store.FailJob(job, "test failure", time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := srv.Store.EnqueueProcessingRerun("ready", "second-retry", part, time.Now()); err != nil {
				t.Fatal(err)
			}
			job, err = srv.Store.ClaimNextJob(context.Background(), []string{jobType}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if part == "transcription" {
				sm, err = srv.Store.CompleteJob(job, &store.NewJob{ID: "metadata-after-transcription", Type: "extract_metadata", Stage: "metadata", Parameters: job.Parameters}, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				if sm.Stage != "metadata" || sm.Status != "pending" {
					t.Fatalf("metadata not scheduled: %+v", sm)
				}
				job, err = srv.Store.ClaimNextJob(context.Background(), []string{"extract_metadata"}, time.Now())
				if err != nil {
					t.Fatal(err)
				}
			}
			sm, err = srv.Store.CompleteJob(job, nil, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if sm.Stage != "normalization" || sm.Status != "done" {
				t.Fatalf("original stage not restored: %+v", sm)
			}
			if _, err := srv.Store.ClaimNextJob(context.Background(), []string{jobType}, time.Now()); !errors.Is(err, store.ErrNoJob) {
				t.Fatalf("unexpected leftover job: %v", err)
			}
		})
	}
}

func TestRetryProcessingValidation(t *testing.T) {
	for _, tt := range []struct {
		name, id, part, status string
		want                   int
	}{
		{"unknown part", "ready", "anything", "done", 400},
		{"missing sermon", "missing", "title", "done", 404},
		{"missing transcription sermon", "missing", "transcription", "done", 404},
		{"missing transcript", "ready", "topics", "done", 409},
		{"missing scripture transcript", "ready", "scriptures", "done", 409},
		{"missing original audio", "ready", "transcription", "done", 409},
		{"running sermon", "ready", "transcription", "running", 409},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv, ts := newTestServer(t)
			if err := srv.Store.CreateSermon(store.Sermon{ID: "ready", OriginalFilename: "source.mp3", UploadedAt: "2026-10-06T00:00:00Z", Stage: "metadata", Status: tt.status}); err != nil {
				t.Fatal(err)
			}
			if tt.status == "running" {
				dir := filepath.Join(srv.UploadsDir, "ready")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "original.mp3"), []byte("audio"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			resp, err := http.Post(ts.URL+"/api/sermons/"+tt.id+"/retry/"+tt.part, "application/json", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
			if _, err := srv.Store.ClaimNextJob(context.Background(), []string{"extract_title", "extract_topics", "transcribe", "extract_scriptures"}, time.Now()); !errors.Is(err, store.ErrNoJob) {
				t.Fatalf("invalid request queued work: %v", err)
			}
		})
	}
}

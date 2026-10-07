package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

func TestPlaybackChoosesExistingAudioAfterMetadata(t *testing.T) {
	srv, ts := newTestServer(t)
	if err := srv.Store.CreateSermon(store.Sermon{ID: "playback", OriginalFilename: "source.mp3", UploadedAt: time.Now().Format(time.RFC3339Nano), Stage: "metadata", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(srv.UploadsDir, "playback")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"original.mp3", "normalized.mp3", "final.mp3"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		resp, err := http.Get(ts.URL + "/api/sermons/playback/audio/playback?download=1")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || string(body) != name || resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("playback after adding %s = %d %q, %v", name, resp.StatusCode, body, err)
		}
	}
}

func TestRetiredEditorRoutesCannotQueueJobs(t *testing.T) {
	srv, ts := newTestServer(t)
	_, sm := uploadFile(t, ts, "source.wav", []byte("source"), nil)
	before, err := srv.Store.GetCurrentJob(sm.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []struct{ method, path string }{
		{"GET", "waveform"}, {"POST", "analyze"}, {"POST", "apply-edits"},
		{"POST", "approve-edit"}, {"GET", "audio/section?start=0&end=1"},
	} {
		req, err := http.NewRequest(endpoint.method, ts.URL+"/api/sermons/"+sm.ID+"/"+endpoint.path, strings.NewReader(`{"regions":[{"start":0,"end":1,"type":"speaking","keep":true}]}`))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", endpoint.path, resp.StatusCode)
		}
	}
	after, err := srv.Store.GetCurrentJob(sm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ID != before.ID || after.Type != before.Type || after.State != before.State {
		t.Fatalf("job changed: before %+v after %+v", before, after)
	}
	if _, err := srv.Store.ClaimNextJob(context.Background(), []string{"apply_edits"}, time.Now()); !errors.Is(err, store.ErrNoJob) {
		t.Fatalf("retired job claim: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(srv.UploadsDir, sm.ID, "original.wav")); err != nil || string(data) != "source" {
		t.Fatalf("source = %q, %v", data, err)
	}
}

func TestLegacyStagingRestoresSourcesWithoutCleanup(t *testing.T) {
	srv, _ := newTestServer(t)
	if err := srv.Store.CreateSermon(store.Sermon{ID: "legacy", OriginalFilename: "source.wav", UploadedAt: time.Now().Format(time.RFC3339Nano), Stage: "edit", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(srv.UploadsDir, "legacy")
	staged := filepath.Join(dir, ".approval-staged")
	if err := os.MkdirAll(staged, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"original.wav", "normalized.flac", "normalized.mp3"} {
		if err := os.WriteFile(filepath.Join(staged, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "final.mp3"), []byte("final"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := srv.ReconcileTimelineArtifacts(); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"original.wav", "normalized.flac", "normalized.mp3", "final.mp3"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	// A collision stops recovery without overwriting or deleting either copy.
	if err := os.Mkdir(staged, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "original.wav"), []byte("other source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := srv.ReconcileTimelineArtifacts(); err == nil {
		t.Fatal("source collision accepted")
	}
	for path, want := range map[string]string{filepath.Join(staged, "original.wav"): "other source", filepath.Join(dir, "original.wav"): "original.wav"} {
		if data, err := os.ReadFile(path); err != nil || string(data) != want {
			t.Fatalf("source %s = %q, %v", path, data, err)
		}
	}
}

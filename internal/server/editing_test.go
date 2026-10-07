package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

func TestEditingHTTPContract(t *testing.T) {
	srv, ts := newTestServer(t)
	if err := srv.Store.CreateSermon(store.Sermon{ID: "editing", Stage: "editing", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	d := store.Editing{Duration: 8, Breakpoints: []store.Breakpoint{{ID: "start", Time: 0, Kind: "start"}, {ID: "end", Time: 8, Kind: "end"}}, Sections: []store.Section{{ID: "section", Keep: true}}}
	if err := srv.Store.PrepareEditing("editing", d, "source", store.TranscriptionMetadata{Duration: 8}); err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, body any, status int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, ts.URL+"/api/sermons/editing/"+path, bytes.NewReader(raw))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != status {
			t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, data)
		}
		return data
	}
	raw := request("GET", "editing", nil, 200)
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.Revision != 1 || len(d.Sections) != 1 {
		t.Fatalf("GET shape %+v", d)
	}
	raw = request("PUT", "editing", d, 200)
	var saved store.Editing
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 2 {
		t.Fatal("PUT did not return incremented revision")
	}
	request("PUT", "editing", d, 409)
	request("POST", "editing/apply", map[string]any{"revision": 1, "skip": false}, 409)
	request("POST", "editing/apply", map[string]any{"revision": 2}, 400)
	dir := filepath.Join(srv.UploadsDir, "editing")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"normalized.flac", "normalized.mp3", "final.mp3", "edited.mp3"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if body := request("GET", "audio/source", nil, 200); string(body) != "normalized.flac" {
		t.Fatal("source not FLAC")
	}
	if body := request("GET", "audio/playback", nil, 200); string(body) != "normalized.mp3" {
		t.Fatal("stale render played")
	}
	if err := srv.Store.CommitEditing("editing", "edited.mp3", "committed", store.TranscriptionMetadata{}, true); err != nil {
		t.Fatal(err)
	}
	if body := request("GET", "audio/playback", nil, 200); string(body) != "edited.mp3" {
		t.Fatal("committed render not played")
	}
	request("POST", "editing/apply", map[string]any{"revision": 2, "skip": false}, 202)
	request("PUT", "editing", saved, 409)
	request("POST", "editing/apply", map[string]any{"revision": 2, "skip": true}, 409)
	sm, _ := srv.Store.GetSermon("editing")
	if *sm.Transcript != "committed" {
		t.Fatal("apply queue replaced prior transcript")
	}
	if body := request("GET", "audio/playback", nil, 200); string(body) != "edited.mp3" {
		t.Fatal("pending apply replaced prior playback")
	}
	job, err := srv.Store.GetCurrentJob("editing")
	if err != nil || job.Type != "render_edit" {
		t.Fatalf("apply job %+v %v", job, err)
	}
}

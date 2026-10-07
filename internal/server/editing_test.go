package server

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

func TestRegenerateSpeakerBreakpoints(t *testing.T) {
	srv, ts := newTestServer(t)
	if err := srv.Store.CreateSermon(store.Sermon{ID: "regenerate", Stage: "editing", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	speaker := 0
	metadata := store.TranscriptionMetadata{Duration: 8, Segments: []store.TranscriptSegment{{Start: 0.08, End: 2, Text: "first phrase", Speaker: &speaker}, {Start: 3.4, End: 7, Text: "second phrase", Speaker: &speaker}}}
	if err := srv.Store.SaveSourceTranscription("regenerate", "original", metadata); err != nil {
		t.Fatal(err)
	}
	d := store.Editing{Duration: 8, Breakpoints: []store.Breakpoint{{ID: "start", Kind: "start"}, {ID: "end", Time: 8, Kind: "end"}}, Sections: []store.Section{{ID: "section", Keep: false}}}
	if err := srv.Store.PrepareEditing("regenerate", d, "original", metadata); err != nil {
		t.Fatal(err)
	}
	request := func(body string, status int) store.Editing {
		t.Helper()
		resp, err := http.Post(ts.URL+"/api/sermons/regenerate/editing/regenerate", "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != status {
			data, _ := io.ReadAll(resp.Body)
			t.Fatalf("status %d, want %d: %s", resp.StatusCode, status, data)
		}
		var got store.Editing
		if status == 200 {
			if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
		}
		return got
	}
	request(`{}`, 400)
	got := request(`{"revision":1}`, 200)
	if got.Revision != 2 || len(got.Breakpoints) != 4 || got.Breakpoints[1].Time != 0.08 || got.Breakpoints[2].Time != 3.4 || len(got.Sections) != 3 {
		t.Fatalf("regenerated draft: %+v", got)
	}
	for i, keep := range []bool{false, true, true} {
		if got.Sections[i].Keep != keep {
			t.Fatalf("regenerated section %d keep = %t, want %t", i, got.Sections[i].Keep, keep)
		}
	}
	request(`{"revision":1}`, 409)
	got.Breakpoints[2].Time = 3.8
	got.Breakpoints[2].Edited = true
	got.Sections[1].Keep = false
	if _, err := srv.Store.MutateEditing("regenerate", &got, 2, false, ""); err != nil {
		t.Fatal(err)
	}
	preserved := request(`{"revision":3,"preserve_edited":true}`, 200)
	if preserved.Revision != 4 || len(preserved.Breakpoints) != 4 || preserved.Breakpoints[2].Time != 3.8 || preserved.Sections[1].Keep {
		t.Fatalf("preservation request lost draft edits: %+v", preserved)
	}
	request(`{"revision":3,"preserve_edited":true}`, 409)
	sm, err := srv.Store.GetSermon("regenerate")
	if err != nil || sm.Stage != "editing" || sm.Status != "done" || sm.Transcript == nil || *sm.Transcript != "original" {
		t.Fatalf("regeneration changed source transcript or pipeline: %+v %v", sm, err)
	}
	if err := srv.Store.EnqueueJob(store.NewJob{ID: "busy", SermonID: "regenerate", Type: "prepare_edit", Stage: "editing"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	request(`{"revision":4}`, 409)
}

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
	if err := srv.Store.CommitEditing("editing", "edited.mp3", saved, true); err != nil {
		t.Fatal(err)
	}
	if body := request("GET", "audio/playback", nil, 200); string(body) != "edited.mp3" {
		t.Fatal("committed render not played")
	}
	request("POST", "editing/apply", map[string]any{"revision": 2, "skip": false}, 202)
	request("PUT", "editing", saved, 409)
	request("POST", "editing/apply", map[string]any{"revision": 2, "skip": true}, 409)
	sm, _ := srv.Store.GetSermon("editing")
	if *sm.Transcript != "source" {
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

func TestEditingWaveformCacheTracksSourceNotDraft(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	srv, ts := newTestServer(t)
	if err := srv.Store.CreateSermon(store.Sermon{ID: "waveform", Stage: "editing", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	d := store.Editing{Duration: 4, Breakpoints: []store.Breakpoint{{ID: "start", Kind: "start"}, {ID: "end", Time: 4, Kind: "end"}}, Sections: []store.Section{{ID: "section", Keep: true}}}
	if err := srv.Store.PrepareEditing("waveform", d, "source", store.TranscriptionMetadata{Duration: 4}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(srv.UploadsDir, "waveform")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	writeSource := func(level string) {
		t.Helper()
		if out, err := exec.Command("ffmpeg", "-y", "-v", "error", "-f", "lavfi", "-i", "aevalsrc="+level+":s=8000:d=4", filepath.Join(dir, "normalized.flac")).CombinedOutput(); err != nil {
			t.Fatalf("fixture: %v %s", err, out)
		}
	}
	read := func(want float64) {
		t.Helper()
		resp, err := http.Get(ts.URL + "/api/sermons/waveform/editing/waveform")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var got struct {
			Duration float64   `json:"duration"`
			Peaks    []float64 `json:"peaks"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 || got.Duration != 4 || len(got.Peaks) != 4000 || got.Peaks[1000] < want-0.0001 || got.Peaks[1000] > want+0.0001 || resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("waveform response: %d, duration=%f, count=%d", resp.StatusCode, got.Duration, len(got.Peaks))
		}
	}
	readHires := func(want int16) {
		t.Helper()
		resp, err := http.Get(ts.URL + "/api/sermons/waveform/editing/waveform/highres")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/octet-stream" || resp.Header.Get("X-Waveform-Rate") != "50" || len(data) != 4*50*2*2 {
			t.Fatalf("high-resolution response: status=%d type=%q rate=%q bytes=%d", resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("X-Waveform-Rate"), len(data))
		}
		got := int(binary.LittleEndian.Uint16(data))
		if got >= 1<<15 {
			got -= 1 << 16
		}
		delta := got - int(want)
		if delta < 0 {
			delta = -delta
		}
		if delta > 1 {
			t.Fatalf("first high-resolution pair: %d, want %d", got, want)
		}
	}
	writeSource("0.2")
	read(0.2)
	readHires(6553)
	if _, err := os.Stat(filepath.Join(dir, "waveform.json")); err != nil {
		t.Fatal("cache not written:", err)
	}
	d, _ = srv.Store.GetEditing("waveform")
	d.Sections[0].Keep = false
	if _, err := srv.Store.MutateEditing("waveform", &d, d.Revision, false, ""); err != nil {
		t.Fatal(err)
	}
	read(0.2)
	writeSource("-0.6")
	read(0.6)
	readHires(-19660)
}

package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := &Server{Store: st, UploadsDir: filepath.Join(dir, "uploads")}
	ts := httptest.NewServer(srv.Routes(fstest.MapFS{}))
	t.Cleanup(ts.Close)
	return srv, ts
}

func multipartBody(t *testing.T, filename string, content []byte) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(content)
	mw.Close()
	return &buf, mw.FormDataContentType()
}

func uploadFile(t *testing.T, ts *httptest.Server, filename string, content []byte, headers map[string]string) (*http.Response, store.Sermon) {
	t.Helper()
	body, ctype := multipartBody(t, filename, content)
	req, err := http.NewRequest("POST", ts.URL+"/api/sermons", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", ctype)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sm store.Sermon
	if resp.StatusCode == http.StatusCreated {
		if err := json.NewDecoder(resp.Body).Decode(&sm); err != nil {
			t.Fatalf("decode upload response: %v", err)
		}
	}
	return resp, sm
}

func TestUpload(t *testing.T) {
	srv, ts := newTestServer(t)

	content := []byte("fake audio bytes")
	resp, sm := uploadFile(t, ts, "Sunday Sermon.MP3", content, map[string]string{"X-Exedev-Email": "pastor@example.com"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	if sm.ID == "" {
		t.Fatal("sermon id is empty")
	}
	if sm.OriginalFilename != "Sunday Sermon.MP3" {
		t.Errorf("original_filename = %q", sm.OriginalFilename)
	}
	if sm.Stage != "upload" || sm.Status != "done" {
		t.Errorf("stage/status = %q/%q, want upload/done", sm.Stage, sm.Status)
	}
	if sm.UploadedBy == nil || *sm.UploadedBy != "pastor@example.com" {
		t.Errorf("uploaded_by = %v, want pastor@example.com", sm.UploadedBy)
	}

	// File on disk, extension sanitized to lowercase.
	got, err := os.ReadFile(filepath.Join(srv.UploadsDir, sm.ID, "original.mp3"))
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Error("stored file content mismatch")
	}

	// Row in DB.
	if _, err := srv.Store.GetSermon(sm.ID); err != nil {
		t.Errorf("GetSermon after upload: %v", err)
	}
}

func TestUploadWithoutEmail(t *testing.T) {
	_, ts := newTestServer(t)
	resp, sm := uploadFile(t, ts, "a.wav", []byte("x"), nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	if sm.UploadedBy != nil {
		t.Errorf("uploaded_by = %v, want nil", *sm.UploadedBy)
	}
}

func TestListNewestFirst(t *testing.T) {
	_, ts := newTestServer(t)

	names := []string{"first.wav", "second.wav", "third.wav"}
	for _, n := range names {
		resp, _ := uploadFile(t, ts, n, []byte("x"), nil)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("upload %s: status %d", n, resp.StatusCode)
		}
	}

	resp, err := http.Get(ts.URL + "/api/sermons")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list []store.Sermon
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("len = %d, want 3", len(list))
	}
	want := []string{"third.wav", "second.wav", "first.wav"}
	for i, w := range want {
		if list[i].OriginalFilename != w {
			t.Errorf("list[%d] = %q, want %q", i, list[i].OriginalFilename, w)
		}
	}
}

func TestDelete(t *testing.T) {
	srv, ts := newTestServer(t)

	_, sm := uploadFile(t, ts, "gone.mp3", []byte("x"), nil)
	dir := filepath.Join(srv.UploadsDir, sm.ID)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("uploads dir missing before delete: %v", err)
	}

	req, _ := http.NewRequest("DELETE", ts.URL+"/api/sermons/"+sm.ID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	if _, err := srv.Store.GetSermon(sm.ID); err != store.ErrNotFound {
		t.Errorf("GetSermon after delete: err = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("uploads dir still present after delete (err=%v)", err)
	}
	if _, err := os.Stat(dir + ".deleting"); !os.IsNotExist(err) {
		t.Errorf("staged uploads dir still present after delete (err=%v)", err)
	}
}

func TestDeleteNotFound(t *testing.T) {
	_, ts := newTestServer(t)
	req, _ := http.NewRequest("DELETE", ts.URL+"/api/sermons/nope", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestUploadSizeCap(t *testing.T) {
	srv, ts := newTestServer(t)
	srv.MaxUploadBytes = 1024 // small cap for the test

	resp, _ := uploadFile(t, ts, "big.wav", bytes.Repeat([]byte("a"), 4096), nil)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}

	// Nothing left behind: no rows, no upload dirs.
	sermons, err := srv.Store.ListSermons()
	if err != nil {
		t.Fatal(err)
	}
	if len(sermons) != 0 {
		t.Errorf("sermon rows = %d, want 0", len(sermons))
	}
	entries, err := os.ReadDir(srv.UploadsDir)
	if err == nil && len(entries) > 0 {
		t.Errorf("uploads dir has %d entries, want 0", len(entries))
	}
}

func TestSanitizeExt(t *testing.T) {
	cases := map[string]string{
		"a.mp3":            ".mp3",
		"A.WAV":            ".wav",
		"noext":            "",
		"trailing.":        "",
		"weird.m p3":       "",
		"../../etc/passwd": "",
		"x.m4a":            ".m4a",
	}
	for in, want := range cases {
		if got := sanitizeExt(in); got != want {
			t.Errorf("sanitizeExt(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeleteStageFailureKeepsRowAndFiles(t *testing.T) {
	srv, ts := newTestServer(t)

	_, sm := uploadFile(t, ts, "sticky.mp3", []byte("x"), nil)
	dir := filepath.Join(srv.UploadsDir, sm.ID)
	stagedDir := dir + ".deleting"

	// Block the staging rename with a non-empty destination directory.
	if err := os.Mkdir(stagedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stagedDir, "blocker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("DELETE", ts.URL+"/api/sermons/"+sm.ID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}

	// Row must survive so the delete can be retried.
	if _, err := srv.Store.GetSermon(sm.ID); err != nil {
		t.Errorf("row missing after failed staging: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "original.mp3")); err != nil || string(got) != "x" {
		t.Errorf("original file after failed staging = %q, %v", got, err)
	}

	// Retry succeeds once the staging destination is clear.
	if err := os.RemoveAll(stagedDir); err != nil {
		t.Fatal(err)
	}
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Fatalf("retry status = %d, want 204", resp2.StatusCode)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("uploads dir still present after retry (err=%v)", err)
	}
}

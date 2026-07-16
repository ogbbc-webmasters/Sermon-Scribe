package main

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
)

func newTestServer(t *testing.T) (*server, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	db, err := openDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	srv := &server{db: db, uploadsDir: filepath.Join(dir, "uploads")}
	ts := httptest.NewServer(srv.routes())
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

func uploadFile(t *testing.T, ts *httptest.Server, filename string, content []byte, headers map[string]string) (*http.Response, sermon) {
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
	var sm sermon
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
	got, err := os.ReadFile(filepath.Join(srv.uploadsDir, sm.ID, "original.mp3"))
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Error("stored file content mismatch")
	}

	// Row in DB.
	var count int
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM sermons WHERE id = ?`, sm.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("rows for id = %d, want 1", count)
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
	var list []sermon
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
	dir := filepath.Join(srv.uploadsDir, sm.ID)
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

	var count int
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM sermons WHERE id = ?`, sm.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("row still present after delete")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("uploads dir still present after delete (err=%v)", err)
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
	srv.maxUploadBytes = 1024 // small cap for the test

	resp, _ := uploadFile(t, ts, "big.wav", bytes.Repeat([]byte("a"), 4096), nil)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}

	// Nothing left behind: no rows, no upload dirs.
	var count int
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM sermons`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("sermon rows = %d, want 0", count)
	}
	entries, err := os.ReadDir(srv.uploadsDir)
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

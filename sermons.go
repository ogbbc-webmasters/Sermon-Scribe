package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultMaxUploadBytes = 2 << 30 // 2 GB

type sermon struct {
	ID               string  `json:"id"`
	OriginalFilename string  `json:"original_filename"`
	UploadedAt       string  `json:"uploaded_at"`
	UploadedBy       *string `json:"uploaded_by"`
	Stage            string  `json:"stage"`
	Status           string  `json:"status"`
}

// newUUID returns a random (version 4) UUID string.
func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// sanitizeExt extracts a safe file extension (including the leading dot)
// from an uploaded filename. Returns "" if there is no usable extension.
func sanitizeExt(filename string) string {
	ext := filepath.Ext(filepath.Base(filename))
	if ext == "." || ext == "" {
		return ""
	}
	ext = strings.ToLower(ext)
	for _, r := range ext[1:] {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return ""
		}
	}
	if len(ext) > 11 { // dot + 10 chars is plenty for any audio extension
		return ""
	}
	return ext
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *server) handleUploadSermon(w http.ResponseWriter, r *http.Request) {
	maxBytes := s.maxUploadBytes
	if maxBytes == 0 {
		maxBytes = defaultMaxUploadBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "expected multipart/form-data")
		return
	}

	var part io.Reader
	var filename string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			s.uploadReadError(w, err)
			return
		}
		if p.FormName() == "file" {
			part = p
			filename = p.FileName()
			break
		}
	}
	if part == nil {
		writeError(w, http.StatusBadRequest, `missing "file" part`)
		return
	}
	if filename == "" {
		writeError(w, http.StatusBadRequest, "uploaded file has no filename")
		return
	}
	filename = filepath.Base(filename)

	id := newUUID()
	dir := filepath.Join(s.uploadsDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("upload: mkdir %s: %v", dir, err)
		writeError(w, http.StatusInternalServerError, "could not store upload")
		return
	}
	dst := filepath.Join(dir, "original"+sanitizeExt(filename))

	f, err := os.Create(dst)
	if err != nil {
		log.Printf("upload: create %s: %v", dst, err)
		os.RemoveAll(dir)
		writeError(w, http.StatusInternalServerError, "could not store upload")
		return
	}
	_, err = io.Copy(f, part)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.RemoveAll(dir)
		s.uploadReadError(w, err)
		return
	}

	sm := sermon{
		ID:               id,
		OriginalFilename: filename,
		UploadedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		Stage:            "upload",
		Status:           "done",
	}
	if email := r.Header.Get("X-Exedev-Email"); email != "" {
		sm.UploadedBy = &email
	}

	_, err = s.db.Exec(
		`INSERT INTO sermons (id, original_filename, uploaded_at, uploaded_by, stage, status)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sm.ID, sm.OriginalFilename, sm.UploadedAt, sm.UploadedBy, sm.Stage, sm.Status,
	)
	if err != nil {
		log.Printf("upload: insert sermon: %v", err)
		os.RemoveAll(dir)
		writeError(w, http.StatusInternalServerError, "could not record upload")
		return
	}

	writeJSON(w, http.StatusCreated, sm)
}

// uploadReadError reports a body-read failure, distinguishing the
// MaxBytesReader size cap from other errors.
func (s *server) uploadReadError(w http.ResponseWriter, err error) {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		writeError(w, http.StatusRequestEntityTooLarge, "upload exceeds size limit")
		return
	}
	log.Printf("upload: read body: %v", err)
	writeError(w, http.StatusBadRequest, "error reading upload")
}

func (s *server) handleListSermons(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(
		`SELECT id, original_filename, uploaded_at, uploaded_by, stage, status
		 FROM sermons ORDER BY uploaded_at DESC, id`)
	if err != nil {
		log.Printf("list sermons: %v", err)
		writeError(w, http.StatusInternalServerError, "could not list sermons")
		return
	}
	defer rows.Close()

	sermons := []sermon{}
	for rows.Next() {
		var sm sermon
		if err := rows.Scan(&sm.ID, &sm.OriginalFilename, &sm.UploadedAt, &sm.UploadedBy, &sm.Stage, &sm.Status); err != nil {
			log.Printf("list sermons: scan: %v", err)
			writeError(w, http.StatusInternalServerError, "could not list sermons")
			return
		}
		sermons = append(sermons, sm)
	}
	if err := rows.Err(); err != nil {
		log.Printf("list sermons: rows: %v", err)
		writeError(w, http.StatusInternalServerError, "could not list sermons")
		return
	}
	writeJSON(w, http.StatusOK, sermons)
}

func (s *server) handleDeleteSermon(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	res, err := s.db.Exec(`DELETE FROM sermons WHERE id = ?`, id)
	if err != nil {
		log.Printf("delete sermon %s: %v", id, err)
		writeError(w, http.StatusInternalServerError, "could not delete sermon")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, http.StatusNotFound, "sermon not found")
		return
	}

	if err := os.RemoveAll(filepath.Join(s.uploadsDir, id)); err != nil {
		log.Printf("delete sermon %s: remove uploads dir: %v", id, err)
	}

	w.WriteHeader(http.StatusNoContent)
}

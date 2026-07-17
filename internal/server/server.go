// Package server implements the HTTP API and serves the embedded frontend.
// All persistence goes through the store package.
package server

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

const defaultMaxUploadBytes = 2 << 30 // 2 GB

// Server holds the HTTP handlers' dependencies.
type Server struct {
	Store          *store.Store
	UploadsDir     string
	MaxUploadBytes int64 // 0 means defaultMaxUploadBytes
}

// Routes returns the full handler: the JSON API plus the embedded frontend.
func (s *Server) Routes(webFS fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sermons", s.handleUploadSermon)
	mux.HandleFunc("GET /api/sermons", s.handleListSermons)
	mux.HandleFunc("DELETE /api/sermons/{id}", s.handleDeleteSermon)
	mux.Handle("/", http.FileServerFS(webFS))
	return mux
}

// newUUID returns a time-ordered (version 7) UUID string.
func newUUID() string {
	return uuid.Must(uuid.NewV7()).String()
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

func (s *Server) handleUploadSermon(w http.ResponseWriter, r *http.Request) {
	maxBytes := s.MaxUploadBytes
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
	dir := filepath.Join(s.UploadsDir, id)
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

	sm := store.Sermon{
		ID:               id,
		OriginalFilename: filename,
		UploadedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		Stage:            "upload",
		Status:           "done",
	}
	if email := r.Header.Get("X-Exedev-Email"); email != "" {
		sm.UploadedBy = &email
	}

	if err := s.Store.CreateSermon(sm); err != nil {
		log.Printf("upload: insert sermon: %v", err)
		os.RemoveAll(dir)
		writeError(w, http.StatusInternalServerError, "could not record upload")
		return
	}

	writeJSON(w, http.StatusCreated, sm)
}

// uploadReadError reports a body-read failure, distinguishing the
// MaxBytesReader size cap from other errors.
func (s *Server) uploadReadError(w http.ResponseWriter, err error) {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		writeError(w, http.StatusRequestEntityTooLarge, "upload exceeds size limit")
		return
	}
	log.Printf("upload: read body: %v", err)
	writeError(w, http.StatusBadRequest, "error reading upload")
}

func (s *Server) handleListSermons(w http.ResponseWriter, r *http.Request) {
	sermons, err := s.Store.ListSermons()
	if err != nil {
		log.Printf("list sermons: %v", err)
		writeError(w, http.StatusInternalServerError, "could not list sermons")
		return
	}
	writeJSON(w, http.StatusOK, sermons)
}

func (s *Server) handleDeleteSermon(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// The uploads dir is removed before the row deletion commits: if the
	// removal fails, the transaction rolls back so the row survives and
	// the delete can be retried instead of orphaning files on disk.
	deleted, err := s.Store.DeleteSermon(id, func() error {
		return os.RemoveAll(filepath.Join(s.UploadsDir, id))
	})
	if err != nil {
		log.Printf("delete sermon %s: %v", id, err)
		writeError(w, http.StatusInternalServerError, "could not delete sermon")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "sermon not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

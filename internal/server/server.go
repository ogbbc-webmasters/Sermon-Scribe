// Package server implements the HTTP API and serves the embedded frontend.
// All persistence goes through the store package.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/processing"
	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

const defaultMaxUploadBytes = 2 << 30 // 2 GB

// Server holds the HTTP handlers' dependencies.
type Server struct {
	Store          *store.Store
	UploadsDir     string
	MaxUploadBytes int64 // 0 means defaultMaxUploadBytes
	Events         *EventHub
	Queue          interface{ Notify() }
}

// ReconcileTimelineArtifacts repairs interrupted legacy approval staging and
// restores sources regardless of legacy approval state. It never deletes audio.
func (s *Server) ReconcileTimelineArtifacts() error {
	sermons, err := s.Store.ListSermons()
	if err != nil {
		return fmt.Errorf("list sermons for timeline reconciliation: %w", err)
	}
	for _, sermon := range sermons {
		dir := filepath.Join(s.UploadsDir, sermon.ID)
		if err := reconcileApprovalStaging(dir); err != nil {
			return fmt.Errorf("reconcile approval artifacts for sermon %s: %w", sermon.ID, err)
		}
	}
	return nil
}

// Routes returns the full handler: the JSON API plus the embedded frontend.
func (s *Server) Routes(webFS fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sermons", s.handleUploadSermon)
	mux.HandleFunc("GET /api/sermons", s.handleListSermons)
	mux.HandleFunc("DELETE /api/sermons/{id}", s.handleDeleteSermon)
	mux.HandleFunc("POST /api/sermons/{id}/retry", s.handleRetrySermon)
	mux.HandleFunc("POST /api/sermons/{id}/retry/{part}", s.handleRetryProcessing)
	mux.HandleFunc("PUT /api/sermons/{id}/scriptures", s.handleSaveScriptures)
	mux.HandleFunc("POST /api/sermons/{id}/normalize", s.handleRerunNormalization)
	mux.HandleFunc("POST /api/sermons/{id}/review-normalization", s.handleReviewNormalization)
	mux.HandleFunc("GET /api/sermons/{id}/audio/{type}", s.handleSermonAudio)
	mux.HandleFunc("GET /api/sermons/{id}/editing", s.handleEditing)
	mux.HandleFunc("PUT /api/sermons/{id}/editing", s.handleEditing)
	mux.HandleFunc("POST /api/sermons/{id}/editing/regenerate", s.handleRegenerateEditing)
	mux.HandleFunc("POST /api/sermons/{id}/editing/apply", s.handleApplyEditing)
	mux.HandleFunc("GET /api/sermons/{id}/editing/preview", s.handleEditingPreview)
	mux.HandleFunc("GET /api/sermons/{id}/editing/waveform/highres", s.handleEditingWaveformHighRes)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	files := http.FileServerFS(webFS)
	mux.HandleFunc("GET /sermons/{id}", func(w http.ResponseWriter, r *http.Request) {
		// Detail URLs load the same Elm application as the list page.
		request := r.Clone(r.Context())
		request.URL.Path = "/"
		files.ServeHTTP(w, request)
	})
	mux.Handle("/", files)
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

	sm := store.Sermon{
		ID:               id,
		OriginalFilename: filename,
		UploadedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		Stage:            "upload",
		Status:           "pending",
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
	if err := s.Store.StartUpload(id); err != nil {
		log.Printf("upload: start sermon: %v", err)
		s.abandonUpload(id, dir)
		writeError(w, http.StatusInternalServerError, "could not record upload")
		return
	}

	dst := filepath.Join(dir, "original"+sanitizeExt(filename))
	f, err := os.Create(dst)
	if err != nil {
		log.Printf("upload: create %s: %v", dst, err)
		s.abandonUpload(id, dir)
		writeError(w, http.StatusInternalServerError, "could not store upload")
		return
	}
	_, err = io.Copy(f, part)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		s.abandonUpload(id, dir)
		s.uploadReadError(w, err)
		return
	}

	sm, err = s.Store.CompleteUpload(id, newUUID(), time.Now())
	if err != nil {
		log.Printf("upload: complete sermon: %v", err)
		s.abandonUpload(id, dir)
		writeError(w, http.StatusInternalServerError, "could not record upload")
		return
	}
	if s.Events != nil {
		s.Events.Publish(processing.Event{Name: processing.EventStageCompleted, Sermon: sm})
	}
	if s.Queue != nil {
		s.Queue.Notify()
	}
	writeJSON(w, http.StatusCreated, sm)
}

func (s *Server) abandonUpload(id, dir string) {
	deleted, err := s.Store.DeleteSermon(id, nil)
	if err != nil {
		log.Printf("upload: remove abandoned sermon %s: %v", id, err)
		return
	}
	if deleted && s.Events != nil {
		s.Events.PublishDeleted(id)
	}
	if err := os.RemoveAll(dir); err != nil {
		log.Printf("upload: remove abandoned files %s: %v", id, err)
	}
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

func (s *Server) handleRetrySermon(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.Store.GetSermon(id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "sermon not found")
		return
	} else if err != nil {
		log.Printf("retry sermon %s: %v", id, err)
		writeError(w, http.StatusInternalServerError, "could not retry sermon")
		return
	}

	_, sm, err := s.Store.RetryFailedJob(id, time.Now())
	if errors.Is(err, store.ErrNotRetryable) {
		writeError(w, http.StatusConflict, "sermon has no failed job to retry")
		return
	}
	if err != nil {
		log.Printf("retry sermon %s: %v", id, err)
		writeError(w, http.StatusInternalServerError, "could not retry sermon")
		return
	}
	if s.Events != nil {
		s.Events.Publish(processing.Event{Name: processing.EventProgress, Sermon: sm})
	}
	if s.Queue != nil {
		s.Queue.Notify()
	}
	writeJSON(w, http.StatusAccepted, sm)
}

func (s *Server) handleRetryProcessing(w http.ResponseWriter, r *http.Request) {
	id, part := r.PathValue("id"), r.PathValue("part")
	if part != "title" && part != "topics" && part != "transcription" && part != "scriptures" {
		writeError(w, http.StatusBadRequest, "unknown processing part")
		return
	}
	if _, err := s.Store.GetSermon(id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "sermon not found")
		return
	} else if err != nil {
		log.Printf("load sermon for processing retry %s: %v", id, err)
		writeError(w, http.StatusInternalServerError, "could not retry processing")
		return
	}
	if part == "transcription" {
		dir := filepath.Join(s.UploadsDir, id)
		_, err := os.Stat(filepath.Join(dir, "normalized.mp3"))
		if errors.Is(err, os.ErrNotExist) {
			_, err = originalAudioPath(dir)
		}
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusConflict, "audio is not available for transcription")
			return
		} else if err != nil {
			log.Printf("load audio for retry %s: %v", id, err)
			writeError(w, http.StatusInternalServerError, "could not retry transcription")
			return
		}
	}
	sm, err := s.Store.EnqueueProcessingRerun(id, newUUID(), part, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "sermon not found")
		return
	} else if errors.Is(err, store.ErrNotRetryable) {
		writeError(w, http.StatusConflict, "processing is busy or the required input is unavailable")
		return
	} else if err != nil {
		log.Printf("retry %s for sermon %s: %v", part, id, err)
		writeError(w, http.StatusInternalServerError, "could not retry processing")
		return
	}
	if s.Events != nil {
		s.Events.Publish(processing.Event{Name: processing.EventProgress, Sermon: sm})
	}
	if s.Queue != nil {
		s.Queue.Notify()
	}
	writeJSON(w, http.StatusAccepted, sm)
}

func (s *Server) handleSaveScriptures(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sm, err := s.Store.GetSermon(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "sermon not found")
		return
	} else if err != nil {
		log.Printf("load sermon for scripture selection %s: %v", id, err)
		writeError(w, http.StatusInternalServerError, "could not save scripture selection")
		return
	}
	var request struct {
		Scriptures []string `json:"scriptures"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid scripture selection")
		return
	}
	available := sm.ScriptureOptions
	if len(available) == 0 {
		available = sm.Scriptures
	}
	options := make(map[string]bool, len(available))
	for _, option := range available {
		options[option] = true
	}
	seen := make(map[string]bool, len(request.Scriptures))
	for _, scripture := range request.Scriptures {
		if !options[scripture] || seen[scripture] {
			writeError(w, http.StatusBadRequest, "scripture selection must contain unique available references")
			return
		}
		seen[scripture] = true
	}
	if err := s.Store.SaveScriptureSelection(id, sm.OldTestamentReading, sm.NewTestamentReading, request.Scriptures, available); err != nil {
		log.Printf("save scripture selection %s: %v", id, err)
		writeError(w, http.StatusInternalServerError, "could not save scripture selection")
		return
	}
	sm, err = s.Store.GetSermon(id)
	if err != nil {
		log.Printf("reload sermon after scripture selection %s: %v", id, err)
		writeError(w, http.StatusInternalServerError, "could not save scripture selection")
		return
	}
	if s.Events != nil {
		s.Events.Publish(processing.Event{Name: processing.EventProgress, Sermon: sm})
	}
	writeJSON(w, http.StatusOK, sm)
}

func (s *Server) handleRerunNormalization(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sm, err := s.Store.GetSermon(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "sermon not found")
		return
	} else if err != nil {
		log.Printf("load sermon for normalization rerun %s: %v", id, err)
		writeError(w, http.StatusInternalServerError, "could not rerun normalization")
		return
	}

	var request struct {
		Adjustment string `json:"adjustment"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid normalization request")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid normalization request")
		return
	}
	if !processing.ValidNormalizationAdjustment(request.Adjustment) {
		writeError(w, http.StatusBadRequest, "unknown normalization adjustment")
		return
	}
	if _, err := originalAudioPath(filepath.Join(s.UploadsDir, id)); errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusConflict, "the original upload was removed after normalization")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load original audio")
		return
	}
	settings, err := processing.AdjustNormalization(processing.NormalizationSettings{
		GateAdjustment:   sm.NormalizationGateAdjustment,
		VolumeAdjustment: sm.NormalizationVolumeAdjustment,
	}, request.Adjustment)
	if err != nil {
		writeError(w, http.StatusConflict, "normalization adjustment limit reached")
		return
	}
	parameters, err := processing.NormalizationParameters(settings)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid normalization adjustment")
		return
	}
	sm, err = s.Store.EnqueueNormalizationRerun(id, newUUID(), parameters, time.Now())
	if errors.Is(err, store.ErrNotRerunnable) {
		writeError(w, http.StatusConflict, "normalization is not ready to rerun")
		return
	}
	if err != nil {
		log.Printf("rerun normalization %s: %v", id, err)
		writeError(w, http.StatusInternalServerError, "could not rerun normalization")
		return
	}
	if s.Events != nil {
		s.Events.Publish(processing.Event{Name: processing.EventProgress, Sermon: sm})
	}
	if s.Queue != nil {
		s.Queue.Notify()
	}
	writeJSON(w, http.StatusAccepted, sm)
}

func (s *Server) handleReviewNormalization(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sm, err := s.Store.MarkNormalizationReviewed(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "sermon not found")
		return
	}
	if errors.Is(err, store.ErrEditConflict) {
		writeError(w, http.StatusConflict, "normalization is not ready for review")
		return
	}
	if err != nil {
		log.Printf("review normalization %s: %v", id, err)
		writeError(w, http.StatusInternalServerError, "could not save normalization review")
		return
	}
	if s.Events != nil {
		s.Events.Publish(processing.Event{Name: processing.EventProgress, Sermon: sm})
	}
	writeJSON(w, http.StatusOK, sm)
}

func (s *Server) handleSermonAudio(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sm, err := s.Store.GetSermon(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "sermon not found")
		return
	}
	if err != nil {
		log.Printf("load sermon audio %s: %v", id, err)
		writeError(w, http.StatusInternalServerError, "could not load audio")
		return
	}

	dir := filepath.Join(s.UploadsDir, id)
	audioType := r.PathValue("type")
	var path, contentType, downloadName string
	switch audioType {
	case "source":
		path = filepath.Join(dir, "normalized.flac")
		contentType, downloadName = "audio/flac", "normalized.flac"
	case "playback":
		committed, playbackErr := s.Store.EditingPlayback(id)
		if playbackErr != nil {
			writeError(w, http.StatusInternalServerError, "could not load playback")
			return
		}
		names := []string{"normalized.mp3"}
		if committed != "" {
			names = append([]string{committed}, names...)
		}
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			if _, statErr := os.Stat(candidate); statErr == nil {
				path, contentType, downloadName = candidate, "audio/mpeg", name
				break
			} else if !errors.Is(statErr, os.ErrNotExist) {
				writeError(w, http.StatusInternalServerError, "could not load audio")
				return
			}
		}
		if path == "" {
			path, err = originalAudioPath(dir)
			contentType = "application/octet-stream"
			downloadName = "original" + sanitizeExt(sm.OriginalFilename)
		}
	case "original":
		path, err = originalAudioPath(dir)
		contentType = "application/octet-stream"
		downloadName = "original" + sanitizeExt(sm.OriginalFilename)
	case "normalized", "proxy":
		if sm.Stage == "upload" || (sm.Stage == "normalization" && sm.Status != "done") {
			writeError(w, http.StatusConflict, "normalized audio is not ready")
			return
		}
		path = filepath.Join(dir, "normalized.mp3")
		contentType = "audio/mpeg"
		downloadName = "normalized.mp3"
	case "final":
		// Legacy renders are read-only; regeneration may have changed the stage.
		path = filepath.Join(dir, "final.mp3")
		contentType = "audio/mpeg"
		downloadName = "final.mp3"
	default:
		writeError(w, http.StatusNotFound, "audio type not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusNotFound, "audio file not found")
		return
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "audio file not found")
		return
	}
	if err != nil {
		log.Printf("open sermon audio %s: %v", id, err)
		writeError(w, http.StatusInternalServerError, "could not load audio")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load audio")
		return
	}
	w.Header().Set("Content-Type", contentType)
	if audioType != "original" {
		// Normalization reruns overwrite these paths, so clients must revalidate.
		w.Header().Set("Cache-Control", "no-store")
	}
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", downloadName))
	}
	http.ServeContent(w, r, downloadName, info.ModTime(), file)
}

func reconcileApprovalStaging(dir string) error {
	staged := filepath.Join(dir, ".approval-staged")
	entries, err := os.ReadDir(staged)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		dst := filepath.Join(dir, entry.Name())
		if _, err := os.Lstat(dst); err == nil {
			return fmt.Errorf("restore staged source %s: destination exists", entry.Name())
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(filepath.Join(staged, entry.Name()), dst); err != nil {
			return fmt.Errorf("restore staged source %s: %w", entry.Name(), err)
		}
	}
	return os.Remove(staged)
}

func originalAudioPath(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var found string
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type().IsRegular() && (name == "original" || strings.HasPrefix(name, "original.")) {
			if found != "" {
				return "", errors.New("multiple original audio files")
			}
			found = filepath.Join(dir, name)
		}
	}
	if found == "" {
		return "", os.ErrNotExist
	}
	return found, nil
}

func (s *Server) handleDeleteSermon(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	uploadDir := filepath.Join(s.UploadsDir, id)
	stagedDir := uploadDir + ".deleting"
	staged := false

	// Atomically stage the uploads before the row deletion commits. If the
	// transaction fails, the directory can be restored without losing files.
	deleted, err := s.Store.DeleteSermon(id, func() error {
		if err := os.Rename(uploadDir, stagedDir); err != nil {
			return err
		}
		staged = true
		return nil
	})
	if err != nil {
		if staged {
			if restoreErr := os.Rename(stagedDir, uploadDir); restoreErr != nil {
				err = errors.Join(err, fmt.Errorf("restore uploads: %w", restoreErr))
			}
		}
		log.Printf("delete sermon %s: %v", id, err)
		writeError(w, http.StatusInternalServerError, "could not delete sermon")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "sermon not found")
		return
	}

	// The row is committed and the original path is gone. A cleanup failure
	// may leave staged files for manual cleanup, but cannot corrupt a live row.
	if err := os.RemoveAll(stagedDir); err != nil {
		log.Printf("delete sermon %s: remove staged uploads: %v", id, err)
	}
	if s.Events != nil {
		s.Events.PublishDeleted(id)
	}

	w.WriteHeader(http.StatusNoContent)
}

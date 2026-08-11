// Package server implements the HTTP API and serves the embedded frontend.
// All persistence goes through the store package.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	renderSection  func(context.Context, string, string, float64, float64) error
}

// ReconcileTimelineArtifacts repairs interrupted legacy approval staging and
// finishes source cleanup for approvals committed before a process stopped.
// It runs before workers so an unapproved edit never starts without sources
// that can still be restored safely.
func (s *Server) ReconcileTimelineArtifacts() error {
	sermons, err := s.Store.ListSermons()
	if err != nil {
		return fmt.Errorf("list sermons for timeline reconciliation: %w", err)
	}
	for _, sermon := range sermons {
		dir := filepath.Join(s.UploadsDir, sermon.ID)
		if err := reconcileApprovalStaging(dir, sermon.EditApproved); err != nil {
			return fmt.Errorf("reconcile approval artifacts for sermon %s: %w", sermon.ID, err)
		}
		if sermon.EditApproved {
			if _, err := os.Stat(filepath.Join(dir, "final.mp3")); err != nil {
				return fmt.Errorf("verify final audio for approved sermon %s: %w", sermon.ID, err)
			}
			if err := cleanupApprovedSources(dir); err != nil {
				return fmt.Errorf("clean approved sources for sermon %s: %w", sermon.ID, err)
			}
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
	mux.HandleFunc("POST /api/sermons/{id}/normalize", s.handleRerunNormalization)
	mux.HandleFunc("GET /api/sermons/{id}/waveform", s.handleWaveform)
	mux.HandleFunc("POST /api/sermons/{id}/analyze", s.handleAnalyze)
	mux.HandleFunc("POST /api/sermons/{id}/apply-edits", s.handleApplyEdits)
	mux.HandleFunc("POST /api/sermons/{id}/approve-edit", s.handleApproveEdit)
	mux.HandleFunc("GET /api/sermons/{id}/audio/{type}", s.handleSermonAudio)
	mux.HandleFunc("GET /api/events", s.handleEvents)
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
	case "original":
		path, err = originalAudioPath(dir)
		contentType = "application/octet-stream"
		downloadName = "original" + sanitizeExt(sm.OriginalFilename)
	case "normalized":
		if sm.Stage != "normalization" || sm.Status != "done" {
			writeError(w, http.StatusConflict, "normalized audio is not ready")
			return
		}
		path = filepath.Join(dir, "normalized.flac")
		contentType = "audio/flac"
		downloadName = "normalized.flac"
	case "proxy":
		if sm.EditApproved || (sm.Stage == "normalization" && sm.Status != "done") || (sm.Stage != "normalization" && sm.Stage != "edit") {
			writeError(w, http.StatusConflict, "normalized audio is not ready")
			return
		}
		path = filepath.Join(dir, "normalized.mp3")
		contentType = "audio/mpeg"
		downloadName = "normalized.mp3"
	case "section":
		if sm.EditApproved || (sm.Stage == "normalization" && sm.Status != "done") || (sm.Stage != "normalization" && sm.Stage != "edit") {
			writeError(w, http.StatusConflict, "normalized audio is not ready")
			return
		}
		_, waveform, waveformErr := s.loadWaveform(id)
		if waveformErr != nil {
			writeError(w, http.StatusConflict, "waveform is not ready")
			return
		}
		start, startErr := strconv.ParseFloat(r.URL.Query().Get("start"), 64)
		end, endErr := strconv.ParseFloat(r.URL.Query().Get("end"), 64)
		if startErr != nil || endErr != nil || math.IsNaN(start) || math.IsNaN(end) || math.IsInf(start, 0) || math.IsInf(end, 0) || start < 0 || end <= start || end > waveform.Duration+0.01 {
			writeError(w, http.StatusBadRequest, "invalid section range")
			return
		}
		path, err = s.sectionAudioPath(r.Context(), dir, start, min(end, waveform.Duration))
		if err != nil {
			log.Printf("render sermon audio section %s: %v", id, err)
			writeError(w, http.StatusInternalServerError, "could not render audio section")
			return
		}
		contentType = "audio/mpeg"
		downloadName = "section.mp3"
	case "final":
		if sm.Stage != "edit" || sm.Status != "done" || sm.AppliedRegions == nil {
			writeError(w, http.StatusConflict, "final audio is not ready")
			return
		}
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

func (s *Server) sectionAudioPath(ctx context.Context, dir string, start, end float64) (string, error) {
	input := filepath.Join(dir, "normalized.flac")
	info, err := os.Stat(input)
	if err != nil {
		return "", err
	}
	startMicros := int64(math.Round(start * 1e6))
	endMicros := int64(math.Round(end * 1e6))
	cacheDir := filepath.Join(dir, ".sections")
	path := filepath.Join(cacheDir, fmt.Sprintf("v1-%d-%d-%d.mp3", info.ModTime().UnixNano(), startMicros, endMicros))
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	temp, err := os.CreateTemp(cacheDir, ".section-*.mp3")
	if err != nil {
		return "", err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Close(); err != nil {
		return "", err
	}
	render := s.renderSection
	if render == nil {
		render = processing.RenderAudioSection
	}
	if err := render(ctx, input, tempPath, float64(startMicros)/1e6, float64(endMicros)/1e6); err != nil {
		return "", err
	}
	if info, err := os.Stat(tempPath); err != nil || info.Size() == 0 {
		return "", fmt.Errorf("rendered audio section is empty")
	}
	if err := os.Rename(tempPath, path); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Server) loadWaveform(id string) (store.Sermon, processing.Waveform, error) {
	sm, err := s.Store.GetSermon(id)
	if err != nil {
		return sm, processing.Waveform{}, err
	}
	if sm.EditApproved || !((sm.Stage == "normalization" && sm.Status == "done") || sm.Stage == "edit") {
		return sm, processing.Waveform{}, store.ErrEditConflict
	}
	data, err := os.ReadFile(filepath.Join(s.UploadsDir, id, "waveform.json"))
	if err != nil {
		return sm, processing.Waveform{}, err
	}
	var waveform processing.Waveform
	if err := json.Unmarshal(data, &waveform); err != nil {
		return sm, waveform, err
	}
	if err := processing.ValidateWaveform(waveform); err != nil {
		return sm, waveform, err
	}
	return sm, waveform, nil
}

func (s *Server) handleWaveform(w http.ResponseWriter, r *http.Request) {
	_, waveform, err := s.loadWaveform(r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "waveform not found")
		return
	}
	if errors.Is(err, store.ErrEditConflict) {
		writeError(w, http.StatusConflict, "waveform is not available")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load waveform")
		return
	}
	writeJSON(w, http.StatusOK, waveform)
}

func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	sm, waveform, err := s.loadWaveform(r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "sermon not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, "waveform is not ready")
		return
	}
	var regions []processing.Region
	if sm.AppliedRegions != nil {
		if err := json.Unmarshal(sm.AppliedRegions, &regions); err != nil {
			writeError(w, 500, "could not load applied edits")
			return
		}
	} else {
		regions, err = processing.AnalyzeWaveform(waveform)
		if err != nil {
			writeError(w, 500, "could not analyze waveform")
			return
		}
	}
	writeJSON(w, 200, map[string]any{"duration": waveform.Duration, "regions": regions})
}

func (s *Server) handleApplyEdits(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, waveform, err := s.loadWaveform(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "sermon not found")
		return
	}
	if err != nil {
		writeError(w, 409, "sermon is not ready for editing")
		return
	}
	var req struct {
		Regions []processing.Region `json:"regions"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, 400, "invalid edit request")
		return
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, 400, "invalid edit request")
		return
	}
	if err := processing.ValidateRegions(req.Regions, waveform.Duration); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	parameters, _ := json.Marshal(map[string]any{"duration": waveform.Duration, "regions": req.Regions})
	sm, err := s.Store.EnqueueApplyEdits(id, newUUID(), string(parameters), time.Now())
	if errors.Is(err, store.ErrEditConflict) {
		writeError(w, 409, "an edit is already running or approved")
		return
	}
	if err != nil {
		writeError(w, 500, "could not queue edits")
		return
	}
	if s.Queue != nil {
		s.Queue.Notify()
	}
	writeJSON(w, http.StatusAccepted, sm)
}

func (s *Server) handleApproveEdit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	dir := filepath.Join(s.UploadsDir, id)
	if _, err := os.Stat(filepath.Join(dir, "final.mp3")); err != nil {
		writeError(w, 409, "final audio is not ready")
		return
	}
	current, err := s.Store.GetSermon(id)
	if err == nil {
		if err := reconcileApprovalStaging(dir, current.EditApproved); err != nil {
			log.Printf("approval reconciliation %s: %v", id, err)
			writeError(w, 500, "could not approve edit")
			return
		}
		// A retry after a crash reconciles any source files left behind after
		// the approval transaction committed.
		if current.EditApproved {
			if err := cleanupApprovedSources(dir); err != nil {
				log.Printf("approval reconciliation %s: %v", id, err)
				writeError(w, 500, "could not approve edit")
				return
			}
		}
	}
	sm, err := s.Store.ApproveEdit(id)
	if err != nil {
		if errors.Is(err, store.ErrEditConflict) {
			writeError(w, 409, "edit cannot be approved")
			return
		}
		writeError(w, 500, "could not approve edit")
		return
	}
	if s.Events != nil {
		s.Events.Publish(processing.Event{Name: processing.EventStageCompleted, Sermon: sm})
	}
	if err := cleanupApprovedSources(dir); err != nil {
		log.Printf("approval cleanup %s: %v", id, err)
		writeError(w, 500, "edit approved but source cleanup failed")
		return
	}
	writeJSON(w, 200, sm)
}

func approvalSource(name string) bool {
	return name == "normalized.flac" || name == "normalized.mp3" || name == "waveform.json" || name == ".sections" || name == "original" || strings.HasPrefix(name, "original.") || strings.HasPrefix(name, ".normalization-")
}

func reconcileApprovalStaging(dir string, approved bool) error {
	staged := filepath.Join(dir, ".approval-staged")
	entries, err := os.ReadDir(staged)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if approved {
		return os.RemoveAll(staged)
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

func cleanupApprovedSources(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if approvalSource(entry.Name()) || entry.Name() == ".approval-staged" {
			if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
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

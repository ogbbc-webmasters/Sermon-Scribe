package server

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/processing"
	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

type exportResponse struct {
	JobID    string  `json:"job_id"`
	State    string  `json:"state"`
	Progress int     `json:"progress"`
	Error    *string `json:"error"`
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sm, err := s.Store.GetSermon(id)
	if err != nil {
		editingError(w, err)
		return
	}
	jobID := newUUID()
	if sm.Stage == "export" && sm.Status == "failed" {
		var job store.Job
		job, sm, err = s.Store.RetryFailedJob(id, time.Now())
		jobID = job.ID
	} else {
		sm, err = s.Store.EnqueueExport(id, jobID, time.Now())
	}
	if err != nil {
		editingError(w, err)
		return
	}
	if s.Events != nil {
		s.Events.Publish(processing.Event{Name: processing.EventProgress, Sermon: sm})
	}
	if s.Queue != nil {
		s.Queue.Notify()
	}
	writeJSON(w, http.StatusAccepted, exportResponse{JobID: jobID, State: "queued"})
}

func (s *Server) handleExportStatus(w http.ResponseWriter, r *http.Request) {
	job, err := s.Store.GetJob(r.PathValue("job"))
	if err != nil || job.SermonID != r.PathValue("id") || job.Type != "export" {
		writeError(w, http.StatusNotFound, "export not found")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("download") != "1" {
		writeJSON(w, http.StatusOK, exportResponse{JobID: job.ID, State: job.State, Progress: job.Progress, Error: job.LastError})
		return
	}
	if job.State != "done" {
		writeError(w, http.StatusConflict, "export is not ready")
		return
	}
	path := filepath.Join(s.UploadsDir, job.SermonID, "export-"+job.ID, "export.mp3")
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "export file not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load export")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load export")
		return
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Content-Disposition", `attachment; filename="sermon.mp3"`)
	http.ServeContent(w, r, "sermon.mp3", info.ModTime(), file)
}

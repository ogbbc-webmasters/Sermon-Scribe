package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/processing"
	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

func editingError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, store.ErrNotFound) {
		status = http.StatusNotFound
	}
	if errors.Is(err, store.ErrEditConflict) {
		status = http.StatusConflict
	}
	writeError(w, status, err.Error())
}
func decodeEditing(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, 400, "invalid editing JSON")
		return false
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		writeError(w, 400, "expected one JSON object")
		return false
	}
	return true
}
func (s *Server) handleEditing(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if r.Method == http.MethodGet {
		d, err := s.Store.GetEditing(id)
		if err != nil {
			editingError(w, err)
			return
		}
		writeJSON(w, 200, d)
		return
	}
	var d store.Editing
	if !decodeEditing(w, r, &d) {
		return
	}
	saved, err := s.Store.MutateEditing(id, &d, d.Revision, false, "")
	if err != nil {
		editingError(w, err)
		return
	}
	writeJSON(w, 200, saved)
}
func (s *Server) handleApplyEditing(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Revision *int  `json:"revision"`
		Skip     *bool `json:"skip"`
	}
	if !decodeEditing(w, r, &body) {
		return
	}
	if body.Revision == nil || body.Skip == nil {
		writeError(w, 400, "revision and skip are required")
		return
	}
	_, err := s.Store.MutateEditing(r.PathValue("id"), nil, *body.Revision, *body.Skip, newUUID())
	if err != nil {
		editingError(w, err)
		return
	}
	sm, err := s.Store.GetSermon(r.PathValue("id"))
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
	writeJSON(w, http.StatusAccepted, sm)
}
func (s *Server) handleEditingPreview(w http.ResponseWriter, r *http.Request) {
	d, err := s.Store.GetEditing(r.PathValue("id"))
	if err != nil {
		editingError(w, err)
		return
	}
	q := r.URL.Query()
	mode := q.Get("mode")
	var start, end float64
	if mode == "breakpoint" {
		start, err = strconv.ParseFloat(q.Get("time"), 64)
	} else {
		start, err = strconv.ParseFloat(q.Get("start"), 64)
		if err == nil {
			end, err = strconv.ParseFloat(q.Get("end"), 64)
		}
	}
	if err != nil {
		writeError(w, 400, "invalid preview times")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	audio, err := processing.PreviewAudio(ctx, filepath.Join(s.UploadsDir, r.PathValue("id"), "normalized.flac"), mode, start, end, d.Duration)
	if err != nil {
		editingError(w, err)
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(audio)
}

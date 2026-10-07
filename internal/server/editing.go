package server

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
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
func (s *Server) handleRegenerateEditing(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Revision       *int `json:"revision"`
		PreserveEdited bool `json:"preserve_edited"`
	}
	if !decodeEditing(w, r, &body) {
		return
	}
	if body.Revision == nil {
		writeError(w, 400, "revision is required")
		return
	}
	id := r.PathValue("id")
	d, err := s.Store.GetEditing(id)
	if err != nil {
		editingError(w, err)
		return
	}
	_, metadata, err := s.Store.SourceTranscription(id)
	if err != nil {
		editingError(w, err)
		return
	}
	d = processing.SpeakerDraft(d, metadata, body.PreserveEdited)
	saved, err := s.Store.MutateEditing(id, &d, *body.Revision, false, "")
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
func (s *Server) handleEditingWaveformHighRes(w http.ResponseWriter, r *http.Request) {
	path, ok := s.cachedEditingWaveform(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Waveform-Rate", "50")
	http.ServeFile(w, r, path)
}

type waveformCache struct {
	Modified    int64   `json:"modified"`
	Size        int64   `json:"size"`
	Duration    float64 `json:"duration"`
	HiresValues int     `json:"hires_values"`
}

func (s *Server) cachedEditingWaveform(w http.ResponseWriter, r *http.Request) (string, bool) {
	d, err := s.Store.GetEditing(r.PathValue("id"))
	if err != nil {
		editingError(w, err)
		return "", false
	}
	dir := filepath.Join(s.UploadsDir, r.PathValue("id"))
	source := filepath.Join(dir, "normalized.flac")
	info, err := os.Stat(source)
	if err != nil {
		writeError(w, http.StatusNotFound, "source audio is not available")
		return "", false
	}
	cache := filepath.Join(dir, "waveform-hires.json")
	hiresPath := filepath.Join(dir, "waveform-hires.bin")
	var wave waveformCache
	raw, _ := os.ReadFile(cache)
	cacheValid := json.Unmarshal(raw, &wave) == nil && wave.Modified == info.ModTime().UnixNano() && wave.Size == info.Size() && wave.Duration == d.Duration && wave.HiresValues > 0
	hiresInfo, hiresErr := os.Stat(hiresPath)
	if !cacheValid || hiresErr != nil || hiresInfo.Size() != int64(wave.HiresValues*2) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		hires, err := processing.WaveformPeaks(ctx, source, d.Duration)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not generate waveform")
			return "", false
		}
		encoded := make([]byte, len(hires)*2)
		for i, sample := range hires {
			binary.LittleEndian.PutUint16(encoded[i*2:], uint16(sample))
		}
		if err = os.WriteFile(hiresPath, encoded, 0600); err != nil {
			writeError(w, http.StatusInternalServerError, "could not cache waveform")
			return "", false
		}
		wave.Modified, wave.Size, wave.Duration = info.ModTime().UnixNano(), info.Size(), d.Duration
		wave.HiresValues = len(hires)
		if raw, err = json.Marshal(wave); err == nil {
			// A missing or incomplete cache is regenerated on the next request.
			_ = os.WriteFile(cache, raw, 0600)
		}
	}
	return hiresPath, true
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

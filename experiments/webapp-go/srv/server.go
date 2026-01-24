package srv

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

//go:embed index.html
var staticFiles embed.FS

const DailyLimit = 20

type Server struct {
	APIKey string
	DB     *DB
	Worker *Worker

	mu         sync.Mutex
	usageDate  string // YYYY-MM-DD
	usageCount int    // total requests today
}

func New(apiKey string, db *DB) *Server {
	s := &Server{
		APIKey: apiKey,
		DB:     db,
	}
	s.Worker = NewWorker(db, apiKey)
	return s
}

func (s *Server) Serve(addr string) error {
	// Reset any jobs that were interrupted by a server restart
	if count, err := s.DB.ResetStaleJobs(); err != nil {
		slog.Error("failed to reset stale jobs", "error", err)
	} else if count > 0 {
		slog.Info("reset stale jobs", "count", count)
	}

	// Start background worker
	s.Worker.Start()
	defer s.Worker.Stop()

	mux := http.NewServeMux()
	
	// Static
	mux.HandleFunc("GET /{$}", s.handleIndex)
	
	// Usage
	mux.HandleFunc("GET /api/usage", s.requireAuth(s.handleUsage))
	
	// Sermons
	mux.HandleFunc("GET /api/sermons", s.handleListSermons)
	mux.HandleFunc("POST /api/sermons", s.requireAuth(s.handleCreateSermon))
	mux.HandleFunc("GET /api/sermons/{id}", s.handleGetSermon)
	mux.HandleFunc("DELETE /api/sermons/{id}", s.requireAuth(s.handleDeleteSermon))
	
	// Audio
	mux.HandleFunc("GET /api/sermons/{id}/audio", s.handleSermonAudio)
	
	// Jobs
	mux.HandleFunc("GET /api/sermons/{id}/jobs", s.handleListJobs)
	mux.HandleFunc("GET /api/jobs/{id}", s.handleGetJob)
	mux.HandleFunc("GET /api/jobs/{id}/stream", s.handleJobStream)
	mux.HandleFunc("POST /api/jobs/{id}/retry", s.requireAuth(s.handleRetryJob))

	slog.Info("starting server", "addr", addr, "dailyLimit", DailyLimit)
	return http.ListenAndServe(addr, mux)
}

// requireAuth wraps a handler to require exe.dev authentication
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("X-Exedev-Userid")
		if userID == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Login required"})
			return
		}
		next(w, r)
	}
}

// checkAndIncrementUsage returns true if site is within daily limit
func (s *Server) checkAndIncrementUsage() (current int, allowed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	today := time.Now().Format("2006-01-02")

	// Reset count if it's a new day
	if s.usageDate != today {
		s.usageDate = today
		s.usageCount = 0
		slog.Info("reset daily usage counter", "date", today)
	}

	if s.usageCount >= DailyLimit {
		return s.usageCount, false
	}

	s.usageCount++
	return s.usageCount, true
}

// getUsage returns current site-wide usage
func (s *Server) getUsage() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	if s.usageDate != today {
		return 0
	}
	return s.usageCount
}

// handleUsage returns the site-wide usage
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"used":  s.getUsage(),
		"limit": DailyLimit,
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	data, _ := staticFiles.ReadFile("index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

// MetadataResponse is the response from metadata extraction
type MetadataResponse struct {
	Title           string   `json:"title"`
	TitleGenerated  bool     `json:"title_generated"`
	TitleReasoning  string   `json:"title_reasoning"`
	Speaker         string   `json:"speaker"`
	Scriptures      []string `json:"scriptures"`
	Topics          []string `json:"topics"`
	TopicsReasoning map[string]string `json:"topics_reasoning"`
	Error           string   `json:"error,omitempty"`
}

// handleListSermons returns all sermons
func (s *Server) handleListSermons(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	sermons, err := s.DB.ListSermons(100, 0)
	if err != nil {
		slog.Error("failed to list sermons", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to list sermons"})
		return
	}

	// Get the latest job for each sermon to include status
	type SermonWithJob struct {
		*Sermon
		LatestJob *Job `json:"latest_job,omitempty"`
	}

	results := make([]SermonWithJob, len(sermons))
	for i, sermon := range sermons {
		results[i] = SermonWithJob{Sermon: sermon}
		jobs, err := s.DB.GetJobsForSermon(sermon.ID)
		if err == nil && len(jobs) > 0 {
			results[i].LatestJob = jobs[0]
		}
	}

	json.NewEncoder(w).Encode(results)
}

// handleGetSermon returns a single sermon by ID
func (s *Server) handleGetSermon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	id := r.PathValue("id")
	sermon, err := s.DB.GetSermon(id)
	if err != nil {
		slog.Error("failed to get sermon", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to get sermon"})
		return
	}
	if sermon == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Sermon not found"})
		return
	}

	// Include jobs
	jobs, _ := s.DB.GetJobsForSermon(sermon.ID)

	json.NewEncoder(w).Encode(map[string]any{
		"sermon": sermon,
		"jobs":   jobs,
	})
}

// handleCreateSermon uploads audio and creates a processing job
func (s *Server) handleCreateSermon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Check rate limit
	used, allowed := s.checkAndIncrementUsage()
	if !allowed {
		userID := r.Header.Get("X-Exedev-Userid")
		slog.Warn("rate limit exceeded", "userID", userID, "used", used)
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("Daily site limit reached (%d/%d). Try again tomorrow.", used, DailyLimit),
		})
		return
	}

	userID := r.Header.Get("X-Exedev-Userid")
	slog.Info("creating sermon", "userID", userID, "usage", fmt.Sprintf("%d/%d", used, DailyLimit))

	// Parse multipart form (max 500MB)
	if err := r.ParseMultipartForm(500 << 20); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to parse upload: " + err.Error()})
		return
	}

	file, header, err := r.FormFile("audio")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "No audio file provided"})
		return
	}
	defer file.Close()

	slog.Info("received audio file", "name", header.Filename, "size", header.Size)

	// Create sermon record
	sermon, err := s.DB.CreateSermon(header.Filename, header.Size)
	if err != nil {
		slog.Error("failed to create sermon", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create sermon"})
		return
	}

	// Save audio file
	uploadsDir := filepath.Join("uploads", sermon.ID)
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		slog.Error("failed to create uploads dir", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save file"})
		return
	}

	ext := filepath.Ext(header.Filename)
	if ext == "" {
		ext = ".mp3"
	}
	audioPath := filepath.Join(uploadsDir, "original"+ext)

	f, err := os.Create(audioPath)
	if err != nil {
		slog.Error("failed to create audio file", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save file"})
		return
	}
	if _, err := io.Copy(f, file); err != nil {
		f.Close()
		slog.Error("failed to write audio file", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save file"})
		return
	}
	f.Close()

	// Create processing job
	job, err := s.DB.CreateJob(sermon.ID, JobTypeProcess)
	if err != nil {
		slog.Error("failed to create job", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create job"})
		return
	}

	slog.Info("created sermon and job", "sermonID", sermon.ID, "jobID", job.ID)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"sermon": sermon,
		"job":    job,
	})
}

// handleDeleteSermon deletes a sermon and its files
func (s *Server) handleDeleteSermon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	id := r.PathValue("id")

	// Delete from database (cascades to jobs)
	if err := s.DB.DeleteSermon(id); err != nil {
		slog.Error("failed to delete sermon", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to delete sermon"})
		return
	}

	// Delete files
	uploadsDir := filepath.Join("uploads", id)
	os.RemoveAll(uploadsDir)

	slog.Info("deleted sermon", "id", id)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleSermonAudio serves the audio file for a sermon
func (s *Server) handleSermonAudio(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	uploadsDir := filepath.Join("uploads", id)

	// Look for original.* file (could be .mp3 or .wav)
	var audioPath string
	var contentType string
	for _, ext := range []string{".mp3", ".wav"} {
		path := filepath.Join(uploadsDir, "original"+ext)
		if _, err := os.Stat(path); err == nil {
			audioPath = path
			if ext == ".mp3" {
				contentType = "audio/mpeg"
			} else {
				contentType = "audio/wav"
			}
			break
		}
	}

	if audioPath == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Audio file not found"})
		return
	}

	w.Header().Set("Content-Type", contentType)
	http.ServeFile(w, r, audioPath)
}

// handleListJobs returns jobs for a sermon
func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	sermonID := r.PathValue("id")
	jobs, err := s.DB.GetJobsForSermon(sermonID)
	if err != nil {
		slog.Error("failed to list jobs", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to list jobs"})
		return
	}

	json.NewEncoder(w).Encode(jobs)
}

// handleGetJob returns a job by ID
func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	id := r.PathValue("id")
	job, err := s.DB.GetJob(id)
	if err != nil {
		slog.Error("failed to get job", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to get job"})
		return
	}
	if job == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Job not found"})
		return
	}

	json.NewEncoder(w).Encode(job)
}

// handleJobStream streams job progress via SSE
func (s *Server) handleJobStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Check if job exists
	job, err := s.DB.GetJob(id)
	if err != nil || job == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Job not found"})
		return
	}

	// Set up SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Send current state immediately
	sendJobSSE(w, job)

	// If already complete or error, we're done
	if job.Status == JobStatusComplete || job.Status == JobStatusError {
		return
	}

	// Subscribe to updates
	ch := s.Worker.Subscribe(id)
	defer s.Worker.Unsubscribe(id, ch)

	// Stream updates
	for {
		select {
		case <-r.Context().Done():
			return
		case update, ok := <-ch:
			if !ok {
				return
			}
			sendJobSSE(w, update)
			if update.Status == JobStatusComplete || update.Status == JobStatusError {
				return
			}
		}
	}
}

func sendJobSSE(w http.ResponseWriter, job *Job) {
	data, _ := json.Marshal(job)
	fmt.Fprintf(w, "data: %s\n\n", data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// handleRetryJob resets a failed job to pending so it can be retried
func (s *Server) handleRetryJob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	id := r.PathValue("id")
	job, err := s.DB.GetJob(id)
	if err != nil {
		slog.Error("failed to get job", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to get job"})
		return
	}
	if job == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Job not found"})
		return
	}

	if job.Status != JobStatusError {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Only failed jobs can be retried"})
		return
	}

	if err := s.DB.RetryJob(id); err != nil {
		slog.Error("failed to retry job", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to retry job"})
		return
	}

	slog.Info("job queued for retry", "jobID", id)

	// Return updated job
	job, _ = s.DB.GetJob(id)
	json.NewEncoder(w).Encode(job)
}

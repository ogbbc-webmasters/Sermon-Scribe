package srv

import (
	"encoding/json"
	"time"
)

// Sermon represents a processed sermon with metadata
type Sermon struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	TitleGenerated bool      `json:"title_generated"`
	TitleReasoning string    `json:"title_reasoning"`
	Speaker        string    `json:"speaker"`
	Scriptures     []string  `json:"scriptures"`
	Topics         []string  `json:"topics"`
	Transcript     string    `json:"transcript"`
	Filename       string    `json:"filename"`
	FileSize       int64     `json:"file_size"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// JobStatus represents the state of a job
type JobStatus string

const (
	JobStatusPending    JobStatus = "pending"
	JobStatusProcessing JobStatus = "processing"
	JobStatusComplete   JobStatus = "complete"
	JobStatusError      JobStatus = "error"
)

// JobType represents the type of processing job
type JobType string

const (
	JobTypeProcess JobType = "process" // Full pipeline: transcribe + extract metadata
)

// Job represents a background processing job
type Job struct {
	ID          string    `json:"id"`
	SermonID    string    `json:"sermon_id"`
	Type        JobType   `json:"type"`
	Status      JobStatus `json:"status"`
	Progress    string    `json:"progress"`
	Percent     int       `json:"percent"` // 0-100, -1 for indeterminate
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// SermonRow is the database representation of a Sermon
type SermonRow struct {
	ID             string
	Title          string
	TitleGenerated bool
	TitleReasoning string
	Speaker        string
	ScripturesJSON string
	TopicsJSON     string
	Transcript     string
	Filename       string
	FileSize       int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ToSermon converts a database row to a Sermon
func (r *SermonRow) ToSermon() (*Sermon, error) {
	s := &Sermon{
		ID:             r.ID,
		Title:          r.Title,
		TitleGenerated: r.TitleGenerated,
		TitleReasoning: r.TitleReasoning,
		Speaker:        r.Speaker,
		Transcript:     r.Transcript,
		Filename:       r.Filename,
		FileSize:       r.FileSize,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}

	if r.ScripturesJSON != "" {
		if err := json.Unmarshal([]byte(r.ScripturesJSON), &s.Scriptures); err != nil {
			return nil, err
		}
	}
	if r.TopicsJSON != "" {
		if err := json.Unmarshal([]byte(r.TopicsJSON), &s.Topics); err != nil {
			return nil, err
		}
	}

	return s, nil
}

// Checkpoint stores intermediate processing state for job resumption
type Checkpoint struct {
	Stage           string   `json:"stage"`            // "splitting", "transcribing", "extracting_metadata"
	ChunkCount      int      `json:"chunk_count"`      // Total number of chunks
	Transcripts     []string `json:"transcripts"`      // Transcripts for completed chunks (indexed by chunk number)
	FullTranscript  string   `json:"full_transcript"`  // Combined transcript (set when all chunks done)
}

// JobRow is the database representation of a Job
type JobRow struct {
	ID             string
	SermonID       string
	Type           string
	Status         string
	Progress       string
	Percent        int
	Error          string
	CheckpointJSON string
	CreatedAt      time.Time
	StartedAt      *time.Time
	CompletedAt    *time.Time
}

// ToJob converts a database row to a Job
func (r *JobRow) ToJob() *Job {
	return &Job{
		ID:          r.ID,
		SermonID:    r.SermonID,
		Type:        JobType(r.Type),
		Status:      JobStatus(r.Status),
		Progress:    r.Progress,
		Percent:     r.Percent,
		Error:       r.Error,
		CreatedAt:   r.CreatedAt,
		StartedAt:   r.StartedAt,
		CompletedAt: r.CompletedAt,
	}
}

// GetCheckpoint parses the checkpoint JSON from a JobRow
func (r *JobRow) GetCheckpoint() (*Checkpoint, error) {
	var cp Checkpoint
	if r.CheckpointJSON == "" || r.CheckpointJSON == "{}" {
		return &cp, nil
	}
	if err := json.Unmarshal([]byte(r.CheckpointJSON), &cp); err != nil {
		return nil, err
	}
	return &cp, nil
}

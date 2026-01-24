package srv

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

// DB wraps the SQLite database connection
type DB struct {
	db *sql.DB
}

// OpenDB opens or creates the SQLite database
func OpenDB(path string) (*DB, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	d := &DB{db: db}
	if err := d.migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return d, nil
}

// Close closes the database connection
func (d *DB) Close() error {
	return d.db.Close()
}

// migrate creates or updates the database schema
func (d *DB) migrate() error {
	_, err := d.db.Exec(`
		CREATE TABLE IF NOT EXISTS sermons (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL DEFAULT '',
			title_generated INTEGER NOT NULL DEFAULT 0,
			speaker TEXT NOT NULL DEFAULT '',
			scriptures_json TEXT NOT NULL DEFAULT '[]',
			topics_json TEXT NOT NULL DEFAULT '[]',
			transcript TEXT NOT NULL DEFAULT '',
			filename TEXT NOT NULL,
			file_size INTEGER NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);

		CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			sermon_id TEXT NOT NULL REFERENCES sermons(id) ON DELETE CASCADE,
			type TEXT NOT NULL,
			status TEXT NOT NULL,
			progress TEXT NOT NULL DEFAULT '',
			percent INTEGER NOT NULL DEFAULT -1,
			error TEXT NOT NULL DEFAULT '',
			checkpoint_json TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME NOT NULL,
			started_at DATETIME,
			completed_at DATETIME
		);

		CREATE INDEX IF NOT EXISTS idx_jobs_sermon_id ON jobs(sermon_id);
		CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
	`)
	if err != nil {
		return err
	}

	// Add checkpoint_json column if it doesn't exist (migration for existing DBs)
	d.db.Exec(`ALTER TABLE jobs ADD COLUMN checkpoint_json TEXT NOT NULL DEFAULT '{}'`)

	// Add title_reasoning column if it doesn't exist (migration for existing DBs)
	d.db.Exec(`ALTER TABLE sermons ADD COLUMN title_reasoning TEXT NOT NULL DEFAULT ''`)

	// Add topics_reasoning column if it doesn't exist (migration for existing DBs)
	d.db.Exec(`ALTER TABLE sermons ADD COLUMN topics_reasoning TEXT NOT NULL DEFAULT ''`)

	return nil
}

// CreateSermon creates a new sermon record
func (d *DB) CreateSermon(filename string, fileSize int64) (*Sermon, error) {
	id := uuid.New().String()
	now := time.Now()

	_, err := d.db.Exec(`
		INSERT INTO sermons (id, filename, file_size, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, id, filename, fileSize, now, now)
	if err != nil {
		return nil, err
	}

	return &Sermon{
		ID:        id,
		Filename:  filename,
		FileSize:  fileSize,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// GetSermon retrieves a sermon by ID
func (d *DB) GetSermon(id string) (*Sermon, error) {
	row := d.db.QueryRow(`
		SELECT id, title, title_generated, title_reasoning, speaker, scriptures_json, topics_json,
		       topics_reasoning, transcript, filename, file_size, created_at, updated_at
		FROM sermons WHERE id = ?
	`, id)

	var r SermonRow
	err := row.Scan(&r.ID, &r.Title, &r.TitleGenerated, &r.TitleReasoning, &r.Speaker,
		&r.ScripturesJSON, &r.TopicsJSON, &r.TopicsReasoningJSON, &r.Transcript, &r.Filename,
		&r.FileSize, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return r.ToSermon()
}

// ListSermons returns all sermons, most recent first
func (d *DB) ListSermons(limit, offset int) ([]*Sermon, error) {
	rows, err := d.db.Query(`
		SELECT id, title, title_generated, title_reasoning, speaker, scriptures_json, topics_json,
		       topics_reasoning, transcript, filename, file_size, created_at, updated_at
		FROM sermons
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sermons []*Sermon
	for rows.Next() {
		var r SermonRow
		err := rows.Scan(&r.ID, &r.Title, &r.TitleGenerated, &r.TitleReasoning, &r.Speaker,
			&r.ScripturesJSON, &r.TopicsJSON, &r.TopicsReasoningJSON, &r.Transcript, &r.Filename,
			&r.FileSize, &r.CreatedAt, &r.UpdatedAt)
		if err != nil {
			return nil, err
		}
		s, err := r.ToSermon()
		if err != nil {
			return nil, err
		}
		sermons = append(sermons, s)
	}

	return sermons, rows.Err()
}

// UpdateSermonMetadata updates the extracted metadata for a sermon
func (d *DB) UpdateSermonMetadata(id string, title string, titleGenerated bool, titleReasoning string, speaker string, scriptures, topics []string, topicsReasoning map[string]string, transcript string) error {
	scripturesJSON, _ := json.Marshal(scriptures)
	topicsJSON, _ := json.Marshal(topics)
	topicsReasoningJSON, _ := json.Marshal(topicsReasoning)

	_, err := d.db.Exec(`
		UPDATE sermons
		SET title = ?, title_generated = ?, title_reasoning = ?, speaker = ?, scriptures_json = ?,
		    topics_json = ?, topics_reasoning = ?, transcript = ?, updated_at = ?
		WHERE id = ?
	`, title, titleGenerated, titleReasoning, speaker, string(scripturesJSON), string(topicsJSON), string(topicsReasoningJSON), transcript, time.Now(), id)
	return err
}

// DeleteSermon deletes a sermon and its associated jobs
func (d *DB) DeleteSermon(id string) error {
	_, err := d.db.Exec(`DELETE FROM sermons WHERE id = ?`, id)
	return err
}

// CreateJob creates a new job for a sermon
func (d *DB) CreateJob(sermonID string, jobType JobType) (*Job, error) {
	id := uuid.New().String()
	now := time.Now()

	_, err := d.db.Exec(`
		INSERT INTO jobs (id, sermon_id, type, status, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, id, sermonID, string(jobType), string(JobStatusPending), now)
	if err != nil {
		return nil, err
	}

	return &Job{
		ID:        id,
		SermonID:  sermonID,
		Type:      jobType,
		Status:    JobStatusPending,
		Percent:   -1,
		CreatedAt: now,
	}, nil
}

// GetJob retrieves a job by ID
func (d *DB) GetJob(id string) (*Job, error) {
	row := d.db.QueryRow(`
		SELECT id, sermon_id, type, status, progress, percent, error,
		       created_at, started_at, completed_at
		FROM jobs WHERE id = ?
	`, id)

	var r JobRow
	err := row.Scan(&r.ID, &r.SermonID, &r.Type, &r.Status, &r.Progress,
		&r.Percent, &r.Error, &r.CreatedAt, &r.StartedAt, &r.CompletedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return r.ToJob(), nil
}

// GetJobsForSermon retrieves all jobs for a sermon
func (d *DB) GetJobsForSermon(sermonID string) ([]*Job, error) {
	rows, err := d.db.Query(`
		SELECT id, sermon_id, type, status, progress, percent, error,
		       created_at, started_at, completed_at
		FROM jobs WHERE sermon_id = ?
		ORDER BY created_at DESC
	`, sermonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*Job
	for rows.Next() {
		var r JobRow
		err := rows.Scan(&r.ID, &r.SermonID, &r.Type, &r.Status, &r.Progress,
			&r.Percent, &r.Error, &r.CreatedAt, &r.StartedAt, &r.CompletedAt)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, r.ToJob())
	}

	return jobs, rows.Err()
}

// ResetStaleJobs resets any jobs stuck in 'processing' state back to 'pending'
// This should be called on server startup to recover from crashes
func (d *DB) ResetStaleJobs() (int64, error) {
	result, err := d.db.Exec(`
		UPDATE jobs SET status = ?, started_at = NULL
		WHERE status = ?
	`, string(JobStatusPending), string(JobStatusProcessing))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// GetPendingJobWithCheckpoint retrieves the oldest pending job with its checkpoint and marks it as processing
func (d *DB) GetPendingJobWithCheckpoint() (*Job, *Checkpoint, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	row := tx.QueryRow(`
		SELECT id, sermon_id, type, status, progress, percent, error, checkpoint_json,
		       created_at, started_at, completed_at
		FROM jobs
		WHERE status = ?
		ORDER BY created_at ASC
		LIMIT 1
	`, string(JobStatusPending))

	var r JobRow
	err = row.Scan(&r.ID, &r.SermonID, &r.Type, &r.Status, &r.Progress,
		&r.Percent, &r.Error, &r.CheckpointJSON, &r.CreatedAt, &r.StartedAt, &r.CompletedAt)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}

	now := time.Now()
	_, err = tx.Exec(`
		UPDATE jobs SET status = ?, started_at = ? WHERE id = ?
	`, string(JobStatusProcessing), now, r.ID)
	if err != nil {
		return nil, nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}

	job := r.ToJob()
	job.Status = JobStatusProcessing
	job.StartedAt = &now

	checkpoint, err := r.GetCheckpoint()
	if err != nil {
		return nil, nil, err
	}

	return job, checkpoint, nil
}

// SaveCheckpoint saves the checkpoint state for a job
func (d *DB) SaveCheckpoint(jobID string, checkpoint *Checkpoint) error {
	data, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	_, err = d.db.Exec(`UPDATE jobs SET checkpoint_json = ? WHERE id = ?`, string(data), jobID)
	return err
}

// UpdateJobProgress updates the progress of a job
func (d *DB) UpdateJobProgress(id string, progress string, percent int) error {
	_, err := d.db.Exec(`
		UPDATE jobs SET progress = ?, percent = ? WHERE id = ?
	`, progress, percent, id)
	return err
}

// CompleteJob marks a job as complete
func (d *DB) CompleteJob(id string) error {
	now := time.Now()
	_, err := d.db.Exec(`
		UPDATE jobs SET status = ?, progress = 'Complete', percent = 100, completed_at = ? WHERE id = ?
	`, string(JobStatusComplete), now, id)
	return err
}

// FailJob marks a job as failed with an error message
func (d *DB) FailJob(id string, errMsg string) error {
	now := time.Now()
	_, err := d.db.Exec(`
		UPDATE jobs SET status = ?, error = ?, completed_at = ? WHERE id = ?
	`, string(JobStatusError), errMsg, now, id)
	return err
}

// RetryJob resets a failed job to pending so it can be resumed from checkpoint
func (d *DB) RetryJob(id string) error {
	_, err := d.db.Exec(`
		UPDATE jobs SET status = ?, error = '', started_at = NULL, completed_at = NULL
		WHERE id = ? AND status = ?
	`, string(JobStatusPending), id, string(JobStatusError))
	return err
}

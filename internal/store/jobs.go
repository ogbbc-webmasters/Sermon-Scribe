package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const maxJobAttempts = 3

// ErrNoJob indicates that no queued job of a requested type is ready.
var ErrNoJob = errors.New("no job ready")

// ErrNotRetryable indicates that a sermon has no failed current-stage job.
var ErrNotRetryable = errors.New("sermon has no failed job to retry")

// ErrNotRerunnable indicates that normalization is not currently complete.
var ErrNotRerunnable = errors.New("sermon normalization is not ready to rerun")

// Job is one persistent unit of background work.
type Job struct {
	ID          string
	SermonID    string
	Type        string
	Stage       string
	State       string
	Attempts    int
	Progress    int
	Checkpoint  *string
	Parameters  string
	LastError   *string
	AvailableAt time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NewJob describes a job to enqueue as part of a stage transition.
type NewJob struct {
	ID         string
	SermonID   string
	Type       string
	Stage      string
	Parameters string
}

// JobError is one retained failed execution attempt.
type JobError struct {
	Attempt   int
	Error     string
	CreatedAt time.Time
}

// StartUpload moves a newly-created sermon into upload/running.
func (s *Store) StartUpload(id string) error {
	res, err := s.db.Exec(
		`UPDATE sermons SET status = 'running'
		 WHERE id = ? AND stage = 'upload' AND status = 'pending'`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CompleteUpload atomically advances a stored upload to normalization/pending
// and enqueues its normalize job.
func (s *Store) CompleteUpload(sermonID, jobID string, now time.Time) (Sermon, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Sermon{}, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`UPDATE sermons SET stage = 'normalization', status = 'pending'
		 WHERE id = ? AND stage = 'upload' AND status = 'running'`, sermonID)
	if err != nil {
		return Sermon{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Sermon{}, err
	}
	if n == 0 {
		return Sermon{}, ErrNotFound
	}

	job := NewJob{
		ID: jobID, SermonID: sermonID, Type: "normalize", Stage: "normalization",
		Parameters: `{"gate_adjustment":0,"volume_adjustment":0}`,
	}
	if err := enqueueJobTx(tx, job, now); err != nil {
		return Sermon{}, err
	}
	sm, err := getSermon(tx, sermonID)
	if err != nil {
		return Sermon{}, err
	}
	if err := tx.Commit(); err != nil {
		return Sermon{}, err
	}
	return sm, nil
}

// EnqueueJob adds a queued job. Stage-specific handlers can use this when a
// completed stage schedules another machine stage.
func (s *Store) EnqueueJob(job NewJob, now time.Time) error {
	return enqueueJobTx(s.db, job, now)
}

// EnqueueNormalizationRerun returns a completed normalization stage to pending
// and queues a new normalize job carrying the adjusted settings.
func (s *Store) EnqueueNormalizationRerun(sermonID, jobID, parameters string, now time.Time) (Sermon, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Sermon{}, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`UPDATE sermons SET status = 'pending'
		 WHERE id = ? AND stage = 'normalization' AND status = 'done'`, sermonID)
	if err != nil {
		return Sermon{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Sermon{}, err
	}
	if n == 0 {
		return Sermon{}, ErrNotRerunnable
	}
	if err := enqueueJobTx(tx, NewJob{
		ID: jobID, SermonID: sermonID, Type: "normalize", Stage: "normalization",
		Parameters: parameters,
	}, now); err != nil {
		return Sermon{}, err
	}
	sm, err := getSermon(tx, sermonID)
	if err != nil {
		return Sermon{}, err
	}
	return sm, tx.Commit()
}

// SetNormalizationAdjustments records the settings that produced the committed audio.
func (s *Store) SetNormalizationAdjustments(sermonID string, gate, volume int) error {
	res, err := s.db.Exec(
		`UPDATE sermons
		 SET normalization_gate_adjustment = ?, normalization_volume_adjustment = ?
		 WHERE id = ?`, gate, volume, sermonID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func enqueueJobTx(q sqlExecer, job NewJob, now time.Time) error {
	if job.Parameters == "" {
		job.Parameters = "{}"
	}
	ts := now.UTC().Format(time.RFC3339Nano)
	_, err := q.Exec(
		`INSERT INTO jobs
		 (id, sermon_id, type, stage, state, attempts, progress, parameters, available_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 'queued', 0, 0, ?, ?, ?, ?)`,
		job.ID, job.SermonID, job.Type, job.Stage, job.Parameters, ts, ts, ts,
	)
	return err
}

// DiscardInterruptedUploads removes upload records that cannot resume after a
// process restart. The caller removes the corresponding partial directories.
func (s *Store) DiscardInterruptedUploads() ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(
		`SELECT id FROM sermons
		 WHERE stage = 'upload' AND status IN ('pending', 'running')`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(
		`DELETE FROM sermons
		 WHERE stage = 'upload' AND status IN ('pending', 'running')`); err != nil {
		return nil, err
	}
	return ids, tx.Commit()
}

// RecoverRunningJobs returns work abandoned by a stopped process to the queue.
func (s *Store) RecoverRunningJobs() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`UPDATE sermons SET status = 'pending'
		 WHERE status = 'running' AND EXISTS (
			SELECT 1 FROM jobs
			WHERE jobs.sermon_id = sermons.id
			  AND jobs.stage = sermons.stage
			  AND jobs.state = 'running'
		)`); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE jobs
		 SET state = 'queued',
		     attempts = CASE WHEN attempts > 0 THEN attempts - 1 ELSE 0 END,
		     updated_at = ?
		 WHERE state = 'running'`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

// ClaimNextJob atomically claims the oldest ready job whose type has a
// registered handler. Claiming increments the current execution-cycle attempt.
func (s *Store) ClaimNextJob(ctx context.Context, types []string, now time.Time) (Job, error) {
	if len(types) == 0 {
		return Job{}, ErrNoJob
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(types)), ",")
	query := `UPDATE jobs
		SET state = 'running', attempts = attempts + 1, updated_at = ?
		WHERE id = (
			SELECT id FROM jobs
			WHERE state = 'queued' AND available_at <= ? AND type IN (` + placeholders + `)
			ORDER BY available_at, created_at, id LIMIT 1
		) AND state = 'queued'
		RETURNING id, sermon_id, type, stage, state, attempts, progress,
		          checkpoint, parameters, last_error, available_at, created_at, updated_at`
	args := []any{now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano)}
	for _, typ := range types {
		args = append(args, typ)
	}

	job, err := scanJob(tx.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNoJob
	}
	if err != nil {
		return Job{}, err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE sermons SET stage = ?, status = 'running' WHERE id = ?`,
		job.Stage, job.SermonID)
	if err != nil {
		return Job{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Job{}, err
	}
	if n == 0 {
		return Job{}, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	return job, nil
}

// UpdateJobProgress persists progress and an optional checkpoint for a running
// job, returning the updated sermon view for event publication.
func (s *Store) UpdateJobProgress(id string, progress int, checkpoint *string, now time.Time) (Sermon, error) {
	if progress < -1 || progress > 100 {
		return Sermon{}, fmt.Errorf("progress %d outside -1..100", progress)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Sermon{}, err
	}
	defer tx.Rollback()

	var sermonID string
	err = tx.QueryRow(
		`UPDATE jobs SET progress = ?, checkpoint = ?, updated_at = ?
		 WHERE id = ? AND state = 'running'
		 RETURNING sermon_id`,
		progress, checkpoint, now.UTC().Format(time.RFC3339Nano), id).Scan(&sermonID)
	if err != nil {
		return Sermon{}, err
	}
	sm, err := getSermon(tx, sermonID)
	if err != nil {
		return Sermon{}, err
	}
	return sm, tx.Commit()
}

// RequeueJob records a transient error and schedules another automatic attempt.
func (s *Store) RequeueJob(job Job, message string, availableAt, now time.Time) (Sermon, error) {
	return s.recordJobFailure(job, message, "queued", availableAt, now)
}

// FailJob records an exhausted attempt cycle and exposes failure on the sermon.
func (s *Store) FailJob(job Job, message string, now time.Time) (Sermon, error) {
	return s.recordJobFailure(job, message, "failed", now, now)
}

func (s *Store) recordJobFailure(job Job, message, state string, availableAt, now time.Time) (Sermon, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Sermon{}, err
	}
	defer tx.Rollback()

	ts := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(
		`INSERT INTO job_errors (job_id, attempt, error, created_at) VALUES (?, ?, ?, ?)`,
		job.ID, job.Attempts, message, ts); err != nil {
		return Sermon{}, err
	}
	res, err := tx.Exec(
		`UPDATE jobs SET state = ?, last_error = ?, available_at = ?, updated_at = ?
		 WHERE id = ? AND state = 'running'`,
		state, message, availableAt.UTC().Format(time.RFC3339Nano), ts, job.ID)
	if err != nil {
		return Sermon{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Sermon{}, err
	}
	if n == 0 {
		return Sermon{}, ErrNotFound
	}
	if state == "failed" {
		if _, err := tx.Exec(
			`UPDATE sermons SET status = 'failed' WHERE id = ? AND stage = ?`,
			job.SermonID, job.Stage); err != nil {
			return Sermon{}, err
		}
	}
	sm, err := getSermon(tx, job.SermonID)
	if err != nil {
		return Sermon{}, err
	}
	return sm, tx.Commit()
}

// CompleteJob commits a successful job. If next is nil the current stage is
// terminal; otherwise the sermon advances and the supplied next job is queued.
func (s *Store) CompleteJob(job Job, next *NewJob, now time.Time) (Sermon, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Sermon{}, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`UPDATE jobs SET state = 'done', progress = 100, last_error = NULL, updated_at = ?
		 WHERE id = ? AND state = 'running'`,
		now.UTC().Format(time.RFC3339Nano), job.ID)
	if err != nil {
		return Sermon{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Sermon{}, err
	}
	if n == 0 {
		return Sermon{}, ErrNotFound
	}

	if next == nil {
		_, err = tx.Exec(
			`UPDATE sermons SET stage = ?, status = 'done' WHERE id = ?`,
			job.Stage, job.SermonID)
	} else {
		next.SermonID = job.SermonID
		_, err = tx.Exec(
			`UPDATE sermons SET stage = ?, status = 'pending' WHERE id = ?`,
			next.Stage, job.SermonID)
		if err == nil {
			err = enqueueJobTx(tx, *next, now)
		}
	}
	if err != nil {
		return Sermon{}, err
	}
	sm, err := getSermon(tx, job.SermonID)
	if err != nil {
		return Sermon{}, err
	}
	return sm, tx.Commit()
}

// RetryFailedJob resets the same failed current-stage job for a fresh
// three-attempt execution cycle while preserving checkpoints and error history.
func (s *Store) RetryFailedJob(sermonID string, now time.Time) (Job, Sermon, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Job{}, Sermon{}, err
	}
	defer tx.Rollback()

	var jobID string
	err = tx.QueryRow(
		`SELECT j.id FROM jobs j JOIN sermons s ON s.id = j.sermon_id
		 WHERE s.id = ? AND s.status = 'failed' AND j.stage = s.stage AND j.state = 'failed'
		 ORDER BY j.created_at DESC, j.id DESC LIMIT 1`, sermonID).Scan(&jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, Sermon{}, ErrNotRetryable
	}
	if err != nil {
		return Job{}, Sermon{}, err
	}

	ts := now.UTC().Format(time.RFC3339Nano)
	job, err := scanJob(tx.QueryRow(
		`UPDATE jobs SET state = 'queued', attempts = 0, progress = 0,
		                  last_error = NULL, available_at = ?, updated_at = ?
		 WHERE id = ? AND state = 'failed'
		 RETURNING id, sermon_id, type, stage, state, attempts, progress,
		           checkpoint, parameters, last_error, available_at, created_at, updated_at`,
		ts, ts, jobID))
	if err != nil {
		return Job{}, Sermon{}, err
	}
	if _, err := tx.Exec(
		`UPDATE sermons SET status = 'pending' WHERE id = ?`, sermonID); err != nil {
		return Job{}, Sermon{}, err
	}
	sm, err := getSermon(tx, sermonID)
	if err != nil {
		return Job{}, Sermon{}, err
	}
	return job, sm, tx.Commit()
}

// GetJob returns a job by id.
func (s *Store) GetJob(id string) (Job, error) {
	return scanJob(s.db.QueryRow(
		`SELECT id, sermon_id, type, stage, state, attempts, progress,
		        checkpoint, parameters, last_error, available_at, created_at, updated_at
		 FROM jobs WHERE id = ?`, id))
}

// GetCurrentJob returns the newest job for the sermon's current stage.
func (s *Store) GetCurrentJob(sermonID string) (Job, error) {
	return scanJob(s.db.QueryRow(
		`SELECT j.id, j.sermon_id, j.type, j.stage, j.state, j.attempts, j.progress,
		        j.checkpoint, j.parameters, j.last_error, j.available_at, j.created_at, j.updated_at
		 FROM jobs j JOIN sermons s ON s.id = j.sermon_id AND s.stage = j.stage
		 WHERE s.id = ? ORDER BY j.created_at DESC, j.id DESC LIMIT 1`, sermonID))
}

// ListJobErrors returns retained failures oldest first.
func (s *Store) ListJobErrors(jobID string) ([]JobError, error) {
	rows, err := s.db.Query(
		`SELECT attempt, error, created_at FROM job_errors
		 WHERE job_id = ? ORDER BY id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []JobError
	for rows.Next() {
		var item JobError
		var created string
		if err := rows.Scan(&item.Attempt, &item.Error, &created); err != nil {
			return nil, err
		}
		item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func scanJob(row rowScanner) (Job, error) {
	var job Job
	var available, created, updated string
	err := row.Scan(
		&job.ID, &job.SermonID, &job.Type, &job.Stage, &job.State,
		&job.Attempts, &job.Progress, &job.Checkpoint, &job.Parameters,
		&job.LastError, &available, &created, &updated,
	)
	if err != nil {
		return Job{}, err
	}
	var parseErr error
	job.AvailableAt, parseErr = time.Parse(time.RFC3339Nano, available)
	if parseErr != nil {
		return Job{}, parseErr
	}
	job.CreatedAt, parseErr = time.Parse(time.RFC3339Nano, created)
	if parseErr != nil {
		return Job{}, parseErr
	}
	job.UpdatedAt, parseErr = time.Parse(time.RFC3339Nano, updated)
	if parseErr != nil {
		return Job{}, parseErr
	}
	return job, nil
}

// MaxJobAttempts is the fixed automatic attempt budget per execution cycle.
func MaxJobAttempts() int { return maxJobAttempts }

// Package store owns all SQLite access: opening the database, running
// migrations, and CRUD on sermons. No SQL lives outside this package.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	_ "modernc.org/sqlite"
)

// Sermon is one uploaded recording and its pipeline state.
type Sermon struct {
	ID                            string          `json:"id"`
	OriginalFilename              string          `json:"original_filename"`
	UploadedAt                    string          `json:"uploaded_at"`
	UploadedBy                    *string         `json:"uploaded_by"`
	Stage                         string          `json:"stage"`
	Status                        string          `json:"status"`
	Progress                      int             `json:"progress"`
	Error                         *string         `json:"error"`
	NormalizationGateAdjustment   int             `json:"normalization_gate_adjustment"`
	NormalizationVolumeAdjustment int             `json:"normalization_volume_adjustment"`
	AppliedRegions                json.RawMessage `json:"applied_regions,omitempty"`
	EditApproved                  bool            `json:"edit_approved"`
}

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path, configures
// it, and applies any pending migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// One connection keeps SQLite connection-level PRAGMAs consistent while
	// still allowing worker handlers themselves to run concurrently.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode = WAL; PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure database: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.RecoverRunningJobs(); err != nil {
		db.Close()
		return nil, fmt.Errorf("recover running jobs: %w", err)
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// CreateSermon inserts a new sermon row.
func (s *Store) CreateSermon(sm Sermon) error {
	_, err := s.db.Exec(
		`INSERT INTO sermons (id, original_filename, uploaded_at, uploaded_by, stage, status)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sm.ID, sm.OriginalFilename, sm.UploadedAt, sm.UploadedBy, sm.Stage, sm.Status,
	)
	return err
}

const sermonViewSQL = `
	SELECT s.id, s.original_filename, s.uploaded_at, s.uploaded_by,
	       s.stage, s.status, COALESCE(j.progress, 0), j.last_error,
	       s.normalization_gate_adjustment, s.normalization_volume_adjustment,
	       s.applied_regions, s.edit_approved
	FROM sermons s
	LEFT JOIN jobs j ON j.id = (
		SELECT id FROM jobs
		WHERE sermon_id = s.id AND stage = s.stage
		ORDER BY created_at DESC, id DESC LIMIT 1
	)`

// ListSermons returns all sermons, newest first.
func (s *Store) ListSermons() ([]Sermon, error) {
	rows, err := s.db.Query(sermonViewSQL + ` ORDER BY s.uploaded_at DESC, s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sermons := []Sermon{}
	for rows.Next() {
		var sm Sermon
		if err := scanSermon(rows, &sm); err != nil {
			return nil, err
		}
		sermons = append(sermons, sm)
	}
	return sermons, rows.Err()
}

// ErrNotFound is returned when a sermon or job does not exist.
var ErrNotFound = sql.ErrNoRows

// GetSermon returns the sermon with the given id, or ErrNotFound.
func (s *Store) GetSermon(id string) (Sermon, error) {
	return getSermon(s.db, id)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func getSermon(q interface {
	QueryRow(query string, args ...any) *sql.Row
}, id string) (Sermon, error) {
	var sm Sermon
	err := scanSermon(q.QueryRow(sermonViewSQL+` WHERE s.id = ?`, id), &sm)
	return sm, err
}

func scanSermon(row rowScanner, sm *Sermon) error {
	var applied []byte
	err := row.Scan(
		&sm.ID, &sm.OriginalFilename, &sm.UploadedAt, &sm.UploadedBy,
		&sm.Stage, &sm.Status, &sm.Progress, &sm.Error,
		&sm.NormalizationGateAdjustment, &sm.NormalizationVolumeAdjustment,
		&applied, &sm.EditApproved,
	)
	if len(applied) > 0 {
		sm.AppliedRegions = json.RawMessage(applied)
	}
	return err
}

// DeleteSermon removes the sermon row with the given id. It reports whether
// a row was deleted. If cleanup is non-nil it runs after the row deletion
// but before the transaction commits; if cleanup fails, the deletion is
// rolled back so the row is preserved for a retry.
func (s *Store) DeleteSermon(id string, cleanup func() error) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`DELETE FROM sermons WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	if cleanup != nil {
		if err := cleanup(); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

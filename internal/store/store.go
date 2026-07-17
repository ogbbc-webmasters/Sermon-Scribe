// Package store owns all SQLite access: opening the database, running
// migrations, and CRUD on sermons. No SQL lives outside this package.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Sermon is one uploaded recording and its pipeline state.
type Sermon struct {
	ID               string  `json:"id"`
	OriginalFilename string  `json:"original_filename"`
	UploadedAt       string  `json:"uploaded_at"`
	UploadedBy       *string `json:"uploaded_by"`
	Stage            string  `json:"stage"`
	Status           string  `json:"status"`
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
	if _, err := db.Exec(`PRAGMA journal_mode = WAL; PRAGMA foreign_keys = ON;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure database: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
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

// ListSermons returns all sermons, newest first.
func (s *Store) ListSermons() ([]Sermon, error) {
	rows, err := s.db.Query(
		`SELECT id, original_filename, uploaded_at, uploaded_by, stage, status
		 FROM sermons ORDER BY uploaded_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sermons := []Sermon{}
	for rows.Next() {
		var sm Sermon
		if err := rows.Scan(&sm.ID, &sm.OriginalFilename, &sm.UploadedAt, &sm.UploadedBy, &sm.Stage, &sm.Status); err != nil {
			return nil, err
		}
		sermons = append(sermons, sm)
	}
	return sermons, rows.Err()
}

// ErrNotFound is returned when a sermon id does not exist.
var ErrNotFound = sql.ErrNoRows

// GetSermon returns the sermon with the given id, or ErrNotFound.
func (s *Store) GetSermon(id string) (Sermon, error) {
	var sm Sermon
	err := s.db.QueryRow(
		`SELECT id, original_filename, uploaded_at, uploaded_by, stage, status
		 FROM sermons WHERE id = ?`, id).
		Scan(&sm.ID, &sm.OriginalFilename, &sm.UploadedAt, &sm.UploadedBy, &sm.Stage, &sm.Status)
	return sm, err
}

// DeleteSermon removes the sermon row with the given id. It reports whether
// a row was deleted.
func (s *Store) DeleteSermon(id string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM sermons WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

package store

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

type Breakpoint struct {
	ID         string   `json:"id"`
	Time       float64  `json:"time"`
	Kind       string   `json:"kind"`
	Edited     bool     `json:"edited,omitempty"`
	SourceTime *float64 `json:"source_time,omitempty"`
}
type Section struct {
	ID   string `json:"id"`
	Keep bool   `json:"keep"`
}
type Editing struct {
	Duration    float64      `json:"duration"`
	Revision    int          `json:"revision"`
	Breakpoints []Breakpoint `json:"breakpoints"`
	Sections    []Section    `json:"sections"`
}
type EditSnapshot struct {
	Draft    Editing               `json:"draft"`
	Skip     bool                  `json:"skip"`
	Text     string                `json:"text"`
	Metadata TranscriptionMetadata `json:"metadata"`
}

func (s *Store) SaveSourceTranscription(id, text string, m TranscriptionMetadata) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO source_transcriptions VALUES(?,?,?) ON CONFLICT(sermon_id) DO UPDATE SET text=excluded.text,metadata=excluded.metadata`, id, text, string(raw))
	return err
}
func (s *Store) SourceTranscription(id string) (string, TranscriptionMetadata, error) {
	var text, raw string
	var m TranscriptionMetadata
	err := s.db.QueryRow(`SELECT text,metadata FROM source_transcriptions WHERE sermon_id=?`, id).Scan(&text, &raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &m)
	}
	return text, m, err
}

func (d Editing) Validate() error {
	if math.IsNaN(d.Duration) || math.IsInf(d.Duration, 0) || d.Duration < 0.05 || len(d.Breakpoints) < 2 || len(d.Sections) != len(d.Breakpoints)-1 {
		return fmt.Errorf("invalid duration or section count")
	}
	seen := map[string]bool{}
	for i, b := range d.Breakpoints {
		if b.ID == "" || seen[b.ID] || math.IsNaN(b.Time) || math.IsInf(b.Time, 0) || b.Time < 0 || b.Time > d.Duration {
			return fmt.Errorf("invalid breakpoint")
		}
		seen[b.ID] = true
		switch b.Kind {
		case "start", "end", "manual", "silence_start", "silence_end", "speaker", "singing_start", "singing_end":
		default:
			return fmt.Errorf("invalid breakpoint kind")
		}
		if i > 0 && b.Time-d.Breakpoints[i-1].Time < 0.05-1e-9 {
			return fmt.Errorf("breakpoints must have 0.05 second gaps")
		}
		if i > 0 && i < len(d.Breakpoints)-1 && (b.Kind == "start" || b.Kind == "end") {
			return fmt.Errorf("interior endpoint")
		}
	}
	first, last := d.Breakpoints[0], d.Breakpoints[len(d.Breakpoints)-1]
	if first.Time != 0 || first.Kind != "start" || last.Time != d.Duration || last.Kind != "end" {
		return fmt.Errorf("invalid recording endpoints")
	}
	for _, s := range d.Sections {
		if s.ID == "" || seen[s.ID] {
			return fmt.Errorf("duplicate or empty section id")
		}
		seen[s.ID] = true
	}
	return nil
}

func (s *Store) GetEditing(id string) (Editing, error) {
	var raw string
	var d Editing
	err := s.db.QueryRow(`SELECT draft FROM editing WHERE sermon_id=?`, id).Scan(&raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &d)
	}
	return d, err
}
func (s *Store) PrepareEditing(id string, d Editing, text string, m TranscriptionMetadata) error {
	if err := d.Validate(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var rev int
	if err := tx.QueryRow(`SELECT COALESCE((SELECT revision FROM editing WHERE sermon_id=?),0)`, id).Scan(&rev); err != nil {
		return err
	}
	d.Revision = rev + 1
	raw, _ := json.Marshal(d)
	meta, _ := json.Marshal(m)
	_, err = tx.Exec(`INSERT INTO editing(sermon_id,draft,revision,source_text,source_metadata) VALUES(?,?,?,?,?) ON CONFLICT(sermon_id) DO UPDATE SET draft=excluded.draft,revision=excluded.revision,source_text=excluded.source_text,source_metadata=excluded.source_metadata`, id, string(raw), d.Revision, text, string(meta))
	if err != nil {
		return err
	}
	return tx.Commit()
}

// MutateEditing serializes draft saves and snapshot creation against all jobs.
func (s *Store) MutateEditing(id string, d *Editing, revision int, skip bool, jobID string) (Editing, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Editing{}, err
	}
	defer tx.Rollback()
	var raw, text, meta string
	var rev int
	err = tx.QueryRow(`SELECT draft,revision,source_text,source_metadata FROM editing WHERE sermon_id=?`, id).Scan(&raw, &rev, &text, &meta)
	if err != nil {
		return Editing{}, err
	}
	var old Editing
	if err = json.Unmarshal([]byte(raw), &old); err != nil {
		return old, err
	}
	var idle bool
	err = tx.QueryRow(`SELECT ((stage='editing' AND status='done') OR (stage='metadata' AND status IN ('done','failed'))) AND NOT EXISTS(SELECT 1 FROM jobs WHERE sermon_id=? AND state IN ('queued','running')) FROM sermons WHERE id=?`, id, id).Scan(&idle)
	if err != nil {
		return old, err
	}
	if !idle || revision != rev {
		return old, ErrEditConflict
	}
	if d != nil {
		if err = d.Validate(); err != nil {
			return old, err
		}
		if d.Duration != old.Duration || d.Breakpoints[0] != old.Breakpoints[0] || d.Breakpoints[len(d.Breakpoints)-1] != old.Breakpoints[len(old.Breakpoints)-1] {
			return old, fmt.Errorf("recording endpoints are immutable")
		}
		d.Revision = rev + 1
		rawBytes, _ := json.Marshal(d)
		_, err = tx.Exec(`UPDATE editing SET draft=?,revision=? WHERE sermon_id=?`, string(rawBytes), d.Revision, id)
		old = *d
	} else {
		snap := EditSnapshot{Draft: old, Skip: skip, Text: text}
		if err = json.Unmarshal([]byte(meta), &snap.Metadata); err != nil {
			return old, err
		}
		kept := skip
		cuts := false
		for _, section := range old.Sections {
			kept = kept || section.Keep
			cuts = cuts || !section.Keep
		}
		if !kept {
			return old, fmt.Errorf("at least one section must be kept")
		}
		if !skip && cuts && len(snap.Metadata.Words) == 0 && len(snap.Metadata.Segments) == 0 {
			return old, fmt.Errorf("transcript timing is unavailable; regenerate transcription before applying cuts")
		}
		parameters, _ := json.Marshal(snap)
		err = enqueueJobTx(tx, NewJob{ID: jobID, SermonID: id, Type: "render_edit", Stage: "editing", Parameters: string(parameters)}, time.Now())
		if err == nil {
			_, err = tx.Exec(`UPDATE sermons SET stage='editing',status='pending' WHERE id=?`, id)
		}
	}
	if err != nil {
		return old, err
	}
	return old, tx.Commit()
}
func (s *Store) EditingPlayback(id string) (string, error) {
	var path string
	err := s.db.QueryRow(`SELECT CASE WHEN active THEN playback ELSE '' END FROM editing WHERE sermon_id=?`, id).Scan(&path)
	if err == ErrNotFound {
		return "", nil
	}
	return path, err
}
func (s *Store) CommitEditing(id, path, text string, m TranscriptionMetadata, active bool) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE editing SET playback=?,active=? WHERE sermon_id=?`, path, active, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE sermons SET transcript=?,transcription_metadata=? WHERE id=?`, text, string(raw), id); err != nil {
		return err
	}
	return tx.Commit()
}

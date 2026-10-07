package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"time"
)

// ExportSnapshot pins the applied audio and settings while the export runs.
type ExportSnapshot struct {
	Source           string `json:"source"`
	GateAdjustment   int    `json:"gate_adjustment"`
	VolumeAdjustment int    `json:"volume_adjustment"`
	processingReturnState
}

func (s *Store) EnqueueExport(sermonID, jobID string, now time.Time) (Sermon, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Sermon{}, err
	}
	defer tx.Rollback()
	sm, err := getSermon(tx, sermonID)
	if err != nil {
		return Sermon{}, err
	}
	if !((sm.Stage == "editing" && sm.Status == "done") ||
		(sm.Stage == "metadata" && (sm.Status == "done" || sm.Status == "failed"))) {
		return Sermon{}, ErrEditConflict
	}
	var playback string
	if err := tx.QueryRow(`SELECT CASE WHEN active THEN playback ELSE '' END FROM editing WHERE sermon_id=?`, sermonID).Scan(&playback); err != nil && !errors.Is(err, ErrNotFound) {
		return Sermon{}, err
	}
	source := "normalized.flac"
	if playback != "" {
		source = filepath.Join(filepath.Dir(playback), "edited.flac")
	}
	snapshot := ExportSnapshot{
		Source:         source,
		GateAdjustment: sm.NormalizationGateAdjustment, VolumeAdjustment: sm.NormalizationVolumeAdjustment,
		processingReturnState: processingReturnState{Stage: sm.Stage, Status: sm.Status},
	}
	parameters, err := json.Marshal(snapshot)
	if err != nil {
		return Sermon{}, err
	}
	res, err := tx.Exec(`UPDATE sermons SET stage='export',status='pending'
		WHERE id=? AND NOT EXISTS (SELECT 1 FROM jobs WHERE sermon_id=? AND state IN ('queued','running'))`, sermonID, sermonID)
	if err != nil {
		return Sermon{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Sermon{}, err
	}
	if n == 0 {
		return Sermon{}, ErrEditConflict
	}
	if err := enqueueJobTx(tx, NewJob{ID: jobID, SermonID: sermonID, Type: "export", Stage: "export", Parameters: string(parameters)}, now); err != nil {
		return Sermon{}, err
	}
	sm, err = getSermon(tx, sermonID)
	if err != nil {
		return Sermon{}, err
	}
	return sm, tx.Commit()
}

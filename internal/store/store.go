// Package store owns all SQLite access: opening the database, running
// migrations, and CRUD on sermons. No SQL lives outside this package.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	_ "modernc.org/sqlite"
)

// Sermon is one uploaded recording and its pipeline state.
type Sermon struct {
	ID                            string                 `json:"id"`
	OriginalFilename              string                 `json:"original_filename"`
	UploadedAt                    string                 `json:"uploaded_at"`
	UploadedBy                    *string                `json:"uploaded_by"`
	Stage                         string                 `json:"stage"`
	Status                        string                 `json:"status"`
	Progress                      int                    `json:"progress"`
	Error                         *string                `json:"error"`
	NormalizationGateAdjustment   int                    `json:"normalization_gate_adjustment"`
	NormalizationVolumeAdjustment int                    `json:"normalization_volume_adjustment"`
	NormalizationReviewed         bool                   `json:"normalization_reviewed"`
	AppliedRegions                json.RawMessage        `json:"applied_regions,omitempty"`
	EditApproved                  bool                   `json:"edit_approved"`
	EditingDuration               *float64               `json:"editing_duration,omitempty"`
	PlaybackVersion               string                 `json:"playback_version,omitempty"`
	Transcript                    *string                `json:"transcript,omitempty"`
	Title                         *string                `json:"title,omitempty"`
	TitleGenerated                *bool                  `json:"title_generated,omitempty"`
	TitleReasoning                *string                `json:"title_reasoning,omitempty"`
	Speaker                       *string                `json:"speaker,omitempty"`
	OldTestamentReading           string                 `json:"old_testament_reading,omitempty"`
	NewTestamentReading           string                 `json:"new_testament_reading,omitempty"`
	Scriptures                    []string               `json:"scriptures,omitempty"`
	ScriptureOptions              []string               `json:"scripture_options,omitempty"`
	Topics                        []string               `json:"topics,omitempty"`
	TopicScores                   map[string]float64     `json:"topic_scores,omitempty"`
	TranscriptionMetadata         *TranscriptionMetadata `json:"transcription_metadata,omitempty"`
	SourceTranscriptionMetadata   *TranscriptionMetadata `json:"source_transcription_metadata,omitempty"`
	AICosts                       []AICost               `json:"ai_costs"`
}

// TranscriptionMetadata contains provider-supplied timing and diarization.
type TranscriptionMetadata struct {
	Language string              `json:"language,omitempty"`
	Duration float64             `json:"duration,omitempty"`
	Segments []TranscriptSegment `json:"segments,omitempty"`
	Words    []TranscriptWord    `json:"words,omitempty"`
}

// TranscriptSegment is one timed phrase in the transcript.
type TranscriptSegment struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Text    string  `json:"text"`
	Speaker *int    `json:"speaker,omitempty"`
}

// TranscriptWord is one timed word in the transcript.
type TranscriptWord struct {
	Word         string   `json:"word"`
	Start        float64  `json:"start"`
	End          float64  `json:"end"`
	Speaker      *int     `json:"speaker,omitempty"`
	SpeakerLabel string   `json:"speaker_label,omitempty"`
	Confidence   *float64 `json:"confidence,omitempty"`
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
	       s.normalization_reviewed, s.applied_regions, s.edit_approved,
	       COALESCE(t.text, s.transcript),
	       CASE WHEN t.sermon_id IS NOT NULL THEN t.metadata ELSE s.transcription_metadata END,
	       s.title, s.title_generated, s.title_reasoning,
	       s.speaker, s.old_testament_reading, s.new_testament_reading,
	       s.scriptures, s.scripture_options, s.topics, s.topic_scores, e.draft,
	       CASE WHEN e.active THEN e.playback ELSE '' END,
	       (SELECT json_group_array(json_object('task', task, 'model', model,
	           'calls', calls, 'cost_usd', cost_usd, 'unknown_costs', unknown_costs))
	        FROM (SELECT task, model, COUNT(*) AS calls, COALESCE(SUM(cost_usd), 0) AS cost_usd,
	              COUNT(*) - COUNT(cost_usd) AS unknown_costs
	              FROM ai_calls WHERE sermon_id = s.id GROUP BY task, model ORDER BY task, model)),
	       CASE WHEN e.active THEN e.applied_draft END
	FROM sermons s
	LEFT JOIN source_transcriptions t ON t.sermon_id = s.id
	LEFT JOIN editing e ON e.sermon_id = s.id
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
	var transcript, transcriptionMetadata, title, titleReasoning, speaker, oldTestamentReading, newTestamentReading, scriptures, scriptureOptions, topics, topicScores sql.NullString
	var titleGenerated sql.NullBool
	var editingDraft, appliedDraft sql.NullString
	var costs string
	err := row.Scan(
		&sm.ID, &sm.OriginalFilename, &sm.UploadedAt, &sm.UploadedBy,
		&sm.Stage, &sm.Status, &sm.Progress, &sm.Error,
		&sm.NormalizationGateAdjustment, &sm.NormalizationVolumeAdjustment,
		&sm.NormalizationReviewed, &applied, &sm.EditApproved,
		&transcript, &transcriptionMetadata, &title, &titleGenerated, &titleReasoning, &speaker,
		&oldTestamentReading, &newTestamentReading, &scriptures, &scriptureOptions, &topics, &topicScores, &editingDraft, &sm.PlaybackVersion, &costs, &appliedDraft,
	)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(costs), &sm.AICosts); err != nil {
		return fmt.Errorf("decode AI costs: %w", err)
	}
	if editingDraft.Valid {
		var draft Editing
		if err := json.Unmarshal([]byte(editingDraft.String), &draft); err != nil {
			return fmt.Errorf("decode editing draft for sermon %s: %w", sm.ID, err)
		}
		duration := 0.0
		for i, section := range draft.Sections {
			if section.Keep {
				duration += draft.Breakpoints[i+1].Time - draft.Breakpoints[i].Time
			}
		}
		sm.EditingDuration = &duration
	}
	if len(applied) > 0 {
		sm.AppliedRegions = json.RawMessage(applied)
	}
	if transcript.Valid {
		sm.Transcript = &transcript.String
	}
	if transcriptionMetadata.Valid {
		var metadata TranscriptionMetadata
		if err := json.Unmarshal([]byte(transcriptionMetadata.String), &metadata); err != nil {
			return fmt.Errorf("decode transcription metadata for sermon %s: %w", sm.ID, err)
		}
		sm.TranscriptionMetadata = &metadata
	}
	sm.SourceTranscriptionMetadata = sm.TranscriptionMetadata
	if appliedDraft.Valid && sm.TranscriptionMetadata != nil {
		var draft Editing
		if err := json.Unmarshal([]byte(appliedDraft.String), &draft); err != nil {
			return fmt.Errorf("decode applied draft for sermon %s: %w", sm.ID, err)
		}
		var ranges []AudioRange
		cuts := false
		for i, section := range draft.Sections {
			if section.Keep {
				start, end := draft.Breakpoints[i].Time, draft.Breakpoints[i+1].Time
				if len(ranges) > 0 && ranges[len(ranges)-1].End == start {
					ranges[len(ranges)-1].End = end
				} else {
					ranges = append(ranges, AudioRange{Start: start, End: end})
				}
			} else {
				cuts = true
			}
		}
		if cuts {
			text, metadata := MapTranscript(*sm.TranscriptionMetadata, ranges)
			sm.Transcript, sm.TranscriptionMetadata = &text, &metadata
		}
	}
	if title.Valid {
		title.String = titleCase(title.String)
		sm.Title = &title.String
	}
	if titleGenerated.Valid {
		sm.TitleGenerated = &titleGenerated.Bool
	}
	if titleReasoning.Valid {
		sm.TitleReasoning = &titleReasoning.String
	}
	if speaker.Valid {
		sm.Speaker = &speaker.String
	}
	if oldTestamentReading.Valid {
		sm.OldTestamentReading = oldTestamentReading.String
	}
	if newTestamentReading.Valid {
		sm.NewTestamentReading = newTestamentReading.String
	}
	_ = json.Unmarshal([]byte(scriptures.String), &sm.Scriptures)
	_ = json.Unmarshal([]byte(scriptureOptions.String), &sm.ScriptureOptions)
	_ = json.Unmarshal([]byte(topics.String), &sm.Topics)
	_ = json.Unmarshal([]byte(topicScores.String), &sm.TopicScores)
	return err
}

// SaveMetadata stores structured metadata produced by the extraction stage.
func (s *Store) SaveMetadata(id, title string, generated bool, reasoning, speaker string, scriptures, topics []string, topicScores map[string]float64) error {
	return s.SaveMetadataWithScriptureOptions(id, title, generated, reasoning, speaker, "", "", scriptures, scriptures, topics, topicScores)
}

// SaveMetadataWithScriptureOptions stores metadata and all scripture candidates.
func (s *Store) SaveMetadataWithScriptureOptions(id, title string, generated bool, reasoning, speaker, oldTestamentReading, newTestamentReading string, scriptures, scriptureOptions, topics []string, topicScores map[string]float64) error {
	title = titleCase(title)
	scripturesJSON, err := json.Marshal(scriptures)
	if err != nil {
		return err
	}
	scriptureOptionsJSON, err := json.Marshal(scriptureOptions)
	if err != nil {
		return err
	}
	topicsJSON, err := json.Marshal(topics)
	if err != nil {
		return err
	}
	scoreJSON, err := json.Marshal(topicScores)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE sermons SET title=?, title_generated=?, title_reasoning=?, speaker=?, old_testament_reading=?, new_testament_reading=?, scriptures=?, scripture_options=?, topics=?, topic_scores=? WHERE id=?`,
		title, generated, reasoning, speaker, oldTestamentReading, newTestamentReading, string(scripturesJSON), string(scriptureOptionsJSON), string(topicsJSON), string(scoreJSON), id)
	return err
}

// SaveTitle replaces only title metadata after a targeted retry.
func (s *Store) SaveTitle(id, title string, generated bool, reasoning string) error {
	_, err := s.db.Exec(`UPDATE sermons SET title=?, title_generated=?, title_reasoning=? WHERE id=?`, titleCase(title), generated, reasoning, id)
	return err
}

var titleWords = regexp.MustCompile(`[\p{L}\p{N}]+(?:['’][\p{L}\p{N}]+)*`)

// titleCase applies English headline casing without rewriting the quote's
// punctuation, spacing, or existing acronyms.
func titleCase(title string) string {
	words := titleWords.FindAllStringIndex(title, -1)
	var result strings.Builder
	end := 0
	for i, word := range words {
		separator := title[end:word[0]]
		result.WriteString(separator)
		value := title[word[0]:word[1]]
		small := false
		switch strings.ToLower(value) {
		case "a", "an", "and", "as", "at", "but", "by", "for", "from", "in", "nor", "of", "on", "or", "the", "to", "via", "with":
			small = true
		}
		if small && i > 0 && i < len(words)-1 && !strings.ContainsAny(separator, ":.!?") {
			value = strings.ToLower(value)
		} else {
			runes := []rune(value)
			runes[0] = unicode.ToUpper(runes[0])
			value = string(runes)
		}
		result.WriteString(value)
		end = word[1]
	}
	result.WriteString(title[end:])
	return result.String()
}

// SaveTopics replaces only topic labels and scores after a targeted retry.
func (s *Store) SaveTopics(id string, topics []string, scores map[string]float64) error {
	labels, err := json.Marshal(topics)
	if err != nil {
		return err
	}
	values, err := json.Marshal(scores)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE sermons SET topics=?, topic_scores=? WHERE id=?`, string(labels), string(values), id)
	return err
}

// SaveScriptures replaces only the selected primary references.
func (s *Store) SaveScriptures(id string, scriptures []string) error {
	references, err := json.Marshal(scriptures)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE sermons SET scriptures=? WHERE id=?`, string(references), id)
	return err
}

// SaveScriptureSelection stores the selected references and their candidates.
func (s *Store) SaveScriptureSelection(id, oldTestamentReading, newTestamentReading string, selected, options []string) error {
	selectedJSON, err := json.Marshal(selected)
	if err != nil {
		return err
	}
	optionsJSON, err := json.Marshal(options)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE sermons SET old_testament_reading=?, new_testament_reading=?, scriptures=?, scripture_options=? WHERE id=?`, oldTestamentReading, newTestamentReading, string(selectedJSON), string(optionsJSON), id)
	return err
}

// MarkNormalizationReviewed durably records that the user accepted the
// completed normalization before entering the timeline editor.
func (s *Store) MarkNormalizationReviewed(id string) (Sermon, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Sermon{}, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(
		`UPDATE sermons SET normalization_reviewed = 1
		 WHERE id = ? AND stage = 'normalization' AND status = 'done'`, id)
	if err != nil {
		return Sermon{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Sermon{}, err
	}
	if n == 0 {
		if _, err := getSermon(tx, id); err != nil {
			return Sermon{}, err
		}
		return Sermon{}, ErrEditConflict
	}
	sm, err := getSermon(tx, id)
	if err != nil {
		return Sermon{}, err
	}
	return sm, tx.Commit()
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

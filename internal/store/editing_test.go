package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func editingFixture() Editing {
	return Editing{Duration: 10, Breakpoints: []Breakpoint{{ID: "start", Kind: "start"}, {ID: "middle", Time: 5, Kind: "manual"}, {ID: "end", Time: 10, Kind: "end"}}, Sections: []Section{{"a", true}, {"b", true}}}
}

func TestPlaybackVersionTracksCommittedGeneration(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateSermon(Sermon{ID: "playback", Stage: "editing", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	check := func(want string) {
		t.Helper()
		sm, err := st.GetSermon("playback")
		if err != nil || sm.PlaybackVersion != want {
			t.Fatalf("detail playback version: %q, want %q: %v", sm.PlaybackVersion, want, err)
		}
		list, err := st.ListSermons()
		if err != nil || len(list) != 1 || list[0].PlaybackVersion != want {
			t.Fatalf("snapshot playback version: %+v: %v", list, err)
		}
	}
	check("")
	if err := st.PrepareEditing("playback", editingFixture(), "source", TranscriptionMetadata{}); err != nil {
		t.Fatal(err)
	}
	check("")
	for _, path := range []string{"edit-first/edited.mp3", "edit-second/edited.mp3"} {
		if err := st.CommitEditing("playback", path, editingFixture(), true); err != nil {
			t.Fatal(err)
		}
		check(path)
	}
	if _, err := st.MutateEditing("playback", nil, 1, false, "render-next"); err != nil {
		t.Fatal(err)
	}
	check("edit-second/edited.mp3")
	if err := st.CommitEditing("playback", "", editingFixture(), false); err != nil {
		t.Fatal(err)
	}
	check("")
}

func TestEditingDurationUsesKeptSections(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateSermon(Sermon{ID: "duration", Stage: "editing", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ cut, want float64 }{{1799.75, 5400.25}, {1800, 5400}, {1800.25, 5399.75}} {
		d := Editing{Duration: 7200, Breakpoints: []Breakpoint{{ID: "start", Kind: "start"}, {ID: "cut", Time: tc.cut, Kind: "manual"}, {ID: "end", Time: 7200, Kind: "end"}}, Sections: []Section{{"delete", false}, {"keep", true}}}
		if err := st.PrepareEditing("duration", d, "source", TranscriptionMetadata{Duration: 12}); err != nil {
			t.Fatal(err)
		}
		sm, err := st.GetSermon("duration")
		if err != nil || sm.EditingDuration == nil || *sm.EditingDuration != tc.want {
			t.Fatalf("kept duration: %v, want %v: %v", sm.EditingDuration, tc.want, err)
		}
		list, err := st.ListSermons()
		if err != nil || len(list) != 1 || list[0].EditingDuration == nil || *list[0].EditingDuration != tc.want {
			t.Fatalf("listed kept duration: %+v %v", list, err)
		}
	}
}

func TestSourceTranscriptTakesPrecedenceOverLegacyOutput(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	source := TranscriptionMetadata{Duration: 10, Words: []TranscriptWord{{Word: "source", Start: 6, End: 7}}}
	applied := TranscriptionMetadata{Duration: 4, Words: []TranscriptWord{{Word: "kept", Start: 1, End: 2}}}
	for _, tc := range []struct {
		id       string
		text     string
		metadata *TranscriptionMetadata
	}{
		{"source-only", "source recording", &source},
		{"applied", "source recording", &source},
		{"legacy", "source recording", &source},
	} {
		t.Run(tc.id, func(t *testing.T) {
			if err := st.CreateSermon(Sermon{ID: tc.id, Stage: "editing", Status: "done"}); err != nil {
				t.Fatal(err)
			}
			if err := st.SaveSourceTranscription(tc.id, "source recording", source); err != nil {
				t.Fatal(err)
			}
			if tc.id == "applied" {
				raw, _ := json.Marshal(applied)
				if _, err := st.db.Exec(`UPDATE sermons SET transcript='kept output',transcription_metadata=? WHERE id=?`, string(raw), tc.id); err != nil {
					t.Fatal(err)
				}
			} else if tc.id == "legacy" {
				if _, err := st.db.Exec(`UPDATE sermons SET transcript='legacy output' WHERE id=?`, tc.id); err != nil {
					t.Fatal(err)
				}
			}
			check := func(sm Sermon) {
				t.Helper()
				if sm.Transcript == nil || *sm.Transcript != tc.text || !reflect.DeepEqual(sm.TranscriptionMetadata, tc.metadata) {
					t.Fatalf("transcript/timing mismatch: %+v", sm)
				}
				if sm.Stage != "editing" || sm.Status != "done" {
					t.Fatal("reading transcript advanced the pipeline")
				}
			}
			sm, err := st.GetSermon(tc.id)
			if err != nil {
				t.Fatal(err)
			}
			check(sm)
			list, err := st.ListSermons()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, sm := range list {
				if sm.ID == tc.id {
					check(sm)
					found = true
				}
			}
			if !found {
				t.Fatal("sermon missing from list")
			}
			var uncommitted bool
			if err := st.db.QueryRow(`SELECT transcript IS NULL FROM sermons WHERE id = ?`, tc.id).Scan(&uncommitted); err != nil {
				t.Fatal(err)
			}
			if uncommitted != (tc.id == "source-only") {
				t.Fatal("reading source transcript changed committed output")
			}
		})
	}
}

func TestEditingValidation(t *testing.T) {
	for _, mutate := range []func(*Editing){
		func(d *Editing) { d.Duration = math.NaN() }, func(d *Editing) { d.Breakpoints[1].Time = math.Inf(1) },
		func(d *Editing) { d.Breakpoints[1].Time = 0.049 }, func(d *Editing) { d.Breakpoints[1].ID = "start" },
		func(d *Editing) { d.Sections[0].ID = "start" }, func(d *Editing) { d.Sections = d.Sections[:1] },
		func(d *Editing) { d.Breakpoints[1].Kind = "unknown" }, func(d *Editing) { d.Breakpoints[2].Time = 9 },
	} {
		d := editingFixture()
		mutate(&d)
		if d.Validate() == nil {
			t.Fatalf("accepted invalid %+v", d)
		}
	}
	d := editingFixture()
	d.Breakpoints[1].Time = 0.05
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
}
func TestEditingConflictsSnapshotAndPreservation(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.CreateSermon(Sermon{ID: "s", Stage: "metadata", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	if err = st.PrepareEditing("s", editingFixture(), "source", TranscriptionMetadata{Duration: 10, Words: []TranscriptWord{{Word: "source", Start: 6, End: 7}}}); err != nil {
		t.Fatal(err)
	}
	d, _ := st.GetEditing("s")
	revision := d.Revision
	d.Sections[0].Keep = false
	d, err = st.MutateEditing("s", &d, revision, false, "")
	if err != nil || d.Revision != revision+1 {
		t.Fatalf("save %v %+v", err, d)
	}
	if _, err = st.MutateEditing("s", &d, revision, false, ""); !errors.Is(err, ErrEditConflict) {
		t.Fatalf("stale save %v", err)
	}
	changed := editingFixture()
	changed.Revision = d.Revision
	changed.Breakpoints[0].ID = "other"
	if _, err = st.MutateEditing("s", &changed, d.Revision, false, ""); err == nil {
		t.Fatal("endpoint identity changed")
	}
	sm, _ := st.GetSermon("s")
	if *sm.Transcript != "source" {
		t.Fatal("draft changed source transcript")
	}
	if _, err = st.MutateEditing("s", nil, d.Revision, false, "render"); err != nil {
		t.Fatal(err)
	}
	if _, err = st.MutateEditing("s", &d, d.Revision, false, ""); !errors.Is(err, ErrEditConflict) {
		t.Fatal("draft raced queued render")
	}
	if _, err = st.MutateEditing("s", nil, d.Revision, true, "duplicate"); !errors.Is(err, ErrEditConflict) {
		t.Fatal("apply raced queued render")
	}
	job, err := st.ClaimNextJob(context.Background(), []string{"render_edit"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var snap EditSnapshot
	if err = json.Unmarshal([]byte(job.Parameters), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Text != "source" || snap.Draft.Sections[0].Keep || snap.Draft.Revision != d.Revision {
		t.Fatalf("snapshot %+v", snap)
	}
	if _, err = st.CompleteJob(job, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	d.Sections[1].Keep = false
	d, err = st.MutateEditing("s", &d, d.Revision, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.MutateEditing("s", nil, d.Revision, false, "empty"); err == nil {
		t.Fatal("empty apply accepted")
	}
	if _, err = st.MutateEditing("s", nil, d.Revision, true, "skip"); err != nil {
		t.Fatal(err)
	}
}

func TestSourceTranscriptionRefreshPreservesDraft(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateSermon(Sermon{ID: "refresh", Stage: "metadata", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	draft := editingFixture()
	draft.Sections[0].Keep = false
	if err := st.PrepareEditing("refresh", draft, "old source", TranscriptionMetadata{Duration: 10}); err != nil {
		t.Fatal(err)
	}
	savedDraft, err := st.GetEditing("refresh")
	if err != nil {
		t.Fatal(err)
	}
	sourceMetadata := TranscriptionMetadata{Language: "en", Duration: 10, Words: []TranscriptWord{{Word: "new", Start: 6, End: 7}}}
	if err := st.SaveSourceTranscription("refresh", "new source", sourceMetadata); err != nil {
		t.Fatal(err)
	}
	gotDraft, err := st.GetEditing("refresh")
	if err != nil || !reflect.DeepEqual(gotDraft, savedDraft) {
		t.Fatalf("draft changed during transcript refresh: %+v, want %+v; %v", gotDraft, savedDraft, err)
	}
	sourceText, gotSourceMetadata, err := st.SourceTranscription("refresh")
	if err != nil || sourceText != "new source" || !reflect.DeepEqual(gotSourceMetadata, sourceMetadata) {
		t.Fatalf("source transcription: %q %+v, %v", sourceText, gotSourceMetadata, err)
	}
	sermon, err := st.GetSermon("refresh")
	if err != nil || sermon.Transcript == nil || *sermon.Transcript != "new source" || !reflect.DeepEqual(sermon.SourceTranscriptionMetadata, &sourceMetadata) {
		t.Fatalf("source transcript: %+v, %v", sermon, err)
	}
	var editingSource string
	if err := st.db.QueryRow(`SELECT source_text FROM editing WHERE sermon_id=?`, "refresh").Scan(&editingSource); err != nil || editingSource != "" {
		t.Fatalf("editor must not duplicate source transcript: %q, %v", editingSource, err)
	}
}

func TestMetadataCannotBypassEditingPause(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateSermon(Sermon{ID: "s", Stage: "editing", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	// Redoing transcription can leave a previous source transcript available.
	if err := st.SaveSourceTranscription("s", "previous output", TranscriptionMetadata{}); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"title", "topics", "scriptures"} {
		if _, err := st.EnqueueProcessingRerun("s", part, part, time.Now()); !errors.Is(err, ErrNotRetryable) {
			t.Fatalf("%s bypassed editing pause: %v", part, err)
		}
	}
	if _, err := st.EnqueueProcessingRerun("s", "transcribe", "transcription", time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestCutsRequireTranscriptTiming(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateSermon(Sermon{ID: "s", Stage: "editing", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	d := editingFixture()
	d.Sections[0].Keep = false
	if err := st.PrepareEditing("s", d, "untimed text", TranscriptionMetadata{}); err != nil {
		t.Fatal(err)
	}
	d, _ = st.GetEditing("s")
	if _, err := st.MutateEditing("s", nil, d.Revision, false, "cut"); err == nil {
		t.Fatal("cuts accepted without timing")
	}
	sm, _ := st.GetSermon("s")
	if sm.Status != "done" {
		t.Fatal("rejection changed pipeline state")
	}
	if _, err := st.MutateEditing("s", nil, d.Revision, true, "skip"); err != nil {
		t.Fatal(err)
	}
}

func TestRedoAfterMetadataFailure(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateSermon(Sermon{ID: "s", Stage: "metadata", Status: "failed"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PrepareEditing("s", editingFixture(), "source", TranscriptionMetadata{}); err != nil {
		t.Fatal(err)
	}
	d, _ := st.GetEditing("s")
	d.Breakpoints[1].Time = 6
	d, err = st.MutateEditing("s", &d, d.Revision, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.MutateEditing("s", nil, d.Revision, false, "redo"); err != nil {
		t.Fatal(err)
	}
}

func TestAppliedTranscriptIsDerivedWithoutWritingSource(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateSermon(Sermon{ID: "source", Stage: "metadata", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	source := TranscriptionMetadata{Duration: 10, Words: []TranscriptWord{
		{Word: "removed", Start: 1, End: 2}, {Word: "retained", Start: 6, End: 7},
	}}
	draft := editingFixture()
	draft.Sections[0].Keep = false
	if err := st.PrepareEditing("source", draft, "removed retained", source); err != nil {
		t.Fatal(err)
	}
	checkSource := func() {
		t.Helper()
		text, metadata, err := st.SourceTranscription("source")
		if err != nil || text != "removed retained" || !reflect.DeepEqual(metadata, source) {
			t.Fatalf("source changed: %q %+v %v", text, metadata, err)
		}
		var untouched bool
		if err := st.db.QueryRow(`SELECT transcript IS NULL AND transcription_metadata IS NULL FROM sermons WHERE id='source'`).Scan(&untouched); err != nil || !untouched {
			t.Fatalf("edit wrote a derived transcript: %t %v", untouched, err)
		}
	}
	if err := st.CommitEditing("source", "edited.mp3", draft, true); err != nil {
		t.Fatal(err)
	}
	checkSource()
	checkOverview := func() {
		t.Helper()
		sermon, err := st.GetSermon("source")
		if err != nil || sermon.Transcript == nil || *sermon.Transcript != "retained" || sermon.TranscriptionMetadata.Words[0].Start != 1 || !reflect.DeepEqual(sermon.SourceTranscriptionMetadata, &source) {
			t.Fatalf("derived transcript/source timestamps: %+v %v", sermon, err)
		}
		list, err := st.ListSermons()
		if err != nil || len(list) != 1 || *list[0].Transcript != "retained" {
			t.Fatalf("list must use the same derived transcript: %+v %v", list, err)
		}
	}
	checkOverview()
	draft, err = st.GetEditing("source")
	if err != nil {
		t.Fatal(err)
	}
	draft.Sections[0].Keep = true
	draft.Sections[1].Keep = false
	if _, err := st.MutateEditing("source", &draft, draft.Revision, false, ""); err != nil {
		t.Fatal(err)
	}
	checkOverview() // Unapplied cuts must not change the playback transcript.
	checkSource()
	if err := st.CommitEditing("source", "", draft, false); err != nil {
		t.Fatal(err)
	}
	sermon, err := st.GetSermon("source")
	if err != nil || sermon.Transcript == nil || *sermon.Transcript != "removed retained" {
		t.Fatalf("skipping edits must restore the full transcript: %+v %v", sermon, err)
	}
	checkSource()
}

func TestSourceMigrationRecoversFullTranscriptAndAppliedSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "013" {
			break
		}
		raw, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`CREATE TABLE schema_version(version INTEGER NOT NULL); INSERT INTO schema_version VALUES(12);`); err != nil {
		t.Fatal(err)
	}
	metadata := TranscriptionMetadata{Duration: 10, Words: []TranscriptWord{{Word: "removed", Start: 1, End: 2}, {Word: "kept", Start: 6, End: 7}}}
	meta, _ := json.Marshal(metadata)
	applied := editingFixture()
	applied.Sections[0].Keep = false
	snapshot, _ := json.Marshal(EditSnapshot{Draft: applied})
	pending := editingFixture()
	pending.Sections[1].Keep = false
	draft, _ := json.Marshal(pending)
	for _, id := range []string{"canonical", "editor-fallback"} {
		if _, err := db.Exec(`INSERT INTO sermons(id,original_filename,uploaded_at,stage,status,transcript,transcription_metadata) VALUES (?,'test.mp3','','metadata','done','old cut','{}')`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO editing VALUES(?,?,1,?,?,'edit-render/edited.mp3',1)`, id, string(draft), "removed kept", string(meta)); err != nil {
			t.Fatal(err)
		}
	}
	// An existing API response must win over stale editor/sermon copies.
	if _, err := db.Exec(`INSERT INTO source_transcriptions VALUES('canonical','API source',?)`, string(meta)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO jobs(id,sermon_id,type,stage,state,parameters,available_at,created_at,updated_at) VALUES('render','canonical','render_edit','editing','done',?,'','','')`, string(snapshot)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for id, want := range map[string]string{"canonical": "API source", "editor-fallback": "removed kept"} {
		text, got, err := st.SourceTranscription(id)
		if err != nil || text != want || !reflect.DeepEqual(got, metadata) {
			t.Fatalf("migrated source %s: %q %+v %v", id, text, got, err)
		}
	}
	sermon, err := st.GetSermon("canonical")
	if err != nil || sermon.Transcript == nil || *sermon.Transcript != "kept" || sermon.TranscriptionMetadata.Words[0].Start != 1 {
		t.Fatalf("migration used pending cuts instead of rendered snapshot: %+v %v", sermon, err)
	}
}

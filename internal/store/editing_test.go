package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"
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
		if err := st.CommitEditing("playback", path, "same text", TranscriptionMetadata{Duration: 5}, true); err != nil {
			t.Fatal(err)
		}
		check(path)
	}
	if _, err := st.MutateEditing("playback", nil, 1, false, "render-next"); err != nil {
		t.Fatal(err)
	}
	check("edit-second/edited.mp3")
	if err := st.CommitEditing("playback", "", "source", TranscriptionMetadata{}, false); err != nil {
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

func TestSourceTranscriptVisibleWithoutReplacingAppliedOutput(t *testing.T) {
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
		{"applied", "kept output", &applied},
		{"legacy", "legacy output", nil},
	} {
		t.Run(tc.id, func(t *testing.T) {
			if err := st.CreateSermon(Sermon{ID: tc.id, Stage: "editing", Status: "done"}); err != nil {
				t.Fatal(err)
			}
			if err := st.SaveSourceTranscription(tc.id, "source recording", source); err != nil {
				t.Fatal(err)
			}
			if tc.id == "applied" {
				if err := st.SaveTranscription(tc.id, tc.text, applied); err != nil {
					t.Fatal(err)
				}
			} else if tc.id == "legacy" {
				if err := st.SaveTranscript(tc.id, tc.text); err != nil {
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
	if err = st.SaveTranscription("s", "applied", TranscriptionMetadata{Duration: 4}); err != nil {
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
	if *sm.Transcript != "applied" {
		t.Fatal("draft changed applied transcript")
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

func TestRefreshEditingTranscriptionPreservesDraft(t *testing.T) {
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
	keptMetadata := TranscriptionMetadata{Language: "en", Duration: 4, Words: []TranscriptWord{{Word: "kept", Start: 0, End: 1}}}
	if err := st.RefreshEditingTranscription("refresh", "new source", sourceMetadata, "kept transcript", keptMetadata); err != nil {
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
	if err != nil || sermon.Transcript == nil || *sermon.Transcript != "kept transcript" || !reflect.DeepEqual(sermon.TranscriptionMetadata, &keptMetadata) {
		t.Fatalf("applied transcript: %+v, %v", sermon, err)
	}
	var editingSource string
	if err := st.db.QueryRow(`SELECT source_text FROM editing WHERE sermon_id=?`, "refresh").Scan(&editingSource); err != nil || editingSource != "new source" {
		t.Fatalf("editing source transcript: %q, %v", editingSource, err)
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
	// Redoing transcription can leave an older applied transcript available.
	if err := st.SaveTranscript("s", "previous output"); err != nil {
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

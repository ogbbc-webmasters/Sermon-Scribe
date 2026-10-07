package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"
)

func editingFixture() Editing {
	return Editing{Duration: 10, Breakpoints: []Breakpoint{{"start", 0, "start"}, {"middle", 5, "manual"}, {"end", 10, "end"}}, Sections: []Section{{"a", true}, {"b", true}}}
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

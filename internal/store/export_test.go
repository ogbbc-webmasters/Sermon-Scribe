package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestExportSnapshotsAppliedSourceAndRestoresState(t *testing.T) {
	for _, applied := range []bool{false, true} {
		t.Run(map[bool]string{false: "whole-recording", true: "applied-edits"}[applied], func(t *testing.T) {
			st := openTestStore(t)
			if err := st.CreateSermon(Sermon{ID: "sermon", Stage: "metadata", Status: "failed"}); err != nil {
				t.Fatal(err)
			}
			d := Editing{Duration: 12, Breakpoints: []Breakpoint{{ID: "start", Kind: "start"}, {ID: "end", Time: 12, Kind: "end"}}, Sections: []Section{{ID: "section", Keep: true}}}
			if err := st.PrepareEditing("sermon", d, "text", TranscriptionMetadata{Duration: 12}); err != nil {
				t.Fatal(err)
			}
			if err := st.CommitEditing("sermon", "edit-first/edited.mp3", "text", TranscriptionMetadata{Duration: 4}, applied); err != nil {
				t.Fatal(err)
			}
			if err := st.SetNormalizationAdjustments("sermon", 1, -2); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			if _, err := st.EnqueueExport("sermon", "export", now); err != nil {
				t.Fatal(err)
			}
			if _, err := st.EnqueueExport("sermon", "duplicate", now); !errors.Is(err, ErrEditConflict) {
				t.Fatalf("duplicate export: %v", err)
			}
			if _, err := st.MutateEditing("sermon", nil, 1, true, "edit"); !errors.Is(err, ErrEditConflict) {
				t.Fatalf("concurrent edit: %v", err)
			}
			job, err := st.ClaimNextJob(context.Background(), []string{"export"}, now)
			if err != nil {
				t.Fatal(err)
			}
			var snap ExportSnapshot
			if err := json.Unmarshal([]byte(job.Parameters), &snap); err != nil {
				t.Fatal(err)
			}
			want := "normalized.flac"
			if applied {
				want = "edit-first/edited.flac"
			}
			if snap.Source != want || snap.GateAdjustment != 1 || snap.VolumeAdjustment != -2 {
				t.Fatalf("snapshot: %+v", snap)
			}
			sm, err := st.CompleteJob(job, nil, now)
			if err != nil || sm.Stage != "metadata" || sm.Status != "failed" || sm.Transcript == nil || *sm.Transcript != "text" {
				t.Fatalf("restored state: %+v, %v", sm, err)
			}
		})
	}
}

func TestExportLegacySermonWithoutEditingDraft(t *testing.T) {
	st := openTestStore(t)
	if err := st.CreateSermon(Sermon{ID: "legacy", Stage: "metadata", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueExport("legacy", "export", time.Now()); err != nil {
		t.Fatal(err)
	}
	job, err := st.GetJob("export")
	if err != nil {
		t.Fatal(err)
	}
	var snapshot ExportSnapshot
	if err := json.Unmarshal([]byte(job.Parameters), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Source != "normalized.flac" {
		t.Fatalf("legacy source: %s", snapshot.Source)
	}
}

package processing

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func nonempty(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return errors.New("empty")
	}
	return nil
}

func TestCommitArtifactsWritesMarkerLast(t *testing.T) {
	dir := t.TempDir()
	flacTemp := filepath.Join(dir, "normalized.flac.job-1.tmp")
	mp3Temp := filepath.Join(dir, "normalized.mp3.job-1.tmp")
	flacFinal := filepath.Join(dir, "normalized.flac")
	mp3Final := filepath.Join(dir, "normalized.mp3")
	markerPath := filepath.Join(dir, ".normalization-complete.json")
	if err := os.WriteFile(flacTemp, []byte("flac"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mp3Temp, []byte("mp3"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifacts := []Artifact{
		{TemporaryPath: flacTemp, FinalPath: flacFinal, Validate: nonempty},
		{TemporaryPath: mp3Temp, FinalPath: mp3Final, Validate: nonempty},
	}
	marker := CompletionMarker{JobID: "job-1", Parameters: `{"gate_adjustment":0,"volume_adjustment":0}`, PipelineVersion: 1}

	if err := CommitArtifacts(markerPath, marker, artifacts); err != nil {
		t.Fatal(err)
	}
	committed, err := HasCommittedArtifacts(markerPath, marker, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if !committed {
		t.Fatal("committed artifacts not recognized")
	}
	if _, err := os.Stat(flacTemp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary FLAC remains: %v", err)
	}
	if _, err := os.Stat(mp3Temp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary MP3 remains: %v", err)
	}

	other := marker
	other.JobID = "job-2"
	committed, err = HasCommittedArtifacts(markerPath, other, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if committed {
		t.Fatal("marker for an older job satisfied a new job")
	}
}

func TestCommitArtifactsDoesNotPublishInvalidOutputs(t *testing.T) {
	dir := t.TempDir()
	temp := filepath.Join(dir, "output.tmp")
	final := filepath.Join(dir, "output")
	markerPath := filepath.Join(dir, "complete.json")
	if err := os.WriteFile(temp, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	err := CommitArtifacts(markerPath, CompletionMarker{JobID: "job-1"}, []Artifact{
		{TemporaryPath: temp, FinalPath: final, Validate: nonempty},
	})
	if err == nil {
		t.Fatal("CommitArtifacts accepted invalid output")
	}
	if _, err := os.Stat(final); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid output was published: %v", err)
	}
	if _, err := os.Stat(markerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker was published: %v", err)
	}
}

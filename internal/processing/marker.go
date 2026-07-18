package processing

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// CompletionMarker is the filesystem commit record for a completed job.
type CompletionMarker struct {
	JobID           string `json:"job_id"`
	Parameters      string `json:"parameters"`
	PipelineVersion int    `json:"pipeline_version"`
}

// Artifact describes one validated temporary output to publish.
type Artifact struct {
	TemporaryPath string
	FinalPath     string
	Validate      func(string) error
}

// CommitArtifacts validates every temporary output, publishes them at their
// final paths, then atomically writes the marker last. Without the marker,
// partially published outputs are deliberately not considered committed.
func CommitArtifacts(markerPath string, marker CompletionMarker, artifacts []Artifact) error {
	for _, artifact := range artifacts {
		if artifact.Validate != nil {
			if err := artifact.Validate(artifact.TemporaryPath); err != nil {
				return fmt.Errorf("validate %s: %w", artifact.TemporaryPath, err)
			}
		}
	}
	for _, artifact := range artifacts {
		if err := os.Rename(artifact.TemporaryPath, artifact.FinalPath); err != nil {
			return fmt.Errorf("publish %s: %w", artifact.FinalPath, err)
		}
	}
	if err := writeMarker(markerPath, marker); err != nil {
		return err
	}
	return syncDir(filepath.Dir(markerPath))
}

// HasCommittedArtifacts reports whether the expected marker matches and all
// final artifacts still validate.
func HasCommittedArtifacts(markerPath string, expected CompletionMarker, artifacts []Artifact) (bool, error) {
	data, err := os.ReadFile(markerPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read completion marker: %w", err)
	}
	var actual CompletionMarker
	if err := json.Unmarshal(data, &actual); err != nil {
		return false, nil
	}
	if actual != expected {
		return false, nil
	}
	for _, artifact := range artifacts {
		if artifact.Validate == nil {
			if _, err := os.Stat(artifact.FinalPath); errors.Is(err, os.ErrNotExist) {
				return false, nil
			} else if err != nil {
				return false, err
			}
			continue
		}
		if err := artifact.Validate(artifact.FinalPath); err != nil {
			return false, nil
		}
	}
	return true, nil
}

func writeMarker(path string, marker CompletionMarker) error {
	data, err := json.Marshal(marker)
	if err != nil {
		return fmt.Errorf("encode completion marker: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".completion-*.tmp")
	if err != nil {
		return fmt.Errorf("create completion marker: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write completion marker: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync completion marker: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close completion marker: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("publish completion marker: %w", err)
	}
	return nil
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

type ExportHandler struct {
	uploadsDir string
}

func NewExportHandler(uploadsDir string) *ExportHandler {
	return &ExportHandler{uploadsDir: uploadsDir}
}

func (h *ExportHandler) Run(ctx context.Context, job store.Job, reporter Reporter) (Result, error) {
	var snapshot store.ExportSnapshot
	if err := json.Unmarshal([]byte(job.Parameters), &snapshot); err != nil {
		return Result{}, err
	}
	if !filepath.IsLocal(snapshot.Source) {
		return Result{}, fmt.Errorf("invalid export source")
	}
	dir := filepath.Join(h.uploadsDir, job.SermonID)
	generation := filepath.Join(dir, "export-"+job.ID)
	if err := os.MkdirAll(generation, 0755); err != nil {
		return Result{}, err
	}
	output, temporary := filepath.Join(generation, "export.mp3"), filepath.Join(generation, ".export.mp3")
	defer os.Remove(temporary)
	marker := CompletionMarker{JobID: job.ID, Parameters: job.Parameters, PipelineVersion: 1}
	markerPath := filepath.Join(generation, ".complete.json")
	artifacts := []Artifact{{TemporaryPath: temporary, FinalPath: output, Validate: func(path string) error {
		return probeAudio(path, audioSpec{codec: "mp3", sampleRate: 44100, channels: 1, bitRate: 32000})
	}}}
	committed, err := HasCommittedArtifacts(markerPath, marker, artifacts)
	if err != nil {
		return Result{}, err
	}
	if !committed {
		input := filepath.Join(dir, snapshot.Source)
		if err := probeAudio(input, audioSpec{codec: "mp3", sampleRate: 44100, channels: 1, bitRate: 32000}); err != nil {
			return Result{}, fmt.Errorf("export source is not ready: %w", err)
		}
		// Normalization and applying edits already produced the delivery format.
		// Copy compressed packets without filtering or another lossy encode.
		if err := encodeAudio(ctx, input, temporary, []string{"-c:a", "copy"}, func(percent int) error {
			return reporter.Progress(percent, nil)
		}); err != nil {
			return Result{}, err
		}
		if err := CommitArtifacts(markerPath, marker, artifacts); err != nil {
			return Result{}, err
		}
	}
	return Result{}, reporter.Progress(100, nil)
}

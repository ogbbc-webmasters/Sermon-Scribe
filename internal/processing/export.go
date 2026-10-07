package processing

import (
	"context"
	"encoding/json"
	"errors"
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
	settings := NormalizationSettings{GateAdjustment: snapshot.GateAdjustment, VolumeAdjustment: snapshot.VolumeAdjustment}
	if err := validateNormalizationSettings(settings); err != nil {
		return Result{}, err
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
		return probeAudio(path, audioSpec{codec: "mp3", sampleRate: 44100, channels: 1})
	}}}
	committed, err := HasCommittedArtifacts(markerPath, marker, artifacts)
	if err != nil {
		return Result{}, err
	}
	if !committed {
		options := []string{"-c:a", "libmp3lame", "-b:a", "32k"}
		if _, err := os.Stat(filepath.Join(dir, ".source-prepared.json")); err == nil {
			options = append([]string{"-af", normalizationFilter(settings)}, options...)
		} else if !errors.Is(err, os.ErrNotExist) {
			return Result{}, err
		}
		// Older recordings are already treated. Always encode from the retained
		// lossless source, never from a previous export or the playback MP3.
		if err := encodeAudio(ctx, filepath.Join(dir, snapshot.Source), temporary, options, func(percent int) error {
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

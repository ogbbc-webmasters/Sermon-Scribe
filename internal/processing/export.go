package processing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
		normalize := false
		if _, err := os.Stat(filepath.Join(dir, ".source-prepared.json")); err == nil {
			normalize = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return Result{}, err
		}
		input := filepath.Join(dir, snapshot.Source)
		if _, err := os.Stat(input); errors.Is(err, os.ErrNotExist) {
			// Some legacy recordings only retain their MP3. For applied edits,
			// use the matching generation rather than undoing cuts by falling
			// back to the full recording.
			input = strings.TrimSuffix(input, ".flac") + ".mp3"
			if _, err := os.Stat(input); errors.Is(err, os.ErrNotExist) && snapshot.Source == "normalized.flac" {
				input, err = findOriginal(dir)
				if err != nil {
					return Result{}, fmt.Errorf("export source audio is unavailable: %w", err)
				}
				normalize = true
			} else if err != nil {
				return Result{}, fmt.Errorf("export source audio is unavailable: %w", err)
			}
		} else if err != nil {
			return Result{}, err
		}
		options := []string{"-c:a", "libmp3lame", "-b:a", "32k"}
		if normalize {
			options = append([]string{"-af", normalizationFilter(settings)}, options...)
		}
		// Prefer lossless audio; legacy treated MP3s must not be treated twice.
		// Never use a previous export as the source for a new export.
		if err := encodeAudio(ctx, input, temporary, options, func(percent int) error {
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

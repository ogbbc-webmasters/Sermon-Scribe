package processing

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

type recordingReporter struct {
	progress []int
	err      error
}

func (r *recordingReporter) Progress(percent int, _ *string) error {
	r.progress = append(r.progress, percent)
	return r.err
}

func TestNormalizeHandlerCommitsArtifactsAndPreset(t *testing.T) {
	st := processingTestStore(t)
	uploads := t.TempDir()
	sermonID := "sermon-1"
	dir := filepath.Join(uploads, sermonID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "original.wav"), []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSermon(store.Sermon{
		ID: sermonID, OriginalFilename: "original.wav",
		UploadedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Stage:      "normalization", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	parameters, err := NormalizationParameters(string(PresetLouder))
	if err != nil {
		t.Fatal(err)
	}
	job := store.Job{ID: "job-1", SermonID: sermonID, Type: "normalize", Parameters: parameters}

	runs := 0
	handler := NewNormalizeHandler(st, uploads)
	handler.runFFmpeg = func(_ context.Context, input, flac, mp3, filter string, progress func(int) error) error {
		runs++
		if input != filepath.Join(dir, "original.wav") {
			t.Fatalf("input = %q", input)
		}
		if !strings.Contains(filter, "loudnorm=I=-14") || !strings.Contains(filter, "asplit=2") {
			t.Fatalf("louder filter = %q", filter)
		}
		if err := os.WriteFile(flac, []byte("flac"), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(mp3, []byte("mp3"), 0o644); err != nil {
			return err
		}
		return progress(60)
	}
	handler.probe = func(path string, _ audioSpec) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			return errors.New("empty audio")
		}
		return nil
	}
	reporter := &recordingReporter{}
	if _, err := handler.Run(context.Background(), job, reporter); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("ffmpeg runs = %d, want 1", runs)
	}
	for _, name := range []string{"normalized.flac", "normalized.mp3", ".normalization-complete.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	sm, err := st.GetSermon(sermonID)
	if err != nil {
		t.Fatal(err)
	}
	if sm.NormalizationPreset != string(PresetLouder) {
		t.Fatalf("preset = %q, want louder", sm.NormalizationPreset)
	}
	if got := reporter.progress; len(got) < 3 || got[0] != 0 || got[len(got)-1] != 100 {
		t.Fatalf("progress = %v", got)
	}

	// A retry after the filesystem commit uses the marker instead of encoding again.
	if _, err := handler.Run(context.Background(), job, &recordingReporter{}); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("ffmpeg runs after committed retry = %d, want 1", runs)
	}
}

func TestNormalizeHandlerRejectsBadParametersAndMissingOriginal(t *testing.T) {
	st := processingTestStore(t)
	uploads := t.TempDir()
	if err := st.CreateSermon(store.Sermon{
		ID: "sermon-1", OriginalFilename: "missing.wav",
		UploadedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Stage:      "normalization", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewNormalizeHandler(st, uploads)
	job := store.Job{ID: "job-1", SermonID: "sermon-1", Parameters: `{"preset":"mystery"}`}
	if _, err := handler.Run(context.Background(), job, &recordingReporter{}); err == nil || !strings.Contains(err.Error(), "unknown normalization preset") {
		t.Fatalf("bad preset error = %v", err)
	}
	job.Parameters = `{"preset":"standard"}`
	if _, err := handler.Run(context.Background(), job, &recordingReporter{}); err == nil || !strings.Contains(err.Error(), "read sermon uploads") {
		t.Fatalf("missing original error = %v", err)
	}
}

func TestNormalizationFilters(t *testing.T) {
	if got := normalizationFilter(PresetNoGate); strings.Contains(got, "agate=") {
		t.Fatalf("no-gate filter contains gate: %q", got)
	}
	if got := normalizationFilter(PresetStrongerGate); !strings.Contains(got, "threshold=0.035") {
		t.Fatalf("stronger-gate filter = %q", got)
	}
	if !ValidNormalizationPreset("standard") || ValidNormalizationPreset("unknown") {
		t.Fatal("preset validation mismatch")
	}
}

func TestRunFFmpegProducesMatchingMonoOutputs(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "input.wav")
	generate := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi",
		"-i", "sine=frequency=440:duration=0.25:sample_rate=48000", "-ac", "2", input)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate fixture: %v: %s", err, output)
	}
	flac := filepath.Join(dir, "normalized.flac")
	mp3 := filepath.Join(dir, "normalized.mp3")
	if err := runFFmpeg(context.Background(), input, flac, mp3, normalizationFilter(PresetStandard), func(int) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := probeAudio(flac, audioSpec{codec: "flac", sampleRate: 44100, channels: 1}); err != nil {
		t.Fatal(err)
	}
	if err := probeAudio(mp3, audioSpec{codec: "mp3", sampleRate: 44100, channels: 1}); err != nil {
		t.Fatal(err)
	}
}

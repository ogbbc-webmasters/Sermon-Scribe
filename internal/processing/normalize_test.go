package processing

import (
	"context"
	"errors"
	"math"
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

func TestNormalizeHandlerCommitsArtifactsAndAdjustments(t *testing.T) {
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
	settings := NormalizationSettings{GateAdjustment: 1, VolumeAdjustment: 1}
	parameters, err := NormalizationParameters(settings)
	if err != nil {
		t.Fatal(err)
	}
	job := store.Job{ID: "job-1", SermonID: sermonID, Type: "normalize", Parameters: parameters}

	runs := 0
	var lastFilter string
	handler := NewNormalizeHandler(st, uploads)
	handler.runFFmpeg = func(_ context.Context, input, flac, mp3, filter string, progress func(int) error) error {
		runs++
		lastFilter = filter
		if input != filepath.Join(dir, "original.wav") {
			t.Fatalf("input = %q", input)
		}
		if !strings.Contains(filter, "asplit=2") {
			t.Fatalf("normalization filter = %q", filter)
		}
		if err := os.WriteFile(flac, []byte("flac"), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(mp3, []byte("mp3"), 0o644); err != nil {
			return err
		}
		if err := progress(0); err != nil {
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
	handler.waveform = func(context.Context, string) (Waveform, error) {
		return Waveform{Duration: 1, SamplesPerSecond: 20, Samples: make([]float64, 20)}, nil
	}
	reporter := &recordingReporter{}
	if _, err := handler.Run(context.Background(), job, reporter); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || !strings.Contains(lastFilter, "threshold=0.030") || !strings.Contains(lastFilter, "loudnorm=I=-14") {
		t.Fatalf("ffmpeg runs/filter = %d %q", runs, lastFilter)
	}
	for _, name := range []string{"normalized.flac", "normalized.mp3", "waveform.json", ".normalization-complete.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	sm, err := st.GetSermon(sermonID)
	if err != nil {
		t.Fatal(err)
	}
	if sm.NormalizationGateAdjustment != 1 || sm.NormalizationVolumeAdjustment != 1 {
		t.Fatalf("adjustments = %d/%d, want 1/1", sm.NormalizationGateAdjustment, sm.NormalizationVolumeAdjustment)
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

	// A relative rerun has a new job ID and adjusted settings, forcing a new render.
	settings, err = AdjustNormalization(settings, string(AdjustmentLessGate))
	if err != nil {
		t.Fatal(err)
	}
	job.ID = "job-2"
	job.Parameters, err = NormalizationParameters(settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler.Run(context.Background(), job, &recordingReporter{}); err != nil {
		t.Fatal(err)
	}
	if runs != 2 || !strings.Contains(lastFilter, "threshold=0.020") {
		t.Fatalf("rerun runs/filter = %d %q", runs, lastFilter)
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
	job := store.Job{ID: "job-1", SermonID: "sermon-1", Parameters: `{"gate_adjustment":4,"volume_adjustment":0}`}
	if _, err := handler.Run(context.Background(), job, &recordingReporter{}); err == nil || !strings.Contains(err.Error(), "gate adjustment") {
		t.Fatalf("bad adjustment error = %v", err)
	}
	job.Parameters = `{"gate_adjustment":0,"volume_adjustment":0}`
	if _, err := handler.Run(context.Background(), job, &recordingReporter{}); err == nil || !strings.Contains(err.Error(), "read sermon uploads") {
		t.Fatalf("missing original error = %v", err)
	}
}

func TestNormalizationAdjustmentsAndFilters(t *testing.T) {
	settings := NormalizationSettings{}
	var err error
	settings, err = AdjustNormalization(settings, "more-gate")
	if err != nil || settings.GateAdjustment != 1 {
		t.Fatalf("more gate = %+v, %v", settings, err)
	}
	settings, err = AdjustNormalization(settings, "less-gate")
	if err != nil || settings.GateAdjustment != 0 {
		t.Fatalf("less gate = %+v, %v", settings, err)
	}
	settings, err = AdjustNormalization(settings, "more-volume")
	if err != nil || settings.VolumeAdjustment != 1 {
		t.Fatalf("more volume = %+v, %v", settings, err)
	}
	settings, err = AdjustNormalization(settings, "less-volume")
	if err != nil || settings.VolumeAdjustment != 0 {
		t.Fatalf("less volume = %+v, %v", settings, err)
	}
	if _, err := AdjustNormalization(settings, "mystery"); err == nil {
		t.Fatal("unknown adjustment accepted")
	}

	if got := normalizationFilter(NormalizationSettings{GateAdjustment: -3}); strings.Contains(got, "agate=") {
		t.Fatalf("minimum gate filter contains gate: %q", got)
	}
	if got := normalizationFilter(NormalizationSettings{GateAdjustment: 2}); !strings.Contains(got, "threshold=0.040") {
		t.Fatalf("more-gate filter = %q", got)
	}
	if got := normalizationFilter(NormalizationSettings{VolumeAdjustment: -1}); !strings.Contains(got, "loudnorm=I=-18") {
		t.Fatalf("less-volume filter = %q", got)
	}
	atLimit := NormalizationSettings{GateAdjustment: maxAdjustment}
	if _, err := AdjustNormalization(atLimit, "more-gate"); err == nil {
		t.Fatal("gate adjustment exceeded maximum")
	}
	if !ValidNormalizationAdjustment("less-volume") || ValidNormalizationAdjustment("unknown") {
		t.Fatal("adjustment validation mismatch")
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
	if err := runFFmpeg(context.Background(), input, flac, mp3, normalizationFilter(NormalizationSettings{}), func(int) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := probeAudio(flac, audioSpec{codec: "flac", sampleRate: 44100, channels: 1}); err != nil {
		t.Fatal(err)
	}
	if err := probeAudio(mp3, audioSpec{codec: "mp3", sampleRate: 44100, channels: 1}); err != nil {
		t.Fatal(err)
	}
	flacDuration, err := probeDuration(context.Background(), flac)
	if err != nil {
		t.Fatal(err)
	}
	mp3Duration, err := probeDuration(context.Background(), mp3)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(flacDuration-mp3Duration) > 0.05 {
		t.Fatalf("output durations differ: flac=%f mp3=%f", flacDuration, mp3Duration)
	}
}

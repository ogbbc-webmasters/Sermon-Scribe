package processing

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

const normalizationPipelineVersion = 1

// NormalizationSettings are relative adjustments from the default treatment.
type NormalizationSettings struct {
	GateAdjustment   int `json:"gate_adjustment"`
	VolumeAdjustment int `json:"volume_adjustment"`
}

// NormalizationAdjustment identifies one relative user-requested change.
type NormalizationAdjustment string

const (
	AdjustmentMoreGate   NormalizationAdjustment = "more-gate"
	AdjustmentLessGate   NormalizationAdjustment = "less-gate"
	AdjustmentMoreVolume NormalizationAdjustment = "more-volume"
	AdjustmentLessVolume NormalizationAdjustment = "less-volume"
	minAdjustment                                = -3
	maxAdjustment                                = 3
)

// ValidNormalizationAdjustment reports whether name is one of the four controls.
func ValidNormalizationAdjustment(name string) bool {
	switch NormalizationAdjustment(name) {
	case AdjustmentMoreGate, AdjustmentLessGate, AdjustmentMoreVolume, AdjustmentLessVolume:
		return true
	default:
		return false
	}
}

// AdjustNormalization applies one relative change to the current settings.
func AdjustNormalization(current NormalizationSettings, adjustment string) (NormalizationSettings, error) {
	next := current
	switch NormalizationAdjustment(adjustment) {
	case AdjustmentMoreGate:
		next.GateAdjustment++
	case AdjustmentLessGate:
		next.GateAdjustment--
	case AdjustmentMoreVolume:
		next.VolumeAdjustment++
	case AdjustmentLessVolume:
		next.VolumeAdjustment--
	default:
		return NormalizationSettings{}, fmt.Errorf("unknown normalization adjustment %q", adjustment)
	}
	if err := validateNormalizationSettings(next); err != nil {
		return NormalizationSettings{}, err
	}
	return next, nil
}

// NormalizationParameters returns canonical job parameters for settings.
func NormalizationParameters(settings NormalizationSettings) (string, error) {
	if err := validateNormalizationSettings(settings); err != nil {
		return "", err
	}
	data, err := json.Marshal(settings)
	return string(data), err
}

func parseNormalizationParameters(raw string) (NormalizationSettings, error) {
	if raw == "" || raw == "{}" {
		return NormalizationSettings{}, nil
	}
	var settings NormalizationSettings
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		return NormalizationSettings{}, fmt.Errorf("decode normalization parameters: %w", err)
	}
	if err := validateNormalizationSettings(settings); err != nil {
		return NormalizationSettings{}, err
	}
	return settings, nil
}

func validateNormalizationSettings(settings NormalizationSettings) error {
	if settings.GateAdjustment < minAdjustment || settings.GateAdjustment > maxAdjustment {
		return fmt.Errorf("gate adjustment %d outside %d..%d", settings.GateAdjustment, minAdjustment, maxAdjustment)
	}
	if settings.VolumeAdjustment < minAdjustment || settings.VolumeAdjustment > maxAdjustment {
		return fmt.Errorf("volume adjustment %d outside %d..%d", settings.VolumeAdjustment, minAdjustment, maxAdjustment)
	}
	return nil
}

type audioSpec struct {
	codec      string
	sampleRate int
	channels   int
}

type ffmpegRunner func(context.Context, string, string, string, string, func(int) error) error
type audioProbe func(string, audioSpec) error

// NormalizeHandler produces the lossless master and browser proxy for one job.
type NormalizeHandler struct {
	store      *store.Store
	uploadsDir string
	runFFmpeg  ffmpegRunner
	probe      audioProbe
}

// NewNormalizeHandler builds the production FFmpeg-backed normalization handler.
func NewNormalizeHandler(st *store.Store, uploadsDir string) *NormalizeHandler {
	return &NormalizeHandler{
		store: st, uploadsDir: uploadsDir,
		runFFmpeg: runFFmpeg, probe: probeAudio,
	}
}

// Run implements Handler.
func (h *NormalizeHandler) Run(ctx context.Context, job store.Job, reporter Reporter) (Result, error) {
	settings, err := parseNormalizationParameters(job.Parameters)
	if err != nil {
		return Result{}, err
	}
	input, err := findOriginal(filepath.Join(h.uploadsDir, job.SermonID))
	if err != nil {
		return Result{}, err
	}

	dir := filepath.Dir(input)
	flacFinal := filepath.Join(dir, "normalized.flac")
	mp3Final := filepath.Join(dir, "normalized.mp3")
	flacTemp := filepath.Join(dir, ".normalize-"+job.ID+".flac")
	mp3Temp := filepath.Join(dir, ".normalize-"+job.ID+".mp3")
	markerPath := filepath.Join(dir, ".normalization-complete.json")
	defer os.Remove(flacTemp)
	defer os.Remove(mp3Temp)

	marker := CompletionMarker{
		JobID: job.ID, Parameters: job.Parameters,
		PipelineVersion: normalizationPipelineVersion,
	}
	artifacts := []Artifact{
		{TemporaryPath: flacTemp, FinalPath: flacFinal, Validate: func(path string) error {
			return h.probe(path, audioSpec{codec: "flac", sampleRate: 44100, channels: 1})
		}},
		{TemporaryPath: mp3Temp, FinalPath: mp3Final, Validate: func(path string) error {
			return h.probe(path, audioSpec{codec: "mp3", sampleRate: 44100, channels: 1})
		}},
	}
	committed, err := HasCommittedArtifacts(markerPath, marker, artifacts)
	if err != nil {
		return Result{}, err
	}
	if !committed {
		filter := normalizationFilter(settings)
		if err := h.runFFmpeg(ctx, input, flacTemp, mp3Temp, filter, func(percent int) error {
			return reporter.Progress(percent, nil)
		}); err != nil {
			return Result{}, err
		}
		if err := CommitArtifacts(markerPath, marker, artifacts); err != nil {
			return Result{}, err
		}
	}
	if err := h.store.SetNormalizationAdjustments(
		job.SermonID, settings.GateAdjustment, settings.VolumeAdjustment,
	); err != nil {
		return Result{}, fmt.Errorf("store normalization adjustments: %w", err)
	}
	if err := reporter.Progress(100, nil); err != nil {
		return Result{}, err
	}
	return Result{}, nil
}

func normalizationFilter(settings NormalizationSettings) string {
	filters := []string{"aformat=channel_layouts=mono"}
	if gate := normalizationGate(settings.GateAdjustment); gate != "" {
		filters = append(filters, gate)
	}
	loudnessLU := -16 + 2*settings.VolumeAdjustment
	filters = append(filters,
		fmt.Sprintf("loudnorm=I=%d:LRA=11:TP=-1.5:dual_mono=true", loudnessLU),
		"aresample=44100",
		"asplit=2[master][proxy]",
	)
	return "[0:a:0]" + strings.Join(filters, ",")
}

func normalizationGate(adjustment int) string {
	switch adjustment {
	case -3:
		return ""
	case -2:
		return "agate=threshold=0.008:ratio=2:range=0.35:attack=20:release=250"
	case -1:
		return "agate=threshold=0.014:ratio=3:range=0.25:attack=20:release=250"
	case 0:
		return "agate=threshold=0.020:ratio=4:range=0.15:attack=20:release=250"
	case 1:
		return "agate=threshold=0.030:ratio=5:range=0.12:attack=20:release=275"
	case 2:
		return "agate=threshold=0.040:ratio=6:range=0.10:attack=20:release=300"
	case 3:
		return "agate=threshold=0.055:ratio=8:range=0.08:attack=20:release=325"
	default:
		return ""
	}
}

func findOriginal(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read sermon uploads: %w", err)
	}
	var found string
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type().IsRegular() && (name == "original" || strings.HasPrefix(name, "original.")) {
			if found != "" {
				return "", errors.New("multiple original audio files found")
			}
			found = filepath.Join(dir, name)
		}
	}
	if found == "" {
		return "", errors.New("original audio file not found")
	}
	return found, nil
}

func runFFmpeg(ctx context.Context, input, flacOutput, mp3Output, filter string, onProgress func(int) error) error {
	duration, durationErr := probeDuration(ctx, input)
	initialProgress := 0
	if durationErr != nil || duration <= 0 {
		duration = 0
		initialProgress = -1
	}
	if err := onProgress(initialProgress); err != nil {
		return fmt.Errorf("report ffmpeg progress: %w", err)
	}
	args := []string{
		"-hide_banner", "-nostdin", "-y", "-i", input,
		"-filter_complex", filter,
		"-map", "[master]", "-ac", "1", "-ar", "44100", "-c:a", "flac", flacOutput,
		"-map", "[proxy]", "-ac", "1", "-ar", "44100", "-c:a", "libmp3lame", "-b:a", "128k", mp3Output,
		"-progress", "pipe:1", "-nostats",
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}
	last := -1
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if duration <= 0 || !strings.HasPrefix(scanner.Text(), "out_time_us=") {
			continue
		}
		microseconds, err := strconv.ParseFloat(strings.TrimPrefix(scanner.Text(), "out_time_us="), 64)
		if err != nil {
			continue
		}
		percent := min(99, max(0, int(microseconds/1_000_000/duration*100)))
		if percent != last {
			last = percent
			if err := onProgress(percent); err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return fmt.Errorf("report ffmpeg progress: %w", err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Wait()
		return fmt.Errorf("read ffmpeg progress: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("ffmpeg normalization failed: %s", message)
	}
	return nil
}

func probeDuration(ctx context.Context, path string) (float64, error) {
	output, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path).Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
}

func probeAudio(path string, expected audioSpec) error {
	output, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "a:0",
		"-show_entries", "stream=codec_name,sample_rate,channels", "-of", "json", path).Output()
	if err != nil {
		return fmt.Errorf("probe audio: %w", err)
	}
	var result struct {
		Streams []struct {
			CodecName  string `json:"codec_name"`
			SampleRate string `json:"sample_rate"`
			Channels   int    `json:"channels"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return fmt.Errorf("decode probe output: %w", err)
	}
	if len(result.Streams) != 1 {
		return fmt.Errorf("expected one audio stream, found %d", len(result.Streams))
	}
	stream := result.Streams[0]
	sampleRate, err := strconv.Atoi(stream.SampleRate)
	if err != nil {
		return fmt.Errorf("invalid sample rate %q", stream.SampleRate)
	}
	if stream.CodecName != expected.codec || sampleRate != expected.sampleRate || stream.Channels != expected.channels {
		return fmt.Errorf("audio is %s/%dHz/%dch, want %s/%dHz/%dch",
			stream.CodecName, sampleRate, stream.Channels,
			expected.codec, expected.sampleRate, expected.channels)
	}
	return nil
}

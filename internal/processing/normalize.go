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

// NormalizationPreset identifies one fixed, user-facing audio treatment.
type NormalizationPreset string

const (
	PresetStandard     NormalizationPreset = "standard"
	PresetStrongerGate NormalizationPreset = "stronger-gate"
	PresetNoGate       NormalizationPreset = "no-gate"
	PresetLouder       NormalizationPreset = "louder"
)

var normalizationPresets = map[NormalizationPreset]struct {
	gate       string
	loudnessLU int
}{
	PresetStandard:     {gate: "agate=threshold=0.020:ratio=4:range=0.15:attack=20:release=250", loudnessLU: -16},
	PresetStrongerGate: {gate: "agate=threshold=0.035:ratio=6:range=0.08:attack=20:release=300", loudnessLU: -16},
	PresetNoGate:       {loudnessLU: -16},
	PresetLouder:       {gate: "agate=threshold=0.020:ratio=4:range=0.15:attack=20:release=250", loudnessLU: -14},
}

// ValidNormalizationPreset reports whether name is one of the fixed presets.
func ValidNormalizationPreset(name string) bool {
	_, ok := normalizationPresets[NormalizationPreset(name)]
	return ok
}

// NormalizationParameters returns the canonical job parameters for a preset.
func NormalizationParameters(name string) (string, error) {
	if !ValidNormalizationPreset(name) {
		return "", fmt.Errorf("unknown normalization preset %q", name)
	}
	data, err := json.Marshal(struct {
		Preset string `json:"preset"`
	}{Preset: name})
	return string(data), err
}

func parseNormalizationParameters(raw string) (NormalizationPreset, error) {
	if raw == "" || raw == "{}" {
		return PresetStandard, nil
	}
	var parameters struct {
		Preset string `json:"preset"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parameters); err != nil {
		return "", fmt.Errorf("decode normalization parameters: %w", err)
	}
	preset := NormalizationPreset(parameters.Preset)
	if _, ok := normalizationPresets[preset]; !ok {
		return "", fmt.Errorf("unknown normalization preset %q", parameters.Preset)
	}
	return preset, nil
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
	preset, err := parseNormalizationParameters(job.Parameters)
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
		if err := reporter.Progress(0, nil); err != nil {
			return Result{}, err
		}
		filter := normalizationFilter(preset)
		if err := h.runFFmpeg(ctx, input, flacTemp, mp3Temp, filter, func(percent int) error {
			return reporter.Progress(percent, nil)
		}); err != nil {
			return Result{}, err
		}
		if err := CommitArtifacts(markerPath, marker, artifacts); err != nil {
			return Result{}, err
		}
	}
	if err := h.store.SetNormalizationPreset(job.SermonID, string(preset)); err != nil {
		return Result{}, fmt.Errorf("store normalization preset: %w", err)
	}
	if err := reporter.Progress(100, nil); err != nil {
		return Result{}, err
	}
	return Result{}, nil
}

func normalizationFilter(preset NormalizationPreset) string {
	settings := normalizationPresets[preset]
	filters := []string{"aformat=channel_layouts=mono"}
	if settings.gate != "" {
		filters = append(filters, settings.gate)
	}
	filters = append(filters,
		fmt.Sprintf("loudnorm=I=%d:LRA=11:TP=-1.5:dual_mono=true", settings.loudnessLU),
		"aresample=44100",
		"asplit=2[master][proxy]",
	)
	return "[0:a:0]" + strings.Join(filters, ",")
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
	duration, _ := probeDuration(ctx, input)
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

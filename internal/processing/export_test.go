package processing

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

func TestExportNormalizesOnlyPreparedSourcesToSmallMonoMP3(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	for _, prepared := range []bool{true, false} {
		t.Run(map[bool]string{true: "untreated-source", false: "legacy-treated-source"}[prepared], func(t *testing.T) {
			dir := t.TempDir()
			sermonDir := filepath.Join(dir, "sermon")
			if err := os.MkdirAll(filepath.Join(sermonDir, "edit-first"), 0755); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(sermonDir, "edit-first", "edited.flac")
			// A quiet stereo source distinguishes missing treatment from doubled
			// legacy treatment and verifies the export downmix independently.
			out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=12", "-af", "volume=0.2", "-ac", "2", source).CombinedOutput()
			if err != nil {
				t.Fatalf("fixture: %v: %s", err, out)
			}
			if prepared {
				if err := writeMarker(filepath.Join(sermonDir, ".source-prepared.json"), CompletionMarker{JobID: "prepare", PipelineVersion: 2}); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, _ := json.Marshal(store.ExportSnapshot{Source: "edit-first/edited.flac"})
			job := store.Job{ID: "first", SermonID: "sermon", Parameters: string(snapshot)}
			h := NewExportHandler(dir)
			if _, err := h.Run(context.Background(), job, &recordingReporter{}); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(sermonDir, "export-first", "export.mp3")
			if err := probeAudio(output, audioSpec{codec: "mp3", sampleRate: 44100, channels: 1}); err != nil {
				t.Fatal(err)
			}
			bitrate, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=bit_rate", "-of", "default=noprint_wrappers=1:nokey=1", output).Output()
			if err != nil || strings.TrimSpace(string(bitrate)) != "32000" {
				t.Fatalf("bitrate: %s, %v", bitrate, err)
			}
			seconds, err := probeDuration(context.Background(), output)
			if err != nil || math.Abs(seconds-12) > 0.08 {
				t.Fatalf("duration: %f, %v", seconds, err)
			}
			info, err := os.Stat(output)
			if err != nil || info.Size() > 52000 {
				t.Fatalf("12-second export should be about 48 KB: %v, %v", info, err)
			}
			measurement, err := exec.Command("ffmpeg", "-hide_banner", "-nostats", "-i", output, "-af", "loudnorm=dual_mono=true:print_format=json", "-f", "null", "-").CombinedOutput()
			if err != nil {
				t.Fatalf("measure export: %v: %s", err, measurement)
			}
			var stats map[string]string
			if err := json.Unmarshal(measurement[strings.LastIndex(string(measurement), "{"):], &stats); err != nil {
				t.Fatal(err)
			}
			loudness, err := strconv.ParseFloat(stats["input_i"], 64)
			if err != nil || (prepared && math.Abs(loudness-(-16)) > 0.6) || (!prepared && loudness > -30) {
				t.Fatalf("prepared=%t output loudness=%f: %v", prepared, loudness, err)
			}
			// Retries reuse the durable artifact; a second export starts from the
			// same lossless audio instead of treating the first export again.
			before := info.ModTime()
			if _, err := h.Run(context.Background(), job, &recordingReporter{}); err != nil {
				t.Fatal(err)
			}
			info, _ = os.Stat(output)
			if !info.ModTime().Equal(before) {
				t.Fatal("retry re-encoded committed output")
			}
			job.ID = "second"
			if _, err := h.Run(context.Background(), job, &recordingReporter{}); err != nil {
				t.Fatal(err)
			}
			first, _ := os.ReadFile(output)
			second, _ := os.ReadFile(filepath.Join(sermonDir, "export-second", "export.mp3"))
			if string(first) != string(second) {
				t.Fatal("repeat export changed audio")
			}
		})
	}
}

func TestUploadPreparationDefersTreatmentUntilExport(t *testing.T) {
	st := processingTestStore(t)
	uploads := t.TempDir()
	dir := filepath.Join(uploads, "sermon")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "original.wav"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSermon(store.Sermon{ID: "sermon", Stage: "upload", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CompleteUpload("sermon", "prepare", time.Now()); err != nil {
		t.Fatal(err)
	}
	job, err := st.GetJob("prepare")
	if err != nil {
		t.Fatal(err)
	}
	h := NewNormalizeHandler(st, uploads)
	h.runFFmpeg = func(_ context.Context, _, flac, mp3, filter string, _ func(int) error) error {
		if strings.Contains(filter, "agate") || strings.Contains(filter, "loudnorm") {
			t.Fatalf("upload applied treatment: %s", filter)
		}
		if err := os.WriteFile(flac, []byte("source"), 0600); err != nil {
			return err
		}
		return os.WriteFile(mp3, []byte("proxy"), 0600)
	}
	h.probe = func(string, audioSpec) error { return nil }
	result, err := h.Run(context.Background(), job, &recordingReporter{})
	if err != nil || result.Next == nil || result.Next.Type != "transcribe" {
		t.Fatalf("preparation: %+v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".source-prepared.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Run(context.Background(), job, &recordingReporter{}); err != nil {
		t.Fatalf("prepared retry without original: %v", err)
	}
}

func TestExportMissingFLACUsesMatchingAudio(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	for _, tc := range []struct {
		name, source, available string
		prepared, wantError     bool
	}{
		{"legacy-proxy", "normalized.flac", "normalized.mp3", false, false},
		{"prepared-proxy", "normalized.flac", "normalized.mp3", true, false},
		{"edited-playback", "edit-first/edited.flac", "edit-first/edited.mp3", false, false},
		{"original-only", "normalized.flac", "original.wav", false, false},
		{"missing-edited-audio", "edit-first/edited.flac", "normalized.mp3", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uploads := t.TempDir()
			dir := filepath.Join(uploads, "sermon")
			available := filepath.Join(dir, tc.available)
			if err := os.MkdirAll(filepath.Dir(available), 0755); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=330:duration=8:sample_rate=48000", "-af", "volume=0.2", "-ac", "2", available).CombinedOutput()
			if err != nil {
				t.Fatalf("fixture: %v: %s", err, out)
			}
			if tc.source == "edit-first/edited.flac" && !tc.wantError {
				// A different full-recording duration exposes choosing the wrong
				// fallback and silently losing previously applied cuts.
				out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=880:duration=20", filepath.Join(dir, "normalized.mp3")).CombinedOutput()
				if err != nil {
					t.Fatalf("full recording: %v: %s", err, out)
				}
			}
			if tc.prepared {
				if err := writeMarker(filepath.Join(dir, ".source-prepared.json"), CompletionMarker{JobID: "prepare"}); err != nil {
					t.Fatal(err)
				}
			}
			parameters, _ := json.Marshal(store.ExportSnapshot{Source: tc.source})
			job := store.Job{ID: "export", SermonID: "sermon", Parameters: string(parameters)}
			_, err = NewExportHandler(uploads).Run(context.Background(), job, &recordingReporter{})
			if tc.wantError {
				if err == nil {
					t.Fatal("missing edited audio silently exported the full recording")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(dir, "export-export", "export.mp3")
			if err := probeAudio(output, audioSpec{codec: "mp3", sampleRate: 44100, channels: 1}); err != nil {
				t.Fatal(err)
			}
			duration, err := probeDuration(context.Background(), output)
			if err != nil || math.Abs(duration-8) > 0.12 {
				t.Fatalf("fallback duration: %f, %v", duration, err)
			}
			measurement, err := exec.Command("ffmpeg", "-hide_banner", "-nostats", "-i", output, "-af", "loudnorm=dual_mono=true:print_format=json", "-f", "null", "-").CombinedOutput()
			if err != nil {
				t.Fatalf("measure fallback: %v: %s", err, measurement)
			}
			var stats map[string]string
			if err := json.Unmarshal(measurement[strings.LastIndex(string(measurement), "{"):], &stats); err != nil {
				t.Fatal(err)
			}
			loudness, err := strconv.ParseFloat(stats["input_i"], 64)
			normalized := tc.prepared || tc.available == "original.wav"
			if err != nil || (normalized && math.Abs(loudness-(-16)) > 0.6) || (!normalized && loudness > -30) {
				t.Fatalf("normalized=%t fallback loudness=%f: %v", normalized, loudness, err)
			}
		})
	}
}

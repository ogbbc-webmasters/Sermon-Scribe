package processing

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

func TestUploadNormalizesBeforeTranscription(t *testing.T) {
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
		if !strings.Contains(filter, "agate") || !strings.Contains(filter, "loudnorm=I=-16") {
			t.Fatalf("upload did not apply treatment before transcription: %s", filter)
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
	if _, err := os.Stat(filepath.Join(dir, ".normalization-complete.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Run(context.Background(), job, &recordingReporter{}); err != nil {
		t.Fatalf("prepared retry without original: %v", err)
	}
}

func TestExportStreamCopiesMono32KbpsMP3(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	for _, tc := range []struct {
		name, bitrate string
		channels      string
		wantCopy      bool
	}{
		{"ready", "32k", "1", true},
		{"higher-bitrate", "64k", "1", false},
		{"stereo", "32k", "2", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uploads := t.TempDir()
			dir := filepath.Join(uploads, "sermon")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			input := filepath.Join(dir, "normalized.mp3")
			out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=330:duration=8:sample_rate=44100", "-ac", tc.channels, "-b:a", tc.bitrate, input).CombinedOutput()
			if err != nil {
				t.Fatalf("fixture: %v: %s", err, out)
			}
			job := store.Job{ID: "export", SermonID: "sermon", Parameters: `{"source":"normalized.mp3"}`}
			h := NewExportHandler(uploads)
			_, err = h.Run(context.Background(), job, &recordingReporter{})
			if !tc.wantCopy {
				if err == nil {
					t.Fatal("export accepted audio outside the delivery format")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(dir, "export-export", "export.mp3")
			if err := probeAudio(output, audioSpec{codec: "mp3", sampleRate: 44100, channels: 1, bitRate: 32000}); err != nil {
				t.Fatal(err)
			}
			hashes := func(path string) string {
				t.Helper()
				out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "a:0", "-show_packets", "-show_entries", "packet=data_hash", "-show_data_hash", "sha256", "-of", "json", path).Output()
				if err != nil {
					t.Fatal(err)
				}
				var data struct {
					Packets []struct {
						Hash string `json:"data_hash"`
					}
				}
				if err := json.Unmarshal(out, &data); err != nil || len(data.Packets) == 0 {
					t.Fatalf("packet hashes: %v", err)
				}
				var hashes []string
				for _, packet := range data.Packets {
					hashes = append(hashes, packet.Hash)
				}
				return strings.Join(hashes, ",")
			}
			if hashes(input) != hashes(output) {
				t.Fatal("export changed compressed audio packets")
			}
			if err := os.Rename(input, input+".held"); err != nil {
				t.Fatal(err)
			}
			if _, err := h.Run(context.Background(), job, &recordingReporter{}); err != nil {
				t.Fatalf("committed retry tried to read source again: %v", err)
			}
		})
	}
}

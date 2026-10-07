package processing

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEditingAudioContent(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "source.flac")
	// Asymmetric regions expose wrong seeks, repeated starts, and cue leakage.
	signal := "aevalsrc='0.2*sin(2*PI*if(lt(t,4),330,if(lt(t,14),660,880))*t)':s=48000:d=18"
	out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", signal, "-ac", "2", source).CombinedOutput()
	if err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	decode := func(audio []byte) []float32 {
		t.Helper()
		cmd := exec.Command("ffmpeg", "-v", "error", "-i", "pipe:0", "-ar", "48000", "-ac", "1", "-f", "f32le", "pipe:1")
		cmd.Stdin = bytes.NewReader(audio)
		raw, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		samples := make([]float32, len(raw)/4)
		for i := range samples {
			samples[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
		}
		return samples
	}
	check := func(samples []float32, duration float64, windows [][3]float64) {
		t.Helper()
		if got := float64(len(samples)) / 48000; math.Abs(got-duration) > 0.002 {
			t.Fatalf("duration %f, want %f", got, duration)
		}
		for _, w := range windows {
			from, to := int(w[0]*48000), int(w[1]*48000)
			crossings := 0
			for i := from + 1; i < to; i++ {
				if w[2] == 0 && math.Abs(float64(samples[i])) > 0.00001 {
					t.Fatalf("window %.2f–%.2f is not silent", w[0], w[1])
				}
				if samples[i-1] <= 0 && samples[i] > 0 {
					crossings++
				}
			}
			frequency := float64(crossings) / (w[1] - w[0])
			if math.Abs(frequency-w[2]) > 15 {
				t.Fatalf("window %.2f–%.2f: frequency %.1f, want %.1f", w[0], w[1], frequency, w[2])
			}
		}
	}
	for _, tc := range []struct {
		mode                 string
		start, end, duration float64
		windows              [][3]float64
	}{
		{"breakpoint", 14, 0, 7, [][3]float64{{0.2, 2.8, 660}, {3.05, 3.2, 0}, {3.35, 3.65, 1200}, {3.8, 3.95, 0}, {4.2, 6.8, 880}}},
		{"section", 2, 18, 7, [][3]float64{{0.2, 0.8, 330}, {3.1, 3.9, 250}, {4.2, 4.8, 880}}},
		{"section", 2, 4, 2, [][3]float64{{0.2, 1.8, 330}}},
	} {
		t.Run(fmt.Sprintf("%s-%.0f-%.0f", tc.mode, tc.start, tc.end), func(t *testing.T) {
			audio, err := PreviewAudio(context.Background(), source, tc.mode, tc.start, tc.end, 18)
			if err != nil {
				t.Fatal(err)
			}
			check(decode(audio), tc.duration, tc.windows)
		})
	}
	rendered := filepath.Join(dir, "rendered.flac")
	if err := renderRanges(context.Background(), source, rendered, []AudioRange{{1, 2}, {15, 17}}); err != nil {
		t.Fatal(err)
	}
	audio, err := os.ReadFile(rendered)
	if err != nil {
		t.Fatal(err)
	}
	check(decode(audio), 3, [][3]float64{{0.2, 0.8, 330}, {1.2, 2.8, 880}})
}

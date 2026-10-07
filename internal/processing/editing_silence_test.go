package processing

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFFmpegSilenceFiveVersusSixSeconds(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	for _, duration := range []int{5, 6} {
		t.Run(fmt.Sprint(duration), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.flac")
			out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono", "-t", fmt.Sprint(duration), "-c:a", "flac", path).CombinedOutput()
			if err != nil {
				t.Fatalf("fixture %v %s", err, out)
			}
			out, err = exec.CommandContext(context.Background(), "ffmpeg", "-hide_banner", "-i", path, "-af", "silencedetect=noise=-40dB:d=5", "-f", "null", "-").CombinedOutput()
			if err != nil {
				t.Fatalf("detection %v %s", err, out)
			}
			want := 0
			if duration == 6 {
				want = 2
			}
			if b := silenceBreakpoints(string(out), float64(duration)); len(b) != want {
				t.Fatalf("duration %d boundaries %+v: %s", duration, b, out)
			}
		})
	}
}

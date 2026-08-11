package processing

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

type editParameters struct {
	Duration float64  `json:"duration"`
	Regions  []Region `json:"regions"`
}
type editRunner func(context.Context, string, string, []Region, float64, func(int) error) error

type ApplyEditsHandler struct {
	store      *store.Store
	uploadsDir string
	run        editRunner
	probe      audioProbe
}

func NewApplyEditsHandler(st *store.Store, uploads string) *ApplyEditsHandler {
	return &ApplyEditsHandler{store: st, uploadsDir: uploads, run: runEditFFmpeg, probe: probeAudio}
}

func (h *ApplyEditsHandler) Run(ctx context.Context, job store.Job, reporter Reporter) (Result, error) {
	var p editParameters
	d := json.NewDecoder(strings.NewReader(job.Parameters))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return Result{}, fmt.Errorf("decode edit plan: %w", err)
	}
	if err := ValidateRegions(p.Regions, p.Duration); err != nil {
		return Result{}, err
	}
	dir := filepath.Join(h.uploadsDir, job.SermonID)
	input := filepath.Join(dir, "normalized.flac")
	temp := filepath.Join(dir, ".edit-"+job.ID+".mp3")
	final := filepath.Join(dir, "final.mp3")
	defer os.Remove(temp)
	if err := h.run(ctx, input, temp, p.Regions, p.Duration, func(v int) error { return reporter.Progress(v, nil) }); err != nil {
		return Result{}, err
	}
	if err := h.probe(temp, audioSpec{codec: "mp3", sampleRate: 44100, channels: 1}); err != nil {
		return Result{}, err
	}
	if err := os.Rename(temp, final); err != nil {
		return Result{}, fmt.Errorf("publish final audio: %w", err)
	}
	if err := syncDir(dir); err != nil {
		return Result{}, err
	}
	regions, _ := json.Marshal(p.Regions)
	if err := h.store.SetAppliedRegions(job.SermonID, regions); err != nil {
		return Result{}, err
	}
	return Result{}, reporter.Progress(100, nil)
}

func runEditFFmpeg(ctx context.Context, input, output string, regions []Region, duration float64, onProgress func(int) error) error {
	parts := []string{}
	labels := []string{}
	n := 0
	for _, r := range regions {
		if !r.Keep {
			continue
		}
		label := fmt.Sprintf("a%d", n)
		parts = append(parts, fmt.Sprintf("[0:a]atrim=start=%.6f:end=%.6f,asetpts=PTS-STARTPTS[%s]", r.Start, r.End, label))
		labels = append(labels, "["+label+"]")
		n++
	}
	filter := strings.Join(parts, ";") + ";" + strings.Join(labels, "") + fmt.Sprintf("concat=n=%d:v=0:a=1[out]", n)
	args := []string{"-hide_banner", "-nostdin", "-y", "-i", input, "-filter_complex", filter, "-map", "[out]", "-ac", "1", "-ar", "44100", "-c:a", "libmp3lame", "-b:a", "32k", output, "-progress", "pipe:1", "-nostats"}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	s := bufio.NewScanner(stdout)
	for s.Scan() {
		if strings.HasPrefix(s.Text(), "out_time_us=") {
			v, _ := strconv.ParseFloat(strings.TrimPrefix(s.Text(), "out_time_us="), 64)
			if err := onProgress(min(99, int(v/1e6/duration*100))); err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return fmt.Errorf("report ffmpeg edit progress: %w", err)
			}
		}
	}
	if err := s.Err(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("read ffmpeg edit progress: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg edit failed: %s", strings.TrimSpace(stderr.String()))
	}
	return nil
}

// RenderAudioSection creates a seekable MP3 containing only the requested
// source interval. The output starts at zero so browser-native audio controls
// expose the section's duration rather than the full recording's duration.
func RenderAudioSection(ctx context.Context, input, output string, start, end float64) error {
	filter := fmt.Sprintf("atrim=start=%.6f:end=%.6f,asetpts=PTS-STARTPTS", start, end)
	args := []string{"-hide_banner", "-nostdin", "-y", "-i", input, "-af", filter, "-ac", "1", "-ar", "44100", "-c:a", "libmp3lame", "-b:a", "128k", output}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg section render failed: %s", strings.TrimSpace(stderr.String()))
	}
	return nil
}

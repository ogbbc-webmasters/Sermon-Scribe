package processing

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

func TestSilenceStrictThreshold(t *testing.T) {
	for _, tc := range []struct {
		log      string
		duration float64
		count    int
	}{
		{"silence_start: 1\nsilence_end: 6", 10, 0},
		{"silence_start: 1\nsilence_end: 6.001", 10, 2},
		{"silence_start: 0", 5, 0}, {"silence_start: 0", 6, 2},
	} {
		if got := len(silenceBreakpoints(tc.log, tc.duration)); got != tc.count {
			t.Fatalf("%q: got %d want %d", tc.log, got, tc.count)
		}
	}
}
func TestTranscriptMapping(t *testing.T) {
	source := store.TranscriptionMetadata{Words: []store.TranscriptWord{{Word: "one", Start: 0, End: 1}, {Word: "drop", Start: 2, End: 3}, {Word: "two", Start: 4.8, End: 5.4}}}
	text, m := MapTranscript(source, []AudioRange{{0, 1}, {5, 6}})
	if text != "one two" || m.Duration != 2 || m.Words[1].Start != 1 || math.Abs(m.Words[1].End-1.4) > 1e-9 {
		t.Fatalf("mapping %q %+v", text, m)
	}
	text, m = MapTranscript(store.TranscriptionMetadata{Segments: []store.TranscriptSegment{{Text: "phrase", Start: 4, End: 6}}}, []AudioRange{{5, 7}})
	if text != "phrase" || m.Segments[0].Start != 0 || m.Segments[0].End != 1 {
		t.Fatalf("segment fallback %q %+v", text, m)
	}
	if source.Words[2].Start != 4.8 {
		t.Fatal("source mutated")
	}
}
func TestEditingFFmpegPipelinePauseAndRedo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	st := processingTestStore(t)
	ctx := context.Background()
	uploads := t.TempDir()
	dir := filepath.Join(uploads, "edit-test")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSermon(store.Sermon{ID: "edit-test", Stage: "editing", Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "normalized.flac")
	out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono", "-t", "8", "-c:a", "flac", source).CombinedOutput()
	if err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	m := store.TranscriptionMetadata{Duration: 8, Words: []store.TranscriptWord{{Word: "first", Start: 1, End: 2, SpeakerLabel: "A"}, {Word: "last", Start: 5, End: 6, SpeakerLabel: "B"}}}
	if err := st.SaveSourceTranscription("edit-test", "first last", m); err != nil {
		t.Fatal(err)
	}
	if err := st.EnqueueJob(store.NewJob{ID: "prepare", SermonID: "edit-test", Type: "prepare_edit", Stage: "editing"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	job, err := st.ClaimNextJob(ctx, []string{"prepare_edit"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	h := NewEditingHandler(st, uploads)
	result, err := h.Run(ctx, job, &recordingReporter{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Next != nil {
		t.Fatal("preparation did not pause")
	}
	sm, err := st.CompleteJob(job, nil, time.Now())
	if err != nil || sm.Stage != "editing" || sm.Status != "done" {
		t.Fatalf("pause %+v %v", sm, err)
	}
	d, err := st.GetEditing("edit-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Breakpoints) != 3 || d.Breakpoints[1].Kind != "speaker" {
		t.Fatalf("speaker proposal %+v", d)
	}
	for _, tc := range []struct {
		mode       string
		start, end float64
	}{{"breakpoint", 0, 0}, {"breakpoint", 5, 0}, {"breakpoint", 8, 0}, {"section", 0, 8}, {"section", 1, 4}} {
		audio, err := PreviewAudio(ctx, source, tc.mode, tc.start, tc.end, 8)
		if err != nil || len(audio) < 44 || string(audio[:4]) != "RIFF" {
			t.Fatalf("preview %+v: %v", tc, err)
		}
	}
	d.Sections[0].Keep = false
	d, err = st.MutateEditing("edit-test", &d, d.Revision, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.MutateEditing("edit-test", nil, d.Revision, false, "render"); err != nil {
		t.Fatal(err)
	}
	job, err = st.ClaimNextJob(ctx, []string{"render_edit"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var snapshot store.EditSnapshot
	if err = json.Unmarshal([]byte(job.Parameters), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Draft.Sections[0].Keep {
		t.Fatal("snapshot changed")
	}
	result, err = h.Run(ctx, job, &recordingReporter{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Next == nil || result.Next.Type != "extract_metadata" {
		t.Fatal("render did not enqueue metadata")
	}
	sm, err = st.GetSermon("edit-test")
	if err != nil || sm.Transcript == nil || *sm.Transcript != "last" {
		t.Fatalf("edited transcript %+v %v", sm, err)
	}
	path, err := st.EditingPlayback("edit-test")
	if err != nil || path == "" {
		t.Fatal("missing committed playback")
	}
	duration, err := audioDuration(ctx, filepath.Join(dir, filepath.Dir(path), "edited.flac"))
	if err != nil || math.Abs(duration-3) > 0.01 {
		t.Fatalf("render duration %v %v", duration, err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal("source lost")
	}
	// Complete rendering without metadata for the redo exercise.
	if _, err = st.CompleteJob(job, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = st.MutateEditing("edit-test", nil, d.Revision, true, "skip"); err != nil {
		t.Fatal(err)
	}
	job, err = st.ClaimNextJob(ctx, []string{"render_edit"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Run(ctx, job, &recordingReporter{}); err != nil {
		t.Fatal(err)
	}
	if path, err = st.EditingPlayback("edit-test"); err != nil || path != "" {
		t.Fatal("skip still active")
	}
	saved, _ := st.GetEditing("edit-test")
	if saved.Sections[0].Keep {
		t.Fatal("skip erased draft")
	}
}

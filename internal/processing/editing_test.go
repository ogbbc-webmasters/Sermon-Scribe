package processing

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

func TestSpeakerDraftUsesEverySegmentStart(t *testing.T) {
	speaker := 0
	d := store.Editing{Duration: 10, Revision: 7,
		Breakpoints: []store.Breakpoint{{ID: "start", Kind: "start"}, {ID: "old", Time: 4, Kind: "manual"}, {ID: "end", Time: 10, Kind: "end"}},
		Sections:    []store.Section{{ID: "old-a", Keep: false}, {ID: "old-b", Keep: true}},
	}
	segments := []store.TranscriptSegment{
		{Start: 5, Speaker: &speaker}, {Start: 0, Speaker: &speaker},
		{Start: 0.049, Speaker: &speaker}, {Start: 0.05, Speaker: &speaker},
		{Start: 2, Speaker: &speaker}, {Start: 2, Speaker: &speaker},
		{Start: 3}, {Start: 9.95, Speaker: &speaker}, {Start: 10, Speaker: &speaker},
	}
	got := SpeakerDraft(d, segments)
	var times []float64
	for _, b := range got.Breakpoints {
		times = append(times, b.Time)
	}
	if !reflect.DeepEqual(times, []float64{0, 0.05, 2, 5, 9.95, 10}) {
		t.Fatalf("segment boundaries: %v", times)
	}
	if got.Revision != 7 || got.Breakpoints[0] != d.Breakpoints[0] || got.Breakpoints[len(got.Breakpoints)-1] != d.Breakpoints[2] {
		t.Fatal("recording endpoints or revision changed")
	}
	for _, section := range got.Sections {
		if !section.Keep {
			t.Fatal("regenerated sections must default to Keep")
		}
	}
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
	if d.Breakpoints[1].Time != 4 || segments[0].Start != 5 {
		t.Fatal("source draft or segments mutated")
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

func TestKeptSectionsPreservePhrases(t *testing.T) {
	snap := store.EditSnapshot{Draft: store.Editing{
		Duration:    10,
		Breakpoints: []store.Breakpoint{{Time: 0}, {Time: 1.5}, {Time: 3}, {Time: 5}, {Time: 10}},
		Sections:    []store.Section{{Keep: true}, {Keep: true}, {Keep: false}, {Keep: true}},
	}}
	ranges := keptRanges(snap)
	if len(ranges) != 2 || ranges[0] != (AudioRange{0, 3}) || ranges[1] != (AudioRange{5, 10}) {
		t.Fatalf("adjacent kept sections not merged: %+v", ranges)
	}
	source := store.TranscriptionMetadata{
		Words:    []store.TranscriptWord{{Word: "first", Start: 1, End: 1.4}, {Word: "phrase", Start: 1.4, End: 2}, {Word: "drop", Start: 3.5, End: 4}, {Word: "last", Start: 5, End: 5.5}, {Word: "phrase", Start: 5.5, End: 6}, {Word: "next", Start: 6, End: 7}},
		Segments: []store.TranscriptSegment{{Start: 1, End: 2}, {Start: 3.5, End: 4}, {Start: 5, End: 6}, {Start: 6, End: 7}},
	}
	text, mapped := MapTranscript(source, ranges)
	if text != "first phrase last phrase next" || len(mapped.Segments) != 3 || mapped.Segments[0].Text != "first phrase" || mapped.Segments[1].Text != "last phrase" || mapped.Segments[1].Start != 3 || mapped.Segments[2].Start != 4 {
		t.Fatalf("phrase mapping: %q %+v", text, mapped)
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
	speaker := 1
	m.Segments = []store.TranscriptSegment{{Start: 5, End: 6, Text: "last", Speaker: &speaker}}
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
	if len(d.Breakpoints) != 3 || len(d.Sections) != 2 || !d.Sections[0].Keep ||
		d.Breakpoints[0].Kind != "start" || d.Breakpoints[0].Time != 0 ||
		d.Breakpoints[1].Kind != "speaker" || d.Breakpoints[1].Time != 5 ||
		d.Breakpoints[2].Kind != "end" || d.Breakpoints[2].Time != 8 {
		t.Fatalf("preparation must use speaker segment starts: %+v", d)
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
	// Manual boundaries can be added alongside generated speaker boundaries.
	d.Breakpoints = []store.Breakpoint{d.Breakpoints[0], {ID: "manual", Time: 2, Kind: "manual"}, d.Breakpoints[1], d.Breakpoints[2]}
	d.Sections = []store.Section{{ID: "first", Keep: false}, {ID: "middle", Keep: false}, {ID: "last", Keep: true}}
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

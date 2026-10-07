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
		{Start: 5, End: 7, Speaker: &speaker}, {Start: 0, End: 0.01, Speaker: &speaker},
		{Start: 0.049, End: 0.05, Speaker: &speaker}, {Start: 0.05, End: 0.06, Speaker: &speaker},
		{Start: 2, End: 2.5, Speaker: &speaker}, {Start: 2, End: 2.5, Speaker: &speaker},
		{Start: 3}, {Start: 9.95, End: 9.96, Speaker: &speaker}, {Start: 10, End: 10.01, Speaker: &speaker},
	}
	got := SpeakerDraft(d, segments, false)
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

func TestSpeakerDraftMarksLongSilenceForDeletion(t *testing.T) {
	speaker := 0
	d := store.Editing{
		Duration: 20,
		Breakpoints: []store.Breakpoint{
			{ID: "start", Kind: "start"},
			{ID: "end", Time: 20, Kind: "end"},
		},
		Sections: []store.Section{{ID: "whole", Keep: true}},
	}
	segments := []store.TranscriptSegment{
		{Start: 1, End: 2, Speaker: &speaker},
		{Start: 5, End: 6, Speaker: &speaker},
		{Start: 9, End: 9.5, Speaker: &speaker},
		{Start: 13, End: 14, Speaker: &speaker},
	}

	got := SpeakerDraft(d, segments, false)
	wantTimes := []float64{0, 1, 5, 9, 9.5, 13, 20}
	if len(got.Breakpoints) != len(wantTimes) {
		t.Fatalf("breakpoint count: %d, want %d", len(got.Breakpoints), len(wantTimes))
	}
	for i, want := range wantTimes {
		if got.Breakpoints[i].Time != want {
			t.Fatalf("breakpoint %d time: %v, want %v", i, got.Breakpoints[i].Time, want)
		}
	}
	if got.Breakpoints[4].Kind != "silence_start" {
		t.Fatalf("long pause boundary kind: %q", got.Breakpoints[4].Kind)
	}
	if !got.Sections[3].Keep || got.Sections[4].Keep {
		t.Fatalf("speech/silence keep decisions: %+v", got.Sections)
	}
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}

	got.Sections[4].Keep = true
	regenerated := SpeakerDraft(got, segments, true)
	if !regenerated.Sections[4].Keep {
		t.Fatal("regeneration discarded an explicit keep choice for an unchanged silence section")
	}
}

func TestSpeakerDraftPreservesEditedBoundariesAndSections(t *testing.T) {
	speaker := 0
	origin := 2.0
	d := store.Editing{Duration: 10,
		Breakpoints: []store.Breakpoint{
			{ID: "start", Kind: "start"},
			{ID: "moved", Time: 2.4, Kind: "speaker", Edited: true, SourceTime: &origin},
			{ID: "manual", Time: 5.02, Kind: "manual"},
			{ID: "end", Time: 10, Kind: "end"},
		},
		Sections: []store.Section{{ID: "a", Keep: false}, {ID: "b", Keep: true}, {ID: "c", Keep: false}},
	}
	segments := []store.TranscriptSegment{{Start: 2, End: 4.8, Speaker: &speaker}, {Start: 5, End: 6.8, Speaker: &speaker}, {Start: 7, End: 8, Speaker: &speaker}}
	got := SpeakerDraft(d, segments, true)
	var times []float64
	for _, b := range got.Breakpoints {
		times = append(times, b.Time)
	}
	if !reflect.DeepEqual(times, []float64{0, 2.4, 5.02, 7, 10}) {
		t.Fatalf("preserved boundaries: %v", times)
	}
	if got.Breakpoints[1].ID != "moved" || got.Breakpoints[2].ID != "manual" {
		t.Fatal("edited boundary identities lost")
	}
	if got.Sections[0].ID != "a" || got.Sections[1].ID != "b" || got.Sections[0].Keep || !got.Sections[1].Keep || got.Sections[2].Keep || got.Sections[3].Keep {
		t.Fatalf("section decisions: %+v", got.Sections)
	}
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
	reset := SpeakerDraft(d, segments, false)
	if reset.Breakpoints[1].Time != 2 || reset.Breakpoints[2].Time != 5 || !reset.Sections[0].Keep {
		t.Fatalf("all mode did not reset: %+v", reset)
	}
}

func TestSpeakerDraftPreservesLegacyMovedBoundary(t *testing.T) {
	speaker := 0
	d := store.Editing{Duration: 10,
		Breakpoints: []store.Breakpoint{{ID: "start", Kind: "start"}, {ID: "moved", Time: 2.3, Kind: "speaker"}, {ID: "end", Time: 10, Kind: "end"}},
		Sections:    []store.Section{{ID: "a", Keep: false}, {ID: "b", Keep: true}},
	}
	got := SpeakerDraft(d, []store.TranscriptSegment{{Start: 2, Speaker: &speaker}}, true)
	if len(got.Breakpoints) != 3 || got.Breakpoints[1].Time != 2.3 || !got.Breakpoints[1].Edited || *got.Breakpoints[1].SourceTime != 2 {
		t.Fatalf("legacy move lost or duplicated: %+v", got.Breakpoints)
	}
	if d.Breakpoints[1].SourceTime != nil || d.Breakpoints[1].Edited {
		t.Fatal("source draft mutated")
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
	previewPath := filepath.Join(t.TempDir(), "preview.wav")
	for _, tc := range []struct {
		mode             string
		start, end, want float64
	}{{"breakpoint", 0, 0, 2}, {"breakpoint", 5, 0, 3}, {"breakpoint", 8, 0, 2}, {"section", 0, 8, 7}, {"section", 1, 4, 3}} {
		audio, err := PreviewAudio(ctx, source, tc.mode, tc.start, tc.end, 8)
		if err != nil || len(audio) < 44 || string(audio[:4]) != "RIFF" {
			t.Fatalf("preview %+v: %v", tc, err)
		}
		if err := os.WriteFile(previewPath, audio, 0600); err != nil {
			t.Fatal(err)
		}
		seconds, err := audioDuration(ctx, previewPath)
		if err != nil || math.Abs(seconds-tc.want) > 0.01 {
			t.Fatalf("preview %+v duration: %f, %v", tc, seconds, err)
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
	// Completion publishes the new audio generation and queues metadata from
	// the cut transcript, rather than stopping after the render.
	sm, err = st.CompleteJob(job, result.Next, time.Now())
	if err != nil || sm.Stage != "metadata" || sm.Status != "pending" || sm.PlaybackVersion != path || *sm.Transcript != "last" {
		t.Fatalf("metadata handoff: %+v %v", sm, err)
	}
	metadataJob, err := st.ClaimNextJob(ctx, []string{"extract_metadata"}, time.Now())
	if err != nil || metadataJob.SermonID != "edit-test" {
		t.Fatalf("metadata not queued: %+v %v", metadataJob, err)
	}
	if _, err = st.CompleteJob(metadataJob, nil, time.Now()); err != nil {
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

func TestWaveformPeaksPreserveTimeAndSignedAmplitude(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	input := filepath.Join(t.TempDir(), "source.flac")
	// Different positive/negative levels and silence distinguish correct peak
	// placement from averaging, signed maxima, or shifted time buckets.
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "aevalsrc=if(lt(t\\,1)\\,0.2\\,if(lt(t\\,2)\\,-0.6\\,if(lt(t\\,3)\\,0\\,0.1))):s=8000:d=4", input).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	peaks, hires, err := WaveformPeaks(context.Background(), input, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(peaks) != 4000 {
		t.Fatalf("peak count: %d", len(peaks))
	}
	if len(hires) != 400 {
		t.Fatalf("high-resolution pair count: %d", len(hires)/2)
	}
	for index, want := range map[int]float64{100: 0.2, 1200: 0.6, 2200: 0, 3500: 0.1} {
		if math.Abs(peaks[index]-want) > 0.0001 {
			t.Fatalf("peak %d: %f, want %f", index, peaks[index], want)
		}
	}
	if hires[0] <= 0 || hires[1] <= 0 || hires[100] >= 0 || hires[101] >= 0 || hires[200] != 0 || hires[201] != 0 || hires[300] <= 0 || hires[301] <= 0 {
		t.Fatalf("high-resolution signed min/max pairs: %+v", hires[:8])
	}
	if _, _, err := WaveformPeaks(context.Background(), input, 0); err == nil {
		t.Fatal("accepted zero duration")
	}
	if _, _, err := WaveformPeaks(context.Background(), input+"missing", 4); err == nil {
		t.Fatal("accepted missing source")
	}
}

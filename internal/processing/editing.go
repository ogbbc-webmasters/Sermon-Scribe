package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

type EditingHandler struct {
	store      *store.Store
	uploadsDir string
}

func NewEditingHandler(st *store.Store, dir string) *EditingHandler { return &EditingHandler{st, dir} }

type AudioRange struct{ Start, End float64 }

func audioDuration(ctx context.Context, path string) (float64, error) {
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
}

var silencePattern = regexp.MustCompile(`silence_(start|end):\s*([0-9.eE+-]+)`)

func silenceBreakpoints(log string, duration float64) []store.Breakpoint {
	var result []store.Breakpoint
	start := -1.0
	add := func(end float64) {
		if start >= 0 && end-start > 5 {
			result = append(result, store.Breakpoint{ID: uuid.NewString(), Time: start, Kind: "silence_start"}, store.Breakpoint{ID: uuid.NewString(), Time: end, Kind: "silence_end"})
		}
		start = -1
	}
	for _, match := range silencePattern.FindAllStringSubmatch(log, -1) {
		v, _ := strconv.ParseFloat(match[2], 64)
		if match[1] == "start" {
			start = v
		} else {
			add(v)
		}
	}
	if start >= 0 {
		add(duration)
	}
	return result
}
func proposedDraft(duration float64, b []store.Breakpoint, m store.TranscriptionMetadata) store.Editing {
	previous := ""
	for _, w := range m.Words {
		label := w.SpeakerLabel
		if w.Speaker != nil {
			label = strconv.Itoa(*w.Speaker)
		}
		if label != "" {
			if previous != "" && previous != label {
				b = append(b, store.Breakpoint{ID: uuid.NewString(), Time: w.Start, Kind: "speaker"})
			}
			previous = label
		}
	}
	if previous == "" {
		for _, s := range m.Segments {
			if s.Speaker != nil {
				label := strconv.Itoa(*s.Speaker)
				if previous != "" && previous != label {
					b = append(b, store.Breakpoint{ID: uuid.NewString(), Time: s.Start, Kind: "speaker"})
				}
				previous = label
			}
		}
	}
	sort.SliceStable(b, func(i, j int) bool { return b[i].Time < b[j].Time })
	d := store.Editing{Duration: duration, Breakpoints: []store.Breakpoint{{ID: uuid.NewString(), Time: 0, Kind: "start"}}, Sections: []store.Section{}}
	for _, p := range b {
		if p.Time-d.Breakpoints[len(d.Breakpoints)-1].Time >= 0.05 && duration-p.Time >= 0.05 {
			d.Breakpoints = append(d.Breakpoints, p)
		}
	}
	d.Breakpoints = append(d.Breakpoints, store.Breakpoint{ID: uuid.NewString(), Time: duration, Kind: "end"})
	for i := 1; i < len(d.Breakpoints); i++ {
		d.Sections = append(d.Sections, store.Section{ID: uuid.NewString(), Keep: true})
	}
	return d
}
func keptRanges(s store.EditSnapshot) []AudioRange {
	if s.Skip {
		return []AudioRange{{0, s.Draft.Duration}}
	}
	var ranges []AudioRange
	for i, section := range s.Draft.Sections {
		if section.Keep {
			ranges = append(ranges, AudioRange{s.Draft.Breakpoints[i].Time, s.Draft.Breakpoints[i+1].Time})
		}
	}
	return ranges
}
func MapTranscript(source store.TranscriptionMetadata, ranges []AudioRange) (string, store.TranscriptionMetadata) {
	m := store.TranscriptionMetadata{Language: source.Language}
	var text []string
	offset := 0.0
	for _, r := range ranges {
		firstWord := len(m.Words)
		for _, w := range source.Words {
			mid := (w.Start + w.End) / 2
			if mid >= r.Start && mid < r.End {
				w.Start = offset + math.Max(w.Start, r.Start) - r.Start
				w.End = offset + math.Min(w.End, r.End) - r.Start
				m.Words = append(m.Words, w)
				text = append(text, w.Word)
			}
		}
		if len(source.Words) > 0 {
			for _, w := range m.Words[firstWord:] {
				m.Segments = append(m.Segments, store.TranscriptSegment{Start: w.Start, End: w.End, Text: w.Word, Speaker: w.Speaker})
			}
		}
		for _, s := range source.Segments {
			if len(source.Words) > 0 {
				break
			}
			mid := (s.Start + s.End) / 2
			if mid >= r.Start && mid < r.End {
				s.Start = offset + math.Max(s.Start, r.Start) - r.Start
				s.End = offset + math.Min(s.End, r.End) - r.Start
				m.Segments = append(m.Segments, s)
				if len(source.Words) == 0 {
					text = append(text, s.Text)
				}
			}
		}
		offset += r.End - r.Start
	}
	m.Duration = offset
	return strings.Join(text, " "), m
}
func renderRanges(ctx context.Context, input, output string, ranges []AudioRange) error {
	var filters, labels []string
	for i, r := range ranges {
		label := fmt.Sprintf("a%d", i)
		filters = append(filters, fmt.Sprintf("[0:a]atrim=start=%.9f:end=%.9f,asetpts=PTS-STARTPTS[%s]", r.Start, r.End, label))
		labels = append(labels, "["+label+"]")
	}
	filters = append(filters, fmt.Sprintf("%sconcat=n=%d:v=0:a=1[out]", strings.Join(labels, ""), len(ranges)))
	out, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-y", "-i", input, "-filter_complex", strings.Join(filters, ";"), "-map", "[out]", output).CombinedOutput()
	if err != nil {
		return fmt.Errorf("render audio: %w: %s", err, out)
	}
	return nil
}
func (h *EditingHandler) Run(ctx context.Context, job store.Job, reporter Reporter) (Result, error) {
	dir := filepath.Join(h.uploadsDir, job.SermonID)
	source := filepath.Join(dir, "normalized.flac")
	if job.Type == "prepare_edit" {
		text, m, err := h.store.SourceTranscription(job.SermonID)
		if err != nil {
			return Result{}, err
		}
		duration, err := audioDuration(ctx, source)
		if err != nil {
			return Result{}, err
		}
		out, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-i", source, "-af", "silencedetect=noise=-40dB:d=5", "-f", "null", "-").CombinedOutput()
		if err != nil {
			return Result{}, fmt.Errorf("detect silence: %w", err)
		}
		d := proposedDraft(duration, silenceBreakpoints(string(out), duration), m)
		if err = h.store.PrepareEditing(job.SermonID, d, text, m); err != nil {
			return Result{}, err
		}
		return Result{}, reporter.Progress(100, nil)
	}
	var snap store.EditSnapshot
	if err := json.Unmarshal([]byte(job.Parameters), &snap); err != nil {
		return Result{}, err
	}
	if err := snap.Draft.Validate(); err != nil {
		return Result{}, err
	}
	ranges := keptRanges(snap)
	if len(ranges) == 0 {
		return Result{}, fmt.Errorf("no kept sections")
	}
	// Job-specific immutable outputs keep the previous committed generation playable
	// even across crashes between filesystem publication and the database commit.
	generation := filepath.Join(dir, "edit-"+job.ID)
	if err := os.MkdirAll(generation, 0755); err != nil {
		return Result{}, err
	}
	flac, mp3 := filepath.Join(generation, "edited.flac"), filepath.Join(generation, "edited.mp3")
	flacTemp, mp3Temp := filepath.Join(generation, ".render.flac"), filepath.Join(generation, ".render.mp3")
	defer os.Remove(flacTemp)
	defer os.Remove(mp3Temp)
	marker := CompletionMarker{JobID: job.ID, Parameters: job.Parameters, PipelineVersion: 1}
	markerPath := filepath.Join(generation, ".complete.json")
	artifacts := []Artifact{{TemporaryPath: flacTemp, FinalPath: flac}, {TemporaryPath: mp3Temp, FinalPath: mp3}}
	committed, err := HasCommittedArtifacts(markerPath, marker, artifacts)
	if err != nil {
		return Result{}, err
	}
	if !committed {
		if err := renderRanges(ctx, source, flacTemp, ranges); err != nil {
			return Result{}, err
		}
		out, encodeErr := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-y", "-i", flacTemp, "-codec:a", "libmp3lame", "-q:a", "2", mp3Temp).CombinedOutput()
		if encodeErr != nil {
			return Result{}, fmt.Errorf("encode edited MP3: %w: %s", encodeErr, out)
		}
		if err = CommitArtifacts(markerPath, marker, artifacts); err != nil {
			return Result{}, err
		}
	}
	text, m := MapTranscript(snap.Metadata, ranges)
	if snap.Skip {
		text = snap.Text
		m = snap.Metadata
	}
	if len(snap.Metadata.Words) == 0 && len(snap.Metadata.Segments) == 0 {
		text = snap.Text
	}
	if err = reporter.Progress(100, nil); err != nil {
		return Result{}, err
	}
	if err = h.store.CommitEditing(job.SermonID, filepath.Join("edit-"+job.ID, "edited.mp3"), text, m, !snap.Skip); err != nil {
		return Result{}, err
	}
	return Result{Next: &store.NewJob{ID: uuid.NewString(), Type: "extract_metadata", Stage: "metadata"}}, nil
}

// PreviewAudio generates at most six seconds of source audio plus a short cue.
func PreviewAudio(ctx context.Context, input, mode string, start, end, duration float64) ([]byte, error) {
	if math.IsNaN(start) || math.IsInf(start, 0) || math.IsNaN(end) || math.IsInf(end, 0) || start < 0 || start > duration {
		return nil, fmt.Errorf("invalid preview time")
	}
	var filters []string
	if mode == "breakpoint" {
		filters = []string{fmt.Sprintf("[0:a]atrim=start=%f:end=%f,asetpts=PTS-STARTPTS[a]", math.Max(0, start-1), start), "sine=frequency=1200:duration=0.12, aformat=sample_rates=44100:channel_layouts=mono[b]", fmt.Sprintf("[0:a]atrim=start=%f:end=%f,asetpts=PTS-STARTPTS[c]", start, math.Min(duration, start+1)), "[a][b][c]concat=n=3:v=0:a=1[out]"}
	} else if mode == "section" && end > start && end <= duration {
		if end-start <= 6 {
			filters = []string{fmt.Sprintf("[0:a]atrim=start=%f:end=%f,asetpts=PTS-STARTPTS[out]", start, end)}
		} else {
			filters = []string{fmt.Sprintf("[0:a]atrim=start=%f:end=%f,asetpts=PTS-STARTPTS[a]", start, start+3), "sine=frequency=250:duration=0.2,aformat=sample_rates=44100:channel_layouts=mono[b]", fmt.Sprintf("[0:a]atrim=start=%f:end=%f,asetpts=PTS-STARTPTS[c]", end-3, end), "[a][b][c]concat=n=3:v=0:a=1[out]"}
		}
	} else {
		return nil, fmt.Errorf("invalid preview mode or range")
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", input, "-filter_complex", strings.Join(filters, ";"), "-map", "[out]", "-f", "wav", "pipe:1")
	return cmd.Output()
}

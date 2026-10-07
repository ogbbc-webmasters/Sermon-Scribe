package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
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

func keptRanges(s store.EditSnapshot) []AudioRange {
	if s.Skip {
		return []AudioRange{{0, s.Draft.Duration}}
	}
	var ranges []AudioRange
	for i, section := range s.Draft.Sections {
		if section.Keep {
			r := AudioRange{s.Draft.Breakpoints[i].Time, s.Draft.Breakpoints[i+1].Time}
			if len(ranges) > 0 && ranges[len(ranges)-1].End == r.Start {
				ranges[len(ranges)-1].End = r.End
			} else {
				ranges = append(ranges, r)
			}
		}
	}
	return ranges
}
func MapTranscript(source store.TranscriptionMetadata, ranges []AudioRange) (string, store.TranscriptionMetadata) {
	m := store.TranscriptionMetadata{Language: source.Language}
	var text []string
	offset := 0.0
	for _, r := range ranges {
		firstSegment := len(m.Segments)
		sourceSegment, previousSegment := 0, -1
		previousSpeaker := ""
		for _, w := range source.Words {
			mid := (w.Start + w.End) / 2
			if mid >= r.Start && mid < r.End {
				for sourceSegment+1 < len(source.Segments) && mid >= source.Segments[sourceSegment].End {
					sourceSegment++
				}
				speaker := w.SpeakerLabel
				if w.Speaker != nil {
					speaker = strconv.Itoa(*w.Speaker)
				}
				w.Start = offset + math.Max(w.Start, r.Start) - r.Start
				w.End = offset + math.Min(w.End, r.End) - r.Start
				m.Words = append(m.Words, w)
				text = append(text, w.Word)
				// Keep phrases readable instead of turning the transcript into one
				// displayed paragraph per word. Never join across a deleted range.
				if len(m.Segments) == firstSegment || sourceSegment != previousSegment || speaker != previousSpeaker || w.Start-m.Segments[len(m.Segments)-1].End > 2 {
					m.Segments = append(m.Segments, store.TranscriptSegment{Start: w.Start, End: w.End, Text: w.Word, Speaker: w.Speaker})
				} else {
					segment := &m.Segments[len(m.Segments)-1]
					segment.End = w.End
					segment.Text += " " + w.Word
				}
				previousSegment, previousSpeaker = sourceSegment, speaker
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
	// Sequential FLAC pieces avoid buffering all later sections in a concat
	// filter while the first section of a long recording is still playing.
	dir, err := os.MkdirTemp(filepath.Dir(output), ".pieces-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	var manifest strings.Builder
	for i, r := range ranges {
		name := fmt.Sprintf("piece-%d.flac", i)
		out, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-y", "-ss", fmt.Sprintf("%.9f", r.Start), "-i", input, "-t", fmt.Sprintf("%.9f", r.End-r.Start), "-vn", "-c:a", "flac", filepath.Join(dir, name)).CombinedOutput()
		if err != nil {
			return fmt.Errorf("cut section: %w: %s", err, out)
		}
		fmt.Fprintf(&manifest, "file '%s'\n", name)
	}
	list := filepath.Join(dir, "pieces.txt")
	if err := os.WriteFile(list, []byte(manifest.String()), 0600); err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-y", "-f", "concat", "-safe", "0", "-i", list, "-vn", "-c:a", "flac", output).CombinedOutput()
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
		d := store.Editing{
			Duration: duration,
			Breakpoints: []store.Breakpoint{
				{ID: uuid.NewString(), Time: 0, Kind: "start"},
				{ID: uuid.NewString(), Time: duration, Kind: "end"},
			},
			Sections: []store.Section{{ID: uuid.NewString(), Keep: true}},
		}
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
	if snap.Skip {
		// Continuing without edits needs no full-recording render.
		if err := h.store.CommitEditing(job.SermonID, "", snap.Text, snap.Metadata, false); err != nil {
			return Result{}, err
		}
		return Result{Next: &store.NewJob{ID: uuid.NewString(), Type: "extract_metadata", Stage: "metadata"}}, nil
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
		out, encodeErr := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-y", "-i", flacTemp, "-vn", "-codec:a", "libmp3lame", "-q:a", "2", mp3Temp).CombinedOutput()
		if encodeErr != nil {
			return Result{}, fmt.Errorf("encode edited MP3: %w: %s", encodeErr, out)
		}
		if err = CommitArtifacts(markerPath, marker, artifacts); err != nil {
			return Result{}, err
		}
	}
	text, m := MapTranscript(snap.Metadata, ranges)
	if len(ranges) == 1 && ranges[0].Start == 0 && ranges[0].End == snap.Draft.Duration {
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
	args := []string{"-v", "error"}
	if mode == "breakpoint" {
		clipStart := math.Max(0, start-1)
		clipEnd := math.Min(duration, start+1)
		edge := start - clipStart
		args = append(args, "-ss", fmt.Sprintf("%.9f", clipStart), "-t", fmt.Sprintf("%.9f", clipEnd-clipStart), "-i", input)
		filters = []string{fmt.Sprintf("[0:a]atrim=start=0:end=%.9f,asetpts=PTS-STARTPTS[a]", edge), "sine=frequency=1200:duration=0.12,aformat=sample_rates=44100:channel_layouts=mono[b]", fmt.Sprintf("[0:a]atrim=start=%.9f,asetpts=PTS-STARTPTS[c]", edge), "[a][b][c]concat=n=3:v=0:a=1[out]"}
	} else if mode == "section" && end > start && end <= duration {
		if end-start <= 6 {
			args = append(args, "-ss", fmt.Sprintf("%.9f", start), "-t", fmt.Sprintf("%.9f", end-start), "-i", input)
			filters = []string{"[0:a]asetpts=PTS-STARTPTS[out]"}
		} else {
			args = append(args, "-ss", fmt.Sprintf("%.9f", start), "-t", "3", "-i", input, "-ss", fmt.Sprintf("%.9f", end-3), "-t", "3", "-i", input)
			filters = []string{"[0:a]asetpts=PTS-STARTPTS[a]", "sine=frequency=250:duration=0.2,aformat=sample_rates=44100:channel_layouts=mono[b]", "[1:a]asetpts=PTS-STARTPTS[c]", "[a][b][c]concat=n=3:v=0:a=1[out]"}
		}
	} else {
		return nil, fmt.Errorf("invalid preview mode or range")
	}
	args = append(args, "-vn", "-filter_complex", strings.Join(filters, ";"), "-map", "[out]", "-f", "wav", "pipe:1")
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	return cmd.Output()
}

package processing

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
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

type AudioRange = store.AudioRange

const breakpointPreviewSeconds = 3

// SpeakerDraft uses diarized segment starts and marks long pauses at the end of
// the preceding segment. Edited boundaries take priority when preserving edits.
func SpeakerDraft(d store.Editing, metadata store.TranscriptionMetadata, preserveEdited bool) store.Editing {
	segments := metadata.Segments
	var starts []float64
	var speaking []store.TranscriptSegment
	for _, segment := range segments {
		if segment.Speaker != nil {
			starts = append(starts, segment.Start)
			if !math.IsNaN(segment.Start) && !math.IsInf(segment.Start, 0) && !math.IsNaN(segment.End) && !math.IsInf(segment.End, 0) && segment.Start >= 0 && segment.End > segment.Start && segment.End <= d.Duration {
				speaking = append(speaking, segment)
			}
		}
	}
	sort.Float64s(starts)
	sort.SliceStable(speaking, func(i, j int) bool { return speaking[i].Start < speaking[j].Start })
	type candidate struct {
		time float64
		kind string
	}
	boundaries := make([]candidate, 0, len(starts)*2)
	for _, start := range starts {
		boundaries = append(boundaries, candidate{time: start, kind: "speaker"})
	}
	for i := 0; i+1 < len(speaking); i++ {
		segment, next := speaking[i], speaking[i+1]
		if next.Start-segment.End > 3 {
			boundaries = append(boundaries, candidate{time: segment.End, kind: "silence_start"})
		}
	}
	sort.SliceStable(boundaries, func(i, j int) bool { return boundaries[i].time < boundaries[j].time })
	old := d
	d.Breakpoints = []store.Breakpoint{old.Breakpoints[0], old.Breakpoints[len(old.Breakpoints)-1]}
	if preserveEdited {
		for _, b := range old.Breakpoints[1 : len(old.Breakpoints)-1] {
			// Legacy drafts did not record edits or original generated times.
			// Match their moved speaker boundaries to the closest source start.
			if b.Kind == "speaker" && b.SourceTime == nil && len(starts) > 0 {
				nearest := starts[0]
				for _, start := range starts {
					if math.Abs(start-b.Time) < math.Abs(nearest-b.Time) {
						nearest = start
					}
				}
				b.SourceTime = &nearest
				b.Edited = b.Edited || math.Abs(nearest-b.Time) > 0.001
			}
			if b.Edited || b.Kind == "manual" {
				d.Breakpoints = append(d.Breakpoints, b)
			}
		}
	}
	for _, candidate := range boundaries {
		allowed := candidate.time >= 0.05-1e-9 && d.Duration-candidate.time >= 0.05-1e-9
		for _, b := range d.Breakpoints {
			if math.Abs(candidate.time-b.Time) < 0.05-1e-9 || (b.SourceTime != nil && math.Abs(candidate.time-*b.SourceTime) < 1e-9) {
				allowed = false
				break
			}
		}
		if allowed {
			sourceTime := candidate.time
			b := store.Breakpoint{ID: uuid.NewString(), Time: candidate.time, Kind: candidate.kind, SourceTime: &sourceTime}
			if preserveEdited {
				for _, previous := range old.Breakpoints {
					if previous.Kind == candidate.kind && previous.Time == candidate.time && !previous.Edited {
						b.ID = previous.ID
						break
					}
				}
			}
			d.Breakpoints = append(d.Breakpoints, b)
		}
	}
	sort.Slice(d.Breakpoints, func(i, j int) bool { return d.Breakpoints[i].Time < d.Breakpoints[j].Time })
	d.Sections = make([]store.Section, len(d.Breakpoints)-1)
	for i := range d.Sections {
		startsInSilence := d.Breakpoints[i].Kind == "silence_start"
		start, end := d.Breakpoints[i].Time, d.Breakpoints[i+1].Time
		d.Sections[i] = store.Section{ID: uuid.NewString(), Keep: !preserveEdited && !startsInSilence && sectionHasTranscript(metadata, start, end)}
		if preserveEdited {
			for j, section := range old.Sections {
				left, right := old.Breakpoints[j].Time, old.Breakpoints[j+1].Time
				if start == left && end == right {
					d.Sections[i].ID = section.ID
					d.Sections[i].Keep = section.Keep
				}
				// Silence intervals default to deleted; other regenerated
				// intervals retain kept audio from overlapping source sections.
				if !startsInSilence && end > left && start < right && section.Keep {
					d.Sections[i].Keep = true
				}
			}
		}
	}
	return d
}

func sectionHasTranscript(metadata store.TranscriptionMetadata, start, end float64) bool {
	if len(metadata.Words) > 0 {
		for _, word := range metadata.Words {
			mid := (word.Start + word.End) / 2
			if mid >= start && mid < end && strings.TrimSpace(word.Word) != "" {
				return true
			}
		}
		return false
	}
	for _, segment := range metadata.Segments {
		mid := (segment.Start + segment.End) / 2
		if mid >= start && mid < end && strings.TrimSpace(segment.Text) != "" {
			return true
		}
	}
	return false
}

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
		d = SpeakerDraft(d, m, false)
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
		if err := h.store.CommitEditing(job.SermonID, "", snap.Draft, false); err != nil {
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
		out, encodeErr := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-y", "-i", flacTemp, "-vn", "-ac", "1", "-ar", "44100", "-codec:a", "libmp3lame", "-b:a", "32k", mp3Temp).CombinedOutput()
		if encodeErr != nil {
			return Result{}, fmt.Errorf("encode edited MP3: %w: %s", encodeErr, out)
		}
		if err = CommitArtifacts(markerPath, marker, artifacts); err != nil {
			return Result{}, err
		}
	}
	if err = reporter.Progress(100, nil); err != nil {
		return Result{}, err
	}
	if err = h.store.CommitEditing(job.SermonID, filepath.Join("edit-"+job.ID, "edited.mp3"), snap.Draft, !snap.Skip); err != nil {
		return Result{}, err
	}
	return Result{Next: &store.NewJob{ID: uuid.NewString(), Type: "extract_metadata", Stage: "metadata"}}, nil
}

// WaveformPeaks produces 20 ms min/max pairs without
// retaining the decoded recording in memory.
func WaveformPeaks(ctx context.Context, input string, duration float64) ([]int16, error) {
	if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return nil, fmt.Errorf("invalid waveform duration")
	}
	const rate = 8000
	const hiresRate = 50
	const samplesPerHiresPair = rate / hiresRate
	var hires []int16
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", input, "-vn", "-ac", "1", "-ar", "8000", "-f", "s16le", "pipe:1")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	var sample int64
	var data [16000]byte
	for {
		n, readErr := io.ReadFull(pipe, data[:])
		for i := 0; i+1 < n; i += 2 {
			value := int16(binary.LittleEndian.Uint16(data[i : i+2]))
			pair := int(sample/samplesPerHiresPair) * 2
			if pair == len(hires) {
				hires = append(hires, value, value)
			} else {
				hires[pair] = min(hires[pair], value)
				hires[pair+1] = max(hires[pair+1], value)
			}
			sample++
		}
		if readErr != nil {
			if readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return nil, readErr
			}
			break
		}
	}
	if err = cmd.Wait(); err != nil {
		return nil, err
	}
	return hires, nil
}

// PreviewAudio generates at most six seconds of source audio plus a short cue.
func PreviewAudio(ctx context.Context, input, mode string, start, end, duration float64) ([]byte, error) {
	if math.IsNaN(start) || math.IsInf(start, 0) || math.IsNaN(end) || math.IsInf(end, 0) || start < 0 || start > duration {
		return nil, fmt.Errorf("invalid preview time")
	}
	var filters []string
	args := []string{"-v", "error"}
	if mode == "breakpoint" {
		clipStart := math.Max(0, start-breakpointPreviewSeconds)
		clipEnd := math.Min(duration, start+breakpointPreviewSeconds)
		edge := start - clipStart
		args = append(args, "-ss", fmt.Sprintf("%.9f", clipStart), "-t", fmt.Sprintf("%.9f", clipEnd-clipStart), "-i", input)
		filters = []string{fmt.Sprintf("[0:a]atrim=start=0:end=%.9f,asetpts=PTS-STARTPTS[a]", edge), "sine=frequency=1200:duration=0.5,aformat=sample_rates=44100:channel_layouts=mono,adelay=250:all=1,apad=pad_dur=0.25[b]", fmt.Sprintf("[0:a]atrim=start=%.9f,asetpts=PTS-STARTPTS[c]", edge), "[a][b][c]concat=n=3:v=0:a=1[out]"}
	} else if mode == "section" && end > start && end <= duration {
		if end-start <= 6 {
			args = append(args, "-ss", fmt.Sprintf("%.9f", start), "-t", fmt.Sprintf("%.9f", end-start), "-i", input)
			filters = []string{"[0:a]asetpts=PTS-STARTPTS[out]"}
		} else {
			args = append(args, "-ss", fmt.Sprintf("%.9f", start), "-t", "3", "-i", input, "-ss", fmt.Sprintf("%.9f", end-3), "-t", "3", "-i", input)
			filters = []string{"[0:a]asetpts=PTS-STARTPTS[a]", "sine=frequency=250:duration=1,aformat=sample_rates=44100:channel_layouts=mono[b]", "[1:a]asetpts=PTS-STARTPTS[c]", "[a][b][c]concat=n=3:v=0:a=1[out]"}
		}
	} else {
		return nil, fmt.Errorf("invalid preview mode or range")
	}
	args = append(args, "-vn", "-filter_complex", strings.Join(filters, ";"), "-map", "[out]", "-f", "wav", "pipe:1")
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	return cmd.Output()
}

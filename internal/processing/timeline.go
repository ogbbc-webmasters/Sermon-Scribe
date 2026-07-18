package processing

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
)

const WaveformSamplesPerSecond = 20

type Waveform struct {
	Duration         float64   `json:"duration"`
	SamplesPerSecond int       `json:"samples_per_second"`
	Samples          []float64 `json:"samples"`
}

type Region struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Type  string  `json:"type"`
	Keep  bool    `json:"keep"`
}

func GenerateWaveform(ctx context.Context, input string) (Waveform, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-nostdin", "-i", input,
		"-map", "0:a:0", "-ac", "1", "-ar", "44100", "-f", "f32le", "pipe:1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return Waveform{}, err
	}
	if err := cmd.Start(); err != nil {
		return Waveform{}, fmt.Errorf("start waveform ffmpeg: %w", err)
	}
	w := Waveform{SamplesPerSecond: WaveformSamplesPerSecond, Samples: []float64{}}
	const perBucket = 44100 / WaveformSamplesPerSecond
	var raw [4]byte
	count := 0
	peak := float64(0)
	total := 0
	for {
		_, err := io.ReadFull(out, raw[:])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = cmd.Wait()
			return Waveform{}, fmt.Errorf("read waveform samples: %w", err)
		}
		v := math.Abs(float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[:]))))
		if v > peak {
			peak = v
		}
		count++
		total++
		if count == perBucket {
			w.Samples = append(w.Samples, math.Min(1, peak))
			count = 0
			peak = 0
		}
	}
	if count > 0 {
		w.Samples = append(w.Samples, math.Min(1, peak))
	}
	if err := cmd.Wait(); err != nil {
		return Waveform{}, fmt.Errorf("waveform ffmpeg: %w", err)
	}
	w.Duration = float64(total) / 44100
	return w, nil
}

func ValidateWaveform(w Waveform) error {
	if w.SamplesPerSecond != WaveformSamplesPerSecond || !finite(w.Duration) || w.Duration <= 0 || len(w.Samples) == 0 {
		return errors.New("invalid waveform metadata")
	}
	if math.Abs(float64(len(w.Samples))/float64(w.SamplesPerSecond)-w.Duration) > 1.0/float64(w.SamplesPerSecond)+1e-6 {
		return errors.New("waveform sample count does not match duration")
	}
	for _, v := range w.Samples {
		if !finite(v) || v < 0 || v > 1 {
			return errors.New("invalid waveform sample")
		}
	}
	return nil
}

// AnalyzeWaveform classifies deterministic one-second windows and merges them.
func AnalyzeWaveform(w Waveform) ([]Region, error) {
	if err := ValidateWaveform(w); err != nil {
		return nil, err
	}
	regions := []Region{}
	for i := 0; i < len(w.Samples); i += w.SamplesPerSecond {
		end := min(i+w.SamplesPerSecond, len(w.Samples))
		values := w.Samples[i:end]
		mean := 0.0
		gaps := 0
		for _, v := range values {
			mean += v
			if v < 0.025 {
				gaps++
			}
		}
		mean /= float64(len(values))
		variance := 0.0
		for _, v := range values {
			d := v - mean
			variance += d * d
		}
		variance /= float64(len(values))
		typ := "speaking"
		keep := true
		if mean < 0.035 {
			typ = "silence"
			keep = false
		} else if variance < 0.0025 && float64(gaps)/float64(len(values)) < .1 {
			typ = "singing"
			keep = false
		}
		start := float64(i) / float64(w.SamplesPerSecond)
		stop := math.Min(w.Duration, float64(end)/float64(w.SamplesPerSecond))
		if len(regions) > 0 && regions[len(regions)-1].Type == typ {
			regions[len(regions)-1].End = stop
		} else {
			regions = append(regions, Region{Start: start, End: stop, Type: typ, Keep: keep})
		}
	}
	// Preserve exactly one second across internal deleted silence (half at each edge).
	withGaps := make([]Region, 0, len(regions)+4)
	for i, region := range regions {
		if i > 0 && i < len(regions)-1 && region.Type == "silence" {
			length := region.End - region.Start
			if length <= 1 {
				region.Keep = true
			} else {
				withGaps = append(withGaps, Region{Start: region.Start, End: region.Start + .5, Type: "silence", Keep: true})
				withGaps = append(withGaps, Region{Start: region.Start + .5, End: region.End - .5, Type: "silence", Keep: false})
				withGaps = append(withGaps, Region{Start: region.End - .5, End: region.End, Type: "silence", Keep: true})
				continue
			}
		}
		withGaps = append(withGaps, region)
	}
	regions = withGaps
	return regions, validateRegions(regions, w.Duration, false)
}

func ValidateRegions(regions []Region, duration float64) error {
	return validateRegions(regions, duration, true)
}

func validateRegions(regions []Region, duration float64, requireKept bool) error {
	if !finite(duration) || duration <= 0 || len(regions) == 0 {
		return errors.New("regions must cover a positive duration")
	}
	position := 0.0
	kept := false
	for i, r := range regions {
		if !finite(r.Start) || !finite(r.End) || r.Start < 0 || r.End <= r.Start || r.End > duration+1e-6 {
			return fmt.Errorf("region %d has invalid bounds", i)
		}
		if math.Abs(r.Start-position) > 1e-6 {
			return fmt.Errorf("region %d is not contiguous", i)
		}
		if r.Type != "silence" && r.Type != "speaking" && r.Type != "singing" {
			return fmt.Errorf("region %d has invalid type", i)
		}
		position = r.End
		kept = kept || r.Keep
	}
	if math.Abs(position-duration) > 1e-6 {
		return errors.New("regions do not cover waveform duration")
	}
	if requireKept && !kept {
		return errors.New("at least one region must be kept")
	}
	return nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func EncodeWaveform(w Waveform) ([]byte, error) {
	if err := ValidateWaveform(w); err != nil {
		return nil, err
	}
	return json.Marshal(w)
}

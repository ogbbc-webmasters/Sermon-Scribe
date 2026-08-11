package processing

import "testing"

func TestAnalyzeWaveformClassificationAndInternalGap(t *testing.T) {
	samples := make([]float64, 120)
	for i := 20; i < 40; i++ {
		samples[i] = .1 + float64(i%2)*.2
	}
	for i := 80; i < 100; i++ {
		samples[i] = .2
	}
	w := Waveform{Duration: 6, SamplesPerSecond: 20, Samples: samples}
	regions, err := AnalyzeWaveform(w)
	if err != nil {
		t.Fatal(err)
	}
	if regions[0].Type != "silence" || regions[0].Keep {
		t.Fatalf("leading region = %+v", regions[0])
	}
	if len(regions) != 5 {
		t.Fatalf("regions = %+v", regions)
	}
	internal := regions[2]
	if internal.Type != "silence" || internal.Keep || internal.Start != 2.5 || internal.End != 3.5 {
		t.Fatalf("regions = %+v", regions)
	}
	if regions[1].End != 2.5 || regions[3].Start != 3.5 || regions[3].Type != "singing" {
		t.Fatalf("one-second gap was not preserved: %+v", regions)
	}
	for i := 1; i < len(regions); i++ {
		if regions[i-1].Type == "silence" && regions[i].Type == "silence" {
			t.Fatalf("adjacent silence regions: %+v", regions)
		}
	}
}

func TestValidateRegions(t *testing.T) {
	valid := []Region{{Start: 0, End: 1, Type: "speaking", Keep: true}, {Start: 1, End: 2, Type: "silence"}}
	if err := ValidateRegions(valid, 2); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]Region{
		{{Start: 0, End: 1, Type: "speaking", Keep: false}},
		{{Start: 0, End: 1, Type: "invalid", Keep: true}},
		{{Start: 0, End: .9, Type: "speaking", Keep: true}, {Start: 1, End: 2, Type: "silence"}},
	} {
		if err := ValidateRegions(bad, 2); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

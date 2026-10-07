package store

import (
	"reflect"
	"testing"
)

func TestSaveSourceTranscriptionPersistsStructuredMetadata(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.CreateSermon(Sermon{
		ID: "structured-transcript", OriginalFilename: "sermon.mp3",
		UploadedAt: "2026-10-07T00:00:00Z", Stage: "transcription", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	speaker := 1
	confidence := 0.97
	want := TranscriptionMetadata{
		Language: "en", Duration: 8.5,
		Segments: []TranscriptSegment{{Start: 1.25, End: 4.5, Text: "Test phrase.", Speaker: &speaker}},
		Words:    []TranscriptWord{{Word: "Test", Start: 1.25, End: 1.7, Speaker: &speaker, SpeakerLabel: "Speaker 2", Confidence: &confidence}},
	}
	if err := st.SaveSourceTranscription("structured-transcript", "Test phrase.", want); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetSermon("structured-transcript")
	if err != nil {
		t.Fatal(err)
	}
	if got.Transcript == nil || *got.Transcript != "Test phrase." {
		t.Fatalf("transcript = %v", got.Transcript)
	}
	if got.TranscriptionMetadata == nil || !reflect.DeepEqual(*got.TranscriptionMetadata, want) {
		t.Fatalf("transcription metadata = %#v, want %#v", got.TranscriptionMetadata, want)
	}
}

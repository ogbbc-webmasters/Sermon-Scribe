package processing

import (
	"reflect"
	"testing"
)

func TestNormalizeScriptures(t *testing.T) {
	for _, tt := range []struct {
		name        string
		input, want []string
	}{
		{"chapter versus verses", []string{"Jeremiah 2", "James 1:5", "Jeremiah 2:1-37", "Jeremiah 2:8"}, []string{"Jeremiah 2:1-37", "James 1:5"}},
		{"chapter after verses", []string{"James 1:5-8", "James 1", "James 1:5"}, []string{"James 1:5-8"}},
		{"contiguous repeated chapters", []string{"Revelation 2:1, 2:2, 2:3"}, []string{"Revelation 2:1-3"}},
		{"overlap and bridge", []string{"Acts 6:1-2", "John 3:16", "Acts 6:5-7", "Acts 6:2-5"}, []string{"Acts 6:1-7", "John 3:16"}},
		{"disjoint preserve first mentions", []string{"Acts 6:5-7", "John 3:16", "Acts 6:1-2"}, []string{"Acts 6:5-7", "John 3:16", "Acts 6:1-2"}},
		{"one missing verse is not contiguous", []string{"James 1:5", "James 1:7"}, []string{"James 1:5", "James 1:7"}},
		{"contiguous separate references", []string{"James 1:6", "John 3:16", "James 1:5"}, []string{"James 1:5-6", "John 3:16"}},
		{"formatting and numbered books", []string{"  1corinthians 01:18–25  ", "1 Corinthians 1:18-25", "PSALM 119:105", "psalm 119:105", " "}, []string{"1 Corinthians 1:18-25", "Psalm 119:105"}},
		{"multiple verses", []string{"Proverbs 3:5,8-9,6"}, []string{"Proverbs 3:5-6", "Proverbs 3:8-9"}},
		{"comma order is not lexical or numeric", []string{"Acts 6:10,2"}, []string{"Acts 6:10", "Acts 6:2"}},
		{"different books and chapters", []string{"1 John 1", "John 1", "John 2:1", "2 John 1"}, []string{"1 John 1", "John 1", "John 2:1", "2 John 1"}},
		{"unsupported references retained", []string{"John 3:16-4:2", "James 1:5,2:1", "James 1:8-5", "James 0:1", "unknown", "unknown"}, []string{"John 3:16-4:2", "James 1:5,2:1", "James 1:8-5", "James 0:1", "unknown"}},
		{"empty", nil, []string{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeScriptures(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("normalizeScriptures(%v) = %v, want %v", tt.input, got, tt.want)
			}
			if again := normalizeScriptures(got); !reflect.DeepEqual(again, got) {
				t.Fatalf("normalization not idempotent: %v -> %v", got, again)
			}
		})
	}
}

func TestValidateScriptureVerseReferences(t *testing.T) {
	for _, tt := range []struct {
		name       string
		references []string
		wantError  bool
	}{
		{name: "single verse", references: []string{"Romans 8:28"}},
		{name: "full chapter range", references: []string{"Romans 8:1-39"}},
		{name: "chapter only", references: []string{"Romans 8"}, wantError: true},
		{name: "chapter only is not hidden by a verse citation", references: []string{"Romans 8", "Romans 8:28"}, wantError: true},
		{name: "empty", references: []string{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateScriptureVerseReferences(tt.references)
			if (err != nil) != tt.wantError {
				t.Fatalf("validateScriptureVerseReferences(%v) error = %v, wantError %v", tt.references, err, tt.wantError)
			}
		})
	}
}

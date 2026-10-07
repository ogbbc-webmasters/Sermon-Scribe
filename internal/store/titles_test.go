package store

import "testing"

func TestTitleCase(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{"receiving wisdom", "Receiving Wisdom"},
		{"the gift of wisdom", "The Gift of Wisdom"},
		{"what James has to teach us", "What James Has to Teach Us"},
		{"faith to live by", "Faith to Live By"},
		{"wisdom: the gift of God", "Wisdom: The Gift of God"},
		{"hope in the LORD", "Hope in the LORD"},
		{"God’s grace and god-given wisdom", "God’s Grace and God-Given Wisdom"},
		{"  “éternal wisdom”\tand faith  ", "  “Éternal Wisdom”\tand Faith  "},
		{"", ""},
	} {
		t.Run(tt.input, func(t *testing.T) {
			if got := titleCase(tt.input); got != tt.want {
				t.Fatalf("titleCase(%q) = %q, want %q", tt.input, got, tt.want)
			}
			if got := titleCase(tt.want); got != tt.want {
				t.Fatalf("title case is not idempotent: %q", got)
			}
		})
	}
}

func TestTitleCaseAtStorageBoundary(t *testing.T) {
	st := openTestStore(t)
	if err := st.CreateSermon(Sermon{ID: "title", OriginalFilename: "source.mp3", UploadedAt: "2026-10-06T00:00:00Z", Stage: "metadata", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveMetadata("title", "receiving wisdom", true, "Reason", "Speaker", nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	var persisted string
	if err := st.db.QueryRow(`SELECT title FROM sermons WHERE id='title'`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != "Receiving Wisdom" {
		t.Fatalf("metadata persisted title = %q", persisted)
	}
	if err := st.SaveTitle("title", "the gift of wisdom", false, "New reason"); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT title FROM sermons WHERE id='title'`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != "The Gift of Wisdom" {
		t.Fatalf("title retry persisted title = %q", persisted)
	}
	// Existing records need no regeneration or database backfill to display
	// consistently. Reads leave their stored value untouched.
	if _, err := st.db.Exec(`UPDATE sermons SET title='receiving wisdom' WHERE id='title'`); err != nil {
		t.Fatal(err)
	}
	for _, list := range []bool{false, true} {
		var sm Sermon
		if list {
			sermons, err := st.ListSermons()
			if err != nil {
				t.Fatal(err)
			}
			sm = sermons[0]
		} else {
			var err error
			sm, err = st.GetSermon("title")
			if err != nil {
				t.Fatal(err)
			}
		}
		if sm.Title == nil || *sm.Title != "Receiving Wisdom" || *sm.TitleReasoning != "New reason" || *sm.TitleGenerated {
			t.Fatalf("incorrect title metadata: %+v", sm)
		}
	}
	if err := st.db.QueryRow(`SELECT title FROM sermons WHERE id='title'`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != "receiving wisdom" {
		t.Fatal("reading legacy title changed stored data")
	}
}

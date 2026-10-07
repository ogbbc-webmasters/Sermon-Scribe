package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

func TestSaveScriptureSelection(t *testing.T) {
	srv, _ := newTestServer(t)
	if err := srv.Store.CreateSermon(store.Sermon{ID: "selection", OriginalFilename: "source.mp3", UploadedAt: "2026-10-06T00:00:00Z", Stage: "metadata", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	options := []string{"Genesis 1:1-31", "John 1:1-18", "James 1:5"}
	if err := srv.Store.SaveMetadataWithScriptureOptions("selection", "Title", true, "Reason", "Speaker", options[0], options[1], options[:2], options, nil, nil); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPut, "/api/sermons/selection/scriptures", strings.NewReader(`{"scriptures":["John 1:1-18","James 1:5"]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	srv.Routes(nil).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var sermon store.Sermon
	if err := json.NewDecoder(response.Body).Decode(&sermon); err != nil {
		t.Fatal(err)
	}
	if sermon.OldTestamentReading != options[0] || sermon.NewTestamentReading != options[1] || !reflect.DeepEqual(sermon.Scriptures, []string{"John 1:1-18", "James 1:5"}) || !reflect.DeepEqual(sermon.ScriptureOptions, options) {
		t.Fatalf("unexpected saved references: OT=%q NT=%q selected=%v options=%v", sermon.OldTestamentReading, sermon.NewTestamentReading, sermon.Scriptures, sermon.ScriptureOptions)
	}

	request = httptest.NewRequest(http.MethodPut, "/api/sermons/selection/scriptures", strings.NewReader(`{"scriptures":["Jude 1:1"]}`))
	response = httptest.NewRecorder()
	srv.Routes(nil).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown option status = %d, want 400", response.Code)
	}
	sermon, err := srv.Store.GetSermon("selection")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sermon.Scriptures, []string{"John 1:1-18", "James 1:5"}) {
		t.Fatalf("invalid selection changed persisted references: %v", sermon.Scriptures)
	}
}

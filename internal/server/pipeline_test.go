package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/processing"
	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

type countingNotifier struct {
	calls atomic.Int32
}

func (n *countingNotifier) Notify() {
	n.calls.Add(1)
}

func TestRetryFailedSermon(t *testing.T) {
	srv, ts := newTestServer(t)
	notifier := &countingNotifier{}
	srv.Queue = notifier

	_, uploaded := uploadFile(t, ts, "retry.wav", []byte("audio"), nil)
	now := time.Now()
	job, err := srv.Store.ClaimNextJob(context.Background(), []string{"normalize"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.FailJob(job, "decoder failed", now); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(ts.URL+"/api/sermons/"+uploaded.ID+"/retry", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 202: %s", resp.StatusCode, body)
	}
	var sm store.Sermon
	if err := json.NewDecoder(resp.Body).Decode(&sm); err != nil {
		t.Fatal(err)
	}
	if sm.Status != "pending" || sm.Error != nil || sm.Progress != 0 {
		t.Fatalf("retried sermon = %+v", sm)
	}
	job, err = srv.Store.GetCurrentJob(uploaded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != "queued" || job.Attempts != 0 {
		t.Fatalf("retried job = %+v", job)
	}
	// One notification follows upload and another follows manual retry.
	if notifier.calls.Load() != 2 {
		t.Fatalf("queue notifications = %d, want 2", notifier.calls.Load())
	}
}

func TestRetryRejectsNonfailedAndMissingSermons(t *testing.T) {
	_, ts := newTestServer(t)
	_, uploaded := uploadFile(t, ts, "pending.wav", []byte("audio"), nil)

	resp, err := http.Post(ts.URL+"/api/sermons/"+uploaded.ID+"/retry", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("pending retry status = %d, want 409", resp.StatusCode)
	}

	resp, err = http.Post(ts.URL+"/api/sermons/missing/retry", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing retry status = %d, want 404", resp.StatusCode)
	}
}

func TestSSEStartsWithSnapshotThenStreamsEvents(t *testing.T) {
	srv, ts := newTestServer(t)
	_, uploaded := uploadFile(t, ts, "live.wav", []byte("audio"), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q", got)
	}

	reader := bufio.NewReader(resp.Body)
	first := readSSEBlock(t, reader)
	if !strings.Contains(first, "event: snapshot") || !strings.Contains(first, uploaded.ID) {
		t.Fatalf("first SSE block is not snapshot: %q", first)
	}

	updated := uploaded
	updated.Status = "running"
	updated.Progress = 35
	srv.Events.Publish(processing.Event{Name: processing.EventProgress, Sermon: updated})
	second := readSSEBlock(t, reader)
	if !strings.Contains(second, "event: progress") || !strings.Contains(second, `"progress":35`) {
		t.Fatalf("second SSE block is not progress: %q", second)
	}
}

func TestEventHubDisconnectsSlowSubscriber(t *testing.T) {
	hub := NewEventHub()
	events, unsubscribe := hub.subscribe()
	defer unsubscribe()
	for i := 0; i <= eventBufferSize; i++ {
		hub.Publish(processing.Event{Name: processing.EventProgress})
	}
	for range eventBufferSize {
		if _, ok := <-events; !ok {
			t.Fatal("subscriber closed before buffered events were readable")
		}
	}
	if _, ok := <-events; ok {
		t.Fatal("slow subscriber was not disconnected")
	}
}

func readSSEBlock(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	var block strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("read SSE: %v", err)
			}
			t.Fatalf("SSE ended before blank line: %q", block.String())
		}
		if line == "\n" {
			return block.String()
		}
		block.WriteString(line)
	}
}

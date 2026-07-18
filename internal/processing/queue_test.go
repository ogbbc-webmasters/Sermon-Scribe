package processing

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

func processingTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func enqueueTestJob(t *testing.T, st *store.Store, sermonID, jobID string, now time.Time) {
	t.Helper()
	if err := st.CreateSermon(store.Sermon{
		ID: sermonID, OriginalFilename: sermonID + ".wav",
		UploadedAt: now.UTC().Format(time.RFC3339Nano),
		Stage:      "normalization", Status: "pending",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.EnqueueJob(store.NewJob{
		ID: jobID, SermonID: sermonID, Type: "test", Stage: "normalization",
	}, now); err != nil {
		t.Fatal(err)
	}
}

func waitForSermon(t *testing.T, st *store.Store, id string, predicate func(store.Sermon) bool) store.Sermon {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sm, err := st.GetSermon(id)
		if err == nil && predicate(sm) {
			return sm
		}
		time.Sleep(5 * time.Millisecond)
	}
	sm, err := st.GetSermon(id)
	t.Fatalf("timed out waiting for sermon %s: %+v, %v", id, sm, err)
	return store.Sermon{}
}

type collectingSink struct {
	events chan Event
}

func (s collectingSink) Publish(event Event) {
	s.events <- event
}

func TestQueueRetriesAndCompletes(t *testing.T) {
	st := processingTestStore(t)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	enqueueTestJob(t, st, "sermon-1", "job-1", now)

	var calls atomic.Int32
	sink := collectingSink{events: make(chan Event, 20)}
	handler := HandlerFunc(func(_ context.Context, _ store.Job, reporter Reporter) (Result, error) {
		call := calls.Add(1)
		if err := reporter.Progress(int(call)*10, nil); err != nil {
			return Result{}, err
		}
		if call < 3 {
			return Result{}, errors.New("temporary")
		}
		return Result{}, nil
	})
	queue := NewQueue(st, map[string]Handler{"test": handler}, Config{
		Workers: 1, PollEvery: 5 * time.Millisecond,
		Backoff: func(int) time.Duration { return 0 },
		Now:     func() time.Time { return now }, Events: sink,
	})
	queue.Start(context.Background())
	t.Cleanup(queue.Stop)

	sm := waitForSermon(t, st, "sermon-1", func(sm store.Sermon) bool { return sm.Status == "done" })
	if sm.Progress != 100 || calls.Load() != 3 {
		t.Fatalf("completed sermon=%+v calls=%d", sm, calls.Load())
	}
	job, err := st.GetJob("job-1")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != "done" || job.Attempts != 3 {
		t.Fatalf("completed job = %+v", job)
	}
	history, err := st.ListJobErrors(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("error history length = %d, want 2", len(history))
	}

	foundComplete := false
	for len(sink.events) > 0 {
		if event := <-sink.events; event.Name == EventStageCompleted {
			foundComplete = true
		}
	}
	if !foundComplete {
		t.Fatal("stage_completed event not published")
	}
}

func TestQueueCapsConcurrentHandlers(t *testing.T) {
	st := processingTestStore(t)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	for i, id := range []string{"one", "two", "three"} {
		enqueueTestJob(t, st, id, "job-"+id, now.Add(time.Duration(i)*time.Nanosecond))
	}

	release := make(chan struct{})
	var active atomic.Int32
	var maximum atomic.Int32
	handler := HandlerFunc(func(ctx context.Context, _ store.Job, _ Reporter) (Result, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			old := maximum.Load()
			if current <= old || maximum.CompareAndSwap(old, current) {
				break
			}
		}
		select {
		case <-release:
			return Result{}, nil
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	})
	queue := NewQueue(st, map[string]Handler{"test": handler}, Config{
		Workers: 2, PollEvery: 5 * time.Millisecond,
	})
	queue.Start(context.Background())

	deadline := time.Now().Add(2 * time.Second)
	for active.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if active.Load() != 2 {
		close(release)
		queue.Stop()
		t.Fatalf("active handlers = %d, want 2", active.Load())
	}
	if maximum.Load() > 2 {
		close(release)
		queue.Stop()
		t.Fatalf("maximum concurrency = %d, want <=2", maximum.Load())
	}
	close(release)
	for _, id := range []string{"one", "two", "three"} {
		waitForSermon(t, st, id, func(sm store.Sermon) bool { return sm.Status == "done" })
	}
	queue.Stop()
	if maximum.Load() != 2 {
		t.Fatalf("maximum concurrency = %d, want 2", maximum.Load())
	}
}

func TestQueueRecoversPanicsAndFailsAfterThreeAttempts(t *testing.T) {
	st := processingTestStore(t)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	enqueueTestJob(t, st, "sermon-1", "job-1", now)

	queue := NewQueue(st, map[string]Handler{
		"test": HandlerFunc(func(context.Context, store.Job, Reporter) (Result, error) {
			panic("boom")
		}),
	}, Config{
		Workers: 1, PollEvery: 5 * time.Millisecond,
		Backoff: func(int) time.Duration { return 0 },
		Now:     func() time.Time { return now },
	})
	queue.Start(context.Background())
	t.Cleanup(queue.Stop)

	sm := waitForSermon(t, st, "sermon-1", func(sm store.Sermon) bool { return sm.Status == "failed" })
	if sm.Error == nil || len(*sm.Error) == 0 {
		t.Fatalf("failed sermon has no error: %+v", sm)
	}
	job, err := st.GetJob("job-1")
	if err != nil {
		t.Fatal(err)
	}
	if job.Attempts != store.MaxJobAttempts() || job.State != "failed" {
		t.Fatalf("failed job = %+v", job)
	}
	history, err := st.ListJobErrors(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != store.MaxJobAttempts() {
		t.Fatalf("panic history length = %d", len(history))
	}
}

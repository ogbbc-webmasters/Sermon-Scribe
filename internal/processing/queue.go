// Package processing runs persistent background jobs from the SQLite queue.
package processing

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

// Event names match the SSE events consumed by the frontend.
const (
	EventStageStarted   = "stage_started"
	EventProgress       = "progress"
	EventStageCompleted = "stage_completed"
	EventFailed         = "failed"
)

// Event is one current sermon state transition.
type Event struct {
	Name   string
	Sermon store.Sermon
}

// EventSink receives worker state transitions without coupling the queue to HTTP.
type EventSink interface {
	Publish(Event)
}

type discardEvents struct{}

func (discardEvents) Publish(Event) {}

// Reporter persists handler progress and publishes it to observers.
type Reporter interface {
	Progress(percent int, checkpoint *string) error
}

// Result describes the transition after a successful handler. A nil Next marks
// the current stage done; a non-nil Next advances and queues another job.
type Result struct {
	Next *store.NewJob
}

// Handler performs one job type. Implementations must honor context cancellation
// and use Reporter for durable progress/checkpoints.
type Handler interface {
	Run(context.Context, store.Job, Reporter) (Result, error)
}

// HandlerFunc adapts a function into a Handler.
type HandlerFunc func(context.Context, store.Job, Reporter) (Result, error)

func (f HandlerFunc) Run(ctx context.Context, job store.Job, reporter Reporter) (Result, error) {
	return f(ctx, job, reporter)
}

// Queue is a bounded worker pool over the persistent store queue.
type Queue struct {
	store        *store.Store
	handlers     map[string]Handler
	handlerTypes []string
	events       EventSink
	workers      int
	pollEvery    time.Duration
	backoff      func(int) time.Duration
	now          func() time.Time

	wake   chan struct{}
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Config controls queue runtime behavior. Zero values select production defaults.
type Config struct {
	Workers   int
	PollEvery time.Duration
	Backoff   func(attempt int) time.Duration
	Now       func() time.Time
	Events    EventSink
}

// NewQueue builds a queue. Only registered handler types are claimed.
func NewQueue(st *store.Store, handlers map[string]Handler, cfg Config) *Queue {
	workers := cfg.Workers
	if workers == 0 {
		workers = 2
	}
	pollEvery := cfg.PollEvery
	if pollEvery == 0 {
		pollEvery = time.Second
	}
	backoff := cfg.Backoff
	if backoff == nil {
		backoff = func(attempt int) time.Duration {
			return time.Duration(1<<(attempt-1)) * time.Second
		}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	events := cfg.Events
	if events == nil {
		events = discardEvents{}
	}
	types := make([]string, 0, len(handlers))
	for typ := range handlers {
		types = append(types, typ)
	}
	sort.Strings(types)
	return &Queue{
		store: st, handlers: handlers, handlerTypes: types, events: events,
		workers: workers, pollEvery: pollEvery, backoff: backoff, now: now,
		wake: make(chan struct{}, 1),
	}
}

// Start launches the configured workers. It is safe to call once.
func (q *Queue) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	q.cancel = cancel
	for range q.workers {
		q.wg.Add(1)
		go q.worker(ctx)
	}
	q.Notify()
}

// Stop cancels handlers and waits for workers to exit. Running jobs remain in
// SQLite and are returned to queued by startup recovery on the next process run.
func (q *Queue) Stop() {
	if q.cancel != nil {
		q.cancel()
		q.wg.Wait()
	}
}

// Notify wakes idle workers after new work is committed.
func (q *Queue) Notify() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *Queue) worker(ctx context.Context) {
	defer q.wg.Done()
	ticker := time.NewTicker(q.pollEvery)
	defer ticker.Stop()

	for {
		worked, err := q.runOne(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("processing queue: %v", err)
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-q.wake:
		case <-ticker.C:
		}
	}
}

func (q *Queue) runOne(ctx context.Context) (bool, error) {
	job, err := q.store.ClaimNextJob(ctx, q.handlerTypes, q.now())
	if errors.Is(err, store.ErrNoJob) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	sm, err := q.store.GetSermon(job.SermonID)
	if err != nil {
		return true, fmt.Errorf("load claimed sermon %s: %w", job.SermonID, err)
	}
	q.events.Publish(Event{Name: EventStageStarted, Sermon: sm})

	reporter := jobReporter{queue: q, jobID: job.ID}
	result, runErr := runHandler(ctx, q.handlers[job.Type], job, reporter)
	if ctx.Err() != nil {
		// Leave the job running. Startup recovery distinguishes shutdown from a
		// genuine processing failure and requeues it without another error entry.
		return true, ctx.Err()
	}
	if runErr != nil {
		return true, q.handleFailure(job, runErr)
	}

	sm, err = q.store.CompleteJob(job, result.Next, q.now())
	if err != nil {
		return true, fmt.Errorf("complete job %s: %w", job.ID, err)
	}
	q.events.Publish(Event{Name: EventStageCompleted, Sermon: sm})
	if result.Next != nil {
		q.Notify()
	}
	return true, nil
}

func runHandler(ctx context.Context, handler Handler, job store.Job, reporter Reporter) (result Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("handler panic: %v\n%s", recovered, debug.Stack())
		}
	}()
	return handler.Run(ctx, job, reporter)
}

func (q *Queue) handleFailure(job store.Job, runErr error) error {
	message := runErr.Error()
	if job.Attempts < store.MaxJobAttempts() {
		available := q.now().Add(q.backoff(job.Attempts))
		sm, err := q.store.RequeueJob(job, message, available, q.now())
		if err != nil {
			return fmt.Errorf("requeue job %s: %w", job.ID, err)
		}
		q.events.Publish(Event{Name: EventProgress, Sermon: sm})
		return nil
	}
	sm, err := q.store.FailJob(job, message, q.now())
	if err != nil {
		return fmt.Errorf("fail job %s: %w", job.ID, err)
	}
	q.events.Publish(Event{Name: EventFailed, Sermon: sm})
	return nil
}

type jobReporter struct {
	queue *Queue
	jobID string
}

func (r jobReporter) Progress(percent int, checkpoint *string) error {
	sm, err := r.queue.store.UpdateJobProgress(r.jobID, percent, checkpoint, r.queue.now())
	if err != nil {
		return err
	}
	r.queue.events.Publish(Event{Name: EventProgress, Sermon: sm})
	return nil
}

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/processing"
	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

const eventBufferSize = 64

// EventHub fans best-effort processing events out to connected SSE clients.
// A client that cannot keep up is disconnected and will recover from a fresh
// snapshot when EventSource reconnects.
type streamEvent struct {
	Name string
	Data any
}

type EventHub struct {
	mu          sync.Mutex
	subscribers map[chan streamEvent]struct{}
}

// NewEventHub constructs an empty event hub.
func NewEventHub() *EventHub {
	return &EventHub{subscribers: make(map[chan streamEvent]struct{})}
}

// Publish implements processing.EventSink.
func (h *EventHub) Publish(event processing.Event) {
	h.publish(streamEvent{Name: event.Name, Data: event.Sermon})
}

// PublishDeleted removes a sermon from connected clients. The id remains a
// frontend tombstone so a delayed worker event cannot recreate the card.
func (h *EventHub) PublishDeleted(id string) {
	h.publish(streamEvent{Name: "deleted", Data: map[string]string{"id": id}})
}

func (h *EventHub) publish(event streamEvent) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subscribers {
		select {
		case ch <- event:
		default:
			delete(h.subscribers, ch)
			close(ch)
		}
	}
}

func (h *EventHub) subscribe() (<-chan streamEvent, func()) {
	ch := make(chan streamEvent, eventBufferSize)
	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subscribers[ch]; ok {
			delete(h.subscribers, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

// subscribeWithSnapshot serializes the database snapshot and registration
// with Publish. The lock is released before the caller performs any writes.
func (h *EventHub) subscribeWithSnapshot(load func() ([]store.Sermon, error)) (<-chan streamEvent, []store.Sermon, func(), error) {
	h.mu.Lock()
	snapshot, err := load()
	if err != nil {
		h.mu.Unlock()
		return nil, nil, nil, err
	}
	ch := make(chan streamEvent, eventBufferSize)
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()
	unsubscribe := func() {
		h.mu.Lock()
		if _, ok := h.subscribers[ch]; ok {
			delete(h.subscribers, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
	return ch, snapshot, unsubscribe, nil
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unavailable")
		return
	}
	if s.Events == nil {
		writeError(w, http.StatusServiceUnavailable, "event stream unavailable")
		return
	}

	events, snapshot, unsubscribe, err := s.Events.subscribeWithSnapshot(s.Store.ListSermons)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load event snapshot")
		return
	}
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 3000\n")
	if err := writeSSE(w, "snapshot", snapshot); err != nil {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := writeSSE(w, event.Name, event.Data); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
	return err
}

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// RealtimeAction specifies the mutation type.
type RealtimeAction string

const (
	ActionCreate      RealtimeAction = "create"
	ActionUpdate      RealtimeAction = "update"
	ActionDelete      RealtimeAction = "delete"
	ActionImport      RealtimeAction = "import"
	ActionCreateTable RealtimeAction = "create_table"
	ActionDropTable   RealtimeAction = "drop_table"
)

// RealtimeEvent represents a database mutation event sent over SSE.
type RealtimeEvent struct {
	Action RealtimeAction `json:"action"`
	Table  string         `json:"table"`
	ID     any            `json:"id,omitempty"`
	Record map[string]any `json:"record,omitempty"`
	Time   int64          `json:"time"`
}

// RealtimeBroker distributes mutation events to connected SSE subscribers.
type RealtimeBroker struct {
	mu          sync.RWMutex
	subscribers map[chan RealtimeEvent]struct{}
	closed      bool
}

// NewRealtimeBroker creates a thread-safe SSE pub/sub broker.
func NewRealtimeBroker() *RealtimeBroker {
	return &RealtimeBroker{
		subscribers: make(map[chan RealtimeEvent]struct{}),
	}
}

// Subscribe registers a new client channel with a buffer.
func (b *RealtimeBroker) Subscribe() chan RealtimeEvent {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan RealtimeEvent, 64)
	if b.closed {
		close(ch)
		return ch
	}
	b.subscribers[ch] = struct{}{}
	return ch
}

// Unsubscribe removes a client channel and drains remaining items.
func (b *RealtimeBroker) Unsubscribe(ch chan RealtimeEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.subscribers[ch]; exists {
		delete(b.subscribers, ch)
		close(ch)
	}
}

// Broadcast distributes an event to all subscribers without blocking.
func (b *RealtimeBroker) Broadcast(event RealtimeEvent) {
	if event.Time == 0 {
		event.Time = time.Now().UnixMilli()
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return
	}

	for ch := range b.subscribers {
		select {
		case ch <- event:
		default:
			// Client buffer is full or slow; drop to prevent producer worker hangs
		}
	}
}

// SubscriberCount returns the active number of subscribers.
func (b *RealtimeBroker) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}

// Close closes all subscriber channels.
func (b *RealtimeBroker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	b.closed = true
	for ch := range b.subscribers {
		close(ch)
	}
	b.subscribers = make(map[chan RealtimeEvent]struct{})
}

// handleRealtimeStream serves the SSE stream over HTTP.
func (s *Server) handleRealtimeStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Accel-Buffering", "no")

	// Send initial connection greeting
	initMsg, _ := json.Marshal(map[string]any{
		"status": "connected",
		"time":   time.Now().UnixMilli(),
	})
	fmt.Fprintf(w, "event: connected\ndata: %s\n\n", initMsg)
	flusher.Flush()

	events := s.broker.Subscribe()
	defer s.broker.Unsubscribe(events)

	notify := r.Context().Done()

	// Keep-alive heartbeat ticker
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-notify:
			return
		case <-ticker.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		case event, ok := <-events:
			if !ok {
				return
			}
			payload, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: record_change\ndata: %s\n\n", payload)
			flusher.Flush()
		}
	}
}

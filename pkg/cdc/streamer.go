package cdc

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"sqlion/pkg/storage/memengine"
)

// Streamer coordinates CDC event ingestion, ring-buffer caching, and real-time streaming.
type Streamer struct {
	mu          sync.RWMutex
	subscribers map[string]*subscriber
	history     []ChangeEvent
	historyCap  int
	historyHead int
	historyLen  int
	latestLSN   atomic.Int64
	closed      atomic.Bool
	nextSubID   atomic.Uint64
}

type subscriber struct {
	id          string
	ch          chan ChangeEvent
	tableFilter string
	fromLSN     int64
}

// NewStreamer creates a new CDC streamer with a bounded ring buffer for historical replay.
func NewStreamer(historyCap int) *Streamer {
	if historyCap <= 0 {
		historyCap = 10000
	}
	return &Streamer{
		subscribers: make(map[string]*subscriber),
		history:     make([]ChangeEvent, historyCap),
		historyCap:  historyCap,
	}
}

// OnWALRecord is the engine hook callback invoked when an atomic transaction commits.
func (s *Streamer) OnWALRecord(rec memengine.WALRecord) {
	if s.closed.Load() {
		return
	}
	events := WALRecordToChangeEvents(rec)
	if len(events) == 0 {
		return
	}
	s.latestLSN.Store(rec.LSN)

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, ev := range events {
		// 1. Append to bounded ring buffer
		idx := (s.historyHead + s.historyLen) % s.historyCap
		if s.historyLen < s.historyCap {
			s.history[idx] = ev
			s.historyLen++
		} else {
			s.history[s.historyHead] = ev
			s.historyHead = (s.historyHead + 1) % s.historyCap
		}

		// 2. Non-blocking fan-out to active subscribers (Golden Rule 29)
		for _, sub := range s.subscribers {
			if sub.tableFilter != "" && !strings.EqualFold(sub.tableFilter, ev.Table) {
				continue
			}
			select {
			case sub.ch <- ev:
			default:
				// Slow consumer buffer saturated; drop to protect hot path from stalling
			}
		}
	}
}

// LatestLSN returns the latest Log Sequence Number ingested.
func (s *Streamer) LatestLSN() int64 {
	return s.latestLSN.Load()
}

// SubscriberCount returns the current number of active subscribers.
func (s *Streamer) SubscriberCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.subscribers)
}

// Subscribe registers a new subscriber channel starting from fromLSN with an optional table filter.
func (s *Streamer) Subscribe(ctx context.Context, fromLSN int64, tableFilter string, bufSize int) (<-chan ChangeEvent, func(), error) {
	if s.closed.Load() {
		return nil, nil, fmt.Errorf("cdc streamer is closed")
	}
	if bufSize <= 0 {
		bufSize = 1024
	}

	subID := fmt.Sprintf("sub_%d", s.nextSubID.Add(1))
	ch := make(chan ChangeEvent, bufSize)
	sub := &subscriber{
		id:          subID,
		ch:          ch,
		tableFilter: tableFilter,
		fromLSN:     fromLSN,
	}

	s.mu.Lock()
	// Replay missed historical events if fromLSN is behind
	if fromLSN > 0 && s.historyLen > 0 {
		for i := 0; i < s.historyLen; i++ {
			ev := s.history[(s.historyHead+i)%s.historyCap]
			if ev.LSN > fromLSN {
				if tableFilter == "" || strings.EqualFold(tableFilter, ev.Table) {
					select {
					case ch <- ev:
					default:
					}
				}
			}
		}
	}
	s.subscribers[subID] = sub
	s.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			s.mu.Lock()
			if existing, ok := s.subscribers[subID]; ok {
				delete(s.subscribers, subID)
				close(existing.ch)
			}
			s.mu.Unlock()
		})
	}

	return ch, cancel, nil
}

// ServeHTTP implements the HTTP streaming endpoint (e.g. GET /api/cdc/stream).
// Query Parameters:
//   - from_lsn: int64 (optional, replay events committed after this LSN)
//   - table: string (optional, filter events by table name)
//   - format: "ndjson" (default) or "sse"
func (s *Streamer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	fromLSNStr := r.URL.Query().Get("from_lsn")
	var fromLSN int64
	if fromLSNStr != "" {
		if parsed, err := strconv.ParseInt(fromLSNStr, 10, 64); err == nil {
			fromLSN = parsed
		}
	}
	tableFilter := r.URL.Query().Get("table")
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = "ndjson"
	}

	if format == "sse" {
		w.Header().Set("Content-Type", "text/event-stream")
	} else {
		w.Header().Set("Content-Type", "application/x-ndjson")
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, cancel, err := s.Subscribe(r.Context(), fromLSN, tableFilter, 2048)
	if err != nil {
		return
	}
	defer cancel()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			// Keep-alive heartbeat
			if format == "sse" {
				fmt.Fprintf(w, ": ping\n\n")
			} else {
				fmt.Fprintf(w, "{\"type\":\"ping\"}\n")
			}
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			data, err := ev.ToJSON()
			if err != nil {
				continue
			}
			if format == "sse" {
				fmt.Fprintf(w, "id: %d\nevent: change\ndata: %s\n\n", ev.LSN, string(data))
			} else {
				w.Write(data)
				w.Write([]byte("\n"))
			}
			flusher.Flush()
		}
	}
}

// Close shuts down the streamer and unsubscribes all active listeners.
func (s *Streamer) Close() {
	if s.closed.Swap(true) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sub := range s.subscribers {
		close(sub.ch)
		delete(s.subscribers, id)
	}
}

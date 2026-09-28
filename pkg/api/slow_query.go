package api

import (
	"sync"
	"sync/atomic"
	"time"
)

// SlowQueryEntry captures execution diagnostic details for slow queries.
type SlowQueryEntry struct {
	ID           int64     `json:"id"`
	Query        string    `json:"query"`
	DurationUS   int64     `json:"duration_us"`
	DurationMS   float64   `json:"duration_ms"`
	Timestamp    time.Time `json:"timestamp"`
	RowsAffected int64     `json:"rows_affected"`
	SessionID    string    `json:"session_id"`
	Error        string    `json:"error,omitempty"`
}

const (
	// DefaultSlowQueryThreshold is the baseline threshold (50ms) to identify slow queries.
	DefaultSlowQueryThreshold = 50 * time.Millisecond
	// MaxSlowQueryCapacity is the maximum capacity of the bounded in-memory ring buffer.
	MaxSlowQueryCapacity = 200
)

// SlowQueryLogger provides bounded in-memory circular history for slow queries.
type SlowQueryLogger struct {
	mu          sync.RWMutex
	threshold   atomic.Int64 // nanoseconds
	idCounter   atomic.Int64
	entries     []SlowQueryEntry
	maxCapacity int
}

// NewSlowQueryLogger creates a bounded SlowQueryLogger.
func NewSlowQueryLogger(threshold time.Duration) *SlowQueryLogger {
	if threshold <= 0 {
		threshold = DefaultSlowQueryThreshold
	}
	l := &SlowQueryLogger{
		entries:     make([]SlowQueryEntry, 0, MaxSlowQueryCapacity),
		maxCapacity: MaxSlowQueryCapacity,
	}
	l.threshold.Store(threshold.Nanoseconds())
	return l
}

// Threshold returns the current slow query threshold duration.
func (l *SlowQueryLogger) Threshold() time.Duration {
	return time.Duration(l.threshold.Load())
}

// SetThreshold updates the slow query threshold duration.
func (l *SlowQueryLogger) SetThreshold(d time.Duration) {
	l.threshold.Store(d.Nanoseconds())
}

// Record records a slow query entry if duration meets or exceeds threshold.
func (l *SlowQueryLogger) Record(sessionID, query string, duration time.Duration, rowsAffected int64, err error) bool {
	thresh := l.threshold.Load()
	if thresh <= 0 || duration.Nanoseconds() < thresh {
		return false
	}

	errStr := ""
	if err != nil {
		errStr = err.Error()
	}

	entry := SlowQueryEntry{
		ID:           l.idCounter.Add(1),
		Query:        query,
		DurationUS:   duration.Microseconds(),
		DurationMS:   float64(duration.Microseconds()) / 1000.0,
		Timestamp:    time.Now(),
		RowsAffected: rowsAffected,
		SessionID:    sessionID,
		Error:        errStr,
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.entries) >= l.maxCapacity {
		// Evict oldest entry (sliding ring buffer)
		l.entries = l.entries[1:]
	}
	l.entries = append(l.entries, entry)
	return true
}

// GetEntries returns a slice copy of recent slow queries, newest first.
func (l *SlowQueryLogger) GetEntries(limit int) []SlowQueryEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	n := len(l.entries)
	if n == 0 {
		return []SlowQueryEntry{}
	}
	if limit <= 0 || limit > n {
		limit = n
	}

	res := make([]SlowQueryEntry, limit)
	for i := 0; i < limit; i++ {
		res[i] = l.entries[n-1-i] // reverse order (newest first)
	}
	return res
}

// Clear purges all recorded slow queries.
func (l *SlowQueryLogger) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = l.entries[:0]
}

package engine

import (
	"sync"
	"sync/atomic"
	"time"
)

// GeneralLogEntry captures diagnostic details for all executed SQL queries.
type GeneralLogEntry struct {
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
	// MaxGeneralLogCapacity is the default capacity of the bounded in-memory circular history buffer.
	MaxGeneralLogCapacity = 500
)

// GeneralLogger provides bounded in-memory circular history and optional rotating file output for all executed queries.
type GeneralLogger struct {
	mu          sync.RWMutex
	enabled     atomic.Bool
	idCounter   atomic.Int64
	entries     []GeneralLogEntry
	maxCapacity int
	writer      *RotatingLogWriter
}

// NewGeneralLogger creates a new GeneralLogger instance.
func NewGeneralLogger(enabled bool) *GeneralLogger {
	l := &GeneralLogger{
		entries:     make([]GeneralLogEntry, 0, MaxGeneralLogCapacity),
		maxCapacity: MaxGeneralLogCapacity,
	}
	l.enabled.Store(enabled)
	return l
}

// IsEnabled returns true if general query logging is active.
func (l *GeneralLogger) IsEnabled() bool {
	return l.enabled.Load()
}

// SetEnabled toggles general query logging.
func (l *GeneralLogger) SetEnabled(enabled bool) {
	l.enabled.Store(enabled)
}

// SetLogFile configures the rotating disk log file for general queries.
func (l *GeneralLogger) SetLogFile(filePath string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.writer != nil {
		_ = l.writer.Close()
		l.writer = nil
	}

	if filePath == "" {
		return nil
	}

	w, err := NewRotatingLogWriter(filePath, DefaultMaxLogFileSize)
	if err != nil {
		return err
	}
	l.writer = w
	return nil
}

// LogFilePath returns the current log file path, or empty string if disabled.
func (l *GeneralLogger) LogFilePath() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.writer != nil {
		return l.writer.FilePath()
	}
	return ""
}

// Record captures an executed query entry into memory buffer and optional rotating file.
func (l *GeneralLogger) Record(sessionID, query string, duration time.Duration, rowsAffected int64, err error) {
	if !l.enabled.Load() {
		return
	}

	errStr := ""
	if err != nil {
		errStr = err.Error()
	}

	entry := GeneralLogEntry{
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
	if len(l.entries) >= l.maxCapacity {
		l.entries = l.entries[1:]
	}
	l.entries = append(l.entries, entry)
	w := l.writer
	l.mu.Unlock()

	if w != nil {
		_ = w.WriteLine(entry)
	}
}

// GetEntries returns a slice copy of recent queries, newest first.
func (l *GeneralLogger) GetEntries(limit int) []GeneralLogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	n := len(l.entries)
	if n == 0 {
		return []GeneralLogEntry{}
	}
	if limit <= 0 || limit > n {
		limit = n
	}

	res := make([]GeneralLogEntry, limit)
	for i := 0; i < limit; i++ {
		res[i] = l.entries[n-1-i]
	}
	return res
}

// Clear purges in-memory general query entries.
func (l *GeneralLogger) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = l.entries[:0]
}

// Close gracefully flushes and closes disk log files.
func (l *GeneralLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.writer != nil {
		err := l.writer.Close()
		l.writer = nil
		return err
	}
	return nil
}

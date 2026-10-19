package api

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var reqLogSeq uint64

// RequestLogEntry represents a captured HTTP API request and its execution metrics.
type RequestLogEntry struct {
	ID         string    `json:"id"`
	Timestamp  time.Time `json:"timestamp"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	DurationMs float64   `json:"duration_ms"`
	ClientIP   string    `json:"client_ip"`
	UserAgent  string    `json:"user_agent,omitempty"`
}

// RequestActivityLogger maintains a thread-safe, bounded ring buffer of recent HTTP requests.
// Strictly adheres to Golden Rule 23 (Bounded Buffers & Defensive Resource Limits).
type RequestActivityLogger struct {
	mu       sync.RWMutex
	capacity int
	entries  []RequestLogEntry
}

// NewRequestActivityLogger creates a logger with the given capacity.
func NewRequestActivityLogger(capacity int) *RequestActivityLogger {
	if capacity <= 0 {
		capacity = 200
	}
	return &RequestActivityLogger{
		capacity: capacity,
		entries:  make([]RequestLogEntry, 0, capacity),
	}
}

// Add appends a new request log entry, evicting the oldest entry when at capacity.
func (l *RequestActivityLogger) Add(entry RequestLogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.entries) >= l.capacity {
		copy(l.entries, l.entries[1:])
		l.entries[len(l.entries)-1] = entry
	} else {
		l.entries = append(l.entries, entry)
	}
}

// GetRecent returns a copy of recent entries in reverse chronological order (newest first).
func (l *RequestActivityLogger) GetRecent(limit int, methodFilter, statusFilter, queryFilter string) []RequestLogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if limit <= 0 || limit > len(l.entries) {
		limit = len(l.entries)
	}

	result := make([]RequestLogEntry, 0, limit)
	methodFilter = strings.ToUpper(strings.TrimSpace(methodFilter))
	queryFilter = strings.ToLower(strings.TrimSpace(queryFilter))

	// Iterate backwards (newest first)
	for i := len(l.entries) - 1; i >= 0 && len(result) < limit; i-- {
		e := l.entries[i]

		// Apply method filter
		if methodFilter != "" && methodFilter != "ALL" && e.Method != methodFilter {
			continue
		}

		// Apply status filter (e.g. "2XX", "3XX", "4XX", "5XX", "ERR")
		if statusFilter != "" && statusFilter != "ALL" {
			switch statusFilter {
			case "2XX":
				if e.Status < 200 || e.Status >= 300 {
					continue
				}
			case "3XX":
				if e.Status < 300 || e.Status >= 400 {
					continue
				}
			case "4XX":
				if e.Status < 400 || e.Status >= 500 {
					continue
				}
			case "5XX":
				if e.Status < 500 {
					continue
				}
			case "ERR":
				if e.Status < 400 {
					continue
				}
			}
		}

		// Apply query filter (search in path or client IP)
		if queryFilter != "" {
			if !strings.Contains(strings.ToLower(e.Path), queryFilter) &&
				!strings.Contains(strings.ToLower(e.ClientIP), queryFilter) {
				continue
			}
		}

		result = append(result, e)
	}

	return result
}

// Clear clears all stored request logs.
func (l *RequestActivityLogger) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = l.entries[:0]
}

// Count returns the total number of entries currently stored.
func (l *RequestActivityLogger) Count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.entries)
}

// loggingResponseWriter captures the HTTP status code for logging.
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

func (lrw *loggingResponseWriter) Flush() {
	if f, ok := lrw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (lrw *loggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := lrw.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// requestLoggingMiddleware records HTTP request activity into RequestActivityLogger.
func requestLoggingMiddleware(logger *RequestActivityLogger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Skip requests for logs polling itself to prevent endless feedback loops
		if strings.HasPrefix(path, "/api/logs/requests") {
			next.ServeHTTP(w, r)
			return
		}

		// Only log /api/ requests (ignore static assets and web studio bundles)
		if !strings.HasPrefix(path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		lrw := &loggingResponseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		next.ServeHTTP(lrw, r)

		duration := time.Since(start)
		durationMs := float64(duration.Microseconds()) / 1000.0

		clientIP := r.RemoteAddr
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			clientIP = strings.TrimSpace(parts[0])
		} else if host, _, err := net.SplitHostPort(clientIP); err == nil {
			clientIP = host
		}

		seq := atomic.AddUint64(&reqLogSeq, 1)
		entry := RequestLogEntry{
			ID:         fmt.Sprintf("req_%d_%d", time.Now().Unix(), seq),
			Timestamp:  start,
			Method:     r.Method,
			Path:       path,
			Status:     lrw.statusCode,
			DurationMs: durationMs,
			ClientIP:   clientIP,
			UserAgent:  r.UserAgent(),
		}

		logger.Add(entry)
	})
}

// handleGetRequestLogs handles GET /api/logs/requests.
func (s *Server) handleGetRequestLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 100
	if lStr := q.Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}

	methodFilter := q.Get("method")
	statusFilter := q.Get("status")
	searchQuery := q.Get("q")
	if searchQuery == "" {
		searchQuery = q.Get("search")
	}

	logs := s.requestLogger.GetRecent(limit, methodFilter, statusFilter, searchQuery)
	total := s.requestLogger.Count()

	writeJSON(w, http.StatusOK, map[string]any{
		"logs":  logs,
		"total": total,
	})
}

// handleClearRequestLogs handles DELETE /api/logs/requests.
func (s *Server) handleClearRequestLogs(w http.ResponseWriter, r *http.Request) {
	s.requestLogger.Clear()
	writeJSON(w, http.StatusOK, map[string]any{
		"message": "request logs cleared successfully",
	})
}

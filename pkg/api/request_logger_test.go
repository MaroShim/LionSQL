package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestRequestActivityLogger_BoundedCapacity(t *testing.T) {
	logger := NewRequestActivityLogger(5)

	for i := 1; i <= 10; i++ {
		logger.Add(RequestLogEntry{
			ID:         string(rune('A' + i)),
			Timestamp:  time.Now(),
			Method:     "GET",
			Path:       "/api/tables",
			Status:     200,
			DurationMs: float64(i),
			ClientIP:   "127.0.0.1",
		})
	}

	if count := logger.Count(); count != 5 {
		t.Fatalf("expected 5 entries, got %d", count)
	}

	recent := logger.GetRecent(10, "", "", "")
	if len(recent) != 5 {
		t.Fatalf("expected 5 recent entries, got %d", len(recent))
	}

	// First recent entry should be the latest (i=10)
	if recent[0].DurationMs != 10.0 {
		t.Errorf("expected newest duration 10.0, got %f", recent[0].DurationMs)
	}
}

func TestRequestActivityLogger_Filters(t *testing.T) {
	logger := NewRequestActivityLogger(10)

	logger.Add(RequestLogEntry{
		ID:        "1",
		Method:    "GET",
		Path:      "/api/tables/users/records",
		Status:    200,
		ClientIP:  "192.168.1.10",
		Timestamp: time.Now(),
	})
	logger.Add(RequestLogEntry{
		ID:        "2",
		Method:    "POST",
		Path:      "/api/tables/users/records",
		Status:    201,
		ClientIP:  "10.0.0.1",
		Timestamp: time.Now(),
	})
	logger.Add(RequestLogEntry{
		ID:        "3",
		Method:    "DELETE",
		Path:      "/api/tables/posts/records/42",
		Status:    404,
		ClientIP:  "10.0.0.1",
		Timestamp: time.Now(),
	})

	// Filter by method
	posts := logger.GetRecent(10, "POST", "", "")
	if len(posts) != 1 || posts[0].ID != "2" {
		t.Errorf("expected 1 POST entry, got %v", posts)
	}

	// Filter by status 4XX
	errors := logger.GetRecent(10, "", "4XX", "")
	if len(errors) != 1 || errors[0].ID != "3" {
		t.Errorf("expected 1 4XX entry, got %v", errors)
	}

	// Filter by query
	userLogs := logger.GetRecent(10, "", "", "users")
	if len(userLogs) != 2 {
		t.Errorf("expected 2 user logs, got %d", len(userLogs))
	}

	// Filter by client IP
	ipLogs := logger.GetRecent(10, "", "", "192.168")
	if len(ipLogs) != 1 || ipLogs[0].ID != "1" {
		t.Errorf("expected 1 IP match, got %v", ipLogs)
	}
}

func TestServer_RequestLogging_EndToEnd(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	cat := catalog.New(eng)
	server := NewServer(cat, eng)

	// Make an API request to /api/health
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	// Now check /api/logs/requests
	reqLogs := httptest.NewRequest(http.MethodGet, "/api/logs/requests", nil)
	wLogs := httptest.NewRecorder()
	server.ServeHTTP(wLogs, reqLogs)

	if wLogs.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from logs endpoint, got %d: %s", wLogs.Code, wLogs.Body.String())
	}

	var resp struct {
		Logs  []RequestLogEntry `json:"logs"`
		Total int               `json:"total"`
	}
	if err := json.Unmarshal(wLogs.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode logs JSON: %v", err)
	}

	if resp.Total != 1 {
		t.Fatalf("expected total 1, got %d", resp.Total)
	}
	if len(resp.Logs) != 1 {
		t.Fatalf("expected 1 log item, got %d", len(resp.Logs))
	}
	if resp.Logs[0].Path != "/api/health" || resp.Logs[0].Method != "GET" || resp.Logs[0].Status != 200 {
		t.Errorf("unexpected log entry: %+v", resp.Logs[0])
	}

	// Test DELETE /api/logs/requests
	reqDel := httptest.NewRequest(http.MethodDelete, "/api/logs/requests", nil)
	wDel := httptest.NewRecorder()
	server.ServeHTTP(wDel, reqDel)

	if wDel.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from delete logs, got %d", wDel.Code)
	}

	if server.requestLogger.Count() != 0 {
		t.Errorf("expected logger count 0 after clear, got %d", server.requestLogger.Count())
	}
}

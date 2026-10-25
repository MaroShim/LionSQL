package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleOverviewStats(t *testing.T) {
	srv := setupTestServer()

	// 1. Initial overview test
	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse json response: %v", err)
	}

	if resp["success"] != true {
		t.Fatalf("expected success true, got %v", resp["success"])
	}
	if resp["engine"] == "" {
		t.Fatalf("expected engine to be non-empty, got %v", resp["engine"])
	}
	if resp["version"] == "" {
		t.Fatalf("expected version to be non-empty, got %v", resp["version"])
	}
	if _, ok := resp["uptime_seconds"]; !ok {
		t.Fatalf("expected uptime_seconds in response")
	}
	if _, ok := resp["active_sessions"]; !ok {
		t.Fatalf("expected active_sessions in response")
	}
	if _, ok := resp["tps"]; !ok {
		t.Fatalf("expected tps in response")
	}
	if _, ok := resp["table_count"]; !ok {
		t.Fatalf("expected table_count in response")
	}

	// 2. Test formatUptime helper
	if res := formatUptime(45); res != "45s" {
		t.Errorf("expected 45s, got %s", res)
	}
	if res := formatUptime(125); res != "2m 5s" {
		t.Errorf("expected 2m 5s, got %s", res)
	}
	if res := formatUptime(3665); res != "1h 1m 5s" {
		t.Errorf("expected 1h 1m 5s, got %s", res)
	}
}

func TestOverviewStats_MemoryCaching(t *testing.T) {
	srv := setupTestServer()

	// First call populates cache
	alloc1, goroutines1 := srv.getCachedMemStats()
	if alloc1 == 0 {
		t.Errorf("expected non-zero allocBytes from getCachedMemStats")
	}
	if goroutines1 == 0 {
		t.Errorf("expected non-zero goroutines from getCachedMemStats")
	}

	// Immediate second call should return cached stats
	alloc2, goroutines2 := srv.getCachedMemStats()
	if alloc1 != alloc2 || goroutines1 != goroutines2 {
		t.Errorf("expected cached stats to match within 1s TTL: (%d, %d) vs (%d, %d)",
			alloc1, goroutines1, alloc2, goroutines2)
	}

	// Verify HTTP overview endpoint delivers these stats
	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if allocVal, ok := resp["memory_alloc_bytes"].(float64); !ok || allocVal == 0 {
		t.Errorf("expected positive memory_alloc_bytes, got %v", resp["memory_alloc_bytes"])
	}
	if gVal, ok := resp["goroutines"].(float64); !ok || gVal == 0 {
		t.Errorf("expected positive goroutines, got %v", resp["goroutines"])
	}
}

func TestWebStudio_SessionIsolation(t *testing.T) {
	srv := setupTestServer()

	// Setup table
	setupReq := httptest.NewRequest(http.MethodPost, "/api/sql", strings.NewReader(`{"query":"CREATE TABLE accounts (id INT PRIMARY KEY, balance INT); INSERT INTO accounts VALUES (1, 100);", "session_id":"setup"}`))
	setupReq.Header.Set("Content-Type", "application/json")
	setupW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(setupW, setupReq)
	if setupW.Code != http.StatusOK {
		t.Fatalf("failed to create table: %s", setupW.Body.String())
	}

	// Session A begins transaction and modifies row
	reqA := httptest.NewRequest(http.MethodPost, "/api/sql", strings.NewReader(`{"query":"BEGIN TRANSACTION; UPDATE accounts SET balance = 500 WHERE id = 1;", "session_id":"web-sess-tab-a"}`))
	reqA.Header.Set("Content-Type", "application/json")
	wA := httptest.NewRecorder()
	srv.Handler().ServeHTTP(wA, reqA)
	if wA.Code != http.StatusOK {
		t.Fatalf("session A failed: %s", wA.Body.String())
	}

	// Session B queries table concurrently - must see original balance (100), not dirty read (500)
	reqB := httptest.NewRequest(http.MethodPost, "/api/sql", strings.NewReader(`{"query":"SELECT balance FROM accounts WHERE id = 1;", "session_id":"web-sess-tab-b"}`))
	reqB.Header.Set("Content-Type", "application/json")
	wB := httptest.NewRecorder()
	srv.Handler().ServeHTTP(wB, reqB)
	if wB.Code != http.StatusOK {
		t.Fatalf("session B failed: %s", wB.Body.String())
	}

	var resB struct {
		Rows []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal(wB.Body.Bytes(), &resB); err != nil {
		t.Fatalf("failed to unmarshal B response: %v", err)
	}
	if len(resB.Rows) != 1 {
		t.Fatalf("expected 1 row in B, got %d", len(resB.Rows))
	}
	balB, ok := resB.Rows[0]["balance"].(float64)
	if !ok || int(balB) != 100 {
		t.Fatalf("expected Session B to read uncommitted balance 100, got %v", resB.Rows[0]["balance"])
	}

	// Session A commits
	commitReq := httptest.NewRequest(http.MethodPost, "/api/sql", strings.NewReader(`{"query":"COMMIT;", "session_id":"web-sess-tab-a"}`))
	commitReq.Header.Set("Content-Type", "application/json")
	commitW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(commitW, commitReq)
	if commitW.Code != http.StatusOK {
		t.Fatalf("session A commit failed: %s", commitW.Body.String())
	}

	// Session B queries again - now sees 500
	reqB2 := httptest.NewRequest(http.MethodPost, "/api/sql", strings.NewReader(`{"query":"SELECT balance FROM accounts WHERE id = 1;", "session_id":"web-sess-tab-b"}`))
	reqB2.Header.Set("Content-Type", "application/json")
	wB2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(wB2, reqB2)
	var resB2 struct {
		Rows []map[string]any `json:"rows"`
	}
	_ = json.Unmarshal(wB2.Body.Bytes(), &resB2)
	if len(resB2.Rows) != 1 || int(resB2.Rows[0]["balance"].(float64)) != 500 {
		t.Fatalf("expected Session B to read committed balance 500, got %v", resB2.Rows)
	}
}

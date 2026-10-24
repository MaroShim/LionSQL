package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

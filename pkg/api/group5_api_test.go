package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPI_CreateRecord_DefaultValues(t *testing.T) {
	server := setupMemServer(t)

	// Create table with NOT NULL columns having DEFAULT values
	ddl := `
	CREATE TABLE tasks (
		id INT PRIMARY KEY,
		title VARCHAR(100) NOT NULL,
		status VARCHAR(20) NOT NULL DEFAULT 'pending',
		priority INT NOT NULL DEFAULT 5
	);
	`
	execStatements(t, server, ddl)

	handler := server.Handler()

	// POST record omitting 'status' and 'priority'
	payload := map[string]any{
		"id":    101,
		"title": "Clean codebase",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/records/tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected HTTP 201 Created, got %d: %s", w.Code, w.Body.String())
	}

	var created map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("failed to decode created record: %v", err)
	}

	if created["status"] != "pending" {
		t.Errorf("expected default status 'pending', got %v", created["status"])
	}
	// JSON numbers unmarshal as float64
	if prio, ok := created["priority"].(float64); !ok || int64(prio) != 5 {
		t.Errorf("expected default priority 5, got %v", created["priority"])
	}
}

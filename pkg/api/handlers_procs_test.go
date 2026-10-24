package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPI_ExecProcedure_MalformedJSON_Returns400(t *testing.T) {
	server := setupTestServer()

	// 1. Create a procedure
	ddl := `
	CREATE PROCEDURE dbo.sp_TestEcho @val INT
	AS
	BEGIN
		SELECT @val AS result;
	END;
	`
	if _, err := server.ExecuteSQL(ddl); err != nil {
		t.Fatalf("failed to create procedure: %v", err)
	}

	handler := server.Handler()

	// 2. Send malformed JSON to /api/procs/sp_TestEcho/exec
	req := httptest.NewRequest(http.MethodPost, "/api/procs/sp_TestEcho/exec", bytes.NewReader([]byte("{malformed_json: true,")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("REV-API-001: expected HTTP 400 Bad Request for malformed JSON, got %d: %s", w.Code, w.Body.String())
	}
}

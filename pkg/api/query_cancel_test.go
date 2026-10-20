package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"lionsql/pkg/api"
	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func setupTestServerWithLoopProc(t *testing.T) (*api.Server, func()) {
	t.Helper()
	eng := memengine.New()
	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	createProcSQL := `CREATE PROCEDURE InfiniteSpin AS
BEGIN
    DECLARE @c INT;
    SET @c = 0;
    WHILE 1 = 1
    BEGIN
        SET @c = @c + 1;
    END;
END;`
	res, err := server.ExecuteSQL(createProcSQL)
	if err != nil || !res.Success {
		t.Fatalf("setup infinite loop procedure failed: %v", err)
	}

	return server, func() {
		eng.Close()
	}
}

func waitForActiveQuery(server *api.Server, sessionID string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		queries := server.GetActiveQueries()
		for _, q := range queries {
			if q.SessionID == sessionID {
				return true
			}
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

func TestQueryCancel_ContextCancellation(t *testing.T) {
	server, cleanup := setupTestServerWithLoopProc(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	var queryErr error

	go func() {
		defer wg.Done()
		_, queryErr = server.ExecuteSessionSQLContext(ctx, "worker-sess", "EXEC InfiniteSpin;")
	}()

	if !waitForActiveQuery(server, "worker-sess", 2*time.Second) {
		t.Fatalf("timed out waiting for worker-sess query to become active")
	}

	cancel()
	wg.Wait()

	if queryErr == nil {
		t.Fatalf("expected query to be canceled, but succeeded")
	}
	if !strings.Contains(queryErr.Error(), "canceling statement due to user request") && !strings.Contains(queryErr.Error(), "context canceled") {
		t.Errorf("unexpected cancel error message: %v", queryErr)
	}

	if len(server.GetActiveQueries()) != 0 {
		t.Errorf("expected 0 active queries after cancel, got %d", len(server.GetActiveQueries()))
	}
}

func TestQueryCancel_KillCommand(t *testing.T) {
	server, cleanup := setupTestServerWithLoopProc(t)
	defer cleanup()

	sessA, err := server.GetOrCreateSession("session-a")
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	spidA := sessA.SPID

	var wg sync.WaitGroup
	wg.Add(1)
	var queryErr error

	go func() {
		defer wg.Done()
		_, queryErr = server.ExecuteSessionSQL("session-a", "EXEC InfiniteSpin;")
	}()

	if !waitForActiveQuery(server, "session-a", 2*time.Second) {
		t.Fatalf("timed out waiting for session-a query to become active")
	}

	// Session B executes KILL <spidA>
	killRes, err := server.ExecuteSessionSQL("session-b", fmt.Sprintf("KILL %d;", spidA))
	if err != nil || !killRes.Success {
		t.Fatalf("KILL command failed: %v", err)
	}

	wg.Wait()

	if queryErr == nil {
		t.Fatalf("expected session A query to be killed, but completed successfully")
	}
	if !strings.Contains(queryErr.Error(), "canceling statement due to user request") {
		t.Errorf("expected cancellation message, got: %v", queryErr)
	}
}

func TestQueryCancel_PgCancelBackend(t *testing.T) {
	server, cleanup := setupTestServerWithLoopProc(t)
	defer cleanup()

	sessA, err := server.GetOrCreateSession("pg-sess-a")
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	pidA := sessA.SPID

	var wg sync.WaitGroup
	wg.Add(1)
	var sessAErr error

	go func() {
		defer wg.Done()
		_, sessAErr = server.ExecuteSessionSQL("pg-sess-a", "EXEC InfiniteSpin;")
	}()

	if !waitForActiveQuery(server, "pg-sess-a", 2*time.Second) {
		t.Fatalf("timed out waiting for pg-sess-a query to become active")
	}

	// Session B cancels Session A via pg_cancel_backend(pid)
	cancelRes, err := server.ExecuteSessionSQL("pg-sess-b", fmt.Sprintf("SELECT pg_cancel_backend(%d) AS canceled;", pidA))
	if err != nil || !cancelRes.Success {
		t.Fatalf("pg_cancel_backend call failed: %v", err)
	}
	if len(cancelRes.Rows) != 1 || cancelRes.Rows[0]["canceled"] != true {
		t.Errorf("expected pg_cancel_backend to return true, got %v", cancelRes.Rows)
	}

	wg.Wait()

	if sessAErr == nil {
		t.Fatalf("expected session A query to be cancelled by pg_cancel_backend")
	}
	if !strings.Contains(sessAErr.Error(), "canceling statement due to user request") {
		t.Errorf("expected cancellation error message, got: %v", sessAErr)
	}
}

func TestQueryCancel_PgTerminateBackend(t *testing.T) {
	server, cleanup := setupTestServerWithLoopProc(t)
	defer cleanup()

	sessA, err := server.GetOrCreateSession("term-sess-a")
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	pidA := sessA.SPID

	var wg sync.WaitGroup
	wg.Add(1)
	var sessAErr error

	go func() {
		defer wg.Done()
		_, sessAErr = server.ExecuteSessionSQL("term-sess-a", "EXEC InfiniteSpin;")
	}()

	if !waitForActiveQuery(server, "term-sess-a", 2*time.Second) {
		t.Fatalf("timed out waiting for term-sess-a query to become active")
	}

	termRes, err := server.ExecuteSessionSQL("term-sess-b", fmt.Sprintf("SELECT pg_terminate_backend(%d) AS terminated;", pidA))
	if err != nil || !termRes.Success {
		t.Fatalf("pg_terminate_backend call failed: %v", err)
	}
	if len(termRes.Rows) != 1 || termRes.Rows[0]["terminated"] != true {
		t.Errorf("expected pg_terminate_backend to return true, got %v", termRes.Rows)
	}

	wg.Wait()

	if sessAErr == nil {
		t.Fatalf("expected session to be terminated")
	}
}

func TestQueryCancel_HTTPEndpoints(t *testing.T) {
	server, cleanup := setupTestServerWithLoopProc(t)
	defer cleanup()

	var wg sync.WaitGroup
	wg.Add(1)
	var queryErr error

	go func() {
		defer wg.Done()
		_, queryErr = server.ExecuteSessionSQL("http-sess", "EXEC InfiniteSpin;")
	}()

	if !waitForActiveQuery(server, "http-sess", 2*time.Second) {
		t.Fatalf("timed out waiting for http-sess query to become active")
	}

	// 1. Verify GET /api/queries/active reports the running query
	reqActive := httptest.NewRequest(http.MethodGet, "/api/queries/active", nil)
	recActive := httptest.NewRecorder()
	server.Handler().ServeHTTP(recActive, reqActive)

	if recActive.Code != http.StatusOK {
		t.Fatalf("GET /api/queries/active failed: code %d", recActive.Code)
	}

	var activeResp struct {
		Success bool                  `json:"success"`
		Queries []api.ActiveQueryInfo `json:"queries"`
		Total   int                   `json:"total"`
	}
	if err := json.Unmarshal(recActive.Body.Bytes(), &activeResp); err != nil {
		t.Fatalf("unmarshal active queries failed: %v", err)
	}
	if activeResp.Total < 1 {
		t.Errorf("expected at least 1 active query, got %d", activeResp.Total)
	}

	// 2. Trigger POST /api/query/cancel
	bodyJSON := `{"session_id": "http-sess"}`
	reqCancel := httptest.NewRequest(http.MethodPost, "/api/query/cancel", bytes.NewBufferString(bodyJSON))
	reqCancel.Header.Set("Content-Type", "application/json")
	recCancel := httptest.NewRecorder()
	server.Handler().ServeHTTP(recCancel, reqCancel)

	if recCancel.Code != http.StatusOK {
		t.Fatalf("POST /api/query/cancel failed: code %d", recCancel.Code)
	}

	var cancelResp map[string]any
	if err := json.Unmarshal(recCancel.Body.Bytes(), &cancelResp); err != nil {
		t.Fatalf("unmarshal cancel response failed: %v", err)
	}
	if cancelResp["canceled"] != true {
		t.Errorf("expected canceled=true in HTTP response, got %v", cancelResp)
	}

	wg.Wait()

	if queryErr == nil {
		t.Fatalf("expected query to be canceled via HTTP API, but succeeded")
	}
	if !strings.Contains(queryErr.Error(), "canceling statement due to user request") {
		t.Errorf("expected cancellation message, got: %v", queryErr)
	}
}

func TestQueryTimeout_SessionQueryTimeout(t *testing.T) {
	server, cleanup := setupTestServerWithLoopProc(t)
	defer cleanup()

	// 1. Configure session query timeout to 100ms
	setRes, err := server.ExecuteSessionSQL("timeout-sess", "SET QUERY_TIMEOUT = 100;")
	if err != nil || !setRes.Success {
		t.Fatalf("SET QUERY_TIMEOUT failed: %v", err)
	}

	start := time.Now()
	_, err = server.ExecuteSessionSQL("timeout-sess", "EXEC InfiniteSpin;")
	dur := time.Since(start)

	if err == nil {
		t.Fatalf("expected query to timeout, but succeeded")
	}
	if !strings.Contains(err.Error(), "canceling statement due to statement timeout") {
		t.Fatalf("expected statement timeout error, got: %v", err)
	}
	if dur > 2*time.Second {
		t.Fatalf("query took too long to timeout: %v", dur)
	}
}

func TestQueryTimeout_StatementTimeout(t *testing.T) {
	server, cleanup := setupTestServerWithLoopProc(t)
	defer cleanup()

	// 2. Configure statement_timeout to '80ms'
	setRes, err := server.ExecuteSessionSQL("pg-timeout-sess", "SET statement_timeout = '80ms';")
	if err != nil || !setRes.Success {
		t.Fatalf("SET statement_timeout failed: %v", err)
	}

	start := time.Now()
	_, err = server.ExecuteSessionSQL("pg-timeout-sess", "EXEC InfiniteSpin;")
	dur := time.Since(start)

	if err == nil {
		t.Fatalf("expected query to timeout, but succeeded")
	}
	if !strings.Contains(err.Error(), "canceling statement due to statement timeout") {
		t.Fatalf("expected statement timeout error, got: %v", err)
	}
	if dur > 2*time.Second {
		t.Fatalf("query took too long to timeout: %v", dur)
	}
}

func TestQueryTimeout_DisableZero(t *testing.T) {
	server, cleanup := setupTestServerWithLoopProc(t)
	defer cleanup()

	sessID := "disable-sess"
	_, err := server.ExecuteSessionSQL(sessID, "SET QUERY_TIMEOUT = 100;")
	if err != nil {
		t.Fatalf("set timeout failed: %v", err)
	}

	// Disable timeout
	_, err = server.ExecuteSessionSQL(sessID, "SET QUERY_TIMEOUT = 0;")
	if err != nil {
		t.Fatalf("disable timeout failed: %v", err)
	}

	// Fast query should execute cleanly
	res, err := server.ExecuteSessionSQL(sessID, "SELECT 1 AS num;")
	if err != nil || !res.Success {
		t.Fatalf("SELECT failed: %v", err)
	}
}

func TestQueryTimeout_HTTPPayload(t *testing.T) {
	server, cleanup := setupTestServerWithLoopProc(t)
	defer cleanup()

	bodyJSON := `{"query": "EXEC InfiniteSpin;", "timeout_ms": 100}`
	req := httptest.NewRequest(http.MethodPost, "/api/sql", bytes.NewBufferString(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	start := time.Now()
	server.Handler().ServeHTTP(rec, req)
	dur := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}

	if resp.Success {
		t.Fatalf("expected query to fail due to timeout, got success")
	}
	if !strings.Contains(resp.Error, "canceling statement due to statement timeout") {
		t.Fatalf("expected timeout error message, got: %q", resp.Error)
	}
	if dur > 2*time.Second {
		t.Fatalf("HTTP query took too long to timeout: %v", dur)
	}
}

func TestQueryTimeout_HTTPHeader(t *testing.T) {
	server, cleanup := setupTestServerWithLoopProc(t)
	defer cleanup()

	bodyJSON := `{"query": "EXEC InfiniteSpin;"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sql", bytes.NewBufferString(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Query-Timeout", "100ms")
	rec := httptest.NewRecorder()

	start := time.Now()
	server.Handler().ServeHTTP(rec, req)
	dur := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}

	if resp.Success {
		t.Fatalf("expected query to fail due to timeout, got success")
	}
	if !strings.Contains(resp.Error, "canceling statement due to statement timeout") {
		t.Fatalf("expected timeout error message, got: %q", resp.Error)
	}
	if dur > 2*time.Second {
		t.Fatalf("HTTP query took too long to timeout: %v", dur)
	}
}

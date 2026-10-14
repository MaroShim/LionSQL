package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"sqlion/pkg/api"
	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
)

func TestMetrics_PrometheusExposition(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	// Execute several distinct queries
	_, err := server.ExecuteSQL("CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(50));")
	if err != nil {
		t.Fatalf("create table failed: %v", err)
	}

	_, err = server.ExecuteSQL("INSERT INTO users VALUES (1, 'Alice'), (2, 'Bob');")
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	_, err = server.ExecuteSQL("SELECT * FROM users;")
	if err != nil {
		t.Fatalf("select failed: %v", err)
	}

	// Deliberate error query to verify error counter
	_, _ = server.ExecuteSQL("SELECT * FROM non_existent_table;")

	// Request GET /metrics
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 for /metrics, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/plain") || !strings.Contains(contentType, "version=0.0.4") {
		t.Errorf("unexpected content type for /metrics: %s", contentType)
	}

	body := rec.Body.String()

	// Verify required Prometheus metrics
	requiredMetrics := []string{
		"sqlion_uptime_seconds",
		"sqlion_active_connections",
		"sqlion_queries_total",
		"sqlion_queries_total{command=\"CREATE TABLE\"}",
		"sqlion_queries_total{command=\"INSERT\"}",
		"sqlion_queries_total{command=\"SELECT\"}",
		"sqlion_query_errors_total 1",
		"sqlion_query_duration_seconds_total",
		"sqlion_slow_queries_total",
		"sqlion_transactions_total{type=\"commit\"}",
		"sqlion_transactions_total{type=\"rollback\"}",
		"sqlion_tables_total 1",
		"sqlion_plan_cache_hits_total",
		"sqlion_plan_cache_misses_total",
		"sqlion_memory_alloc_bytes",
		"sqlion_memory_sys_bytes",
		"sqlion_goroutines",
	}

	for _, m := range requiredMetrics {
		if !strings.Contains(body, m) {
			t.Errorf("expected metric %q in /metrics output, but not found.\nFull output:\n%s", m, body)
		}
	}
}

func TestMetrics_TransactionCommitAndRollback(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	_, _ = server.ExecuteSQL("CREATE TABLE accounts (id INT PRIMARY KEY, balance INT);")

	// 1. Transaction Commit
	_, _ = server.ExecuteSQL("BEGIN TRANSACTION;")
	_, _ = server.ExecuteSQL("INSERT INTO accounts VALUES (1, 100);")
	_, err := server.ExecuteSQL("COMMIT;")
	if err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// 2. Transaction Rollback
	_, _ = server.ExecuteSQL("BEGIN TRANSACTION;")
	_, _ = server.ExecuteSQL("INSERT INTO accounts VALUES (2, 200);")
	_, err = server.ExecuteSQL("ROLLBACK;")
	if err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "sqlion_transactions_total{type=\"commit\"} 1") {
		t.Errorf("expected commit count 1 in /metrics, got:\n%s", body)
	}
	if !strings.Contains(body, "sqlion_transactions_total{type=\"rollback\"} 1") {
		t.Errorf("expected rollback count 1 in /metrics, got:\n%s", body)
	}
}

func TestSlowQuery_LoggerAndEndpoints(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	// Set threshold to 0 to capture every query as a slow query
	server.SetSlowQueryThreshold(1 * time.Nanosecond)

	_, _ = server.ExecuteSQL("CREATE TABLE test_slow (id INT PRIMARY KEY, val VARCHAR(50));")
	_, _ = server.ExecuteSQL("INSERT INTO test_slow VALUES (1, 'slow query test');")

	// 1. GET /api/slow-queries
	req := httptest.NewRequest(http.MethodGet, "/api/slow-queries", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		ThresholdMS float64              `json:"threshold_ms"`
		Count       int                  `json:"count"`
		Entries     []api.SlowQueryEntry `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal slow queries failed: %v", err)
	}

	if res.Count < 2 {
		t.Errorf("expected at least 2 slow query entries, got %d", res.Count)
	}
	if len(res.Entries) == 0 || !strings.Contains(res.Entries[0].Query, "test_slow") {
		t.Errorf("unexpected newest slow query entry: %+v", res.Entries)
	}

	// 2. POST /api/slow-queries/threshold
	threshBody := bytes.NewBufferString(`{"threshold_ms": 150.0}`)
	req = httptest.NewRequest(http.MethodPost, "/api/slow-queries/threshold", threshBody)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 for threshold update, got %d", rec.Code)
	}
	if server.SlowQueries().Threshold() != 150*time.Millisecond {
		t.Errorf("expected threshold 150ms, got %v", server.SlowQueries().Threshold())
	}

	// 3. DELETE /api/slow-queries
	req = httptest.NewRequest(http.MethodDelete, "/api/slow-queries", nil)
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 for clear, got %d", rec.Code)
	}
	entries := server.SlowQueries().GetEntries(10)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after clear, got %d", len(entries))
	}
}

func TestSlowQuery_RingBufferCapacity(t *testing.T) {
	logger := api.NewSlowQueryLogger(1 * time.Nanosecond)

	// Record 250 entries into bounded buffer (max capacity 200)
	for i := 1; i <= 250; i++ {
		logger.Record("sess", fmt.Sprintf("SELECT %d", i), 10*time.Millisecond, 1, nil)
	}

	entries := logger.GetEntries(300)
	if len(entries) != api.MaxSlowQueryCapacity {
		t.Fatalf("expected exactly %d entries, got %d", api.MaxSlowQueryCapacity, len(entries))
	}

	// Newest entry should be SELECT 250
	if entries[0].Query != "SELECT 250" {
		t.Errorf("expected newest entry 'SELECT 250', got %q", entries[0].Query)
	}
	// Oldest entry should be SELECT 51 (250 - 200 + 1)
	if entries[len(entries)-1].Query != "SELECT 51" {
		t.Errorf("expected oldest entry 'SELECT 51', got %q", entries[len(entries)-1].Query)
	}
}

func TestMetrics_ConcurrentAccess(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	_, _ = server.ExecuteSQL("CREATE TABLE conc (id INT PRIMARY KEY);")

	var wg sync.WaitGroup
	workers := 10
	iterations := 20

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			sessID := fmt.Sprintf("worker-%d", workerID)
			for i := 0; i < iterations; i++ {
				_, _ = server.ExecuteSessionSQL(sessID, fmt.Sprintf("INSERT INTO conc VALUES (%d);", workerID*1000+i))

				// Periodically scrape /metrics
				if i%5 == 0 {
					req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
					rec := httptest.NewRecorder()
					server.ServeHTTP(rec, req)
				}
				// Periodically query /api/slow-queries
				if i%5 == 0 {
					req := httptest.NewRequest(http.MethodGet, "/api/slow-queries", nil)
					rec := httptest.NewRecorder()
					server.ServeHTTP(rec, req)
				}
			}
		}(w)
	}

	wg.Wait()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	expectedInserts := fmt.Sprintf("sqlion_queries_total{command=\"INSERT\"} %d", workers*iterations)
	if !strings.Contains(body, expectedInserts) {
		t.Errorf("expected %q in /metrics, got:\n%s", expectedInserts, body)
	}
}

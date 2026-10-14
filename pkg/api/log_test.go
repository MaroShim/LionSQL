package api_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"sqlion/pkg/api"
	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
)

func TestSlowQuery_FileLoggingAndRotation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sqlion_slowlog_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	logPath := filepath.Join(tempDir, "slow.log")
	logger := api.NewSlowQueryLogger(1 * time.Nanosecond)

	if err := logger.SetLogFile(logPath); err != nil {
		t.Fatalf("failed to set log file: %v", err)
	}
	defer func() { _ = logger.Close() }()

	if logger.LogFilePath() != logPath {
		t.Errorf("expected log file %s, got %s", logPath, logger.LogFilePath())
	}

	// Record entries
	for i := 1; i <= 5; i++ {
		logger.Record("sess-1", fmt.Sprintf("SELECT %d", i), 10*time.Millisecond, int64(i), nil)
	}

	// Verify file contents (JSON lines)
	f, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("failed to open log file: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		line := scanner.Text()
		var entry api.SlowQueryEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("line %d is not valid JSON (%v): %s", count+1, err, line)
		}
		count++
		if entry.SessionID != "sess-1" {
			t.Errorf("expected session_id 'sess-1', got %q", entry.SessionID)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}
	if count != 5 {
		t.Fatalf("expected 5 lines in log file, got %d", count)
	}
}

func TestRotatingLogWriter_SizeRotation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sqlion_rotate_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	logPath := filepath.Join(tempDir, "test.log")
	// Set very small max bytes to trigger rotation quickly (e.g. 150 bytes)
	writer, err := api.NewRotatingLogWriter(logPath, 150)
	if err != nil {
		t.Fatalf("failed to create writer: %v", err)
	}
	defer func() { _ = writer.Close() }()

	// Write several lines
	for i := 0; i < 10; i++ {
		entry := map[string]any{"id": i, "message": fmt.Sprintf("Log entry payload number %d", i)}
		if err := writer.WriteLine(entry); err != nil {
			t.Fatalf("write line failed: %v", err)
		}
	}

	// Check that rotated backup .1 exists
	backupPath := logPath + ".1"
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Errorf("expected rotated backup file %s to exist", backupPath)
	}

	// Verify main file exists and is valid JSON
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read active log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 {
		t.Fatalf("expected non-empty active log")
	}
	var testObj map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &testObj); err != nil {
		t.Fatalf("active log line is not valid JSON: %s", lines[0])
	}
}

func TestGeneralLogger_LifecycleAndEndpoints(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sqlion_general_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	genLogPath := filepath.Join(tempDir, "general.log")
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)
	defer func() { _ = server.Close() }()

	// 1. Initially disabled
	if server.GeneralLog().IsEnabled() {
		t.Errorf("general log should be disabled by default")
	}

	// Enable general log and set log file
	server.SetGeneralLogEnabled(true)
	if err := server.SetGeneralLogFile(genLogPath); err != nil {
		t.Fatalf("failed to set general log file: %v", err)
	}

	// Execute queries
	_, err = server.ExecuteSQL("CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	_, err = server.ExecuteSQL("INSERT INTO users VALUES (1, 'Alice');")
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}
	_, err = server.ExecuteSQL("SELECT * FROM users;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}

	// Verify in-memory buffer
	entries := server.GeneralLog().GetEntries(10)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries in general log, got %d", len(entries))
	}
	if !strings.Contains(entries[0].Query, "SELECT") {
		t.Errorf("expected newest entry to be SELECT, got %s", entries[0].Query)
	}

	// Verify HTTP GET /api/general-queries
	req := httptest.NewRequest(http.MethodGet, "/api/general-queries", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	var resp struct {
		Enabled bool                  `json:"enabled"`
		Count   int                   `json:"count"`
		LogFile string                `json:"log_file"`
		Entries []api.GeneralLogEntry `json:"entries"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Enabled || resp.Count != 3 || resp.LogFile != genLogPath {
		t.Errorf("unexpected GET response: %+v", resp)
	}

	// Verify HTTP POST /api/general-queries/toggle
	toggleBody := bytes.NewBufferString(`{"enabled": false}`)
	req = httptest.NewRequest(http.MethodPost, "/api/general-queries/toggle", toggleBody)
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || server.GeneralLog().IsEnabled() {
		t.Errorf("failed to toggle general log to false")
	}

	// Execute another query while disabled
	_, _ = server.ExecuteSQL("SELECT 1;")
	if len(server.GeneralLog().GetEntries(10)) != 3 {
		t.Errorf("query should not be logged while disabled")
	}

	// Verify HTTP DELETE /api/general-queries
	req = httptest.NewRequest(http.MethodDelete, "/api/general-queries", nil)
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK for delete, got %d", rec.Code)
	}
	if len(server.GeneralLog().GetEntries(10)) != 0 {
		t.Errorf("expected 0 entries after clear")
	}

	// Verify file on disk has 3 JSON lines
	data, err := os.ReadFile(genLogPath)
	if err != nil {
		t.Fatalf("failed to read general log file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines in disk file, got %d", len(lines))
	}
}

func TestSlowAndGeneralLog_ConcurrentAccess(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sqlion_log_concurrent_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	slowPath := filepath.Join(tempDir, "slow.log")
	genPath := filepath.Join(tempDir, "gen.log")

	slowLogger := api.NewSlowQueryLogger(1 * time.Nanosecond)
	_ = slowLogger.SetLogFile(slowPath)
	defer func() { _ = slowLogger.Close() }()

	genLogger := api.NewGeneralLogger(true)
	_ = genLogger.SetLogFile(genPath)
	defer func() { _ = genLogger.Close() }()

	var wg sync.WaitGroup
	workers := 10
	iterations := 50

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				q := fmt.Sprintf("SELECT %d, %d", workerID, i)
				slowLogger.Record(fmt.Sprintf("sess-%d", workerID), q, 5*time.Millisecond, 1, nil)
				genLogger.Record(fmt.Sprintf("sess-%d", workerID), q, 5*time.Millisecond, 1, nil)
				_ = slowLogger.GetEntries(5)
				_ = genLogger.GetEntries(5)
			}
		}(w)
	}

	wg.Wait()

	slowEntries := slowLogger.GetEntries(1000)
	genEntries := genLogger.GetEntries(1000)
	if len(slowEntries) == 0 || len(genEntries) == 0 {
		t.Errorf("expected non-empty entries from concurrent recording")
	}
}

package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
)

func TestAPI_BackupAndRestoreStream(t *testing.T) {
	srv := setupTestServer()

	// 1. Seed database
	seedReq := httptest.NewRequest(http.MethodPost, "/api/seed", nil)
	seedRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(seedRec, seedReq)
	if seedRec.Code != http.StatusOK {
		t.Fatalf("Seed failed: %s", seedRec.Body.String())
	}

	// 2. Download live snapshot backup via GET /api/backup
	backupReq := httptest.NewRequest(http.MethodGet, "/api/backup", nil)
	backupRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(backupRec, backupReq)

	if backupRec.Code != http.StatusOK {
		t.Fatalf("GET /api/backup failed with code %d: %s", backupRec.Code, backupRec.Body.String())
	}
	if ct := backupRec.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("expected Content-Type application/gzip, got %s", ct)
	}
	if cd := backupRec.Header().Get("Content-Disposition"); !strings.Contains(cd, "sqlion-backup-") {
		t.Errorf("unexpected Content-Disposition: %s", cd)
	}

	backupBytes := backupRec.Body.Bytes()
	if len(backupBytes) == 0 {
		t.Fatalf("backup stream is empty")
	}

	// 3. Set up a fresh server instance
	freshEng := memengine.New()
	_ = freshEng.Open("")
	freshCat := catalog.New(freshEng)
	freshSrv := NewServer(freshCat, freshEng)

	// 4. Restore via multipart POST /api/restore
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("backup", "snapshot.tar.gz")
	if err != nil {
		t.Fatalf("failed to create multipart form file: %v", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(backupBytes)); err != nil {
		t.Fatalf("failed to copy backup to multipart form: %v", err)
	}
	_ = writer.Close()

	restoreReq := httptest.NewRequest(http.MethodPost, "/api/restore", body)
	restoreReq.Header.Set("Content-Type", writer.FormDataContentType())
	restoreRec := httptest.NewRecorder()
	freshSrv.Handler().ServeHTTP(restoreRec, restoreReq)

	if restoreRec.Code != http.StatusOK {
		t.Fatalf("POST /api/restore failed with code %d: %s", restoreRec.Code, restoreRec.Body.String())
	}

	var restoreResp map[string]any
	if err := json.Unmarshal(restoreRec.Body.Bytes(), &restoreResp); err != nil {
		t.Fatalf("failed to parse restore response: %v", err)
	}
	if restoreResp["status"] != "success" {
		t.Errorf("expected status success, got %v", restoreResp["status"])
	}

	// 5. Verify restored data on fresh server
	recordsReq := httptest.NewRequest(http.MethodGet, "/api/tables/accounts/records", nil)
	recordsRec := httptest.NewRecorder()
	freshSrv.Handler().ServeHTTP(recordsRec, recordsReq)

	if recordsRec.Code != http.StatusOK {
		t.Fatalf("failed to get records after restore: %s", recordsRec.Body.String())
	}
	var res map[string]any
	_ = json.Unmarshal(recordsRec.Body.Bytes(), &res)
	recordsList, ok := res["records"].([]any)
	if !ok || len(recordsList) == 0 {
		t.Fatalf("expected restored records, got 0 or invalid format")
	}
}

func TestAPI_BackupFileAndRestoreFilePath(t *testing.T) {
	srv := setupTestServer()

	// Seed database
	seedReq := httptest.NewRequest(http.MethodPost, "/api/seed", nil)
	seedRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(seedRec, seedReq)
	if seedRec.Code != http.StatusOK {
		t.Fatalf("Seed failed: %s", seedRec.Body.String())
	}

	tmpDir, err := os.MkdirTemp("", "sqlion-api-backup-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	targetPath := filepath.Join(tmpDir, "server-backup.tar.gz")

	// 1. Trigger POST /api/backup with JSON target
	postBody, _ := json.Marshal(map[string]string{"target": targetPath})
	postReq := httptest.NewRequest(http.MethodPost, "/api/backup", bytes.NewReader(postBody))
	postReq.Header.Set("Content-Type", "application/json")
	postRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(postRec, postReq)

	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/backup failed: %s", postRec.Body.String())
	}

	if _, err := os.Stat(targetPath); err != nil {
		t.Fatalf("target backup file was not created: %v", err)
	}

	// 2. Set up fresh target server
	freshEng := memengine.New()
	_ = freshEng.Open("")
	freshCat := catalog.New(freshEng)
	freshSrv := NewServer(freshCat, freshEng)

	// 3. Restore via POST /api/restore referencing the file path
	restoreBody, _ := json.Marshal(map[string]string{"source": targetPath})
	restoreReq := httptest.NewRequest(http.MethodPost, "/api/restore", bytes.NewReader(restoreBody))
	restoreReq.Header.Set("Content-Type", "application/json")
	restoreRec := httptest.NewRecorder()
	freshSrv.Handler().ServeHTTP(restoreRec, restoreReq)

	if restoreRec.Code != http.StatusOK {
		t.Fatalf("POST /api/restore failed: %s", restoreRec.Body.String())
	}

	// 4. Verify tables exist
	tablesReq := httptest.NewRequest(http.MethodGet, "/api/tables", nil)
	tablesRec := httptest.NewRecorder()
	freshSrv.Handler().ServeHTTP(tablesRec, tablesReq)

	var tables []map[string]any
	_ = json.Unmarshal(tablesRec.Body.Bytes(), &tables)
	if len(tables) == 0 {
		t.Fatalf("expected tables on restored server, got 0")
	}
}

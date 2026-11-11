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

	"lionsql/pkg/auth"
	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
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
	if cd := backupRec.Header().Get("Content-Disposition"); !strings.Contains(cd, "lionsql-backup-") {
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

	tmpDir, err := os.MkdirTemp("", "lionsql-api-backup-*")
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

func TestAPI_Backup_RequiresSuperuser_AuditFull037(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)

	authSvc := auth.NewAuthService(auth.Config{})
	_, _ = authSvc.EnsureDefaultSuperuser("admin@lionsql.local", "SuperMasterSecret123!")

	// 1. Create a table-restricted API key (allowed_tables: ["guest_book"] only)
	keyObj, err := authSvc.CreateAPIKey("ThirdPartyIntegrationKey", auth.RoleReadOnly, []string{"guest_book"})
	if err != nil {
		t.Fatalf("failed to create API key: %v", err)
	}

	server := NewServerWithAuth(cat, eng, authSvc, true) // enableAuth = true

	// 2. Unauthenticated GET /api/backup -> 401 Unauthorized
	req1 := httptest.NewRequest(http.MethodGet, "/api/backup", nil)
	rec1 := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for unauthenticated GET /api/backup, got %d: %s", rec1.Code, rec1.Body.String())
	}

	// 3. AUDIT-FULL-037 Test: Attempt privilege escalation via table-restricted API key
	req2 := httptest.NewRequest(http.MethodGet, "/api/backup", nil)
	req2.Header.Set("X-API-Key", keyObj.Key)
	rec2 := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec2, req2)

	// MUST be rejected because /api/backup requires Superuser JWT, not a table-scoped API key!
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("AUDIT-FULL-037: privilege escalation detected! Table-scoped API key was allowed to access /api/backup (code %d)", rec2.Code)
	}

	// 4. Same test for POST /api/backup with API key
	postBody, _ := json.Marshal(map[string]string{"target": "/tmp/evil_backup.tar.gz"})
	req3 := httptest.NewRequest(http.MethodPost, "/api/backup", bytes.NewReader(postBody))
	req3.Header.Set("X-API-Key", keyObj.Key)
	req3.Header.Set("Content-Type", "application/json")
	rec3 := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusUnauthorized {
		t.Fatalf("AUDIT-FULL-037: privilege escalation detected on POST /api/backup with API key (code %d)", rec3.Code)
	}

	// 5. Login as Superuser and get valid JWT Bearer token
	loginPayload, _ := json.Marshal(map[string]string{
		"identity": "admin@lionsql.local",
		"password": "SuperMasterSecret123!",
	})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginPayload))
	loginRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusOK {
		t.Fatalf("failed to login superuser: %d: %s", loginRec.Code, loginRec.Body.String())
	}
	var authRes struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(loginRec.Body.Bytes(), &authRes)
	if authRes.Token == "" {
		t.Fatalf("expected valid token from login")
	}

	// 6. Superuser with Bearer token can successfully access GET /api/backup
	req4 := httptest.NewRequest(http.MethodGet, "/api/backup", nil)
	req4.Header.Set("Authorization", "Bearer "+authRes.Token)
	rec4 := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec4, req4)

	if rec4.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for superuser GET /api/backup, got %d: %s", rec4.Code, rec4.Body.String())
	}
}

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
)

func setupTestServer() *Server {
	eng := memengine.New()
	_ = eng.Open("")
	cat := catalog.New(eng)
	return NewServer(cat, eng)
}

func TestAPI_HealthAndSeed(t *testing.T) {
	srv := setupTestServer()

	// 1. Health check
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Health check failed with code %d: %s", rec.Code, rec.Body.String())
	}

	// 2. Seed data
	req = httptest.NewRequest(http.MethodPost, "/api/seed", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Seed failed with code %d: %s", rec.Code, rec.Body.String())
	}

	// 3. List tables
	req = httptest.NewRequest(http.MethodGet, "/api/tables", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("List tables failed with code %d: %s", rec.Code, rec.Body.String())
	}
	var tables []map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&tables)
	if len(tables) == 0 {
		t.Fatalf("Expected seeded tables, got 0")
	}

	// 4. Get records
	req = httptest.NewRequest(http.MethodGet, "/api/tables/accounts/records", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Get records failed with code %d: %s", rec.Code, rec.Body.String())
	}
	var recsResp struct {
		Records []map[string]any `json:"records"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&recsResp)
	if len(recsResp.Records) != 2 {
		t.Fatalf("Expected 2 records, got %d", len(recsResp.Records))
	}
}

func TestAPI_RegisterAndExecProcedure(t *testing.T) {
	srv := setupTestServer()

	// Seed accounts table
	req := httptest.NewRequest(http.MethodPost, "/api/seed", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	// Register procedure
	script := `
CREATE PROCEDURE TransferFunds
    @from INT,
    @to INT,
    @amount INT
AS
BEGIN
    DECLARE @sender_bal INT;
    SELECT balance INTO @sender_bal FROM accounts WHERE id = @from;

    IF @sender_bal < @amount
    BEGIN
        RAISERROR('Insufficient funds');
        RETURN -1;
    END

    UPDATE accounts SET balance = balance - @amount WHERE id = @from;
    UPDATE accounts SET balance = balance + @amount WHERE id = @to;
    PRINT 'Money transferred';
    RETURN 0;
END
`

	regBody, _ := json.Marshal(map[string]string{"script": script})
	req = httptest.NewRequest(http.MethodPost, "/api/procs", bytes.NewReader(regBody))
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("Register proc failed with code %d: %s", rec.Code, rec.Body.String())
	}

	var regResp struct {
		Name   string `json:"name"`
		JSCode string `json:"js_code"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&regResp)
	if !strings.Contains(regResp.JSCode, "TransferFunds") || !strings.Contains(regResp.JSCode, "Instructions") {
		t.Fatalf("Expected bytecode disassembly in registration response, got: %s", regResp.JSCode)
	}

	// Execute procedure via JSON HTTP RPC
	execBody, _ := json.Marshal(map[string]any{
		"args": map[string]any{
			"from":   1,
			"to":     2,
			"amount": 250,
		},
	})
	req = httptest.NewRequest(http.MethodPost, "/api/procs/TransferFunds/exec", bytes.NewReader(execBody))
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Exec proc failed with code %d: %s", rec.Code, rec.Body.String())
	}

	var execResp struct {
		Success    bool     `json:"success"`
		ReturnCode int      `json:"return_code"`
		Logs       []string `json:"logs"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&execResp)
	if !execResp.Success || execResp.ReturnCode != 0 {
		t.Fatalf("Expected success with code 0, got: %+v", execResp)
	}
	if len(execResp.Logs) == 0 || execResp.Logs[0] != "Money transferred" {
		t.Fatalf("Expected log 'Money transferred', got %v", execResp.Logs)
	}

	// Verify balance update
	req = httptest.NewRequest(http.MethodGet, "/api/tables/accounts/records", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var recsResp struct {
		Records []map[string]any `json:"records"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&recsResp)

	for _, r := range recsResp.Records {
		id := int(r["id"].(float64))
		bal := int(r["balance"].(float64))
		if id == 1 && bal != 750 {
			t.Errorf("Alice balance expected 750, got %d", bal)
		}
		if id == 2 && bal != 750 {
			t.Errorf("Bob balance expected 750, got %d", bal)
		}
	}
}

func TestAPI_RegisterTSqlProcedureWithCalculations(t *testing.T) {
	srv := setupTestServer()

	tsqlScript := `CREATE PROCEDURE AddBonus
    @accId INT,
    @bonus INT
AS
BEGIN
    UPDATE accounts SET balance = balance + @bonus WHERE id = @accId;
    RETURN 0;
END;`

	regBody, _ := json.Marshal(map[string]string{"script": tsqlScript})
	req := httptest.NewRequest(http.MethodPost, "/api/procs", bytes.NewReader(regBody))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("Register procedure failed: %d (%s)", rec.Code, rec.Body.String())
	}

	var regResp struct {
		Name   string `json:"name"`
		JSCode string `json:"js_code"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&regResp)
	if regResp.Name != "AddBonus" {
		t.Errorf("Expected proc name AddBonus, got %s", regResp.Name)
	}
	if !strings.Contains(regResp.JSCode, "AddBonus") || !strings.Contains(regResp.JSCode, "UPDATE") {
		t.Errorf("Expected disassembly to contain AddBonus and UPDATE, got: %s", regResp.JSCode)
	}
}

func TestAPI_AuthWorkflow(t *testing.T) {
	srv := setupTestServer()
	srv.SetEnableAuth(true)

	// 1. Unauthorized request should fail with 401
	req := httptest.NewRequest(http.MethodGet, "/api/tables", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 Unauthorized, got %d", rec.Code)
	}

	// 2. Health check remains open without token
	req = httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for health, got %d", rec.Code)
	}

	// 3. Login with wrong password should fail with 401
	badLoginPayload, _ := json.Marshal(map[string]string{
		"email":    "admin@sqlion.local",
		"password": "wrongpassword",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(badLoginPayload))
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 Unauthorized for bad password, got %d", rec.Code)
	}

	// 4. Login with default credentials
	loginPayload, _ := json.Marshal(map[string]string{
		"email":    "admin@sqlion.local",
		"password": "1234567890",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginPayload))
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for valid login, got %d: %s", rec.Code, rec.Body.String())
	}

	var loginResp struct {
		Token  string         `json:"token"`
		Record map[string]any `json:"record"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&loginResp)
	if loginResp.Token == "" {
		t.Fatalf("Expected non-empty token")
	}
	if loginResp.Record["email"] != "admin@sqlion.local" {
		t.Fatalf("Expected email admin@sqlion.local, got %v", loginResp.Record["email"])
	}

	// 5. Access protected endpoint with Bearer token
	req = httptest.NewRequest(http.MethodGet, "/api/tables", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.Token)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK with Bearer token, got %d: %s", rec.Code, rec.Body.String())
	}

	// 6. Check /api/auth/me
	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.Token)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for /api/auth/me, got %d: %s", rec.Code, rec.Body.String())
	}

	// 7. Refresh token
	req = httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.Token)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for refresh, got %d: %s", rec.Code, rec.Body.String())
	}
	var refreshResp struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&refreshResp)
	if refreshResp.Token == "" {
		t.Fatalf("Expected fresh token")
	}

	// 8. Logout (Revoke All Sessions)
	req = httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+refreshResp.Token)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for logout, got %d: %s", rec.Code, rec.Body.String())
	}

	// 9. Now both previous tokens must be rejected
	req = httptest.NewRequest(http.MethodGet, "/api/tables", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.Token)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 after logout, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/tables", nil)
	req.Header.Set("Authorization", "Bearer "+refreshResp.Token)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 after logout for refreshed token, got %d", rec.Code)
	}
}

func TestAPI_APIKeysWorkflow(t *testing.T) {
	srv := setupTestServer()
	srv.SetEnableAuth(true)

	// 1. Login as Superuser
	loginPayload, _ := json.Marshal(map[string]string{
		"email":    "admin@sqlion.local",
		"password": "1234567890",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginPayload))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var loginResp struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&loginResp)
	superToken := loginResp.Token

	// Seed tables with superuser token
	req = httptest.NewRequest(http.MethodPost, "/api/seed", nil)
	req.Header.Set("Authorization", "Bearer "+superToken)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Seed failed with code %d: %s", rec.Code, rec.Body.String())
	}

	// 2. Superuser creates a Read-Only API Key restricted to "accounts"
	keyReq, _ := json.Marshal(map[string]any{
		"name":           "frontend-widget",
		"role":           "read_only",
		"allowed_tables": []string{"accounts"},
	})
	req = httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewReader(keyReq))
	req.Header.Set("Authorization", "Bearer "+superToken)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("Create API key failed: %d (%s)", rec.Code, rec.Body.String())
	}

	var createdKey struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&createdKey)
	if createdKey.Key == "" || !strings.HasPrefix(createdKey.Key, "sqlion_") {
		t.Fatalf("Expected valid plaintext key, got: %s", createdKey.Key)
	}

	// 3. Unauthorized API Key request fails
	req = httptest.NewRequest(http.MethodGet, "/api/tables/accounts/records", nil)
	req.Header.Set("X-API-Key", "sqlion_invalidsecretkey12345")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 for invalid api key, got %d", rec.Code)
	}

	// 4. Valid API Key reads permitted table
	req = httptest.NewRequest(http.MethodGet, "/api/tables/accounts/records", nil)
	req.Header.Set("X-API-Key", createdKey.Key)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for permitted table read, got %d: %s", rec.Code, rec.Body.String())
	}

	// 5. Valid API Key denied on non-permitted table
	req = httptest.NewRequest(http.MethodGet, "/api/tables/unpermitted_table/records", nil)
	req.Header.Set("X-API-Key", createdKey.Key)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Expected 403 Forbidden for non-allowed table, got %d", rec.Code)
	}

	// 6. Valid API Key with Authorization: ApiKey <key> header
	req = httptest.NewRequest(http.MethodGet, "/api/tables/accounts/schema", nil)
	req.Header.Set("Authorization", "ApiKey "+createdKey.Key)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK with ApiKey auth header, got %d", rec.Code)
	}

	// 7. Revoke API Key
	req = httptest.NewRequest(http.MethodDelete, "/api/keys/"+createdKey.ID, nil)
	req.Header.Set("Authorization", "Bearer "+superToken)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for key revocation, got %d", rec.Code)
	}

	// 8. Key no longer works
	req = httptest.NewRequest(http.MethodGet, "/api/tables/accounts/records", nil)
	req.Header.Set("X-API-Key", createdKey.Key)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 after key revocation, got %d", rec.Code)
	}
}


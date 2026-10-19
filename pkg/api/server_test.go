package api

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	var healthResp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &healthResp); err != nil {
		t.Fatalf("Failed to decode health check response: %v", err)
	}
	if healthResp["engine"] != "In-Memory OCC" {
		t.Fatalf("Expected engine 'In-Memory OCC', got '%s'", healthResp["engine"])
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
		Name           string `json:"name"`
		BytecodeDisasm string `json:"bytecode_disasm"`
		JSCode         string `json:"js_code"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&regResp)
	if !strings.Contains(regResp.BytecodeDisasm, "TransferFunds") || !strings.Contains(regResp.BytecodeDisasm, "Instructions") {
		t.Fatalf("Expected bytecode disassembly in registration response, got: %s", regResp.BytecodeDisasm)
	}
	if regResp.JSCode != regResp.BytecodeDisasm {
		t.Fatalf("Expected js_code backward compatibility alias to match bytecode_disasm")
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
		"email":    "admin@lionsql.local",
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
		"email":    "admin@lionsql.local",
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
	if loginResp.Record["email"] != "admin@lionsql.local" {
		t.Fatalf("Expected email admin@lionsql.local, got %v", loginResp.Record["email"])
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
		"email":    "admin@lionsql.local",
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

func TestAPI_TableAndRecordCRUD(t *testing.T) {
	srv := setupTestServer()

	// 1. Create table via POST /api/tables
	tblPayload, _ := json.Marshal(map[string]any{
		"name": "products",
		"columns": []map[string]any{
			{"name": "id", "type": "INT", "primary_key": true},
			{"name": "title", "type": "VARCHAR", "nullable": false},
			{"name": "price", "type": "FLOAT", "nullable": false},
			{"name": "active", "type": "BOOL", "nullable": true},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/tables", bytes.NewReader(tblPayload))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("Create table failed: %d (%s)", rec.Code, rec.Body.String())
	}

	// 2. Insert record via POST /api/tables/products/records
	rowPayload, _ := json.Marshal(map[string]any{
		"id":     101,
		"title":  "Mechanical Keyboard",
		"price":  149.99,
		"active": true,
	})
	req = httptest.NewRequest(http.MethodPost, "/api/tables/products/records", bytes.NewReader(rowPayload))
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("Create record failed: %d (%s)", rec.Code, rec.Body.String())
	}

	// 3. Update record via PATCH /api/tables/products/records/101
	updatePayload, _ := json.Marshal(map[string]any{
		"price": 129.99,
		"title": "Pro Mechanical Keyboard",
	})
	req = httptest.NewRequest(http.MethodPatch, "/api/tables/products/records/101", bytes.NewReader(updatePayload))
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Update record failed: %d (%s)", rec.Code, rec.Body.String())
	}

	// 4. Verify updated record
	req = httptest.NewRequest(http.MethodGet, "/api/tables/products/records", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var recsResp struct {
		Records []map[string]any `json:"records"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&recsResp)
	if len(recsResp.Records) != 1 {
		t.Fatalf("Expected 1 record, got %d", len(recsResp.Records))
	}
	if recsResp.Records[0]["title"] != "Pro Mechanical Keyboard" {
		t.Fatalf("Expected updated title, got: %v", recsResp.Records[0]["title"])
	}

	// 5. Delete record via DELETE /api/tables/products/records/101
	req = httptest.NewRequest(http.MethodDelete, "/api/tables/products/records/101", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Delete record failed: %d (%s)", rec.Code, rec.Body.String())
	}

	// Verify table empty
	req = httptest.NewRequest(http.MethodGet, "/api/tables/products/records", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	_ = json.NewDecoder(rec.Body).Decode(&recsResp)
	if len(recsResp.Records) != 0 {
		t.Fatalf("Expected 0 records after delete, got %d", len(recsResp.Records))
	}

	// 6. Drop table via DELETE /api/tables/products
	req = httptest.NewRequest(http.MethodDelete, "/api/tables/products", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Drop table failed: %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestAPI_RecordsSearchSortPagination(t *testing.T) {
	srv := setupTestServer()

	// 1. Create table 'items'
	tablePayload, _ := json.Marshal(map[string]any{
		"name": "items",
		"columns": []map[string]any{
			{"name": "id", "type": "INT", "primary_key": true, "nullable": false},
			{"name": "title", "type": "VARCHAR", "primary_key": false, "nullable": false},
			{"name": "priority", "type": "INT", "primary_key": false, "nullable": false},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/tables", bytes.NewReader(tablePayload))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("Create table failed: %d (%s)", rec.Code, rec.Body.String())
	}

	// 2. Insert 5 records
	testItems := []struct {
		id       int
		title    string
		priority int
	}{
		{1, "Apple Pie", 30},
		{2, "Banana Bread", 10},
		{3, "Cherry Tart", 50},
		{4, "Date Cake", 20},
		{5, "Egg Custard", 40},
	}

	for _, item := range testItems {
		payload, _ := json.Marshal(map[string]any{
			"id":       item.id,
			"title":    item.title,
			"priority": item.priority,
		})
		req = httptest.NewRequest(http.MethodPost, "/api/tables/items/records", bytes.NewReader(payload))
		rec = httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("Insert record %d failed: %d (%s)", item.id, rec.Code, rec.Body.String())
		}
	}

	// 3. Test Search
	req = httptest.NewRequest(http.MethodGet, "/api/tables/items/records?search=Pie", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Search failed: %d (%s)", rec.Code, rec.Body.String())
	}
	var searchResp struct {
		TotalItems int              `json:"totalItems"`
		Records    []map[string]any `json:"records"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&searchResp)
	if searchResp.TotalItems != 1 || len(searchResp.Records) != 1 {
		t.Fatalf("Expected 1 match for 'Pie', got %d", searchResp.TotalItems)
	}
	if searchResp.Records[0]["title"] != "Apple Pie" {
		t.Fatalf("Expected title 'Apple Pie', got %v", searchResp.Records[0]["title"])
	}

	// 4. Test Sorting ASC
	req = httptest.NewRequest(http.MethodGet, "/api/tables/items/records?sort=priority", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var sortAscResp struct {
		Records []map[string]any `json:"records"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&sortAscResp)
	if len(sortAscResp.Records) != 5 {
		t.Fatalf("Expected 5 records, got %d", len(sortAscResp.Records))
	}
	// First item should have priority 10 (Banana Bread)
	if fmt.Sprint(sortAscResp.Records[0]["priority"]) != "10" {
		t.Fatalf("Expected lowest priority 10 first, got %v", sortAscResp.Records[0]["priority"])
	}
	// Last item should have priority 50 (Cherry Tart)
	if fmt.Sprint(sortAscResp.Records[4]["priority"]) != "50" {
		t.Fatalf("Expected highest priority 50 last, got %v", sortAscResp.Records[4]["priority"])
	}

	// 5. Test Sorting DESC
	req = httptest.NewRequest(http.MethodGet, "/api/tables/items/records?sort=-priority", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var sortDescResp struct {
		Records []map[string]any `json:"records"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&sortDescResp)
	// First item should have priority 50 (Cherry Tart)
	if fmt.Sprint(sortDescResp.Records[0]["priority"]) != "50" {
		t.Fatalf("Expected highest priority 50 first, got %v", sortDescResp.Records[0]["priority"])
	}

	// 6. Test Pagination
	req = httptest.NewRequest(http.MethodGet, "/api/tables/items/records?page=1&perPage=2&sort=id", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var pageResp struct {
		Page       int              `json:"page"`
		PerPage    int              `json:"perPage"`
		TotalItems int              `json:"totalItems"`
		TotalPages int              `json:"totalPages"`
		Records    []map[string]any `json:"records"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&pageResp)
	if pageResp.Page != 1 || pageResp.PerPage != 2 || pageResp.TotalItems != 5 || pageResp.TotalPages != 3 {
		t.Fatalf("Pagination metadata mismatch: %+v", pageResp)
	}
	if len(pageResp.Records) != 2 {
		t.Fatalf("Expected 2 records on page 1, got %d", len(pageResp.Records))
	}
	if fmt.Sprint(pageResp.Records[0]["id"]) != "1" || fmt.Sprint(pageResp.Records[1]["id"]) != "2" {
		t.Fatalf("Page 1 should contain items 1 and 2, got %v and %v", pageResp.Records[0]["id"], pageResp.Records[1]["id"])
	}

	// Page 3 should contain only item 5
	req = httptest.NewRequest(http.MethodGet, "/api/tables/items/records?page=3&perPage=2&sort=id", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	_ = json.NewDecoder(rec.Body).Decode(&pageResp)
	if len(pageResp.Records) != 1 {
		t.Fatalf("Expected 1 record on page 3, got %d", len(pageResp.Records))
	}
	if fmt.Sprint(pageResp.Records[0]["id"]) != "5" {
		t.Fatalf("Page 3 should contain item 5, got %v", pageResp.Records[0]["id"])
	}
}

func TestAPI_TableExportAndImport(t *testing.T) {
	srv := setupTestServer()

	// 1. Seed accounts
	req := httptest.NewRequest(http.MethodPost, "/api/seed", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Seed failed with code %d: %s", rec.Code, rec.Body.String())
	}

	// 2. Test CSV Export
	req = httptest.NewRequest(http.MethodGet, "/api/tables/accounts/export?format=csv", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Export CSV failed with code %d: %s", rec.Code, rec.Body.String())
	}
	csvBody := rec.Body.String()
	if !strings.Contains(csvBody, "id,holder,balance") || !strings.Contains(csvBody, "Alice") {
		t.Fatalf("Unexpected CSV export output:\n%s", csvBody)
	}

	// 3. Test JSON Export
	req = httptest.NewRequest(http.MethodGet, "/api/tables/accounts/export?format=json", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Export JSON failed with code %d: %s", rec.Code, rec.Body.String())
	}
	var jsonExport []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&jsonExport); err != nil || len(jsonExport) != 2 {
		t.Fatalf("Unexpected JSON export output: %+v (err=%v)", jsonExport, err)
	}

	// 4. Create new table for import
	createPayload := `{"name":"products","columns":[{"name":"id","type":"INT","primary_key":true},{"name":"title","type":"VARCHAR","nullable":false},{"name":"price","type":"FLOAT","nullable":false}]}`
	req = httptest.NewRequest(http.MethodPost, "/api/tables", strings.NewReader(createPayload))
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("Create table failed: %s", rec.Body.String())
	}

	// 5. Test CSV Import
	csvData := `id,title,price
101,Mechanical Keyboard,129.50
102,Wireless Mouse,49.99
103,4K Monitor,399.00`
	req = httptest.NewRequest(http.MethodPost, "/api/tables/products/import", strings.NewReader(csvData))
	req.Header.Set("Content-Type", "text/csv")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Import CSV failed with code %d: %s", rec.Code, rec.Body.String())
	}
	var importResp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&importResp)
	if fmt.Sprint(importResp["imported"]) != "3" {
		t.Fatalf("Expected 3 imported records, got %v", importResp)
	}

	// Verify imported records
	req = httptest.NewRequest(http.MethodGet, "/api/tables/products/records", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var recordsResp struct {
		TotalItems int              `json:"totalItems"`
		Records    []map[string]any `json:"records"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&recordsResp)
	if recordsResp.TotalItems != 3 {
		t.Fatalf("Expected 3 products in database, got %d", recordsResp.TotalItems)
	}

	// 6. Test JSON Batch Import
	jsonBatch := `[{"id": 104, "title": "USB-C Hub", "price": 29.95}]`
	req = httptest.NewRequest(http.MethodPost, "/api/tables/products/import", strings.NewReader(jsonBatch))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Import JSON failed with code %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/tables/products/records", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	_ = json.NewDecoder(rec.Body).Decode(&recordsResp)
	if recordsResp.TotalItems != 4 {
		t.Fatalf("Expected 4 products after batch JSON import, got %d", recordsResp.TotalItems)
	}
}

func TestAPI_ProcedureDuplicateRejection(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	srv := NewServer(cat, eng)

	scriptCreate := `CREATE PROCEDURE TransferCoins
    @from INT,
    @to INT,
    @amount INT
AS
BEGIN
    RETURN 0;
END;`

	// 1. Initial creation succeeds with 201
	body, _ := json.Marshal(map[string]string{"script": scriptCreate})
	req := httptest.NewRequest(http.MethodPost, "/api/procs", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 on first creation, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2. Second creation with exact same name fails with 409 Conflict
	req2 := httptest.NewRequest(http.MethodPost, "/api/procs", bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict on duplicate creation, got %d: %s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), "already exists") {
		t.Errorf("expected 'already exists' in response, got: %s", rec2.Body.String())
	}

	// 3. Second creation with different case (transfercoins) also fails with 409 Conflict
	scriptCase := `CREATE PROCEDURE transfercoins
    @from INT,
    @to INT
AS
BEGIN
    RETURN 0;
END;`
	bodyCase, _ := json.Marshal(map[string]string{"script": scriptCase})
	reqCase := httptest.NewRequest(http.MethodPost, "/api/procs", bytes.NewReader(bodyCase))
	recCase := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recCase, reqCase)
	if recCase.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict on case-different duplicate creation, got %d: %s", recCase.Code, recCase.Body.String())
	}

	// 4. CREATE OR ALTER succeeds with 201
	scriptOrAlter := `CREATE OR ALTER PROCEDURE TransferCoins
    @from INT,
    @to INT,
    @amount INT
AS
BEGIN
    PRINT 'Altered';
    RETURN 1;
END;`
	bodyOrAlter, _ := json.Marshal(map[string]string{"script": scriptOrAlter})
	reqOrAlter := httptest.NewRequest(http.MethodPost, "/api/procs", bytes.NewReader(bodyOrAlter))
	recOrAlter := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recOrAlter, reqOrAlter)
	if recOrAlter.Code != http.StatusCreated {
		t.Fatalf("expected 201 on CREATE OR ALTER, got %d: %s", recOrAlter.Code, recOrAlter.Body.String())
	}

	// 5. ALTER PROCEDURE succeeds with 201
	scriptAlter := `ALTER PROCEDURE TransferCoins
    @from INT,
    @to INT,
    @amount INT
AS
BEGIN
    PRINT 'Altered again';
    RETURN 2;
END;`
	bodyAlter, _ := json.Marshal(map[string]string{"script": scriptAlter})
	reqAlter := httptest.NewRequest(http.MethodPost, "/api/procs", bytes.NewReader(bodyAlter))
	recAlter := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recAlter, reqAlter)
	if recAlter.Code != http.StatusCreated {
		t.Fatalf("expected 201 on ALTER PROCEDURE, got %d: %s", recAlter.Code, recAlter.Body.String())
	}

	// 6. ALTER PROCEDURE on non-existent proc fails with 404 Not Found
	scriptMissing := `ALTER PROCEDURE NonExistentProc AS BEGIN RETURN 0; END;`
	bodyMissing, _ := json.Marshal(map[string]string{"script": scriptMissing})
	reqMissing := httptest.NewRequest(http.MethodPost, "/api/procs", bytes.NewReader(bodyMissing))
	recMissing := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recMissing, reqMissing)
	if recMissing.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on ALTER non-existent, got %d: %s", recMissing.Code, recMissing.Body.String())
	}

	// 7. List procedures should have exactly 1 procedure (TransferCoins)
	reqList := httptest.NewRequest(http.MethodGet, "/api/procs", nil)
	recList := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recList, reqList)
	var list []map[string]any
	_ = json.NewDecoder(recList.Body).Decode(&list)
	if len(list) != 1 {
		t.Fatalf("expected 1 procedure in list, got %d: %+v", len(list), list)
	}
}

func TestAPI_ExecuteSQL_FormatArray(t *testing.T) {
	srv := setupTestServer()

	// Seed data
	reqSeed := httptest.NewRequest(http.MethodPost, "/api/seed", nil)
	recSeed := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recSeed, reqSeed)
	if recSeed.Code != http.StatusOK {
		t.Fatalf("seed failed: %s", recSeed.Body.String())
	}

	query := "SELECT id, holder, balance FROM accounts ORDER BY id ASC"

	// 1. Default envelope format
	bodyDefault, _ := json.Marshal(map[string]string{"sql": query})
	req1 := httptest.NewRequest(http.MethodPost, "/api/sql", bytes.NewReader(bodyDefault))
	rec1 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("default format request failed: %s", rec1.Body.String())
	}
	var envResp QueryResult
	if err := json.Unmarshal(rec1.Body.Bytes(), &envResp); err != nil {
		t.Fatalf("failed to unmarshal envelope response: %v", err)
	}
	if !envResp.Success || len(envResp.Columns) != 3 || len(envResp.Rows) != 2 {
		t.Fatalf("unexpected envelope response: %+v", envResp)
	}

	// 2. format=array via query parameter (?format=array)
	req2 := httptest.NewRequest(http.MethodPost, "/api/sql?format=array", bytes.NewReader(bodyDefault))
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("format=array query param failed: %s", rec2.Body.String())
	}
	var arrResp1 []map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &arrResp1); err != nil {
		t.Fatalf("failed to unmarshal raw array response: %v, body: %s", err, rec2.Body.String())
	}
	if len(arrResp1) != 2 {
		t.Fatalf("expected 2 items in raw array, got %d", len(arrResp1))
	}
	if arrResp1[0]["holder"] != "Alice" || arrResp1[1]["holder"] != "Bob" {
		t.Errorf("unexpected rows content: %+v", arrResp1)
	}

	// 3. format="records" via request body
	bodyRecords, _ := json.Marshal(map[string]string{
		"sql":    query,
		"format": "records",
	})
	req3 := httptest.NewRequest(http.MethodPost, "/api/sql", bytes.NewReader(bodyRecords))
	rec3 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusOK {
		t.Fatalf("format=records body failed: %s", rec3.Body.String())
	}
	var arrResp2 []map[string]any
	if err := json.Unmarshal(rec3.Body.Bytes(), &arrResp2); err != nil {
		t.Fatalf("failed to unmarshal raw array response from body option: %v", err)
	}
	if len(arrResp2) != 2 {
		t.Fatalf("expected 2 items, got %d", len(arrResp2))
	}
}

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
	if !strings.Contains(regResp.JSCode, "function TransferFunds") {
		t.Fatalf("Expected JSCode in registration response, got: %s", regResp.JSCode)
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

func TestAPI_RegisterNativeJSProcedure(t *testing.T) {
	srv := setupTestServer()

	jsScript := `function AddBonus(params, db) {
  let accId = params['accId'];
  let bonus = params['bonus'];
  let current = db.get('accounts', accId, 'balance');
  db.update('accounts', accId, { 'balance': current + bonus });
  return 0;
}`

	regBody, _ := json.Marshal(map[string]string{"script": jsScript})
	req := httptest.NewRequest(http.MethodPost, "/api/procs", bytes.NewReader(regBody))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("Register native JS proc failed: %d (%s)", rec.Code, rec.Body.String())
	}

	var regResp struct {
		Name   string `json:"name"`
		JSCode string `json:"js_code"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&regResp)
	if regResp.Name != "AddBonus" {
		t.Errorf("Expected proc name AddBonus, got %s", regResp.Name)
	}
	if !strings.Contains(regResp.JSCode, "function AddBonus") {
		t.Errorf("Expected JSCode to contain function AddBonus, got: %s", regResp.JSCode)
	}
}


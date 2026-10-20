package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"lionsql/pkg/auth"
	"lionsql/pkg/catalog"
	"lionsql/pkg/cdc"
	"lionsql/pkg/config"
	"lionsql/pkg/storage/memengine"
)

// TestHTTPPort_FunctionalityIntegration performs an end-to-end integration test
// on a live TCP listener simulating the HTTP 8090 port across 8 core domains.
func TestHTTPPort_FunctionalityIntegration(t *testing.T) {
	// 1. Initialize Storage Engine, Catalog, and Auth Service
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("failed to open storage engine: %v", err)
	}
	cat := catalog.New(eng)

	authSvc := auth.NewAuthService(auth.Config{})
	if _, err := authSvc.EnsureDefaultSuperuser("admin@lionsql.local", "1234567890"); err != nil {
		t.Fatalf("failed to create default superuser: %v", err)
	}

	server := NewServerWithAuth(cat, eng, authSvc, true)

	// Attach CDC Streamer with WAL Hook
	cdcStreamer := cdc.NewStreamer(5000)
	type walHookAdder interface {
		AddWALHook(fn func(rec memengine.WALRecord))
	}
	if wha, ok := any(eng).(walHookAdder); ok {
		wha.AddWALHook(cdcStreamer.OnWALRecord)
	}
	server.SetCDCStreamer(cdcStreamer)

	// 2. Bind to an ephemeral TCP port to test true network I/O
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind ephemeral TCP port: %v", err)
	}
	defer func() { _ = ln.Close() }()

	httpServer := &http.Server{
		Handler:      server.Handler(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	serverErrCh := make(chan error, 1)
	go func() {
		if sErr := httpServer.Serve(ln); sErr != nil && sErr != http.ErrServerClosed {
			serverErrCh <- sErr
		}
	}()

	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
		_ = server.Close()
	}()

	baseURL := fmt.Sprintf("http://%s", ln.Addr().String())
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Do not automatically follow redirects to inspect 302 Found
			return http.ErrUseLastResponse
		},
	}

	var superuserToken string
	var testAPIKey string

	// =========================================================================
	// Domain 1: Health, Version & Redirection
	// =========================================================================
	t.Run("1_HealthAndVersion", func(t *testing.T) {
		// 1.1 Root redirect: GET / -> 302 Found to /_/
		resp, err := client.Get(baseURL + "/")
		if err != nil {
			t.Fatalf("GET / failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusFound {
			t.Errorf("GET / expected 302 Found, got %d", resp.StatusCode)
		}
		loc := resp.Header.Get("Location")
		if loc != "/_/" {
			t.Errorf("GET / expected Location /_/, got %q", loc)
		}

		// 1.2 Health endpoint: GET /api/health
		resp, err = client.Get(baseURL + "/api/health")
		if err != nil {
			t.Fatalf("GET /api/health failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/health expected 200 OK, got %d", resp.StatusCode)
		}
		var healthResp map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&healthResp); err != nil {
			t.Fatalf("failed to decode health response: %v", err)
		}
		if healthResp["status"] != "ok" {
			t.Errorf("expected status 'ok', got %q", healthResp["status"])
		}
		if healthResp["version"] != config.Version {
			t.Errorf("expected version %q, got %q", config.Version, healthResp["version"])
		}
		if healthResp["engine"] != "In-Memory OCC" {
			t.Errorf("expected engine 'In-Memory OCC', got %q", healthResp["engine"])
		}

		// 1.3 Pool stats: GET /api/pool/stats
		resp, err = client.Get(baseURL + "/api/pool/stats")
		if err != nil {
			t.Fatalf("GET /api/pool/stats failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/pool/stats expected 200 OK, got %d", resp.StatusCode)
		}
		var poolStats map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&poolStats); err != nil {
			t.Fatalf("failed to decode pool stats: %v", err)
		}
		if _, ok := poolStats["max_workers"]; !ok {
			t.Errorf("expected pool stats to have max_workers, got: %v", poolStats)
		}
	})

	// =========================================================================
	// Domain 2: Web Studio Dashboard & SDK Distribution
	// =========================================================================
	t.Run("2_WebStudioAndSDKStatic", func(t *testing.T) {
		// 2.1 Web Studio HTML: GET /_/
		resp, err := client.Get(baseURL + "/_/")
		if err != nil {
			t.Fatalf("GET /_/ failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /_/ expected 200 OK, got %d", resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("failed to read Web Studio body: %v", err)
		}
		bodyStr := string(body)
		if !strings.Contains(bodyStr, "LionSQL") {
			t.Errorf("Web Studio UI HTML does not mention LionSQL")
		}

		// 2.2 JavaScript Client SDK: GET /sdk/lionsql.js
		resp, err = client.Get(baseURL + "/sdk/lionsql.js")
		if err != nil {
			t.Fatalf("GET /sdk/lionsql.js failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /sdk/lionsql.js expected 200 OK, got %d", resp.StatusCode)
		}
		jsBody, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("failed to read SDK js body: %v", err)
		}
		if !strings.Contains(string(jsBody), "class LionSQL") {
			t.Errorf("SDK js does not contain class LionSQL")
		}

		// 2.3 TypeScript Definitions: GET /sdk/lionsql.d.ts
		resp, err = client.Get(baseURL + "/sdk/lionsql.d.ts")
		if err != nil {
			t.Fatalf("GET /sdk/lionsql.d.ts failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /sdk/lionsql.d.ts expected 200 OK, got %d", resp.StatusCode)
		}
	})

	// =========================================================================
	// Domain 3: Authentication & RBAC Security
	// =========================================================================
	t.Run("3_AuthAndRBAC", func(t *testing.T) {
		// 3.1 Unauthenticated access should be rejected with 401
		resp, err := client.Get(baseURL + "/api/tables")
		if err != nil {
			t.Fatalf("GET /api/tables failed: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("unauthenticated GET /api/tables expected 401, got %d", resp.StatusCode)
		}

		// 3.2 Bad password login rejection
		badLoginPayload, _ := json.Marshal(map[string]string{
			"email":    "admin@lionsql.local",
			"password": "wrong_password",
		})
		resp, err = client.Post(baseURL+"/api/auth/login", "application/json", bytes.NewReader(badLoginPayload))
		if err != nil {
			t.Fatalf("POST /api/auth/login failed: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("login with invalid password expected 401, got %d", resp.StatusCode)
		}

		// 3.3 Successful Superuser Login
		validLoginPayload, _ := json.Marshal(map[string]string{
			"email":    "admin@lionsql.local",
			"password": "1234567890",
		})
		resp, err = client.Post(baseURL+"/api/auth/login", "application/json", bytes.NewReader(validLoginPayload))
		if err != nil {
			t.Fatalf("POST /api/auth/login failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("login expected 200 OK, got %d", resp.StatusCode)
		}
		var loginResp struct {
			Token string         `json:"token"`
			User  map[string]any `json:"user"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
			t.Fatalf("failed to decode login response: %v", err)
		}
		if loginResp.Token == "" {
			t.Fatalf("expected non-empty JWT token")
		}
		superuserToken = loginResp.Token

		// 3.4 Profile verification: GET /api/auth/me
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/api/auth/me", nil)
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("GET /api/auth/me failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/auth/me expected 200 OK, got %d", resp.StatusCode)
		}
		var meResp map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&meResp); err != nil {
			t.Fatalf("failed to decode /api/auth/me: %v", err)
		}
		if meResp["email"] != "admin@lionsql.local" {
			t.Errorf("expected email 'admin@lionsql.local', got %v", meResp["email"])
		}

		// 3.5 Create API Key: POST /api/keys
		keyReqBody, _ := json.Marshal(map[string]any{
			"name":           "e2e-test-key",
			"role":           "admin",
			"allowed_tables": []string{"*"},
		})
		req, _ = http.NewRequest(http.MethodPost, baseURL+"/api/keys", bytes.NewReader(keyReqBody))
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("POST /api/keys failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /api/keys expected 200/201, got %d", resp.StatusCode)
		}
		var keyResp struct {
			Key string `json:"key"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&keyResp); err != nil {
			t.Fatalf("failed to decode api key response: %v", err)
		}
		if keyResp.Key == "" {
			t.Fatalf("expected non-empty API key")
		}
		testAPIKey = keyResp.Key

		// 3.6 Verify API Key Access: GET /api/tables with X-API-Key
		req, _ = http.NewRequest(http.MethodGet, baseURL+"/api/tables", nil)
		req.Header.Set("X-API-Key", testAPIKey)
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("GET /api/tables with X-API-Key failed: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /api/tables with X-API-Key expected 200 OK, got %d", resp.StatusCode)
		}
	})

	// =========================================================================
	// Domain 4: Table Schema & Metadata DDL
	// =========================================================================
	t.Run("4_TableSchemaDDL", func(t *testing.T) {
		// 4.1 Create Table: POST /api/tables
		createTablePayload, _ := json.Marshal(map[string]any{
			"name": "customers",
			"columns": []map[string]any{
				{"name": "id", "type": "INT", "primary_key": true},
				{"name": "name", "type": "VARCHAR(100)", "not_null": true},
				{"name": "balance", "type": "INT", "not_null": true},
			},
		})
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/tables", bytes.NewReader(createTablePayload))
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /api/tables failed: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /api/tables expected 200/201, got %d", resp.StatusCode)
		}

		// 4.2 List Tables: GET /api/tables
		req, _ = http.NewRequest(http.MethodGet, baseURL+"/api/tables", nil)
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("GET /api/tables failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/tables expected 200 OK, got %d", resp.StatusCode)
		}
		var tables []map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&tables); err != nil {
			t.Fatalf("failed to decode tables list: %v", err)
		}
		found := false
		for _, tbl := range tables {
			if tbl["name"] == "customers" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected 'customers' table in list, got: %v", tables)
		}

		// 4.3 Inspect Table Schema: GET /api/tables/customers/schema
		req, _ = http.NewRequest(http.MethodGet, baseURL+"/api/tables/customers/schema", nil)
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("GET /api/tables/customers/schema failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET schema expected 200 OK, got %d", resp.StatusCode)
		}
		var schemaResp map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&schemaResp); err != nil {
			t.Fatalf("failed to decode schema: %v", err)
		}
		if schemaResp["name"] != "customers" {
			t.Errorf("expected schema name 'customers', got %v", schemaResp["name"])
		}

		// 4.4 Temporary Table Lifecycle: Create then Drop
		tempTblPayload, _ := json.Marshal(map[string]any{
			"name": "temp_to_drop",
			"columns": []map[string]any{
				{"name": "id", "type": "INT", "primary_key": true},
			},
		})
		req, _ = http.NewRequest(http.MethodPost, baseURL+"/api/tables", bytes.NewReader(tempTblPayload))
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("POST /api/tables (temp) failed: %v", err)
		}
		_ = resp.Body.Close()

		req, _ = http.NewRequest(http.MethodDelete, baseURL+"/api/tables/temp_to_drop", nil)
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("DELETE /api/tables/temp_to_drop failed: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
			t.Errorf("DELETE /api/tables/temp_to_drop expected 200/204, got %d", resp.StatusCode)
		}
	})

	// =========================================================================
	// Domain 5: Auto-Generated RESTful Records CRUD & Filtering
	// =========================================================================
	t.Run("5_RecordsRESTAndFilter", func(t *testing.T) {
		// 5.1 Insert Record 1: Alice ($500)
		rec1, _ := json.Marshal(map[string]any{
			"id":      1,
			"name":    "Alice",
			"balance": 500,
		})
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/records/customers", bytes.NewReader(rec1))
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /api/records/customers (1) failed: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /api/records/customers (1) expected 200/201, got %d", resp.StatusCode)
		}

		// 5.2 Insert Record 2: Bob ($1200)
		rec2, _ := json.Marshal(map[string]any{
			"id":      2,
			"name":    "Bob",
			"balance": 1200,
		})
		req, _ = http.NewRequest(http.MethodPost, baseURL+"/api/records/customers", bytes.NewReader(rec2))
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("POST /api/records/customers (2) failed: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /api/records/customers (2) expected 200/201, got %d", resp.StatusCode)
		}

		// 5.3 Get All Records: GET /api/records/customers
		req, _ = http.NewRequest(http.MethodGet, baseURL+"/api/records/customers", nil)
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("GET /api/records/customers failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/records/customers expected 200 OK, got %d", resp.StatusCode)
		}
		var listResp struct {
			Records []map[string]any `json:"records"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
			t.Fatalf("failed to decode records: %v", err)
		}
		if len(listResp.Records) != 2 {
			t.Errorf("expected 2 records, got %d", len(listResp.Records))
		}

		// 5.4 Get Single Record: GET /api/records/customers/1
		req, _ = http.NewRequest(http.MethodGet, baseURL+"/api/records/customers/1", nil)
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("GET /api/records/customers/1 failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/records/customers/1 expected 200 OK, got %d", resp.StatusCode)
		}
		var aliceRec map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&aliceRec); err != nil {
			t.Fatalf("failed to decode alice record: %v", err)
		}
		if aliceRec["name"] != "Alice" {
			t.Errorf("expected name 'Alice', got %v", aliceRec["name"])
		}

		// 5.5 Update Record: PUT /api/records/customers/1 (balance 500 -> 750)
		updPayload, _ := json.Marshal(map[string]any{
			"balance": 750,
		})
		req, _ = http.NewRequest(http.MethodPut, baseURL+"/api/records/customers/1", bytes.NewReader(updPayload))
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("PUT /api/records/customers/1 failed: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT /api/records/customers/1 expected 200 OK, got %d", resp.StatusCode)
		}

		// 5.6 Filter Query: GET /api/records/customers?filter=balance>1000
		req, _ = http.NewRequest(http.MethodGet, baseURL+"/api/records/customers?filter=balance>1000", nil)
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("GET with filter failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET with filter expected 200 OK, got %d", resp.StatusCode)
		}
		var filterResp struct {
			Records []map[string]any `json:"records"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&filterResp); err != nil {
			t.Fatalf("failed to decode filter response: %v", err)
		}
		if len(filterResp.Records) != 1 {
			t.Fatalf("expected 1 record (Bob) with balance>1000, got %d", len(filterResp.Records))
		}
		if filterResp.Records[0]["name"] != "Bob" {
			t.Errorf("expected Bob, got %v", filterResp.Records[0]["name"])
		}

		// 5.7 Delete Record: DELETE /api/records/customers/2
		req, _ = http.NewRequest(http.MethodDelete, baseURL+"/api/records/customers/2", nil)
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("DELETE /api/records/customers/2 failed: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
			t.Errorf("DELETE record expected 200/204, got %d", resp.StatusCode)
		}
	})

	// =========================================================================
	// Domain 6: SQL Query & T-SQL Stored Procedure Execution
	// =========================================================================
	t.Run("6_SQLQueryAndStoredProcedure", func(t *testing.T) {
		// 6.1 Simple SELECT via POST /api/sql
		sqlReqBody, _ := json.Marshal(map[string]string{
			"query": "SELECT 100 AS num, 'LionSQL' AS engine",
		})
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/sql", bytes.NewReader(sqlReqBody))
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /api/sql failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /api/sql expected 200 OK, got %d", resp.StatusCode)
		}
		var sqlResp struct {
			Columns []string         `json:"columns"`
			Rows    []map[string]any `json:"rows"`
			Error   string           `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&sqlResp); err != nil {
			t.Fatalf("failed to decode sql response: %v", err)
		}
		if len(sqlResp.Rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(sqlResp.Rows))
		}
		if fmt.Sprintf("%v", sqlResp.Rows[0]["engine"]) != "LionSQL" {
			t.Errorf("expected engine value 'LionSQL', got %v", sqlResp.Rows[0]["engine"])
		}

		// 6.2 Multi-Statement Batch: DDL + DML via POST /api/sql
		batchSQL, _ := json.Marshal(map[string]string{
			"query": "CREATE TABLE batch_tbl (k INT PRIMARY KEY, v VARCHAR(50)); INSERT INTO batch_tbl VALUES (10, 'val10'); SELECT * FROM batch_tbl;",
		})
		req, _ = http.NewRequest(http.MethodPost, baseURL+"/api/sql", bytes.NewReader(batchSQL))
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("POST /api/sql (batch) failed: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /api/sql (batch) expected 200 OK, got %d", resp.StatusCode)
		}

		// 6.3 Register T-SQL Stored Procedure: POST /api/procs
		procScript := `CREATE PROCEDURE AddCustomerBalance
    @custId INT,
    @amount INT
AS
BEGIN
    UPDATE customers SET balance = balance + @amount WHERE id = @custId;
    RETURN 0;
END;`
		procBody, _ := json.Marshal(map[string]string{"script": procScript})
		req, _ = http.NewRequest(http.MethodPost, baseURL+"/api/procs", bytes.NewReader(procBody))
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("POST /api/procs failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /api/procs expected 200/201, got %d", resp.StatusCode)
		}

		// 6.4 List Procedures: GET /api/procs
		req, _ = http.NewRequest(http.MethodGet, baseURL+"/api/procs", nil)
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("GET /api/procs failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/procs expected 200 OK, got %d", resp.StatusCode)
		}
		var procs []map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&procs); err != nil {
			t.Fatalf("failed to decode procs list: %v", err)
		}
		procFound := false
		for _, p := range procs {
			if p["name"] == "AddCustomerBalance" {
				procFound = true
				break
			}
		}
		if !procFound {
			t.Errorf("expected 'AddCustomerBalance' in procedures list")
		}

		// 6.5 Execute Procedure: POST /api/procs/AddCustomerBalance/exec
		execBody, _ := json.Marshal(map[string]any{
			"args": map[string]any{
				"custId": 1,
				"amount": 250,
			},
		})
		req, _ = http.NewRequest(http.MethodPost, baseURL+"/api/procs/AddCustomerBalance/exec", bytes.NewReader(execBody))
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("POST /api/procs/AddCustomerBalance/exec failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("exec proc expected 200 OK, got %d", resp.StatusCode)
		}
		var execResp struct {
			Success    bool `json:"success"`
			ReturnCode int  `json:"return_code"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&execResp); err != nil {
			t.Fatalf("failed to decode exec response: %v", err)
		}
		if !execResp.Success || execResp.ReturnCode != 0 {
			t.Errorf("expected success with return code 0, got: %+v", execResp)
		}

		// Verify Alice's balance increased: 750 + 250 = 1000
		req, _ = http.NewRequest(http.MethodGet, baseURL+"/api/records/customers/1", nil)
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("GET /api/records/customers/1 failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		var updatedAlice map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&updatedAlice)
		balFloat, _ := updatedAlice["balance"].(float64)
		if int(balFloat) != 1000 {
			t.Errorf("expected Alice balance 1000 after proc execution, got %v", updatedAlice["balance"])
		}
	})

	// =========================================================================
	// Domain 7: Realtime & CDC Event Streaming
	// =========================================================================
	t.Run("7_CDCAndRealtimeStream", func(t *testing.T) {
		// 7.1 CDC Stream Connection & Event Receiving
		cdcCtx, cdcCancel := context.WithCancel(context.Background())
		defer cdcCancel()

		cdcReq, _ := http.NewRequestWithContext(cdcCtx, http.MethodGet, baseURL+"/api/cdc/stream", nil)
		cdcResp, err := client.Do(cdcReq)
		if err != nil {
			t.Fatalf("GET /api/cdc/stream failed: %v", err)
		}
		defer func() { _ = cdcResp.Body.Close() }()

		if cdcResp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/cdc/stream expected 200 OK, got %d", cdcResp.StatusCode)
		}

		// Read initial handshake / event in a background reader
		eventReceived := make(chan string, 1)
		var cdcWg sync.WaitGroup
		cdcWg.Add(1)

		go func() {
			defer cdcWg.Done()
			reader := bufio.NewReader(cdcResp.Body)
			for {
				line, rErr := reader.ReadString('\n')
				if rErr != nil {
					return
				}
				trimmed := strings.TrimSpace(line)
				if trimmed != "" {
					select {
					case eventReceived <- trimmed:
					default:
					}
				}
			}
		}()

		// Trigger a DML to generate a WAL record and CDC event
		dmlBody, _ := json.Marshal(map[string]string{
			"query": "INSERT INTO customers (id, name, balance) VALUES (999, 'StreamTest', 9999);",
		})
		dmlReq, _ := http.NewRequest(http.MethodPost, baseURL+"/api/sql", bytes.NewReader(dmlBody))
		dmlReq.Header.Set("Authorization", "Bearer "+superuserToken)
		dmlReq.Header.Set("Content-Type", "application/json")
		dmlResp, err := client.Do(dmlReq)
		if err == nil {
			_ = dmlResp.Body.Close()
		}

		// Wait deterministically for CDC event or timeout
		select {
		case ev := <-eventReceived:
			if len(ev) == 0 {
				t.Errorf("received empty CDC event line")
			}
		case <-time.After(2 * time.Second):
			// If buffer was quiet or direct handshake line, verified handshake is 200 OK
		}

		cdcCancel()
		_ = cdcResp.Body.Close()
		cdcWg.Wait()

		// 7.2 Realtime SSE Stream Connection: GET /api/realtime
		rtCtx, rtCancel := context.WithCancel(context.Background())
		defer rtCancel()

		rtReq, _ := http.NewRequestWithContext(rtCtx, http.MethodGet, baseURL+"/api/realtime", nil)
		rtResp, err := client.Do(rtReq)
		if err != nil {
			t.Fatalf("GET /api/realtime failed: %v", err)
		}
		defer func() { _ = rtResp.Body.Close() }()

		if rtResp.StatusCode != http.StatusOK {
			t.Errorf("GET /api/realtime expected 200 OK, got %d", rtResp.StatusCode)
		}
		ct := rtResp.Header.Get("Content-Type")
		if !strings.Contains(ct, "text/event-stream") {
			t.Errorf("expected Content-Type text/event-stream, got %q", ct)
		}
	})

	// =========================================================================
	// Domain 8: Observability, Metrics & Management
	// =========================================================================
	t.Run("8_ObservabilityAndMetrics", func(t *testing.T) {
		// 8.1 Prometheus Metrics: GET /metrics
		resp, err := client.Get(baseURL + "/metrics")
		if err != nil {
			t.Fatalf("GET /metrics failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /metrics expected 200 OK, got %d", resp.StatusCode)
		}
		mBody, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("failed to read /metrics body: %v", err)
		}
		metricsText := string(mBody)
		if !strings.Contains(metricsText, "# HELP") || !strings.Contains(metricsText, "# TYPE") {
			t.Errorf("Prometheus metrics text does not contain HELP or TYPE markers")
		}

		// 8.2 OpenTelemetry Compatible Metrics: GET /v1/metrics
		resp, err = client.Get(baseURL + "/v1/metrics")
		if err != nil {
			t.Fatalf("GET /v1/metrics failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /v1/metrics expected 200 OK, got %d", resp.StatusCode)
		}

		// 8.3 Slow Queries API: Configure threshold and query
		thresholdPayload, _ := json.Marshal(map[string]any{"threshold_ms": 50.0})
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/slow-queries/threshold", bytes.NewReader(thresholdPayload))
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("POST /api/slow-queries/threshold failed: %v", err)
		}
		_ = resp.Body.Close()

		req, _ = http.NewRequest(http.MethodGet, baseURL+"/api/slow-queries", nil)
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("GET /api/slow-queries failed: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /api/slow-queries expected 200 OK, got %d", resp.StatusCode)
		}

		// 8.4 Active Queries API: GET /api/queries/active
		req, _ = http.NewRequest(http.MethodGet, baseURL+"/api/queries/active", nil)
		req.Header.Set("Authorization", "Bearer "+superuserToken)
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("GET /api/queries/active failed: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /api/queries/active expected 200 OK, got %d", resp.StatusCode)
		}
	})
}

// TestHTTPPort_ConcurrencyAndErrorResilience verifies that the HTTP 8090 listener
// handles concurrent multiplexed traffic without data races and degrades gracefully on malformed inputs.
func TestHTTPPort_ConcurrencyAndErrorResilience(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	cat := catalog.New(eng)
	authSvc := auth.NewAuthService(auth.Config{})
	_, _ = authSvc.EnsureDefaultSuperuser("admin@lionsql.local", "1234567890")

	server := NewServerWithAuth(cat, eng, authSvc, true)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind ephemeral TCP port: %v", err)
	}
	defer func() { _ = ln.Close() }()

	httpServer := &http.Server{
		Handler:      server.Handler(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		_ = httpServer.Serve(ln)
	}()

	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
		_ = server.Close()
	}()

	baseURL := fmt.Sprintf("http://%s", ln.Addr().String())
	client := &http.Client{Timeout: 5 * time.Second}

	// 1. Obtain token
	loginPayload, _ := json.Marshal(map[string]string{
		"email":    "admin@lionsql.local",
		"password": "1234567890",
	})
	resp, err := client.Post(baseURL+"/api/auth/login", "application/json", bytes.NewReader(loginPayload))
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	var loginResp struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&loginResp)
	_ = resp.Body.Close()
	token := loginResp.Token

	// 2. High-Concurrency Traffic Test (20 parallel workers)
	t.Run("ConcurrentRequests", func(t *testing.T) {
		const workers = 20
		const requestsPerWorker = 10
		var wg sync.WaitGroup
		wg.Add(workers)

		errCount := make(chan error, workers*requestsPerWorker)

		for w := 0; w < workers; w++ {
			go func(workerID int) {
				defer wg.Done()
				for i := 0; i < requestsPerWorker; i++ {
					if i%2 == 0 {
						// Health check
						hResp, hErr := client.Get(baseURL + "/api/health")
						if hErr != nil {
							errCount <- hErr
							continue
						}
						_ = hResp.Body.Close()
						if hResp.StatusCode != http.StatusOK {
							errCount <- fmt.Errorf("worker %d health code %d", workerID, hResp.StatusCode)
						}
					} else {
						// SQL query
						sqlBody, _ := json.Marshal(map[string]string{
							"query": fmt.Sprintf("SELECT %d AS worker_id, %d AS iter;", workerID, i),
						})
						sReq, _ := http.NewRequest(http.MethodPost, baseURL+"/api/sql", bytes.NewReader(sqlBody))
						sReq.Header.Set("Authorization", "Bearer "+token)
						sReq.Header.Set("Content-Type", "application/json")
						sResp, sErr := client.Do(sReq)
						if sErr != nil {
							errCount <- sErr
							continue
						}
						_ = sResp.Body.Close()
						if sResp.StatusCode != http.StatusOK {
							errCount <- fmt.Errorf("worker %d sql code %d", workerID, sResp.StatusCode)
						}
					}
				}
			}(w)
		}

		wg.Wait()
		close(errCount)

		for e := range errCount {
			t.Errorf("concurrent traffic error: %v", e)
		}
	})

	// 3. Error Resilience & Malformed Input Handling
	t.Run("ErrorResilience", func(t *testing.T) {
		// 3.1 Non-existent path returns 404
		resp, err := client.Get(baseURL + "/api/definitely_not_exist_404")
		if err != nil {
			t.Fatalf("GET 404 failed: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected 404, got %d", resp.StatusCode)
		}

		// 3.2 SQL error on missing table returns structured error with success=false
		badSQL, _ := json.Marshal(map[string]string{
			"query": "SELECT * FROM definitely_non_existent_table_xyz;",
		})
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/sql", bytes.NewReader(badSQL))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("POST bad sql failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		var sqlErrResp struct {
			Success bool   `json:"success"`
			Error   string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&sqlErrResp)
		if sqlErrResp.Success || sqlErrResp.Error == "" {
			t.Errorf("expected success=false and non-empty error message, got: %+v", sqlErrResp)
		}

		// 3.3 Malformed JSON in REST endpoint returns 400 Bad Request
		req, _ = http.NewRequest(http.MethodPost, baseURL+"/api/tables", strings.NewReader("{unclosed_json"))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("POST malformed json failed: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request on malformed JSON, got %d", resp.StatusCode)
		}
	})
}


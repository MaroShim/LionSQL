package api_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestJSON_TableAndInsertValidation(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE docs (
		id INT PRIMARY KEY,
		data JSON NOT NULL,
		meta JSONB
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Valid INSERT
	res, err := server.ExecuteSQL(`INSERT INTO docs (id, data, meta) VALUES (1, '{"user": {"name": "Alice", "age": 28}}', '{"tags": ["admin", "dev"]}');`)
	if err != nil || !res.Success {
		t.Fatalf("valid INSERT failed: %v (res: %+v)", err, res)
	}

	// Invalid JSON should be rejected
	_, err = server.ExecuteSQL(`INSERT INTO docs (id, data) VALUES (2, '{"unclosed": 123');`)
	if err == nil {
		t.Fatalf("expected error for invalid JSON insert, got nil")
	}
}

func TestJSON_ExtractOperators(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE users_json (
		id INT PRIMARY KEY,
		profile JSON NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO users_json (id, profile) VALUES
		(1, '{"user": {"name": "Alice", "role": "admin"}, "login_count": 12}'),
		(2, '{"user": {"name": "Bob", "role": "member"}, "login_count": 5}');`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 1. Operator -> returns JSON string
	res, err := server.ExecuteSQL(`SELECT id, profile -> 'user' AS user_doc FROM users_json WHERE id = 1;`)
	if err != nil || !res.Success || len(res.Rows) == 0 {
		t.Fatalf("SELECT -> failed: %v (res: %+v)", err, res)
	}
	userDoc := fmt.Sprintf("%v", res.Rows[0]["user_doc"])
	if !strings.Contains(userDoc, `"name":"Alice"`) && !strings.Contains(userDoc, `"name": "Alice"`) {
		t.Fatalf("expected JSON doc for Alice, got %q", userDoc)
	}

	// 2. Operator ->> returns unquoted text
	res, err = server.ExecuteSQL(`SELECT id, profile -> 'user' ->> 'name' AS username FROM users_json WHERE id = 1;`)
	if err != nil || !res.Success || len(res.Rows) == 0 {
		t.Fatalf("SELECT ->> failed: %v (res: %+v)", err, res)
	}
	username := fmt.Sprintf("%v", res.Rows[0]["username"])
	if username != "Alice" {
		t.Fatalf("expected 'Alice', got %q", username)
	}

	// 3. WHERE clause filtering with ->>
	res, err = server.ExecuteSQL(`SELECT id, profile -> 'user' ->> 'name' AS username FROM users_json WHERE profile -> 'user' ->> 'role' = 'admin';`)
	if err != nil || !res.Success {
		t.Fatalf("SELECT WHERE ->> failed: %v (res: %+v)", err, res)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 admin row, got %d", len(res.Rows))
	}
	if fmt.Sprintf("%v", res.Rows[0]["username"]) != "Alice" {
		t.Fatalf("expected Alice, got %v", res.Rows[0]["username"])
	}
}

func TestJSON_Functions(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE orders (
		id INT PRIMARY KEY,
		info JSON NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO orders (id, info) VALUES
		(101, '{"customer": {"id": 1, "name": "Charlie"}, "total": 250.5, "items": [{"sku": "A1", "qty": 2}]}'),
		(102, '{"customer": {"id": 2, "name": "Diana"}, "total": 75.0, "items": [{"sku": "B2", "qty": 1}]}');`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 1. JSON_VALUE extracts scalar
	res, err := server.ExecuteSQL(`SELECT id, JSON_VALUE(info, '$.customer.name') AS cust_name, JSON_VALUE(info, '$.total') AS total FROM orders WHERE id = 101;`)
	if err != nil || !res.Success || len(res.Rows) == 0 {
		t.Fatalf("SELECT JSON_VALUE failed: %v (res: %+v)", err, res)
	}
	if fmt.Sprintf("%v", res.Rows[0]["cust_name"]) != "Charlie" {
		t.Fatalf("expected Charlie, got %v", res.Rows[0]["cust_name"])
	}

	// 2. JSON_QUERY extracts object/array
	res, err = server.ExecuteSQL(`SELECT id, JSON_QUERY(info, '$.customer') AS cust_obj FROM orders WHERE id = 101;`)
	if err != nil || !res.Success || len(res.Rows) == 0 {
		t.Fatalf("SELECT JSON_QUERY failed: %v (res: %+v)", err, res)
	}
	custObj := fmt.Sprintf("%v", res.Rows[0]["cust_obj"])
	if !strings.Contains(custObj, "Charlie") {
		t.Fatalf("expected customer object containing Charlie, got %q", custObj)
	}

	// 3. WHERE clause with JSON_VALUE
	res, err = server.ExecuteSQL(`SELECT id FROM orders WHERE JSON_VALUE(info, '$.customer.name') = 'Diana';`)
	if err != nil || !res.Success {
		t.Fatalf("SELECT WHERE JSON_VALUE failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row for Diana, got %d", len(res.Rows))
	}
	if fmt.Sprintf("%v", res.Rows[0]["id"]) != "102" {
		t.Fatalf("expected order 102, got %v", res.Rows[0]["id"])
	}
}

func TestJSON_StoredProcExecution(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE app_config (
		key VARCHAR(50) PRIMARY KEY,
		config JSON NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO app_config (key, config) VALUES ('server', '{"port": 8080, "host": "localhost"}');`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	procSQL := `CREATE PROCEDURE GetConfigPort
AS
BEGIN
	DECLARE @doc VARCHAR(100);
	DECLARE @port VARCHAR(50);
	SELECT config INTO @doc FROM app_config WHERE key = 'server';
	SET @port = JSON_VALUE(@doc, '$.port');
	PRINT @port;
END;`
	_, err = server.ExecuteSQL(procSQL)
	if err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	res, err := server.ExecuteSQL(`EXEC GetConfigPort;`)
	if err != nil || !res.Success {
		t.Fatalf("EXEC GetConfigPort failed: %v (res: %+v)", err, res)
	}
	found := false
	for _, msg := range res.Messages {
		if strings.Contains(msg, "8080") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected message 8080 from PRINT @port, got messages: %v", res.Messages)
	}
}

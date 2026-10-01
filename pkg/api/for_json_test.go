package api_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestForJSON_BasicPath(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	stmts := []string{
		"CREATE TABLE members (id INT PRIMARY KEY, name VARCHAR(50), role VARCHAR(30))",
		"INSERT INTO members VALUES (1, 'Alice', 'Engineer')",
		"INSERT INTO members VALUES (2, 'Bob',   'Manager')",
	}
	for _, stmt := range stmts {
		if _, err := server.ExecuteSQL(stmt); err != nil {
			t.Fatalf("setup failed (%q): %v", stmt, err)
		}
	}

	query := "SELECT id, name, role FROM members ORDER BY id ASC FOR JSON PATH;"
	res, err := server.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("FOR JSON PATH query failed: %v", err)
	}

	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}

	rawJSON, ok := res.Rows[0]["JSON_F52E2B61-18A1-11d1-B105-00805F49916B"].(string)
	if !ok {
		t.Fatalf("expected string JSON column, got: %v", res.Rows[0])
	}

	var parsed []map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		t.Fatalf("failed to parse JSON result: %v (raw: %s)", err, rawJSON)
	}

	if len(parsed) != 2 {
		t.Fatalf("expected 2 elements in JSON array, got %d", len(parsed))
	}
	if parsed[0]["name"] != "Alice" || parsed[0]["role"] != "Engineer" {
		t.Errorf("element 0 mismatch: %+v", parsed[0])
	}
	if parsed[1]["name"] != "Bob" || parsed[1]["role"] != "Manager" {
		t.Errorf("element 1 mismatch: %+v", parsed[1])
	}
}

func TestForJSON_WithoutArrayWrapper(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	stmts := []string{
		"CREATE TABLE configs (k VARCHAR(20) PRIMARY KEY, v VARCHAR(50))",
		"INSERT INTO configs VALUES ('theme', 'dark')",
	}
	for _, stmt := range stmts {
		if _, err := server.ExecuteSQL(stmt); err != nil {
			t.Fatalf("setup failed (%q): %v", stmt, err)
		}
	}

	query := "SELECT k, v FROM configs WHERE k = 'theme' FOR JSON PATH, WITHOUT_ARRAY_WRAPPER;"
	res, err := server.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("FOR JSON WITHOUT_ARRAY_WRAPPER query failed: %v", err)
	}

	rawJSON := res.Rows[0]["JSON_F52E2B61-18A1-11d1-B105-00805F49916B"].(string)
	if strings.HasPrefix(rawJSON, "[") {
		t.Errorf("expected object without array wrapper, got: %s", rawJSON)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		t.Fatalf("failed to parse single object JSON: %v", err)
	}
	if parsed["k"] != "theme" || parsed["v"] != "dark" {
		t.Errorf("parsed object mismatch: %+v", parsed)
	}
}

func TestForJSON_RootOption(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	stmts := []string{
		"CREATE TABLE items (id INT PRIMARY KEY, name VARCHAR(20))",
		"INSERT INTO items VALUES (10, 'Notebook')",
	}
	for _, stmt := range stmts {
		if _, err := server.ExecuteSQL(stmt); err != nil {
			t.Fatalf("setup failed (%q): %v", stmt, err)
		}
	}

	query := "SELECT id, name FROM items FOR JSON PATH, ROOT('inventory');"
	res, err := server.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("FOR JSON ROOT query failed: %v", err)
	}

	rawJSON := res.Rows[0]["JSON_F52E2B61-18A1-11d1-B105-00805F49916B"].(string)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		t.Fatalf("failed to parse root JSON: %v", err)
	}

	inv, ok := parsed["inventory"].([]any)
	if !ok || len(inv) != 1 {
		t.Fatalf("expected inventory array of length 1, got: %+v", parsed)
	}
}

func TestForJSON_DotNotationNestedObjects(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	stmts := []string{
		"CREATE TABLE staff (id INT PRIMARY KEY, name VARCHAR(30), dept_name VARCHAR(30), dept_building VARCHAR(30))",
		"INSERT INTO staff VALUES (1, 'Alice', 'Engineering', 'Tower A')",
	}
	for _, stmt := range stmts {
		if _, err := server.ExecuteSQL(stmt); err != nil {
			t.Fatalf("setup failed (%q): %v", stmt, err)
		}
	}

	// In T-SQL FOR JSON PATH, aliasing as [department.name] creates nested object {"department": {"name": ...}}
	query := `
		SELECT 
			id,
			name,
			dept_name AS [department.name],
			dept_building AS [department.building]
		FROM staff
		FOR JSON PATH;
	`
	res, err := server.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("FOR JSON nested dot notation query failed: %v", err)
	}

	rawJSON := res.Rows[0]["JSON_F52E2B61-18A1-11d1-B105-00805F49916B"].(string)
	var parsed []map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		t.Fatalf("failed to parse nested JSON: %v", err)
	}

	if len(parsed) != 1 {
		t.Fatalf("expected 1 record, got %d", len(parsed))
	}
	dept, ok := parsed[0]["department"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested 'department' object, got: %+v", parsed[0])
	}
	if dept["name"] != "Engineering" || dept["building"] != "Tower A" {
		t.Errorf("nested department values mismatch: %+v", dept)
	}
}

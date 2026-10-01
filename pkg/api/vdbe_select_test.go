package api_test

import (
	"strings"
	"testing"
)

func TestVDBEScalarSelect(t *testing.T) {
	s, cleanup := setupTestServer()
	defer cleanup()

	// 1. Simple arithmetic expression
	res, err := s.ExecuteSQL("SELECT 1 + 1 AS sum_val;")
	if err != nil {
		t.Fatalf("scalar select failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if fmtVal := res.Rows[0]["sum_val"]; fmtVal != int64(2) && fmtVal != 2 {
		t.Fatalf("expected 2, got %v (%T)", fmtVal, fmtVal)
	}

	// 2. Multi-column calculation and string literal
	res, err = s.ExecuteSQL("SELECT 10 * (2 + 3) AS calc, 'hello world' AS greeting;")
	if err != nil {
		t.Fatalf("multi-column scalar select failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if calcVal := res.Rows[0]["calc"]; calcVal != int64(50) && calcVal != 50 {
		t.Fatalf("expected 50, got %v", calcVal)
	}
	if greetingVal := res.Rows[0]["greeting"]; greetingVal != "hello world" {
		t.Fatalf("expected 'hello world', got %v", greetingVal)
	}

	// 3. Built-in scalar function evaluation
	res, err = s.ExecuteSQL("SELECT UPPER('sqlion') AS upper_name;")
	if err != nil {
		t.Fatalf("scalar function select failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["upper_name"] != "SQLION" {
		t.Fatalf("unexpected upper_name result: %+v", res.Rows)
	}

	// 4. Verify OpResultRow bytecode generation via EXPLAIN BYTECODE
	res, err = s.ExecuteSQL("EXPLAIN BYTECODE SELECT 40 + 2 AS answer;")
	if err != nil {
		t.Fatalf("EXPLAIN BYTECODE failed: %v", err)
	}
	var bytecodeLines []string
	for _, row := range res.Rows {
		if b, ok := row["Bytecode"].(string); ok {
			bytecodeLines = append(bytecodeLines, b)
		}
	}
	bytecodeOutput := strings.Join(bytecodeLines, "\n")
	if !strings.Contains(bytecodeOutput, "RESULT_ROW") {
		t.Fatalf("expected RESULT_ROW in bytecode disassembly, got:\n%s", bytecodeOutput)
	}
}

func TestVDBEPointSelect(t *testing.T) {
	s, cleanup := setupTestServer()
	defer cleanup()

	_, err := s.ExecuteSQL("CREATE TABLE products_vdbe (id INT PRIMARY KEY, name VARCHAR(50), price INT);")
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}
	_, err = s.ExecuteSQL("INSERT INTO products_vdbe VALUES (1, 'Laptop', 1200), (2, 'Mouse', 25), (3, 'Keyboard', 75);")
	if err != nil {
		t.Fatalf("failed to insert products: %v", err)
	}

	// 1. Explicit projection point lookup
	res, err := s.ExecuteSQL("SELECT id, name, price FROM products_vdbe WHERE id = 1;")
	if err != nil {
		t.Fatalf("point select failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if res.Rows[0]["name"] != "Laptop" {
		t.Fatalf("expected Laptop, got %v", res.Rows[0]["name"])
	}

	// 2. Wildcard projection point lookup
	res, err = s.ExecuteSQL("SELECT * FROM products_vdbe WHERE id = 2;")
	if err != nil {
		t.Fatalf("wildcard point select failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Mouse" {
		t.Fatalf("unexpected wildcard result: %+v", res.Rows)
	}

	// 3. Non-existent primary key lookup (0 rows)
	res, err = s.ExecuteSQL("SELECT id, name FROM products_vdbe WHERE id = 999;")
	if err != nil {
		t.Fatalf("non-existent key select failed: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows for non-existent key, got %d", len(res.Rows))
	}

	// 4. Verify OpPointSelect bytecode generation via EXPLAIN BYTECODE
	res, err = s.ExecuteSQL("EXPLAIN BYTECODE SELECT id, name FROM products_vdbe WHERE id = 3;")
	if err != nil {
		t.Fatalf("EXPLAIN BYTECODE failed: %v", err)
	}
	var bytecodeLines []string
	for _, row := range res.Rows {
		if b, ok := row["Bytecode"].(string); ok {
			bytecodeLines = append(bytecodeLines, b)
		}
	}
	bytecodeOutput := strings.Join(bytecodeLines, "\n")
	if !strings.Contains(bytecodeOutput, "POINT_SELECT") {
		t.Fatalf("expected POINT_SELECT in bytecode disassembly, got:\n%s", bytecodeOutput)
	}

	// 5. Query with session variable in WHERE clause
	sessID := "vdbe_point_sess"
	_, err = s.ExecuteSessionSQL(sessID, "DECLARE @targetId INT = 3;")
	if err != nil {
		t.Fatalf("DECLARE failed: %v", err)
	}
	res, err = s.ExecuteSessionSQL(sessID, "SELECT name, price FROM products_vdbe WHERE id = @targetId;")
	if err != nil {
		t.Fatalf("session variable point select failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Keyboard" {
		t.Fatalf("unexpected result with session variable: %+v", res.Rows)
	}

	// 6. Point select with aliases and expressions
	res, err = s.ExecuteSQL("SELECT id AS item_id, price * 2 AS double_price FROM products_vdbe WHERE id = 1;")
	if err != nil {
		t.Fatalf("point select with aliases failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if fmtVal := res.Rows[0]["item_id"]; fmtVal != int64(1) && fmtVal != 1 {
		t.Fatalf("expected item_id=1, got %v", fmtVal)
	}
	if fmtVal := res.Rows[0]["double_price"]; fmtVal != int64(2400) && fmtVal != 2400 {
		t.Fatalf("expected double_price=2400, got %v", fmtVal)
	}
}

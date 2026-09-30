package api_test

import (
	"fmt"
	"testing"
)

func TestMerge_Basic(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	// 1. Create target and source tables
	_, err := server.ExecuteSQL(`CREATE TABLE target_products (
		id INT PRIMARY KEY,
		name VARCHAR(50) NOT NULL,
		price FLOAT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE target_products failed: %v", err)
	}

	_, err = server.ExecuteSQL(`CREATE TABLE source_products (
		id INT PRIMARY KEY,
		name VARCHAR(50) NOT NULL,
		price FLOAT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE source_products failed: %v", err)
	}

	// Initial data: target has 1 (Widget, 100) and 2 (Gadget, 200)
	_, err = server.ExecuteSQL(`INSERT INTO target_products VALUES (1, 'Widget', 100.0), (2, 'Gadget', 200.0);`)
	if err != nil {
		t.Fatalf("INSERT target failed: %v", err)
	}

	// Source has 2 (Gadget Pro, 250.0) -> MATCHED (UPDATE), and 3 (Doohickey, 300.0) -> NOT MATCHED (INSERT)
	_, err = server.ExecuteSQL(`INSERT INTO source_products VALUES (2, 'Gadget Pro', 250.0), (3, 'Doohickey', 300.0);`)
	if err != nil {
		t.Fatalf("INSERT source failed: %v", err)
	}

	// 2. Execute MERGE
	res, err := server.ExecuteSQL(`
MERGE INTO target_products AS t
USING source_products AS s
ON t.id = s.id
WHEN MATCHED THEN
    UPDATE SET t.name = s.name, t.price = s.price
WHEN NOT MATCHED THEN
    INSERT (id, name, price) VALUES (s.id, s.name, s.price);
`)
	if err != nil || !res.Success {
		t.Fatalf("MERGE statement failed: %v (res: %+v)", err, res)
	}
	if res.RowsAffected != 2 {
		t.Fatalf("expected 2 rows affected, got: %d", res.RowsAffected)
	}

	// 3. Verify target_products state
	res, err = server.ExecuteSQL(`SELECT id, name, price FROM target_products ORDER BY id;`)
	if err != nil || len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows in target, got: %+v", res.Rows)
	}

	// id=1: kept original
	if fmt.Sprintf("%v", res.Rows[0]["name"]) != "Widget" || fmt.Sprintf("%v", res.Rows[0]["price"]) != "100" {
		t.Errorf("row 1 mismatch: %+v", res.Rows[0])
	}
	// id=2: updated to Gadget Pro, 250
	if fmt.Sprintf("%v", res.Rows[1]["name"]) != "Gadget Pro" || fmt.Sprintf("%v", res.Rows[1]["price"]) != "250" {
		t.Errorf("row 2 mismatch: %+v", res.Rows[1])
	}
	// id=3: inserted Doohickey, 300
	if fmt.Sprintf("%v", res.Rows[2]["name"]) != "Doohickey" || fmt.Sprintf("%v", res.Rows[2]["price"]) != "300" {
		t.Errorf("row 3 mismatch: %+v", res.Rows[2])
	}
}

func TestMerge_UsingValues(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE inventory (
		item_id INT PRIMARY KEY,
		stock INT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO inventory VALUES (10, 50), (20, 100);`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// Merge with inline VALUES source
	res, err := server.ExecuteSQL(`
MERGE INTO inventory AS inv
USING (VALUES (10, 80), (30, 15)) AS src (item_id, stock)
ON inv.item_id = src.item_id
WHEN MATCHED THEN
    UPDATE SET inv.stock = src.stock
WHEN NOT MATCHED THEN
    INSERT (item_id, stock) VALUES (src.item_id, src.stock);
`)
	if err != nil || !res.Success {
		t.Fatalf("MERGE with VALUES failed: %v (res: %+v)", err, res)
	}
	if res.RowsAffected != 2 {
		t.Fatalf("expected 2 rows affected, got: %d", res.RowsAffected)
	}

	res, err = server.ExecuteSQL(`SELECT item_id, stock FROM inventory ORDER BY item_id;`)
	if err != nil || len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got: %+v", res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["stock"]) != "80" || fmt.Sprintf("%v", res.Rows[1]["stock"]) != "100" || fmt.Sprintf("%v", res.Rows[2]["stock"]) != "15" {
		t.Fatalf("unexpected inventory rows: %+v", res.Rows)
	}
}

func TestMerge_MatchedDelete(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE staff (
		emp_id INT PRIMARY KEY,
		status VARCHAR(20) NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO staff VALUES (1, 'active'), (2, 'leave');`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// Merge with condition: DELETE when matched and status = 'terminate'
	res, err := server.ExecuteSQL(`
MERGE staff s
USING (VALUES (1, 'terminate'), (3, 'active')) src (emp_id, status)
ON s.emp_id = src.emp_id
WHEN MATCHED AND src.status = 'terminate' THEN
    DELETE
WHEN NOT MATCHED THEN
    INSERT (emp_id, status) VALUES (src.emp_id, src.status);
`)
	if err != nil || !res.Success {
		t.Fatalf("MERGE with DELETE failed: %v (res: %+v)", err, res)
	}

	res, err = server.ExecuteSQL(`SELECT emp_id FROM staff ORDER BY emp_id;`)
	if err != nil || len(res.Rows) != 2 {
		t.Fatalf("expected 2 remaining rows, got: %+v", res.Rows)
	}
	// Row 1 should be deleted; row 2 and row 3 should exist
	if fmt.Sprintf("%v", res.Rows[0]["emp_id"]) != "2" || fmt.Sprintf("%v", res.Rows[1]["emp_id"]) != "3" {
		t.Fatalf("expected staff IDs [2, 3], got: %+v", res.Rows)
	}
}

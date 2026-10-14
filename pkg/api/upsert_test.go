package api_test

import (
	"fmt"
	"testing"
)

func TestOnConflict_DoNothing(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE test_oc_nothing (
		id INT PRIMARY KEY,
		name VARCHAR(50) NOT NULL,
		val INT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 1. Initial Insert
	_, err = server.ExecuteSQL(`INSERT INTO test_oc_nothing (id, name, val) VALUES (1, 'Alice', 10);`)
	if err != nil {
		t.Fatalf("initial INSERT failed: %v", err)
	}

	// 2. Insert with ON CONFLICT (id) DO NOTHING: 1 should be ignored, 2 should be inserted
	res, err := server.ExecuteSQL(`INSERT INTO test_oc_nothing (id, name, val) VALUES (1, 'Bob', 20), (2, 'Charlie', 30) ON CONFLICT (id) DO NOTHING;`)
	if err != nil {
		t.Fatalf("INSERT ON CONFLICT DO NOTHING failed: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("expected 1 row affected, got: %d", res.RowsAffected)
	}

	// Verify table contents
	sel, err := server.ExecuteSQL(`SELECT id, name, val FROM test_oc_nothing ORDER BY id ASC;`)
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(sel.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(sel.Rows))
	}
	if fmt.Sprintf("%v", sel.Rows[0]["name"]) != "Alice" || fmt.Sprintf("%v", sel.Rows[0]["val"]) != "10" {
		t.Fatalf("expected id=1 to remain Alice/10, got: %+v", sel.Rows[0])
	}
	if fmt.Sprintf("%v", sel.Rows[1]["name"]) != "Charlie" || fmt.Sprintf("%v", sel.Rows[1]["val"]) != "30" {
		t.Fatalf("expected id=2 to be Charlie/30, got: %+v", sel.Rows[1])
	}

	// 3. Test ON CONFLICT DO NOTHING without explicit target column
	res, err = server.ExecuteSQL(`INSERT INTO test_oc_nothing (id, name, val) VALUES (2, 'Duplicate', 99) ON CONFLICT DO NOTHING;`)
	if err != nil {
		t.Fatalf("INSERT ON CONFLICT DO NOTHING (no target) failed: %v", err)
	}
	if res.RowsAffected != 0 {
		t.Fatalf("expected 0 rows affected, got: %d", res.RowsAffected)
	}
}

func TestOnConflict_DoUpdate(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE test_oc_update (
		id INT PRIMARY KEY,
		name VARCHAR(50) NOT NULL,
		counter INT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 1. Initial Insert
	_, err = server.ExecuteSQL(`INSERT INTO test_oc_update (id, name, counter) VALUES (1, 'Initial', 5);`)
	if err != nil {
		t.Fatalf("initial INSERT failed: %v", err)
	}

	// 2. Conflict DO UPDATE using EXCLUDED and existing row column reference
	res, err := server.ExecuteSQL(`INSERT INTO test_oc_update (id, name, counter) VALUES (1, 'Updated', 10)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, counter = test_oc_update.counter + EXCLUDED.counter;`)
	if err != nil {
		t.Fatalf("INSERT ON CONFLICT DO UPDATE failed: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("expected 1 row affected, got: %d", res.RowsAffected)
	}

	sel, err := server.ExecuteSQL(`SELECT id, name, counter FROM test_oc_update WHERE id = 1;`)
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(sel.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(sel.Rows))
	}
	if fmt.Sprintf("%v", sel.Rows[0]["name"]) != "Updated" {
		t.Fatalf("expected name='Updated', got: %v", sel.Rows[0]["name"])
	}
	if fmt.Sprintf("%v", sel.Rows[0]["counter"]) != "15" {
		t.Fatalf("expected counter=15 (5+10), got: %v", sel.Rows[0]["counter"])
	}
}

func TestOnConflict_DoUpdate_Where(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE test_oc_where (
		id INT PRIMARY KEY,
		version INT NOT NULL,
		data VARCHAR(50) NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO test_oc_where (id, version, data) VALUES (1, 10, 'Version 10');`)
	if err != nil {
		t.Fatalf("initial INSERT failed: %v", err)
	}

	// Stale update (EXCLUDED.version < version): WHERE condition false -> no update
	res, err := server.ExecuteSQL(`INSERT INTO test_oc_where (id, version, data) VALUES (1, 5, 'Stale Version')
		ON CONFLICT (id) DO UPDATE SET version = EXCLUDED.version, data = EXCLUDED.data
		WHERE EXCLUDED.version > test_oc_where.version;`)
	if err != nil {
		t.Fatalf("INSERT ON CONFLICT with false WHERE failed: %v", err)
	}
	if res.RowsAffected != 0 {
		t.Fatalf("expected 0 rows affected due to WHERE condition, got: %d", res.RowsAffected)
	}

	// Verify unchanged
	sel, err := server.ExecuteSQL(`SELECT id, version, data FROM test_oc_where WHERE id = 1;`)
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if fmt.Sprintf("%v", sel.Rows[0]["version"]) != "10" {
		t.Fatalf("expected version=10, got: %v", sel.Rows[0]["version"])
	}

	// Fresh update (EXCLUDED.version > version): WHERE condition true -> update succeeds
	res, err = server.ExecuteSQL(`INSERT INTO test_oc_where (id, version, data) VALUES (1, 20, 'Fresh Version')
		ON CONFLICT (id) DO UPDATE SET version = EXCLUDED.version, data = EXCLUDED.data
		WHERE EXCLUDED.version > test_oc_where.version;`)
	if err != nil {
		t.Fatalf("INSERT ON CONFLICT with true WHERE failed: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("expected 1 row affected, got: %d", res.RowsAffected)
	}

	sel, err = server.ExecuteSQL(`SELECT id, version, data FROM test_oc_where WHERE id = 1;`)
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if fmt.Sprintf("%v", sel.Rows[0]["version"]) != "20" || fmt.Sprintf("%v", sel.Rows[0]["data"]) != "Fresh Version" {
		t.Fatalf("expected version=20 / Fresh Version, got: %+v", sel.Rows[0])
	}
}

func TestUpsert_Shorthand(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE test_upsert_short (
		id INT PRIMARY KEY,
		name VARCHAR(50) NOT NULL,
		score FLOAT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 1. First UPSERT inserts row
	res, err := server.ExecuteSQL(`UPSERT INTO test_upsert_short (id, name, score) VALUES (1, 'Alice', 95.5);`)
	if err != nil {
		t.Fatalf("initial UPSERT failed: %v", err)
	}
	if res.Command != "UPSERT" {
		t.Fatalf("expected Command='UPSERT', got: %s", res.Command)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("expected 1 row affected, got: %d", res.RowsAffected)
	}

	// 2. Second UPSERT on same PK updates non-PK columns
	res, err = server.ExecuteSQL(`UPSERT INTO test_upsert_short (id, name, score) VALUES (1, 'Alice Wonderland', 99.0);`)
	if err != nil {
		t.Fatalf("conflict UPSERT failed: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("expected 1 row affected, got: %d", res.RowsAffected)
	}

	// 3. UPSERT a new row
	res, err = server.ExecuteSQL(`UPSERT INTO test_upsert_short (id, name, score) VALUES (2, 'Bob', 88.0);`)
	if err != nil {
		t.Fatalf("new row UPSERT failed: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("expected 1 row affected, got: %d", res.RowsAffected)
	}

	// Verify all rows
	sel, err := server.ExecuteSQL(`SELECT id, name, score FROM test_upsert_short ORDER BY id ASC;`)
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(sel.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(sel.Rows))
	}
	if fmt.Sprintf("%v", sel.Rows[0]["name"]) != "Alice Wonderland" {
		t.Fatalf("expected id=1 to be Alice Wonderland, got: %v", sel.Rows[0]["name"])
	}
	if fmt.Sprintf("%v", sel.Rows[1]["name"]) != "Bob" {
		t.Fatalf("expected id=2 to be Bob, got: %v", sel.Rows[1]["name"])
	}
}

func TestOnConflict_BatchWithMixedConflicts(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE test_oc_mixed (
		id INT PRIMARY KEY,
		val INT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Batch insert with duplicate keys in the same batch
	res, err := server.ExecuteSQL(`INSERT INTO test_oc_mixed (id, val) VALUES (1, 10), (2, 20), (1, 99)
		ON CONFLICT (id) DO UPDATE SET val = EXCLUDED.val;`)
	if err != nil {
		t.Fatalf("batch INSERT ON CONFLICT DO UPDATE failed: %v", err)
	}
	if res.RowsAffected != 3 {
		t.Fatalf("expected 3 rows affected, got: %d", res.RowsAffected)
	}

	sel, err := server.ExecuteSQL(`SELECT id, val FROM test_oc_mixed WHERE id = 1;`)
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(sel.Rows) != 1 || fmt.Sprintf("%v", sel.Rows[0]["val"]) != "99" {
		t.Fatalf("expected id=1 to be updated to 99, got: %+v", sel.Rows)
	}
}

func TestOnConflict_OnConstraint(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE test_oc_const (
		id INT PRIMARY KEY,
		name VARCHAR(50) NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO test_oc_const (id, name) VALUES (1, 'First');`)
	if err != nil {
		t.Fatalf("initial INSERT failed: %v", err)
	}

	// Test ON CONFLICT ON CONSTRAINT pk_test_oc_const DO NOTHING
	res, err := server.ExecuteSQL(`INSERT INTO test_oc_const (id, name) VALUES (1, 'Second') ON CONFLICT ON CONSTRAINT pk_test_oc_const DO NOTHING;`)
	if err != nil {
		t.Fatalf("INSERT ON CONFLICT ON CONSTRAINT DO NOTHING failed: %v", err)
	}
	if res.RowsAffected != 0 {
		t.Fatalf("expected 0 rows affected, got: %d", res.RowsAffected)
	}

	sel, err := server.ExecuteSQL(`SELECT name FROM test_oc_const WHERE id = 1;`)
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if fmt.Sprintf("%v", sel.Rows[0]["name"]) != "First" {
		t.Fatalf("expected id=1 name to be 'First', got: %v", sel.Rows[0]["name"])
	}
}

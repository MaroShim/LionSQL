package tds_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/wire/tds"
)

func openTdsServerForMultiResults(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	eng := memengine.New()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		eng.Close()
		t.Fatalf("failed to start TDS server: %v", err)
	}

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)
	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		_ = srv.Close()
		_ = eng.Close()
		t.Fatalf("sql.Open failed: %v", err)
	}

	cleanup := func() {
		_ = db.Close()
		_ = srv.Close()
		_ = eng.Close()
	}
	return db, cleanup
}

// TestTDS_MultipleResultSets_SequentialNextResult verifies that sequential multiple result sets
// (Multiple Result Sets / Sequential NextResult) are properly streamed and navigable via
// rows.NextResultSet() in the official go-mssqldb driver (FEAT-TDS-MULTIPLE-RESULT-SETS-1, Defect 107).
func TestTDS_MultipleResultSets_SequentialNextResult(t *testing.T) {
	db, cleanup := openTdsServerForMultiResults(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Three sequential SELECT statements in a single batch
	batchSQL := "SELECT 100 AS num, 'first' AS label; SELECT 200 AS num, 'second' AS label; SELECT 300 AS num, 'third' AS label;"
	rows, err := db.QueryContext(ctx, batchSQL)
	if err != nil {
		t.Fatalf("QueryContext failed for multi-result batch: %v", err)
	}
	defer rows.Close()

	// Result Set 1
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("failed to get columns for result set 1: %v", err)
	}
	if len(cols) != 2 || cols[0] != "num" || cols[1] != "label" {
		t.Fatalf("unexpected columns for result set 1: %v", cols)
	}
	if !rows.Next() {
		t.Fatalf("expected row in result set 1")
	}
	var num int64
	var label string
	if err := rows.Scan(&num, &label); err != nil {
		t.Fatalf("scan result set 1 failed: %v", err)
	}
	if num != 100 || label != "first" {
		t.Fatalf("expected (100, 'first'), got (%d, %q)", num, label)
	}
	if rows.Next() {
		t.Fatalf("unexpected extra row in result set 1")
	}

	// Result Set 2
	if !rows.NextResultSet() {
		t.Fatalf("expected NextResultSet() == true for result set 2")
	}
	cols, err = rows.Columns()
	if err != nil {
		t.Fatalf("failed to get columns for result set 2: %v", err)
	}
	if len(cols) != 2 || cols[0] != "num" || cols[1] != "label" {
		t.Fatalf("unexpected columns for result set 2: %v", cols)
	}
	if !rows.Next() {
		t.Fatalf("expected row in result set 2")
	}
	if err := rows.Scan(&num, &label); err != nil {
		t.Fatalf("scan result set 2 failed: %v", err)
	}
	if num != 200 || label != "second" {
		t.Fatalf("expected (200, 'second'), got (%d, %q)", num, label)
	}
	if rows.Next() {
		t.Fatalf("unexpected extra row in result set 2")
	}

	// Result Set 3
	if !rows.NextResultSet() {
		t.Fatalf("expected NextResultSet() == true for result set 3")
	}
	cols, err = rows.Columns()
	if err != nil {
		t.Fatalf("failed to get columns for result set 3: %v", err)
	}
	if len(cols) != 2 || cols[0] != "num" || cols[1] != "label" {
		t.Fatalf("unexpected columns for result set 3: %v", cols)
	}
	if !rows.Next() {
		t.Fatalf("expected row in result set 3")
	}
	if err := rows.Scan(&num, &label); err != nil {
		t.Fatalf("scan result set 3 failed: %v", err)
	}
	if num != 300 || label != "third" {
		t.Fatalf("expected (300, 'third'), got (%d, %q)", num, label)
	}
	if rows.Next() {
		t.Fatalf("unexpected extra row in result set 3")
	}

	// No more result sets
	if rows.NextResultSet() {
		t.Fatalf("expected NextResultSet() == false after result set 3")
	}
}

// TestTDS_MultipleResultSets_MultiRowTables tests multiple result sets returning multiple rows per set.
func TestTDS_MultipleResultSets_MultiRowTables(t *testing.T) {
	db, cleanup := openTdsServerForMultiResults(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := db.ExecContext(ctx, "CREATE TABLE items (id INT PRIMARY KEY, name VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE items failed: %v", err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO items VALUES (1, 'Alpha'), (2, 'Beta'), (3, 'Gamma');"); err != nil {
		t.Fatalf("INSERT INTO items failed: %v", err)
	}

	batch := "SELECT id, name FROM items ORDER BY id ASC; SELECT COUNT(*) AS total FROM items;"
	rows, err := db.QueryContext(ctx, batch)
	if err != nil {
		t.Fatalf("QueryContext failed: %v", err)
	}
	defer rows.Close()

	// Set 1: 3 rows
	expectedNames := []string{"Alpha", "Beta", "Gamma"}
	for i, exp := range expectedNames {
		if !rows.Next() {
			t.Fatalf("expected row %d in result set 1", i+1)
		}
		var id int
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatalf("scan row %d failed: %v", i+1, err)
		}
		if id != i+1 || name != exp {
			t.Fatalf("row %d: expected (%d, %s), got (%d, %s)", i+1, i+1, exp, id, name)
		}
	}
	if rows.Next() {
		t.Fatalf("unexpected extra row in result set 1")
	}

	// Set 2: 1 row
	if !rows.NextResultSet() {
		t.Fatalf("expected result set 2")
	}
	if !rows.Next() {
		t.Fatalf("expected row in result set 2")
	}
	var total int64
	if err := rows.Scan(&total); err != nil {
		t.Fatalf("scan total failed: %v", err)
	}
	if total != 3 {
		t.Fatalf("expected total=3, got %d", total)
	}
	if rows.Next() {
		t.Fatalf("unexpected extra row in result set 2")
	}
	if rows.NextResultSet() {
		t.Fatalf("expected no more result sets")
	}
}

// TestTDS_MultipleResultSets_StoredProcedure verifies that stored procedures emitting multiple
// SELECT queries stream multiple result sets via rows.NextResultSet().
func TestTDS_MultipleResultSets_StoredProcedure(t *testing.T) {
	db, cleanup := openTdsServerForMultiResults(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	procSQL := `
CREATE PROCEDURE sp_dual_dataset
AS
BEGIN
    SELECT 1 AS set_id, 'alpha' AS set_name;
    SELECT 2 AS set_id, 'beta' AS set_name;
END;
`
	if _, err := db.ExecContext(ctx, procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	rows, err := db.QueryContext(ctx, "EXEC sp_dual_dataset;")
	if err != nil {
		t.Fatalf("EXEC sp_dual_dataset failed: %v", err)
	}
	defer rows.Close()

	// Result Set 1
	if !rows.Next() {
		t.Fatalf("expected row in proc result set 1")
	}
	var setID int
	var setName string
	if err := rows.Scan(&setID, &setName); err != nil {
		t.Fatalf("scan proc result set 1 failed: %v", err)
	}
	if setID != 1 || setName != "alpha" {
		t.Fatalf("expected (1, 'alpha'), got (%d, %s)", setID, setName)
	}
	if rows.Next() {
		t.Fatalf("unexpected extra row in proc result set 1")
	}

	// Result Set 2
	if !rows.NextResultSet() {
		t.Fatalf("expected NextResultSet() == true for proc result set 2")
	}
	if !rows.Next() {
		t.Fatalf("expected row in proc result set 2")
	}
	if err := rows.Scan(&setID, &setName); err != nil {
		t.Fatalf("scan proc result set 2 failed: %v", err)
	}
	if setID != 2 || setName != "beta" {
		t.Fatalf("expected (2, 'beta'), got (%d, %s)", setID, setName)
	}
	if rows.Next() {
		t.Fatalf("unexpected extra row in proc result set 2")
	}

	if rows.NextResultSet() {
		t.Fatalf("expected no more result sets after proc result set 2")
	}
}

// TestTDS_MultipleResultSets_MixedDMLAndProc tests a stored procedure executing an INSERT followed by two SELECTs.
func TestTDS_MultipleResultSets_MixedDMLAndProc(t *testing.T) {
	db, cleanup := openTdsServerForMultiResults(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := db.ExecContext(ctx, "CREATE TABLE mixed_items (id INT PRIMARY KEY, status VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE mixed_items failed: %v", err)
	}

	procSQL := `
CREATE PROCEDURE sp_mixed_multi
AS
BEGIN
    INSERT INTO mixed_items VALUES (10, 'Created');
    SELECT id, status FROM mixed_items WHERE id = 10;
    SELECT COUNT(*) AS total_count FROM mixed_items;
END;
`
	if _, err := db.ExecContext(ctx, procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE sp_mixed_multi failed: %v", err)
	}

	rows, err := db.QueryContext(ctx, "EXEC sp_mixed_multi;")
	if err != nil {
		t.Fatalf("EXEC sp_mixed_multi failed: %v", err)
	}
	defer rows.Close()

	// Result Set 1: id=10, status='Created'
	if !rows.Next() {
		t.Fatalf("expected row in mixed result set 1")
	}
	var id int
	var status string
	if err := rows.Scan(&id, &status); err != nil {
		t.Fatalf("scan mixed result set 1 failed: %v", err)
	}
	if id != 10 || status != "Created" {
		t.Fatalf("expected (10, 'Created'), got (%d, %s)", id, status)
	}
	if rows.Next() {
		t.Fatalf("unexpected extra row in mixed result set 1")
	}

	// Result Set 2: total_count=1
	if !rows.NextResultSet() {
		t.Fatalf("expected NextResultSet() == true for mixed result set 2")
	}
	if !rows.Next() {
		t.Fatalf("expected row in mixed result set 2")
	}
	var totalCount int64
	if err := rows.Scan(&totalCount); err != nil {
		t.Fatalf("scan mixed total_count failed: %v", err)
	}
	if totalCount != 1 {
		t.Fatalf("expected totalCount=1, got %d", totalCount)
	}
	if rows.Next() {
		t.Fatalf("unexpected extra row in mixed result set 2")
	}

	if rows.NextResultSet() {
		t.Fatalf("expected no more result sets after mixed result set 2")
	}
}


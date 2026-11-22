package spec_defects_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/wire"
	"lionsql/pkg/wire/tds"

	_ "github.com/lib/pq"
	_ "github.com/microsoft/go-mssqldb"
)

// TestDefect_Duality_Case1_ComplexFunctionDotTruncation verifies that nested function expressions
// like COALESCE(SUM(o.amount), 0) are never truncated to "amount), 0)" across TDS and PGWire.
func TestDefect_Duality_Case1_ComplexFunctionDotTruncation(t *testing.T) {
	// Setup isolated TDS server
	engTDS := memengine.New()
	_ = engTDS.Open("")
	defer engTDS.Close()
	catTDS := catalog.New(engTDS)
	srvTDS := tds.NewServer("127.0.0.1:0", catTDS, engTDS)
	if err := srvTDS.Start(); err != nil {
		t.Fatalf("failed to start TDS server: %v", err)
	}
	defer srvTDS.Close()

	// Setup isolated PGWire server
	engPG := memengine.New()
	_ = engPG.Open("")
	defer engPG.Close()
	catPG := catalog.New(engPG)
	coordPG := engine.NewCoordinator(catPG, engPG, engine.DefaultConfig())
	srvPG := wire.NewServer("127.0.0.1:0", catPG, engPG)
	srvPG.SetCoordinator(coordPG)
	if err := srvPG.Start(); err != nil {
		t.Fatalf("failed to start PGWire server: %v", err)
	}
	defer srvPG.Close()

	partsTDS := strings.Split(srvTDS.Addr().String(), ":")
	dsnTDS := fmt.Sprintf("server=%s;port=%s;user=sa;password=Secret123!;database=master;encrypt=disable", partsTDS[0], partsTDS[1])
	dbTDS, err := sql.Open("sqlserver", dsnTDS)
	if err != nil {
		t.Fatalf("failed to open TDS client: %v", err)
	}
	defer dbTDS.Close()

	dsnPG := fmt.Sprintf("postgres://postgres@%s/master?sslmode=disable", srvPG.Addr().String())
	dbPG, err := sql.Open("postgres", dsnPG)
	if err != nil {
		t.Fatalf("failed to open PG client: %v", err)
	}
	defer dbPG.Close()

	initSQL := `
CREATE TABLE slt_sales_dept (dept_id INT PRIMARY KEY, dept_name VARCHAR(50));
INSERT INTO slt_sales_dept VALUES (1, 'Sales');
CREATE TABLE slt_sales_orders (order_id INT PRIMARY KEY, dept_id INT, amount INT);
INSERT INTO slt_sales_orders VALUES (10, 1, 1000);
`
	for _, stmt := range strings.Split(initSQL, ";") {
		trimmed := strings.TrimSpace(stmt)
		if trimmed == "" {
			continue
		}
		if _, err := dbTDS.Exec(trimmed); err != nil {
			t.Fatalf("TDS init failed: %v", err)
		}
		if _, err := dbPG.Exec(trimmed); err != nil {
			t.Fatalf("PG init failed: %v", err)
		}
	}

	testQuery := `SELECT d.dept_name, COUNT(o.order_id), COALESCE(SUM(o.amount), 0)
FROM slt_sales_dept d
LEFT JOIN slt_sales_orders o ON d.dept_id = o.dept_id
GROUP BY d.dept_name;`

	// Verify TDS column metadata and value
	rowsTDS, err := dbTDS.QueryContext(context.Background(), testQuery)
	if err != nil {
		t.Fatalf("TDS query failed: %v", err)
	}
	defer rowsTDS.Close()
	colsTDS, err := rowsTDS.Columns()
	if err != nil {
		t.Fatalf("TDS columns failed: %v", err)
	}
	if len(colsTDS) != 3 {
		t.Fatalf("TDS expected 3 cols, got %d (%v)", len(colsTDS), colsTDS)
	}
	if colsTDS[2] == "amount), 0)" || !strings.HasPrefix(colsTDS[2], "COALESCE") {
		t.Fatalf("TDS column 2 was truncated: %q", colsTDS[2])
	}
	if !rowsTDS.Next() {
		t.Fatalf("TDS expected row")
	}
	var nameTDS string
	var countTDS, sumTDS int64
	if err := rowsTDS.Scan(&nameTDS, &countTDS, &sumTDS); err != nil {
		t.Fatalf("TDS scan failed: %v", err)
	}
	if sumTDS != 1000 {
		t.Fatalf("TDS expected sum 1000, got %d", sumTDS)
	}

	// Verify PGWire column metadata and value
	rowsPG, err := dbPG.QueryContext(context.Background(), testQuery)
	if err != nil {
		t.Fatalf("PG query failed: %v", err)
	}
	defer rowsPG.Close()
	colsPG, err := rowsPG.Columns()
	if err != nil {
		t.Fatalf("PG columns failed: %v", err)
	}
	if len(colsPG) != 3 {
		t.Fatalf("PG expected 3 cols, got %d (%v)", len(colsPG), colsPG)
	}
	if colsPG[2] == "amount), 0)" || !strings.HasPrefix(colsPG[2], "COALESCE") {
		t.Fatalf("PG column 2 was truncated: %q", colsPG[2])
	}
	if !rowsPG.Next() {
		t.Fatalf("PG expected row")
	}
	var namePG string
	var countPG, sumPG int64
	if err := rowsPG.Scan(&namePG, &countPG, &sumPG); err != nil {
		t.Fatalf("PG scan failed: %v", err)
	}
	if sumPG != 1000 {
		t.Fatalf("PG expected sum 1000, got %d", sumPG)
	}
}

// TestDefect_Duality_Case2_SelfJoinDuplicateAlias verifies that self-join duplicate projection
// columns like "SELECT e.emp_id, e.emp_name, m.emp_name" retain their SQL names and values across TDS and PGWire.
func TestDefect_Duality_Case2_SelfJoinDuplicateAlias(t *testing.T) {
	// Setup isolated TDS server
	engTDS := memengine.New()
	_ = engTDS.Open("")
	defer engTDS.Close()
	catTDS := catalog.New(engTDS)
	srvTDS := tds.NewServer("127.0.0.1:0", catTDS, engTDS)
	if err := srvTDS.Start(); err != nil {
		t.Fatalf("failed to start TDS server: %v", err)
	}
	defer srvTDS.Close()

	// Setup isolated PGWire server
	engPG := memengine.New()
	_ = engPG.Open("")
	defer engPG.Close()
	catPG := catalog.New(engPG)
	coordPG := engine.NewCoordinator(catPG, engPG, engine.DefaultConfig())
	srvPG := wire.NewServer("127.0.0.1:0", catPG, engPG)
	srvPG.SetCoordinator(coordPG)
	if err := srvPG.Start(); err != nil {
		t.Fatalf("failed to start PGWire server: %v", err)
	}
	defer srvPG.Close()

	partsTDS := strings.Split(srvTDS.Addr().String(), ":")
	dsnTDS := fmt.Sprintf("server=%s;port=%s;user=sa;password=Secret123!;database=master;encrypt=disable", partsTDS[0], partsTDS[1])
	dbTDS, err := sql.Open("sqlserver", dsnTDS)
	if err != nil {
		t.Fatalf("failed to open TDS client: %v", err)
	}
	defer dbTDS.Close()

	dsnPG := fmt.Sprintf("postgres://postgres@%s/master?sslmode=disable", srvPG.Addr().String())
	dbPG, err := sql.Open("postgres", dsnPG)
	if err != nil {
		t.Fatalf("failed to open PG client: %v", err)
	}
	defer dbPG.Close()

	initSQL := `
CREATE TABLE slt_emp2 (emp_id INT PRIMARY KEY, emp_name VARCHAR(50), mgr_id INT);
INSERT INTO slt_emp2 VALUES (101, 'Kim', 0);
INSERT INTO slt_emp2 VALUES (102, 'Lee', 101);
`
	for _, stmt := range strings.Split(initSQL, ";") {
		trimmed := strings.TrimSpace(stmt)
		if trimmed == "" {
			continue
		}
		if _, err := dbTDS.Exec(trimmed); err != nil {
			t.Fatalf("TDS init failed: %v", err)
		}
		if _, err := dbPG.Exec(trimmed); err != nil {
			t.Fatalf("PG init failed: %v", err)
		}
	}

	testQuery := `SELECT e.emp_id, e.emp_name, m.emp_name FROM slt_emp2 e JOIN slt_emp2 m ON e.mgr_id = m.emp_id;`

	// Verify TDS: columns must be emp_id, emp_name, emp_name and values 102, Lee, Kim
	rowsTDS, err := dbTDS.QueryContext(context.Background(), testQuery)
	if err != nil {
		t.Fatalf("TDS query failed: %v", err)
	}
	defer rowsTDS.Close()
	colsTDS, err := rowsTDS.Columns()
	if err != nil {
		t.Fatalf("TDS columns failed: %v", err)
	}
	if len(colsTDS) != 3 || colsTDS[0] != "emp_id" || colsTDS[1] != "emp_name" || colsTDS[2] != "emp_name" {
		t.Fatalf("TDS unexpected column names: %v (expected [emp_id, emp_name, emp_name])", colsTDS)
	}
	if !rowsTDS.Next() {
		t.Fatalf("TDS expected row")
	}
	var idTDS int64
	var empTDS, mgrTDS string
	if err := rowsTDS.Scan(&idTDS, &empTDS, &mgrTDS); err != nil {
		t.Fatalf("TDS scan failed: %v", err)
	}
	if idTDS != 102 || empTDS != "Lee" || mgrTDS != "Kim" {
		t.Fatalf("TDS unexpected values: id=%d, emp=%q, mgr=%q (expected 102, Lee, Kim)", idTDS, empTDS, mgrTDS)
	}

	// Verify PGWire: columns must be emp_id, emp_name, emp_name and values 102, Lee, Kim
	rowsPG, err := dbPG.QueryContext(context.Background(), testQuery)
	if err != nil {
		t.Fatalf("PG query failed: %v", err)
	}
	defer rowsPG.Close()
	colsPG, err := rowsPG.Columns()
	if err != nil {
		t.Fatalf("PG columns failed: %v", err)
	}
	if len(colsPG) != 3 || colsPG[0] != "emp_id" || colsPG[1] != "emp_name" || colsPG[2] != "emp_name" {
		t.Fatalf("PG unexpected column names: %v (expected [emp_id, emp_name, emp_name])", colsPG)
	}
	if !rowsPG.Next() {
		t.Fatalf("PG expected row")
	}
	var idPG int64
	var empPG, mgrPG string
	if err := rowsPG.Scan(&idPG, &empPG, &mgrPG); err != nil {
		t.Fatalf("PG scan failed: %v", err)
	}
	if idPG != 102 || empPG != "Lee" || mgrPG != "Kim" {
		t.Fatalf("PG unexpected values: id=%d, emp=%q, mgr=%q (expected 102, Lee, Kim)", idPG, empPG, mgrPG)
	}
}

var _ storage.Engine = (*memengine.Engine)(nil)

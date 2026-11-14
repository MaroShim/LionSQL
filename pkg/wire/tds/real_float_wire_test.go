package tds_test

import (
	"database/sql"
	"fmt"
	"math"
	"net"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/wire/tds"

	_ "github.com/microsoft/go-mssqldb"
)

func TestRealVsFloatWireAndCatalog(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		eng.Close()
		t.Fatalf("failed to start TDS server: %v", err)
	}
	defer func() {
		_ = srv.Close()
		_ = eng.Close()
	}()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)
	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("failed to open TDS connection: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		t.Fatalf("failed to ping TDS server: %v", err)
	}

	// 1. Verify sys.types has both 'real' (59) and 'float' (62)
	typesQuery := "SELECT name, system_type_id, max_length, precision FROM sys.types WHERE name IN ('real', 'float') ORDER BY system_type_id"
	rows, err := db.Query(typesQuery)
	if err != nil {
		t.Fatalf("query sys.types failed: %v", err)
	}
	defer rows.Close()

	foundReal := false
	foundFloat := false
	for rows.Next() {
		var name string
		var sysTypeID, maxLen, prec int
		if err := rows.Scan(&name, &sysTypeID, &maxLen, &prec); err != nil {
			t.Fatalf("scan sys.types failed: %v", err)
		}
		if name == "real" {
			foundReal = true
			if sysTypeID != 59 || maxLen != 4 || prec != 24 {
				t.Fatalf("expected real(59, 4, 24), got (%d, %d, %d)", sysTypeID, maxLen, prec)
			}
		} else if name == "float" {
			foundFloat = true
			if sysTypeID != 62 || maxLen != 8 || prec != 53 {
				t.Fatalf("expected float(62, 8, 53), got (%d, %d, %d)", sysTypeID, maxLen, prec)
			}
		}
	}
	if !foundReal || !foundFloat {
		t.Fatalf("expected both real and float in sys.types, found real=%v, float=%v", foundReal, foundFloat)
	}

	// 2. Create table with REAL, FLOAT, FLOAT(24), FLOAT(53)
	_, err = db.Exec("CREATE TABLE test_types_rf (id INT PRIMARY KEY, r REAL, f FLOAT, f24 FLOAT(24), f53 FLOAT(53))")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Verify sys.columns mapping
	colQuery := "SELECT name, system_type_id, max_length, precision FROM sys.columns WHERE table_name = 'test_types_rf' ORDER BY column_id"
	cRows, err := db.Query(colQuery)
	if err != nil {
		t.Fatalf("query sys.columns failed: %v", err)
	}
	defer cRows.Close()

	expectedCols := []struct {
		name      string
		sysTypeID int
		maxLen    int
		prec      int
	}{
		{"id", 56, 4, 10},
		{"r", 59, 4, 24},
		{"f", 62, 8, 53},
		{"f24", 59, 4, 24},
		{"f53", 62, 8, 53},
	}

	idx := 0
	for cRows.Next() {
		var name string
		var sysTypeID, maxLen, prec int
		if err := cRows.Scan(&name, &sysTypeID, &maxLen, &prec); err != nil {
			t.Fatalf("scan sys.columns failed: %v", err)
		}
		if idx >= len(expectedCols) {
			t.Fatalf("unexpected extra column %q", name)
		}
		exp := expectedCols[idx]
		if name != exp.name || sysTypeID != exp.sysTypeID || maxLen != exp.maxLen || prec != exp.prec {
			t.Fatalf("col %d want (%s, %d, %d, %d), got (%s, %d, %d, %d)",
				idx, exp.name, exp.sysTypeID, exp.maxLen, exp.prec, name, sysTypeID, maxLen, prec)
		}
		idx++
	}
	if idx != len(expectedCols) {
		t.Fatalf("expected %d columns, got %d", len(expectedCols), idx)
	}

	// 3. Insert and query data via go-mssqldb
	_, err = db.Exec("INSERT INTO test_types_rf VALUES (1, 1.25, 2.5, 3.75, 5.0)")
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	var id int
	var rVal float32
	var fVal float64
	var f24Val float32
	var f53Val float64
	err = db.QueryRow("SELECT id, r, f, f24, f53 FROM test_types_rf WHERE id = 1").Scan(&id, &rVal, &fVal, &f24Val, &f53Val)
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}

	if id != 1 || math.Abs(float64(rVal-1.25)) > 1e-5 || math.Abs(fVal-2.5) > 1e-9 || math.Abs(float64(f24Val-3.75)) > 1e-5 || math.Abs(f53Val-5.0) > 1e-9 {
		t.Fatalf("unexpected values: id=%d, r=%v, f=%v, f24=%v, f53=%v", id, rVal, fVal, f24Val, f53Val)
	}
}

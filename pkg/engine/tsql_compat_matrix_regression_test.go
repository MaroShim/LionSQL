package engine_test

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func newCompatMatrixTestCoord(t *testing.T) (*engine.Coordinator, func()) {
	t.Helper()
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	cleanup := func() {
		coord.Close()
		eng.Close()
	}
	return coord, cleanup
}

func TestTSQLCompatMatrix_InlineTVF(t *testing.T) {
	coord, cleanup := newCompatMatrixTestCoord(t)
	defer cleanup()

	setupQueries := []string{
		"CREATE TABLE compat_items (id INT PRIMARY KEY, name VARCHAR(50), price INT, active INT);",
		"INSERT INTO compat_items VALUES (1, 'Pen', 10, 1), (2, 'Notebook', 50, 1), (3, 'Monitor', 300, 0), (4, 'Keyboard', 120, 1);",
		"CREATE FUNCTION fn_ActiveItems() RETURNS TABLE AS RETURN (SELECT id, name FROM compat_items WHERE active = 1 ORDER BY id ASC);",
		"CREATE FUNCTION dbo.fn_ExpensiveItems(@minPrice INT) RETURNS TABLE AS RETURN (SELECT id, name, price FROM compat_items WHERE price >= @minPrice ORDER BY id ASC);",
	}
	for _, q := range setupQueries {
		if _, err := coord.ExecuteSQL(q); err != nil {
			t.Fatalf("setup query %q failed: %v", q, err)
		}
	}

	// 1. Parameterless inline TVF
	res, err := coord.ExecuteSQL("SELECT * FROM fn_ActiveItems();")
	if err != nil {
		t.Fatalf("SELECT from fn_ActiveItems() failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 active items from fn_ActiveItems(), got %d (%+v)", len(res.Rows), res.Rows)
	}
	if res.Rows[0]["name"] != "Pen" || res.Rows[2]["name"] != "Keyboard" {
		t.Fatalf("unexpected fn_ActiveItems() rows: %+v", res.Rows)
	}

	// 2. Parameterized inline TVF (both unqualified and dbo-qualified)
	for _, q := range []string{
		"SELECT * FROM fn_ExpensiveItems(100);",
		"SELECT * FROM dbo.fn_ExpensiveItems(100);",
	} {
		res, err = coord.ExecuteSQL(q)
		if err != nil {
			t.Fatalf("%q failed: %v", q, err)
		}
		if len(res.Rows) != 2 {
			t.Fatalf("%q expected 2 rows, got %d (%+v)", q, len(res.Rows), res.Rows)
		}
		if res.Rows[0]["name"] != "Monitor" || res.Rows[1]["name"] != "Keyboard" {
			t.Fatalf("%q unexpected rows: %+v", q, res.Rows)
		}
	}

	// 3. DROP FUNCTION removes TVF from catalog
	if _, err := coord.ExecuteSQL("DROP FUNCTION dbo.fn_ExpensiveItems;"); err != nil {
		t.Fatalf("DROP FUNCTION failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("SELECT * FROM fn_ExpensiveItems(100);"); err == nil {
		t.Fatalf("expected error querying dropped TVF fn_ExpensiveItems")
	}
}

func TestTSQLCompatMatrix_UseDatabaseAndSessionContext(t *testing.T) {
	coord, cleanup := newCompatMatrixTestCoord(t)
	defer cleanup()

	if _, err := coord.ExecuteSessionSQL("sess-a", "USE [SalesDB];"); err != nil {
		t.Fatalf("sess-a USE [SalesDB] failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL("sess-b", "USE HRDB;"); err != nil {
		t.Fatalf("sess-b USE HRDB failed: %v", err)
	}

	resA, err := coord.ExecuteSessionSQL("sess-a", "SELECT DB_NAME() AS db;")
	if err != nil || len(resA.Rows) != 1 || resA.Rows[0]["db"] != "SalesDB" {
		t.Fatalf("sess-a expected DB_NAME()=SalesDB, got err=%v rows=%+v", err, resA)
	}

	resB, err := coord.ExecuteSessionSQL("sess-b", "SELECT DB_NAME() AS db;")
	if err != nil || len(resB.Rows) != 1 || resB.Rows[0]["db"] != "HRDB" {
		t.Fatalf("sess-b expected DB_NAME()=HRDB, got err=%v rows=%+v", err, resB)
	}

	if _, err := coord.ExecuteSessionSQL("sess-a", "USE master;"); err != nil {
		t.Fatalf("sess-a USE master failed: %v", err)
	}
	resA2, err := coord.ExecuteSessionSQL("sess-a", "SELECT DB_NAME() AS db;")
	if err != nil || len(resA2.Rows) != 1 || resA2.Rows[0]["db"] != "master" {
		t.Fatalf("sess-a expected DB_NAME()=master, got err=%v rows=%+v", err, resA2)
	}
}

func TestTSQLCompatMatrix_SessionOptionsAndSystemVars(t *testing.T) {
	coord, cleanup := newCompatMatrixTestCoord(t)
	defer cleanup()

	sess := "sess-opts"
	for _, stmt := range []string{
		"SET NOCOUNT ON;",
		"SET ANSI_NULLS ON;",
		"SET QUOTED_IDENTIFIER ON;",
		"SET XACT_ABORT ON;",
	} {
		res, err := coord.ExecuteSessionSQL(sess, stmt)
		if err != nil || !res.Success {
			t.Fatalf("option statement %q failed: err=%v res=%+v", stmt, err, res)
		}
	}

	res, err := coord.ExecuteSessionSQL(sess, "SELECT @@TRANCOUNT AS tc, @@ERROR AS errnum;")
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("SELECT @@TRANCOUNT, @@ERROR failed: err=%v res=%+v", err, res)
	}
	if res.Rows[0]["tc"] != int64(0) || res.Rows[0]["errnum"] != int64(0) {
		t.Fatalf("expected @@TRANCOUNT=0, @@ERROR=0, got %+v", res.Rows[0])
	}
}

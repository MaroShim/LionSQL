package catalog_test

import (
	"testing"

	"sqlion/pkg/catalog"
	"sqlion/pkg/planner"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
)

func TestCatalog_TableStats(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)

	stats := planner.NewTableStats("customers", 5000)
	cat.SetTableStats("customers", stats)

	retrieved, found := cat.GetTableStats("customers")
	if !found || retrieved == nil {
		t.Fatalf("expected to find table stats for customers")
	}
	if retrieved.RowCount != 5000 {
		t.Fatalf("expected row count 5000, got %d", retrieved.RowCount)
	}

	all := cat.ListTableStats()
	if len(all) != 1 || all["customers"] == nil {
		t.Fatalf("expected 1 table stats in list, got %d", len(all))
	}
}

func TestCatalog_DynamicTableRowCountFallback(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)

	schema := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	_ = tx.Insert("items", storage.Record{"id": storage.IntValue(1), "name": storage.StringValue("A")})
	_ = tx.Insert("items", storage.Record{"id": storage.IntValue(2), "name": storage.StringValue("B")})
	_ = tx.Insert("items", storage.Record{"id": storage.IntValue(3), "name": storage.StringValue("C")})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Dynamic fallback without explicit ANALYZE / SetTableStats
	stats, found := cat.GetTableStats("items")
	if !found || stats == nil {
		t.Fatalf("expected dynamic TableStats fallback to find 'items'")
	}
	if stats.RowCount != 3 {
		t.Fatalf("expected dynamic row count 3, got: %d", stats.RowCount)
	}
}

func TestCatalog_ProcedureDuplicateCheck(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)

	proc1 := &catalog.Procedure{
		Name: "TransferFunds",
		Body: "CREATE PROCEDURE TransferFunds AS BEGIN PRINT '1'; END;",
	}

	// 1. First registration via CreateProcedure succeeds
	if err := cat.CreateProcedure(proc1); err != nil {
		t.Fatalf("first CreateProcedure failed: %v", err)
	}

	// 2. Duplicate registration with exact same name fails with ErrProcExists
	procDup := &catalog.Procedure{
		Name: "TransferFunds",
		Body: "CREATE PROCEDURE TransferFunds AS BEGIN PRINT '2'; END;",
	}
	if err := cat.CreateProcedure(procDup); err == nil {
		t.Fatalf("expected ErrProcExists for duplicate procedure, got nil")
	}

	// 3. Duplicate registration with different casing (transferfunds) also fails with ErrProcExists
	procCase := &catalog.Procedure{
		Name: "transferfunds",
		Body: "CREATE PROCEDURE transferfunds AS BEGIN PRINT '3'; END;",
	}
	if err := cat.CreateProcedure(procCase); err == nil {
		t.Fatalf("expected ErrProcExists for case-different duplicate, got nil")
	}

	// 4. AlterProcedure updates existing procedure
	procAlter := &catalog.Procedure{
		Name: "TransferFunds",
		Body: "ALTER PROCEDURE TransferFunds AS BEGIN PRINT 'altered'; END;",
	}
	if err := cat.AlterProcedure(procAlter); err != nil {
		t.Fatalf("AlterProcedure failed: %v", err)
	}

	// 5. AlterProcedure on non-existent procedure fails with ErrProcNotFound
	procNonExistent := &catalog.Procedure{
		Name: "NonExistentProc",
		Body: "ALTER PROCEDURE NonExistentProc AS BEGIN PRINT 'nope'; END;",
	}
	if err := cat.AlterProcedure(procNonExistent); err == nil {
		t.Fatalf("expected ErrProcNotFound for AlterProcedure on non-existent proc, got nil")
	}

	// 6. Case-insensitive lookup
	p, err := cat.GetProcedure("transferfunds")
	if err != nil || p == nil {
		t.Fatalf("GetProcedure with lowercase failed: %v", err)
	}
	if p.Body != procAlter.Body {
		t.Errorf("expected altered body, got %q", p.Body)
	}

	// 7. ListProcedures should contain only 1 procedure
	procs := cat.ListProcedures()
	if len(procs) != 1 {
		t.Fatalf("expected exactly 1 procedure in list, got %d", len(procs))
	}
}

package memengine

import (
	"os"
	"path/filepath"
	"testing"

	"lionsql/pkg/storage"
)

// TestGroup2_StorageUpdateCase tests BUG-STORAGE-UPDATE-CASE-1
func TestGroup2_StorageUpdateCase(t *testing.T) {
	e := New()
	defer e.Close()

	schema := &storage.TableSchema{
		Name: "Accounts",
		Columns: []storage.Column{
			{Name: "ID", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "BalanceAmount", Type: storage.TypeInt, Nullable: false},
		},
	}
	if err := e.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, err := e.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	rec := storage.Record{
		"ID":            storage.IntValue(1),
		"BalanceAmount": storage.IntValue(100),
	}
	if err := tx.Insert("Accounts", rec); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Update with lowercase column name
	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx 2 failed: %v", err)
	}
	patch := storage.Record{
		"balanceamount": storage.IntValue(500),
	}
	if err := tx2.Update("Accounts", storage.IntValue(1), patch); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Verify updated value is preserved
	tx3, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx 3 failed: %v", err)
	}
	defer tx3.Rollback()
	readRec, err := tx3.Get("Accounts", storage.IntValue(1))
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	v, ok := readRec.Get("BalanceAmount")
	if !ok || v.IVal != 500 {
		t.Fatalf("expected BalanceAmount=500, got ok=%v, val=%+v", ok, v)
	}
}

func TestStorage_REV_STOR_001_ParsePKValue_RobustnessAndZeroAlloc(t *testing.T) {
	// 1. Hot-path allocation check: fmt.Sscanf causes 6 allocs/op (120B).
	// With strconv.ParseInt, this MUST be 0 allocs/op (Golden Rule 24).
	allocs := testing.AllocsPerRun(100, func() {
		_ = ParsePKValue("1002345", storage.TypeInt)
	})
	if allocs > 0 {
		t.Fatalf("REV-STOR-001: ParsePKValue hot-path allocates %v allocs/op (expected 0 allocs/op via strconv)", allocs)
	}

	// 2. Valid integers
	v1 := ParsePKValue("12345", storage.TypeInt)
	if v1.Type != storage.TypeInt || v1.IVal != 12345 {
		t.Fatalf("expected IntValue(12345), got %v", v1)
	}

	// 3. Valid floats
	v2 := ParsePKValue("3.1415", storage.TypeFloat)
	if v2.Type != storage.TypeFloat || v2.FVal != 3.1415 {
		t.Fatalf("expected FloatValue(3.1415), got %v", v2)
	}

	// 4. Negative values
	v3 := ParsePKValue("-999", storage.TypeInt)
	if v3.IVal != -999 {
		t.Fatalf("expected IntValue(-999), got %v", v3)
	}
}

func TestStorage_REV_STOR_002_Open_AOFCompactionErrorPropagated(t *testing.T) {
	dir := t.TempDir()
	aofPath := filepath.Join(dir, "wal.aof")

	// Write valid WAL records so ReplayWAL doesn't truncate them as garbage
	walWriter, err := NewWALWriter(aofPath, FsyncNone)
	if err != nil {
		t.Fatalf("failed to create WAL writer: %v", err)
	}
	rec1 := WALRecord{
		LSN:  1,
		TxID: 1,
		Ops: []WALOp{
			{Type: OpInsert, Table: "t1", Key: "1", Record: storage.Record{"id": storage.IntValue(1)}},
		},
	}
	if err := walWriter.Append(rec1); err != nil {
		t.Fatalf("failed to append WAL record: %v", err)
	}
	rec2 := WALRecord{
		LSN:  2,
		TxID: 2,
		Ops: []WALOp{
			{Type: OpInsert, Table: "t1", Key: "2", Record: storage.Record{"id": storage.IntValue(2)}},
		},
	}
	if err := walWriter.Append(rec2); err != nil {
		t.Fatalf("failed to append WAL record: %v", err)
	}
	_ = walWriter.Close()

	// Create tables.mpk.tmp as a directory so that saveDiskLocked's os.WriteFile fails with EISDIR
	tmpFile := filepath.Join(dir, "tables.mpk.tmp")
	if err := os.Mkdir(tmpFile, 0755); err != nil {
		t.Fatalf("failed to mkdir: %v", err)
	}

	eng := New()
	defer eng.Close()
	opts := Options{
		DataDir:      dir,
		EnableAOF:    true,
		AOFThreshold: 50, // smaller than WAL file size to trigger rewriteAOFLocked() on startup
	}

	err = eng.OpenWithOptions(opts)
	if err == nil {
		t.Fatalf("REV-STOR-002: expected OpenWithOptions() to return compaction error when rewriteAOFLocked fails on startup, got nil")
	}
}

func BenchmarkStorage_ParsePKValue(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ParsePKValue("1002345", storage.TypeInt)
	}
}

// TestGroup2_IndexKeyCase tests BUG-INDEX-KEY-CASE-1
func TestGroup2_IndexKeyCase(t *testing.T) {
	e := New()
	defer e.Close()

	schema := &storage.TableSchema{
		Name: "Products",
		Columns: []storage.Column{
			{Name: "ProductID", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "CategoryName", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{
				Name:    "idx_products_cat",
				Columns: []string{"CategoryName"},
			},
		},
	}
	if err := e.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, err := e.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	// Insert with different case
	rec := storage.Record{
		"productid":    storage.IntValue(10),
		"categoryname": storage.StringValue("Electronics"),
	}
	if err := tx.Insert("Products", rec); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx 2 failed: %v", err)
	}
	defer tx2.Rollback()
	cur, err := tx2.IndexScan("Products", "idx_products_cat", nil, nil)
	if err != nil {
		t.Fatalf("IndexScan failed: %v", err)
	}
	defer cur.Close()

	if !cur.Next() {
		t.Fatalf("expected 1 row in index scan, got 0")
	}
	r, err := cur.Record()
	if err != nil {
		t.Fatalf("cur.Record failed: %v", err)
	}
	v, ok := r.Get("CategoryName")
	if !ok || v.SVal != "Electronics" {
		t.Fatalf("expected Electronics, got %+v", v)
	}
}

// TestGroup2_FKCaseAndChildCase tests BUG-FK-CASE-1, BUG-FK-CHILD-CASE-1, and BUG-FK-PARENT-TABLE-CASE-1
func TestGroup2_FKCaseAndChildCase(t *testing.T) {
	e := New()
	defer e.Close()

	parentSchema := &storage.TableSchema{
		Name: "Departments",
		Columns: []storage.Column{
			{Name: "DeptID", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "DeptName", Type: storage.TypeVarChar},
		},
	}
	if err := e.CreateTable(parentSchema); err != nil {
		t.Fatalf("CreateTable Departments failed: %v", err)
	}

	childSchema := &storage.TableSchema{
		Name: "Employees",
		Columns: []storage.Column{
			{Name: "EmpID", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "EmpDeptID", Type: storage.TypeInt},
		},
		ForeignKeys: []storage.ForeignKeySchema{
			{
				Name:          "fk_emp_dept",
				ParentTable:   "departments", // lowercase parent table
				ChildColumns:  []string{"empdeptid"}, // lowercase child col
				ParentColumns: []string{"deptid"},    // lowercase parent col
				OnDelete:      storage.ActionRestrict,
			},
		},
	}
	if err := e.CreateTable(childSchema); err != nil {
		t.Fatalf("CreateTable Employees failed: %v", err)
	}

	sess, err := e.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	// 1. Insert parent
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := tx.Insert("Departments", storage.Record{"DeptID": storage.IntValue(1), "DeptName": storage.StringValue("HR")}); err != nil {
		t.Fatalf("Insert parent failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit parent failed: %v", err)
	}

	// 2. Insert invalid child FK (dept 999) with mixed case -> must fail with FK violation
	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx 2 failed: %v", err)
	}
	badChild := storage.Record{
		"EmpID":     storage.IntValue(100),
		"EmpDeptID": storage.IntValue(999),
	}
	if err := tx2.Insert("Employees", badChild); err == nil {
		t.Fatalf("expected FK violation for invalid parent, got nil")
	}
	tx2.Rollback()

	// 3. Insert valid child FK (dept 1) with uppercase col -> must succeed
	tx3, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx 3 failed: %v", err)
	}
	goodChild := storage.Record{
		"EmpID":     storage.IntValue(100),
		"EmpDeptID": storage.IntValue(1),
	}
	if err := tx3.Insert("Employees", goodChild); err != nil {
		t.Fatalf("Insert valid child failed: %v", err)
	}
	if err := tx3.Commit(); err != nil {
		t.Fatalf("Commit valid child failed: %v", err)
	}

	// 4. Try to delete referenced parent -> must be restricted
	tx4, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx 4 failed: %v", err)
	}
	if err := tx4.Delete("Departments", storage.IntValue(1)); err == nil {
		t.Fatalf("expected ON DELETE RESTRICT failure, got nil")
	}
	tx4.Rollback()
}

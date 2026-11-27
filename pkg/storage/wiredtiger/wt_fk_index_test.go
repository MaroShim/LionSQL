//go:build cgo && wiredtiger

package wiredtiger

import (
	"errors"
	"testing"

	"lionsql/pkg/storage"
)

// TestWTTx_ForeignKey_NonPK_IndexScan tests that foreign keys referencing
// non-PK parent unique columns correctly utilize secondary indexes and enforce constraints.
func TestWTTx_ForeignKey_NonPK_IndexScan(t *testing.T) {
	dir := t.TempDir()
	eng := New()
	if err := eng.Open(dir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	// 1. Parent table: departments (id PK, code unique)
	deptSchema := &storage.TableSchema{
		Name: "departments",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "code", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_dept_code", TableName: "departments", Columns: []string{"code"}, Unique: true},
		},
	}
	if err := eng.CreateTable(deptSchema); err != nil {
		t.Fatalf("CreateTable departments failed: %v", err)
	}

	// 2. Child table: employees (dept_code references departments(code))
	empSchema := &storage.TableSchema{
		Name: "employees",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "dept_code", Type: storage.TypeVarChar},
		},
		ForeignKeys: []storage.ForeignKeySchema{
			{
				Name:          "fk_emp_dept",
				ChildTable:    "employees",
				ChildColumns:  []string{"dept_code"},
				ParentTable:   "departments",
				ParentColumns: []string{"code"},
			},
		},
	}
	if err := eng.CreateTable(empSchema); err != nil {
		t.Fatalf("CreateTable employees failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	// Seed parent departments
	tx0, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = tx0.Insert("departments", storage.Record{"id": storage.IntValue(1), "code": storage.StringValue("ENG")})
	_ = tx0.Insert("departments", storage.Record{"id": storage.IntValue(2), "code": storage.StringValue("SALES")})
	if err := tx0.Commit(); err != nil {
		t.Fatal(err)
	}

	// 3. Child insert with existing dept_code "ENG" -> must succeed
	tx1, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx1.Insert("employees", storage.Record{
		"id":        storage.IntValue(101),
		"name":      storage.StringValue("Alice"),
		"dept_code": storage.StringValue("ENG"),
	}); err != nil {
		t.Fatalf("valid FK insert failed: %v", err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("tx1 commit failed: %v", err)
	}

	// 4. Child insert with non-existent dept_code "GHOST" -> must fail with ErrForeignKeyViolation
	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx2.Rollback()

	err = tx2.Insert("employees", storage.Record{
		"id":        storage.IntValue(102),
		"name":      storage.StringValue("Bob"),
		"dept_code": storage.StringValue("GHOST"),
	})
	if err == nil || !errors.Is(err, storage.ErrForeignKeyViolation) {
		t.Fatalf("expected ErrForeignKeyViolation on invalid FK, got: %v", err)
	}
}

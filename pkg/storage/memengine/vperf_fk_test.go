package memengine_test

import (
	"testing"

	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestVPerf_FKReverseCache(t *testing.T) {
	engine := memengine.New()

	// Initial check on non-existent or empty table
	refs := engine.GetChildFKRefs("parent_tab")
	if len(refs) != 0 {
		t.Fatalf("expected 0 child FK refs, got %d", len(refs))
	}

	parentSchema := &storage.TableSchema{
		Name: "parent_tab",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
		},
	}
	if err := engine.CreateTable(parentSchema); err != nil {
		t.Fatalf("CreateTable parent failed: %v", err)
	}

	childSchema := &storage.TableSchema{
		Name: "child_tab",
		Columns: []storage.Column{
			{Name: "cid", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "pid", Type: storage.TypeInt},
		},
		ForeignKeys: []storage.ForeignKeySchema{
			{
				Name:          "fk_child_parent",
				ChildColumns:  []string{"pid"},
				ParentTable:   "parent_tab",
				ParentColumns: []string{"id"},
			},
		},
	}
	if err := engine.CreateTable(childSchema); err != nil {
		t.Fatalf("CreateTable child failed: %v", err)
	}

	// Verify reverse FK cache indexed child_tab under parent_tab
	refsAfter := engine.GetChildFKRefs("parent_tab")
	if len(refsAfter) != 1 {
		t.Fatalf("expected 1 child FK ref for parent_tab, got %d", len(refsAfter))
	}
	if refsAfter[0].ChildTable != "child_tab" || refsAfter[0].FK.Name != "fk_child_parent" {
		t.Fatalf("unexpected child FK ref: %+v", refsAfter[0])
	}

	// Another table with no child references
	otherRefs := engine.GetChildFKRefs("child_tab")
	if len(otherRefs) != 0 {
		t.Fatalf("expected 0 child FK refs for child_tab, got %d", len(otherRefs))
	}
}

func TestForeignKey_NonPK_ParentRowLocking(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	// 1. Setup parent table with non-PK UNIQUE column
	parentSchema := &storage.TableSchema{
		Name: "companies",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "code", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(parentSchema); err != nil {
		t.Fatalf("CreateTable parent failed: %v", err)
	}
	if err := eng.CreateIndex(&storage.IndexSchema{
		Name:      "idx_companies_code",
		TableName: "companies",
		Columns:   []string{"code"},
		Unique:    true,
	}); err != nil {
		t.Fatalf("CreateIndex failed: %v", err)
	}

	// 2. Setup child table referencing non-PK UNIQUE column 'code'
	childSchema := &storage.TableSchema{
		Name: "employees",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "comp_code", Type: storage.TypeVarChar},
		},
		ForeignKeys: []storage.ForeignKeySchema{
			{
				Name:          "fk_emp_comp",
				ChildColumns:  []string{"comp_code"},
				ParentTable:   "companies",
				ParentColumns: []string{"code"},
			},
		},
	}
	if err := eng.CreateTable(childSchema); err != nil {
		t.Fatalf("CreateTable child failed: %v", err)
	}

	sess1, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession 1 failed: %v", err)
	}
	defer sess1.Close()

	sess2, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession 2 failed: %v", err)
	}
	defer sess2.Close()

	// 3. Seed parent row (10, 'COMP_A')
	txInit, _ := sess1.BeginTx(storage.TxOptions{})
	_ = txInit.Insert("companies", storage.Record{"id": storage.IntValue(10), "code": storage.StringValue("COMP_A")})
	if err := txInit.Commit(); err != nil {
		t.Fatalf("Seed commit failed: %v", err)
	}

	// 4. T1 inserts child referencing non-PK 'COMP_A'
	tx1, err := sess1.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx tx1 failed: %v", err)
	}
	defer tx1.Rollback()

	err = tx1.Insert("employees", storage.Record{"id": storage.IntValue(1), "comp_code": storage.StringValue("COMP_A")})
	if err != nil {
		t.Fatalf("Insert child failed: %v", err)
	}

	// 5. Concurrent T2 attempts to DELETE the referenced parent row 10
	tx2, err := sess2.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx tx2 failed: %v", err)
	}
	defer tx2.Rollback()

	err = tx2.Delete("companies", storage.IntValue(10))
	if err == nil {
		t.Fatalf("FATAL: T2 was able to delete referenced parent row while T1 is referencing it via non-PK FK! Expected ErrSerializationFailure")
	}

	// 6. T1 commits successfully
	if err := tx1.Commit(); err != nil {
		t.Fatalf("tx1 commit failed: %v", err)
	}
}

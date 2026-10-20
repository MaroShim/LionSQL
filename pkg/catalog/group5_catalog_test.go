package catalog_test

import (
	"os"
	"path/filepath"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestCatalog_ObjectsPersistenceAndReload(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_catalog_objs_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng1 := memengine.New()
	defer eng1.Close()
	cat1 := catalog.New(eng1)

	if err := cat1.SetPersistenceDir(tmpDir); err != nil {
		t.Fatalf("SetPersistenceDir failed: %v", err)
	}

	// 1. Register objects
	proc := &catalog.Procedure{
		Name: "CalculateBonus",
		Body: "CREATE PROCEDURE CalculateBonus AS BEGIN RETURN 10; END;",
	}
	if err := cat1.CreateProcedure(proc); err != nil {
		t.Fatalf("CreateProcedure failed: %v", err)
	}

	fn := &catalog.Function{
		Name:       "AddTax",
		ReturnType: storage.TypeInt,
		Body:       "CREATE FUNCTION AddTax(@val INT) RETURNS INT AS BEGIN RETURN @val + 1; END;",
	}
	if err := cat1.RegisterFunction(fn); err != nil {
		t.Fatalf("RegisterFunction failed: %v", err)
	}

	trig := &catalog.Trigger{
		Name:   "trg_audit",
		Table:  "orders",
		Timing: "AFTER",
		Events: []string{"INSERT"},
		Body:   "CREATE TRIGGER trg_audit ON orders AFTER INSERT AS BEGIN PRINT '1'; END;",
	}
	if err := cat1.RegisterTrigger(trig); err != nil {
		t.Fatalf("RegisterTrigger failed: %v", err)
	}

	vw := &catalog.View{
		Name:  "v_active_orders",
		Query: "SELECT * FROM orders WHERE status = 'active'",
	}
	if err := cat1.CreateView(vw); err != nil {
		t.Fatalf("CreateView failed: %v", err)
	}

	// Verify catalog_objects.json exists
	objsFile := filepath.Join(tmpDir, "catalog_objects.json")
	if _, err := os.Stat(objsFile); err != nil {
		t.Fatalf("expected catalog_objects.json to exist on disk: %v", err)
	}

	// 2. Open brand new catalog with same persistence directory
	eng2 := memengine.New()
	defer eng2.Close()
	cat2 := catalog.New(eng2)

	if err := cat2.SetPersistenceDir(tmpDir); err != nil {
		t.Fatalf("cat2 SetPersistenceDir failed: %v", err)
	}

	// Verify all 4 objects reloaded
	p, err := cat2.GetProcedure("calculatebonus")
	if err != nil || p == nil {
		t.Fatalf("failed to reload procedure: %v", err)
	}
	if p.Name != "CalculateBonus" {
		t.Errorf("procedure name mismatch: %s", p.Name)
	}

	f, err := cat2.GetFunction("addtax")
	if err != nil || f == nil {
		t.Fatalf("failed to reload function: %v", err)
	}
	if f.ReturnType != storage.TypeInt {
		t.Errorf("function return type mismatch: %v", f.ReturnType)
	}

	trigs := cat2.GetTableTriggers("orders")
	if len(trigs) != 1 || trigs[0].Name != "trg_audit" {
		t.Fatalf("failed to reload trigger: %+v", trigs)
	}

	v, err := cat2.GetView("v_active_orders")
	if err != nil || v == nil {
		t.Fatalf("failed to reload view: %v", err)
	}
	if v.Query != vw.Query {
		t.Errorf("view query mismatch: %s", v.Query)
	}

	// 3. Drop view & procedure, verify disk updates
	if err := cat2.DropView("v_active_orders"); err != nil {
		t.Fatalf("DropView failed: %v", err)
	}
	if err := cat2.DropProcedure("CalculateBonus"); err != nil {
		t.Fatalf("DropProcedure failed: %v", err)
	}

	// Open third catalog to verify deletions persisted
	cat3 := catalog.New(eng2)
	if err := cat3.SetPersistenceDir(tmpDir); err != nil {
		t.Fatalf("cat3 SetPersistenceDir failed: %v", err)
	}

	if _, err := cat3.GetProcedure("CalculateBonus"); err == nil {
		t.Errorf("expected dropped procedure to not exist in cat3")
	}
	if _, err := cat3.GetView("v_active_orders"); err == nil {
		t.Errorf("expected dropped view to not exist in cat3")
	}
	// Function and Trigger should still exist
	if _, err := cat3.GetFunction("AddTax"); err != nil {
		t.Errorf("expected AddTax function to still exist: %v", err)
	}
}

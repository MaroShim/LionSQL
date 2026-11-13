package runtime_test

import (
	"testing"

	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestVM_Insert_SyntheticHiddenRowID(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	// Create table WITHOUT explicit primary key
	schema := &storage.TableSchema{
		Name: "test_no_pk",
		Columns: []storage.Column{
			{Name: "val", Type: storage.TypeVarChar},
		},
	}
	schema.EnsurePrimaryKey("_row_id")
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
	defer tx.Rollback()

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)
	executor.SetTableGetter(eng.GetTable)

	// OpInsert without providing _row_id
	prog := &proc.Program{
		Name: "TestInsertNoPK",
	}
	aux1 := prog.AddAux(&proc.DMLData{
		Table:   "test_no_pk",
		Columns: []string{"val"},
		ValExprs: []proc.Expr{
			&proc.LiteralExpr{Val: storage.StringValue("row_one")},
		},
	})
	aux2 := prog.AddAux(&proc.DMLData{
		Table:   "test_no_pk",
		Columns: []string{"val"},
		ValExprs: []proc.Expr{
			&proc.LiteralExpr{Val: storage.StringValue("row_two")},
		},
	})
	prog.Instructions = []proc.Instruction{
		{Op: proc.OpInsert, Aux: aux1},
		{Op: proc.OpInsert, Aux: aux2},
		{Op: proc.OpHalt},
	}

	_, _, err = executor.RunInTx(prog, nil, tx, nil)
	if err != nil {
		t.Fatalf("RunInTx with synthetic row id failed: %v", err)
	}

	// Verify both rows exist with distinct _row_id values
	rec1, err := tx.Get("test_no_pk", storage.IntValue(1))
	if err != nil {
		t.Fatalf("tx.Get row 1 failed: %v", err)
	}
	if v, _ := rec1.Get("val"); v.SVal != "row_one" {
		t.Fatalf("expected val='row_one', got %v", v.SVal)
	}

	rec2, err := tx.Get("test_no_pk", storage.IntValue(2))
	if err != nil {
		t.Fatalf("tx.Get row 2 failed: %v", err)
	}
	if v, _ := rec2.Get("val"); v.SVal != "row_two" {
		t.Fatalf("expected val='row_two', got %v", v.SVal)
	}
}

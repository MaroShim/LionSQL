package runtime_test

import (
	"testing"

	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestRegression_BUG_VM_DML_COL_CASE_1_DMLColumnCasing(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "Products",
		Columns: []storage.Column{
			{Name: "ProductID", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "ProductName", Type: storage.TypeVarChar},
			{Name: "UnitPrice", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)
	executor.SetTableGetter(eng.GetTable)

	// 1. Execute OpInsert with lowercase column names
	insertProg := &proc.Program{
		Name: "TestInsert",
	}
	auxInsert := insertProg.AddAux(&proc.DMLData{
		Table:   "Products",
		Columns: []string{"productid", "productname", "unitprice"},
		ValExprs: []proc.Expr{
			&proc.LiteralExpr{Val: storage.IntValue(10)},
			&proc.LiteralExpr{Val: storage.StringValue("Widget A")},
			&proc.LiteralExpr{Val: storage.IntValue(100)},
		},
	})
	insertProg.Instructions = []proc.Instruction{
		{
			Op:  proc.OpInsert,
			Aux: auxInsert,
		},
		{Op: proc.OpHalt},
	}

	_, _, err = executor.RunInTx(insertProg, nil, tx, nil)
	if err != nil {
		t.Fatalf("RunInTx Insert failed: %v", err)
	}

	// 2. Execute OpUpdate with mixed case column assignment
	updateProg := &proc.Program{
		Name: "TestUpdate",
	}
	auxUpdate := updateProg.AddAux(&proc.DMLData{
		Table:    "Products",
		WhereCol: "ProductID",
		WhereVal: &proc.LiteralExpr{Val: storage.IntValue(10)},
		Assignments: []proc.UpdateAssignment{
			{
				Column: "unitprice", // lowercase
				Value:  &proc.LiteralExpr{Val: storage.IntValue(250)},
			},
		},
	})
	updateProg.Instructions = []proc.Instruction{
		{
			Op:  proc.OpUpdate,
			Aux: auxUpdate,
		},
		{Op: proc.OpHalt},
	}

	_, _, err = executor.RunInTx(updateProg, nil, tx, nil)
	if err != nil {
		t.Fatalf("RunInTx Update failed: %v", err)
	}

	// Verify record in storage has updated UnitPrice = 250
	rec, err := tx.Get("Products", storage.IntValue(10))
	if err != nil {
		t.Fatalf("tx.Get failed: %v", err)
	}
	val, ok := rec.Get("UnitPrice")
	if !ok || val.IVal != 250 {
		t.Fatalf("expected UnitPrice=250, got ok=%v, val=%+v", ok, val)
	}
}

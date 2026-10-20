package runtime_test

import (
	"testing"

	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// TestGroup3_VDBE_DML_KeyNotFound_ZeroCount tests BUG-VM-DML-PK-NOT-FOUND-CRASH-1:
// Updating or deleting a non-existent PK in VDBE must not error or crash; it should return rowCount = 0.
func TestGroup3_VDBE_DML_KeyNotFound_ZeroCount(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "Accounts",
		Columns: []storage.Column{
			{Name: "AccountID", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "Balance", Type: storage.TypeInt},
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

	// 1. OpUpdate on non-existent PK (AccountID = 99999)
	updateProg := &proc.Program{Name: "TestUpdateNonExistent"}
	auxUpdate := updateProg.AddAux(&proc.DMLData{
		Table:    "Accounts",
		WhereCol: "AccountID",
		WhereVal: &proc.LiteralExpr{Val: storage.IntValue(99999)},
		Assignments: []proc.UpdateAssignment{
			{Column: "Balance", Value: &proc.LiteralExpr{Val: storage.IntValue(500)}},
		},
	})
	updateProg.Instructions = []proc.Instruction{
		{
			Op:  proc.OpUpdate,
			Aux: auxUpdate,
		},
		{
			Op: proc.OpHalt,
		},
	}

	res, err := executor.ExecuteInTx(updateProg, nil, tx, nil)
	if err != nil {
		t.Fatalf("OpUpdate on non-existent PK should NOT return error, got: %v", err)
	}
	if res.RowsAffected != 0 {
		t.Fatalf("expected RowsAffected 0 for non-existent PK update, got: %d", res.RowsAffected)
	}

	// 2. OpDelete on non-existent PK (AccountID = 99999)
	deleteProg := &proc.Program{Name: "TestDeleteNonExistent"}
	auxDelete := deleteProg.AddAux(&proc.DMLData{
		Table:    "Accounts",
		WhereCol: "AccountID",
		WhereVal: &proc.LiteralExpr{Val: storage.IntValue(99999)},
	})
	deleteProg.Instructions = []proc.Instruction{
		{
			Op:  proc.OpDelete,
			Aux: auxDelete,
		},
		{
			Op: proc.OpHalt,
		},
	}

	resDel, err := executor.ExecuteInTx(deleteProg, nil, tx, nil)
	if err != nil {
		t.Fatalf("OpDelete on non-existent PK should NOT return error, got: %v", err)
	}
	if resDel.RowsAffected != 0 {
		t.Fatalf("expected RowsAffected 0 for non-existent PK delete, got: %d", resDel.RowsAffected)
	}
}

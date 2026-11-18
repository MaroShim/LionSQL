package runtime_test

import (
	"errors"
	"testing"

	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

type mockConflictTx struct {
	storage.Transaction
	failOnGet bool
}

func (m *mockConflictTx) Get(tableName string, pk storage.Value) (storage.Record, error) {
	if m.failOnGet {
		return nil, storage.ErrSerializationFailure
	}
	return m.Transaction.Get(tableName, pk)
}

func TestVM_ExecUpdateWhere_PropagatesGetError(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	tblSchema := &storage.TableSchema{
		Name: "t_test",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(tblSchema); err != nil {
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

	// Insert row
	rec := storage.Record{"id": storage.IntValue(1), "val": storage.StringValue("hello")}
	if err := tx.Insert("t_test", rec); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)
	executor.SetTableGetter(eng.GetTable)

	// UPDATE t_test SET val = 'world' WHERE id = 1
	progWhere := &proc.Program{
		Name: "TestWhereUpdateConflict",
	}
	auxWhere := progWhere.AddAux(&proc.DMLData{
		Table: "t_test",
		WhereExpr: &proc.BinaryExpr{
			Left:  &proc.ColumnRefExpr{Name: "id"},
			Op:    proc.TokenEq,
			Right: &proc.LiteralExpr{Val: storage.IntValue(1)},
		},
		Assignments: []proc.UpdateAssignment{
			{Column: "val", Value: &proc.LiteralExpr{Val: storage.StringValue("world")}},
		},
	})
	progWhere.Instructions = []proc.Instruction{
		{
			Op:  proc.OpUpdateWhere,
			Aux: auxWhere,
		},
		{Op: proc.OpHalt},
	}

	// 1. Without conflict, update succeeds
	_, _, err = executor.RunInTx(progWhere, nil, tx, nil)
	if err != nil {
		t.Fatalf("expected update to succeed, got: %v", err)
	}

	// 2. With mock conflict on Get, update MUST propagate error, NOT return (0 rows, nil)
	wrappedTx := &mockConflictTx{Transaction: tx, failOnGet: true}
	_, _, errConflict := executor.RunInTx(progWhere, nil, wrappedTx, nil)
	if errConflict == nil {
		t.Fatalf("CRITICAL: execUpdateWhere swallowed Get conflict error and returned nil error!")
	}
	if !errors.Is(errConflict, storage.ErrSerializationFailure) {
		t.Fatalf("expected ErrSerializationFailure, got: %v", errConflict)
	}
}

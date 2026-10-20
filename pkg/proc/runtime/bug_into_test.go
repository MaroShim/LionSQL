package runtime_test

import (
	"testing"

	"sqlion/pkg/proc"
	"sqlion/pkg/proc/runtime"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
)

func TestRegression_BUG_VM_INTO_1_DeterministicColumnBinding(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	err := eng.CreateTable(&storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "code", Type: storage.TypeVarChar},
			{Name: "description", Type: storage.TypeVarChar},
			{Name: "price", Type: storage.TypeInt},
		},
	})
	if err != nil {
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
	_ = tx.Insert("items", storage.Record{
		"id":          storage.IntValue(1),
		"code":        storage.StringValue("SKU-100"),
		"description": storage.StringValue("Super Widget"),
		"price":       storage.IntValue(999),
	})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Program with OpRelQuery selecting multiple columns into target var
	prog := &proc.Program{
		Name:      "TestDeterministicInto",
		Type:      "PROCEDURE",
		SlotCount: 1,
		VarNames:  []string{"target"},
		VarSlots:  map[string]int32{"target": 0},
	}
	auxIdx := prog.AddAux(&proc.RelQueryData{
		SQL:         "SELECT code, description, price FROM items WHERE id = 1;",
		IntoVar:     "target",
		Projections: []string{"code", "description", "price"},
	})
	prog.Instructions = []proc.Instruction{
		{
			Op:  proc.OpRelQuery,
			Aux: auxIdx,
		},
		{
			Op: proc.OpLoadVar,
			A:  0,
			B:  0,
		},
		{
			Op: proc.OpReturn,
			A:  0,
		},
	}

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)
	executor.SetTableGetter(eng.GetTable)

	for i := 0; i < 20; i++ {
		res, err := executor.Run(prog, "TestDeterministicInto", nil, session)
		if err != nil {
			t.Fatalf("run %d failed: %v", i, err)
		}
		if res.ReturnValue != "SKU-100" {
			t.Fatalf("run %d: expected 'SKU-100' (first column), got %v", i, res.ReturnValue)
		}
	}
}

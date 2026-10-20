package runtime_test

import (
	"testing"

	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestVM_VariableSlotIndexing(t *testing.T) {
	sql := `
CREATE PROCEDURE sp_TestSlots
    @a INT,
    @b INT
AS
BEGIN
    DECLARE @c INT;
    DECLARE @d INT;
    SET @c = @a + @b;
    SET @d = @c * 2;
    RETURN @d;
END;
`
	lexer := proc.NewLexer(sql)
	parser := proc.NewParser(lexer)
	stmt, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("ParseProcedure failed: %v", err)
	}

	prog, err := proc.CompileProcedure(stmt)
	if err != nil {
		t.Fatalf("CompileProcedure failed: %v", err)
	}

	// 1. Verify compiler allocated slots for @a, @b, @c, @d
	if prog.SlotCount < 4 {
		t.Fatalf("expected at least 4 slots, got %d", prog.SlotCount)
	}
	if prog.VarSlots == nil {
		t.Fatalf("expected non-nil VarSlots map")
	}

	for _, expectedVar := range []string{"a", "b", "c", "d"} {
		slot, exists := prog.VarSlots[expectedVar]
		if !exists {
			t.Fatalf("expected slot for variable %q, but not found", expectedVar)
		}
		if slot < 0 || slot >= prog.SlotCount {
			t.Fatalf("invalid slot index %d for %q (total slots %d)", slot, expectedVar, prog.SlotCount)
		}
	}

	// 2. Verify OpLoadVar and OpStoreVar instructions have valid slot indices in inst.B
	hasLoadSlot := false
	hasStoreSlot := false
	for _, inst := range prog.Instructions {
		if inst.Op == proc.OpLoadVar {
			if inst.B >= 0 && inst.B < prog.SlotCount {
				hasLoadSlot = true
			}
		}
		if inst.Op == proc.OpStoreVar {
			if inst.B >= 0 && inst.B < prog.SlotCount {
				hasStoreSlot = true
			}
		}
	}
	if !hasLoadSlot {
		t.Fatalf("expected at least one OpLoadVar with valid slot index in inst.B")
	}
	if !hasStoreSlot {
		t.Fatalf("expected at least one OpStoreVar with valid slot index in inst.B")
	}

	// 3. Execute and verify O(1) slot evaluation produces correct result
	eng := memengine.New()
	defer eng.Close()

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	cache := runtime.NewPlanCache()
	cache.PutByName(stmt.Name, prog)
	executor := runtime.NewExecutor(cache)

	res, err := executor.Run(prog, stmt.Name, map[string]any{
		"a": 15,
		"b": 25,
	}, session)
	if err != nil {
		t.Fatalf("executor.Run failed: %v", err)
	}

	expectedResult := int64(80) // (15 + 25) * 2 = 80
	if res.ReturnValue != expectedResult {
		t.Fatalf("expected return value %d, got %v", expectedResult, res.ReturnValue)
	}
}

func TestVM_VariableSlotSelectIntoAndDynamicAccess(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	_ = eng.CreateTable(&storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "price", Type: storage.TypeInt},
		},
	})

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	tx, _ := session.BeginTx(storage.TxOptions{})
	_ = tx.Insert("items", storage.Record{
		"id":    storage.IntValue(10),
		"price": storage.IntValue(250),
	})
	_ = tx.Commit()

	sql := `
CREATE PROCEDURE sp_TestSelectIntoSlot
    @itemId INT
AS
BEGIN
    DECLARE @foundPrice INT;
    SELECT price INTO @foundPrice FROM items WHERE id = @itemId;
    RETURN @foundPrice + 50;
END;
`
	lexer := proc.NewLexer(sql)
	parser := proc.NewParser(lexer)
	stmt, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("ParseProcedure failed: %v", err)
	}

	prog, err := proc.CompileProcedure(stmt)
	if err != nil {
		t.Fatalf("CompileProcedure failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)

	res, err := executor.Run(prog, stmt.Name, map[string]any{
		"itemId": 10,
	}, session)
	if err != nil {
		t.Fatalf("executor.Run failed: %v", err)
	}

	expectedResult := int64(300) // 250 + 50
	if res.ReturnValue != expectedResult {
		t.Fatalf("expected return value %d, got %v", expectedResult, res.ReturnValue)
	}
}

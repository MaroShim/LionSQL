package runtime_test

import (
	"testing"

	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage/memengine"
)

func TestVM_OpcodeSpecialization(t *testing.T) {
	sql := `
CREATE PROCEDURE sp_TestSpecialization
    @n INT,
    @threshold INT
AS
BEGIN
    DECLARE @total INT;
    DECLARE @diff INT;
    DECLARE @prod INT;
    DECLARE @is_valid INT;

    SET @total = @n + 10;
    SET @diff = @total - 5;
    SET @prod = @diff * 3;

    IF @prod > @threshold AND @n >= 1
    BEGIN
        SET @is_valid = 1;
    END
    ELSE
    BEGIN
        SET @is_valid = 0;
    END;

    RETURN @is_valid;
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

	// 1. Verify specialized opcodes were emitted in bytecode
	hasAddInt := false
	hasSubInt := false
	hasMulInt := false
	hasGtInt := false
	hasGteInt := false
	hasAnd := false

	for _, inst := range prog.Instructions {
		switch inst.Op {
		case proc.OpAddInt:
			hasAddInt = true
		case proc.OpSubInt:
			hasSubInt = true
		case proc.OpMulInt:
			hasMulInt = true
		case proc.OpGtInt:
			hasGtInt = true
		case proc.OpGteInt:
			hasGteInt = true
		case proc.OpAnd:
			hasAnd = true
		}
	}

	if !hasAddInt {
		t.Errorf("expected OpAddInt in compiled program, but not found")
	}
	if !hasSubInt {
		t.Errorf("expected OpSubInt in compiled program, but not found")
	}
	if !hasMulInt {
		t.Errorf("expected OpMulInt in compiled program, but not found")
	}
	if !hasGtInt {
		t.Errorf("expected OpGtInt in compiled program, but not found")
	}
	if !hasGteInt {
		t.Errorf("expected OpGteInt in compiled program, but not found")
	}
	if !hasAnd {
		t.Errorf("expected OpAnd in compiled program, but not found")
	}

	// 2. Execute program on In-Memory engine and verify execution correctness
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("failed to open memengine: %v", err)
	}
	defer eng.Close()

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	defer sess.Close()

	cache := runtime.NewPlanCache()
	cache.PutByName(stmt.Name, prog)
	exec := runtime.NewExecutor(cache)
	exec.SetTableGetter(eng.GetTable)

	// Case A: @n = 5, @threshold = 20
	// @total = 5 + 10 = 15
	// @diff = 15 - 5 = 10
	// @prod = 10 * 3 = 30
	// 30 > 20 AND 5 >= 1 -> TRUE -> return 1
	resA, err := exec.Run(prog, stmt.Name, map[string]any{
		"n":         5,
		"threshold": 20,
	}, sess)
	if err != nil {
		t.Fatalf("Run Case A failed: %v", err)
	}
	if resA.ReturnValue != int64(1) {
		t.Errorf("Case A: expected return 1, got %v", resA.ReturnValue)
	}

	// Case B: @n = 0, @threshold = 20
	// @total = 0 + 10 = 10
	// @diff = 10 - 5 = 5
	// @prod = 5 * 3 = 15
	// 15 > 20 AND 0 >= 1 -> FALSE -> return 0
	resB, err := exec.Run(prog, stmt.Name, map[string]any{
		"n":         0,
		"threshold": 20,
	}, sess)
	if err != nil {
		t.Fatalf("Run Case B failed: %v", err)
	}
	if resB.ReturnValue != int64(0) {
		t.Errorf("Case B: expected return 0, got %v", resB.ReturnValue)
	}
}

func TestVM_LogicalOperatorsOrAnd(t *testing.T) {
	sql := `
CREATE PROCEDURE sp_TestOrAnd
    @a INT,
    @b INT
AS
BEGIN
    DECLARE @res INT;
    IF @a = 1 OR @b = 1
    BEGIN
        SET @res = 100;
    END
    ELSE
    BEGIN
        SET @res = 200;
    END;
    RETURN @res;
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

	hasOr := false
	hasEqInt := false
	for _, inst := range prog.Instructions {
		if inst.Op == proc.OpOr {
			hasOr = true
		}
		if inst.Op == proc.OpEqInt {
			hasEqInt = true
		}
	}
	if !hasOr {
		t.Errorf("expected OpOr in compiled instructions")
	}
	if !hasEqInt {
		t.Errorf("expected OpEqInt in compiled instructions")
	}

	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	sess, _ := eng.NewSession()
	defer sess.Close()

	cache := runtime.NewPlanCache()
	cache.PutByName(stmt.Name, prog)
	exec := runtime.NewExecutor(cache)
	exec.SetTableGetter(eng.GetTable)

	// a=0, b=1 -> OR is true -> 100
	res1, err := exec.Run(prog, stmt.Name, map[string]any{"a": 0, "b": 1}, sess)
	if err != nil || res1.ReturnValue != int64(100) {
		t.Errorf("expected 100, got %v (err: %v)", res1.ReturnValue, err)
	}

	// a=0, b=0 -> OR is false -> 200
	res2, err := exec.Run(prog, stmt.Name, map[string]any{"a": 0, "b": 0}, sess)
	if err != nil || res2.ReturnValue != int64(200) {
		t.Errorf("expected 200, got %v (err: %v)", res2.ReturnValue, err)
	}
}

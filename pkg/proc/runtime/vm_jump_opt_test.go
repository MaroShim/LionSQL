package runtime_test

import (
	"testing"

	"sqlion/pkg/proc"
	"sqlion/pkg/proc/runtime"
	"sqlion/pkg/storage/memengine"
)

func TestVM_FusedCompareAndBranch(t *testing.T) {
	sql := `
CREATE PROCEDURE sp_TestFusedJump
    @a INT,
    @b INT
AS
BEGIN
    DECLARE @res INT;
    IF @a < @b
    BEGIN
        SET @res = 1;
    END
    ELSE
    BEGIN
        SET @res = 2;
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

	// Invert of @a < @b is @a >= @b -> OpJumpIfGteInt must be emitted directly!
	hasFusedJump := false
	hasLtInt := false
	for _, inst := range prog.Instructions {
		if inst.Op == proc.OpJumpIfGteInt {
			hasFusedJump = true
		}
		if inst.Op == proc.OpLtInt || inst.Op == proc.OpLt {
			hasLtInt = true
		}
	}

	if !hasFusedJump {
		t.Errorf("expected fused OpJumpIfGteInt, but not found")
	}
	if hasLtInt {
		t.Errorf("expected OpLtInt to be eliminated via fused jump, but found it in instructions")
	}

	// Verify runtime execution
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	sess, _ := eng.NewSession()
	defer sess.Close()

	cache := runtime.NewPlanCache()
	cache.PutByName(stmt.Name, prog)
	exec := runtime.NewExecutor(cache)
	exec.SetTableGetter(eng.GetTable)

	// a=5, b=10 -> a < b -> res=1
	res1, err := exec.Run(prog, stmt.Name, map[string]any{"a": 5, "b": 10}, sess)
	if err != nil || res1.ReturnValue != int64(1) {
		t.Fatalf("expected return 1, got %v (err: %v)", res1.ReturnValue, err)
	}

	// a=15, b=10 -> a >= b -> res=2
	res2, err := exec.Run(prog, stmt.Name, map[string]any{"a": 15, "b": 10}, sess)
	if err != nil || res2.ReturnValue != int64(2) {
		t.Fatalf("expected return 2, got %v (err: %v)", res2.ReturnValue, err)
	}
}

func TestVM_WhileLoopFusedJumpAndPeephole(t *testing.T) {
	sql := `
CREATE PROCEDURE sp_TestWhileLoop
    @limit INT
AS
BEGIN
    DECLARE @sum INT;
    DECLARE @i INT;
    SET @sum = 0;
    SET @i = 1;

    WHILE @i <= @limit
    BEGIN
        SET @sum = @sum + @i;
        SET @i = @i + 1;
    END;

    RETURN @sum;
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

	// WHILE @i <= @limit -> inverted condition is @i > @limit -> OpJumpIfGtInt
	hasGtIntJump := false
	for _, inst := range prog.Instructions {
		if inst.Op == proc.OpJumpIfGtInt {
			hasGtIntJump = true
		}
	}
	if !hasGtIntJump {
		t.Errorf("expected OpJumpIfGtInt for WHILE loop exit condition")
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

	// 1 + 2 + ... + 10 = 55
	res, err := exec.Run(prog, stmt.Name, map[string]any{"limit": 10}, sess)
	if err != nil || res.ReturnValue != int64(55) {
		t.Fatalf("expected return 55, got %v (err: %v)", res.ReturnValue, err)
	}
}

func TestVM_PeepholeJumpThreading(t *testing.T) {
	// Program with a trampoline jump:
	// 0: OpJump -> 2
	// 1: OpNop
	// 2: OpJump -> 4
	// 3: OpNop
	// 4: OpReturn (100)
	c := proc.NewCompiler()
	p0 := c.EmitForTest(proc.OpJump, 0, 2, 0, nil)
	c.EmitForTest(proc.OpNop, 0, 0, 0, nil)
	c.EmitForTest(proc.OpJump, 0, 4, 0, nil)
	c.EmitForTest(proc.OpNop, 0, 0, 0, nil)
	c.EmitForTest(proc.OpReturn, -1, 0, 0, nil)

	prog := c.FinalizeProgramForTest()

	// After peephole, Instruction 0 should jump directly to 4!
	if prog.Instructions[p0].B != 4 {
		t.Errorf("expected instruction %d to jump directly to 4 via threading, got %d", p0, prog.Instructions[p0].B)
	}
}

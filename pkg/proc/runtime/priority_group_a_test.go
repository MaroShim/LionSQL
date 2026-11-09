package runtime_test

import (
	"fmt"
	"testing"
	"time"

	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func setupPriorityGroupADB(t *testing.T) (storage.Engine, storage.Session) {
	eng := memengine.New()
	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "holder", Type: storage.TypeVarChar, Nullable: false},
			{Name: "balance", Type: storage.TypeInt, Nullable: false},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	_ = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(1),
		"holder":  storage.StringValue("Alice"),
		"balance": storage.IntValue(1000),
	})
	_ = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(2),
		"holder":  storage.StringValue("Bob"),
		"balance": storage.IntValue(500),
	})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Seed commit failed: %v", err)
	}

	return eng, session
}

// TestPriorityGroupA_CompoundAssignment_Variables tests T-SQL compound assignment operators
// (+=, -=, *=, /=, %=, &=, ^=, |=) on local variables.
func TestPriorityGroupA_CompoundAssignment_Variables(t *testing.T) {
	eng, session := setupPriorityGroupADB(t)
	defer eng.Close()
	defer session.Close()

	script := `
CREATE PROCEDURE TestCompoundOps
AS
BEGIN
    DECLARE @x INT = 10;
    SET @x += 5;   -- 15
    SET @x *= 2;   -- 30
    SET @x -= 4;   -- 26
    SET @x /= 2;   -- 13
    SET @x %= 5;   -- 3
    SET @x |= 8;   -- 3 | 8 = 11
    SET @x &= 9;   -- 11 & 9 = 9
    SET @x ^= 1;   -- 9 ^ 1 = 8
    RETURN @x;
END
`

	lexer := proc.NewLexer(script)
	parser := proc.NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("ParseProcedure failed: %v", err)
	}

	prog, err := proc.CompileProcedure(plan)
	if err != nil {
		t.Fatalf("CompileProcedure failed: %v", err)
	}

	executor := runtime.NewExecutor(runtime.NewPlanCache())
	res, err := executor.Run(prog, "TestCompoundOps", nil, session)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if res.ReturnValue != int64(8) {
		t.Fatalf("expected return value 8, got %v (%T)", res.ReturnValue, res.ReturnValue)
	}
}

// TestPriorityGroupA_CompoundAssignment_Update tests T-SQL compound assignment operators
// (+=, -=) in UPDATE statements.
func TestPriorityGroupA_CompoundAssignment_Update(t *testing.T) {
	eng, session := setupPriorityGroupADB(t)
	defer eng.Close()
	defer session.Close()

	script := `
CREATE PROCEDURE TestCompoundUpdate
AS
BEGIN
    UPDATE accounts SET balance += 250 WHERE id = 1;
    UPDATE accounts SET balance -= 150 WHERE id = 2;
    RETURN 0;
END
`

	lexer := proc.NewLexer(script)
	parser := proc.NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("ParseProcedure failed: %v", err)
	}

	prog, err := proc.CompileProcedure(plan)
	if err != nil {
		t.Fatalf("CompileProcedure failed: %v", err)
	}

	executor := runtime.NewExecutor(runtime.NewPlanCache())
	_, err = executor.Run(prog, "TestCompoundUpdate", nil, session)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Verify account 1 has 1000 + 250 = 1250, account 2 has 500 - 150 = 350
	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	rec1, err := tx.Get("accounts", storage.IntValue(1))
	if err != nil {
		t.Fatalf("Get account 1 failed: %v", err)
	}
	if rec1["balance"].IVal != 1250 {
		t.Fatalf("expected balance 1250, got %d", rec1["balance"].IVal)
	}

	rec2, err := tx.Get("accounts", storage.IntValue(2))
	if err != nil {
		t.Fatalf("Get account 2 failed: %v", err)
	}
	if rec2["balance"].IVal != 350 {
		t.Fatalf("expected balance 350, got %d", rec2["balance"].IVal)
	}
}

// TestPriorityGroupA_Waitfor_Delay tests T-SQL WAITFOR DELAY execution.
func TestPriorityGroupA_Waitfor_Delay(t *testing.T) {
	eng, session := setupPriorityGroupADB(t)
	defer eng.Close()
	defer session.Close()

	script := `
CREATE PROCEDURE TestWaitforDelay
AS
BEGIN
    WAITFOR DELAY '00:00:00.030';
    RETURN 42;
END
`

	lexer := proc.NewLexer(script)
	parser := proc.NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("ParseProcedure failed: %v", err)
	}

	prog, err := proc.CompileProcedure(plan)
	if err != nil {
		t.Fatalf("CompileProcedure failed: %v", err)
	}

	executor := runtime.NewExecutor(runtime.NewPlanCache())
	start := time.Now()
	res, err := executor.Run(prog, "TestWaitforDelay", nil, session)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	dur := time.Since(start)

	if res.ReturnValue != int64(42) {
		t.Fatalf("expected return value 42, got %v (%T)", res.ReturnValue, res.ReturnValue)
	}
	if dur < 25*time.Millisecond {
		t.Fatalf("expected at least 25ms delay, got %v", dur)
	}
}

// TestPriorityGroupA_CursorRows tests @@CURSOR_ROWS system variable.
func TestPriorityGroupA_CursorRows(t *testing.T) {
	eng, session := setupPriorityGroupADB(t)
	defer eng.Close()
	defer session.Close()

	script := `
CREATE PROCEDURE TestCursorRows
AS
BEGIN
    DECLARE @rows INT;
    DECLARE cur CURSOR FOR SELECT * FROM accounts;
    OPEN cur;
    SET @rows = @@CURSOR_ROWS;
    CLOSE cur;
    DEALLOCATE cur;
    RETURN @rows;
END
`

	lexer := proc.NewLexer(script)
	parser := proc.NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("ParseProcedure failed: %v", err)
	}

	prog, err := proc.CompileProcedure(plan)
	if err != nil {
		t.Fatalf("CompileProcedure failed: %v", err)
	}

	executor := runtime.NewExecutor(runtime.NewPlanCache())
	res, err := executor.Run(prog, "TestCursorRows", nil, session)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// 2 accounts were seeded
	if res.ReturnValue != int64(2) {
		t.Fatalf("expected @@CURSOR_ROWS to return 2, got %v (%T)", res.ReturnValue, res.ReturnValue)
	}
}

// TestPriorityGroupA_AuditRemediations tests post-audit hardening fixes:
// 1. MinInt64 % -1 integer overflow panic guard ([RULE:DefensiveIndexing])
// 2. WAITFOR DELAY out-of-range validation ([RULE:DefensiveIndexing])
// 3. Multi-cursor @@CURSOR_ROWS preservation when a non-last cursor is closed
func TestPriorityGroupA_AuditRemediations(t *testing.T) {
	eng, session := setupPriorityGroupADB(t)
	defer eng.Close()
	defer session.Close()

	executor := runtime.NewExecutor(runtime.NewPlanCache())

	// 1. MinInt64 % -1 guard
	t.Run("MinInt64ModNegOneGuard", func(t *testing.T) {
		script := `
CREATE PROCEDURE TestMinInt64Mod
AS
BEGIN
    DECLARE @x BIGINT = -9223372036854775808;
    SET @x %= -1;
    RETURN @x;
END
`
		l := proc.NewLexer(script)
		p := proc.NewParser(l)
		plan, err := p.ParseProcedure()
		if err != nil {
			t.Fatalf("ParseProcedure failed: %v", err)
		}
		prog, err := proc.CompileProcedure(plan)
		if err != nil {
			t.Fatalf("CompileProcedure failed: %v", err)
		}
		res, err := executor.Run(prog, "TestMinInt64Mod", nil, session)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		if res.ReturnValue != int64(0) {
			t.Fatalf("expected MinInt64 %% -1 to return 0, got %v", res.ReturnValue)
		}
	})

	// 2. WAITFOR DELAY out-of-range rejection
	t.Run("WaitforDelayValidation", func(t *testing.T) {
		invalidDelays := []string{
			"WAITFOR DELAY '-01:00:00';",
			"WAITFOR DELAY '25:00:00';",
			"WAITFOR DELAY '00:65:00';",
		}
		for _, inv := range invalidDelays {
			script := fmt.Sprintf(`
CREATE PROCEDURE TestWaitforInvalid
AS
BEGIN
    %s
    RETURN 1;
END
`, inv)
			l := proc.NewLexer(script)
			p := proc.NewParser(l)
			plan, err := p.ParseProcedure()
			if err != nil {
				t.Fatalf("ParseProcedure failed for %s: %v", inv, err)
			}
			prog, err := proc.CompileProcedure(plan)
			if err != nil {
				t.Fatalf("CompileProcedure failed for %s: %v", inv, err)
			}
			_, runErr := executor.Run(prog, "TestWaitforInvalid", nil, session)
			if runErr == nil {
				t.Fatalf("expected error for invalid delay %q, got nil", inv)
			}
		}
	})

	// 3. Multi-cursor @@CURSOR_ROWS tracking
	t.Run("MultiCursorRowsPreserved", func(t *testing.T) {
		script := `
CREATE PROCEDURE TestMultiCursorRows
AS
BEGIN
    DECLARE @res INT;
    DECLARE cur1 CURSOR FOR SELECT * FROM accounts;
    DECLARE cur2 CURSOR FOR SELECT * FROM accounts WHERE id = 1;
    OPEN cur1; -- 2 rows
    OPEN cur2; -- 1 row (now last opened)
    CLOSE cur1; -- closing cur1 should NOT reset @@CURSOR_ROWS to 0 because cur2 is last opened
    SET @res = @@CURSOR_ROWS;
    CLOSE cur2; -- closing last opened cur2 should reset to 0
    RETURN @res;
END
`
		l := proc.NewLexer(script)
		p := proc.NewParser(l)
		plan, err := p.ParseProcedure()
		if err != nil {
			t.Fatalf("ParseProcedure failed: %v", err)
		}
		prog, err := proc.CompileProcedure(plan)
		if err != nil {
			t.Fatalf("CompileProcedure failed: %v", err)
		}
		res, err := executor.Run(prog, "TestMultiCursorRows", nil, session)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		if res.ReturnValue != int64(1) {
			t.Fatalf("expected @@CURSOR_ROWS to preserve last opened cursor rows (1), got %v", res.ReturnValue)
		}
	})
}

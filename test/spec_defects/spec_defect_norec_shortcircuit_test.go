package spec_defects

import (
	"context"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

// TestSpecDefect_NoREC_ShortCircuitCommutativeOR verifies:
// Defect ID: DEFECT-ENGINE-NOREC-COMMUTATIVE-OR-1
// Root Cause: In boolean OR expressions, if the left operand encountered an evaluation error (such as arithmetic overflow),
// TokenOr aborted immediately with error without evaluating the right operand. In EvalBoolExpr, this error was swallowed to false,
// causing (overflow OR true) to evaluate to 0 (false) while (true OR overflow) short-circuited to 1 (true),
// violating boolean commutative equivalence (A OR B == B OR A) and causing NoREC discrepancies.
func TestSpecDefect_NoREC_ShortCircuitCommutativeOR(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})
	defer coord.Close()

	sessID := "TestSpecDefect_NoREC_ShortCircuit_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	ctx := context.Background()
	_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE t_fuzz_or (id INT PRIMARY KEY, c1 BIT NOT NULL, c2 INT);")
	_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO t_fuzz_or VALUES (7, 1, 2147483647);")

	// 1. Inverted operand order in CASE WHEN must produce identical true (1) results
	qCase := "SELECT CASE WHEN (c2 + 2 > 42 OR c1 = 1) THEN 1 ELSE 0 END AS r1, CASE WHEN (c1 = 1 OR c2 + 2 > 42) THEN 1 ELSE 0 END AS r2 FROM t_fuzz_or;"
	resCase, err := coord.ExecuteSessionSQLContext(ctx, sessID, qCase)
	if err != nil {
		t.Fatalf("CASE WHEN query failed: %v", err)
	}
	if len(resCase.Rows) == 0 {
		t.Fatalf("expected 1 row, got 0")
	}

	r1 := resCase.Rows[0]["r1"]
	r2 := resCase.Rows[0]["r2"]
	if r1 != int64(1) && r1 != 1 {
		t.Fatalf("TDD RED: r1 (overflow OR true) expected 1, got %v", r1)
	}
	if r2 != int64(1) && r2 != 1 {
		t.Fatalf("TDD RED: r2 (true OR overflow) expected 1, got %v", r2)
	}

	// 2. WHERE count must equal SUM(CASE WHEN ...)
	qWhere := "SELECT COUNT(*) AS total FROM t_fuzz_or WHERE (c2 + 2 > 42 OR c1 = 1);"
	resWhere, err := coord.ExecuteSessionSQLContext(ctx, sessID, qWhere)
	if err != nil {
		t.Fatalf("WHERE query failed: %v", err)
	}
	qSum := "SELECT SUM(CASE WHEN (c2 + 2 > 42 OR c1 = 1) THEN 1 ELSE 0 END) AS total FROM t_fuzz_or;"
	resSum, err := coord.ExecuteSessionSQLContext(ctx, sessID, qSum)
	if err != nil {
		t.Fatalf("SUM query failed: %v", err)
	}

	wTotal := resWhere.Rows[0]["total"]
	sTotal := resSum.Rows[0]["total"]
	if wTotal != sTotal {
		t.Fatalf("NoREC Discrepancy: WHERE total (%v) != SUM total (%v)", wTotal, sTotal)
	}
}

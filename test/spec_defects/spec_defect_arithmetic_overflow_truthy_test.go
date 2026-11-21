package spec_defects

import (
	"context"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

// TestSpecDefect_ArithmeticOverflowTruthy verifies:
// Defect ID: DEFECT-EXEC-ARITHMETIC-OVERFLOW-TRUTHY-EVAL-1
// Root Cause: pkg/execution/eval.go:813 - EvalTupleBoolExprWithContext falls into `default: return true`
// when EvalTupleExprWithContext returns an error (e.g. arithmetic overflow error: bigint out of range).
// This causes runtime arithmetic errors in WHERE clauses to be treated as boolean TRUE and match the row!
func TestSpecDefect_ArithmeticOverflowTruthy(t *testing.T) {
	if testing.Short() {
		t.Skip("TDD RED: DEFECT-EXEC-ARITHMETIC-OVERFLOW-TRUTHY-EVAL-1 - pending resolution sprint")
	}

	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})

	sessID := "TestSpecDefect_ArithmeticOverflowTruthy_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	ctx := context.Background()
	_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE t_ovf (id INT PRIMARY KEY, c1 BIGINT);")
	if err != nil {
		t.Fatalf("setup DDL failed: %v", err)
	}

	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO t_ovf VALUES (1, -9223372036854775808);")
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// 1. Optimized Query (WHERE filter)
	resOpt, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT COUNT(*) AS total FROM t_ovf WHERE ((c1 - 3) > 0);")
	if err != nil {
		t.Fatalf("unexpected query execution failure: %v", err)
	}

	// 2. Unoptimized Query (CASE WHEN projection)
	resUnopt, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT COALESCE(SUM(CASE WHEN ((c1 - 3) > 0) THEN 1 ELSE 0 END), 0) AS total FROM t_ovf;")
	if err != nil {
		t.Fatalf("unexpected unoptimized query failure: %v", err)
	}

	valOpt := resOpt.Rows[0]["total"]
	valUnopt := resUnopt.Rows[0]["total"]

	// The WHERE clause currently matches the row (total=1) because arithmetic overflow error evaluates as true!
	// It should evaluate to 0 matching rows (or return arithmetic overflow error).
	if valOpt != valUnopt {
		t.Fatalf("TDD RED DEFECT-EXEC-ARITHMETIC-OVERFLOW-TRUTHY-EVAL-1: WHERE returned total=%v (overflow treated as truthy), while CASE WHEN returned total=%v", valOpt, valUnopt)
	}
}

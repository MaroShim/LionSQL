package spec_defects

import (
	"context"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

// TestSpecDefect_DecimalFValCorruption verifies:
// Defect ID: DEFECT-VDBE-DECIMAL-FVAL-CORRUPTION-1
// Root Cause: pkg/proc/runtime/vm.go:3841 - toFloatNum reads `v.FVal` for storage.TypeDecimal.
// However, decoded storage records only populate `v.DVal` (storage.Decimal), leaving `v.FVal = 0.0`.
// Consequently, VDBE VM numeric comparisons (OpEq, OpLt, OpGt) evaluate table decimal columns as 0.0!
func TestSpecDefect_DecimalFValCorruption(t *testing.T) {
	if testing.Short() {
		t.Skip("TDD RED: DEFECT-VDBE-DECIMAL-FVAL-CORRUPTION-1 - pending resolution sprint")
	}

	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})

	sessID := "TestSpecDefect_DecimalFValCorruption_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	ctx := context.Background()
	_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE t_dec (id INT PRIMARY KEY, amount DECIMAL(10,2));")
	if err != nil {
		t.Fatalf("setup DDL failed: %v", err)
	}

	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO t_dec VALUES (1, -1.00);")
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// 1. Optimized Query (WHERE COALESCE(amount, -1.00) = -1.00)
	resOpt, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT COUNT(*) AS total FROM t_dec WHERE COALESCE(amount, -1.00) = -1.00;")
	if err != nil {
		t.Fatalf("unexpected query execution failure: %v", err)
	}

	// 2. Unoptimized Query (CASE WHEN projection)
	resUnopt, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT COALESCE(SUM(CASE WHEN COALESCE(amount, -1.00) = -1.00 THEN 1 ELSE 0 END), 0) AS total FROM t_dec;")
	if err != nil {
		t.Fatalf("unexpected unoptimized query failure: %v", err)
	}

	valOpt := resOpt.Rows[0]["total"]
	valUnopt := resUnopt.Rows[0]["total"]

	if valOpt != int64(1) && valOpt != 1 {
		t.Fatalf("TDD RED DEFECT-VDBE-COALESCE-DECIMAL-1: expected WHERE COALESCE(amount, -1.00) = -1.00 to match 1 row, got total=%v (CASE WHEN returned %v)", valOpt, valUnopt)
	}
}

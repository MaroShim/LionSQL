package spec_defects

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

// TestSpecDefect_FloatDecimalScaleTruncation verifies:
// DEFECT-EXEC-FLOAT-DECIMAL-SCALE-TRUNCATION-1:
// In pkg/execution/iterator.go:tryDecimal, float64 values are clamped to scale 4 via storage.NewDecimalFromFloat(n, 4).
// Comparing a FLOAT column with more than 4 decimals (e.g. 3.14159) against a decimal literal truncates to 3.1416,
// causing false inequality in CompareValuesCS and returning wrong rows in WHERE c1 <> 3.14159.
func TestSpecDefect_FloatDecimalScaleTruncation(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})

	sessID := "TestSpecDefect_FloatTrunc_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	// Setup Schema & Data
	_, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE t_float_scale (id INT PRIMARY KEY, c1 FLOAT);")
	if err != nil {
		t.Fatalf("setup DDL failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQL(sessID, "INSERT INTO t_float_scale VALUES (1, 3.14159);"); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// 1. Run Optimized Query (WHERE filter via Volcano)
	resOpt, err := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS total FROM t_float_scale WHERE (c1 <> 3.14159);")
	if err != nil {
		t.Fatalf("optimized query failed: %v", err)
	}

	// 2. Run Unoptimized Query (CASE WHEN evaluation)
	resUnopt, err := coord.ExecuteSessionSQL(sessID, "SELECT COALESCE(SUM(CASE WHEN (c1 <> 3.14159) THEN 1 ELSE 0 END), 0) AS total FROM t_float_scale;")
	if err != nil {
		t.Fatalf("unoptimized query failed: %v", err)
	}

	// 3. Assert Equivalence
	if len(resOpt.Rows) == 0 || len(resUnopt.Rows) == 0 {
		t.Fatalf("empty rows returned")
	}
	rowOpt := resOpt.Rows[0]
	rowUnopt := resUnopt.Rows[0]
	if fmt.Sprintf("%v", rowOpt) != fmt.Sprintf("%v", rowUnopt) {
		t.Fatalf("TDD RED Discrepancy: Optimized (%v) != Unoptimized (%v)", rowOpt, rowUnopt)
	}
}

package spec_defects

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

// TestSpecDefect_BitInListEval verifies:
// DEFECT-PROC-EVAL-BIT-IN-LIST-1:
// In pkg/proc/eval.go, InExpr compares string representations via fmt.Sprintf("%v", val).
// For BIT columns, boolean false formats to "false", but IN-lists contain integer 0 ("0").
// Comparing "false" to "0" evaluates to false in CASE WHEN expressions while passing in WHERE,
// violating semantic parity across execution engines.
func TestSpecDefect_BitInListEval(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})

	sessID := "TestSpecDefect_BitIn_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	// Setup Schema & Data
	_, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE t_bit_in (id INT PRIMARY KEY, flag BIT NOT NULL);")
	if err != nil {
		t.Fatalf("setup DDL failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQL(sessID, "INSERT INTO t_bit_in VALUES (1, 0);"); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// 1. Run Optimized Query (WHERE filter)
	resOpt, err := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS total FROM t_bit_in WHERE (flag IN (0, 0, 1));")
	if err != nil {
		t.Fatalf("optimized query failed: %v", err)
	}

	// 2. Run Unoptimized Query (CASE WHEN evaluation)
	resUnopt, err := coord.ExecuteSessionSQL(sessID, "SELECT COALESCE(SUM(CASE WHEN (flag IN (0, 0, 1)) THEN 1 ELSE 0 END), 0) AS total FROM t_bit_in;")
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

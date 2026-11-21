package spec_defects

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

// TestSpecDefect_BitFloatEquality verifies:
// DEFECT-ENGINE-BIT-FLOAT-LITERAL-EVAL-1:
// In pkg/engine/sqlexec_eval.go, boolean equality checks rightVal == "1" || rightVal == "0"
// using strict string equality. When comparing a BIT column against float literals like 0.0 or 1.0
// (c1 = 0.0), rightVal is "0.0", failing the check and falling back to strings.EqualFold("false", "0.0"),
// which returns false and drops all matching rows in WHERE (total = 0).
// In contrast, Volcano/VDBE expression evaluators coerce 0.0 to 0 (false), matching 3 rows.
func TestSpecDefect_BitFloatEquality(t *testing.T) {
	if testing.Short() {
		t.Skip("TDD RED: DEFECT-ENGINE-BIT-FLOAT-LITERAL-EVAL-1 - pending resolution sprint")
	}

	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})

	sessID := "TestBitFloat_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	_, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE t_bit_float_eq (id INT PRIMARY KEY, is_active BIT);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = coord.ExecuteSessionSQL(sessID, "INSERT INTO t_bit_float_eq VALUES (1, 0), (2, 0);")
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 1. WHERE filter
	resOpt, err := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS total FROM t_bit_float_eq WHERE (is_active = 0.0);")
	if err != nil {
		t.Fatalf("WHERE query failed: %v", err)
	}

	// 2. CASE WHEN query
	resUnopt, err := coord.ExecuteSessionSQL(sessID, "SELECT COALESCE(SUM(CASE WHEN (is_active = 0.0) THEN 1 ELSE 0 END), 0) AS total FROM t_bit_float_eq;")
	if err != nil {
		t.Fatalf("CASE WHEN query failed: %v", err)
	}

	rowOpt := fmt.Sprintf("%v", resOpt.Rows[0])
	rowUnopt := fmt.Sprintf("%v", resUnopt.Rows[0])
	t.Logf("WHERE total: %s, CASE WHEN total: %s", rowOpt, rowUnopt)
	if rowOpt != rowUnopt {
		t.Fatalf("TDD RED Discrepancy: WHERE (%s) != CASE WHEN (%s)", rowOpt, rowUnopt)
	}
}

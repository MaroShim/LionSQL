package spec_defects

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

// TestSpecDefect_CollationBetweenDiscrepancy verifies:
// DEFECT-PROC-EVAL-BETWEEN-CASE-INSENSITIVE-COLLATION-1:
// In pkg/execution/iterator.go, Volcano filters evaluate string BETWEEN using SQL Server's default
// Case-Insensitive collation (strings.ToLower), where 'NULL' -> 'null' (110) > 'hello world' -> 'hello world' (104),
// resulting in an empty range (0 rows).
// In contrast, pkg/proc/eval.go evaluates BetweenExpr using raw Go ASCII comparison
// (fmt.Sprintf("%v", val) >= fmt.Sprintf("%v", lowVal)), where 'N' (78) < 'h' (104), matching rows.
// This causes serious collation divergence between WHERE filters and projection expressions.
func TestSpecDefect_CollationBetweenDiscrepancy(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})

	sessID := "TestCollationBetween_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	_, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE t_collate_between (id INT PRIMARY KEY, word VARCHAR(50));")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 'foo' is between 'N' and 'h' in ASCII ('N' 78 < 'f' 102 < 'h' 104),
	// but in CI collation 'n' 110 > 'h' 104, so 'null' > 'hello world'.
	_, err = coord.ExecuteSessionSQL(sessID, "INSERT INTO t_collate_between VALUES (1, 'foo');")
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 1. WHERE filter query
	resOpt, err := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS total FROM t_collate_between WHERE (word BETWEEN 'NULL' AND 'hello world');")
	if err != nil {
		t.Fatalf("WHERE query failed: %v", err)
	}

	// 2. CASE WHEN query
	resUnopt, err := coord.ExecuteSessionSQL(sessID, "SELECT COALESCE(SUM(CASE WHEN (word BETWEEN 'NULL' AND 'hello world') THEN 1 ELSE 0 END), 0) AS total FROM t_collate_between;")
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

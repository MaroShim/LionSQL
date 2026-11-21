package spec_defects

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

// TestSpecDefect_NoREC_3VL_NullOr verifies:
// DEFECT-ENGINE-NOREC-3VL-IN-NULL-OR-1:
// Evaluates compound 3VL logic with IN lists containing NULL within OR predicates.
// (c2 IN (-1.0, NULL, 3.14159)) OR (id IN (1, 100, 100)) with c2 = 3.14159 should evaluate to TRUE,
// but discrepancy occurs between Volcano filter iterator and projection expression evaluation.
func TestSpecDefect_NoREC_3VL_NullOr(t *testing.T) {
	if testing.Short() {
		t.Skip("TDD RED: DEFECT-ENGINE-NOREC-3VL-IN-NULL-OR-1 - pending resolution sprint")
	}

	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})

	sessID := "TestSpecDefect_3VL_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	// Setup Schema & Data
	_, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE t_3vl_tbl (id INT PRIMARY KEY, c1 FLOAT, c2 FLOAT NOT NULL);")
	if err != nil {
		t.Fatalf("setup DDL failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQL(sessID, "INSERT INTO t_3vl_tbl VALUES (3, NULL, 3.14159);"); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// 1. Run Optimized Query
	resOpt, err := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS total FROM t_3vl_tbl WHERE ((c2 IN (-1.0, NULL, 3.14159)) OR (id IN (1, 100, 100)));")
	if err != nil {
		t.Fatalf("optimized query failed: %v", err)
	}

	// 2. Run Unoptimized Query
	resUnopt, err := coord.ExecuteSessionSQL(sessID, "SELECT COALESCE(SUM(CASE WHEN ((c2 IN (-1.0, NULL, 3.14159)) OR (id IN (1, 100, 100))) THEN 1 ELSE 0 END), 0) AS total FROM t_3vl_tbl;")
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

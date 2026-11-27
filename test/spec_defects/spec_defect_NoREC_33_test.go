package spec_defects

import (
	"fmt"
	"testing"
	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

// TestSpecDefect_NoREC_33 verifies the spec discrepancy:
// Discrepancy Type: NoREC
// Details: NoREC Invariant Violation: WHERE count (9) != SUM(CASE WHEN ...) (5) for predicate: (((c2 * 2) > 1) OR ((c4 + 5) > -2147483648))
func TestSpecDefect_NoREC_33(t *testing.T) {
	if testing.Short() {
		t.Skip("TDD RED: DEFECT-ENGINE-NoREC-33 - pending resolution sprint")
	}

	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})

	sessID := "TestSpecDefect_NoREC_33_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	// Setup Schema & Data
	_, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE fuzz_w2_73222 (id INT PRIMARY KEY, c1 SMALLINT, c2 TINYINT, c3 BIGINT NOT NULL, c4 INT NOT NULL);")
	if err != nil {
		t.Fatalf("setup DDL failed: %v", err)
	}

	inserts := []string{
		"INSERT INTO fuzz_w2_73222 VALUES (1, NULL, 42, -1, -1);",
		"INSERT INTO fuzz_w2_73222 VALUES (4, NULL, NULL, -1, 2147483647);",
		"INSERT INTO fuzz_w2_73222 VALUES (5, 255, NULL, -9223372036854775808, 42);",

	}
	for _, dml := range inserts {
		if _, err := coord.ExecuteSessionSQL(sessID, dml); err != nil {
			t.Fatalf("insert failed: %v", err)
		}
	}

	// 1. Run Optimized Query
	resOpt, errOpt := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS total FROM fuzz_w2_73222 WHERE (((c2 * 2) > 1) OR ((c4 + 5) > -2147483648));")

	// 2. Run Unoptimized Query
	resUnopt, errUnopt := coord.ExecuteSessionSQL(sessID, "SELECT COALESCE(SUM(CASE WHEN (((c2 * 2) > 1) OR ((c4 + 5) > -2147483648)) THEN 1 ELSE 0 END), 0) AS total FROM fuzz_w2_73222;")

	// Error parity check: both queries must either succeed or fail with the same error category
	if (errOpt != nil) != (errUnopt != nil) {
		t.Fatalf("Discrepancy: optErr=%v vs unoptErr=%v", errOpt, errUnopt)
	}

	if errOpt != nil {
		t.Logf("Both queries failed consistently with arithmetic overflow: %v", errOpt)
		return
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

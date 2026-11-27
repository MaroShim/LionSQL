package spec_defects

import (
	"fmt"
	"testing"
	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

// TestSpecDefect_ErrorDifferential_1 verifies the spec discrepancy:
// Discrepancy Type: ErrorDifferential
// Details: Optimized query failed during execution while unoptimized succeeded
func TestSpecDefect_ErrorDifferential_1(t *testing.T) {
	if testing.Short() {
		t.Skip("TDD RED: DEFECT-ENGINE-ErrorDifferential-1 - pending resolution sprint")
	}

	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})

	sessID := "TestSpecDefect_ErrorDifferential_1_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	// Setup Schema & Data
	_, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE fuzz_w3_19473 (id INT PRIMARY KEY, c1 SMALLINT, c2 BIGINT, c3 INT, c4 FLOAT NOT NULL);")
	if err != nil {
		t.Fatalf("setup DDL failed: %v", err)
	}

	inserts := []string{
		"INSERT INTO fuzz_w3_19473 VALUES (1, 32767, 0, 1, -1.0);",
		"INSERT INTO fuzz_w3_19473 VALUES (2, 1, NULL, -2147483648, 1.0);",
		"INSERT INTO fuzz_w3_19473 VALUES (3, 32767, -9223372036854775808, 42, -999.99);",
		"INSERT INTO fuzz_w3_19473 VALUES (4, -32768, 0, NULL, 3.14159);",
		"INSERT INTO fuzz_w3_19473 VALUES (5, 255, NULL, NULL, 1.0);",
		"INSERT INTO fuzz_w3_19473 VALUES (6, 255, 1000000, -2147483648, 3.14159);",
		"INSERT INTO fuzz_w3_19473 VALUES (7, 255, 0, 2147483647, -1.0);",
		"INSERT INTO fuzz_w3_19473 VALUES (8, NULL, NULL, NULL, -1.0);",
		"INSERT INTO fuzz_w3_19473 VALUES (9, NULL, 1, -1, 0.00001);",
		"INSERT INTO fuzz_w3_19473 VALUES (10, 1, 9223372036854775807, 100, 0.00001);",

	}
	for _, dml := range inserts {
		if _, err := coord.ExecuteSessionSQL(sessID, dml); err != nil {
			t.Fatalf("insert failed: %v", err)
		}
	}

	// 1. Run Optimized Query
	resOpt, errOpt := coord.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS total FROM fuzz_w3_19473 WHERE (((c3 < 2147483647) AND ((c3 - 4) > 0)) AND (COALESCE(c1, 32767) = 32767));")

	// 2. Run Unoptimized Query
	resUnopt, errUnopt := coord.ExecuteSessionSQL(sessID, "SELECT COALESCE(SUM(CASE WHEN (((c3 < 2147483647) AND ((c3 - 4) > 0)) AND (COALESCE(c1, 32767) = 32767)) THEN 1 ELSE 0 END), 0) AS total FROM fuzz_w3_19473;")

	// 3. Assert Equivalence or Error Parity
	if (errOpt != nil) != (errUnopt != nil) {
		t.Fatalf("Discrepancy: optErr=%v vs unoptErr=%v", errOpt, errUnopt)
	}
	if errOpt != nil {
		t.Logf("Both queries failed consistently with arithmetic overflow: %v", errOpt)
		return
	}
	if len(resOpt.Rows) == 0 || len(resUnopt.Rows) == 0 {
		t.Fatalf("empty rows returned")
	}
	rowOpt := resOpt.Rows[0]
	rowUnopt := resUnopt.Rows[0]
	if fmt.Sprintf("%v", rowOpt) != fmt.Sprintf("%v", rowUnopt) {
		t.Fatalf("TDD RED Discrepancy: Optimized (%v) != Unoptimized (%v)", rowOpt, rowUnopt)
	}
}

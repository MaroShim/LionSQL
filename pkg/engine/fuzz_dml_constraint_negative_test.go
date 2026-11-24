package engine_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// TestFuzz_DMLConstraintNegativeSuite runs a template-based randomized generative test suite
// targeting all DML constraint validations, verifying that invalid inputs strictly fail-closed.
func TestFuzz_DMLConstraintNegativeSuite(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()
	sess := "fuzz_dml_sess"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	seed := time.Now().UnixNano()
	rng := rand.New(rand.NewSource(seed))
	t.Logf("Running DML constraint negative suite with seed=%d", seed)

	// Sub-suite 1: INSERT Column Count Mismatch Generator
	t.Run("InsertColumnCountMismatch", func(t *testing.T) {
		for i := 0; i < 25; i++ {
			tblName := fmt.Sprintf("tbl_col_mismatch_%d", i)
			colCount := rng.Intn(5) + 2 // 2 to 6 columns
			var colDefs []string
			var colNames []string
			for c := 0; c < colCount; c++ {
				cName := fmt.Sprintf("c%d", c+1)
				colNames = append(colNames, cName)
				colDefs = append(colDefs, fmt.Sprintf("%s INT", cName))
			}
			createSQL := fmt.Sprintf("CREATE TABLE %s (%s);", tblName, strings.Join(colDefs, ", "))
			if _, err := coord.ExecuteSessionSQL(sess, createSQL); err != nil {
				t.Fatalf("setup failed: %v", err)
			}

			// Generate random mismatch: valCount != targetCount
			targetCount := rng.Intn(colCount) + 1
			chosenCols := colNames[:targetCount]
			valCount := rng.Intn(colCount + 4)
			for valCount == targetCount || valCount == 0 {
				valCount = rng.Intn(colCount + 4)
			}

			var valExprs []string
			for v := 0; v < valCount; v++ {
				valExprs = append(valExprs, fmt.Sprintf("%d", rng.Intn(1000)+1))
			}

			insertSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);",
				tblName, strings.Join(chosenCols, ", "), strings.Join(valExprs, ", "))

			_, err := coord.ExecuteSessionSQL(sess, insertSQL)
			if err == nil {
				t.Fatalf("[Seed %d] expected error for column count mismatch (%d cols vs %d vals), but succeeded: %s",
					seed, targetCount, valCount, insertSQL)
			}
			if !strings.Contains(err.Error(), "INSERT has more") && !strings.Contains(err.Error(), "column count mismatch") {
				t.Errorf("[Seed %d] unexpected error message: %v (query: %s)", seed, err, insertSQL)
			}
		}
	})

	// Sub-suite 2: INSERT NOT NULL Column Omission Generator
	t.Run("InsertNotNullOmission", func(t *testing.T) {
		for i := 0; i < 25; i++ {
			tblName := fmt.Sprintf("tbl_nn_omit_%d", i)
			createSQL := fmt.Sprintf("CREATE TABLE %s (id INT, req_val INT NOT NULL, opt_val INT DEFAULT 42);", tblName)
			if _, err := coord.ExecuteSessionSQL(sess, createSQL); err != nil {
				t.Fatalf("setup failed: %v", err)
			}

			// Omit req_val
			insertSQL := fmt.Sprintf("INSERT INTO %s (id, opt_val) VALUES (%d, %d);",
				tblName, rng.Intn(100), rng.Intn(100))

			_, err := coord.ExecuteSessionSQL(sess, insertSQL)
			if err == nil {
				t.Fatalf("[Seed %d] expected NOT NULL violation when omitting req_val, but succeeded: %s", seed, insertSQL)
			}
			if !strings.Contains(err.Error(), "not-null constraint") && !strings.Contains(err.Error(), "does not allow nulls") {
				t.Errorf("[Seed %d] unexpected error message for NOT NULL omission: %v", seed, err)
			}
		}
	})

	// Sub-suite 3: Date Literal Validation Fail-Closed Generator
	t.Run("InvalidDateLiterals", func(t *testing.T) {
		tblName := "tbl_fuzz_dates"
		_, err := coord.ExecuteSessionSQL(sess, fmt.Sprintf("CREATE TABLE %s (id INT, d DATE);", tblName))
		if err != nil {
			t.Fatalf("setup failed: %v", err)
		}

		invalidDates := []string{
			"2026-02-29", // 2026 is not a leap year
			"2026-02-30",
			"2026-02-31",
			"2026-04-31", // April has 30 days
			"2026-06-31", // June has 30 days
			"2026-09-31", // September has 30 days
			"2026-11-31", // November has 30 days
			"2026-13-01", // Month 13
			"2026-00-15", // Month 0
			"2026-05-00", // Day 0
			"2026-05-32", // Day 32
			"invalid-date-string",
			"2026/99/99",
			"not-a-date",
		}

		for idx, badDate := range invalidDates {
			insertSQL := fmt.Sprintf("INSERT INTO %s VALUES (%d, '%s');", tblName, idx+1, badDate)
			_, err := coord.ExecuteSessionSQL(sess, insertSQL)
			if err == nil {
				t.Fatalf("[Seed %d] expected error for invalid date %q, but succeeded", seed, badDate)
			}
			if !strings.Contains(err.Error(), "out of range") &&
				!strings.Contains(err.Error(), "invalid input syntax") &&
				!strings.Contains(err.Error(), "cannot parse date") {
				t.Errorf("[Seed %d] unexpected error message for date %q: %v", seed, badDate, err)
			}
		}
	})

	// Sub-suite 4: UPDATE Non-Existent Column RHS Generator
	t.Run("UpdateNonExistentColumnRHS", func(t *testing.T) {
		tblName := "tbl_fuzz_update_rhs"
		_, err := coord.ExecuteSessionSQL(sess, fmt.Sprintf("CREATE TABLE %s (id INT PRIMARY KEY, val INT);", tblName))
		if err != nil {
			t.Fatalf("setup failed: %v", err)
		}
		_, _ = coord.ExecuteSessionSQL(sess, fmt.Sprintf("INSERT INTO %s VALUES (1, 100);", tblName))

		badExprTemplates := []string{
			"fake_col_%d",
			"val + fake_col_%d",
			"CASE WHEN fake_col_%d > 0 THEN 1 ELSE 2 END",
			"ABS(fake_col_%d)",
		}

		for i, tmpl := range badExprTemplates {
			badCol := fmt.Sprintf(tmpl, i+1)
			updateSQL := fmt.Sprintf("UPDATE %s SET val = %s WHERE id = 1;", tblName, badCol)
			_, err := coord.ExecuteSessionSQL(sess, updateSQL)
			if err == nil {
				t.Fatalf("[Seed %d] expected error for UPDATE with non-existent column in %q, got success", seed, updateSQL)
			}
			if !strings.Contains(err.Error(), "does not exist") {
				t.Errorf("[Seed %d] expected 'does not exist' error, got: %v", seed, err)
			}
		}

		// Verify row value remains intact
		res, err := coord.ExecuteSessionSQL(sess, fmt.Sprintf("SELECT val FROM %s WHERE id = 1;", tblName))
		if err != nil {
			t.Fatalf("SELECT failed: %v", err)
		}
		if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["val"]) != "100" {
			t.Errorf("[Seed %d] data was corrupted, expected 100, got: %v", seed, res.Rows)
		}
	})

	// Sub-suite 5: Ambiguous Column References in Multi-Table Joins
	t.Run("AmbiguousJoinColumns", func(t *testing.T) {
		for i := 0; i < 15; i++ {
			t1 := fmt.Sprintf("t_amb_a_%d", i)
			t2 := fmt.Sprintf("t_amb_b_%d", i)
			_, _ = coord.ExecuteSessionSQL(sess, fmt.Sprintf("CREATE TABLE %s (id INT, shared_col INT, val_a VARCHAR(20));", t1))
			_, _ = coord.ExecuteSessionSQL(sess, fmt.Sprintf("CREATE TABLE %s (id INT, shared_col INT, val_b VARCHAR(20));", t2))
			_, _ = coord.ExecuteSessionSQL(sess, fmt.Sprintf("INSERT INTO %s VALUES (1, 10, 'A');", t1))
			_, _ = coord.ExecuteSessionSQL(sess, fmt.Sprintf("INSERT INTO %s VALUES (1, 10, 'B');", t2))

			// Ambiguous in projection
			qProj := fmt.Sprintf("SELECT shared_col FROM %s JOIN %s ON %s.id = %s.id;", t1, t2, t1, t2)
			_, err := coord.ExecuteSessionSQL(sess, qProj)
			if err == nil {
				t.Fatalf("[Seed %d] expected ambiguous column error for %q, got success", seed, qProj)
			}
			if !strings.Contains(err.Error(), "ambiguous") {
				t.Errorf("[Seed %d] expected ambiguous error, got: %v", seed, err)
			}

			// Ambiguous in WHERE
			qWhere := fmt.Sprintf("SELECT * FROM %s JOIN %s ON %s.id = %s.id WHERE shared_col = 10;", t1, t2, t1, t2)
			_, err = coord.ExecuteSessionSQL(sess, qWhere)
			if err == nil {
				t.Fatalf("[Seed %d] expected ambiguous column error for %q, got success", seed, qWhere)
			}
			if !strings.Contains(err.Error(), "ambiguous") {
				t.Errorf("[Seed %d] expected ambiguous error, got: %v", seed, err)
			}
		}
	})

	// Sub-suite 6: Modulo / Division by Zero in DML & Queries
	t.Run("ModuloDivisionByZero", func(t *testing.T) {
		zeroExprs := []string{
			"SELECT MOD(10, 0);",
			"SELECT MOD(0, 0);",
			"SELECT MOD(100 + 5, 0);",
			"SELECT 10 / 0;",
			"SELECT 10 % 0;",
		}
		for _, q := range zeroExprs {
			_, err := coord.ExecuteSessionSQL(sess, q)
			if err == nil {
				t.Fatalf("[Seed %d] expected division by zero error for %q, got success", seed, q)
			}
			if !strings.Contains(err.Error(), "division by zero") && !strings.Contains(err.Error(), "divide by zero") {
				t.Errorf("[Seed %d] expected division by zero error for %q, got: %v", seed, q, err)
			}
		}
	})
}

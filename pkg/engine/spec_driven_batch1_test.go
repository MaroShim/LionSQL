package engine

import (
	"context"
	"fmt"
	"testing"
)

// TestSpecDriven_Batch1_Suite tests SQL standard string manipulation, CASE,
// NULL-handling, and collation/comparison boundaries.
func TestSpecDriven_Batch1_Suite(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "spec_batch1_sess"
	_, err := coord.GetOrCreateSession(sessID)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	toString := func(v any) string {
		if v == nil {
			return "NULL"
		}
		return fmt.Sprintf("%v", v)
	}

	tests := []specTestCase{
		// -------------------------------------------------------------
		// 1. String Concatenation & NULL propagation (ANSI SQL CONCAT vs '+')
		// -------------------------------------------------------------
		{
			name: "String_Plus_NULL_Yields_NULL",
			sql:  "SELECT 'Hello ' + NULL AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				// Standard ANSI / T-SQL default with CONCAT_NULL_YIELDS_NULL ON: 'abc' + NULL = NULL
				if len(res.Rows) == 0 || res.Rows[0]["r"] != nil {
					t.Errorf("'Hello ' + NULL expected NULL, got: %+v", res.Rows)
				}
			},
		},
		{
			name: "String_CONCAT_Function_Ignores_NULL",
			sql:  "SELECT CONCAT('Hello', NULL, ' World') AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				// CONCAT function ignores NULL and converts it to empty string
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "Hello World" {
					t.Errorf("CONCAT with NULL expected 'Hello World', got: %+v", res.Rows)
				}
			},
		},
		{
			name: "String_CONCAT_WS_Separator_Skips_NULL",
			sql:  "SELECT CONCAT_WS('-', 'A', NULL, 'B', 'C') AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				// CONCAT_WS skips null arguments
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "A-B-C" {
					t.Errorf("CONCAT_WS expected 'A-B-C', got: %+v", res.Rows)
				}
			},
		},
		{
			name: "String_REPLACE_Empty_Search_Returns_Original",
			sql:  "SELECT REPLACE('abcde', '', 'x') AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "abcde" {
					t.Errorf("REPLACE empty search expected 'abcde', got: %+v", res.Rows)
				}
			},
		},
		{
			name: "String_REPLACE_NULL_Pattern_Returns_NULL",
			sql:  "SELECT REPLACE('abcde', 'b', NULL) AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || res.Rows[0]["r"] != nil {
					t.Errorf("REPLACE with NULL pattern expected NULL, got: %+v", res.Rows)
				}
			},
		},
		{
			name: "String_REVERSE_NULL_Returns_NULL",
			sql:  "SELECT REVERSE(NULL) AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || res.Rows[0]["r"] != nil {
					t.Errorf("REVERSE(NULL) expected NULL, got: %+v", res.Rows)
				}
			},
		},
		{
			name: "String_CHARINDEX_Zero_Start_Location_Rejected",
			sql:  "SELECT CHARINDEX('b', 'abcdef', 0) AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				// T-SQL CHARINDEX start_location <= 0 returns 0
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "0" {
					t.Errorf("CHARINDEX start 0 expected 0, got: %+v", res.Rows)
				}
			},
		},
		{
			name: "String_CHARINDEX_Negative_Start_Location_Returns_Zero",
			sql:  "SELECT CHARINDEX('b', 'abcdef', -5) AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "0" {
					t.Errorf("CHARINDEX start -5 expected 0, got: %+v", res.Rows)
				}
			},
		},

		// -------------------------------------------------------------
		// 2. CASE / IIF / Conditional Logic
		// -------------------------------------------------------------
		{
			name: "Case_Simple_Matches_First_Only",
			sql:  "SELECT CASE 1 WHEN 1 THEN 'First' WHEN 1 THEN 'Second' ELSE 'Other' END AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "First" {
					t.Errorf("CASE simple matching expected 'First', got: %+v", res.Rows)
				}
			},
		},
		{
			name: "Case_Searched_No_Match_No_Else_Yields_NULL",
			sql:  "SELECT CASE WHEN 1 = 2 THEN 'Match' END AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || res.Rows[0]["r"] != nil {
					t.Errorf("CASE no match without ELSE expected NULL, got: %+v", res.Rows)
				}
			},
		},
		{
			name: "Case_ShortCircuit_Evaluation_Prevents_DivByZero",
			sql:  "SELECT CASE WHEN 1 = 1 THEN 100 ELSE 1 / 0 END AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "100" {
					t.Errorf("CASE short-circuit expected 100 without evaluating 1/0, got: %+v", res.Rows)
				}
			},
		},
		{
			name: "IIF_Condition_True",
			sql:  "SELECT IIF(10 > 5, 'Yes', 'No') AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "Yes" {
					t.Errorf("IIF true condition expected 'Yes', got: %+v", res.Rows)
				}
			},
		},
		{
			name: "IIF_Condition_False",
			sql:  "SELECT IIF(10 < 5, 'Yes', 'No') AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "No" {
					t.Errorf("IIF false condition expected 'No', got: %+v", res.Rows)
				}
			},
		},
		{
			name:        "IIF_Condition_NULL_Literal_Rejected_As_NonBoolean",
			sql:         "SELECT IIF(NULL, 'Yes', 'No') AS r;",
			expectError: true, // SQL Server Msg 4145: An expression of non-boolean type specified in a context where a condition is expected
		},

		// -------------------------------------------------------------
		// 3. Comparison & BETWEEN Boundaries
		// -------------------------------------------------------------
		{
			name: "Between_Inclusive_Endpoints",
			sql:  "SELECT CASE WHEN 5 BETWEEN 5 AND 10 THEN 'In' ELSE 'Out' END AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "In" {
					t.Errorf("BETWEEN inclusive lower endpoint expected 'In', got: %+v", res.Rows)
				}
			},
		},
		{
			name: "Between_Inverted_Range_Yields_False",
			sql:  "SELECT CASE WHEN 5 BETWEEN 10 AND 1 THEN 'In' ELSE 'Out' END AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				// In SQL standard, val BETWEEN a AND b is equivalent to (val >= a AND val <= b). If a > b, it is always FALSE.
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "Out" {
					t.Errorf("BETWEEN inverted range expected 'Out', got: %+v", res.Rows)
				}
			},
		},
		{
			name: "Between_NULL_Endpoints_Yields_Unknown",
			sql:  "SELECT CASE WHEN 5 BETWEEN NULL AND 10 THEN 'T' WHEN NOT(5 BETWEEN NULL AND 10) THEN 'F' ELSE 'U' END AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "U" {
					t.Errorf("BETWEEN with NULL endpoint expected UNKNOWN ('U'), got: %+v", res.Rows)
				}
			},
		},

		// -------------------------------------------------------------
		// 4. Subquery in Scalar Context
		// -------------------------------------------------------------
		{
			name:  "Scalar_Subquery_More_Than_One_Row_Rejected",
			setup: "CREATE TABLE t_scalar_multi (id INT); INSERT INTO t_scalar_multi VALUES (1), (2);",
			sql:   "SELECT (SELECT id FROM t_scalar_multi) AS r;",
			cleanup: "DROP TABLE t_scalar_multi;",
			expectError: true, // SQL:1999 scalar subquery returning > 1 row must fail
		},
		{
			name:  "Scalar_Subquery_Zero_Rows_Yields_NULL",
			setup: "CREATE TABLE t_scalar_zero (id INT);",
			sql:   "SELECT (SELECT id FROM t_scalar_zero) AS r;",
			cleanup: "DROP TABLE t_scalar_zero;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || res.Rows[0]["r"] != nil {
					t.Errorf("Scalar subquery with 0 rows expected NULL, got: %+v", res.Rows)
				}
			},
		},
		{
			name:  "Scalar_Subquery_Exactly_One_Row_Returns_Value",
			setup: "CREATE TABLE t_scalar_one (id INT); INSERT INTO t_scalar_one VALUES (42);",
			sql:   "SELECT (SELECT id FROM t_scalar_one) AS r;",
			cleanup: "DROP TABLE t_scalar_one;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "42" {
					t.Errorf("Scalar subquery with 1 row expected 42, got: %+v", res.Rows)
				}
			},
		},
	}

	passCount := 0
	failCount := 0

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup != "" {
				if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, tc.setup); err != nil {
					t.Fatalf("setup failed for %s: %v", tc.name, err)
				}
			}
			if tc.cleanup != "" {
				defer func(cleanSQL string) {
					_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, cleanSQL)
				}(tc.cleanup)
			}

			res, err := coord.ExecuteSessionSQLContext(ctx, sessID, tc.sql)
			if tc.expectError {
				if err == nil {
					failCount++
					t.Errorf("[%s] expected error, but succeeded with %d rows", tc.name, len(res.Rows))
				} else {
					passCount++
					t.Logf("[%s] correctly failed as expected: %v", tc.name, err)
				}
			} else {
				if err != nil {
					failCount++
					t.Errorf("[%s] expected success, but failed with: %v", tc.name, err)
				} else {
					if tc.validate != nil {
						tc.validate(t, res)
					}
					passCount++
				}
			}
		})
	}

	t.Logf("\n=== BATCH 1 SUITE SUMMARY: PASS=%d, FAIL=%d ===", passCount, failCount)
}

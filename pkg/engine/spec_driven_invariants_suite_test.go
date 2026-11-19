package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestCase defines a single spec-driven boundary test.
type specTestCase struct {
	name        string
	setup       string
	sql         string
	cleanup     string
	expectError bool
	errContains string
	validate    func(t *testing.T, res *QueryResult)
}

// TestSpecDriven_Standard_Invariants_Suite executes a comprehensive suite of
// SQL standard black-box boundary and invariant tests to catch potential regressions
// without needing internal code inspection.
func TestSpecDriven_Standard_Invariants_Suite(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "spec_suite_sess"
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
		// Category 1: Scalar Function Boundaries (Strings & Numbers)
		// -------------------------------------------------------------
		{
			name:        "Scalar_SUBSTRING_Negative_Length_Rejected",
			sql:         "SELECT SUBSTRING('abcdef', 1, -1) AS r;",
			expectError: true,
			errContains: "", // SQLSTATE 22023 or Msg 536
		},
		{
			name: "Scalar_SUBSTRING_Zero_Length_Returns_Empty",
			sql:  "SELECT SUBSTRING('abcdef', 1, 0) AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "" {
					t.Errorf("SUBSTRING length 0 expected empty string, got: %+v", res.Rows)
				}
			},
		},
		{
			name: "Scalar_SUBSTRING_Start_Beyond_Length",
			sql:  "SELECT SUBSTRING('abcdef', 10, 2) AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "" {
					t.Errorf("SUBSTRING start > len expected empty string, got: %+v", res.Rows)
				}
			},
		},
		{
			name: "Scalar_SUBSTRING_NULL_Input_Returns_NULL",
			sql:  "SELECT SUBSTRING(NULL, 1, 2) AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || res.Rows[0]["r"] != nil {
					t.Errorf("SUBSTRING with NULL expected NULL, got: %+v", res.Rows)
				}
			},
		},
		{
			name:        "Scalar_LEFT_Negative_Length_Rejected",
			sql:         "SELECT LEFT('abcdef', -2) AS r;",
			expectError: true,
		},
		{
			name:        "Scalar_RIGHT_Negative_Length_Rejected",
			sql:         "SELECT RIGHT('abcdef', -2) AS r;",
			expectError: true,
		},
		{
			name:        "Scalar_REPLICATE_Negative_Count_Handled",
			sql:         "SELECT REPLICATE('abc', -1) AS r;",
			expectError: false,
			validate: func(t *testing.T, res *QueryResult) {
				// T-SQL specifies REPLICATE with negative number returns NULL
				if len(res.Rows) == 0 || res.Rows[0]["r"] != nil {
					t.Errorf("REPLICATE with -1 count expected NULL, got: %+v", res.Rows)
				}
			},
		},

		// -------------------------------------------------------------
		// Category 2: 3-Valued Logic & Boolean Truth Matrix
		// -------------------------------------------------------------
		{
			name: "ThreeValued_NULL_Comparison_Yields_Unknown",
			sql:  "SELECT CASE WHEN NULL = NULL THEN 'T' WHEN NOT(NULL = NULL) THEN 'F' ELSE 'U' END AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "U" {
					t.Errorf("NULL = NULL in 3VL expected UNKNOWN ('U'), got: %+v", res.Rows)
				}
			},
		},
		{
			name: "ThreeValued_NULL_NotEqual_Yields_Unknown",
			sql:  "SELECT CASE WHEN NULL <> NULL THEN 'T' WHEN NOT(NULL <> NULL) THEN 'F' ELSE 'U' END AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "U" {
					t.Errorf("NULL <> NULL in 3VL expected UNKNOWN ('U'), got: %+v", res.Rows)
				}
			},
		},
		{
			name: "ThreeValued_NOT_NULL_Is_Unknown",
			sql:  "SELECT CASE WHEN NOT(NULL) THEN 'T' ELSE 'U' END AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "U" {
					t.Errorf("NOT(NULL) expected UNKNOWN ('U'), got: %+v", res.Rows)
				}
			},
		},
		{
			name: "ThreeValued_COALESCE_All_NULLs_Returns_NULL",
			sql:  "SELECT COALESCE(NULL, NULL, NULL) AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || res.Rows[0]["r"] != nil {
					t.Errorf("COALESCE all NULLs expected NULL, got: %+v", res.Rows)
				}
			},
		},
		{
			name: "ThreeValued_NULLIF_Equal_Values_Returns_NULL",
			sql:  "SELECT NULLIF(10, 10) AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || res.Rows[0]["r"] != nil {
					t.Errorf("NULLIF(10, 10) expected NULL, got: %+v", res.Rows)
				}
			},
		},
		{
			name: "ThreeValued_NULLIF_Unequal_Values_Returns_First",
			sql:  "SELECT NULLIF(10, 20) AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "10" {
					t.Errorf("NULLIF(10, 20) expected 10, got: %+v", res.Rows)
				}
			},
		},

		// -------------------------------------------------------------
		// Category 3: Aggregates over Empty & All-NULL Sets
		// -------------------------------------------------------------
		{
			name:  "Aggregate_Empty_Table_COUNT_Is_Zero_Others_NULL",
			setup: "CREATE TABLE t_empty_agg (val INT);",
			sql:   "SELECT COUNT(val) AS cnt, SUM(val) AS sm, AVG(val) AS av, MIN(val) AS mn, MAX(val) AS mx FROM t_empty_agg;",
			cleanup: "DROP TABLE t_empty_agg;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 {
					t.Fatalf("expected 1 row for scalar aggregation on empty table, got 0")
				}
				row := res.Rows[0]
				if toString(row["cnt"]) != "0" {
					t.Errorf("COUNT on empty table expected 0, got %v", row["cnt"])
				}
				if row["sm"] != nil {
					t.Errorf("SUM on empty table expected NULL, got %v", row["sm"])
				}
				if row["av"] != nil {
					t.Errorf("AVG on empty table expected NULL, got %v", row["av"])
				}
				if row["mn"] != nil {
					t.Errorf("MIN on empty table expected NULL, got %v", row["mn"])
				}
				if row["mx"] != nil {
					t.Errorf("MAX on empty table expected NULL, got %v", row["mx"])
				}
			},
		},
		{
			name:  "Aggregate_All_NULLs_COUNT_Col_Is_Zero_COUNT_Star_Is_N",
			setup: "CREATE TABLE t_all_nulls (val INT); INSERT INTO t_all_nulls VALUES (NULL), (NULL), (NULL);",
			sql:   "SELECT COUNT(val) AS cnt_col, COUNT(*) AS cnt_star, SUM(val) AS sm FROM t_all_nulls;",
			cleanup: "DROP TABLE t_all_nulls;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 {
					t.Fatalf("expected 1 row, got none")
				}
				row := res.Rows[0]
				if toString(row["cnt_col"]) != "0" {
					t.Errorf("COUNT(val) on all-null expected 0, got %v", row["cnt_col"])
				}
				if toString(row["cnt_star"]) != "3" {
					t.Errorf("COUNT(*) on 3 rows expected 3, got %v", row["cnt_star"])
				}
				if row["sm"] != nil {
					t.Errorf("SUM(val) on all-null expected NULL, got %v", row["sm"])
				}
			},
		},

		// -------------------------------------------------------------
		// Category 4: Temporal & Date Boundaries
		// -------------------------------------------------------------
		{
			name:        "Temporal_Invalid_Leap_Year_Date_Rejected",
			sql:         "SELECT CAST('2026-02-29' AS DATE) AS r;",
			expectError: true, // 2026 is not a leap year!
		},
		{
			name: "Temporal_Valid_Leap_Year_Date_Accepted",
			sql:  "SELECT CAST('2024-02-29' AS DATE) AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || !strings.HasPrefix(toString(res.Rows[0]["r"]), "2024-02-29") {
					t.Errorf("valid leap day 2024-02-29 rejected or invalid, got: %+v", res.Rows)
				}
			},
		},
		{
			name:        "Temporal_Month_13_Rejected",
			sql:         "SELECT CAST('2025-13-01' AS DATE) AS r;",
			expectError: true,
		},
		{
			name:        "Temporal_Day_32_Rejected",
			sql:         "SELECT CAST('2025-01-32' AS DATE) AS r;",
			expectError: true,
		},

		// -------------------------------------------------------------
		// Category 5: Numeric Bounds & Casting Fail-Closed
		// -------------------------------------------------------------
		{
			name:        "Numeric_Casting_Alpha_To_Int_Rejected",
			sql:         "SELECT CAST('999xyz' AS INT) AS r;",
			expectError: true,
		},
		{
			name:        "Numeric_Casting_Special_Chars_Rejected",
			sql:         "SELECT CAST('$100' AS INT) AS r;",
			expectError: true,
		},
		{
			name:        "Numeric_Division_By_Zero_Literal_Rejected",
			sql:         "SELECT 42 / 0 AS r;",
			expectError: true,
		},
		{
			name:        "Numeric_Modulo_By_Zero_Rejected",
			sql:         "SELECT 42 % 0 AS r;",
			expectError: true,
		},

		// -------------------------------------------------------------
		// Category 6: Set Operations (UNION / EXCEPT / INTERSECT)
		// -------------------------------------------------------------
		{
			name:        "SetOp_UNION_Column_Count_Mismatch_Rejected",
			sql:         "SELECT 1, 2 UNION SELECT 1;",
			expectError: true,
		},
		{
			name: "SetOp_UNION_Deduplicates_NULLs",
			sql:  "SELECT NULL AS x UNION SELECT NULL AS x;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) != 1 {
					t.Errorf("UNION with NULLs expected 1 deduplicated row, got %d", len(res.Rows))
				}
			},
		},
		{
			name: "SetOp_UNION_ALL_Preserves_Duplicates",
			sql:  "SELECT 1 AS x UNION ALL SELECT 1 AS x;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) != 2 {
					t.Errorf("UNION ALL expected 2 rows, got %d", len(res.Rows))
				}
			},
		},

		// -------------------------------------------------------------
		// Category 7: Identifier Quoting & Reserved Keywords
		// -------------------------------------------------------------
		{
			name:  "Identifier_Quoted_Keywords_As_Table_And_Columns",
			setup: "CREATE TABLE [SELECT] ([FROM] INT, [WHERE] VARCHAR(10)); INSERT INTO [SELECT] VALUES (1, 'ok');",
			sql:   "SELECT [FROM], [WHERE] FROM [SELECT] WHERE [FROM] = 1;",
			cleanup: "DROP TABLE [SELECT];",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) != 1 || toString(res.Rows[0]["WHERE"]) != "ok" {
					t.Errorf("quoted keyword table/column query failed, got: %+v", res.Rows)
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
					if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
						failCount++
						t.Errorf("[%s] expected error containing %q, got: %v", tc.name, tc.errContains, err)
					} else {
						passCount++
						t.Logf("[%s] correctly failed as expected: %v", tc.name, err)
					}
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

	t.Logf("\n=== SPEC-DRIVEN INVARIANTS SUITE SUMMARY: PASS=%d, FAIL=%d ===", passCount, failCount)
}

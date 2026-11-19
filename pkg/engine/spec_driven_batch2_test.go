package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestSpecDriven_Batch2_Suite tests SQL standard Math, JSON, and String functions boundaries.
func TestSpecDriven_Batch2_Suite(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "spec_batch2_sess"
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
		// 1. Math Functions (ROUND, FLOOR, CEILING, ABS, POWER, SQRT, SIGN)
		// -------------------------------------------------------------
		{
			name: "Math_ABS_Negative_And_Positive",
			sql:  "SELECT ABS(-123.45) AS neg, ABS(123.45) AS pos, ABS(0) AS zero;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["neg"]) != "123.45" || toString(res.Rows[0]["pos"]) != "123.45" {
					t.Errorf("ABS expected 123.45, got: %+v", res.Rows)
				}
			},
		},
		{
			name: "Math_SQRT_Negative_Yields_Error_Or_NaN",
			sql:  "SELECT SQRT(-1) AS r;",
			expectError: true, // SQRT of negative number must fail in standard SQL
		},
		{
			name: "Math_POWER_Zero_To_Zero_Is_One",
			sql:  "SELECT POWER(0, 0) AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				// Standard 0^0 = 1
				if len(res.Rows) == 0 || (toString(res.Rows[0]["r"]) != "1" && toString(res.Rows[0]["r"]) != "1.000000") {
					t.Errorf("POWER(0, 0) expected 1, got: %+v", res.Rows)
				}
			},
		},
		{
			name: "Math_ROUND_Negative_Decimals",
			sql:  "SELECT ROUND(158.45, -2) AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				// ROUND(158, -2) rounds to nearest hundred -> 200
				val := toString(res.Rows[0]["r"])
				if len(res.Rows) == 0 || (!strings.HasPrefix(val, "200")) {
					t.Errorf("ROUND(158.45, -2) expected 200, got: %+v", res.Rows)
				}
			},
		},
		{
			name: "Math_FLOOR_Negative_Number",
			sql:  "SELECT FLOOR(-1.1) AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				// FLOOR(-1.1) = -2
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "-2" {
					t.Errorf("FLOOR(-1.1) expected -2, got: %+v", res.Rows)
				}
			},
		},
		{
			name: "Math_CEILING_Negative_Number",
			sql:  "SELECT CEILING(-1.9) AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				// CEILING(-1.9) = -1
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "-1" {
					t.Errorf("CEILING(-1.9) expected -1, got: %+v", res.Rows)
				}
			},
		},
		{
			name: "Math_SIGN_Negative_Zero_Positive",
			sql:  "SELECT SIGN(-50) AS neg, SIGN(0) AS zero, SIGN(50) AS pos;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["neg"]) != "-1" || toString(res.Rows[0]["zero"]) != "0" || toString(res.Rows[0]["pos"]) != "1" {
					t.Errorf("SIGN expected -1, 0, 1, got: %+v", res.Rows)
				}
			},
		},

		// -------------------------------------------------------------
		// 2. String Padding and Trimming (LTRIM, RTRIM, TRIM, LPAD, RPAD)
		// -------------------------------------------------------------
		{
			name: "String_TRIM_Leading_And_Trailing",
			sql:  "SELECT TRIM('   hello world   ') AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "hello world" {
					t.Errorf("TRIM expected 'hello world', got: %+v", res.Rows)
				}
			},
		},
		{
			name: "String_LTRIM_RTRIM_Independence",
			sql:  "SELECT LTRIM('  hi  ') AS l, RTRIM('  hi  ') AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["l"]) != "hi  " || toString(res.Rows[0]["r"]) != "  hi" {
					t.Errorf("LTRIM/RTRIM mismatch: %+v", res.Rows)
				}
			},
		},
		{
			name: "String_LPAD_Truncates_If_Length_Smaller",
			sql:  "SELECT LPAD('abcdef', 3, 'x') AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				// LPAD to length 3 from 'abcdef' should return first 3 chars 'abc'
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "abc" {
					t.Errorf("LPAD truncate expected 'abc', got: %+v", res.Rows)
				}
			},
		},
		{
			name: "String_RPAD_Pads_To_Target_Length",
			sql:  "SELECT RPAD('abc', 6, 'x') AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "abcxxx" {
					t.Errorf("RPAD expected 'abcxxx', got: %+v", res.Rows)
				}
			},
		},

		// -------------------------------------------------------------
		// 3. JSON Functions (ISJSON, JSON_VALUE, JSON_QUERY)
		// -------------------------------------------------------------
		{
			name: "JSON_ISJSON_Valid_Object",
			sql:  "SELECT ISJSON('{\"a\": 1}') AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "1" {
					t.Errorf("ISJSON valid expected 1, got: %+v", res.Rows)
				}
			},
		},
		{
			name: "JSON_ISJSON_Invalid_Syntax",
			sql:  "SELECT ISJSON('{invalid json}') AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "0" {
					t.Errorf("ISJSON invalid expected 0, got: %+v", res.Rows)
				}
			},
		},
		{
			name: "JSON_ISJSON_NULL_Input_Yields_NULL",
			sql:  "SELECT ISJSON(NULL) AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || res.Rows[0]["r"] != nil {
					t.Errorf("ISJSON(NULL) expected NULL, got: %+v", res.Rows)
				}
			},
		},
		{
			name: "JSON_VALUE_Scalar_Extraction",
			sql:  "SELECT JSON_VALUE('{\"user\": {\"name\": \"Alice\"}}', '$.user.name') AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["r"]) != "Alice" {
					t.Errorf("JSON_VALUE scalar expected 'Alice', got: %+v", res.Rows)
				}
			},
		},
		{
			name: "JSON_VALUE_On_Object_Yields_NULL",
			sql:  "SELECT JSON_VALUE('{\"user\": {\"name\": \"Alice\"}}', '$.user') AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				// JSON_VALUE on an object or array (non-scalar) yields NULL in lax mode
				if len(res.Rows) == 0 || res.Rows[0]["r"] != nil {
					t.Errorf("JSON_VALUE on non-scalar object expected NULL, got: %+v", res.Rows)
				}
			},
		},
		{
			name: "JSON_QUERY_Object_Extraction",
			sql:  "SELECT JSON_QUERY('{\"user\": {\"name\": \"Alice\"}}', '$.user') AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				val := toString(res.Rows[0]["r"])
				if len(res.Rows) == 0 || !strings.Contains(val, "Alice") {
					t.Errorf("JSON_QUERY expected object JSON containing Alice, got: %+v", res.Rows)
				}
			},
		},
		{
			name: "JSON_QUERY_On_Scalar_Yields_NULL",
			sql:  "SELECT JSON_QUERY('{\"user\": {\"name\": \"Alice\"}}', '$.user.name') AS r;",
			validate: func(t *testing.T, res *QueryResult) {
				// JSON_QUERY on a scalar value yields NULL in lax mode
				if len(res.Rows) == 0 || res.Rows[0]["r"] != nil {
					t.Errorf("JSON_QUERY on scalar expected NULL, got: %+v", res.Rows)
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

	t.Logf("\n=== BATCH 2 SUITE SUMMARY: PASS=%d, FAIL=%d ===", passCount, failCount)
}

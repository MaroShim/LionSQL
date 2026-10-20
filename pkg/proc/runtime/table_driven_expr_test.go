package runtime_test

import (
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage/memengine"
)

type exprCase struct {
	name     string
	dataType string
	expr     string
	expected string
	prefix   bool // If true, matches expected as prefix (useful for timestamps, floats)
}

func TestTableDrivenExpressions(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	testCases := []exprCase{
		// ----------------------------------------------------
		// 1. Arithmetic, Precedence & Math Operators
		// ----------------------------------------------------
		{name: "Math/Add_Simple", dataType: "INT", expr: "10 + 20", expected: "30"},
		{name: "Math/Sub_Simple", dataType: "INT", expr: "100 - 45", expected: "55"},
		{name: "Math/Mul_Simple", dataType: "INT", expr: "7 * 8", expected: "56"},
		{name: "Math/Div_Simple", dataType: "INT", expr: "20 / 4", expected: "5"},
		{name: "Math/Precedence_MulFirst", dataType: "INT", expr: "2 + 3 * 4", expected: "14"},
		{name: "Math/Precedence_Parentheses", dataType: "INT", expr: "(2 + 3) * 4", expected: "20"},
		{name: "Math/Precedence_Complex", dataType: "INT", expr: "(100 - 20) / (2 * 4) + 5", expected: "15"},
		{name: "Math/Unary_Negative", dataType: "INT", expr: "-15 + 5", expected: "-10"},
		{name: "Math/Unary_DoubleNegation", dataType: "INT", expr: "-(-42)", expected: "42"},
		{name: "Math/Float_Add", dataType: "FLOAT", expr: "10.5 + 2.5", expected: "13"},
		{name: "Math/Float_Mul", dataType: "FLOAT", expr: "2.5 * 4.0", expected: "10"},

		// Math Builtin Functions
		{name: "Math/Func_ABS_Negative", dataType: "INT", expr: "ABS(-42)", expected: "42"},
		{name: "Math/Func_ABS_Positive", dataType: "INT", expr: "ABS(42)", expected: "42"},
		{name: "Math/Func_ROUND_Decimals", dataType: "FLOAT", expr: "ROUND(12.3456, 2)", expected: "12.35", prefix: true},
		{name: "Math/Func_ROUND_Integer", dataType: "FLOAT", expr: "ROUND(12.6, 0)", expected: "13"},
		{name: "Math/Func_CEILING_Up", dataType: "FLOAT", expr: "CEILING(4.1)", expected: "5"},
		{name: "Math/Func_FLOOR_Down", dataType: "FLOAT", expr: "FLOOR(4.9)", expected: "4"},
		{name: "Math/Func_POWER_Exponent", dataType: "FLOAT", expr: "POWER(2, 5)", expected: "32"},
		{name: "Math/Func_POWER_Zero", dataType: "FLOAT", expr: "POWER(10, 0)", expected: "1"},
		{name: "Math/Func_SQRT_Square", dataType: "FLOAT", expr: "SQRT(64)", expected: "8"},
		{name: "Math/Func_SIGN_Neg", dataType: "INT", expr: "SIGN(-100)", expected: "-1"},
		{name: "Math/Func_SIGN_Zero", dataType: "INT", expr: "SIGN(0)", expected: "0"},
		{name: "Math/Func_SIGN_Pos", dataType: "INT", expr: "SIGN(100)", expected: "1"},
		{name: "Math/Func_PI", dataType: "FLOAT", expr: "PI()", expected: "3.14159", prefix: true},

		// ----------------------------------------------------
		// 2. String Concatenation & String Builtin Functions
		// ----------------------------------------------------
		{name: "String/Op_Concat", dataType: "VARCHAR(50)", expr: "'Hello ' + 'World'", expected: "Hello World"},
		{name: "String/Op_Concat_Multi", dataType: "VARCHAR(50)", expr: "'A' + '-' + 'B' + '-' + 'C'", expected: "A-B-C"},
		{name: "String/Func_CONCAT_Multi", dataType: "VARCHAR(50)", expr: "CONCAT('Lion', 'SQL', ' ', 'Engine')", expected: "LionSQL Engine"},
		{name: "String/Func_CONCAT_WS_Dashes", dataType: "VARCHAR(50)", expr: "CONCAT_WS('-', '2026', '10', '10')", expected: "2026-10-10"},
		{name: "String/Func_CONCAT_WS_NullSkip", dataType: "VARCHAR(50)", expr: "CONCAT_WS(',', 'apple', NULL, 'banana')", expected: "apple,banana"},
		{name: "String/Func_UPPER", dataType: "VARCHAR(50)", expr: "UPPER('lionsql engine')", expected: "LIONSQL ENGINE"},
		{name: "String/Func_LOWER", dataType: "VARCHAR(50)", expr: "LOWER('LIONSQL ENGINE')", expected: "lionsql engine"},
		{name: "String/Func_LEN", dataType: "INT", expr: "LEN('Hello World')", expected: "11"},
		{name: "String/Func_LEN_Empty", dataType: "INT", expr: "LEN('')", expected: "0"},
		{name: "String/Func_TRIM", dataType: "VARCHAR(50)", expr: "TRIM('   clean me   ')", expected: "clean me"},
		{name: "String/Func_LTRIM", dataType: "VARCHAR(50)", expr: "LTRIM('   left side')", expected: "left side"},
		{name: "String/Func_RTRIM", dataType: "VARCHAR(50)", expr: "RTRIM('right side   ')", expected: "right side"},
		{name: "String/Func_SUBSTRING_Middle", dataType: "VARCHAR(50)", expr: "SUBSTRING('LionSQL Engine', 1, 7)", expected: "LionSQL"},
		{name: "String/Func_SUBSTRING_Offset", dataType: "VARCHAR(50)", expr: "SUBSTRING('LionSQL Engine', 9, 6)", expected: "Engine"},
		{name: "String/Func_LEFT", dataType: "VARCHAR(50)", expr: "LEFT('1234567890', 4)", expected: "1234"},
		{name: "String/Func_RIGHT", dataType: "VARCHAR(50)", expr: "RIGHT('1234567890', 4)", expected: "7890"},
		{name: "String/Func_REPLACE", dataType: "VARCHAR(50)", expr: "REPLACE('cat bat cat', 'cat', 'dog')", expected: "dog bat dog"},
		{name: "String/Func_REVERSE", dataType: "VARCHAR(50)", expr: "REVERSE('racecar')", expected: "racecar"},
		{name: "String/Func_REPLICATE", dataType: "VARCHAR(50)", expr: "REPLICATE('Ab', 3)", expected: "AbAbAb"},
		{name: "String/Func_SPACE", dataType: "VARCHAR(50)", expr: "SPACE(4)", expected: "    "},
		{name: "String/Func_CHARINDEX_Found", dataType: "INT", expr: "CHARINDEX('Engine', 'LionSQL Engine')", expected: "9"},
		{name: "String/Func_CHARINDEX_NotFound", dataType: "INT", expr: "CHARINDEX('Missing', 'LionSQL Engine')", expected: "0"},
		{name: "String/Func_ASCII", dataType: "INT", expr: "ASCII('A')", expected: "65"},
		{name: "String/Func_CHAR", dataType: "VARCHAR(10)", expr: "CHAR(65)", expected: "A"},
		{name: "String/Func_QUOTENAME_Default", dataType: "VARCHAR(50)", expr: "QUOTENAME('Users')", expected: "[Users]"},
		{name: "String/Func_QUOTENAME_Escaped", dataType: "VARCHAR(50)", expr: "QUOTENAME('Data with ] bracket')", expected: "[Data with ]] bracket]"},
		{name: "String/Func_QUOTENAME_SingleQuote", dataType: "VARCHAR(50)", expr: "QUOTENAME('custom', '''')", expected: "'custom'"},

		// ----------------------------------------------------
		// 3. Date & Time Builtin Functions
		// ----------------------------------------------------
		{name: "Date/Func_YEAR", dataType: "INT", expr: "YEAR('2026-10-10 15:30:00')", expected: "2026"},
		{name: "Date/Func_MONTH", dataType: "INT", expr: "MONTH('2026-10-10 15:30:00')", expected: "10"},
		{name: "Date/Func_DAY", dataType: "INT", expr: "DAY('2026-10-25 15:30:00')", expected: "25"},
		{name: "Date/Func_DATEDIFF_Day", dataType: "INT", expr: "DATEDIFF(day, '2026-10-01', '2026-10-15')", expected: "14"},
		{name: "Date/Func_DATEDIFF_Month", dataType: "INT", expr: "DATEDIFF(month, '2026-01-01', '2026-07-01')", expected: "6"},
		{name: "Date/Func_DATEDIFF_Year", dataType: "INT", expr: "DATEDIFF(year, '2020-01-01', '2026-01-01')", expected: "6"},
		{name: "Date/Func_DATEADD_Day", dataType: "VARCHAR(30)", expr: "DATEADD(day, 5, '2026-10-10')", expected: "2026-10-15", prefix: true},
		{name: "Date/Func_DATEADD_Month", dataType: "VARCHAR(30)", expr: "DATEADD(month, 2, '2026-10-10')", expected: "2026-12-10", prefix: true},
		{name: "Date/Func_EOMONTH", dataType: "VARCHAR(30)", expr: "EOMONTH('2026-02-10')", expected: "2026-02-28", prefix: true},
		{name: "Date/Func_ISDATE_Valid", dataType: "INT", expr: "ISDATE('2026-10-10')", expected: "1"},
		{name: "Date/Func_ISDATE_Invalid", dataType: "INT", expr: "ISDATE('not-a-valid-date')", expected: "0"},

		// ----------------------------------------------------
		// 4. Conditional & Logical Builtin Functions
		// ----------------------------------------------------
		{name: "Logic/Func_IIF_True", dataType: "VARCHAR(10)", expr: "IIF(10 > 5, 'PASS', 'FAIL')", expected: "PASS"},
		{name: "Logic/Func_IIF_False", dataType: "VARCHAR(10)", expr: "IIF(10 < 5, 'PASS', 'FAIL')", expected: "FAIL"},
		{name: "Logic/Func_CHOOSE_First", dataType: "VARCHAR(10)", expr: "CHOOSE(1, 'Alpha', 'Beta', 'Gamma')", expected: "Alpha"},
		{name: "Logic/Func_CHOOSE_Third", dataType: "VARCHAR(10)", expr: "CHOOSE(3, 'Alpha', 'Beta', 'Gamma')", expected: "Gamma"},
		{name: "Logic/Func_ISNULL_NotNull", dataType: "VARCHAR(20)", expr: "ISNULL('Original', 'Fallback')", expected: "Original"},
		{name: "Logic/Func_ISNULL_Null", dataType: "VARCHAR(20)", expr: "ISNULL(NULL, 'Fallback')", expected: "Fallback"},
		{name: "Logic/Func_COALESCE_First", dataType: "VARCHAR(20)", expr: "COALESCE('First', 'Second', 'Third')", expected: "First"},
		{name: "Logic/Func_COALESCE_Middle", dataType: "VARCHAR(20)", expr: "COALESCE(NULL, 'Second', 'Third')", expected: "Second"},
		{name: "Logic/Func_COALESCE_Numbers", dataType: "INT", expr: "COALESCE(NULL, NULL, 42, 99)", expected: "42"},
		{name: "Logic/Func_NULLIF_Different", dataType: "INT", expr: "NULLIF(100, 200)", expected: "100"},
		{name: "Logic/Func_ISNUMERIC_Integer", dataType: "INT", expr: "ISNUMERIC('12345')", expected: "1"},
		{name: "Logic/Func_ISNUMERIC_Decimal", dataType: "INT", expr: "ISNUMERIC('123.45')", expected: "1"},
		{name: "Logic/Func_ISNUMERIC_Negative", dataType: "INT", expr: "ISNUMERIC('-99')", expected: "1"},
		{name: "Logic/Func_ISNUMERIC_Text", dataType: "INT", expr: "ISNUMERIC('Hello123')", expected: "0"},

		// ----------------------------------------------------
		// 5. System Functions & Global Variables
		// ----------------------------------------------------
		{name: "System/ServerName", dataType: "VARCHAR(50)", expr: "@@SERVERNAME", expected: "LIONSQL"},
		{name: "System/TranCount_Initial", dataType: "INT", expr: "@@TRANCOUNT", expected: "0"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			procName := "Proc_" + strings.ReplaceAll(strings.ReplaceAll(tc.name, "/", "_"), "-", "_")
			procSQL := fmt.Sprintf(`
CREATE PROCEDURE dbo.%s AS
BEGIN
    DECLARE @res %s;
    SET @res = %s;
    PRINT @res;
END;
`, procName, tc.dataType, tc.expr)

			prog, err := cache.GetOrCompile(procSQL)
			if err != nil {
				t.Fatalf("Compilation failed for %s: %v\nSQL: %s", tc.name, err, procSQL)
			}

			res, err := executor.Run(prog, procName, nil, sess)
			if err != nil {
				t.Fatalf("Execution failed for %s: %v", tc.name, err)
			}

			if len(res.Logs) != 1 {
				t.Fatalf("Expected 1 log entry, got %d (%v)", len(res.Logs), res.Logs)
			}

			actual := res.Logs[0]
			if tc.prefix {
				if !strings.HasPrefix(actual, tc.expected) {
					t.Errorf("Expected log to start with %q, got %q", tc.expected, actual)
				}
			} else {
				if actual != tc.expected {
					t.Errorf("Expected %q, got %q", tc.expected, actual)
				}
			}
		})
	}
}

func TestTableDrivenConditionalAndPredicates(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	predicateCases := []struct {
		name      string
		condition string
		expected  string // "1" for true branch, "0" for false branch
	}{
		// Comparison Operators
		{name: "Compare/Eq_True", condition: "10 = 10", expected: "1"},
		{name: "Compare/Eq_False", condition: "10 = 20", expected: "0"},
		{name: "Compare/Neq_Bang_True", condition: "10 != 20", expected: "1"},
		{name: "Compare/Neq_Bang_False", condition: "10 != 10", expected: "0"},
		{name: "Compare/Neq_Angle_True", condition: "10 <> 20", expected: "1"},
		{name: "Compare/Neq_Angle_False", condition: "10 <> 10", expected: "0"},
		{name: "Compare/Lt_True", condition: "5 < 10", expected: "1"},
		{name: "Compare/Lt_False", condition: "10 < 5", expected: "0"},
		{name: "Compare/Lt_EqualFalse", condition: "10 < 10", expected: "0"},
		{name: "Compare/Lte_LessTrue", condition: "9 <= 10", expected: "1"},
		{name: "Compare/Lte_EqualTrue", condition: "10 <= 10", expected: "1"},
		{name: "Compare/Lte_GreaterFalse", condition: "11 <= 10", expected: "0"},
		{name: "Compare/Gt_True", condition: "15 > 10", expected: "1"},
		{name: "Compare/Gt_False", condition: "10 > 15", expected: "0"},
		{name: "Compare/Gt_EqualFalse", condition: "10 > 10", expected: "0"},
		{name: "Compare/Gte_GreaterTrue", condition: "11 >= 10", expected: "1"},
		{name: "Compare/Gte_EqualTrue", condition: "10 >= 10", expected: "1"},
		{name: "Compare/Gte_LessFalse", condition: "9 >= 10", expected: "0"},

		// Boolean Logic Operators
		{name: "Bool/AND_TrueTrue", condition: "(1 = 1) AND (2 = 2)", expected: "1"},
		{name: "Bool/AND_TrueFalse", condition: "(1 = 1) AND (2 = 3)", expected: "0"},
		{name: "Bool/AND_FalseTrue", condition: "(1 = 2) AND (2 = 2)", expected: "0"},
		{name: "Bool/AND_FalseFalse", condition: "(1 = 2) AND (2 = 3)", expected: "0"},
		{name: "Bool/OR_TrueTrue", condition: "(1 = 1) OR (2 = 2)", expected: "1"},
		{name: "Bool/OR_TrueFalse", condition: "(1 = 1) OR (2 = 3)", expected: "1"},
		{name: "Bool/OR_FalseTrue", condition: "(1 = 2) OR (2 = 2)", expected: "1"},
		{name: "Bool/OR_FalseFalse", condition: "(1 = 2) OR (2 = 3)", expected: "0"},
		{name: "Bool/NOT_True", condition: "NOT (1 = 2)", expected: "1"},
		{name: "Bool/NOT_False", condition: "NOT (1 = 1)", expected: "0"},
		{name: "Bool/Combined_Complex", condition: "((10 > 5) AND (20 > 10)) OR (100 < 50)", expected: "1"},

		// Predicates: LIKE & Wildcards
		{name: "Predicate/LIKE_Prefix", condition: "'foobar' LIKE 'foo%'", expected: "1"},
		{name: "Predicate/LIKE_Suffix", condition: "'foobar' LIKE '%bar'", expected: "1"},
		{name: "Predicate/LIKE_Substring", condition: "'foobar' LIKE '%oba%'", expected: "1"},
		{name: "Predicate/LIKE_Exact", condition: "'foobar' LIKE 'foobar'", expected: "1"},
		{name: "Predicate/LIKE_Mismatch", condition: "'foobar' LIKE 'baz%'", expected: "0"},
		{name: "Predicate/NOT_LIKE_Match", condition: "'foobar' NOT LIKE 'baz%'", expected: "1"},
		{name: "Predicate/NOT_LIKE_Mismatch", condition: "'foobar' NOT LIKE 'foo%'", expected: "0"},

		// Predicates: BETWEEN
		{name: "Predicate/BETWEEN_Middle", condition: "15 BETWEEN 10 AND 20", expected: "1"},
		{name: "Predicate/BETWEEN_LowerBound", condition: "10 BETWEEN 10 AND 20", expected: "1"},
		{name: "Predicate/BETWEEN_UpperBound", condition: "20 BETWEEN 10 AND 20", expected: "1"},
		{name: "Predicate/BETWEEN_Below", condition: "5 BETWEEN 10 AND 20", expected: "0"},
		{name: "Predicate/BETWEEN_Above", condition: "25 BETWEEN 10 AND 20", expected: "0"},
		{name: "Predicate/NOT_BETWEEN_Outside", condition: "25 NOT BETWEEN 10 AND 20", expected: "1"},
		{name: "Predicate/NOT_BETWEEN_Inside", condition: "15 NOT BETWEEN 10 AND 20", expected: "0"},

		// Predicates: IN List
		{name: "Predicate/IN_MatchFirst", condition: "5 IN (5, 10, 15)", expected: "1"},
		{name: "Predicate/IN_MatchMiddle", condition: "10 IN (5, 10, 15)", expected: "1"},
		{name: "Predicate/IN_MatchLast", condition: "15 IN (5, 10, 15)", expected: "1"},
		{name: "Predicate/IN_NoMatch", condition: "7 IN (5, 10, 15)", expected: "0"},

		// Predicates: IS NULL / IS NOT NULL
		{name: "Predicate/IS_NULL", condition: "NULL IS NULL", expected: "1"},
		{name: "Predicate/IS_NOT_NULL_Literal", condition: "'active' IS NOT NULL", expected: "1"},
	}

	for _, pc := range predicateCases {
		t.Run(pc.name, func(t *testing.T) {
			procName := "ProcCond_" + strings.ReplaceAll(strings.ReplaceAll(pc.name, "/", "_"), "-", "_")
			procSQL := fmt.Sprintf(`
CREATE PROCEDURE dbo.%s AS
BEGIN
    DECLARE @res INT;
    IF %s
    BEGIN
        SET @res = 1;
    END
    ELSE
    BEGIN
        SET @res = 0;
    END
    PRINT @res;
END;
`, procName, pc.condition)

			prog, err := cache.GetOrCompile(procSQL)
			if err != nil {
				t.Fatalf("Compilation failed for %s: %v\nSQL: %s", pc.name, err, procSQL)
			}

			res, err := executor.Run(prog, procName, nil, sess)
			if err != nil {
				t.Fatalf("Execution failed for %s: %v", pc.name, err)
			}

			if len(res.Logs) != 1 {
				t.Fatalf("Expected 1 log entry, got %d (%v)", len(res.Logs), res.Logs)
			}

			actual := res.Logs[0]
			if actual != pc.expected {
				t.Errorf("Condition %q expected %s, got %s", pc.condition, pc.expected, actual)
			}
		})
	}
}

func TestTableDrivenCaseWhenExpressions(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	caseWhenTests := []struct {
		name     string
		caseExpr string
		expected string
	}{
		{
			name: "CaseWhen/FirstBranch",
			caseExpr: `CASE 
                WHEN 10 > 5 THEN 'First' 
                WHEN 20 > 5 THEN 'Second' 
                ELSE 'Other' 
            END`,
			expected: "First",
		},
		{
			name: "CaseWhen/SecondBranch",
			caseExpr: `CASE 
                WHEN 10 < 5 THEN 'First' 
                WHEN 20 > 5 THEN 'Second' 
                ELSE 'Other' 
            END`,
			expected: "Second",
		},
		{
			name: "CaseWhen/ElseBranch",
			caseExpr: `CASE 
                WHEN 10 < 5 THEN 'First' 
                WHEN 20 < 5 THEN 'Second' 
                ELSE 'Fallback' 
            END`,
			expected: "Fallback",
		},
		{
			name: "CaseWhen/NumericTiers",
			caseExpr: `CASE 
                WHEN 85 >= 90 THEN 'A' 
                WHEN 85 >= 80 THEN 'B' 
                WHEN 85 >= 70 THEN 'C' 
                ELSE 'F' 
            END`,
			expected: "B",
		},
	}

	for _, tc := range caseWhenTests {
		t.Run(tc.name, func(t *testing.T) {
			procName := "ProcCase_" + strings.ReplaceAll(tc.name, "/", "_")
			procSQL := fmt.Sprintf(`
CREATE PROCEDURE dbo.%s AS
BEGIN
    DECLARE @res VARCHAR(20);
    SET @res = %s;
    PRINT @res;
END;
`, procName, tc.caseExpr)

			prog, err := cache.GetOrCompile(procSQL)
			if err != nil {
				t.Fatalf("Compilation failed for %s: %v\nSQL: %s", tc.name, err, procSQL)
			}

			res, err := executor.Run(prog, procName, nil, sess)
			if err != nil {
				t.Fatalf("Execution failed for %s: %v", tc.name, err)
			}

			if len(res.Logs) != 1 {
				t.Fatalf("Expected 1 log entry, got %d (%v)", len(res.Logs), res.Logs)
			}

			actual := res.Logs[0]
			if actual != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, actual)
			}
		})
	}
}

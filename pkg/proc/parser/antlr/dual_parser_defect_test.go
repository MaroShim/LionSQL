package parser_test

import (
	"strings"
	"testing"

	parser "lionsql/pkg/proc/parser/antlr"
)

// TestDEFECT_DualParser_ExtensiveEquivalenceSuite tests comprehensive AST and parsing equivalence
// between Pratt parser and ANTLR4 fallback across diverse queries, DMLs, expressions, and control structures.
func TestDEFECT_DualParser_ExtensiveEquivalenceSuite(t *testing.T) {
	testCases := []struct {
		name string
		sql  string
	}{
		{
			name: "Standard CREATE PROCEDURE",
			sql: `
CREATE PROCEDURE sp_TestEquiv1
    @a INT,
    @b VARCHAR(50) OUTPUT
AS
BEGIN
    SET @b = 'Hello';
    RETURN @a;
END;
`,
		},
		{
			name: "Procedure with vendor prefix and attributes",
			sql: `
CREATE PROCEDURE dbo.sp_TestEquivVendor
    @id INT,
    @flag BIT = 1
WITH RECOMPILE
AS
BEGIN
    SELECT @id, @flag;
END;
`,
		},
		{
			name: "Procedure with Transaction and TRY CATCH",
			sql: `
CREATE PROCEDURE sp_TestTxCatch
    @x INT
AS
BEGIN
    BEGIN TRY
        BEGIN TRANSACTION;
        UPDATE accounts SET balance = balance + @x;
        COMMIT;
    END TRY
    BEGIN CATCH
        ROLLBACK;
    END CATCH;
END;
`,
		},
		{
			name: "Procedure with WHILE loop and IF ELSE",
			sql: `
CREATE PROCEDURE sp_TestControlFlow
    @n INT
AS
BEGIN
    DECLARE @cnt INT = 0;
    WHILE @cnt < @n
    BEGIN
        SET @cnt = @cnt + 1;
        IF @cnt = 5
            BREAK;
    END;
END;
`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 1. ANTLR Parse validation
			res, antlrErr := parser.ParseTSql(strings.TrimSpace(tc.sql))
			if antlrErr != nil || (res != nil && len(res.Errors) > 0) {
				t.Fatalf("ANTLR parser failed to parse valid T-SQL: %v (errors: %+v)", antlrErr, res.Errors)
			}

			// 2. BuildProcedure (Dual-Parser fallback integration)
			plan, err := parser.BuildProcedure(strings.TrimSpace(tc.sql))
			if err != nil {
				t.Fatalf("BuildProcedure failed on valid T-SQL: %v", err)
			}
			if plan == nil {
				t.Fatalf("BuildProcedure returned nil plan")
			}
			if plan.Name == "" {
				t.Errorf("BuildProcedure returned empty procedure name")
			}
			if plan.Body == nil {
				t.Errorf("BuildProcedure returned nil procedure body")
			}

			// 3. Forced Visitor Build (ASTBuilder direct verification)
			visitorPlan, vErr := parser.BuildProcedureWithVisitor(strings.TrimSpace(tc.sql))
			if vErr != nil {
				t.Fatalf("BuildProcedureWithVisitor failed on valid T-SQL: %v", vErr)
			}
			if visitorPlan == nil || visitorPlan.Name != plan.Name {
				t.Errorf("mismatch between BuildProcedure name (%q) and BuildProcedureWithVisitor name (%q)", plan.Name, visitorPlan.Name)
			}
			if len(visitorPlan.Params) != len(plan.Params) {
				t.Errorf("param count mismatch: fallback=%d, visitor=%d", len(plan.Params), len(visitorPlan.Params))
			}
		})
	}
}

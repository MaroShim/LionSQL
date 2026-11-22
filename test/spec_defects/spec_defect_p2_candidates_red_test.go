package spec_defects

import (
	"context"
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

// TestSpecDefect_P2_04_DecimalToBigIntOverflowFailClosed verifies:
// Defect ID: FIX-DECIMAL-OVERFLOW-AND-LIKE-BRACKET-1 (Candidate P2-04 / Scope-019)
// Root Cause: Casting a Decimal > math.MaxInt64 (e.g. 10^20) to BIGINT silently saturates
// to 9223372036854775807 instead of failing with arithmetic overflow (violating [RULE:ExplicitError]).
func TestSpecDefect_P2_04_DecimalToBigIntOverflowFailClosed(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})
	defer coord.Close()

	sessID := "TestSpecDefect_P2_04_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	ctx := context.Background()
	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT CAST(CAST('100000000000000000000' AS DECIMAL(38,0)) AS BIGINT);")
	if err == nil && len(res.Rows) > 0 {
		val := fmt.Sprintf("%v", res.Rows[0][res.Columns[0]])
		if val == "9223372036854775807" {
			t.Fatalf("TDD: P2-04 silent saturation detected! Decimal 10^20 clamped to %v instead of arithmetic overflow error", val)
		}
	}
}

// TestSpecDefect_P2_80_LikeLeadingBracketPatternMatching verifies:
// Defect ID: FIX-DECIMAL-OVERFLOW-AND-LIKE-BRACKET-1 (Candidate P2-80 / Scope-115)
// Root Cause: A LIKE pattern with a leading closing bracket '[]abc]' should treat ']' as a literal
// set member (matching ']', 'a', 'b', or 'c'), but is misparsed as an empty bracket set and matches nothing.
func TestSpecDefect_P2_80_LikeLeadingBracketPatternMatching(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})
	defer coord.Close()

	sessID := "TestSpecDefect_P2_80_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	ctx := context.Background()
	resA, errA := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT 1 WHERE 'a' LIKE '[]abc]';")
	if errA != nil {
		t.Fatalf("unexpected query error: %v", errA)
	}
	if len(resA.Rows) != 1 {
		t.Fatalf("TDD: P2-80 'a' LIKE '[]abc]' expected 1 row, got %d rows", len(resA.Rows))
	}

	resBracket, errB := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT 1 WHERE ']' LIKE '[]abc]';")
	if errB != nil {
		t.Fatalf("unexpected query error: %v", errB)
	}
	if len(resBracket.Rows) != 1 {
		t.Fatalf("TDD: P2-80 ']' LIKE '[]abc]' expected 1 row, got %d rows", len(resBracket.Rows))
	}
}

// TestSpecDefect_P2_26_SelectTopPercentCalculation verifies:
// Defect ID: FIX-PAGINATION-TOP-PERCENT-PIPELINE-1 (Candidate P2-26 / Scope-046)
// Root Cause: SELECT TOP n PERCENT parses PERCENT but ignores it during plan execution,
// returning n rows instead of (TotalRows * n / 100) rows.
func TestSpecDefect_P2_26_SelectTopPercentCalculation(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})
	defer coord.Close()

	sessID := "TestSpecDefect_P2_26_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	ctx := context.Background()
	_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE t_top_pct (id INT PRIMARY KEY);")
	_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO t_top_pct VALUES (1), (2), (3), (4);")

	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT TOP 50 PERCENT id FROM t_top_pct ORDER BY id;")
	if err != nil {
		t.Fatalf("unexpected query error: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("TDD: P2-26 SELECT TOP 50 PERCENT on 4 rows expected 2 rows, got %d rows", len(res.Rows))
	}
}

// TestSpecDefect_P2_29_ProcOutputVariableTypeCoercion verifies:
// Defect ID: FIX-PROC-OUTPUT-VAR-COERCE-1 (Candidate P2-29 / Scope-050)
// Root Cause: Output parameters returned from stored procedures overwrite the caller's session
// variable without type coercion. For example, returning a DECIMAL into an INT variable contaminates
// the variable type and causes subsequent arithmetic/bitwise operations to fail or return NULL.
func TestSpecDefect_P2_29_ProcOutputVariableTypeCoercion(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})
	defer coord.Close()

	sessID := "TestSpecDefect_P2_29_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	ctx := context.Background()
	_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE PROCEDURE sp_coerce_out @val DECIMAL(10,2) OUTPUT AS BEGIN SET @val = 12.34; END;")
	if err != nil {
		t.Fatalf("failed to create procedure: %v", err)
	}

	_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "DECLARE @res INT = 0;")
	_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC sp_coerce_out @val = @res OUTPUT;")

	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @res & 1 AS bitwise_res;")
	if err != nil {
		t.Fatalf("unexpected query error: %v", err)
	}
	if len(res.Rows) == 0 {
		t.Fatalf("expected 1 row, got 0")
	}

	val := fmt.Sprintf("%v", res.Rows[0]["bitwise_res"])
	if val == "<nil>" || val == "NULL" {
		t.Fatalf("TDD: P2-29 session variable type was contaminated! Bitwise operation '@res & 1' returned %v instead of 0", val)
	}
}

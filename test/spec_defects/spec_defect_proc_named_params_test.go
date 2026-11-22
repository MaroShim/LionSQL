package spec_defects

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

// TestSpecDefect_ProcNamedParams_OutOfOrderAndDefaults verifies:
// Defect ID: DEFECT-PROC-NAMED-PARAM-BINDING-1
// Root Cause: EXEC parameter names (@param = val) were discarded in parser and compiler,
// causing VDBE runtime callProc to bind arguments strictly by declaration position (0, 1, 2...)
// rather than by parameter name, reversing out-of-order arguments and incorrectly overriding
// preceding parameters when subsequent defaulted parameters were specified by name.
func TestSpecDefect_ProcNamedParams_OutOfOrderAndDefaults(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})
	defer coord.Close()

	sessID := "TestSpecDefect_ProcNamedParams_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	ctx := context.Background()

	// Create test stored procedure with default values
	createProcSQL := `
CREATE PROCEDURE dbo.sp_calc_named
    @a INT = 1,
    @b INT = 2
AS
BEGIN
    SELECT (@a * 10 + @b) AS result;
END;
`
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, createProcSQL); err != nil {
		t.Fatalf("failed to create proc: %v", err)
	}

	// 1. Case A: Out-of-order named parameters (@b first, @a second)
	// Expected: @a=3, @b=5 -> 3 * 10 + 5 = 35
	callerOutOfOrder := `
CREATE PROCEDURE dbo.sp_call_out_of_order
AS
BEGIN
    EXEC dbo.sp_calc_named @b = 5, @a = 3;
END;
`
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, callerOutOfOrder); err != nil {
		t.Fatalf("failed to create caller: %v", err)
	}

	resA, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC dbo.sp_call_out_of_order;")
	if err != nil {
		t.Fatalf("execution A failed: %v", err)
	}
	if len(resA.Rows) == 0 {
		t.Fatalf("Case A: expected 1 row, got 0")
	}
	if val := fmt.Sprintf("%v", resA.Rows[0]["result"]); val != "35" {
		t.Errorf("Case A (Out-of-order): expected result 35, got %v", val)
	}

	// 2. Case B: Skip preceding default parameter and specify only @b
	// Expected: @a defaults to 1, @b=5 -> 1 * 10 + 5 = 15
	callerSkipDefault := `
CREATE PROCEDURE dbo.sp_call_skip_default
AS
BEGIN
    EXEC dbo.sp_calc_named @b = 5;
END;
`
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, callerSkipDefault); err != nil {
		t.Fatalf("failed to create caller B: %v", err)
	}

	resB, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC dbo.sp_call_skip_default;")
	if err != nil {
		t.Fatalf("execution B failed: %v", err)
	}
	if len(resB.Rows) == 0 {
		t.Fatalf("Case B: expected 1 row, got 0")
	}
	if val := fmt.Sprintf("%v", resB.Rows[0]["result"]); val != "15" {
		t.Errorf("Case B (Skip default): expected result 15, got %v", val)
	}

	// 3. Case C: Out-of-order OUTPUT parameter
	createOutProcSQL := `
CREATE PROCEDURE dbo.sp_calc_out
    @in_val INT,
    @out_val INT OUTPUT
AS
BEGIN
    SET @out_val = @in_val * 2;
END;
`
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, createOutProcSQL); err != nil {
		t.Fatalf("failed to create out proc: %v", err)
	}

	callerOutSQL := `
CREATE PROCEDURE dbo.sp_call_out_named
AS
BEGIN
    DECLARE @my_out INT;
    EXEC dbo.sp_calc_out @out_val = @my_out OUTPUT, @in_val = 21;
    SELECT @my_out AS result;
END;
`
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, callerOutSQL); err != nil {
		t.Fatalf("failed to create out caller: %v", err)
	}

	resC, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC dbo.sp_call_out_named;")
	if err != nil {
		t.Fatalf("execution C failed: %v", err)
	}
	if len(resC.Rows) == 0 {
		t.Fatalf("Case C: expected 1 row, got 0")
	}
	if val := fmt.Sprintf("%v", resC.Rows[0]["result"]); val != "42" {
		t.Errorf("Case C (Named OUTPUT param): expected result 42, got %v", val)
	}

	// 4. Case D: DEFAULT keyword with named parameter
	// Expected: @b takes default 2, @a = 9 -> 9 * 10 + 2 = 92
	callerDefaultSQL := `
CREATE PROCEDURE dbo.sp_call_default_named
AS
BEGIN
    EXEC dbo.sp_calc_named @b = DEFAULT, @a = 9;
END;
`
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, callerDefaultSQL); err != nil {
		t.Fatalf("failed to create default caller: %v", err)
	}

	resD, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC dbo.sp_call_default_named;")
	if err != nil {
		t.Fatalf("execution D failed: %v", err)
	}
	if len(resD.Rows) == 0 {
		t.Fatalf("Case D: expected 1 row, got 0")
	}
	if val := fmt.Sprintf("%v", resD.Rows[0]["result"]); val != "92" {
		t.Errorf("Case D (DEFAULT keyword with named): expected result 92, got %v", val)
	}

	// 5. Case E: Unknown parameter name error
	callerUnknownSQL := `
CREATE PROCEDURE dbo.sp_call_unknown_param
AS
BEGIN
    EXEC dbo.sp_calc_named @non_existent = 10;
END;
`
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, callerUnknownSQL); err != nil {
		t.Fatalf("failed to create unknown param caller: %v", err)
	}

	_, errE := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC dbo.sp_call_unknown_param;")
	if errE == nil {
		t.Fatalf("Case E: expected error for unknown parameter, got nil")
	}
	if !strings.Contains(errE.Error(), "has no parameter named") {
		t.Errorf("Case E: unexpected error message: %v", errE)
	}
}

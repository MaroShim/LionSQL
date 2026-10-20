package runtime_test

import (
	"testing"

	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage/memengine"
)

func TestUDF_ScalarMathAndProcedureCall(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	// 1. Define and parse UDF: fn_AddTax
	udfSQL := `
CREATE FUNCTION dbo.fn_AddTax(@Amount FLOAT, @Rate FLOAT)
RETURNS FLOAT
AS
BEGIN
    RETURN @Amount * (1 + @Rate);
END
`
	fnStmt, err := proc.ParseFunction(udfSQL)
	if err != nil {
		t.Fatalf("ParseFunction failed: %v", err)
	}

	fnProg, err := proc.CompileFunction(fnStmt)
	if err != nil {
		t.Fatalf("CompileFunction failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)
	executor.RegisterUDF(fnStmt.Name, fnProg)

	// 2. Define and parse Stored Procedure that calls fn_AddTax
	procSQL := `
CREATE PROCEDURE dbo.sp_CalculateTotal
    @SubTotal FLOAT,
    @TaxRate FLOAT
AS
BEGIN
    DECLARE @Total FLOAT;
    SET @Total = dbo.fn_AddTax(@SubTotal, @TaxRate);
    RETURN @Total;
END
`
	procStmt, err := proc.Parse(procSQL)
	if err != nil {
		t.Fatalf("Parse Stored Procedure failed: %v", err)
	}

	procProg, err := proc.CompileProcedure(procStmt)
	if err != nil {
		t.Fatalf("CompileProcedure failed: %v", err)
	}

	cache.PutByName(procStmt.Name, procProg)

	// 3. Execute Procedure with ambient transaction
	res, err := executor.Run(procProg, procStmt.Name, map[string]any{
		"SubTotal": 100.0,
		"TaxRate":  0.08,
	}, session)
	if err != nil {
		t.Fatalf("Executor.Run failed: %v", err)
	}

	if val := toFloat(res.ReturnValue); val != 108.0 {
		t.Fatalf("expected ReturnValue 108.0, got %v (%T)", res.ReturnValue, res.ReturnValue)
	}
}

func TestUDF_ConditionalLogic(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	udfSQL := `
CREATE FUNCTION dbo.fn_DiscountRate(@Qty INT)
RETURNS FLOAT
AS
BEGIN
    IF @Qty >= 100
        RETURN 0.20;
    ELSE IF @Qty >= 50
        RETURN 0.10;
    ELSE
        RETURN 0.0;
END
`
	fnStmt, err := proc.ParseFunction(udfSQL)
	if err != nil {
		t.Fatalf("ParseFunction failed: %v", err)
	}

	fnProg, err := proc.CompileFunction(fnStmt)
	if err != nil {
		t.Fatalf("CompileFunction failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	cache.PutByName(fnStmt.Name, fnProg)
	executor := runtime.NewExecutor(cache)

	testCases := []struct {
		qty      int
		expected float64
	}{
		{qty: 150, expected: 0.20},
		{qty: 75, expected: 0.10},
		{qty: 20, expected: 0.0},
	}

	for _, tc := range testCases {
		res, err := executor.Run(fnProg, fnStmt.Name, map[string]any{
			"Qty": tc.qty,
		}, session)
		if err != nil {
			t.Fatalf("Run UDF for Qty=%d failed: %v", tc.qty, err)
		}
		if val := toFloat(res.ReturnValue); val != tc.expected {
			t.Errorf("for Qty=%d expected %v, got %v", tc.qty, tc.expected, res.ReturnValue)
		}
	}
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	default:
		return 0
	}
}

func TestUDF_StringFormatting(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	udfSQL := `
CREATE FUNCTION dbo.fn_FormatCode(@Code VARCHAR(50))
RETURNS VARCHAR(50)
AS
BEGIN
    RETURN UPPER(TRIM(@Code));
END
`
	fnStmt, err := proc.ParseFunction(udfSQL)
	if err != nil {
		t.Fatalf("ParseFunction failed: %v", err)
	}

	fnProg, err := proc.CompileFunction(fnStmt)
	if err != nil {
		t.Fatalf("CompileFunction failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	cache.PutByName(fnStmt.Name, fnProg)
	executor := runtime.NewExecutor(cache)

	res, err := executor.Run(fnProg, fnStmt.Name, map[string]any{
		"Code": "  sqlion_pro_engine  ",
	}, session)
	if err != nil {
		t.Fatalf("Run UDF failed: %v", err)
	}

	if val, ok := res.ReturnValue.(string); !ok || val != "SQLION_PRO_ENGINE" {
		t.Fatalf("expected 'SQLION_PRO_ENGINE', got %v (%T)", res.ReturnValue, res.ReturnValue)
	}
}

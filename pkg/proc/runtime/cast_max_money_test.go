package runtime_test

import (
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func parseExprHelper(s string) proc.Expr {
	l := proc.NewLexer(s)
	p := proc.NewParser(l)
	return p.ParseExpr()
}

func valToString(v any) string {
	if sv, ok := v.(storage.Value); ok {
		return sv.String()
	}
	if d, ok := v.(storage.Decimal); ok {
		return d.String()
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

func TestTSQL_Cast_Max_And_Money(t *testing.T) {
	// 1. Direct EvalRowExpr verification

	// Test 1: NVARCHAR(MAX)
	expr1 := parseExprHelper("CAST('hello world' AS NVARCHAR(MAX))")
	if expr1 == nil {
		t.Fatalf("ParseExpr for NVARCHAR(MAX) returned nil")
	}
	cast1, ok := expr1.(*proc.CastExpr)
	if !ok {
		t.Fatalf("expected *proc.CastExpr, got %T", expr1)
	}
	if cast1.Precision != -1 {
		t.Errorf("expected Precision -1 for MAX, got %d", cast1.Precision)
	}
	val1, err := proc.EvalRowExpr(cast1, nil, nil)
	if err != nil {
		t.Fatalf("EvalRowExpr failed for NVARCHAR(MAX): %v", err)
	}
	if val1 != "hello world" {
		t.Errorf("expected 'hello world', got %v", val1)
	}

	// Test 2: VARCHAR(MAX) with CONVERT
	expr2 := parseExprHelper("CONVERT(VARCHAR(MAX), 12345)")
	if expr2 == nil {
		t.Fatalf("ParseExpr for CONVERT VARCHAR(MAX) returned nil")
	}
	val2, err := proc.EvalRowExpr(expr2, nil, nil)
	if err != nil {
		t.Fatalf("EvalRowExpr failed for CONVERT VARCHAR(MAX): %v", err)
	}
	if val2 != "12345" {
		t.Errorf("expected '12345', got %v", val2)
	}

	// Test 3: MONEY 4-decimal scale rounding (1.23456 -> 1.2346)
	expr3 := parseExprHelper("CAST('1.23456' AS MONEY)")
	if expr3 == nil {
		t.Fatalf("ParseExpr for CAST AS MONEY returned nil")
	}
	cast3, ok := expr3.(*proc.CastExpr)
	if !ok {
		t.Fatalf("expected *proc.CastExpr, got %T", expr3)
	}
	if cast3.Scale != 4 {
		t.Errorf("expected Scale 4 for MONEY, got %d", cast3.Scale)
	}
	val3, err := proc.EvalRowExpr(cast3, nil, nil)
	if err != nil {
		t.Fatalf("EvalRowExpr failed for CAST AS MONEY: %v", err)
	}
	val3Str := valToString(val3)
	if val3Str != "1.2346" {
		t.Errorf("expected '1.2346', got %v", val3Str)
	}

	// Test 4: SMALLMONEY 4-decimal scale (50.5 -> 50.5000)
	expr4 := parseExprHelper("CAST('50.5' AS SMALLMONEY)")
	if expr4 == nil {
		t.Fatalf("ParseExpr for CAST AS SMALLMONEY returned nil")
	}
	val4, err := proc.EvalRowExpr(expr4, nil, nil)
	if err != nil {
		t.Fatalf("EvalRowExpr failed for SMALLMONEY: %v", err)
	}
	val4Str := valToString(val4)
	if val4Str != "50.5000" {
		t.Errorf("expected '50.5000', got %v", val4Str)
	}

	// Test 5: Currency-symbol parsing in CAST AS MONEY
	currencyTests := []struct {
		input    string
		expected string
	}{
		{"CAST('$10.00' AS MONEY)", "10.0000"},
		{"CAST('-$123.45' AS MONEY)", "-123.4500"},
		{"CAST('$-123.45' AS MONEY)", "-123.4500"},
		{"CAST('($123.45)' AS MONEY)", "-123.4500"},
		{"CAST('$1,234.56' AS MONEY)", "1234.5600"},
		{"CAST('₩50,000' AS MONEY)", "50000.0000"},
		{"CAST('€99.99' AS MONEY)", "99.9900"},
		{"CAST('£75.50' AS MONEY)", "75.5000"},
		{"CAST('¥500' AS MONEY)", "500.0000"},
	}

	for _, ct := range currencyTests {
		expr := parseExprHelper(ct.input)
		if expr == nil {
			t.Fatalf("parseExprHelper(%q) returned nil", ct.input)
		}
		val, err := proc.EvalRowExpr(expr, nil, nil)
		if err != nil {
			t.Fatalf("EvalRowExpr(%q) failed: %v", ct.input, err)
		}
		got := valToString(val)
		if got != ct.expected {
			t.Errorf("for %s expected %q, got %q", ct.input, ct.expected, got)
		}
	}

	// Test 6: LEN(MONEY) parity (SQL Server converts MONEY to VARCHAR with 2 decimals)
	lenExpr := parseExprHelper("LEN(CAST('$10.00' AS MONEY))")
	if lenExpr == nil {
		t.Fatalf("parseExprHelper for LEN(CAST AS MONEY) returned nil")
	}
	lenVal, err := proc.EvalRowExpr(lenExpr, nil, nil)
	if err != nil {
		t.Fatalf("EvalRowExpr for LEN failed: %v", err)
	}
	if lenVal != int64(5) { // '10.00' has length 5
		t.Errorf("expected LEN(CAST('$10.00' AS MONEY)) = 5, got %v", lenVal)
	}

	// Test 7: CONVERT(VARCHAR, MONEY, style)
	convStyle1 := parseExprHelper("CONVERT(VARCHAR, CAST('$1,234.50' AS MONEY), 1)")
	valConv1, err := proc.EvalRowExpr(convStyle1, nil, nil)
	if err != nil {
		t.Fatalf("EvalRowExpr for CONVERT style 1 failed: %v", err)
	}
	if valConv1 != "1,234.50" {
		t.Errorf("expected '1,234.50', got %v", valConv1)
	}

	convStyle2 := parseExprHelper("CONVERT(VARCHAR, CAST('$1,234.50' AS MONEY), 2)")
	valConv2, err := proc.EvalRowExpr(convStyle2, nil, nil)
	if err != nil {
		t.Fatalf("EvalRowExpr for CONVERT style 2 failed: %v", err)
	}
	if valConv2 != "1234.5000" {
		t.Errorf("expected '1234.5000', got %v", valConv2)
	}

	// 2. VDBE VM Procedure execution
	eng := memengine.New()
	defer eng.Close()

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	script := `
CREATE PROCEDURE TestCastMaxAndMoney
AS
BEGIN
    DECLARE @msg NVARCHAR(MAX);
    SET @msg = CAST('antigravity' AS NVARCHAR(MAX));

    DECLARE @m MONEY;
    SET @m = CAST('99.99994' AS MONEY);

    SELECT @msg AS msg, @m AS price;
END;
`
	prog, err := proc.Compile(script)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)

	res, err := executor.Run(prog, "TestCastMaxAndMoney", nil, session)
	if err != nil {
		t.Fatalf("Executor.Run failed: %v", err)
	}

	if len(res.Rows) == 0 {
		t.Fatalf("expected 1 row result, got 0")
	}

	row := res.Rows[0]
	if row["msg"] != "antigravity" {
		t.Errorf("expected msg='antigravity', got %v", row["msg"])
	}
	priceStr := valToString(row["price"])
	if priceStr != "99.9999" {
		t.Errorf("expected price='99.9999', got %v", priceStr)
	}
}

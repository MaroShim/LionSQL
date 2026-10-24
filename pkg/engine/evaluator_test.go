package engine

import (
	"strings"
	"testing"

	"lionsql/pkg/storage"
)

func TestEvaluator_EvalBinaryOp_Integer(t *testing.T) {
	v1 := storage.IntValue(10)
	v2 := storage.IntValue(3)

	// Add
	res, err := EvalBinaryOp('+', v1, v2, storage.TypeInt)
	if err != nil || res.IVal != 13 {
		t.Fatalf("expected 10 + 3 = 13, got %v (err: %v)", res, err)
	}

	// Sub
	res, err = EvalBinaryOp('-', v1, v2, storage.TypeInt)
	if err != nil || res.IVal != 7 {
		t.Fatalf("expected 10 - 3 = 7, got %v (err: %v)", res, err)
	}

	// Mul
	res, err = EvalBinaryOp('*', v1, v2, storage.TypeInt)
	if err != nil || res.IVal != 30 {
		t.Fatalf("expected 10 * 3 = 30, got %v (err: %v)", res, err)
	}

	// Div
	res, err = EvalBinaryOp('/', v1, v2, storage.TypeInt)
	if err != nil || res.IVal != 3 {
		t.Fatalf("expected 10 / 3 = 3, got %v (err: %v)", res, err)
	}

	// Mod
	res, err = EvalBinaryOp('%', v1, v2, storage.TypeInt)
	if err != nil || res.IVal != 1 {
		t.Fatalf("expected 10 %% 3 = 1, got %v (err: %v)", res, err)
	}
}

func TestEvaluator_EvalBinaryOp_FloatAndDecimal(t *testing.T) {
	v1 := storage.FloatValue(10.5)
	v2 := storage.FloatValue(2.5)

	res, err := EvalBinaryOp('+', v1, v2, storage.TypeFloat)
	if err != nil || res.FVal != 13.0 {
		t.Fatalf("expected 10.5 + 2.5 = 13.0, got %v (err: %v)", res, err)
	}

	res, err = EvalBinaryOp('/', v1, v2, storage.TypeFloat)
	if err != nil || res.FVal != 4.2 {
		t.Fatalf("expected 10.5 / 2.5 = 4.2, got %v (err: %v)", res, err)
	}

	// Decimal
	d1 := storage.DecimalValue("100.50")
	d2 := storage.DecimalValue("0.50")
	dRes, err := EvalBinaryOp('+', d1, d2, storage.TypeDecimal)
	if err != nil || dRes.Decimal().String() != "101.00" {
		t.Fatalf("expected 100.50 + 0.50 = 101.00, got %v (err: %v)", dRes, err)
	}
}

func TestEvaluator_DivideByZeroGuards(t *testing.T) {
	// 1. Integer divide by 0
	_, err := EvalBinaryOp('/', storage.IntValue(10), storage.IntValue(0), storage.TypeInt)
	if err == nil || !strings.Contains(err.Error(), "divide by zero") {
		t.Fatalf("expected divide by zero error for 10 / 0, got %v", err)
	}

	// 2. Float divide by 0.0
	_, err = EvalBinaryOp('/', storage.FloatValue(10.0), storage.FloatValue(0.0), storage.TypeFloat)
	if err == nil || !strings.Contains(err.Error(), "divide by zero") {
		t.Fatalf("expected divide by zero error for 10.0 / 0.0, got %v", err)
	}

	// 3. Fractional divisor < 1 casting to 0 on TypeInt
	_, err = EvalBinaryOp('/', storage.IntValue(100), storage.FloatValue(0.4), storage.TypeInt)
	if err == nil || !strings.Contains(err.Error(), "divide by zero") {
		t.Fatalf("expected divide by zero error for 100 / 0.4 on TypeInt, got %v", err)
	}
}

func TestEvaluator_CoerceAndCalculate(t *testing.T) {
	// Int + Float -> Float
	res, err := CoerceAndCalculate('+', storage.IntValue(10), storage.FloatValue(2.5))
	if err != nil || res.Type != storage.TypeFloat || res.FVal != 12.5 {
		t.Fatalf("expected Int(10) + Float(2.5) = Float(12.5), got %v (err: %v)", res, err)
	}

	// Float * Int -> Float
	res, err = CoerceAndCalculate('*', storage.FloatValue(3.5), storage.IntValue(2))
	if err != nil || res.Type != storage.TypeFloat || res.FVal != 7.0 {
		t.Fatalf("expected Float(3.5) * Int(2) = Float(7.0), got %v (err: %v)", res, err)
	}
}

func TestEvaluator_EvalUpdateExpr_Integration(t *testing.T) {
	rec := storage.Record{
		"Price": storage.IntValue(100),
		"Rate":  storage.FloatValue(1.5),
	}

	v1, err := EvalUpdateExpr("[Price] + 20", rec, storage.TypeInt)
	if err != nil || v1.IVal != 120 {
		t.Fatalf("expected [Price] + 20 = 120, got %v (err: %v)", v1, err)
	}

	v2, err := EvalUpdateExpr("rate * 2", rec, storage.TypeFloat)
	if err != nil || v2.FVal != 3.0 {
		t.Fatalf("expected rate * 2 = 3.0, got %v (err: %v)", v2, err)
	}
}

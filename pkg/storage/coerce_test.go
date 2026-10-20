package storage

import (
	"testing"
	"time"
)

func TestCoerceBinaryValues(t *testing.T) {
	// 1. Same types -> no coercion
	vInt1 := IntValue(10)
	vInt2 := IntValue(20)
	c1, c2, ok := CoerceBinaryValues(vInt1, vInt2)
	if ok || c1.Type != TypeInt || c2.Type != TypeInt {
		t.Errorf("expected no coercion for same types")
	}

	// 2. Int <-> Float promotion
	vFloat := FloatValue(10.5)
	c1, c2, ok = CoerceBinaryValues(vInt1, vFloat)
	if !ok || c1.Type != TypeFloat || c2.Type != TypeFloat || c1.FVal != 10.0 || c2.FVal != 10.5 {
		t.Errorf("Int <-> Float coercion failed: (%v, %v, %v)", c1, c2, ok)
	}

	// 3. Decimal promotion
	vDec := DecimalValue("100.50")
	c1, c2, ok = CoerceBinaryValues(vInt1, vDec)
	if !ok || c1.Type != TypeDecimal || c2.Type != TypeDecimal {
		t.Errorf("Decimal promotion failed: (%v, %v, %v)", c1, c2, ok)
	}

	// 4. Bool <-> Int coercion
	vBool := BoolValue(true)
	c1, c2, ok = CoerceBinaryValues(vBool, vInt1)
	if !ok || c1.Type != TypeInt || c2.Type != TypeInt || c1.IVal != 1 {
		t.Errorf("Bool <-> Int coercion failed: (%v, %v, %v)", c1, c2, ok)
	}

	// 5. DateTime <-> VarChar coercion
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	vDt := DateTimeValue(now)
	vStr := StringValue("2026-10-20 12:00:00")
	c1, c2, ok = CoerceBinaryValues(vDt, vStr)
	if !ok || c1.Type != TypeDateTime || c2.Type != TypeDateTime || !c1.TVal.Equal(c2.TVal) {
		t.Errorf("DateTime <-> VarChar coercion failed: (%v, %v, %v)", c1, c2, ok)
	}
}

func TestCoercedEqualAndCompare(t *testing.T) {
	// Int vs Float
	if !IntValue(100).Equal(FloatValue(100.0)) {
		t.Errorf("IntValue(100) should equal FloatValue(100.0)")
	}
	if IntValue(100).Compare(FloatValue(100.5)) >= 0 {
		t.Errorf("IntValue(100) should be less than FloatValue(100.5)")
	}

	// DateTime vs VarChar
	dt := DateTimeValue(time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC))
	strBefore := StringValue("2026-04-30 00:00:00")
	strAfter := StringValue("2026-05-02 00:00:00")
	if dt.Compare(strBefore) <= 0 {
		t.Errorf("dt should be after strBefore")
	}
	if dt.Compare(strAfter) >= 0 {
		t.Errorf("dt should be before strAfter")
	}

	// Bool vs Int
	if !BoolValue(true).Equal(IntValue(1)) {
		t.Errorf("BoolValue(true) should equal IntValue(1)")
	}
	if !BoolValue(false).Equal(IntValue(0)) {
		t.Errorf("BoolValue(false) should equal IntValue(0)")
	}
}

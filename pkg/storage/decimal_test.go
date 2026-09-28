package storage_test

import (
	"math"
	"testing"

	"sqlion/pkg/storage"
)

func TestDecimal_BasicParsingAndString(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		scale    int32
	}{
		{"0", "0", 0},
		{"0.0", "0.0", 1},
		{"123.45", "123.45", 2},
		{"-123.45", "-123.45", 2},
		{"+456.7890", "456.7890", 4},
		{".5", "0.5", 1},
		{"-.5", "-0.5", 1},
		{"100.", "100", 0},
		{"0.0001", "0.0001", 4},
		{"-0.005", "-0.005", 3},
	}

	for _, tc := range tests {
		d, err := storage.ParseDecimal(tc.input)
		if err != nil {
			t.Fatalf("ParseDecimal(%q) failed: %v", tc.input, err)
		}
		if d.String() != tc.expected {
			t.Errorf("ParseDecimal(%q) got String() %q, want %q", tc.input, d.String(), tc.expected)
		}
		if d.Scale() != tc.scale {
			t.Errorf("ParseDecimal(%q) got Scale %d, want %d", tc.input, d.Scale(), tc.scale)
		}
	}
}

func TestDecimal_ExactFinancialArithmetic(t *testing.T) {
	// The famous IEEE-754 floating point issue: 0.1 + 0.2 != 0.3
	f1 := 0.1
	f2 := 0.2
	if f1+f2 == 0.3 {
		t.Errorf("Unexpected: float 0.1 + 0.2 equals 0.3!")
	}

	d1, err1 := storage.ParseDecimal("0.1")
	d2, err2 := storage.ParseDecimal("0.2")
	if err1 != nil || err2 != nil {
		t.Fatalf("Parse error: %v, %v", err1, err2)
	}

	sum := d1.Add(d2)
	expectedSum, _ := storage.ParseDecimal("0.3")
	if !sum.Equal(expectedSum) {
		t.Errorf("expected 0.1 + 0.2 == 0.3, got %s", sum.String())
	}

	// 10.50 - 3.25 = 7.25
	a, _ := storage.ParseDecimal("10.50")
	b, _ := storage.ParseDecimal("3.25")
	diff := a.Sub(b)
	expectedDiff, _ := storage.ParseDecimal("7.25")
	if !diff.Equal(expectedDiff) {
		t.Errorf("expected 10.50 - 3.25 == 7.25, got %s", diff.String())
	}

	// Multiplication: 12.50 * 2.5 = 31.250
	prod := a.Mul(b)
	expectedProd, _ := storage.ParseDecimal("34.1250")
	if !prod.Equal(expectedProd) {
		t.Errorf("expected 10.50 * 3.25 == 34.1250, got %s", prod.String())
	}

	// Division: 100 / 3 with scale 4 -> 33.3333
	d100, _ := storage.ParseDecimal("100")
	d3, _ := storage.ParseDecimal("3")
	quo, err := d100.Div(d3, 4)
	if err != nil {
		t.Fatalf("Div failed: %v", err)
	}
	expectedQuo, _ := storage.ParseDecimal("33.3333")
	if !quo.Equal(expectedQuo) {
		t.Errorf("expected 100 / 3 == 33.3333, got %s", quo.String())
	}
}

func TestDecimal_Comparison(t *testing.T) {
	// 123.450 == 123.45 despite different scales
	d1, _ := storage.ParseDecimal("123.450")
	d2, _ := storage.ParseDecimal("123.45")
	if !d1.Equal(d2) {
		t.Errorf("expected 123.450 == 123.45")
	}
	if d1.Cmp(d2) != 0 {
		t.Errorf("expected d1.Cmp(d2) == 0, got %d", d1.Cmp(d2))
	}

	d3, _ := storage.ParseDecimal("123.451")
	if d1.Cmp(d3) >= 0 {
		t.Errorf("expected 123.450 < 123.451")
	}
	if d3.Cmp(d1) <= 0 {
		t.Errorf("expected 123.451 > 123.450")
	}

	// Negative comparisons
	neg1, _ := storage.ParseDecimal("-50.0")
	neg2, _ := storage.ParseDecimal("-20.0")
	if neg1.Cmp(neg2) >= 0 {
		t.Errorf("expected -50.0 < -20.0")
	}
}

func TestDecimal_RescaleAndRounding(t *testing.T) {
	// 12.345 rescaled to 2 decimal places -> 12.35 (half-up)
	d, _ := storage.ParseDecimal("12.345")
	rescaled := d.Rescale(2)
	if rescaled.String() != "12.35" {
		t.Errorf("expected 12.35, got %s", rescaled.String())
	}

	// 12.344 rescaled to 2 decimal places -> 12.34
	d2, _ := storage.ParseDecimal("12.344")
	rescaled2 := d2.Rescale(2)
	if rescaled2.String() != "12.34" {
		t.Errorf("expected 12.34, got %s", rescaled2.String())
	}

	// Negative half-up rounding: -12.345 -> -12.35
	dNeg, _ := storage.ParseDecimal("-12.345")
	rescaledNeg := dNeg.Rescale(2)
	if rescaledNeg.String() != "-12.35" {
		t.Errorf("expected -12.35, got %s", rescaledNeg.String())
	}
}

func TestDecimal_DivisionByZero(t *testing.T) {
	d1, _ := storage.ParseDecimal("50.0")
	d0, _ := storage.ParseDecimal("0.0")
	_, err := d1.Div(d0, 2)
	if err != storage.ErrDivisionByZero {
		t.Errorf("expected ErrDivisionByZero, got %v", err)
	}
}

func TestDecimal_Conversions(t *testing.T) {
	d, _ := storage.ParseDecimal("123.75")
	f := d.Float64()
	if math.Abs(f-123.75) > 1e-9 {
		t.Errorf("expected 123.75 float, got %f", f)
	}

	i := d.Int64()
	if i != 124 { // 123.75 rescaled to scale 0 with half-up rounds to 124
		t.Errorf("expected 124 int, got %d", i)
	}
}

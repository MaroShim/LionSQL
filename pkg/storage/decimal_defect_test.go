package storage_test

import (
	"testing"

	"lionsql/pkg/storage"
)

func TestDEFECT_Decimal_TruncationNotRounding(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
	}{
		{"1.9", 1},
		{"1.5", 1},
		{"1.1", 1},
		{"0.999", 0},
		{"-1.9", -1},
		{"-1.5", -1},
		{"-1.1", -1},
		{"-0.999", 0},
		{"123.75", 123},
		{"-123.75", -123},
	}

	for _, tc := range tests {
		d, err := storage.ParseDecimal(tc.input)
		if err != nil {
			t.Fatalf("ParseDecimal(%s) failed: %v", tc.input, err)
		}
		got := d.Int64()
		if got != tc.expected {
			t.Errorf("[DEFECT-DECIMAL-TO-BIGINT-ROUNDING-1] Decimal(%s).Int64() = %d; want %d (truncation towards zero)", tc.input, got, tc.expected)
		}
	}
}

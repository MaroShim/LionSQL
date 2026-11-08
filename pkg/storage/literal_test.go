package storage

import (
	"math"
	"testing"
)

func TestParseLiteralDefault_BitShift(t *testing.T) {
	tests := []struct {
		input       string
		targetType  DataType
		expectedInt int64
	}{
		{"1<<63", TypeInt, math.MinInt64},
		{"1 << 4", TypeInt, 16},
		{"16 >> 2", TypeInt, 4},
		{"-16 >> 2", TypeInt, -4},
		{"42", TypeInt, 42},
	}

	for _, tc := range tests {
		val := ParseLiteralDefault(tc.input, tc.targetType)
		if val.Type != TypeInt {
			t.Errorf("input %q expected TypeInt, got %v", tc.input, val.Type)
			continue
		}
		if val.IVal != tc.expectedInt {
			t.Errorf("input %q expected %d, got %d", tc.input, tc.expectedInt, val.IVal)
		}
	}
}

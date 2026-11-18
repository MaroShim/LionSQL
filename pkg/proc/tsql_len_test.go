package proc_test

import (
	"testing"

	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

func TestTSQL_LEN_TrailingSpacesExclusion(t *testing.T) {
	tests := []struct {
		fn       string
		input    string
		expected int64
	}{
		{"LEN", "abc   ", 3},
		{"LEN", " ", 0},
		{"LEN", "   a", 4},
		{"LEN", "   a   ", 4},
		{"LEN", "가나다   ", 3},
		{"LEN", "hello world", 11},
		{"LENGTH", "abc   ", 6},
		{"LENGTH", " ", 1},
		{"LENGTH", "   a", 4},
		{"LENGTH", "가나다   ", 6},
	}

	for _, tc := range tests {
		val := proc.EvalBuiltin(tc.fn, []storage.Value{storage.StringValue(tc.input)})
		if val.IsNull {
			t.Fatalf("%s(%q) returned NULL, expected %d", tc.fn, tc.input, tc.expected)
		}
		if val.IVal != tc.expected {
			t.Errorf("%s(%q) = %d, expected %d", tc.fn, tc.input, val.IVal, tc.expected)
		}
	}

	// NULL propagation
	nullVal := storage.NullValue(storage.TypeVarChar)
	res := proc.EvalBuiltin("LEN", []storage.Value{nullVal})
	if !res.IsNull {
		t.Errorf("LEN(NULL) expected NULL, got %+v", res)
	}
}

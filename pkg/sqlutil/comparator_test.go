package sqlutil

import (
	"testing"
	"time"
)

func TestCompareAny(t *testing.T) {
	tests := []struct {
		name     string
		a, b     any
		expected int
	}{
		// Nil tests
		{"both nil", nil, nil, 0},
		{"a nil", nil, 10, -1},
		{"b nil", "hello", nil, 1},

		// String tests (Must be lexicographical, not parsed as float!)
		{"strings identical", "hello", "hello", 0},
		{"strings order", "abc", "def", -1},
		{"strings reverse", "def", "abc", 1},
		{"strings digits vs digits (012 vs 2)", "012", "2", -1}, // '0' < '2' in string
		{"strings complex key", "1_1_1001", "1_1_1002", -1},
		{"strings zip code", "00123", "123", -1},

		// Integer tests
		{"ints identical", 100, 100, 0},
		{"ints less", int64(10), int64(20), -1},
		{"ints greater", 50, 20, 1},
		{"mixed int types", int32(15), int64(25), -1},
		{"uint vs int", uint(100), int64(50), 1},

		// Float tests
		{"floats identical", 3.14, 3.14, 0},
		{"floats less", 1.23, 4.56, -1},
		{"floats greater", 9.99, 5.55, 1},
		{"int vs float", 10, 10.5, -1},
		{"float vs int", 10.5, 10, 1},

		// Time tests
		{
			"time less",
			time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
			-1,
		},
		{
			"time equal",
			time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			0,
		},

		// Bool tests
		{"bool false vs true", false, true, -1},
		{"bool true vs false", true, false, 1},
		{"bool equal", true, true, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := CompareAny(tc.a, tc.b)
			if res != tc.expected {
				t.Errorf("CompareAny(%v, %v) = %d; expected %d", tc.a, tc.b, res, tc.expected)
			}
		})
	}
}

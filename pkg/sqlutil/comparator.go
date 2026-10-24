package sqlutil

import (
	"fmt"
	"time"
)

// toInt converts an integer-like value to int64 without allocations.
func toInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case int32:
		return int64(n), true
	case int16:
		return int64(n), true
	case int8:
		return int64(n), true
	case uint:
		return int64(n), true
	case uint64:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint8:
		return int64(n), true
	default:
		return 0, false
	}
}

// toFloat converts any numeric value to float64 without string allocations.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case int16:
		return float64(n), true
	case int8:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint64:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint8:
		return float64(n), true
	default:
		return 0, false
	}
}

// CompareAny compares two arbitrary database values according to strict SQL semantics:
// - Returns -1 if a < b, 0 if a == b, 1 if a > b.
// - NULL / nil is considered smaller than any non-NULL value (NULLs first).
// - Strings are compared lexicographically (ordinal, no heuristic float coercion).
// - Numbers (integers, floats) are compared numerically without string conversion.
// - Timestamps are compared chronologically.
// - Booleans are compared (false < true).
// - If types differ between numeric types (e.g. int vs float), they are compared numerically.
// - If types fundamentally differ, falls back to string representation comparison.
func CompareAny(a, b any) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}

	// 1. String vs String fast-path (Strict lexicographical comparison: NO float coercion!)
	if sa, okA := a.(string); okA {
		if sb, okB := b.(string); okB {
			if sa < sb {
				return -1
			}
			if sa > sb {
				return 1
			}
			return 0
		}
	}

	// 2. Pure Integer comparison fast-path
	if ia, okA := toInt(a); okA {
		if ib, okB := toInt(b); okB {
			if ia < ib {
				return -1
			}
			if ia > ib {
				return 1
			}
			return 0
		}
	}

	// 3. Numeric comparison (floats or mixed int/float)
	if fa, okA := toFloat(a); okA {
		if fb, okB := toFloat(b); okB {
			if fa < fb {
				return -1
			}
			if fa > fb {
				return 1
			}
			return 0
		}
	}

	// 4. Timestamp comparison
	if ta, okA := a.(time.Time); okA {
		if tb, okB := b.(time.Time); okB {
			if ta.Before(tb) {
				return -1
			}
			if ta.After(tb) {
				return 1
			}
			return 0
		}
	}

	// 5. Boolean comparison
	if ba, okA := a.(bool); okA {
		if bb, okB := b.(bool); okB {
			if !ba && bb {
				return -1
			}
			if ba && !bb {
				return 1
			}
			return 0
		}
	}

	// 6. Fallback string representation comparison
	sa := fmt.Sprintf("%v", a)
	sb := fmt.Sprintf("%v", b)
	if sa < sb {
		return -1
	}
	if sa > sb {
		return 1
	}
	return 0
}

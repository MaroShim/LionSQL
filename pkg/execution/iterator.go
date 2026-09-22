package execution

import (
	"context"
	"fmt"
	"strings"
	"time"

	"sqlion/pkg/storage"
)

// Tuple represents a single row passing through the Volcano execution pipeline.
type Tuple map[string]any

// Iterator is the canonical Volcano-style (pull-based) relational execution operator.
type Iterator interface {
	// Open initializes the operator and its children.
	Open(ctx context.Context) error

	// Next pulls the next tuple from the operator pipeline.
	// Returns (Tuple, nil) when a tuple is available, or (nil, io.EOF) when iteration finishes.
	Next(ctx context.Context) (Tuple, error)

	// Close releases any allocated resources (cursors, buffers, file handles).
	Close() error

	// Schema returns the output column names for this operator.
	Schema() []string
}

// CloneTuple creates a shallow copy of a tuple.
func CloneTuple(t Tuple) Tuple {
	if t == nil {
		return nil
	}
	res := make(Tuple, len(t))
	for k, v := range t {
		res[k] = v
	}
	return res
}

// FormatRecord converts a storage.Record into a Tuple with both raw column names
// and table-prefixed (or alias-prefixed) names for disambiguation in joins.
func FormatRecord(rec storage.Record, schema *storage.TableSchema, alias string) Tuple {
	t := make(Tuple)
	if rec == nil {
		return t
	}

	for colName, stVal := range rec {
		val := formatStorageVal(stVal)
		t[colName] = val
		t[strings.ToLower(colName)] = val

		if alias != "" {
			prefixed := fmt.Sprintf("%s.%s", alias, colName)
			t[prefixed] = val
			t[strings.ToLower(prefixed)] = val
		}
		if schema != nil && schema.Name != "" && schema.Name != alias {
			prefixed := fmt.Sprintf("%s.%s", schema.Name, colName)
			t[prefixed] = val
			t[strings.ToLower(prefixed)] = val
		}
	}
	return t
}

func formatStorageVal(v storage.Value) any {
	if v.IsNull {
		return nil
	}
	switch v.Type {
	case storage.TypeInt:
		return v.IVal
	case storage.TypeVarChar:
		return v.SVal
	case storage.TypeFloat:
		return v.FVal
	case storage.TypeBool:
		return v.BVal
	case storage.TypeDateTime:
		return v.TVal
	default:
		return v.String()
	}
}

// CompareValues compares two generic values (scalars, strings, floats, times).
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
// NULL is ordered smaller than non-NULL.
func CompareValues(a, b any) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}

	// Universal numeric comparison if both values can be represented as float64
	if fA, okA := toFloat64(a); okA {
		if fB, okB := toFloat64(b); okB {
			if fA < fB {
				return -1
			} else if fA > fB {
				return 1
			}
			return 0
		}
	}

	switch va := a.(type) {
	case string:
		vb := fmt.Sprintf("%v", b)
		return strings.Compare(va, vb)
	case bool:
		vb, ok := b.(bool)
		if !ok {
			return strings.Compare(fmt.Sprintf("%v", a), fmt.Sprintf("%v", b))
		}
		if !va && vb {
			return -1
		} else if va && !vb {
			return 1
		}
		return 0
	case time.Time:
		vb, ok := b.(time.Time)
		if !ok {
			return strings.Compare(fmt.Sprintf("%v", a), fmt.Sprintf("%v", b))
		}
		if va.Before(vb) {
			return -1
		} else if va.After(vb) {
			return 1
		}
		return 0
	default:
		return strings.Compare(fmt.Sprintf("%v", a), fmt.Sprintf("%v", b))
	}
}

func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	default:
		if num, ok := v.(interface{ Int64() (int64, error) }); ok {
			if i, err := num.Int64(); err == nil {
				return i, true
			}
		}
		if num, ok := v.(interface{ Float64() (float64, error) }); ok {
			if f, err := num.Float64(); err == nil {
				return int64(f), true
			}
		}
		return 0, false
	}
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	default:
		if num, ok := v.(interface{ Float64() (float64, error) }); ok {
			if f, err := num.Float64(); err == nil {
				return f, true
			}
		}
		return 0, false
	}
}

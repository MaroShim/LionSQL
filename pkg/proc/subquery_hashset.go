package proc

import (
	"fmt"
	"math"
	"strconv"
	"sync"
	"sync/atomic"

	"lionsql/pkg/storage"
)

// MaxSubqueryHashSetCapacity is the strict upper bound on rows materialized into an in-memory hashset ([RULE:BoundedBuffers]).
const MaxSubqueryHashSetCapacity = 10000

// SubqueryHashSet represents an in-memory materialized result of an uncorrelated subquery.
// It enforces canonical type normalization and tracks NULL presence for SQL-92 Three-Valued Logic (3VL).
type SubqueryHashSet struct {
	Values  map[any]struct{}
	HasNull bool
	Count   int
}

// NewSubqueryHashSet initializes a bounded subquery hash set with pre-allocated capacity.
func NewSubqueryHashSet(capacity int) *SubqueryHashSet {
	if capacity <= 0 {
		capacity = 16
	}
	if capacity > MaxSubqueryHashSetCapacity {
		capacity = MaxSubqueryHashSetCapacity
	}
	return &SubqueryHashSet{
		Values: make(map[any]struct{}, capacity),
	}
}

// Add inserts a value into the hashset after canonical type normalization.
// It returns false if the capacity limit is exceeded.
func (s *SubqueryHashSet) Add(rawVal any) bool {
	normKey, isNull := NormalizeSubqueryKey(rawVal)
	if isNull {
		s.HasNull = true
		s.Count++
		return s.Count <= MaxSubqueryHashSetCapacity
	}

	if s.Count >= MaxSubqueryHashSetCapacity {
		return false
	}

	s.Values[normKey] = struct{}{}
	s.Count++
	return true
}

// Evaluate evaluates whether target value satisfies [NOT] IN condition against the hashset
// complying with SQL-92 Three-Valued Logic (3VL) rules for NULLs.
func (s *SubqueryHashSet) Evaluate(normKey any, isNull bool, isNot bool) bool {
	if isNull {
		// In SQL, NULL IN (...) is UNKNOWN, and NULL NOT IN (...) is UNKNOWN.
		// In WHERE clauses, UNKNOWN evaluates to false.
		return false
	}

	if s.matches(normKey) {
		// Target found in set:
		// x IN (..., x, ...) -> TRUE
		// x NOT IN (..., x, ...) -> FALSE
		return !isNot
	}

	// Target NOT found in set:
	if s.HasNull {
		// SQL-92 Three-Valued Logic (3VL):
		// Target was not found in set, BUT set contains NULL:
		// x IN (1, 2, NULL) -> UNKNOWN (false in WHERE)
		// x NOT IN (1, 2, NULL) -> UNKNOWN (false in WHERE)
		return false
	}

	// Target was not found in set, and set has NO NULL:
	// x IN (1, 2) -> FALSE
	// x NOT IN (1, 2) -> TRUE
	return isNot
}

// matches performs primary O(1) hash lookup, followed by cross-type coercion fallbacks
// (e.g. INT column vs VARCHAR subquery results from STRING_SPLIT)
func (s *SubqueryHashSet) matches(normKey any) bool {
	if _, found := s.Values[normKey]; found {
		return true
	}
	switch v := normKey.(type) {
	case int64:
		if _, found := s.Values[strconv.FormatInt(v, 10)]; found {
			return true
		}
		if _, found := s.Values[float64(v)]; found {
			return true
		}
	case float64:
		if v == math.Trunc(v) {
			if _, found := s.Values[int64(v)]; found {
				return true
			}
		}
		if _, found := s.Values[strconv.FormatFloat(v, 'f', -1, 64)]; found {
			return true
		}
	case string:
		if parsedInt, err := strconv.ParseInt(v, 10, 64); err == nil {
			if _, found := s.Values[parsedInt]; found {
				return true
			}
		}
		if parsedFloat, err := strconv.ParseFloat(v, 64); err == nil {
			if _, found := s.Values[parsedFloat]; found {
				return true
			}
		}
	}
	return false
}

// NormalizeSubqueryKey converts any runtime value into its canonical hashable type:
// - All signed & unsigned integers -> int64
// - All floating point numbers -> float64
// - String and byte slices -> string
// - bool -> bool
// - nil / storage.Value NULL -> isNull=true
func NormalizeSubqueryKey(val any) (any, bool) {
	if val == nil {
		return nil, true
	}
	if sv, ok := val.(storage.Value); ok {
		if sv.IsNull {
			return nil, true
		}
		switch sv.Type {
		case storage.TypeInt:
			return sv.IVal, false
		case storage.TypeFloat, storage.TypeReal:
			return sv.FVal, false
		case storage.TypeVarChar, storage.TypeJSON, storage.TypeUUID:
			return sv.SVal, false
		case storage.TypeBinary:
			return string(sv.BinVal), false
		case storage.TypeBool:
			return sv.BVal, false
		case storage.TypeDecimal:
			return sv.Decimal().String(), false
		case storage.TypeDateTime:
			return sv.TVal.Format("2006-01-02 15:04:05.999999999"), false
		case storage.TypeDate:
			return sv.String(), false
		default:
			return sv.String(), false
		}
	}
	switch v := val.(type) {
	case int:
		return int64(v), false
	case int64:
		return v, false
	case int32:
		return int64(v), false
	case int16:
		return int64(v), false
	case int8:
		return int64(v), false
	case uint:
		return int64(v), false
	case uint64:
		return int64(v), false
	case uint32:
		return int64(v), false
	case uint16:
		return int64(v), false
	case uint8:
		return int64(v), false
	case float64:
		return v, false
	case float32:
		return float64(v), false
	case string:
		return v, false
	case []byte:
		return string(v), false
	case bool:
		return v, false
	default:
		return fmt.Sprintf("%v", v), false
	}
}

var (
	subqueryHashSetRegistry sync.Map
	subqueryTokenSeq        atomic.Uint64
)

// RegisterSubqueryHashSet records a materialized subquery hashset and returns a unique query token.
func RegisterSubqueryHashSet(set *SubqueryHashSet) string {
	token := fmt.Sprintf("__subq_hashset_%d__", subqueryTokenSeq.Add(1))
	subqueryHashSetRegistry.Store(token, set)
	return token
}

// LookupSubqueryHashSet retrieves a materialized subquery hashset by unique token.
func LookupSubqueryHashSet(token string) (*SubqueryHashSet, bool) {
	val, ok := subqueryHashSetRegistry.Load(token)
	if !ok {
		return nil, false
	}
	return val.(*SubqueryHashSet), true
}

// UnregisterSubqueryHashSet cleans up a materialized subquery hashset.
func UnregisterSubqueryHashSet(token string) {
	subqueryHashSetRegistry.Delete(token)
}

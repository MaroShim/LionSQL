package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"sqlion/pkg/storage"
)

// ColumnIndexMap provides O(1) integer offset mapping for column names, aliases, and table prefixes.
type ColumnIndexMap struct {
	indices map[string]int
	cols    []string
}

// NewColumnIndexMap builds a ColumnIndexMap from a TableSchema and optional alias.
func NewColumnIndexMap(schema *storage.TableSchema, alias string) *ColumnIndexMap {
	m := &ColumnIndexMap{
		indices: make(map[string]int),
	}
	if schema != nil {
		m.cols = make([]string, len(schema.Columns))
		for i, c := range schema.Columns {
			m.cols[i] = c.Name
			m.indices[c.Name] = i
			m.indices[strings.ToLower(c.Name)] = i
			if alias != "" {
				m.indices[alias+"."+c.Name] = i
				m.indices[strings.ToLower(alias+"."+c.Name)] = i
			}
			if schema.Name != "" && schema.Name != alias {
				m.indices[schema.Name+"."+c.Name] = i
				m.indices[strings.ToLower(schema.Name+"."+c.Name)] = i
			}
		}
	}
	return m
}

// NewColumnIndexMapFromCols builds a ColumnIndexMap from a simple list of column names.
func NewColumnIndexMapFromCols(cols []string) *ColumnIndexMap {
	m := &ColumnIndexMap{
		indices: make(map[string]int, len(cols)*2),
		cols:    cols,
	}
	for i, c := range cols {
		m.indices[c] = i
		m.indices[strings.ToLower(c)] = i
		if dotIdx := strings.Index(c, "."); dotIdx != -1 {
			bare := c[dotIdx+1:]
			if _, exists := m.indices[bare]; !exists {
				m.indices[bare] = i
				m.indices[strings.ToLower(bare)] = i
			}
		}
	}
	return m
}

// Index returns the slice offset for a given column name or prefix.
func (m *ColumnIndexMap) Index(name string) (int, bool) {
	if m == nil || m.indices == nil {
		return -1, false
	}
	idx, ok := m.indices[name]
	if !ok {
		idx, ok = m.indices[strings.ToLower(name)]
	}
	if !ok {
		cleaned := strings.Trim(name, "[]\"'`")
		if cleaned != name {
			idx, ok = m.indices[cleaned]
			if !ok {
				idx, ok = m.indices[strings.ToLower(cleaned)]
			}
		}
	}
	return idx, ok
}

// Columns returns the canonical column names in order.
func (m *ColumnIndexMap) Columns() []string {
	if m == nil {
		return nil
	}
	return m.cols
}

// Tuple represents a single row passing through the Volcano execution pipeline.
// Backed by a slice of values and a shared ColumnIndexMap for zero-allocation column resolution.
type Tuple struct {
	Values []any
	Index  *ColumnIndexMap
	Lazy   *storage.LazyTuple
}

// IsNil returns true if the tuple is empty or uninitialized.
func (t Tuple) IsNil() bool {
	return t.Values == nil && t.Index == nil && t.Lazy == nil
}

// Get returns the value for the given column name or expression.
func (t Tuple) Get(col string) any {
	if t.Index == nil {
		return nil
	}
	idx, ok := t.Index.Index(col)
	if !ok || idx < 0 {
		return nil
	}
	if t.Lazy != nil && (idx >= len(t.Values) || t.Values[idx] == nil) {
		val, err := t.Lazy.Get(idx)
		if err == nil {
			if idx < len(t.Values) {
				t.Values[idx] = formatStorageVal(val)
				return t.Values[idx]
			}
		}
	}
	if idx >= len(t.Values) {
		return nil
	}
	return t.Values[idx]
}

// GetOK returns the value and whether the column exists in the tuple schema.
func (t Tuple) GetOK(col string) (any, bool) {
	if t.Index == nil {
		return nil, false
	}
	idx, ok := t.Index.Index(col)
	if !ok || idx < 0 {
		return nil, false
	}
	if t.Lazy != nil && (idx >= len(t.Values) || t.Values[idx] == nil) {
		val, err := t.Lazy.Get(idx)
		if err == nil {
			if idx < len(t.Values) {
				t.Values[idx] = formatStorageVal(val)
				return t.Values[idx], true
			}
		}
	}
	if idx >= len(t.Values) {
		return nil, false
	}
	return t.Values[idx], true
}

// Set sets the value for a column name.
func (t Tuple) Set(col string, val any) {
	if t.Index == nil {
		return
	}
	idx, ok := t.Index.Index(col)
	if ok && idx >= 0 && idx < len(t.Values) {
		t.Values[idx] = val
	}
}

// ToMap converts the slice-backed Tuple to a map[string]any for final serialization.
func (t Tuple) ToMap() map[string]any {
	if t.Index == nil {
		return nil
	}
	if t.Lazy != nil {
		_, _ = t.Lazy.MaterializeRow()
		dec := t.Lazy.DecodedValues()
		for i := 0; i < len(t.Values) && i < len(dec); i++ {
			if t.Values[i] == nil {
				t.Values[i] = formatStorageVal(dec[i])
			}
		}
	}
	res := make(map[string]any, len(t.Index.cols))
	for i, col := range t.Index.cols {
		if i < len(t.Values) {
			v := t.Values[i]
			if dec, ok := v.(storage.Decimal); ok {
				v = dec.String()
			}
			res[col] = v
		}
	}
	return res
}

// ToRow converts the slice-backed Tuple to a storage.Row according to column order or schema.
func (t Tuple) ToRow(schema *storage.TableSchema) storage.Row {
	if t.Lazy != nil {
		if row, err := t.Lazy.MaterializeRow(); err == nil {
			return row
		}
	}
	if schema != nil {
		vals := make([]storage.Value, len(schema.Columns))
		for i, col := range schema.Columns {
			if v, ok := t.GetOK(col.Name); ok {
				vals[i] = anyToStorageValue(v, col.Type)
			} else {
				vals[i] = storage.NullValue(col.Type)
			}
		}
		return storage.Row{Values: vals}
	}
	vals := make([]storage.Value, len(t.Values))
	for i, v := range t.Values {
		vals[i] = anyToStorageValue(v, storage.TypeUnknown)
	}
	return storage.Row{Values: vals}
}

func anyToStorageValue(v any, defaultType storage.DataType) storage.Value {
	if v == nil {
		return storage.NullValue(defaultType)
	}
	switch val := v.(type) {
	case storage.Value:
		return val
	case int64:
		return storage.IntValue(val)
	case int:
		return storage.IntValue(int64(val))
	case float64:
		return storage.FloatValue(val)
	case string:
		return storage.StringValue(val)
	case bool:
		return storage.BoolValue(val)
	case time.Time:
		return storage.DateTimeValue(val)
	case storage.Decimal:
		return storage.DecimalValueFromDecimal(val)
	default:
		return storage.StringValue(fmt.Sprintf("%v", v))
	}
}

// ToEvalMap returns a map of all column names, aliases, table prefixes, and expressions for AST evaluation.
func (t Tuple) ToEvalMap() map[string]any {
	if t.Index == nil {
		return nil
	}
	res := make(map[string]any, len(t.Index.indices))
	for k, idx := range t.Index.indices {
		if idx >= 0 && idx < len(t.Values) {
			v := t.Values[idx]
			if dec, ok := v.(storage.Decimal); ok {
				v = dec.String()
			}
			res[k] = v
		}
	}
	return res
}

// MarshalJSON serializes the tuple as JSON using its ToMap representation.
func (t Tuple) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.ToMap())
}

// UnmarshalJSON deserializes a JSON object into a slice-backed Tuple.
func (t *Tuple) UnmarshalJSON(data []byte) error {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	cols := make([]string, 0, len(m))
	for k := range m {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	*t = NewTupleFromMap(m, cols)
	return nil
}

// CloneTuple creates a copy of the tuple with its own Values slice.
func CloneTuple(t Tuple) Tuple {
	if t.IsNil() {
		return Tuple{}
	}
	clonedVals := make([]any, len(t.Values))
	copy(clonedVals, t.Values)
	return Tuple{
		Values: clonedVals,
		Index:  t.Index,
	}
}

// NewTupleFromMap constructs a Tuple from a map given a list of columns.
func NewTupleFromMap(m map[string]any, cols []string) Tuple {
	idxMap := NewColumnIndexMapFromCols(cols)
	vals := make([]any, len(cols))
	for i, c := range cols {
		if v, ok := m[c]; ok {
			vals[i] = v
		} else if v, ok := m[strings.ToLower(c)]; ok {
			vals[i] = v
		}
	}
	return Tuple{
		Values: vals,
		Index:  idxMap,
	}
}

var tupleValuesPool = sync.Pool{
	New: func() any {
		s := make([]any, 0, 16)
		return &s
	},
}

// AcquireValuesSlice fetches a reusable []any slice with at least length size.
func AcquireValuesSlice(size int) []any {
	ptr := tupleValuesPool.Get().(*[]any)
	s := *ptr
	if cap(s) < size {
		return make([]any, size)
	}
	s = s[:size]
	for i := 0; i < size; i++ {
		s[i] = nil
	}
	return s
}

// ReleaseValuesSlice returns the slice to the pool if it fits within defensive thresholds.
func ReleaseValuesSlice(s []any) {
	if cap(s) > 128 {
		return
	}
	s = s[:0]
	tupleValuesPool.Put(&s)
}

// Iterator is the canonical Volcano-style (pull-based) relational execution operator.
type Iterator interface {
	// Open initializes the operator and its children.
	Open(ctx context.Context) error

	// Next pulls the next tuple from the operator pipeline.
	// Returns (Tuple, nil) when a tuple is available, or (Tuple{}, io.EOF) when iteration finishes.
	Next(ctx context.Context) (Tuple, error)

	// Close releases any allocated resources (cursors, buffers, file handles).
	Close() error

	// Schema returns the output column names for this operator.
	Schema() []string

	// ColIndexMap returns the column index map for O(1) resolution and join composition.
	ColIndexMap() *ColumnIndexMap
}

// MergeColumnIndexMaps merges two ColumnIndexMaps side-by-side for join operations.
func MergeColumnIndexMaps(left, right *ColumnIndexMap) *ColumnIndexMap {
	var leftCols, rightCols []string
	if left != nil {
		leftCols = left.cols
	}
	if right != nil {
		rightCols = right.cols
	}
	combinedCols := make([]string, 0, len(leftCols)+len(rightCols))
	combinedCols = append(combinedCols, leftCols...)
	combinedCols = append(combinedCols, rightCols...)

	m := &ColumnIndexMap{
		indices: make(map[string]int),
		cols:    combinedCols,
	}
	if left != nil {
		for k, idx := range left.indices {
			m.indices[k] = idx
		}
	}
	leftLen := len(leftCols)
	if right != nil {
		for k, idx := range right.indices {
			if strings.Contains(k, ".") {
				m.indices[k] = leftLen + idx
			} else if _, exists := m.indices[k]; !exists {
				m.indices[k] = leftLen + idx
			}
		}
	}
	return m
}

// FormatRecord converts a storage.Record into a Tuple using a precomputed ColumnIndexMap.
func FormatRecord(rec storage.Record, schema *storage.TableSchema, alias string, colMap *ColumnIndexMap) Tuple {
	if rec == nil || schema == nil {
		return Tuple{}
	}
	if colMap == nil {
		colMap = NewColumnIndexMap(schema, alias)
	}

	n := len(schema.Columns)
	vals := make([]any, n)
	for i, c := range schema.Columns {
		if stVal, ok := rec[c.Name]; ok {
			vals[i] = formatStorageVal(stVal)
		}
	}

	return Tuple{
		Values: vals,
		Index:  colMap,
	}
}

// FormatRow converts a storage.Row into a Tuple using precomputed column offsets without map lookups.
func FormatRow(row storage.Row, schema *storage.TableSchema, alias string, colMap *ColumnIndexMap) Tuple {
	return FormatRowWithArena(row, schema, alias, colMap, nil)
}

// FormatRowWithArena converts a storage.Row into a Tuple using arena-allocated value slices if arena is non-nil.
func FormatRowWithArena(row storage.Row, schema *storage.TableSchema, alias string, colMap *ColumnIndexMap, arena *storage.Arena) Tuple {
	if schema == nil {
		return Tuple{}
	}
	if colMap == nil {
		colMap = NewColumnIndexMap(schema, alias)
	}

	n := len(schema.Columns)
	var vals []any
	if arena != nil {
		vals = arena.AllocAnySlice(n)
	} else {
		vals = make([]any, n)
	}
	numVals := len(row.Values)
	for i := 0; i < n && i < numVals; i++ {
		vals[i] = formatStorageVal(row.Values[i])
	}

	return Tuple{
		Values: vals,
		Index:  colMap,
	}
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
	case storage.TypeDecimal:
		return v.Decimal()
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

	// Exact Decimal comparison if either value is storage.Decimal
	if isDecValue(a) || isDecValue(b) {
		if decA, okA := tryDecimal(a); okA {
			if decB, okB := tryDecimal(b); okB {
				return decA.Cmp(decB)
			}
		}
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
	case string:
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			return f, true
		}
		return 0, false
	default:
		if num, ok := v.(interface{ Float64() (float64, error) }); ok {
			if f, err := num.Float64(); err == nil {
				return f, true
			}
		}
		return 0, false
	}
}

func isDecValue(v any) bool {
	if v == nil {
		return false
	}
	switch n := v.(type) {
	case storage.Decimal:
		return true
	case storage.Value:
		return n.Type == storage.TypeDecimal
	default:
		return false
	}
}

func tryDecimal(v any) (storage.Decimal, bool) {
	if v == nil {
		return storage.ZeroDecimal(0), false
	}
	switch n := v.(type) {
	case storage.Decimal:
		return n, true
	case storage.Value:
		return n.Decimal(), true
	case int64:
		return storage.NewDecimalFromInt(n, 0), true
	case int:
		return storage.NewDecimalFromInt(int64(n), 0), true
	case float64:
		return storage.NewDecimalFromFloat(n, 4), true
	case string:
		if d, err := storage.ParseDecimal(n); err == nil {
			return d, true
		}
	}
	return storage.ZeroDecimal(0), false
}


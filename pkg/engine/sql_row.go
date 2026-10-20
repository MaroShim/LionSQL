package engine

import (
	"fmt"
	"sort"
	"strings"

	"sqlion/pkg/storage"
)

// SQLRow encapsulates a coordinator or evaluation row, providing case-insensitive,
// table-alias-aware column access without raw Go map iteration hazards.
type SQLRow struct {
	values  map[string]any
	columns []string
}

// NewSQLRow creates an SQLRow wrapping values and an optional column ordering.
func NewSQLRow(values map[string]any, cols ...[]string) *SQLRow {
	var c []string
	if len(cols) > 0 && len(cols[0]) > 0 {
		c = make([]string, len(cols[0]))
		copy(c, cols[0])
	}
	v := values
	if v == nil {
		v = make(map[string]any)
	}
	return &SQLRow{
		values:  v,
		columns: c,
	}
}

// Get returns the column value by checking:
// 1. Exact match
// 2. Table-prefix stripped match: e.g. "users.id" -> "id"
// 3. Case-insensitive match via strings.EqualFold
func (r *SQLRow) Get(colName string) (any, bool) {
	if r == nil || r.values == nil {
		return nil, false
	}
	// 1. Exact match
	if v, ok := r.values[colName]; ok {
		return v, true
	}
	// 2. Table-prefix stripped match
	bare := colName
	if idx := strings.Index(colName, "."); idx != -1 {
		bare = colName[idx+1:]
		if v, ok := r.values[bare]; ok {
			return v, true
		}
	}
	// 3. Case-insensitive match
	for k, v := range r.values {
		if strings.EqualFold(k, colName) || strings.EqualFold(k, bare) {
			return v, true
		}
		if kDot := strings.Index(k, "."); kDot != -1 {
			kBare := k[kDot+1:]
			if strings.EqualFold(kBare, colName) || strings.EqualFold(kBare, bare) {
				return v, true
			}
		}
	}
	return nil, false
}

// GetFirst returns the value of the first specified column in 'cols', or
// if 'cols' is empty or unmatched, returns the first column defined in 'r.columns',
// or falls back to the first key in lexicographically sorted order.
// This guarantees 100% deterministic execution and eliminates random map iteration.
func (r *SQLRow) GetFirst(cols ...string) (any, bool) {
	if r == nil || len(r.values) == 0 {
		return nil, false
	}
	for _, c := range cols {
		if v, ok := r.Get(c); ok {
			return v, true
		}
	}
	// Check defined columns sequence
	for _, c := range r.columns {
		if v, ok := r.Get(c); ok {
			return v, true
		}
	}
	// Deterministic fallback to sorted keys
	keys := r.SortedKeys()
	if len(keys) > 0 {
		return r.values[keys[0]], true
	}
	return nil, false
}

// Set sets the column value, updating existing key if case-insensitively matched.
func (r *SQLRow) Set(colName string, val any) {
	if r == nil {
		return
	}
	if r.values == nil {
		r.values = make(map[string]any)
	}
	for k := range r.values {
		if strings.EqualFold(k, colName) {
			r.values[k] = val
			return
		}
	}
	r.values[colName] = val
}

// SortedKeys returns all column keys sorted lexicographically.
func (r *SQLRow) SortedKeys() []string {
	if r == nil || len(r.values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(r.values))
	for k := range r.values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Columns returns the defined column names, or sorted keys if none were set.
func (r *SQLRow) Columns() []string {
	if r == nil {
		return nil
	}
	if len(r.columns) > 0 {
		return r.columns
	}
	return r.SortedKeys()
}

// ToMap returns the underlying map[string]any.
func (r *SQLRow) ToMap() map[string]any {
	if r == nil {
		return nil
	}
	return r.values
}

// ToRecord converts the SQLRow into a storage.Record mapped according to schema.
func (r *SQLRow) ToRecord(schema *storage.TableSchema) storage.Record {
	if r == nil || schema == nil {
		return make(storage.Record)
	}
	rec := make(storage.Record, len(schema.Columns))
	for _, col := range schema.Columns {
		if v, ok := r.Get(col.Name); ok && v != nil {
			rec[col.Name] = parseLiteralValue(formatRowVal(v), col.Type)
		} else {
			rec[col.Name] = storage.NullValue(col.Type)
		}
	}
	return rec
}

func formatRowVal(v any) string {
	if v == nil {
		return "NULL"
	}
	if sv, ok := v.(storage.Value); ok {
		return fmt.Sprintf("%v", formatStorageValue(sv))
	}
	return fmt.Sprintf("%v", v)
}

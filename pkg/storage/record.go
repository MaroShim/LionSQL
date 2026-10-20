package storage

import (
	"sort"
	"strings"

	"lionsql/pkg/sqlutil"
)

// Get retrieves a value by column name with case-insensitivity and table-prefix awareness.
// Order of resolution:
// 1. Exact key match in r[colName]
// 2. Table-prefix stripped match: e.g. "users.id" -> "id"
// 3. Case-insensitive match via strings.EqualFold
func (r Record) Get(colName string) (Value, bool) {
	if r == nil {
		return NullValue(TypeUnknown), false
	}
	// 1. Exact match
	if v, ok := r[colName]; ok {
		return v, true
	}
	// 2. Table prefix stripped match
	bare := colName
	if idx := strings.Index(colName, "."); idx != -1 {
		bare = colName[idx+1:]
		if v, ok := r[bare]; ok {
			return v, true
		}
	}
	// 3. Case-insensitive and quote-tolerant match
	for k, v := range r {
		if sqlutil.EqualFoldIdent(k, colName) || sqlutil.EqualFoldIdent(k, bare) {
			return v, true
		}
		if kDot := strings.Index(k, "."); kDot != -1 {
			kBare := k[kDot+1:]
			if sqlutil.EqualFoldIdent(kBare, colName) || sqlutil.EqualFoldIdent(kBare, bare) {
				return v, true
			}
		}
	}
	return NullValue(TypeUnknown), false
}

// GetValue returns the value if found, or a NullValue if missing.
func (r Record) GetValue(colName string) Value {
	v, _ := r.Get(colName)
	return v
}

// Set sets the column value, updating the existing key if it matches case-insensitively,
// thereby preserving canonical schema casing and preventing duplicate case-variant keys.
func (r Record) Set(colName string, val Value) {
	if r == nil {
		return
	}
	for k := range r {
		if sqlutil.EqualFoldIdent(k, colName) {
			r[k] = val
			return
		}
	}
	r[colName] = val
}

// Delete removes a column from the record case-insensitively.
func (r Record) Delete(colName string) bool {
	if r == nil {
		return false
	}
	for k := range r {
		if sqlutil.EqualFoldIdent(k, colName) {
			delete(r, k)
			return true
		}
	}
	return false
}

// FirstValue returns the value of the first matching column in 'cols',
// or if 'cols' is empty/unmatched, returns the value of the first key in
// lexicographically sorted order. This guarantees 100% deterministic execution
// and eliminates Go runtime map iteration randomness.
func (r Record) FirstValue(cols ...string) (Value, bool) {
	if len(r) == 0 {
		return NullValue(TypeUnknown), false
	}
	for _, c := range cols {
		if v, ok := r.Get(c); ok {
			return v, true
		}
	}
	keys := r.SortedKeys()
	if len(keys) > 0 {
		return r[keys[0]], true
	}
	return NullValue(TypeUnknown), false
}

// SortedKeys returns all column keys sorted lexicographically.
func (r Record) SortedKeys() []string {
	if len(r) == 0 {
		return nil
	}
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Clone creates a shallow copy of the Record.
func (r Record) Clone() Record {
	if r == nil {
		return nil
	}
	cloned := make(Record, len(r))
	for k, v := range r {
		cloned[k] = v
	}
	return cloned
}

// ToRow converts the Record into a slice-backed Row aligned with the provided schema.
func (r Record) ToRow(schema *TableSchema) Row {
	if schema == nil {
		return Row{}
	}
	return schema.RecordToRow(r)
}

// ExtractIndexKeyFromRecord extracts the index key Value from a Record based on the IndexSchema.
// It is case-insensitive, prefix-aware, and supports single-column, composite, and expression/JSON indexes.
func ExtractIndexKeyFromRecord(rec Record, idx *IndexSchema) Value {
	if idx == nil {
		return NullValue(TypeUnknown)
	}
	if idx.IsExpression() {
		colName := idx.ExprColumn
		if colName == "" && len(idx.Columns) > 0 {
			colName = idx.Columns[0]
		}
		if docVal, ok := rec.Get(colName); ok && !docVal.IsNull {
			return ExtractJSONIndexKey(docVal.String(), idx.ExprPath)
		}
		return NullValue(TypeVarChar)
	}
	if len(idx.Columns) == 0 {
		return NullValue(TypeUnknown)
	}
	if len(idx.Columns) == 1 {
		if v, ok := rec.Get(idx.Columns[0]); ok {
			return v
		}
		return NullValue(TypeUnknown)
	}
	var sb strings.Builder
	for i, c := range idx.Columns {
		if i > 0 {
			sb.WriteByte(0)
		}
		if v, ok := rec.Get(c); ok {
			sb.WriteString(EncodeIndexKeyPart(v))
		} else {
			sb.WriteString("NULL")
		}
	}
	return StringValue(sb.String())
}


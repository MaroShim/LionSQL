package storage

import (
	"strings"
)

// ExtractIndexKeyFromRow extracts an index key Value from a schema-aligned Row based on the IndexSchema.
// It is zero-alloc for single-column indexes, and avoids creating intermediate Record maps.
func ExtractIndexKeyFromRow(schema *TableSchema, row Row, idx *IndexSchema) Value {
	if idx == nil || schema == nil {
		return NullValue(TypeUnknown)
	}
	if idx.IsExpression() {
		colName := idx.ExprColumn
		if colName == "" && len(idx.Columns) > 0 {
			colName = idx.Columns[0]
		}
		if col, colIdx, ok := schema.FindColumn(colName); ok && colIdx >= 0 && colIdx < len(row.Values) {
			_ = col
			docVal := row.Values[colIdx]
			if !docVal.IsNull {
				return ExtractJSONIndexKey(docVal.String(), idx.ExprPath)
			}
		}
		return NullValue(TypeVarChar)
	}
	if len(idx.Columns) == 0 {
		return NullValue(TypeUnknown)
	}
	if len(idx.Columns) == 1 {
		if _, colIdx, ok := schema.FindColumn(idx.Columns[0]); ok && colIdx >= 0 && colIdx < len(row.Values) {
			return row.Values[colIdx]
		}
		return NullValue(TypeUnknown)
	}

	var sb strings.Builder
	for i, c := range idx.Columns {
		if i > 0 {
			sb.WriteByte(0)
		}
		if _, colIdx, ok := schema.FindColumn(c); ok && colIdx >= 0 && colIdx < len(row.Values) {
			sb.WriteString(EncodeIndexKeyPart(row.Values[colIdx]))
		} else {
			sb.WriteString("\x00NULL")
		}
	}
	return StringValue(sb.String())
}

package sqlutil

import (
	"fmt"
	"sort"
	"strings"
)

type nullValueChecker interface {
	IsNullValue() bool
}

// DeduplicateRows removes duplicate rows from a result set for SELECT DISTINCT.
// Rows are compared by serializing all column key-value pairs in sorted key order.
func DeduplicateRows(rows []map[string]any) []map[string]any {
	if len(rows) == 0 {
		return rows
	}
	seen := make(map[string]struct{}, len(rows))
	out := make([]map[string]any, 0, len(rows))
	var buf strings.Builder
	for _, row := range rows {
		keys := make([]string, 0, len(row))
		for k := range row {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		buf.Reset()
		for _, k := range keys {
			buf.WriteString(k)
			buf.WriteByte('=')
			v := row[k]
			if v == nil {
				buf.WriteString("NULL")
			} else if nc, ok := v.(nullValueChecker); ok && nc.IsNullValue() {
				buf.WriteString("NULL")
			} else {
				fmt.Fprintf(&buf, "%v", v)
			}
			buf.WriteByte('\x00')
		}
		key := buf.String()
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			out = append(out, row)
		}
	}
	return out
}

// DeduplicateRowsAndValues removes duplicate rows and their corresponding value slices for SELECT DISTINCT.
func DeduplicateRowsAndValues(rows []map[string]any, values [][]any) ([]map[string]any, [][]any) {
	if len(values) > 0 {
		seen := make(map[string]struct{}, len(values))
		dedupRows := make([]map[string]any, 0, len(rows))
		dedupValues := make([][]any, 0, len(values))
		var buf strings.Builder
		for idx, rowVals := range values {
			buf.Reset()
			for _, v := range rowVals {
				if v == nil {
					buf.WriteString("NULL\x00")
				} else if nc, ok := v.(nullValueChecker); ok && nc.IsNullValue() {
					buf.WriteString("NULL\x00")
				} else {
					fmt.Fprintf(&buf, "%v\x00", v)
				}
			}
			key := buf.String()
			if _, dup := seen[key]; !dup {
				seen[key] = struct{}{}
				dedupValues = append(dedupValues, rowVals)
				if idx < len(rows) {
					dedupRows = append(dedupRows, rows[idx])
				}
			}
		}
		return dedupRows, dedupValues
	}
	return DeduplicateRows(rows), nil
}

package engine

import (
	"lionsql/pkg/storage"
)

// NormalizeRecordKeys returns a new Record where all keys that match schema columns
// (case-insensitively or table-prefixed) are mapped to their canonical schema Column.Name.
func NormalizeRecordKeys(schema *storage.TableSchema, rec storage.Record) storage.Record {
	if schema == nil || rec == nil {
		return rec
	}
	normalized := make(storage.Record, len(rec))
	for k, v := range rec {
		cleanK := cleanIdentifier(k)
		if colDef, _, found := schema.FindColumn(cleanK); found && colDef != nil {
			normalized[colDef.Name] = v
		} else {
			normalized[cleanK] = v
		}
	}
	return normalized
}

// NormalizeMapKeys returns a new map[string]any where keys are normalized to schema Column.Name.
func NormalizeMapKeys(schema *storage.TableSchema, m map[string]any) map[string]any {
	if schema == nil || m == nil {
		return m
	}
	normalized := make(map[string]any, len(m))
	for k, v := range m {
		cleanK := cleanIdentifier(k)
		if colDef, _, found := schema.FindColumn(cleanK); found && colDef != nil {
			normalized[colDef.Name] = v
		} else {
			normalized[cleanK] = v
		}
	}
	return normalized
}

// NormalizeIdentifier returns the canonical schema column name if it exists,
// or the original identifier if no match is found.
func NormalizeIdentifier(schema *storage.TableSchema, ident string) string {
	if schema == nil || ident == "" {
		return ident
	}
	clean := cleanIdentifier(ident)
	if colDef, _, found := schema.FindColumn(clean); found && colDef != nil {
		return colDef.Name
	}
	return ident
}

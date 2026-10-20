package storage

import (
	"strings"
)

// SyntheticPKKind represents the scope or source of a synthetic primary key.
type SyntheticPKKind int

const (
	// SyntheticPKRowID represents the default synthetic PK for PK-less user tables (_row_id).
	SyntheticPKRowID SyntheticPKKind = iota
	// SyntheticPKDerivedTable represents the synthetic PK for materialized derived tables (_dt_row_id).
	SyntheticPKDerivedTable
	// SyntheticPKCTE represents the synthetic PK for materialized CTE tables (_cte_row_id).
	SyntheticPKCTE
)

const (
	// SyntheticPKRowIDName is the canonical column name for default synthetic primary keys.
	SyntheticPKRowIDName = "_row_id"
	// SyntheticPKDerivedName is the canonical column name for derived table synthetic primary keys.
	SyntheticPKDerivedName = "_dt_row_id"
	// SyntheticPKCTEName is the canonical column name for CTE synthetic primary keys.
	SyntheticPKCTEName = "_cte_row_id"
)

// NewSyntheticPKColumn creates a standardized hidden INT IDENTITY PrimaryKey column.
func NewSyntheticPKColumn(kind SyntheticPKKind) Column {
	name := SyntheticPKRowIDName
	switch kind {
	case SyntheticPKDerivedTable:
		name = SyntheticPKDerivedName
	case SyntheticPKCTE:
		name = SyntheticPKCTEName
	}
	return Column{
		Name:       name,
		Type:       TypeInt,
		PrimaryKey: true,
		Identity:   true,
		Hidden:     true,
	}
}

// IsSyntheticPKName checks whether colName matches any known synthetic primary key name case-insensitively.
func IsSyntheticPKName(colName string) bool {
	return strings.EqualFold(colName, SyntheticPKRowIDName) ||
		strings.EqualFold(colName, SyntheticPKDerivedName) ||
		strings.EqualFold(colName, SyntheticPKCTEName)
}

// HasSyntheticPK reports whether the table schema uses a synthetic primary key.
func (s *TableSchema) HasSyntheticPK() bool {
	if s == nil {
		return false
	}
	for i := range s.Columns {
		c := &s.Columns[i]
		if c.PrimaryKey && (c.Hidden || IsSyntheticPKName(c.Name)) {
			return true
		}
	}
	return false
}

// SyntheticPKColumn returns the synthetic PK column if present, or nil.
func (s *TableSchema) SyntheticPKColumn() *Column {
	if s == nil {
		return nil
	}
	for i := range s.Columns {
		c := &s.Columns[i]
		if c.PrimaryKey && (c.Hidden || IsSyntheticPKName(c.Name)) {
			return c
		}
	}
	return nil
}

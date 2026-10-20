package storage

import (
	"testing"
)

func TestSyntheticPK(t *testing.T) {
	colRowID := NewSyntheticPKColumn(SyntheticPKRowID)
	if colRowID.Name != "_row_id" || !colRowID.PrimaryKey || !colRowID.Hidden {
		t.Fatalf("unexpected colRowID: %+v", colRowID)
	}

	colDT := NewSyntheticPKColumn(SyntheticPKDerivedTable)
	if colDT.Name != "_dt_row_id" || !colDT.PrimaryKey || !colDT.Hidden {
		t.Fatalf("unexpected colDT: %+v", colDT)
	}

	colCTE := NewSyntheticPKColumn(SyntheticPKCTE)
	if colCTE.Name != "_cte_row_id" || !colCTE.PrimaryKey || !colCTE.Hidden {
		t.Fatalf("unexpected colCTE: %+v", colCTE)
	}

	if !IsSyntheticPKName("_row_id") || !IsSyntheticPKName("_DT_ROW_ID") || !IsSyntheticPKName("_cte_row_id") {
		t.Errorf("IsSyntheticPKName failed")
	}
	if IsSyntheticPKName("id") || IsSyntheticPKName("user_id") {
		t.Errorf("IsSyntheticPKName false positive for natural PK")
	}

	schema := &TableSchema{
		Name:    "test_synthetic",
		Columns: []Column{colRowID, {Name: "data", Type: TypeVarChar}},
	}
	if !schema.HasSyntheticPK() {
		t.Errorf("expected HasSyntheticPK to be true")
	}
	if schema.SyntheticPKColumn() == nil || schema.SyntheticPKColumn().Name != "_row_id" {
		t.Errorf("expected SyntheticPKColumn to return _row_id")
	}

	naturalSchema := &TableSchema{
		Name: "test_natural",
		Columns: []Column{
			{Name: "id", Type: TypeInt, PrimaryKey: true},
			{Name: "data", Type: TypeVarChar},
		},
	}
	if naturalSchema.HasSyntheticPK() {
		t.Errorf("natural schema should not report HasSyntheticPK")
	}
}

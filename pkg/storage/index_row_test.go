package storage

import (
	"reflect"
	"testing"
)

func TestExtractIndexKeyFromRow_ParityWithRecord(t *testing.T) {
	schema := &TableSchema{
		Name: "users",
		Columns: []Column{
			{Name: "id", Type: TypeInt, PrimaryKey: true},
			{Name: "first_name", Type: TypeVarChar},
			{Name: "last_name", Type: TypeVarChar},
			{Name: "extra", Type: TypeVarChar},
		},
		Indexes: []IndexSchema{
			{Name: "idx_single", Columns: []string{"first_name"}},
			{Name: "idx_composite", Columns: []string{"last_name", "first_name"}},
			{Name: "idx_expr", ExprColumn: "extra", ExprPath: "$.city"},
		},
	}

	row := Row{
		Values: []Value{
			IntValue(42),
			StringValue("Alice"),
			StringValue("Smith"),
			StringValue(`{"city":"Seoul"}`),
		},
	}
	rec := schema.RowToRecord(row)

	for _, idx := range schema.Indexes {
		idxCopy := idx
		keyFromRec := ExtractIndexKeyFromRecord(rec, &idxCopy)
		keyFromRow := ExtractIndexKeyFromRow(schema, row, &idxCopy)

		if !reflect.DeepEqual(keyFromRec, keyFromRow) {
			t.Errorf("Index %q mismatch: Record=%v, Row=%v", idx.Name, keyFromRec, keyFromRow)
		}
	}
}

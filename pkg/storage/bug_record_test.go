package storage_test

import (
	"testing"

	"sqlion/pkg/storage"
)

// TestRegression_BUG_RECORD_1 verifies that RecordToRow and ApplyRecordToRow
// handle case differences between Record keys and TableSchema column names,
// preventing silent data loss or NULL insertion.
func TestRegression_BUG_RECORD_1(t *testing.T) {
	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "user_id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "user_name", Type: storage.TypeVarChar},
			{Name: "email", Type: storage.TypeVarChar},
		},
	}

	// 1. Record with uppercase keys matching lowercase schema
	rec := storage.Record{
		"USER_ID":   storage.IntValue(42),
		"USER_NAME": storage.StringValue("Alice"),
		"EMAIL":     storage.StringValue("alice@example.com"),
	}

	row := schema.RecordToRow(rec)
	if row.Values[0].IsNull || row.Values[0].IVal != 42 {
		t.Fatalf("RecordToRow failed for user_id: got %v", row.Values[0])
	}
	if row.Values[1].IsNull || row.Values[1].String() != "Alice" {
		t.Fatalf("RecordToRow failed for user_name: got %v", row.Values[1])
	}
	if row.Values[2].IsNull || row.Values[2].String() != "alice@example.com" {
		t.Fatalf("RecordToRow failed for email: got %v", row.Values[2])
	}

	// 2. ApplyRecordToRow with mixed-case update
	baseRow := storage.Row{Values: []storage.Value{
		storage.IntValue(42),
		storage.StringValue("Alice"),
		storage.StringValue("old@example.com"),
	}}
	updateRec := storage.Record{
		"Email": storage.StringValue("new@example.com"),
	}

	updatedRow := schema.ApplyRecordToRow(baseRow, updateRec)
	if updatedRow.Values[2].String() != "new@example.com" {
		t.Fatalf("ApplyRecordToRow failed: expected 'new@example.com', got %v", updatedRow.Values[2])
	}
}

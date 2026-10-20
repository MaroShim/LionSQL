package storage_test

import (
	"testing"

	"sqlion/pkg/storage"
)

// TestRegression_BUG_CASE_1 verifies that TableSchema.FindColumn and FindIndex
// perform case-insensitive matching (strings.EqualFold).
func TestRegression_BUG_CASE_1(t *testing.T) {
	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "UserID", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "EmailAddress", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{Name: "IX_Users_Email", TableName: "users", Columns: []string{"EmailAddress"}, Unique: true},
		},
	}

	// 1. FindColumn case variations
	col, idx, ok := schema.FindColumn("userid")
	if !ok || col == nil || idx != 0 {
		t.Fatalf("FindColumn('userid') failed: ok=%v, col=%v, idx=%d", ok, col, idx)
	}

	col, idx, ok = schema.FindColumn("EMAILADDRESS")
	if !ok || col == nil || idx != 1 {
		t.Fatalf("FindColumn('EMAILADDRESS') failed: ok=%v, col=%v, idx=%d", ok, col, idx)
	}

	// 2. FindIndex case variations
	index, idxPos, ok := schema.FindIndex("ix_users_email")
	if !ok || index == nil || idxPos != 0 {
		t.Fatalf("FindIndex('ix_users_email') failed: ok=%v, index=%v, idxPos=%d", ok, index, idxPos)
	}
}

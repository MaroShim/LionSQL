package execution

import (
	"context"
	"testing"

	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// TestDEFECT_INLJ_NullKeyScan_NoMatchingNull verifies that an outer tuple with a NULL join key
// is never matched against inner rows having NULL in the indexed column (SQL 3VL invariant: NULL = NULL is FALSE).
func TestDEFECT_INLJ_NullKeyScan_NoMatchingNull(t *testing.T) {
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("eng.Open failed: %v", err)
	}
	defer eng.Close()

	// 1. Create inner table with secondary index on category (which contains NULL)
	innerSchema := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "cat", Type: storage.TypeVarChar, Nullable: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: false},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_items_cat", TableName: "items", Columns: []string{"cat"}},
		},
	}
	if err := eng.CreateTable(innerSchema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("eng.NewSession failed: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	// Insert rows into items: one with cat=NULL, one with cat='books'
	_ = tx.Insert("items", storage.Record{"id": storage.IntValue(1), "cat": storage.NullValue(storage.TypeVarChar), "name": storage.StringValue("ItemWithNullCat")})
	_ = tx.Insert("items", storage.Record{"id": storage.IntValue(2), "cat": storage.StringValue("books"), "name": storage.StringValue("GoBook")})

	// 2. Outer mock iterator: one row with NULL category
	outerColMap := &ColumnIndexMap{
		indices: map[string]int{"req_cat": 0},
		cols:    []string{"req_cat"},
	}
	outerRows := []Tuple{
		// Case A: keyVal is storage.Value{IsNull: true}
		{
			Values: []any{storage.NullValue(storage.TypeVarChar)},
			Index:  outerColMap,
		},
		// Case B: keyVal is untyped nil
		{
			Values: []any{nil},
			Index:  outerColMap,
		},
	}
	outerMock := &mockIterator{
		tuples: outerRows,
		schema: []string{"req_cat"},
		colMap: outerColMap,
	}

	// 3. INNER IndexNestedLoopJoin on req_cat = items.cat
	innerIdx := &innerSchema.Indexes[0]
	inlj := NewIndexNestedLoopJoinIterator(outerMock, tx, "items", "items", innerIdx, innerSchema, "req_cat", "INNER")
	if err := inlj.Open(context.Background()); err != nil {
		t.Fatalf("inlj.Open failed: %v", err)
	}
	defer inlj.Close()

	// Must return ZERO rows because NULL never equals NULL in an INNER JOIN
	matchedCount := 0
	for {
		tuple, err := inlj.Next(context.Background())
		if err != nil {
			break
		}
		if !tuple.IsNil() {
			matchedCount++
			t.Errorf("[DEFECT-INLJ-NULL-KEY-SCAN-1] Unexpected match found for NULL join key: %v", tuple.Values)
		}
	}

	if matchedCount != 0 {
		t.Fatalf("Expected 0 matches for NULL join key in INNER JOIN, but got %d matches!", matchedCount)
	}
}

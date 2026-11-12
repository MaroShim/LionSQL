package execution_test

import (
	"context"
	"io"
	"testing"

	"lionsql/pkg/execution"
)

// TestTuple_DefensivePadding_GetOK_ToMap verifies that a tuple with fewer values
// than its schema (e.g. from instant ADD COLUMN) returns ok=true for missing columns
// with nil value, and includes them in ToMap() and ToEvalMap() without panics.
func TestTuple_DefensivePadding_GetOK_ToMap(t *testing.T) {
	cols := []string{"id", "name", "created_at", "status"}
	colMap := execution.NewColumnIndexMapFromCols(cols)

	// Simulating an older tuple with only 2 values (created before created_at and status were added)
	tuple := execution.Tuple{
		Values: []any{int64(10), "Alice"},
		Index:  colMap,
	}

	// 1. Get on missing column should safely return nil
	if v := tuple.Get("created_at"); v != nil {
		t.Fatalf("expected nil for created_at, got %v", v)
	}

	// 2. GetOK on existing schema column must report exists=true with value nil
	val, ok := tuple.GetOK("created_at")
	if !ok {
		t.Fatalf("GetOK('created_at') returned ok=false; expected ok=true because column exists in schema")
	}
	if val != nil {
		t.Fatalf("GetOK('created_at') returned non-nil value: %v", val)
	}

	// Non-existent column should still report ok=false
	if _, ok := tuple.GetOK("non_existent_col"); ok {
		t.Fatalf("GetOK('non_existent_col') returned ok=true; expected false")
	}

	// 3. ToMap() must include missing columns with nil values (essential for JSON serialization)
	m := tuple.ToMap()
	if len(m) != 4 {
		t.Fatalf("ToMap() returned %d keys, expected 4", len(m))
	}
	if v, exists := m["created_at"]; !exists || v != nil {
		t.Fatalf("ToMap()['created_at'] expected exists=true with nil, got exists=%v val=%v", exists, v)
	}
	if v, exists := m["status"]; !exists || v != nil {
		t.Fatalf("ToMap()['status'] expected exists=true with nil, got exists=%v val=%v", exists, v)
	}

	// 4. ToEvalMap() must also register the missing column keys with nil
	evalMap := tuple.ToEvalMap()
	if v, exists := evalMap["created_at"]; !exists || v != nil {
		t.Fatalf("ToEvalMap()['created_at'] expected exists=true with nil, got exists=%v val=%v", exists, v)
	}

	// 5. Materialize / PadToSchema auto-pads Values slice to match schema width
	tuple.Materialize()
	if len(tuple.Values) != 4 {
		t.Fatalf("expected tuple.Values to be padded to len 4, got %d", len(tuple.Values))
	}
	if tuple.Values[2] != nil || tuple.Values[3] != nil {
		t.Fatalf("padded values should be nil: %v, %v", tuple.Values[2], tuple.Values[3])
	}
}

// TestProjection_DefensivePadding_NarrowTuple verifies that ProjectionIterator
// safely handles child tuples narrower than the child's schema without panics.
func TestProjection_DefensivePadding_NarrowTuple(t *testing.T) {
	ctx := context.Background()
	schema := []string{"id", "name", "score"}
	colMap := execution.NewColumnIndexMapFromCols(schema)

	// Child iterator yields a tuple with only 2 values (missing score)
	narrowTuple := execution.Tuple{
		Values: []any{int64(42), "Bob"},
		Index:  colMap,
	}

	mock := newMockIterWithTuple(schema, colMap, narrowTuple)
	proj := execution.NewProjectionIterator(mock, []string{"id", "name", "score"})
	if err := proj.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer proj.Close()

	out, err := proj.Next(ctx)
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}

	if len(out.Values) != 3 {
		t.Fatalf("expected 3 projected values, got %d", len(out.Values))
	}
	if out.Values[0] != int64(42) {
		t.Errorf("expected id=42, got %v", out.Values[0])
	}
	if out.Values[1] != "Bob" {
		t.Errorf("expected name='Bob', got %v", out.Values[1])
	}
	if out.Values[2] != nil {
		t.Errorf("expected score=nil, got %v", out.Values[2])
	}
}

// TestJoin_DefensivePadding_OffsetAlignment verifies that when left tuple is narrower
// than left schema, right columns are copied to the correct offset matching combinedIndex.
func TestJoin_DefensivePadding_OffsetAlignment(t *testing.T) {
	ctx := context.Background()

	leftSchema := []string{"u.id", "u.name", "u.email"}
	leftColMap := execution.NewColumnIndexMapFromCols(leftSchema)
	// Left tuple missing "u.email" (only 2 values)
	leftTuple := execution.Tuple{
		Values: []any{int64(100), "Alice"},
		Index:  leftColMap,
	}

	rightSchema := []string{"o.order_id", "o.amount"}
	rightColMap := execution.NewColumnIndexMapFromCols(rightSchema)
	rightTuple := execution.Tuple{
		Values: []any{int64(100), 50.5},
		Index:  rightColMap,
	}

	leftMock := newMockIterWithTuple(leftSchema, leftColMap, leftTuple)
	rightMock := newMockIterWithTuple(rightSchema, rightColMap, rightTuple)

	// HashJoin on u.id = o.order_id
	joinIter := execution.NewHashJoinIterator(leftMock, rightMock, "INNER", "u.id", "o.order_id")
	if err := joinIter.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer joinIter.Close()

	joined, err := joinIter.Next(ctx)
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}

	// Verify columns are resolved at the correct offsets without off-by-one corruption
	if v := joined.Get("u.id"); v != int64(100) {
		t.Errorf("expected u.id=100, got %v", v)
	}
	if v := joined.Get("u.name"); v != "Alice" {
		t.Errorf("expected u.name='Alice', got %v", v)
	}
	if v := joined.Get("u.email"); v != nil {
		t.Errorf("expected u.email=nil, got %v", v)
	}
	// Crucial check: o.order_id MUST be 100, not shifted onto u.email offset!
	if v := joined.Get("o.order_id"); v != int64(100) {
		t.Errorf("CRITICAL OFF-BY-ONE CORRUPTION: expected o.order_id=100, got %v", v)
	}
	if v := joined.Get("o.amount"); v != 50.5 {
		t.Errorf("CRITICAL OFF-BY-ONE CORRUPTION: expected o.amount=50.5, got %v", v)
	}
}

type singleTupleMock struct {
	schema []string
	colMap *execution.ColumnIndexMap
	tuple  execution.Tuple
	called bool
}

func newMockIterWithTuple(schema []string, colMap *execution.ColumnIndexMap, tuple execution.Tuple) *singleTupleMock {
	return &singleTupleMock{
		schema: schema,
		colMap: colMap,
		tuple:  tuple,
	}
}

func (m *singleTupleMock) Open(ctx context.Context) error {
	m.called = false
	return nil
}

func (m *singleTupleMock) Next(ctx context.Context) (execution.Tuple, error) {
	if m.called {
		return execution.Tuple{}, io.EOF
	}
	m.called = true
	return m.tuple, nil
}

func (m *singleTupleMock) Close() error {
	return nil
}

func (m *singleTupleMock) Schema() []string {
	return m.schema
}

func (m *singleTupleMock) ColIndexMap() *execution.ColumnIndexMap {
	return m.colMap
}

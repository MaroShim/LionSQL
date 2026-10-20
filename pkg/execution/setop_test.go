package execution_test

import (
	"context"
	"io"
	"reflect"
	"testing"

	"lionsql/pkg/execution"
)

func collectTuples(t *testing.T, it execution.Iterator) []map[string]any {
	t.Helper()
	ctx := context.Background()
	if err := it.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer it.Close()

	var rows []map[string]any
	for {
		tuple, err := it.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next failed: %v", err)
		}
		rows = append(rows, tuple.ToMap())
	}
	return rows
}

func TestSetOp_UnionAll(t *testing.T) {
	schemaA := []string{"id", "val"}
	schemaB := []string{"b_id", "b_val"} // Different column names, but same length

	left := newMockIterator(schemaA,
		map[string]any{"id": int64(1), "val": "A"},
		map[string]any{"id": int64(2), "val": "B"},
	)
	right := newMockIterator(schemaB,
		map[string]any{"b_id": int64(2), "b_val": "B"}, // duplicate
		map[string]any{"b_id": int64(3), "b_val": "C"},
	)

	it := execution.NewSetOpIterator(left, right, execution.SetOpUnionAll)
	rows := collectTuples(t, it)

	if len(rows) != 4 {
		t.Fatalf("expected 4 rows for UNION ALL, got %d", len(rows))
	}

	// Schema names should match left iterator
	expected := []map[string]any{
		{"id": int64(1), "val": "A"},
		{"id": int64(2), "val": "B"},
		{"id": int64(2), "val": "B"},
		{"id": int64(3), "val": "C"},
	}
	for i, exp := range expected {
		if !reflect.DeepEqual(rows[i], exp) {
			t.Errorf("row %d mismatch: got %v, want %v", i, rows[i], exp)
		}
	}
}

func TestSetOp_UnionDistinct(t *testing.T) {
	schema := []string{"id", "val"}

	left := newMockIterator(schema,
		map[string]any{"id": int64(1), "val": "A"},
		map[string]any{"id": int64(2), "val": "B"},
		map[string]any{"id": int64(2), "val": "B"}, // internal duplicate
	)
	right := newMockIterator(schema,
		map[string]any{"id": int64(2), "val": "B"}, // cross-child duplicate
		map[string]any{"id": int64(3), "val": "C"},
	)

	it := execution.NewSetOpIterator(left, right, execution.SetOpUnion)
	rows := collectTuples(t, it)

	if len(rows) != 3 {
		t.Fatalf("expected 3 distinct rows for UNION, got %d (%v)", len(rows), rows)
	}

	expected := []map[string]any{
		{"id": int64(1), "val": "A"},
		{"id": int64(2), "val": "B"},
		{"id": int64(3), "val": "C"},
	}
	for i, exp := range expected {
		if !reflect.DeepEqual(rows[i], exp) {
			t.Errorf("row %d mismatch: got %v, want %v", i, rows[i], exp)
		}
	}
}

func TestSetOp_Intersect(t *testing.T) {
	schema := []string{"id", "val"}

	left := newMockIterator(schema,
		map[string]any{"id": int64(1), "val": "A"},
		map[string]any{"id": int64(2), "val": "B"},
		map[string]any{"id": int64(2), "val": "B"}, // duplicate in left
		map[string]any{"id": int64(4), "val": "D"},
	)
	right := newMockIterator(schema,
		map[string]any{"id": int64(2), "val": "B"},
		map[string]any{"id": int64(3), "val": "C"},
		map[string]any{"id": int64(4), "val": "D"},
	)

	it := execution.NewSetOpIterator(left, right, execution.SetOpIntersect)
	rows := collectTuples(t, it)

	if len(rows) != 2 {
		t.Fatalf("expected 2 intersecting rows, got %d (%v)", len(rows), rows)
	}

	expected := []map[string]any{
		{"id": int64(2), "val": "B"},
		{"id": int64(4), "val": "D"},
	}
	for i, exp := range expected {
		if !reflect.DeepEqual(rows[i], exp) {
			t.Errorf("row %d mismatch: got %v, want %v", i, rows[i], exp)
		}
	}
}

func TestSetOp_Except(t *testing.T) {
	schema := []string{"id", "val"}

	left := newMockIterator(schema,
		map[string]any{"id": int64(1), "val": "A"},
		map[string]any{"id": int64(2), "val": "B"},
		map[string]any{"id": int64(3), "val": "C"},
		map[string]any{"id": int64(3), "val": "C"}, // duplicate in left
	)
	right := newMockIterator(schema,
		map[string]any{"id": int64(2), "val": "B"},
		map[string]any{"id": int64(4), "val": "D"},
	)

	it := execution.NewSetOpIterator(left, right, execution.SetOpExcept)
	rows := collectTuples(t, it)

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows for EXCEPT (1 and 3), got %d (%v)", len(rows), rows)
	}

	expected := []map[string]any{
		{"id": int64(1), "val": "A"},
		{"id": int64(3), "val": "C"},
	}
	for i, exp := range expected {
		if !reflect.DeepEqual(rows[i], exp) {
			t.Errorf("row %d mismatch: got %v, want %v", i, rows[i], exp)
		}
	}
}

func TestSetOp_SchemaMismatchError(t *testing.T) {
	left := newMockIterator([]string{"id", "name"},
		map[string]any{"id": int64(1), "name": "A"},
	)
	right := newMockIterator([]string{"id", "name", "extra"},
		map[string]any{"id": int64(2), "name": "B", "extra": 100},
	)

	it := execution.NewSetOpIterator(left, right, execution.SetOpUnion)
	err := it.Open(context.Background())
	if err == nil {
		t.Fatalf("expected schema mismatch error, got nil")
	}
}

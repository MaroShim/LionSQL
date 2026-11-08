package execution_test

import (
	"context"
	"io"
	"reflect"
	"testing"

	"lionsql/pkg/execution"
	"lionsql/pkg/storage"
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

func TestSetOp_Regression_P0_33_LazyTupleMaterializationInUnionAndExcept(t *testing.T) {
	schema := &storage.TableSchema{
		Name: "test_setop_lazy",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt},
			{Name: "val", Type: storage.TypeVarChar},
		},
	}

	makeLazyTuple := func(id int64, val string) execution.Tuple {
		row := storage.Row{
			Values: []storage.Value{
				storage.IntValue(id),
				storage.StringValue(val),
			},
		}
		data, err := storage.EncodeRow(schema, row)
		if err != nil {
			t.Fatalf("EncodeRow failed: %v", err)
		}
		lazy, err := storage.NewLazyTuple(schema, data)
		if err != nil {
			t.Fatalf("NewLazyTuple failed: %v", err)
		}

		colMap := execution.NewColumnIndexMapFromCols([]string{"id", "val"})
		return execution.Tuple{
			Values: make([]any, 2), // Un-materialized: all values are nil!
			Index:  colMap,
			Lazy:   lazy,
		}
	}

	// 1. UNION Distinct with 3 distinct LazyTuples across left and right
	left := newLazyMockIterator([]string{"id", "val"},
		makeLazyTuple(1, "A"),
		makeLazyTuple(2, "B"),
	)
	right := newLazyMockIterator([]string{"id", "val"},
		makeLazyTuple(2, "B"), // duplicate
		makeLazyTuple(3, "C"),
	)

	unionIt := execution.NewSetOpIterator(left, right, execution.SetOpUnion)
	unionRows := collectTuples(t, unionIt)

	// If LazyTuple is not materialized during key encoding, all rows share the same nil key "N:\x00N:"
	// and UNION collapses them into a single row (data loss)!
	if len(unionRows) != 3 {
		t.Fatalf("DATA LOSS DETECTED (P0-33): expected 3 distinct rows from LazyTuple UNION, got %d: %+v", len(unionRows), unionRows)
	}

	// 2. EXCEPT with LazyTuples: (1, A) and (2, B) EXCEPT (2, B) and (3, C) -> should return exactly (1, A)
	leftExcept := newLazyMockIterator([]string{"id", "val"},
		makeLazyTuple(1, "A"),
		makeLazyTuple(2, "B"),
	)
	rightExcept := newLazyMockIterator([]string{"id", "val"},
		makeLazyTuple(2, "B"),
		makeLazyTuple(3, "C"),
	)

	exceptIt := execution.NewSetOpIterator(leftExcept, rightExcept, execution.SetOpExcept)
	exceptRows := collectTuples(t, exceptIt)

	if len(exceptRows) != 1 {
		t.Fatalf("EXCEPT DATA CORRUPTION (P0-33): expected exactly 1 row (1, A), got %d: %+v", len(exceptRows), exceptRows)
	}
	if exceptRows[0]["id"] != int64(1) || exceptRows[0]["val"] != "A" {
		t.Errorf("expected (1, A), got %+v", exceptRows[0])
	}
}

type lazyMockIterator struct {
	schema      []string
	colIndexMap *execution.ColumnIndexMap
	tuples      []execution.Tuple
	idx         int
}

func newLazyMockIterator(cols []string, tuples ...execution.Tuple) *lazyMockIterator {
	return &lazyMockIterator{
		schema:      cols,
		colIndexMap: execution.NewColumnIndexMapFromCols(cols),
		tuples:      tuples,
	}
}

func (m *lazyMockIterator) Open(ctx context.Context) error {
	m.idx = 0
	return nil
}

func (m *lazyMockIterator) Next(ctx context.Context) (execution.Tuple, error) {
	if m.idx >= len(m.tuples) {
		return execution.Tuple{}, io.EOF
	}
	t := m.tuples[m.idx]
	m.idx++
	return t, nil
}

func (m *lazyMockIterator) Close() error {
	return nil
}

func (m *lazyMockIterator) Schema() []string {
	return m.schema
}

func (m *lazyMockIterator) ColIndexMap() *execution.ColumnIndexMap {
	return m.colIndexMap
}


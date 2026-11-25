package execution_test

import (
	"context"
	"io"
	"testing"

	"lionsql/pkg/execution"
)

type mockAbortableIterator struct {
	tuples       []execution.Tuple
	idx          int
	aborted      bool
	schema       []string
	colIndexMap  *execution.ColumnIndexMap
}

func (m *mockAbortableIterator) Open(ctx context.Context) error {
	m.idx = 0
	m.aborted = false
	return nil
}

func (m *mockAbortableIterator) Next(ctx context.Context) (execution.Tuple, error) {
	if m.aborted || m.idx >= len(m.tuples) {
		return execution.Tuple{}, io.EOF
	}
	t := m.tuples[m.idx]
	m.idx++
	return t, nil
}

func (m *mockAbortableIterator) Close() error {
	return nil
}

func (m *mockAbortableIterator) Schema() []string {
	return m.schema
}

func (m *mockAbortableIterator) ColIndexMap() *execution.ColumnIndexMap {
	return m.colIndexMap
}

func (m *mockAbortableIterator) EarlyAbort() {
	m.aborted = true
}

func TestLimitIterator_EarlyAbort_DirectChild(t *testing.T) {
	schema := []string{"id", "val"}
	colMap := execution.NewColumnIndexMapFromCols(schema)

	tuples := []execution.Tuple{
		execution.NewTupleFromMap(map[string]any{"id": int64(1), "val": "A"}, schema),
		execution.NewTupleFromMap(map[string]any{"id": int64(2), "val": "B"}, schema),
		execution.NewTupleFromMap(map[string]any{"id": int64(3), "val": "C"}, schema),
		execution.NewTupleFromMap(map[string]any{"id": int64(4), "val": "D"}, schema),
	}

	mock := &mockAbortableIterator{
		tuples:      tuples,
		schema:      schema,
		colIndexMap: colMap,
	}

	limitIt := execution.NewLimitIterator(mock, 2, 0)
	ctx := context.Background()
	if err := limitIt.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Read 2 tuples
	t1, err := limitIt.Next(ctx)
	if err != nil {
		t.Fatalf("read 1 failed: %v", err)
	}
	if t1.Get("id") != int64(1) {
		t.Errorf("expected id 1, got %v", t1.Get("id"))
	}

	t2, err := limitIt.Next(ctx)
	if err != nil {
		t.Fatalf("read 2 failed: %v", err)
	}
	if t2.Get("id") != int64(2) {
		t.Errorf("expected id 2, got %v", t2.Get("id"))
	}

	// Next should return io.EOF and trigger EarlyAbort on mock child
	_, err = limitIt.Next(ctx)
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}

	if !mock.aborted {
		t.Errorf("expected child EarlyAbort() to be invoked, but aborted is false")
	}
}

func TestLimitIterator_EarlyAbort_PropagationThroughProjectionAndFilter(t *testing.T) {
	schema := []string{"id", "val"}
	colMap := execution.NewColumnIndexMapFromCols(schema)

	tuples := []execution.Tuple{
		execution.NewTupleFromMap(map[string]any{"id": int64(1), "val": 10}, schema),
		execution.NewTupleFromMap(map[string]any{"id": int64(2), "val": 20}, schema),
		execution.NewTupleFromMap(map[string]any{"id": int64(3), "val": 30}, schema),
	}

	mock := &mockAbortableIterator{
		tuples:      tuples,
		schema:      schema,
		colIndexMap: colMap,
	}

	// Chain: Limit -> Project -> Filter -> Mock
	filterIt := execution.NewFilterIterator(mock, "id > 0")
	projIt := execution.NewProjectionIterator(filterIt, []string{"id"})
	limitIt := execution.NewLimitIterator(projIt, 1, 0)

	ctx := context.Background()
	if err := limitIt.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	t1, err := limitIt.Next(ctx)
	if err != nil {
		t.Fatalf("read 1 failed: %v", err)
	}
	if t1.Get("id") != int64(1) {
		t.Errorf("expected id 1, got %v", t1.Get("id"))
	}

	_, err = limitIt.Next(ctx)
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}

	if !mock.aborted {
		t.Errorf("expected EarlyAbort to propagate through Projection and Filter to leaf mock, but aborted is false")
	}
}

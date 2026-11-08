package execution

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// mockFaultyIterator delivers a valid first chunk, then fails with a simulated I/O error on the second pull.
type mockFaultyIterator struct {
	schema []string
	calls  int
	closed bool
}

func (m *mockFaultyIterator) Open(ctx context.Context) error {
	m.calls = 0
	m.closed = false
	return nil
}

func (m *mockFaultyIterator) NextChunk(ctx context.Context, dest *Chunk) error {
	m.calls++
	if m.calls == 1 {
		// Deliver valid 3 rows in first chunk
		colMap := NewColumnIndexMapFromCols(m.schema)
		dest.Tuples = []Tuple{
			{Values: []any{int64(1), float64(100.0), "A"}, Index: colMap},
			{Values: []any{int64(2), float64(200.0), "A"}, Index: colMap},
			{Values: []any{int64(3), float64(300.0), "B"}, Index: colMap},
		}
		return nil
	}
	// On second chunk, simulate mid-stream disk/storage I/O error
	return errors.New("simulated storage read I/O error")
}

func (m *mockFaultyIterator) Next(ctx context.Context) (Tuple, error) {
	// Fallback implementation for Iterator interface
	chunk := GetChunk()
	defer PutChunk(chunk)
	err := m.NextChunk(ctx, chunk)
	if err != nil {
		return Tuple{}, err
	}
	if len(chunk.Tuples) > 0 {
		return chunk.Tuples[0], nil
	}
	return Tuple{}, io.EOF
}

func (m *mockFaultyIterator) Close() error {
	m.closed = true
	return nil
}

func (m *mockFaultyIterator) Schema() []string {
	return m.schema
}

func (m *mockFaultyIterator) ColIndexMap() *ColumnIndexMap {
	return NewColumnIndexMapFromCols(m.schema)
}

// TestAggregate_Regression_P0_14_IOErrorFailClosed verifies that when PullChunk encounters
// a mid-stream I/O error from a child iterator:
// 1. Open() immediately propagates the exact error (Fail-Closed).
// 2. Child iterator is safely closed immediately to prevent handle leaks ([RULE:HandleHygiene]).
// 3. No incomplete or distorted partial aggregation results are ever exposed to callers ([RULE:ExplicitError]).
func TestAggregate_Regression_P0_14_IOErrorFailClosed(t *testing.T) {
	schema := []string{"id", "val", "cat"}

	t.Run("GlobalAggregation_FailClosed_And_HandleCleanup", func(t *testing.T) {
		ctx := context.Background()
		child := &mockFaultyIterator{schema: schema}
		agg := NewAggregateIterator(
			child,
			nil, // No GROUP BY: Global aggregate (COUNT(*), SUM(val))
			[]string{"COUNT(*) AS total_count", "SUM(val) AS total_sum"},
			"",
		)

		err := agg.Open(ctx)
		if err == nil {
			t.Fatalf("[P0-14 DEFECT] Expected storage I/O error, but agg.Open() returned nil!")
		}
		if !strings.Contains(err.Error(), "simulated storage read I/O error") {
			t.Fatalf("[P0-14 DEFECT] Expected simulated storage error propagated, got: %v", err)
		}
		if !child.closed {
			t.Fatalf("[P0-14 DEFECT] Child iterator was NOT closed on I/O error; resource leak detected! ([RULE:HandleHygiene])")
		}

		// Ensure no distorted partial aggregates are returned via NextChunk or Next
		chunk := GetChunk()
		defer PutChunk(chunk)
		nextErr := agg.NextChunk(ctx, chunk)
		if nextErr == nil && chunk.Len() > 0 {
			t.Fatalf("[P0-14 DEFECT] Distorted partial aggregate returned after Open() failure: %+v", chunk.Tuples)
		}

		t.Logf("[P0-14 PASS] Global aggregation failed closed cleanly with child closed: %v", err)
	})

	t.Run("GroupByAggregation_FailClosed_And_HandleCleanup", func(t *testing.T) {
		ctx := context.Background()
		child := &mockFaultyIterator{schema: schema}
		agg := NewAggregateIterator(
			child,
			[]string{"cat"}, // GROUP BY cat
			[]string{"cat", "COUNT(*) AS grp_count"},
			"",
		)

		err := agg.Open(ctx)
		if err == nil {
			t.Fatalf("[P0-14 DEFECT] Expected storage I/O error, but agg.Open() returned nil!")
		}
		if !strings.Contains(err.Error(), "simulated storage read I/O error") {
			t.Fatalf("[P0-14 DEFECT] Expected simulated storage error propagated, got: %v", err)
		}
		if !child.closed {
			t.Fatalf("[P0-14 DEFECT] Child iterator was NOT closed on I/O error; resource leak detected! ([RULE:HandleHygiene])")
		}

		// Ensure no partial grouped rows are returned
		chunk := GetChunk()
		defer PutChunk(chunk)
		nextErr := agg.NextChunk(ctx, chunk)
		if nextErr == nil && chunk.Len() > 0 {
			t.Fatalf("[P0-14 DEFECT] Distorted partial grouped tuples returned after Open() failure: %+v", chunk.Tuples)
		}

		t.Logf("[P0-14 PASS] GROUP BY aggregation failed closed cleanly with child closed: %v", err)
	})

	t.Run("ContextCancellation_CleanTeardown", func(t *testing.T) {
		child := &mockFaultyIterator{schema: schema}
		agg := NewAggregateIterator(
			child,
			nil,
			[]string{"COUNT(*) AS total_count"},
			"",
		)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		err := agg.Open(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("[P0-14 DEFECT] Expected context.Canceled, got: %v", err)
		}
		if !child.closed {
			t.Fatalf("[P0-14 DEFECT] Child iterator was NOT closed on context cancellation! ([RULE:CleanTeardown])")
		}

		t.Logf("[P0-14 PASS] Context cancellation handled cleanly with child closed: %v", err)
	})
}

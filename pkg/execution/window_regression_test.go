package execution

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"lionsql/pkg/planner"
)

// mockStreamIterator produces n tuples of integer values.
type mockStreamIterator struct {
	total  int
	cursor int
	schema []string
	colMap *ColumnIndexMap
}

func newMockStreamIterator(total int) *mockStreamIterator {
	schema := []string{"id", "val"}
	return &mockStreamIterator{
		total:  total,
		cursor: 0,
		schema: schema,
		colMap: NewColumnIndexMapFromCols(schema),
	}
}

func (m *mockStreamIterator) Open(ctx context.Context) error {
	m.cursor = 0
	return nil
}

func (m *mockStreamIterator) Next(ctx context.Context) (Tuple, error) {
	if m.cursor >= m.total {
		return Tuple{}, io.EOF
	}
	t := Tuple{
		Values: []any{int64(m.cursor), fmt.Sprintf("value_%06d", m.cursor)},
		Index:  m.colMap,
	}
	m.cursor++
	return t, nil
}

func (m *mockStreamIterator) Close() error {
	return nil
}

func (m *mockStreamIterator) Schema() []string {
	return m.schema
}

func (m *mockStreamIterator) ColIndexMap() *ColumnIndexMap {
	return m.colMap
}

// TestWindow_Regression_P0_12_WorkMemGuard verifies that WindowIterator
// bounds memory consumption via workMem and returns ErrWindowWorkMemExceeded
// when the input stream exceeds the configured memory budget, preventing OOM DoS crashes.
func TestWindow_Regression_P0_12_WorkMemGuard(t *testing.T) {
	ctx := context.Background()

	winDefs := []planner.WindowDef{
		{
			Function: "ROW_NUMBER",
			OrderByKeys: []planner.SortKeyDef{
				{Column: "id", Direction: "ASC"},
			},
			Alias: "row_num",
		},
	}

	// Case 1: Stream of 500 rows with very low workMem (1KB budget)
	child1 := newMockStreamIterator(500)
	winIter1 := NewWindowIterator(child1, winDefs)
	winIter1.SetWorkMem(1024) // 1KB limit

	err := winIter1.Open(ctx)
	if err == nil {
		t.Fatalf("[P0-12 DEFECT] Expected ErrWindowWorkMemExceeded with 1KB budget, but Open() succeeded!")
	}
	if !errors.Is(err, ErrWindowWorkMemExceeded) {
		t.Fatalf("[P0-12 DEFECT] Expected ErrWindowWorkMemExceeded, got: %v", err)
	}
	t.Logf("[P0-12 PASS] Budget exceeded caught cleanly: %v", err)

	// Case 2: Same stream with DefaultWorkMem (64MB budget)
	child2 := newMockStreamIterator(500)
	winIter2 := NewWindowIterator(child2, winDefs)
	// DefaultWorkMem is 64MB

	if err := winIter2.Open(ctx); err != nil {
		t.Fatalf("unexpected error with DefaultWorkMem: %v", err)
	}
	defer winIter2.Close()

	// Verify all 500 rows are emitted with computed window value
	count := 0
	for {
		tuple, err := winIter2.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next() error: %v", err)
		}
		count++
		val := tuple.Get("row_num")
		if val != int64(count) {
			t.Errorf("row %d: expected row_num %d, got %v", count, count, val)
		}
	}
	if count != 500 {
		t.Fatalf("expected 500 rows, got %d", count)
	}
	t.Logf("[P0-12 PASS] Successfully evaluated 500 window rows within DefaultWorkMem budget.")
}

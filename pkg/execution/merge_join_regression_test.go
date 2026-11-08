package execution

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
)

// mockDuplicateKeyIterator yields total tuples all having the exact same join key.
type mockDuplicateKeyIterator struct {
	total  int
	cursor int
	keyVal int64
	schema []string
	colMap *ColumnIndexMap
}

func newMockDuplicateKeyIterator(total int, keyVal int64, prefix string) *mockDuplicateKeyIterator {
	schema := []string{prefix + "_id", prefix + "_val"}
	return &mockDuplicateKeyIterator{
		total:  total,
		cursor: 0,
		keyVal: keyVal,
		schema: schema,
		colMap: NewColumnIndexMapFromCols(schema),
	}
}

func (m *mockDuplicateKeyIterator) Open(ctx context.Context) error {
	m.cursor = 0
	return nil
}

func (m *mockDuplicateKeyIterator) Next(ctx context.Context) (Tuple, error) {
	if m.cursor >= m.total {
		return Tuple{}, io.EOF
	}
	t := Tuple{
		Values: []any{m.keyVal, fmt.Sprintf("payload_%06d", m.cursor)},
		Index:  m.colMap,
	}
	m.cursor++
	return t, nil
}

func (m *mockDuplicateKeyIterator) Close() error {
	return nil
}

func (m *mockDuplicateKeyIterator) Schema() []string {
	return m.schema
}

func (m *mockDuplicateKeyIterator) ColIndexMap() *ColumnIndexMap {
	return m.colMap
}

// TestMergeJoin_Regression_P0_13_WorkMemGuard verifies that MergeJoinIterator
// bounds memory consumption via workMem when buffering contiguous duplicate right keys,
// returning ErrMergeJoinWorkMemExceeded instead of triggering an OOM crash.
func TestMergeJoin_Regression_P0_13_WorkMemGuard(t *testing.T) {
	ctx := context.Background()

	// Case 1: 1 left row, 500 right rows with identical join key (key = 100), with 1KB workMem
	left1 := newMockDuplicateKeyIterator(1, 100, "l")
	right1 := newMockDuplicateKeyIterator(500, 100, "r")
	mj1 := NewMergeJoinIterator(left1, right1, "INNER", "l_id", "r_id")
	mj1.SetWorkMem(1024) // 1KB limit

	if err := mj1.Open(ctx); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer mj1.Close()

	_, err := mj1.Next(ctx)
	if err == nil {
		t.Fatalf("[P0-13 DEFECT] Expected ErrMergeJoinWorkMemExceeded with 1KB budget, but Next() succeeded!")
	}
	if !errors.Is(err, ErrMergeJoinWorkMemExceeded) {
		t.Fatalf("[P0-13 DEFECT] Expected ErrMergeJoinWorkMemExceeded, got: %v", err)
	}
	t.Logf("[P0-13 PASS] Budget exceeded caught cleanly: %v", err)

	// Case 2: Same inputs with DefaultWorkMem (64MB budget)
	left2 := newMockDuplicateKeyIterator(1, 100, "l")
	right2 := newMockDuplicateKeyIterator(500, 100, "r")
	mj2 := NewMergeJoinIterator(left2, right2, "INNER", "l_id", "r_id")
	// DefaultWorkMem is 64MB

	if err := mj2.Open(ctx); err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer mj2.Close()

	count := 0
	for {
		tuple, err := mj2.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next() error: %v", err)
		}
		count++
		if tuple.Get("l_id") != int64(100) || tuple.Get("r_id") != int64(100) {
			t.Fatalf("row %d: join key mismatch", count)
		}
	}
	if count != 500 {
		t.Fatalf("expected 500 joined rows, got %d", count)
	}
	t.Logf("[P0-13 PASS] Successfully evaluated 500 duplicate key merge join rows within DefaultWorkMem budget.")
}

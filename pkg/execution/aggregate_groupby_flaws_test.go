package execution

import (
	"context"
	"fmt"
	"io"
	"testing"

	"lionsql/pkg/storage"
)

// TestAggregate_Regression_GroupBy32Columns verifies that GROUP BY with >= 32 columns
// does not silently truncate or ignore columns from index 31 onwards.
// Specifically: 35 columns where rows differ only in the 35th column (c34)
// MUST produce 2 distinct groups, not 1 corrupt merged group.
func TestAggregate_Regression_GroupBy32Columns(t *testing.T) {
	ctx := context.Background()

	const numCols = 35
	cols := make([]string, numCols)
	for i := 0; i < numCols; i++ {
		cols[i] = fmt.Sprintf("c%d", i)
	}
	colMap := NewColumnIndexMapFromCols(cols)

	// Row 1: c0..c33 = 1, c34 = 100
	// Row 2: c0..c33 = 1, c34 = 200
	vals1 := make([]any, numCols)
	vals2 := make([]any, numCols)
	for i := 0; i < numCols; i++ {
		vals1[i] = int64(1)
		vals2[i] = int64(1)
	}
	vals1[34] = int64(100)
	vals2[34] = int64(200)

	tuples := []Tuple{
		{Values: vals1, Index: colMap},
		{Values: vals2, Index: colMap},
	}

	child := &sliceIterator{
		cols:   cols,
		colMap: colMap,
		tuples: tuples,
	}

	agg := NewAggregateIterator(
		child,
		cols, // All 35 columns in GROUP BY
		[]string{"COUNT(*) AS cnt"},
		"",
	)

	if err := agg.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer agg.Close()

	var resultTuples []Tuple
	for {
		tup, err := agg.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next failed: %v", err)
		}
		resultTuples = append(resultTuples, tup)
	}

	// Must produce 2 distinct groups because c34 is different (100 vs 200)
	if len(resultTuples) != 2 {
		t.Fatalf("[DEFECT: Silently Truncated GROUP BY Keys] Expected 2 distinct groups for 35 columns differing at c34, but got %d groups", len(resultTuples))
	}
}

// TestAggregate_Regression_GroupByMaxColumnsGuard verifies that specifying more than
// MaxGroupByColumns (64) in GROUP BY returns ErrTooManyGroupByColumns instead of silently truncating.
func TestAggregate_Regression_GroupByMaxColumnsGuard(t *testing.T) {
	ctx := context.Background()

	cols65 := make([]string, 65)
	for i := 0; i < 65; i++ {
		cols65[i] = fmt.Sprintf("c%d", i)
	}
	child := NewEmptyIterator(cols65, nil)
	agg := NewAggregateIterator(
		child,
		cols65,
		[]string{"COUNT(*) AS cnt"},
		"",
	)

	err := agg.Open(ctx)
	if err == nil {
		t.Fatalf("[DEFECT: Unbounded GROUP BY] Expected ErrTooManyGroupByColumns for 65 columns, but Open() succeeded")
	}
	t.Logf("Received expected error: %v", err)
}

// TestAggregate_Regression_GroupKeyNullByteCollision verifies that strings or byte values
// containing \x00 do NOT collide across different grouping keys.
// Example: ('A\x00B', 'C') vs ('A', 'B\x00C')
// Under raw delimiter '\x00', both serialize to 'A\x00B\x00C\x00', causing silent false collision.
func TestAggregate_Regression_GroupKeyNullByteCollision(t *testing.T) {
	ctx := context.Background()

	cols := []string{"col1", "col2"}
	colMap := NewColumnIndexMapFromCols(cols)

	// Row 1: col1 = "A\x00B", col2 = "C"
	// Row 2: col1 = "A", col2 = "B\x00C"
	tuples := []Tuple{
		{Values: []any{"A\x00B", "C"}, Index: colMap},
		{Values: []any{"A", "B\x00C"}, Index: colMap},
	}

	child := &sliceIterator{
		cols:   cols,
		colMap: colMap,
		tuples: tuples,
	}

	agg := NewAggregateIterator(
		child,
		cols,
		[]string{"COUNT(*) AS cnt"},
		"",
	)

	if err := agg.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer agg.Close()

	var resultTuples []Tuple
	for {
		tup, err := agg.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next failed: %v", err)
		}
		resultTuples = append(resultTuples, tup)
	}

	// Must produce 2 distinct groups. Under '\x00' collision, len is 1.
	if len(resultTuples) != 2 {
		t.Fatalf("[DEFECT: NUL Byte Group Key Collision] Expected 2 distinct groups for ('A\\x00B', 'C') and ('A', 'B\\x00C'), but got %d (falsely merged into 1 group)", len(resultTuples))
	}
}

// TestAggregate_Regression_GroupKeyNullLiteralVsNullValue verifies that a string literal
// "\x00NULL" does NOT collide with a real NULL value in GROUP BY.
func TestAggregate_Regression_GroupKeyNullLiteralVsNullValue(t *testing.T) {
	ctx := context.Background()

	cols := []string{"val"}
	colMap := NewColumnIndexMapFromCols(cols)

	tuples := []Tuple{
		{Values: []any{nil}, Index: colMap},
		{Values: []any{"\x00NULL"}, Index: colMap},
		{Values: []any{storage.NullValue(storage.TypeVarChar)}, Index: colMap},
	}

	child := &sliceIterator{
		cols:   cols,
		colMap: colMap,
		tuples: tuples,
	}

	agg := NewAggregateIterator(
		child,
		cols,
		[]string{"COUNT(*) AS cnt"},
		"",
	)

	if err := agg.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer agg.Close()

	var resultTuples []Tuple
	for {
		tup, err := agg.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next failed: %v", err)
		}
		resultTuples = append(resultTuples, tup)
	}

	// nil and storage.NullValue both represent NULL -> 1 group
	// "\x00NULL" is a non-null string -> 1 group
	// Total must be exactly 2 distinct groups!
	if len(resultTuples) != 2 {
		t.Fatalf("[DEFECT: Literal vs NULL Collision] Expected 2 distinct groups for NULL and literal '\\x00NULL', but got %d", len(resultTuples))
	}
}

type sliceIterator struct {
	cols   []string
	colMap *ColumnIndexMap
	tuples []Tuple
	idx    int
}

func (s *sliceIterator) Open(ctx context.Context) error {
	s.idx = 0
	return nil
}

func (s *sliceIterator) Next(ctx context.Context) (Tuple, error) {
	if s.idx >= len(s.tuples) {
		return Tuple{}, io.EOF
	}
	tup := s.tuples[s.idx]
	s.idx++
	return tup, nil
}

func (s *sliceIterator) Close() error {
	return nil
}

func (s *sliceIterator) Schema() []string {
	return s.cols
}

func (s *sliceIterator) ColIndexMap() *ColumnIndexMap {
	return s.colMap
}

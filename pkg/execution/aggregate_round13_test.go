package execution

import (
	"context"
	"io"
	"testing"

	"lionsql/pkg/storage"
)

type lazyMockIter struct {
	schema      []string
	colIndexMap *ColumnIndexMap
	tuples      []Tuple
	idx         int
}

func (m *lazyMockIter) Schema() []string               { return m.schema }
func (m *lazyMockIter) ColIndexMap() *ColumnIndexMap   { return m.colIndexMap }
func (m *lazyMockIter) Open(ctx context.Context) error {
	m.idx = 0
	return nil
}
func (m *lazyMockIter) Next(ctx context.Context) (Tuple, error) {
	if m.idx >= len(m.tuples) {
		return Tuple{}, io.EOF
	}
	t := m.tuples[m.idx]
	m.idx++
	return t, nil
}
func (m *lazyMockIter) Close() error { return nil }

// TestAggregate_Regression_Round13_Defect1_LazyTuple_GroupCollapse verifies that
// AggregateIterator properly deserializes LazyTuples when extracting group keys,
// preventing distinct groups from collapsing into a single NULL group.
func TestAggregate_Regression_Round13_Defect1_LazyTuple_GroupCollapse(t *testing.T) {
	schema := &storage.TableSchema{
		Name: "g_t",
		Columns: []storage.Column{
			{Name: "cat", Type: storage.TypeInt},
			{Name: "val", Type: storage.TypeInt},
		},
	}

	makeLazyTuple := func(cat int64, val int64) Tuple {
		row := storage.Row{
			Values: []storage.Value{
				storage.IntValue(cat),
				storage.IntValue(val),
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

		colMap := NewColumnIndexMapFromCols([]string{"cat", "val"})
		return Tuple{
			Values: make([]any, 2), // Un-materialized: Values are nil!
			Index:  colMap,
			Lazy:   lazy,
		}
	}

	// 3 rows: cat=1 val=10, cat=1 val=20, cat=2 val=30
	child := &lazyMockIter{
		schema:      []string{"cat", "val"},
		colIndexMap: NewColumnIndexMapFromCols([]string{"cat", "val"}),
		tuples: []Tuple{
			makeLazyTuple(1, 10),
			makeLazyTuple(1, 20),
			makeLazyTuple(2, 30),
		},
	}

	agg := NewAggregateIterator(child, []string{"cat"}, []string{"cat", "SUM(val)"}, "")
	ctx := context.Background()
	if err := agg.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer agg.Close()

	chunk := GetChunk()
	defer PutChunk(chunk)

	var rows []Tuple
	for {
		chunk.Reset()
		err := PullChunk(ctx, agg, chunk, 1024)
		for _, tup := range chunk.Tuples {
			rows = append(rows, tup)
		}
		if err != nil {
			break
		}
	}

	if len(rows) != 2 {
		t.Fatalf("DEFECT 1 RED: expected 2 groups (cat=1, cat=2), but got %d rows (group collapse): %+v", len(rows), rows)
	}
}

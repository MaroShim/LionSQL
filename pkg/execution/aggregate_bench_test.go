package execution_test

import (
	"context"
	"fmt"
	"io"
	"testing"

	"lionsql/pkg/execution"
)

type benchTupleIterator struct {
	schema    []string
	colIdxMap *execution.ColumnIndexMap
	tuples    []execution.Tuple
	idx       int
}

func newBenchTupleIterator(schema []string, nRows int, numGroups int) *benchTupleIterator {
	colMap := execution.NewColumnIndexMapFromCols(schema)
	tuples := make([]execution.Tuple, nRows)
	for i := 0; i < nRows; i++ {
		deptID := int64(i % numGroups)
		salary := int64(1000 + (i%500)*10)
		category := fmt.Sprintf("CAT_%d", deptID%10)
		tuples[i] = execution.Tuple{
			Values: []any{deptID, category, salary},
			Index:  colMap,
		}
	}
	return &benchTupleIterator{
		schema:    schema,
		colIdxMap: colMap,
		tuples:    tuples,
	}
}

func (b *benchTupleIterator) Reset() {
	b.idx = 0
}

func (b *benchTupleIterator) Open(ctx context.Context) error {
	b.idx = 0
	return nil
}

func (b *benchTupleIterator) Next(ctx context.Context) (execution.Tuple, error) {
	if b.idx >= len(b.tuples) {
		return execution.Tuple{}, io.EOF
	}
	t := b.tuples[b.idx]
	b.idx++
	return t, nil
}

func (b *benchTupleIterator) Close() error {
	return nil
}

func (b *benchTupleIterator) Schema() []string {
	return b.schema
}

func (b *benchTupleIterator) ColIndexMap() *execution.ColumnIndexMap {
	return b.colIdxMap
}

func BenchmarkAggregate_SingleIntGroupBy(b *testing.B) {
	const nRows = 100000
	const numGroups = 100
	schema := []string{"dept_id", "category", "salary"}
	ctx := context.Background()
	src := newBenchTupleIterator(schema, nRows, numGroups)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		src.Reset()
		agg := execution.NewAggregateIterator(
			src,
			[]string{"dept_id"},
			[]string{"dept_id", "COUNT(*)", "SUM(salary)"},
			"",
		)
		if err := agg.Open(ctx); err != nil {
			b.Fatalf("open failed: %v", err)
		}
		var count int
		for {
			_, err := agg.Next(ctx)
			if err == io.EOF {
				break
			}
			if err != nil {
				b.Fatalf("next failed: %v", err)
			}
			count++
		}
		_ = agg.Close()
		if count != numGroups {
			b.Fatalf("expected %d groups, got %d", numGroups, count)
		}
	}
}

func BenchmarkAggregate_CompositeGroupBy(b *testing.B) {
	const nRows = 100000
	const numGroups = 100
	schema := []string{"dept_id", "category", "salary"}
	ctx := context.Background()
	src := newBenchTupleIterator(schema, nRows, numGroups)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		src.Reset()
		agg := execution.NewAggregateIterator(
			src,
			[]string{"dept_id", "category"},
			[]string{"dept_id", "category", "COUNT(*)", "SUM(salary)"},
			"",
		)
		if err := agg.Open(ctx); err != nil {
			b.Fatalf("open failed: %v", err)
		}
		var count int
		for {
			_, err := agg.Next(ctx)
			if err == io.EOF {
				break
			}
			if err != nil {
				b.Fatalf("next failed: %v", err)
			}
			count++
		}
		_ = agg.Close()
		if count != numGroups {
			b.Fatalf("expected %d groups, got %d", numGroups, count)
		}
	}
}

func BenchmarkHashJoin_CompositeKeys(b *testing.B) {
	const leftRows = 20000
	const rightRows = 1000
	schemaL := []string{"dept_id", "category", "salary"}
	schemaR := []string{"dept_id", "category", "dept_name"}

	colMapR := execution.NewColumnIndexMapFromCols(schemaR)
	rightTuples := make([]execution.Tuple, rightRows)
	for i := 0; i < rightRows; i++ {
		deptID := int64(i % 100)
		category := fmt.Sprintf("CAT_%d", deptID%10)
		rightTuples[i] = execution.Tuple{
			Values: []any{deptID, category, fmt.Sprintf("Dept_%d", deptID)},
			Index:  colMapR,
		}
	}

	ctx := context.Background()
	srcL := newBenchTupleIterator(schemaL, leftRows, 100)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		srcL.Reset()
		srcR := &benchTupleIterator{
			schema:    schemaR,
			colIdxMap: colMapR,
			tuples:    rightTuples,
		}
		hj := execution.NewHashJoinIterator(
			srcL,
			srcR,
			"INNER",
			"dept_id,category",
			"dept_id,category",
		)
		if err := hj.Open(ctx); err != nil {
			b.Fatalf("open failed: %v", err)
		}
		var count int
		for {
			_, err := hj.Next(ctx)
			if err == io.EOF {
				break
			}
			if err != nil {
				b.Fatalf("next failed: %v", err)
			}
			count++
		}
		_ = hj.Close()
		if count == 0 {
			b.Fatalf("expected matches, got 0")
		}
	}
}

func BenchmarkAggregate_PrimitiveIntSum(b *testing.B) {
	nRows := 100000
	schema := []string{"id", "val"}
	colMap := execution.NewColumnIndexMapFromCols(schema)
	tuples := make([]execution.Tuple, nRows)
	for i := 0; i < nRows; i++ {
		tuples[i] = execution.Tuple{
			Values: []any{int64(i % 10), int64(i * 10)},
			Index:  colMap,
		}
	}

	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		child := &benchTupleIterator{
			schema:    schema,
			colIdxMap: colMap,
			tuples:    tuples,
		}
		agg := execution.NewAggregateIterator(
			child,
			[]string{"id"},
			[]string{"id", "SUM(val)", "AVG(val)", "MIN(val)", "MAX(val)"},
			"",
		)
		if err := agg.Open(ctx); err != nil {
			b.Fatalf("open failed: %v", err)
		}
		var count int
		for {
			_, err := agg.Next(ctx)
			if err == io.EOF {
				break
			}
			if err != nil {
				b.Fatalf("next failed: %v", err)
			}
			count++
		}
		_ = agg.Close()
		if count != 10 {
			b.Fatalf("expected 10 groups, got %d", count)
		}
	}
}


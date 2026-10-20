package execution_test

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"lionsql/pkg/execution"
)

type mockTupleIterator struct {
	tuples []execution.Tuple
	schema []string
	idx    int
}

func (m *mockTupleIterator) Open(ctx context.Context) error {
	m.idx = 0
	return nil
}

func (m *mockTupleIterator) Next(ctx context.Context) (execution.Tuple, error) {
	if m.idx >= len(m.tuples) {
		return execution.Tuple{}, io.EOF
	}
	t := m.tuples[m.idx]
	m.idx++
	return t, nil
}

func (m *mockTupleIterator) Close() error {
	return nil
}

func (m *mockTupleIterator) Schema() []string {
	if len(m.schema) > 0 {
		return m.schema
	}
	return []string{"id", "val", "category"}
}

func (m *mockTupleIterator) ColIndexMap() *execution.ColumnIndexMap {
	if len(m.tuples) > 0 && m.tuples[0].Index != nil {
		return m.tuples[0].Index
	}
	return execution.NewColumnIndexMapFromCols(m.Schema())
}

func TestSortIterator_InMemoryFastPath(t *testing.T) {
	schema := []string{"id", "val", "category"}
	mock := &mockTupleIterator{
		schema: schema,
		tuples: []execution.Tuple{
			execution.NewTupleFromMap(map[string]any{"id": int64(3), "val": 30.0}, schema),
			execution.NewTupleFromMap(map[string]any{"id": int64(1), "val": 10.0}, schema),
			execution.NewTupleFromMap(map[string]any{"id": int64(2), "val": 20.0}, schema),
		},
	}

	sortIter := execution.NewSortIterator(mock, "id ASC")
	sortIter.SetWorkMem(64 * 1024 * 1024)

	ctx := context.Background()
	if err := sortIter.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer sortIter.Close()

	if sortIter.SortMethod() != "quicksort" {
		t.Errorf("expected quicksort method, got %s", sortIter.SortMethod())
	}
	if sortIter.SpillCount() != 0 {
		t.Errorf("expected 0 spills, got %d", sortIter.SpillCount())
	}

	var results []int64
	for {
		tuple, err := sortIter.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next error: %v", err)
		}
		results = append(results, tuple.Get("id").(int64))
	}

	if len(results) != 3 || results[0] != 1 || results[1] != 2 || results[2] != 3 {
		t.Fatalf("unexpected sort results: %+v", results)
	}
}

func TestSortIterator_ExternalMergeSortSpill(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lionsql-spill-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Generate 1,000 randomized tuples
	r := rand.New(rand.NewSource(42))
	nTuples := 1000
	schema := []string{"id", "val", "category"}
	tuples := make([]execution.Tuple, nTuples)
	for i := 0; i < nTuples; i++ {
		tuples[i] = execution.NewTupleFromMap(map[string]any{
			"id":       int64(i + 1),
			"val":      float64(r.Intn(10000)),
			"category": fmt.Sprintf("cat-%d", r.Intn(5)),
		}, schema)
	}

	mock := &mockTupleIterator{schema: schema, tuples: tuples}

	// Order by val ASC, id ASC with a tight 4KB work_mem to force multiple disk spills
	sortIter := execution.NewSortIterator(mock, "val ASC, id ASC")
	sortIter.SetWorkMem(4 * 1024) // 4KB
	sortIter.SetTempDir(tempDir)

	ctx := context.Background()
	if err := sortIter.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	if sortIter.SortMethod() != "external merge" {
		t.Errorf("expected external merge method, got %s", sortIter.SortMethod())
	}
	if sortIter.SpillCount() < 5 {
		t.Errorf("expected at least 5 spills for 1000 tuples with 4KB workMem, got %d", sortIter.SpillCount())
	}

	var sortedVals []float64
	for {
		tuple, err := sortIter.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next error during external merge: %v", err)
		}
		var val float64
		switch v := tuple.Get("val").(type) {
		case float64:
			val = v
		case int64:
			val = float64(v)
		default:
			var f float64
			fmt.Sscanf(fmt.Sprintf("%v", v), "%f", &f)
			val = f
		}
		sortedVals = append(sortedVals, val)
	}

	if len(sortedVals) != nTuples {
		t.Fatalf("expected %d tuples, got %d", nTuples, len(sortedVals))
	}

	// Verify ascending sorted order
	for i := 1; i < len(sortedVals); i++ {
		if sortedVals[i] < sortedVals[i-1] {
			t.Fatalf("ordering violation at index %d: %f < %f", i, sortedVals[i], sortedVals[i-1])
		}
	}

	// Close and verify that all temp files in tempDir are deleted
	if err := sortIter.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	files, err := filepath.Glob(filepath.Join(tempDir, "lionsql-sort-run-*.tmp"))
	if err != nil {
		t.Fatalf("Glob error: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 leftover temp files after Close, found %d: %+v", len(files), files)
	}
}

func TestSortIterator_DescendingSortSpill(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lionsql-spill-desc-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	nTuples := 500
	descSchema := []string{"id", "val"}
	tuples := make([]execution.Tuple, nTuples)
	for i := 0; i < nTuples; i++ {
		tuples[i] = execution.NewTupleFromMap(map[string]any{
			"id":  int64(i + 1),
			"val": int64((i*37 + 13) % 1000),
		}, descSchema)
	}

	mock := &mockTupleIterator{schema: descSchema, tuples: tuples}
	sortIter := execution.NewSortIterator(mock, "val DESC")
	sortIter.SetWorkMem(2 * 1024) // 2KB tight limit
	sortIter.SetTempDir(tempDir)

	ctx := context.Background()
	if err := sortIter.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer sortIter.Close()

	var sortedVals []int64
	for {
		tuple, err := sortIter.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next error: %v", err)
		}
		var val int64
		switch v := tuple.Get("val").(type) {
		case int64:
			val = v
		default:
			fmt.Sscanf(fmt.Sprintf("%v", v), "%d", &val)
		}
		sortedVals = append(sortedVals, val)
	}

	if len(sortedVals) != nTuples {
		t.Fatalf("expected %d tuples, got %d", nTuples, len(sortedVals))
	}

	for i := 1; i < len(sortedVals); i++ {
		if sortedVals[i] > sortedVals[i-1] {
			t.Fatalf("descending ordering violation at index %d: %d > %d", i, sortedVals[i], sortedVals[i-1])
		}
	}
}

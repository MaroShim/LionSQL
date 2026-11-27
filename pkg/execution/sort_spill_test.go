package execution_test

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"lionsql/pkg/execution"
	"lionsql/pkg/storage"
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

func TestSortIterator_TopNHeapsort(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lionsql-topn-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Generate 1,000 randomized tuples
	nTuples := 1000
	schema := []string{"id", "score"}
	tuples := make([]execution.Tuple, nTuples)
	for i := 0; i < nTuples; i++ {
		tuples[i] = execution.NewTupleFromMap(map[string]any{
			"id":    int64(i + 1),
			"score": int64((i*41 + 17) % 10000),
		}, schema)
	}

	mock := &mockTupleIterator{schema: schema, tuples: tuples}
	sortIter := execution.NewSortIterator(mock, "score ASC, id ASC")
	// Set tight workMem 4KB. Without Top-N heapsort this would spill 5+ times.
	sortIter.SetWorkMem(4 * 1024)
	sortIter.SetTempDir(tempDir)
	// Request top 10 rows
	sortIter.SetLimit(10)

	ctx := context.Background()
	if err := sortIter.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer sortIter.Close()

	if sortIter.SortMethod() != "top-N heapsort" {
		t.Fatalf("expected 'top-N heapsort', got %s", sortIter.SortMethod())
	}
	if sortIter.SpillCount() != 0 {
		t.Fatalf("expected 0 spills with top-N heapsort, got %d", sortIter.SpillCount())
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
		results = append(results, tuple.Get("score").(int64))
	}

	if len(results) != 10 {
		t.Fatalf("expected 10 tuples, got %d", len(results))
	}

	// Verify monotonic ascending order
	for i := 1; i < len(results); i++ {
		if results[i] < results[i-1] {
			t.Fatalf("ordering violation at index %d: %d < %d", i, results[i], results[i-1])
		}
	}

	// Verify these 10 are indeed the smallest 10 scores from the entire 1,000 tuples
	allScores := make([]int64, nTuples)
	for i := range tuples {
		allScores[i] = tuples[i].Get("score").(int64)
	}
	sort.Slice(allScores, func(i, j int) bool { return allScores[i] < allScores[j] })
	for i := 0; i < 10; i++ {
		if results[i] != allScores[i] {
			t.Fatalf("mismatch at rank %d: expected %d, got %d", i, allScores[i], results[i])
		}
	}
}

func TestSortIterator_PreboundMultiSlotSorting(t *testing.T) {
	ctx := context.Background()

	schema := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "cat_id", Type: storage.TypeInt},
			{Name: "score", Type: storage.TypeFloat},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	colMap := execution.NewColumnIndexMap(schema, "items")

	tuples := []execution.Tuple{
		{Values: []any{int64(2), float64(88.5), "Bravo"}, Index: colMap},
		{Values: []any{int64(1), float64(95.0), "Charlie"}, Index: colMap},
		{Values: []any{int64(1), float64(95.0), "Alpha"}, Index: colMap},
		{Values: []any{int64(2), float64(99.0), "Delta"}, Index: colMap},
		{Values: []any{int64(1), float64(70.0), "Echo"}, Index: colMap},
	}

	mock := &mockTupleIterator{tuples: tuples, schema: []string{"cat_id", "score", "name"}}
	sortIter := execution.NewSortIterator(mock, "cat_id ASC, score DESC, name ASC")

	if err := sortIter.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer sortIter.Close()

	var results []execution.Tuple
	for {
		tuple, err := sortIter.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next error: %v", err)
		}
		results = append(results, tuple)
	}

	if len(results) != 5 {
		t.Fatalf("expected 5 tuples, got %d", len(results))
	}

	// Expected order:
	// 1: cat_id 1, score 95.0, name Alpha
	// 2: cat_id 1, score 95.0, name Charlie
	// 3: cat_id 1, score 70.0, name Echo
	// 4: cat_id 2, score 99.0, name Delta
	// 5: cat_id 2, score 88.5, name Bravo
	expectedNames := []string{"Alpha", "Charlie", "Echo", "Delta", "Bravo"}
	for i, exp := range expectedNames {
		gotName := results[i].Get("name").(string)
		if gotName != exp {
			t.Errorf("row %d: expected name %q, got %q", i, exp, gotName)
		}
	}
}

func TestSortIterator_DecimalSpillIntegrity(t *testing.T) {
	ctx := context.Background()
	schema := []string{"id", "amount"}
	colMap := execution.NewColumnIndexMapFromCols(schema)

	// Numerical values: 2.50, 10.00, 100.25, 0.75, 20.00
	// If serialized as strings, lexicographical order would wrongly put 10, 100 before 2!
	// With Tag 0x07 Decimal codec, mathematical ordering must be strictly preserved.
	d2_50, _ := storage.ParseDecimal("2.50")
	d10_00, _ := storage.ParseDecimal("10.00")
	d100_25, _ := storage.ParseDecimal("100.25")
	d0_75, _ := storage.ParseDecimal("0.75")
	d20_00, _ := storage.ParseDecimal("20.00")

	tuples := []execution.Tuple{
		{Values: []any{int64(1), d100_25}, Index: colMap},
		{Values: []any{int64(2), d2_50}, Index: colMap},
		{Values: []any{int64(3), d20_00}, Index: colMap},
		{Values: []any{int64(4), d0_75}, Index: colMap},
		{Values: []any{int64(5), d10_00}, Index: colMap},
	}

	mock := &mockTupleIterator{tuples: tuples, schema: schema}
	sortIter := execution.NewSortIterator(mock, "amount ASC")
	// Set very small work_mem (64 bytes) to force spilling to disk
	sortIter.SetWorkMem(64)

	if err := sortIter.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer sortIter.Close()

	var results []execution.Tuple
	for {
		tuple, err := sortIter.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next error: %v", err)
		}
		results = append(results, tuple)
	}

	if len(results) != 5 {
		t.Fatalf("expected 5 tuples, got %d", len(results))
	}

	expectedIDs := []int64{4, 2, 5, 3, 1} // 0.75, 2.50, 10.00, 20.00, 100.25
	for i, expID := range expectedIDs {
		gotID := results[i].Get("id").(int64)
		if gotID != expID {
			t.Errorf("row %d: expected id %d, got %d (val: %v)", i, expID, gotID, results[i].Get("amount"))
		}
		gotDec, ok := results[i].Get("amount").(storage.Decimal)
		if !ok {
			t.Fatalf("row %d: expected storage.Decimal, got %T (%v)", i, results[i].Get("amount"), results[i].Get("amount"))
		}
		_ = gotDec
	}
}

func TestSortIterator_NegativeDecimalSpillIntegrity(t *testing.T) {
	ctx := context.Background()
	schema := []string{"id", "val"}
	colMap := execution.NewColumnIndexMapFromCols(schema)

	// Negative decimals and different scales:
	// -100.50, -10.0, -2.500, 0.00, 2.50
	d_neg100_50, _ := storage.ParseDecimal("-100.50")
	d_neg10_0, _ := storage.ParseDecimal("-10.0")
	d_neg2_500, _ := storage.ParseDecimal("-2.500")
	d_zero, _ := storage.ParseDecimal("0.00")
	d_pos2_50, _ := storage.ParseDecimal("2.50")

	tuples := []execution.Tuple{
		{Values: []any{int64(1), d_pos2_50}, Index: colMap},
		{Values: []any{int64(2), d_neg2_500}, Index: colMap},
		{Values: []any{int64(3), d_neg100_50}, Index: colMap},
		{Values: []any{int64(4), d_zero}, Index: colMap},
		{Values: []any{int64(5), d_neg10_0}, Index: colMap},
	}

	mock := &mockTupleIterator{tuples: tuples, schema: schema}
	sortIter := execution.NewSortIterator(mock, "val ASC")
	sortIter.SetWorkMem(64) // force disk spill

	if err := sortIter.Open(ctx); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer sortIter.Close()

	var results []execution.Tuple
	for {
		tuple, err := sortIter.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next error: %v", err)
		}
		results = append(results, tuple)
	}

	if len(results) != 5 {
		t.Fatalf("expected 5 tuples, got %d", len(results))
	}

	expectedIDs := []int64{3, 5, 2, 4, 1} // -100.50, -10.0, -2.500, 0.00, 2.50
	for i, expID := range expectedIDs {
		gotID := results[i].Get("id").(int64)
		if gotID != expID {
			t.Errorf("row %d: expected id %d, got %d (amount: %v)", i, expID, gotID, results[i].Get("val"))
		}
	}
}

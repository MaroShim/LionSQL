package execution_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"lionsql/pkg/execution"
)

func TestGovernor_EstimateTupleSize(t *testing.T) {
	schema := []string{"id", "name", "price", "active", "createdAt"}
	tuple := execution.NewTupleFromMap(map[string]any{
		"id":        int64(1001),
		"name":      "Alice in Wonderland",
		"price":     99.95,
		"active":    true,
		"createdAt": time.Now(),
	}, schema)

	size := execution.EstimateTupleSize(tuple)
	if size <= 0 {
		t.Fatalf("expected positive tuple size, got %d", size)
	}
	if size < 50 || size > 1000 {
		t.Errorf("tuple size outside reasonable bounds: %d", size)
	}
}

func TestGovernor_ParseAndFormatMemSize(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		format   string
	}{
		{"64MB", 64 * 1024 * 1024, "64MB"},
		{"'128MB'", 128 * 1024 * 1024, "128MB"},
		{"1GB", 1024 * 1024 * 1024, "1GB"},
		{"16kB", 16 * 1024, "16kB"},
		{"4096", 4096, "4kB"},
		{"4096B", 4096, "4kB"},
	}

	for _, tc := range tests {
		bytes, err := execution.ParseMemSize(tc.input)
		if err != nil {
			t.Fatalf("ParseMemSize(%q) failed: %v", tc.input, err)
		}
		if bytes != tc.expected {
			t.Errorf("ParseMemSize(%q) = %d, expected %d", tc.input, bytes, tc.expected)
		}
		formatted := execution.FormatMemSize(bytes)
		if formatted != tc.format {
			t.Errorf("FormatMemSize(%d) = %q, expected %q", bytes, formatted, tc.format)
		}
	}
}

func TestGovernor_PhysicalMemoryAndDefaultMaxMemory(t *testing.T) {
	phys := execution.GetPhysicalMemory()
	if phys <= 0 {
		t.Fatalf("expected positive physical memory, got %d", phys)
	}

	defMax := execution.GetDefaultMaxMemory()
	expectedRatio := int64(float64(phys) * execution.DefaultMaxMemoryRatio)
	if defMax != expectedRatio {
		t.Errorf("expected default max memory %d, got %d", expectedRatio, defMax)
	}

	// Test soft limit configuration
	oldLimit := execution.SetGlobalMemoryLimit(defMax)
	if oldLimit <= 0 {
		t.Errorf("expected positive old limit, got %d", oldLimit)
	}

	curr := execution.GetGlobalMemoryLimit()
	if curr != defMax {
		t.Errorf("expected current limit %d, got %d", defMax, curr)
	}

	// Restore limit
	execution.SetGlobalMemoryLimit(oldLimit)
}

func TestGovernor_HashJoinCircuitBreaker(t *testing.T) {
	ctx := context.Background()

	// 1. Create mock tuples for build side
	t1 := execution.Tuple{Values: []any{int64(1), "Item 1", "Category A"}}
	t2 := execution.Tuple{Values: []any{int64(2), "Item 2", "Category B"}}
	t3 := execution.Tuple{Values: []any{int64(3), "Item 3", "Category C"}}

	rightIter := newMockSortedIterator([]execution.Tuple{t1, t2, t3}, "items", []string{"id", "name", "category"})
	leftIter := newMockSortedIterator([]execution.Tuple{t1}, "orders", []string{"id", "name", "category"})

	hj := execution.NewHashJoinIterator(leftIter, rightIter, "INNER", "id", "id")

	// Set tiny work_mem (50 bytes) so it fails on second/third tuple
	hj.SetWorkMem(50)

	err := hj.Open(ctx)
	if err == nil {
		t.Fatalf("expected HashJoinIterator to fail when exceeding work_mem, but it succeeded")
	}

	if !strings.Contains(err.Error(), "hash join memory budget exceeded") {
		t.Errorf("unexpected error message: %v", err)
	}

	// 2. Set generous work_mem (64MB) and ensure it succeeds
	rightIter2 := newMockSortedIterator([]execution.Tuple{t1, t2, t3}, "items", []string{"id", "name", "category"})
	leftIter2 := newMockSortedIterator([]execution.Tuple{t1}, "orders", []string{"id", "name", "category"})

	hj2 := execution.NewHashJoinIterator(leftIter2, rightIter2, "INNER", "id", "id")
	hj2.SetWorkMem(64 * 1024 * 1024)

	if err := hj2.Open(ctx); err != nil {
		t.Fatalf("expected HashJoinIterator to succeed with 64MB work_mem, got error: %v", err)
	}
	defer hj2.Close()

	if hj2.BuildMemory() <= 0 {
		t.Errorf("expected positive build memory, got %d", hj2.BuildMemory())
	}
}

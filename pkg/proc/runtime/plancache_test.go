package runtime_test

import (
	"testing"

	"sqlion/pkg/proc/runtime"
	_ "sqlion/pkg/proc/parser/antlr"
)

func TestPlanCache_BoundedLRU(t *testing.T) {
	cache := runtime.NewPlanCacheWithCapacity(2)
	if cache.Capacity() != 2 {
		t.Fatalf("expected capacity 2, got %d", cache.Capacity())
	}
	if cache.Len() != 0 {
		t.Fatalf("expected initial len 0, got %d", cache.Len())
	}

	sqlA := "SELECT 1;"
	sqlB := "SELECT 2;"
	sqlC := "SELECT 3;"

	// 1. Compile A and B
	progA, err := cache.GetOrCompile(sqlA)
	if err != nil {
		t.Fatalf("failed to compile A: %v", err)
	}
	progB, err := cache.GetOrCompile(sqlB)
	if err != nil {
		t.Fatalf("failed to compile B: %v", err)
	}
	if progB == nil {
		t.Fatalf("expected progB to not be nil")
	}
	if cache.Len() != 2 {
		t.Fatalf("expected len 2, got %d", cache.Len())
	}

	// 2. Access A again to promote it to front of LRU
	cachedA, err := cache.GetOrCompile(sqlA)
	if err != nil {
		t.Fatalf("failed to get cached A: %v", err)
	}
	if cachedA != progA {
		t.Errorf("expected cached program A pointer to match")
	}

	// 3. Compile C -> should trigger eviction of least recently used (B)
	progC, err := cache.GetOrCompile(sqlC)
	if err != nil {
		t.Fatalf("failed to compile C: %v", err)
	}
	if cache.Len() != 2 {
		t.Fatalf("expected len to stay at 2 after eviction, got %d", cache.Len())
	}

	// Verify A and C are still compiled and cached
	cachedC, _ := cache.GetOrCompile(sqlC)
	if cachedC != progC {
		t.Errorf("expected C to be cached")
	}

	// 4. Test Clear
	cache.Clear()
	if cache.Len() != 0 {
		t.Errorf("expected len 0 after clear, got %d", cache.Len())
	}
}

func TestPlanCache_PutAndGetByName(t *testing.T) {
	cache := runtime.NewPlanCache()
	sql := `
CREATE PROCEDURE TestProc
AS
BEGIN
    RETURN 42;
END;
`
	prog, err := cache.GetOrCompile(sql)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	byName, ok := cache.GetByName("TestProc")
	if !ok || byName == nil {
		t.Fatalf("expected procedure found by name")
	}
	if byName != prog {
		t.Errorf("expected byName to return identical compiled program")
	}

	// Case-insensitive lookup
	byLower, ok := cache.GetByName("testproc")
	if !ok || byLower != prog {
		t.Errorf("expected case-insensitive byName lookup")
	}
}

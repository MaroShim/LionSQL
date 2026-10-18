package wire

import (
	"fmt"
	"sync"
	"testing"
)

func TestPreparedStatementCache_BasicOperations(t *testing.T) {
	cache := NewPreparedStatementCache(3)
	if cache.Capacity() != 3 {
		t.Fatalf("expected capacity 3, got %d", cache.Capacity())
	}
	if cache.Len() != 0 {
		t.Fatalf("expected len 0, got %d", cache.Len())
	}

	// 1. Unnamed statement
	unnamed := &PreparedStatement{Name: "", Query: "SELECT 1"}
	cache.Put("", unnamed)
	if cache.Len() != 1 {
		t.Fatalf("expected len 1, got %d", cache.Len())
	}
	gotUnnamed, exists := cache.Get("")
	if !exists || gotUnnamed.Query != "SELECT 1" {
		t.Fatalf("expected unnamed statement, got %v, exists=%v", gotUnnamed, exists)
	}

	// 2. Named statement
	prep1 := &PreparedStatement{Name: "stmt1", Query: "SELECT 10"}
	cache.Put("stmt1", prep1)
	if cache.Len() != 2 {
		t.Fatalf("expected len 2, got %d", cache.Len())
	}
	got1, exists := cache.Get("stmt1")
	if !exists || got1.Query != "SELECT 10" {
		t.Fatalf("expected stmt1, got %v", got1)
	}

	// 3. Update existing statement
	prep1Updated := &PreparedStatement{Name: "stmt1", Query: "SELECT 20"}
	cache.Put("stmt1", prep1Updated)
	if cache.Len() != 2 {
		t.Fatalf("expected len 2 after update, got %d", cache.Len())
	}
	got1Updated, exists := cache.Get("stmt1")
	if !exists || got1Updated.Query != "SELECT 20" {
		t.Fatalf("expected updated query 'SELECT 20', got %v", got1Updated)
	}

	// 4. Delete
	cache.Delete("stmt1")
	if cache.Len() != 1 {
		t.Fatalf("expected len 1 after delete, got %d", cache.Len())
	}
	if _, exists := cache.Get("stmt1"); exists {
		t.Fatalf("expected stmt1 to be deleted")
	}

	cache.Delete("")
	if cache.Len() != 0 {
		t.Fatalf("expected len 0 after deleting unnamed, got %d", cache.Len())
	}
}

func TestPreparedStatementCache_LRUEviction(t *testing.T) {
	cache := NewPreparedStatementCache(3)

	cache.Put("s1", &PreparedStatement{Name: "s1", Query: "Q1"})
	cache.Put("s2", &PreparedStatement{Name: "s2", Query: "Q2"})
	cache.Put("s3", &PreparedStatement{Name: "s3", Query: "Q3"})

	if cache.Len() != 3 {
		t.Fatalf("expected len 3, got %d", cache.Len())
	}

	// Adding 4th named statement evicts s1 (the oldest)
	cache.Put("s4", &PreparedStatement{Name: "s4", Query: "Q4"})
	if cache.Len() != 3 {
		t.Fatalf("expected len 3 after eviction, got %d", cache.Len())
	}
	if _, exists := cache.Get("s1"); exists {
		t.Fatalf("expected s1 to be evicted")
	}
	for _, name := range []string{"s2", "s3", "s4"} {
		if _, exists := cache.Get(name); !exists {
			t.Fatalf("expected %s to exist", name)
		}
	}

	// Access s2 to promote it to MRU (front). LRU order: s3 (oldest), s4, s2 (newest)
	if _, exists := cache.Get("s2"); !exists {
		t.Fatalf("s2 should exist")
	}

	// Adding s5 should evict s3 (not s2)
	cache.Put("s5", &PreparedStatement{Name: "s5", Query: "Q5"})
	if _, exists := cache.Get("s3"); exists {
		t.Fatalf("expected s3 to be evicted after s2 was promoted")
	}
	if _, exists := cache.Get("s2"); !exists {
		t.Fatalf("expected s2 to still exist")
	}
	if _, exists := cache.Get("s4"); !exists {
		t.Fatalf("expected s4 to still exist")
	}
	if _, exists := cache.Get("s5"); !exists {
		t.Fatalf("expected s5 to exist")
	}
}

func TestPreparedStatementCache_Concurrency(t *testing.T) {
	cache := NewPreparedStatementCache(10)
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				name := fmt.Sprintf("stmt_%d_%d", workerID, j)
				cache.Put(name, &PreparedStatement{Name: name, Query: "SELECT 1"})
				_, _ = cache.Get(name)
				if j%5 == 0 {
					cache.Delete(name)
				}
			}
		}(i)
	}

	wg.Wait()
	if cache.Len() > 10 {
		t.Fatalf("expected cache len <= 10, got %d", cache.Len())
	}
}

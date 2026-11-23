package storage

import (
	"fmt"
	"reflect"
	"testing"
)

func TestTxReadCache_BasicOperations(t *testing.T) {
	cache := NewTxReadCache(16)
	if cache == nil {
		t.Fatal("expected non-nil cache")
	}
	if cache.Len() != 0 {
		t.Fatalf("expected len 0, got %d", cache.Len())
	}

	// Miss on empty cache
	entry, found := cache.Get("users", "1")
	if found {
		t.Fatalf("expected found=false on empty cache")
	}
	if len(entry.Row.Values) != 0 {
		t.Fatalf("expected empty row values")
	}

	// PutRow
	row := Row{Values: []Value{IntValue(1), StringValue("alice")}}
	cache.PutRow("users", "1", row, 42)
	if cache.Len() != 1 {
		t.Fatalf("expected len 1, got %d", cache.Len())
	}

	// Hit PutRow
	entry, found = cache.Get("users", "1")
	if !found {
		t.Fatalf("expected found=true")
	}
	if !reflect.DeepEqual(row, entry.Row) {
		t.Fatalf("row mismatch: got %+v, want %+v", entry.Row, row)
	}
	if entry.Ver != 42 {
		t.Fatalf("expected ver 42, got %d", entry.Ver)
	}
	if entry.Record != nil {
		t.Fatalf("expected nil record, got %+v", entry.Record)
	}

	// PutRecord updates the same entry preserving existing row
	rec := Record{"id": IntValue(1), "name": StringValue("alice")}
	cache.PutRecord("users", "1", rec)
	entry, found = cache.Get("users", "1")
	if !found {
		t.Fatalf("expected found=true after PutRecord")
	}
	if !reflect.DeepEqual(row, entry.Row) {
		t.Fatalf("row mismatch after PutRecord: got %+v, want %+v", entry.Row, row)
	}
	if !reflect.DeepEqual(rec, entry.Record) {
		t.Fatalf("rec mismatch after PutRecord: got %+v, want %+v", entry.Record, rec)
	}

	// Invalidate
	cache.Invalidate("users", "1")
	_, found = cache.Get("users", "1")
	if found {
		t.Fatalf("expected found=false after Invalidate")
	}
	if cache.Len() != 0 {
		t.Fatalf("expected len 0 after Invalidate, got %d", cache.Len())
	}
}

func TestTxReadCache_ResetAndBounding(t *testing.T) {
	cache := NewTxReadCache(16)

	// Insert 200 items (exceeding 128 threshold)
	for i := 0; i < 200; i++ {
		cache.PutRow("orders", fmt.Sprintf("%d", i), Row{Values: []Value{IntValue(int64(i))}}, int64(i))
	}
	if cache.Len() != 200 {
		t.Fatalf("expected len 200, got %d", cache.Len())
	}

	// Reset should shrink map and clear entries per [RULE:BoundedBuffers]
	cache.Reset()
	if cache.Len() != 0 {
		t.Fatalf("expected len 0 after Reset, got %d", cache.Len())
	}

	// Ensure cache is still usable after Reset
	cache.PutRow("orders", "10", Row{Values: []Value{IntValue(10)}}, 10)
	if cache.Len() != 1 {
		t.Fatalf("expected len 1, got %d", cache.Len())
	}
	entry, found := cache.Get("orders", "10")
	if !found || entry.Ver != 10 {
		t.Fatalf("expected found=true with ver 10, got found=%v, ver=%d", found, entry.Ver)
	}

	// Clear under threshold
	cache.Clear()
	if cache.Len() != 0 {
		t.Fatalf("expected len 0 after Clear, got %d", cache.Len())
	}
}

func TestTxReadCache_NilSafe(t *testing.T) {
	var nilCache *TxReadCache

	// Nil receiver should not panic
	_, found := nilCache.Get("users", "1")
	if found {
		t.Fatal("expected found=false on nil cache")
	}
	if nilCache.Len() != 0 {
		t.Fatalf("expected len 0 on nil cache, got %d", nilCache.Len())
	}

	nilCache.Put("users", "1", CachedRowEntry{})
	nilCache.PutRow("users", "1", Row{}, 1)
	nilCache.PutRecord("users", "1", Record{})
	nilCache.Invalidate("users", "1")
	nilCache.Clear()
	nilCache.Reset()
}

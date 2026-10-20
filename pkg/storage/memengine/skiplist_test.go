package memengine

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"lionsql/pkg/storage"
)

func TestSkipList_BasicCRUD(t *testing.T) {
	sl := NewSkipList(false)

	// 1. Insert multiple items
	_ = sl.Insert(storage.IntValue(10), "pk1")
	_ = sl.Insert(storage.IntValue(20), "pk2")
	_ = sl.Insert(storage.IntValue(15), "pk3")
	_ = sl.Insert(storage.IntValue(20), "pk4") // Duplicate key for non-unique

	if sl.Len() != 3 {
		t.Fatalf("Expected 3 unique keys, got %d", sl.Len())
	}

	// 2. Search
	pks, found := sl.Search(storage.IntValue(20))
	if !found || len(pks) != 2 {
		t.Fatalf("Expected 2 PKs for key 20, got %v (found: %v)", pks, found)
	}

	_, found = sl.Search(storage.IntValue(99))
	if found {
		t.Fatalf("Key 99 should not be found")
	}

	// 3. Delete
	sl.Delete(storage.IntValue(20), "pk2")
	pks, found = sl.Search(storage.IntValue(20))
	if !found || len(pks) != 1 || pks[0] != "pk4" {
		t.Fatalf("Expected only pk4 remaining for key 20, got %v", pks)
	}

	sl.Delete(storage.IntValue(20), "pk4")
	_, found = sl.Search(storage.IntValue(20))
	if found {
		t.Fatalf("Key 20 should be completely pruned after deleting all PKs")
	}
	if sl.Len() != 2 {
		t.Fatalf("Expected 2 keys remaining, got %d", sl.Len())
	}
}

func TestSkipList_UniqueConstraint(t *testing.T) {
	sl := NewSkipList(true)

	err := sl.Insert(storage.StringValue("alice@example.com"), "user_1")
	if err != nil {
		t.Fatalf("First insert failed: %v", err)
	}

	// Re-inserting identical PK for same key is idempotent
	err = sl.Insert(storage.StringValue("alice@example.com"), "user_1")
	if err != nil {
		t.Fatalf("Re-inserting same PK should succeed idempotently, got: %v", err)
	}

	// Inserting duplicate key with different PK must fail
	err = sl.Insert(storage.StringValue("alice@example.com"), "user_2")
	if !errors.Is(err, storage.ErrUniqueViolation) {
		t.Fatalf("Expected ErrUniqueViolation on duplicate key, got: %v", err)
	}
}

func TestSkipList_ScanRange(t *testing.T) {
	sl := NewSkipList(false)

	for i := 1; i <= 10; i++ {
		_ = sl.Insert(storage.IntValue(int64(i*10)), fmt.Sprintf("pk_%d", i*10))
	}

	// Range [30, 70]
	low := storage.IntValue(30)
	high := storage.IntValue(70)
	pks := sl.ScanRange(&low, &high)
	if len(pks) != 5 {
		t.Fatalf("Expected 5 items in [30, 70], got %d (%v)", len(pks), pks)
	}
	if pks[0] != "pk_30" || pks[4] != "pk_70" {
		t.Fatalf("Unexpected range boundaries: %v", pks)
	}

	// Range unbounded high [80, nil]
	low80 := storage.IntValue(80)
	pksHigh := sl.ScanRange(&low80, nil)
	if len(pksHigh) != 3 {
		t.Fatalf("Expected 3 items in [80, nil], got %d (%v)", len(pksHigh), pksHigh)
	}

	// Range unbounded low [nil, 30]
	high30 := storage.IntValue(30)
	pksLow := sl.ScanRange(nil, &high30)
	if len(pksLow) != 3 {
		t.Fatalf("Expected 3 items in [nil, 30], got %d (%v)", len(pksLow), pksLow)
	}

	// Unbounded full scan
	pksAll := sl.ScanRange(nil, nil)
	if len(pksAll) != 10 {
		t.Fatalf("Expected 10 items in full scan, got %d", len(pksAll))
	}

	// Out of bounds range
	lowOut := storage.IntValue(105)
	highOut := storage.IntValue(200)
	pksEmpty := sl.ScanRange(&lowOut, &highOut)
	if len(pksEmpty) != 0 {
		t.Fatalf("Expected empty range for out-of-bounds scan, got %d", len(pksEmpty))
	}
}

func TestSkipList_SerializationRoundTrip(t *testing.T) {
	sl := NewSkipList(false)

	_ = sl.Insert(storage.StringValue("banana"), "pk2")
	_ = sl.Insert(storage.StringValue("apple"), "pk1")
	_ = sl.Insert(storage.StringValue("cherry"), "pk3")

	entries := sl.ToEntries()
	if len(entries) != 3 {
		t.Fatalf("Expected 3 entries, got %d", len(entries))
	}
	if entries[0].Key.SVal != "apple" || entries[1].Key.SVal != "banana" || entries[2].Key.SVal != "cherry" {
		t.Fatalf("Entries should be strictly sorted, got: %v", entries)
	}

	sl2 := NewSkipList(false)
	if err := sl2.FromEntries(entries); err != nil {
		t.Fatalf("FromEntries failed: %v", err)
	}

	if sl2.Len() != 3 {
		t.Fatalf("Expected sl2 len 3, got %d", sl2.Len())
	}
	pks, found := sl2.Search(storage.StringValue("banana"))
	if !found || len(pks) != 1 || pks[0] != "pk2" {
		t.Fatalf("Failed to retrieve banana from restored skiplist: %v", pks)
	}
}

func TestSkipList_ConcurrentReadWriteRace(t *testing.T) {
	sl := NewSkipList(false)
	var wg sync.WaitGroup

	numWriters := 8
	itemsPerWriter := 200

	// Concurrent Writers
	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < itemsPerWriter; i++ {
				keyVal := storage.IntValue(int64(i))
				pk := fmt.Sprintf("w%d_pk%d", workerID, i)
				_ = sl.Insert(keyVal, pk)
				time.Sleep(10 * time.Microsecond)
			}
		}(w)
	}

	// Concurrent Readers
	numReaders := 4
	for r := 0; r < numReaders; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < itemsPerWriter/2; i++ {
				keyVal := storage.IntValue(int64(i))
				_, _ = sl.Search(keyVal)
				low := storage.IntValue(10)
				high := storage.IntValue(50)
				_ = sl.ScanRange(&low, &high)
				time.Sleep(20 * time.Microsecond)
			}
		}()
	}

	wg.Wait()

	if sl.Len() != itemsPerWriter {
		t.Fatalf("Expected %d unique keys, got %d", itemsPerWriter, sl.Len())
	}
}

func BenchmarkSkipList_Insert(b *testing.B) {
	sl := NewSkipList(false)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sl.Insert(storage.IntValue(int64(i)), fmt.Sprintf("pk_%d", i))
	}
}

func BenchmarkSkipList_Search(b *testing.B) {
	sl := NewSkipList(false)
	n := 10000
	for i := 0; i < n; i++ {
		_ = sl.Insert(storage.IntValue(int64(i)), fmt.Sprintf("pk_%d", i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = sl.Search(storage.IntValue(int64(i % n)))
	}
}

func BenchmarkSkipList_ScanRange(b *testing.B) {
	sl := NewSkipList(false)
	n := 10000
	for i := 0; i < n; i++ {
		_ = sl.Insert(storage.IntValue(int64(i)), fmt.Sprintf("pk_%d", i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		low := storage.IntValue(1000)
		high := storage.IntValue(1100)
		_ = sl.ScanRange(&low, &high)
	}
}

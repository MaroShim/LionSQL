package storage

import (
	"fmt"
	"sync"
	"testing"
	"unsafe"
)

func TestArena_NilSafe(t *testing.T) {
	var a *Arena

	b := a.Alloc(16)
	if len(b) != 16 {
		t.Fatalf("expected 16 bytes, got %d", len(b))
	}

	raw := []byte("hello")
	cp := a.AllocBytes(raw)
	if string(cp) != "hello" {
		t.Fatalf("expected 'hello', got %s", string(cp))
	}

	s := a.AllocString("test string")
	if s != "test string" {
		t.Fatalf("expected 'test string', got %s", s)
	}

	vals := a.AllocValues(5)
	if len(vals) != 5 {
		t.Fatalf("expected 5 values, got %d", len(vals))
	}

	anys := a.AllocAnySlice(10)
	if len(anys) != 10 {
		t.Fatalf("expected 10 any values, got %d", len(anys))
	}

	a.Reset()
	a.Release()
	if a.TotalAllocated() != 0 || a.TotalCapacity() != 0 {
		t.Fatalf("expected 0 stats for nil arena")
	}
}

func TestArena_ByteAllocationsAndAlignment(t *testing.T) {
	a := NewArena()
	defer a.Release()

	// Allocate 3 bytes, then 5 bytes, check 8-byte alignment
	b1 := a.Alloc(3)
	if len(b1) != 3 {
		t.Fatalf("expected 3 bytes, got %d", len(b1))
	}
	copy(b1, "abc")

	b2 := a.Alloc(5)
	if len(b2) != 5 {
		t.Fatalf("expected 5 bytes, got %d", len(b2))
	}
	copy(b2, "12345")

	// Pointers should be 8-byte aligned
	p1 := uintptr(unsafe.Pointer(&b1[0]))
	p2 := uintptr(unsafe.Pointer(&b2[0]))
	if p1%8 != 0 {
		t.Errorf("p1 %x not 8-byte aligned", p1)
	}
	if p2%8 != 0 {
		t.Errorf("p2 %x not 8-byte aligned", p2)
	}

	if string(b1) != "abc" || string(b2) != "12345" {
		t.Errorf("data mismatch: b1=%s, b2=%s", string(b1), string(b2))
	}

	// Large block allocation (> DefaultByteSlabSize/2)
	largeSize := DefaultByteSlabSize
	bLarge := a.Alloc(largeSize)
	if len(bLarge) != largeSize {
		t.Fatalf("expected %d bytes, got %d", largeSize, len(bLarge))
	}
	bLarge[0] = 0xAA
	bLarge[largeSize-1] = 0xBB
	if bLarge[0] != 0xAA || bLarge[largeSize-1] != 0xBB {
		t.Fatalf("large block data mismatch")
	}

	if a.TotalAllocated() < int64(3+5+largeSize) {
		t.Errorf("expected total allocated >= %d, got %d", 3+5+largeSize, a.TotalAllocated())
	}
}

func TestArena_StringAndBytes(t *testing.T) {
	a := GetArena()
	defer PutArena(a)

	s1 := a.AllocString("first_query_string")
	s2 := a.AllocString("second_query_string")

	if s1 != "first_query_string" || s2 != "second_query_string" {
		t.Fatalf("string alloc mismatch: s1=%s, s2=%s", s1, s2)
	}

	bytes := a.AllocBytes([]byte("binary_payload"))
	if string(bytes) != "binary_payload" {
		t.Fatalf("bytes mismatch: %s", string(bytes))
	}
}

func TestArena_ValuesAndAnySlice(t *testing.T) {
	a := NewArena()
	defer a.Release()

	vals := a.AllocValues(3)
	if len(vals) != 3 {
		t.Fatalf("expected 3 values, got %d", len(vals))
	}
	vals[0] = IntValue(42)
	vals[1] = StringValue("hello")
	vals[2] = FloatValue(3.14)

	if vals[0].IVal != 42 || vals[1].SVal != "hello" || vals[2].FVal != 3.14 {
		t.Fatalf("values mismatch: %+v", vals)
	}

	anys := a.AllocAnySlice(4)
	if len(anys) != 4 {
		t.Fatalf("expected 4 any values, got %d", len(anys))
	}
	anys[0] = 100
	anys[1] = "tuple_str"
	anys[2] = true
	anys[3] = nil

	if anys[0] != 100 || anys[1] != "tuple_str" || anys[2] != true || anys[3] != nil {
		t.Fatalf("any values mismatch: %+v", anys)
	}

	// Large allocations
	largeVals := a.AllocValues(DefaultValueSlabSize)
	if len(largeVals) != DefaultValueSlabSize {
		t.Fatalf("expected %d large values, got %d", DefaultValueSlabSize, len(largeVals))
	}

	largeAnys := a.AllocAnySlice(DefaultAnySlabSize)
	if len(largeAnys) != DefaultAnySlabSize {
		t.Fatalf("expected %d large anys, got %d", DefaultAnySlabSize, len(largeAnys))
	}
}

func TestArena_ResetAndReuse(t *testing.T) {
	a := GetArena()

	for i := 0; i < 5; i++ {
		_ = a.Alloc(1024)
		_ = a.AllocString(fmt.Sprintf("iter_%d", i))
		_ = a.AllocValues(100)
		_ = a.AllocAnySlice(100)
		if a.TotalAllocated() == 0 {
			t.Fatalf("expected >0 allocated in iter %d", i)
		}
		a.Reset()
		if a.TotalAllocated() != 0 {
			t.Fatalf("expected 0 allocated after reset in iter %d, got %d", i, a.TotalAllocated())
		}
	}

	PutArena(a)
}

func TestArena_ConcurrentSafe(t *testing.T) {
	a := NewArena()
	defer a.Release()

	const workers = 8
	const iters = 200

	var wg sync.WaitGroup
	wg.Add(workers)

	for w := 0; w < workers; w++ {
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				str := fmt.Sprintf("worker_%d_iter_%d", workerID, i)
				s := a.AllocString(str)
				if s != str {
					t.Errorf("worker %d: expected %s, got %s", workerID, str, s)
				}
				v := a.AllocValues(2)
				v[0] = IntValue(int64(i))
				v[1] = StringValue(s)

				anys := a.AllocAnySlice(2)
				anys[0] = i
				anys[1] = s
			}
		}(w)
	}

	wg.Wait()
	if a.TotalAllocated() == 0 {
		t.Fatalf("expected >0 allocated bytes after concurrent test")
	}
}

func BenchmarkArena_AllocString(b *testing.B) {
	a := NewArena()
	defer a.Release()

	sample := "test_column_hash_key_value_12345678"
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = a.AllocString(sample)
		if i%1000 == 0 {
			a.Reset()
		}
	}
}

func BenchmarkArena_AllocValues(b *testing.B) {
	a := NewArena()
	defer a.Release()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		vals := a.AllocValues(8)
		vals[0] = IntValue(int64(i))
		if i%1000 == 0 {
			a.Reset()
		}
	}
}

func BenchmarkHeap_AllocValues(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		vals := make([]Value, 8)
		vals[0] = IntValue(int64(i))
		_ = vals
	}
}

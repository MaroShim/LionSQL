package storage

import (
	"sync"
	"unsafe"
)

const (
	// DefaultByteSlabSize is the default slab size for raw byte allocations (64 KB).
	DefaultByteSlabSize = 64 * 1024

	// DefaultValueSlabSize is the default slab size for Value slice allocations (2048 values).
	DefaultValueSlabSize = 2048

	// DefaultAnySlabSize is the default slab size for any (interface{}) slice allocations (2048 elements).
	DefaultAnySlabSize = 2048
)

var (
	byteSlabPool = sync.Pool{
		New: func() any {
			b := make([]byte, DefaultByteSlabSize)
			return &b
		},
	}
	valueSlabPool = sync.Pool{
		New: func() any {
			v := make([]Value, DefaultValueSlabSize)
			return &v
		},
	}
	anySlabPool = sync.Pool{
		New: func() any {
			a := make([]any, DefaultAnySlabSize)
			return &a
		},
	}
	globalArenaPool = sync.Pool{
		New: func() any {
			return NewArena()
		},
	}
)

// Arena is a high-performance query/session-scoped memory allocator that eliminates
// Go runtime GC pressure, heap allocations, and pause times.
// Slabs are pooled via sync.Pool and bump-allocated with 8-byte alignment.
// All allocations are reclaimed in O(1) time when the query completes via Reset() or Release().
type Arena struct {
	mu sync.Mutex

	// Byte slabs
	byteSlabs       []*[]byte
	currByteSlab    []byte
	currByteOffset  int
	largeByteBlocks [][]byte

	// Value slabs
	valueSlabs       []*[]Value
	currValueSlab    []Value
	currValueOffset  int
	largeValueBlocks [][]Value

	// Any (interface{}) slabs
	anySlabs       []*[]any
	currAnySlab    []any
	currAnyOffset  int
	largeAnyBlocks [][]any

	// Metrics
	totalAllocated int64
	totalCapacity  int64
}

// NewArena creates a new uninitialized Arena.
func NewArena() *Arena {
	return &Arena{}
}

// GetArena borrows an Arena from the global pool.
func GetArena() *Arena {
	return globalArenaPool.Get().(*Arena)
}

// PutArena resets the Arena and returns it to the global pool for reuse.
func PutArena(a *Arena) {
	if a == nil {
		return
	}
	a.Reset()
	globalArenaPool.Put(a)
}

// Alloc allocates a raw byte slice of size bytes with 8-byte alignment.
// If a is nil, it falls back to standard make([]byte, size).
func (a *Arena) Alloc(size int) []byte {
	if size <= 0 {
		return nil
	}
	if a == nil {
		return make([]byte, size)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.totalAllocated += int64(size)

	// Oversized allocation: handle large blocks outside slabs
	if size > DefaultByteSlabSize/2 {
		block := make([]byte, size)
		a.largeByteBlocks = append(a.largeByteBlocks, block)
		a.totalCapacity += int64(size)
		return block
	}

	// 8-byte align offset
	alignedOffset := (a.currByteOffset + 7) &^ 7

	if a.currByteSlab == nil || alignedOffset+size > len(a.currByteSlab) {
		slabPtr := byteSlabPool.Get().(*[]byte)
		a.byteSlabs = append(a.byteSlabs, slabPtr)
		a.currByteSlab = *slabPtr
		alignedOffset = 0
		a.totalCapacity += int64(len(a.currByteSlab))
	}

	res := a.currByteSlab[alignedOffset : alignedOffset+size : alignedOffset+size]
	a.currByteOffset = alignedOffset + size
	return res
}

// AllocBytes copies b into arena memory and returns the allocated copy.
// If a is nil, it falls back to make/copy.
func (a *Arena) AllocBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	if a == nil {
		cp := make([]byte, len(b))
		copy(cp, b)
		return cp
	}
	buf := a.Alloc(len(b))
	copy(buf, b)
	return buf
}

// AllocString copies s into arena memory and returns an arena-backed string.
// If a is nil, it returns s unchanged.
func (a *Arena) AllocString(s string) string {
	if len(s) == 0 {
		return ""
	}
	if a == nil {
		return s
	}
	buf := a.Alloc(len(s))
	copy(buf, s)
	return unsafe.String(unsafe.SliceData(buf), len(buf))
}

// AllocValues allocates a slice of n storage.Value structs from value slabs.
// If a is nil, it falls back to make([]Value, n).
func (a *Arena) AllocValues(n int) []Value {
	if n <= 0 {
		return nil
	}
	if a == nil {
		return make([]Value, n)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	elemSize := int64(unsafe.Sizeof(Value{}))
	a.totalAllocated += int64(n) * elemSize

	// Oversized allocation
	if n > DefaultValueSlabSize/2 {
		block := make([]Value, n)
		a.largeValueBlocks = append(a.largeValueBlocks, block)
		a.totalCapacity += int64(n) * elemSize
		return block
	}

	if a.currValueSlab == nil || a.currValueOffset+n > len(a.currValueSlab) {
		slabPtr := valueSlabPool.Get().(*[]Value)
		a.valueSlabs = append(a.valueSlabs, slabPtr)
		a.currValueSlab = *slabPtr
		a.currValueOffset = 0
		a.totalCapacity += int64(len(a.currValueSlab)) * elemSize
	}

	res := a.currValueSlab[a.currValueOffset : a.currValueOffset+n : a.currValueOffset+n]
	a.currValueOffset += n
	return res
}

// AllocAnySlice allocates a slice of n any (interface{}) elements from any slabs.
// If a is nil, it falls back to make([]any, n).
func (a *Arena) AllocAnySlice(n int) []any {
	if n <= 0 {
		return nil
	}
	if a == nil {
		return make([]any, n)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	elemSize := int64(unsafe.Sizeof(any(nil)))
	a.totalAllocated += int64(n) * elemSize

	// Oversized allocation
	if n > DefaultAnySlabSize/2 {
		block := make([]any, n)
		a.largeAnyBlocks = append(a.largeAnyBlocks, block)
		a.totalCapacity += int64(n) * elemSize
		return block
	}

	if a.currAnySlab == nil || a.currAnyOffset+n > len(a.currAnySlab) {
		slabPtr := anySlabPool.Get().(*[]any)
		a.anySlabs = append(a.anySlabs, slabPtr)
		a.currAnySlab = *slabPtr
		a.currAnyOffset = 0
		a.totalCapacity += int64(len(a.currAnySlab)) * elemSize
	}

	res := a.currAnySlab[a.currAnyOffset : a.currAnyOffset+n : a.currAnyOffset+n]
	a.currAnyOffset += n
	return res
}

// Reset clears all allocations and recycles slabs back to sync.Pool.
func (a *Arena) Reset() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	// Return byte slabs
	for _, slabPtr := range a.byteSlabs {
		byteSlabPool.Put(slabPtr)
	}
	a.byteSlabs = a.byteSlabs[:0]
	a.currByteSlab = nil
	a.currByteOffset = 0
	a.largeByteBlocks = nil

	// Return Value slabs (clear references to prevent memory leak)
	for _, slabPtr := range a.valueSlabs {
		clear(*slabPtr)
		valueSlabPool.Put(slabPtr)
	}
	a.valueSlabs = a.valueSlabs[:0]
	a.currValueSlab = nil
	a.currValueOffset = 0
	a.largeValueBlocks = nil

	// Return any slabs (clear references to prevent memory leak)
	for _, slabPtr := range a.anySlabs {
		clear(*slabPtr)
		anySlabPool.Put(slabPtr)
	}
	a.anySlabs = a.anySlabs[:0]
	a.currAnySlab = nil
	a.currAnyOffset = 0
	a.largeAnyBlocks = nil

	a.totalAllocated = 0
	a.totalCapacity = 0
}

// Release is an alias for Reset to release all arena resources.
func (a *Arena) Release() {
	a.Reset()
}

// TotalAllocated returns total bytes allocated from the arena.
func (a *Arena) TotalAllocated() int64 {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.totalAllocated
}

// TotalCapacity returns total bytes acquired in slabs and large blocks.
func (a *Arena) TotalCapacity() int64 {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.totalCapacity
}

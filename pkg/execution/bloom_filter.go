package execution

import (
	"hash/fnv"
)

// FastBloomFilter is an L1/L2 cache-friendly bitset filter optimized for Hash Join
// runtime key pruning and scan pushdown. It uses two 64-bit hash values to mark bits.
type FastBloomFilter struct {
	bits []uint64
	mask uint64
}

// NewFastBloomFilter creates a new FastBloomFilter with capacity aligned to a power of two.
// expectedEntries: estimated number of distinct keys in the build table.
func NewFastBloomFilter(expectedEntries int) *FastBloomFilter {
	if expectedEntries < 16 {
		expectedEntries = 16
	}
	// Aim for ~10 bits per item (approx. 1% false positive rate with 2-hash functions)
	totalBits := expectedEntries * 10
	if totalBits < 1024 {
		totalBits = 1024 // Minimum 128 bytes
	}
	// Round up to next power of 2
	nBits := nextPowerOf2(totalBits)
	numWords := nBits / 64
	if numWords < 16 {
		numWords = 16
	}
	return &FastBloomFilter{
		bits: make([]uint64, numWords),
		mask: uint64(nBits - 1),
	}
}

// AddInt adds an int64 key into the filter.
func (bf *FastBloomFilter) AddInt(key int64) {
	if bf == nil || len(bf.bits) == 0 {
		return
	}
	h1 := splitmix64(uint64(key))
	h2 := splitmix64(h1 ^ 0x9e3779b97f4a7c15)

	bit1 := h1 & bf.mask
	bit2 := h2 & bf.mask

	bf.bits[bit1/64] |= (1 << (bit1 % 64))
	bf.bits[bit2/64] |= (1 << (bit2 % 64))
}

// MayContainInt tests whether an int64 key might be in the filter.
func (bf *FastBloomFilter) MayContainInt(key int64) bool {
	if bf == nil || len(bf.bits) == 0 {
		return true // Fail-open if no filter
	}
	h1 := splitmix64(uint64(key))
	bit1 := h1 & bf.mask
	if (bf.bits[bit1/64] & (1 << (bit1 % 64))) == 0 {
		return false
	}

	h2 := splitmix64(h1 ^ 0x9e3779b97f4a7c15)
	bit2 := h2 & bf.mask
	return (bf.bits[bit2/64] & (1 << (bit2 % 64))) != 0
}

// AddString adds a string key into the filter.
func (bf *FastBloomFilter) AddString(key string) {
	if bf == nil || len(bf.bits) == 0 {
		return
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	h1 := h.Sum64()
	h2 := splitmix64(h1 ^ 0x9e3779b97f4a7c15)

	bit1 := h1 & bf.mask
	bit2 := h2 & bf.mask

	bf.bits[bit1/64] |= (1 << (bit1 % 64))
	bf.bits[bit2/64] |= (1 << (bit2 % 64))
}

// MayContainString tests whether a string key might be in the filter.
func (bf *FastBloomFilter) MayContainString(key string) bool {
	if bf == nil || len(bf.bits) == 0 {
		return true // Fail-open
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	h1 := h.Sum64()

	bit1 := h1 & bf.mask
	if (bf.bits[bit1/64] & (1 << (bit1 % 64))) == 0 {
		return false
	}

	h2 := splitmix64(h1 ^ 0x9e3779b97f4a7c15)
	bit2 := h2 & bf.mask
	return (bf.bits[bit2/64] & (1 << (bit2 % 64))) != 0
}

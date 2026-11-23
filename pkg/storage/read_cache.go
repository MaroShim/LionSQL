package storage

// TxReadCache provides a bounded, zero-allocation transaction-local read cache
// shared across storage engines (memengine OCC, WiredTiger MVCC) per [RULE:ModularAbstraction] and [RULE:BoundedBuffers].
type TxReadCache struct {
	entries map[string]CachedRowEntry
}

// CachedRowEntry holds a cached decoded row, optional record, and MVCC/OCC version.
type CachedRowEntry struct {
	Row    Row
	Record Record
	Ver    int64
}

// Clone returns a deep-safe copy of the cached entry to prevent caller mutations from polluting the cache.
func (e CachedRowEntry) Clone() CachedRowEntry {
	var clonedRec Record
	if e.Record != nil {
		clonedRec = e.Record.Clone()
	}
	return CachedRowEntry{
		Row:    e.Row.Clone(),
		Record: clonedRec,
		Ver:    e.Ver,
	}
}

// NewTxReadCache creates a new transaction-local read cache with the given initial capacity.
func NewTxReadCache(initialCap int) *TxReadCache {
	if initialCap <= 0 {
		initialCap = 16
	}
	return &TxReadCache{
		entries: make(map[string]CachedRowEntry, initialCap),
	}
}

// Init ensures the internal map is allocated with the given initial capacity.
func (c *TxReadCache) Init(initialCap int) {
	if c.entries == nil {
		if initialCap <= 0 {
			initialCap = 16
		}
		c.entries = make(map[string]CachedRowEntry, initialCap)
	}
}

// Get looks up a cached entry by table name and primary key string.
func (c *TxReadCache) Get(table, pk string) (CachedRowEntry, bool) {
	if c == nil || c.entries == nil {
		return CachedRowEntry{}, false
	}
	entry, ok := c.entries[table+"\x00"+pk]
	return entry, ok
}

// Put caches a full CachedRowEntry for a table and primary key.
func (c *TxReadCache) Put(table, pk string, entry CachedRowEntry) {
	if c == nil {
		return
	}
	if c.entries == nil {
		c.entries = make(map[string]CachedRowEntry, 16)
	}
	c.entries[table+"\x00"+pk] = entry
}

// PutRow caches a Row and version for a table and primary key.
func (c *TxReadCache) PutRow(table, pk string, row Row, ver int64) {
	if c == nil {
		return
	}
	if c.entries == nil {
		c.entries = make(map[string]CachedRowEntry, 16)
	}
	entry := c.entries[table+"\x00"+pk]
	entry.Row = row
	entry.Ver = ver
	c.entries[table+"\x00"+pk] = entry
}

// PutRecord caches a Record for a table and primary key.
func (c *TxReadCache) PutRecord(table, pk string, rec Record) {
	if c == nil {
		return
	}
	if c.entries == nil {
		c.entries = make(map[string]CachedRowEntry, 16)
	}
	entry := c.entries[table+"\x00"+pk]
	entry.Record = rec
	c.entries[table+"\x00"+pk] = entry
}

// Invalidate removes the cached entry for the given table and primary key.
func (c *TxReadCache) Invalidate(table, pk string) {
	if c == nil || c.entries == nil {
		return
	}
	delete(c.entries, table+"\x00"+pk)
}

// Clear clears all cached entries without reallocating map buckets.
func (c *TxReadCache) Clear() {
	if c == nil || c.entries == nil {
		return
	}
	clear(c.entries)
}

// Reset clears the cache for pool recycling, shrinking the underlying map if it exceeded 128 entries per [RULE:BoundedBuffers].
func (c *TxReadCache) Reset() {
	if c == nil || c.entries == nil {
		return
	}
	if len(c.entries) > 128 {
		c.entries = make(map[string]CachedRowEntry, 16)
	} else {
		clear(c.entries)
	}
}

// Len returns the number of cached entries.
func (c *TxReadCache) Len() int {
	if c == nil || c.entries == nil {
		return 0
	}
	return len(c.entries)
}

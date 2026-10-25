package memengine

import (
	"sync"
	"sync/atomic"

	"lionsql/pkg/storage"
)

type IndexEntry struct {
	Key           storage.Value     `json:"key"`
	PKs           []string          `json:"pks"`
	IncludeValues [][]storage.Value `json:"include_values,omitempty"`
}

// IndexData holds sorted entries for a secondary index using a concurrent SkipList.
type IndexData struct {
	Schema  storage.IndexSchema `json:"schema"`
	Entries []IndexEntry        `json:"entries,omitempty"` // For JSON/MessagePack persistence
	sl      *SkipList           `json:"-"`
}

func (idx *IndexData) getSkipList() *SkipList {
	if idx.sl == nil {
		idx.sl = NewSkipList(idx.Schema.Unique)
		if len(idx.Entries) > 0 {
			_ = idx.sl.FromEntries(idx.Entries)
		}
	}
	return idx.sl
}

func (idx *IndexData) Insert(key storage.Value, pk string, incVals ...[]storage.Value) error {
	return idx.getSkipList().Insert(key, pk, incVals...)
}

func (idx *IndexData) Delete(key storage.Value, pk string) {
	idx.getSkipList().Delete(key, pk)
}

func (idx *IndexData) ScanRange(low, high *storage.Value) []string {
	return idx.getSkipList().ScanRange(low, high)
}

func (idx *IndexData) ScanRangeEntries(low, high *storage.Value) []SkipListEntry {
	return idx.getSkipList().ScanRangeEntries(low, high)
}

// SyncEntries exports the in-memory SkipList into the Entries slice for disk snapshot persistence.
func (idx *IndexData) SyncEntries() {
	if idx.sl != nil {
		idx.Entries = idx.sl.ToEntries()
	}
}

const (
	numTableShards     = 256
	numBucketsPerShard = 64 // 256 shards * 64 buckets = 16,384 lock-free hash buckets per table
)

// RowVersion represents an immutable version node in a lock-free snapshot version chain (Hekaton MVCC model).
type RowVersion struct {
	ver      int64
	commitTS int64
	row      storage.Row
	rawTuple []byte
	deleted  bool
	prev     *RowVersion
}

// RowHeader points to the latest active version node via an atomic pointer.
type RowHeader struct {
	head atomic.Pointer[RowVersion]
}

// rowNode is a singly-linked node in a lock-free bucket chain.
type rowNode struct {
	key    string
	header RowHeader
	next   *rowNode
}

type tableShard struct {
	mu      sync.Mutex
	buckets [numBucketsPerShard]atomic.Pointer[rowNode]
	_pad    [32]byte
}

func (sh *tableShard) getNode(key string) *rowNode {
	bIdx := hashBucket(key)
	for n := sh.buckets[bIdx].Load(); n != nil; n = n.next {
		if n.key == key {
			return n
		}
	}
	return nil
}

func (sh *tableShard) insertNodeLocked(key string, ver *RowVersion) *rowNode {
	bIdx := hashBucket(key)
	oldHead := sh.buckets[bIdx].Load()
	newNode := &rowNode{
		key:  key,
		next: oldHead,
	}
	newNode.header.head.Store(ver)
	sh.buckets[bIdx].Store(newNode)
	return newNode
}

func (sh *tableShard) deleteNodeLocked(key string, delVer *RowVersion) {
	bIdx := hashBucket(key)
	oldHead := sh.buckets[bIdx].Load()
	var target *rowNode
	for n := oldHead; n != nil; n = n.next {
		if n.key == key {
			target = n
			break
		}
	}
	if target == nil {
		return
	}
	target.header.head.Store(delVer)

	if oldHead == target {
		sh.buckets[bIdx].Store(target.next)
		return
	}
	var newHead *rowNode
	var tail *rowNode
	for n := oldHead; n != nil && n != target; n = n.next {
		cp := &rowNode{key: n.key, header: n.header}
		if newHead == nil {
			newHead = cp
			tail = cp
		} else {
			tail.next = cp
			tail = cp
		}
	}
	if tail != nil {
		tail.next = target.next
	}
	sh.buckets[bIdx].Store(newHead)
}

// TableData stores rows in memory for a single table with fine-grained sharded OCC version tracking and secondary indexes.
type TableData struct {
	mu         sync.RWMutex                   // Table-level RW lock for DDL (DROP, TRUNCATE) and table-wide schema changes
	partMu     sync.RWMutex                   // Lock for partition metadata
	shards     [numTableShards]tableShard
	initOnce   sync.Once
	shardsInit bool
	Schema     storage.TableSchema            `json:"schema"`
	Rows       map[string]storage.Row         `json:"rows,omitempty"`     // For snapshot persistence & backward compatibility
	Versions   map[string]int64               `json:"versions,omitempty"` // For snapshot persistence
	Keys       []string                       `json:"keys,omitempty"`
	Indexes    map[string]*IndexData          `json:"indexes,omitempty"`
	Partitions map[string]map[string]struct{} `json:"partitions,omitempty"`
}

func hashKey(key string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return h
}

func hashShard(key string) int {
	return int(hashKey(key) & (numTableShards - 1))
}

func hashBucket(key string) int {
	return int((hashKey(key) >> 8) & (numBucketsPerShard - 1))
}

func (td *TableData) initShards() {
	td.initOnce.Do(td.initShardsInternal)
}

func (td *TableData) initShardsInternal() {
	if len(td.Rows) > 0 {
		for k, r := range td.Rows {
			sIdx := hashShard(k)
			ver := int64(1)
			if td.Versions != nil && td.Versions[k] > 0 {
				ver = td.Versions[k]
			}
			sh := &td.shards[sIdx]
			sh.insertNodeLocked(k, &RowVersion{ver: ver, row: r})
		}
	}
	td.shardsInit = true
}

func (td *TableData) ensureShardsInit() {
	td.initOnce.Do(td.initShardsInternal)
}

func (td *TableData) resolveRow(v *RowVersion) storage.Row {
	if len(v.row.Values) > 0 {
		return v.row
	}
	if len(v.rawTuple) > 0 {
		if r, err := storage.DecodeTupleToRow(&td.Schema, v.rawTuple); err == nil {
			return r
		}
	}
	return v.row
}

func (td *TableData) getRowAndVer(pk string) (storage.Row, int64, bool) {
	td.ensureShardsInit()
	sIdx := hashShard(pk)
	n := td.shards[sIdx].getNode(pk)
	if n == nil {
		return storage.Row{}, 0, false
	}
	v := n.header.head.Load()
	if v == nil || v.deleted {
		return storage.Row{}, 0, false
	}
	return td.resolveRow(v), v.ver, true
}

func (td *TableData) hasKey(pk string) bool {
	td.ensureShardsInit()
	sIdx := hashShard(pk)
	n := td.shards[sIdx].getNode(pk)
	if n == nil {
		return false
	}
	v := n.header.head.Load()
	return v != nil && !v.deleted
}

func (td *TableData) putRowAndVer(pk string, row storage.Row, ver int64) {
	td.ensureShardsInit()
	sIdx := hashShard(pk)
	sh := &td.shards[sIdx]
	sh.mu.Lock()
	defer sh.mu.Unlock()
	n := sh.getNode(pk)
	newVer := &RowVersion{ver: ver, row: row}
	if n != nil {
		oldV := n.header.head.Load()
		if oldV != nil {
			newVer.prev = oldV
			oldV.prev = nil
		}
		n.header.head.Store(newVer)
	} else {
		sh.insertNodeLocked(pk, newVer)
	}
}

func (td *TableData) deleteRowAndVer(pk string) (storage.Row, bool) {
	td.ensureShardsInit()
	sIdx := hashShard(pk)
	sh := &td.shards[sIdx]
	sh.mu.Lock()
	defer sh.mu.Unlock()
	n := sh.getNode(pk)
	if n == nil {
		return storage.Row{}, false
	}
	oldV := n.header.head.Load()
	if oldV == nil || oldV.deleted {
		return storage.Row{}, false
	}
	delVer := &RowVersion{ver: oldV.ver + 1, deleted: true, prev: oldV}
	oldV.prev = nil
	sh.deleteNodeLocked(pk, delVer)
	return oldV.row, true
}

func (td *TableData) forEachRow(fn func(pk string, row storage.Row) bool) {
	td.ensureShardsInit()
	for i := 0; i < numTableShards; i++ {
		sh := &td.shards[i]
		for b := 0; b < numBucketsPerShard; b++ {
			for n := sh.buckets[b].Load(); n != nil; n = n.next {
				v := n.header.head.Load()
				if v != nil && !v.deleted {
					if !fn(n.key, td.resolveRow(v)) {
						return
					}
				}
			}
		}
	}
}

func (td *TableData) rowCount() int64 {
	td.ensureShardsInit()
	var total int64
	for i := 0; i < numTableShards; i++ {
		sh := &td.shards[i]
		for b := 0; b < numBucketsPerShard; b++ {
			for n := sh.buckets[b].Load(); n != nil; n = n.next {
				v := n.header.head.Load()
				if v != nil && !v.deleted {
					total++
				}
			}
		}
	}
	return total
}

func (td *TableData) collectKeys(filter func(pk string) bool) []string {
	td.ensureShardsInit()
	var keys []string
	for i := 0; i < numTableShards; i++ {
		sh := &td.shards[i]
		for b := 0; b < numBucketsPerShard; b++ {
			for n := sh.buckets[b].Load(); n != nil; n = n.next {
				v := n.header.head.Load()
				if v != nil && !v.deleted {
					if filter == nil || filter(n.key) {
						keys = append(keys, n.key)
					}
				}
			}
		}
	}
	return keys
}

func (td *TableData) syncShardsToMapsLocked() {
	td.mu.Lock()
	defer td.mu.Unlock()
	td.ensureShardsInit()
	td.Rows = make(map[string]storage.Row)
	td.Versions = make(map[string]int64)
	for i := 0; i < numTableShards; i++ {
		sh := &td.shards[i]
		for b := 0; b < numBucketsPerShard; b++ {
			for n := sh.buckets[b].Load(); n != nil; n = n.next {
				v := n.header.head.Load()
				if v != nil && !v.deleted {
					td.Rows[n.key] = td.resolveRow(v)
					td.Versions[n.key] = v.ver
				}
			}
		}
	}
}

func (td *TableData) clearShardsSyncMapsLocked() {
	td.mu.Lock()
	td.Rows = nil
	td.Versions = nil
	td.mu.Unlock()
}

func (td *TableData) truncateShardsLocked() {
	for i := 0; i < numTableShards; i++ {
		sh := &td.shards[i]
		sh.mu.Lock()
		for b := 0; b < numBucketsPerShard; b++ {
			sh.buckets[b].Store(nil)
		}
		sh.mu.Unlock()
	}
	td.Rows = make(map[string]storage.Row)
	td.Versions = make(map[string]int64)
	td.Keys = make([]string, 0)
}

func cloneRow(r storage.Row) storage.Row {
	vals := make([]storage.Value, len(r.Values))
	copy(vals, r.Values)
	return storage.Row{Values: vals}
}

func (td *TableData) addPartitionKey(partID, pk string) {
	if td.Partitions == nil {
		td.Partitions = make(map[string]map[string]struct{})
	}
	set, ok := td.Partitions[partID]
	if !ok {
		set = make(map[string]struct{})
		td.Partitions[partID] = set
	}
	set[pk] = struct{}{}
}

func (td *TableData) removePartitionKey(partID, pk string) {
	if td.Partitions == nil {
		return
	}
	if set, ok := td.Partitions[partID]; ok {
		delete(set, pk)
		if len(set) == 0 {
			delete(td.Partitions, partID)
		}
	}
}

func (td *TableData) addColumnValues(val storage.Value) {
	td.ensureShardsInit()
	for i := 0; i < numTableShards; i++ {
		sh := &td.shards[i]
		sh.mu.Lock()
		for b := 0; b < numBucketsPerShard; b++ {
			for n := sh.buckets[b].Load(); n != nil; n = n.next {
				v := n.header.head.Load()
				if v != nil {
					newRow := storage.Row{Values: append(v.row.Values, val)}
					newVer := &RowVersion{ver: v.ver, row: newRow, deleted: v.deleted, prev: v}
					v.prev = nil
					n.header.head.Store(newVer)
				}
			}
		}
		sh.mu.Unlock()
	}
}

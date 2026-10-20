package memengine

import (
	"sync"

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

const numTableShards = 64

type tableShard struct {
	mu       sync.RWMutex
	rows     map[string]storage.Row
	versions map[string]int64
	_pad     [32]byte
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

func hashShard(key string) int {
	var h uint32 = 2166136261
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return int(h & (numTableShards - 1))
}

func (td *TableData) initShards() {
	td.initOnce.Do(td.initShardsInternal)
}

func (td *TableData) initShardsInternal() {
	for i := 0; i < numTableShards; i++ {
		if td.shards[i].rows == nil {
			td.shards[i].rows = make(map[string]storage.Row)
		}
		if td.shards[i].versions == nil {
			td.shards[i].versions = make(map[string]int64)
		}
	}
	if len(td.Rows) > 0 {
		for k, r := range td.Rows {
			sIdx := hashShard(k)
			td.shards[sIdx].rows[k] = r
			if td.Versions != nil {
				td.shards[sIdx].versions[k] = td.Versions[k]
			} else {
				td.shards[sIdx].versions[k] = 1
			}
		}
	}
	td.shardsInit = true
}

func (td *TableData) ensureShardsInit() {
	td.initOnce.Do(td.initShardsInternal)
}

func (td *TableData) getRowAndVer(pk string) (storage.Row, int64, bool) {
	td.ensureShardsInit()
	sIdx := hashShard(pk)
	sh := &td.shards[sIdx]
	sh.mu.RLock()
	r, ok := sh.rows[pk]
	v := sh.versions[pk]
	sh.mu.RUnlock()
	return r, v, ok
}

func (td *TableData) hasKey(pk string) bool {
	td.ensureShardsInit()
	sIdx := hashShard(pk)
	sh := &td.shards[sIdx]
	sh.mu.RLock()
	_, ok := sh.rows[pk]
	sh.mu.RUnlock()
	return ok
}

func (td *TableData) putRowAndVer(pk string, row storage.Row, ver int64) {
	td.ensureShardsInit()
	sIdx := hashShard(pk)
	sh := &td.shards[sIdx]
	sh.mu.Lock()
	sh.rows[pk] = row
	sh.versions[pk] = ver
	sh.mu.Unlock()
}

func (td *TableData) deleteRowAndVer(pk string) (storage.Row, bool) {
	td.ensureShardsInit()
	sIdx := hashShard(pk)
	sh := &td.shards[sIdx]
	sh.mu.Lock()
	oldRow, hadOld := sh.rows[pk]
	delete(sh.rows, pk)
	delete(sh.versions, pk)
	sh.mu.Unlock()
	return oldRow, hadOld
}

func (td *TableData) forEachRow(fn func(pk string, row storage.Row) bool) {
	td.ensureShardsInit()
	for i := 0; i < numTableShards; i++ {
		sh := &td.shards[i]
		sh.mu.RLock()
		for k, r := range sh.rows {
			if !fn(k, r) {
				sh.mu.RUnlock()
				return
			}
		}
		sh.mu.RUnlock()
	}
}

func (td *TableData) rowCount() int64 {
	td.ensureShardsInit()
	var total int64
	for i := 0; i < numTableShards; i++ {
		sh := &td.shards[i]
		sh.mu.RLock()
		total += int64(len(sh.rows))
		sh.mu.RUnlock()
	}
	return total
}

func (td *TableData) collectKeys(filter func(pk string) bool) []string {
	td.ensureShardsInit()
	var keys []string
	for i := 0; i < numTableShards; i++ {
		sh := &td.shards[i]
		sh.mu.RLock()
		for k := range sh.rows {
			if filter == nil || filter(k) {
				keys = append(keys, k)
			}
		}
		sh.mu.RUnlock()
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
		sh.mu.RLock()
		for k, r := range sh.rows {
			td.Rows[k] = r
			td.Versions[k] = sh.versions[k]
		}
		sh.mu.RUnlock()
	}
}

func (td *TableData) truncateShardsLocked() {
	for i := 0; i < numTableShards; i++ {
		sh := &td.shards[i]
		sh.mu.Lock()
		sh.rows = make(map[string]storage.Row)
		sh.versions = make(map[string]int64)
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
		for k, r := range sh.rows {
			sh.rows[k] = storage.Row{Values: append(r.Values, val)}
		}
		sh.mu.Unlock()
	}
}

// Engine implements storage.Engine in pure Go with optional atomic disk snapshot persistence and WAL (AOF) logging.
// Options defines Redis-style AOF and RDB persistence configuration.

package memengine

import (
	"log"
	"sort"
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
	once    sync.Once           `json:"-"`
}

func (idx *IndexData) getSkipList() *SkipList {
	idx.once.Do(func() {
		if idx.sl == nil {
			sl := NewSkipList(idx.Schema.Unique)
			if len(idx.Entries) > 0 {
				_ = sl.FromEntries(idx.Entries)
			}
			idx.sl = sl
		}
	})
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

const numTableShards = 256

type shardEntry struct {
	ver     int64
	row     storage.Row // active when mode == OCCModeRaw
	flatBuf []byte      // active when mode == OCCModeFlat (zero GC pointers)
}

func (e shardEntry) materialize(td *TableData) (storage.Row, error) {
	if td.mode == OCCModeFlat && len(e.flatBuf) > 0 && len(e.row.Values) == 0 {
		return td.layout.DecodeFlatToRow(&td.Schema, e.flatBuf)
	}
	return e.row, nil
}

type tableShard struct {
	mu      sync.RWMutex
	entries map[string]shardEntry
	_pad    [32]byte
}

// TableData stores rows in memory for a single table with fine-grained sharded OCC version tracking and secondary indexes.
type TableData struct {
	mu         sync.RWMutex                   // Table-level RW lock for DDL (DROP, TRUNCATE) and table-wide schema changes
	partMu     sync.RWMutex                   // Lock for partition metadata
	shards     [numTableShards]tableShard
	initOnce   sync.Once
	shardsInit bool
	mode       OCCMode                        // OCCModeRaw (default: 0) or OCCModeFlat (1)
	layout     *FlatRecordLayout              // Precomputed layout when mode == OCCModeFlat
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
		if td.shards[i].entries == nil {
			td.shards[i].entries = make(map[string]shardEntry)
		}
	}
	if len(td.Rows) > 0 {
		for k, r := range td.Rows {
			sIdx := hashShard(k)
			ver := int64(1)
			if td.Versions != nil && td.Versions[k] > 0 {
				ver = td.Versions[k]
			}
			td.shards[sIdx].entries[k] = shardEntry{ver: ver, row: r}
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
	entry, ok := sh.entries[pk]
	sh.mu.RUnlock()
	if !ok {
		return storage.Row{}, 0, false
	}
	r, err := entry.materialize(td)
	if err != nil {
		// [RULE:ExplicitError] Log corruption and fail safe rather than silently returning empty row
		log.Printf("[memengine] flat record materialize error on table %q pk %q: %v", td.Schema.Name, pk, err)
		return storage.Row{}, 0, false
	}
	return r, entry.ver, true
}

func (td *TableData) hasKey(pk string) bool {
	td.ensureShardsInit()
	sIdx := hashShard(pk)
	sh := &td.shards[sIdx]
	sh.mu.RLock()
	_, ok := sh.entries[pk]
	sh.mu.RUnlock()
	return ok
}

func (td *TableData) putRowAndVer(pk string, row storage.Row, ver int64) {
	td.ensureShardsInit()
	sIdx := hashShard(pk)
	sh := &td.shards[sIdx]
	sh.mu.Lock()
	if td.mode == OCCModeFlat && td.layout != nil {
		flatBuf, err := td.layout.EncodeRowToFlat(&td.Schema, row)
		if err == nil {
			sh.entries[pk] = shardEntry{ver: ver, flatBuf: flatBuf}
			sh.mu.Unlock()
			return
		}
		log.Printf("[memengine] flat record encode fallback on table %q pk %q: %v", td.Schema.Name, pk, err)
	}
	sh.entries[pk] = shardEntry{ver: ver, row: row}
	sh.mu.Unlock()
}

func (td *TableData) deleteRowAndVer(pk string) (storage.Row, bool) {
	td.ensureShardsInit()
	sIdx := hashShard(pk)
	sh := &td.shards[sIdx]
	sh.mu.Lock()
	entry, hadOld := sh.entries[pk]
	if hadOld {
		delete(sh.entries, pk)
	}
	sh.mu.Unlock()
	if hadOld {
		r, err := entry.materialize(td)
		if err == nil {
			return r, true
		}
		log.Printf("[memengine] flat record delete materialize error on table %q pk %q: %v", td.Schema.Name, pk, err)
	}
	return entry.row, hadOld
}

func (td *TableData) forEachRow(fn func(pk string, row storage.Row) bool) {
	td.ensureShardsInit()
	for i := 0; i < numTableShards; i++ {
		sh := &td.shards[i]
		sh.mu.RLock()
		for k, entry := range sh.entries {
			r, err := entry.materialize(td)
			if err != nil {
				continue
			}
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
		total += int64(len(sh.entries))
		sh.mu.RUnlock()
	}
	return total
}

func (td *TableData) snapshotRowsAndKeys(filter func(pk string) bool) ([]string, []storage.Row) {
	td.ensureShardsInit()
	// Strict canonical order (0 to 255) to prevent deadlocks ([RULE:LockHierarchy])
	for i := 0; i < numTableShards; i++ {
		td.shards[i].mu.RLock()
	}
	defer func() {
		for i := numTableShards - 1; i >= 0; i-- {
			td.shards[i].mu.RUnlock()
		}
	}()

	var total int
	for i := 0; i < numTableShards; i++ {
		total += len(td.shards[i].entries)
	}

	type keyRowItem struct {
		key string
		row storage.Row
	}
	items := make([]keyRowItem, 0, total)
	for i := 0; i < numTableShards; i++ {
		for k, entry := range td.shards[i].entries {
			if filter == nil || filter(k) {
				r, err := entry.materialize(td)
				if err != nil {
					continue
				}
				items = append(items, keyRowItem{key: k, row: r})
			}
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].key < items[j].key
	})

	keys := make([]string, len(items))
	rows := make([]storage.Row, len(items))
	for i, item := range items {
		keys[i] = item.key
		rows[i] = item.row
	}
	return keys, rows
}

func (td *TableData) collectKeys(filter func(pk string) bool) []string {
	td.ensureShardsInit()
	var keys []string
	for i := 0; i < numTableShards; i++ {
		sh := &td.shards[i]
		sh.mu.RLock()
		for k := range sh.entries {
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
		for k, entry := range sh.entries {
			row, err := entry.materialize(td)
			if err != nil {
				row = entry.row
			}
			td.Rows[k] = row
			td.Versions[k] = entry.ver
		}
		sh.mu.RUnlock()
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
		sh.entries = make(map[string]shardEntry)
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
	td.partMu.Lock()
	defer td.partMu.Unlock()

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
	td.partMu.Lock()
	defer td.partMu.Unlock()

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

	var newLayout *FlatRecordLayout
	var oldSchema storage.TableSchema
	if td.mode == OCCModeFlat {
		newLayout = BuildFlatRecordLayout(&td.Schema)
		oldSchema = td.Schema
		if len(oldSchema.Columns) > 1 {
			oldSchema.Columns = oldSchema.Columns[:len(oldSchema.Columns)-1]
		}
	}

	for i := 0; i < numTableShards; i++ {
		sh := &td.shards[i]
		sh.mu.Lock()
		for k, entry := range sh.entries {
			if td.mode == OCCModeFlat {
				var origRow storage.Row
				var err error
				if len(entry.flatBuf) > 0 && len(entry.row.Values) == 0 && td.layout != nil {
					origRow, err = td.layout.DecodeFlatToRow(&oldSchema, entry.flatBuf)
				} else {
					origRow = entry.row
				}
				if err != nil {
					origRow = entry.row
				}
				newRow := storage.Row{Values: append(origRow.Values, val)}
				newBuf, err := newLayout.EncodeRowToFlat(&td.Schema, newRow)
				if err != nil {
					sh.entries[k] = shardEntry{
						ver: entry.ver,
						row: newRow,
					}
				} else {
					sh.entries[k] = shardEntry{
						ver:     entry.ver,
						flatBuf: newBuf,
					}
				}
			} else {
				sh.entries[k] = shardEntry{
					ver: entry.ver,
					row: storage.Row{Values: append(entry.row.Values, val)},
				}
			}
		}
		sh.mu.Unlock()
	}

	if newLayout != nil {
		td.layout = newLayout
	}
}

// Engine implements storage.Engine in pure Go with optional atomic disk snapshot persistence and WAL (AOF) logging.
// Options defines Redis-style AOF and RDB persistence configuration.

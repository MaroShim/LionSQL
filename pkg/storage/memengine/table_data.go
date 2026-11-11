package memengine

import (
	"fmt"
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
	row     storage.Row // active when mode == MemoryPerformance
	flatBuf []byte      // active when mode == MemoryCompact (zero GC pointers)
}

func (e shardEntry) materialize(td *TableData) (storage.Row, error) {
	return e.materializeWithArena(td, nil)
}

func (e shardEntry) materializeWithArena(td *TableData, arena *storage.Arena) (storage.Row, error) {
	if td.mode == MemoryCompact && len(e.flatBuf) > 0 && len(e.row.Values) == 0 {
		return td.layout.DecodeFlatToRowWithArena(&td.Schema, e.flatBuf, arena)
	}
	return e.row, nil
}

func (e shardEntry) materializeWithLayout(mode MemoryMode, schema *storage.TableSchema, layout *FlatRecordLayout, arena *storage.Arena) (storage.Row, error) {
	if mode == MemoryCompact && len(e.flatBuf) > 0 && len(e.row.Values) == 0 && layout != nil && schema != nil {
		return layout.DecodeFlatToRowWithArena(schema, e.flatBuf, arena)
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
	mode       MemoryMode                     // MemoryPerformance (default: 0) or MemoryCompact (1)
	layout     *FlatRecordLayout              // Precomputed layout when mode == MemoryCompact
	snapSchema *storage.TableSchema           // Immutable schema snapshot updated alongside layout under mu.Lock
	Schema     storage.TableSchema            `json:"schema"`
	Rows       map[string]storage.Row         `json:"rows,omitempty"`     // For snapshot persistence & backward compatibility
	Versions   map[string]int64               `json:"versions,omitempty"` // For snapshot persistence
	Keys       []string                       `json:"keys,omitempty"`
	Indexes    map[string]*IndexData          `json:"indexes,omitempty"`
	Partitions map[string]map[string]struct{} `json:"partitions,omitempty"`
}

func (td *TableData) getImmutableSchemaLocked() *storage.TableSchema {
	if td.snapSchema != nil {
		return td.snapSchema
	}
	cp := td.Schema.Clone()
	return &cp
}

func (td *TableData) refreshSnapSchemaLocked() {
	cp := td.Schema.Clone()
	td.snapSchema = &cp
}

// SnapshotSchema returns an immutable snapshot of the table schema under RLock ([RULE:ConcurrencySafety]).
func (td *TableData) SnapshotSchema() *storage.TableSchema {
	td.mu.RLock()
	defer td.mu.RUnlock()
	return td.getImmutableSchemaLocked()
}

func (td *TableData) snapshotSchemaAndLayout() (*storage.TableSchema, *FlatRecordLayout) {
	td.mu.RLock()
	defer td.mu.RUnlock()
	return td.getImmutableSchemaLocked(), td.layout
}

func (td *TableData) getEntryWithLayout(pk string) (shardEntry, *storage.TableSchema, *FlatRecordLayout, bool) {
	td.ensureShardsInit()
	td.mu.RLock()
	schemaSnap := td.getImmutableSchemaLocked()
	layoutSnap := td.layout
	sIdx := hashShard(pk)
	sh := &td.shards[sIdx]
	sh.mu.RLock()
	entry, ok := sh.entries[pk]
	sh.mu.RUnlock()
	td.mu.RUnlock()
	return entry, schemaSnap, layoutSnap, ok
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

func (td *TableData) buildShardEntry(row storage.Row, ver int64) shardEntry {
	if td.mode == MemoryCompact && td.layout != nil {
		flatBuf, err := td.layout.EncodeRowToFlat(&td.Schema, row)
		if err == nil {
			return shardEntry{ver: ver, flatBuf: flatBuf}
		}
		log.Printf("[memengine] flat record encode fallback on table %q: %v", td.Schema.Name, err)
	}
	return shardEntry{ver: ver, row: row}
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
			td.shards[sIdx].entries[k] = td.buildShardEntry(r, ver)
		}
	}
	td.shardsInit = true
}

func (td *TableData) ensureShardsInit() {
	td.initOnce.Do(td.initShardsInternal)
}

func (td *TableData) getEntryAndVer(pk string) (shardEntry, bool) {
	td.ensureShardsInit()
	sIdx := hashShard(pk)
	sh := &td.shards[sIdx]
	sh.mu.RLock()
	entry, ok := sh.entries[pk]
	sh.mu.RUnlock()
	return entry, ok
}

func (td *TableData) getRowAndVer(pk string) (storage.Row, int64, bool) {
	entry, schemaSnap, layoutSnap, ok := td.getEntryWithLayout(pk)
	if !ok {
		return storage.Row{}, 0, false
	}
	r, err := entry.materializeWithLayout(td.mode, schemaSnap, layoutSnap, nil)
	if err != nil {
		// [RULE:ExplicitError] Log corruption and fail safe rather than silently returning empty row
		log.Printf("[memengine] flat record materialize error on table %q pk %q: %v", schemaSnap.Name, pk, err)
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
	entry := td.buildShardEntry(row, ver)
	sIdx := hashShard(pk)
	sh := &td.shards[sIdx]
	sh.mu.Lock()
	sh.entries[pk] = entry
	sh.mu.Unlock()
}

func (td *TableData) deleteRowAndVerLocked(pk string) (storage.Row, bool) {
	td.ensureShardsInit()
	schemaSnap := td.getImmutableSchemaLocked()
	layoutSnap := td.layout
	sIdx := hashShard(pk)
	sh := &td.shards[sIdx]
	sh.mu.Lock()
	entry, hadOld := sh.entries[pk]
	if hadOld {
		delete(sh.entries, pk)
	}
	sh.mu.Unlock()
	if hadOld {
		r, err := entry.materializeWithLayout(td.mode, schemaSnap, layoutSnap, nil)
		if err == nil {
			return r, true
		}
		log.Printf("[memengine] flat record delete materialize error on table %q pk %q: %v", schemaSnap.Name, pk, err)
	}
	return entry.row, hadOld
}

func (td *TableData) deleteRowAndVer(pk string) (storage.Row, bool) {
	td.mu.RLock()
	defer td.mu.RUnlock()
	return td.deleteRowAndVerLocked(pk)
}

func (td *TableData) forEachRow(fn func(pk string, row storage.Row) bool) {
	td.ensureShardsInit()
	td.mu.RLock()
	defer td.mu.RUnlock()
	schemaSnap := td.getImmutableSchemaLocked()
	layoutSnap := td.layout
	for i := 0; i < numTableShards; i++ {
		sh := &td.shards[i]
		sh.mu.RLock()
		for k, entry := range sh.entries {
			r, err := entry.materializeWithLayout(td.mode, schemaSnap, layoutSnap, nil)
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

func (td *TableData) snapshotEntriesAndKeys(filter func(pk string) bool) ([]string, []shardEntry) {
	keys, entries, _, _ := td.snapshotEntriesAndKeysWithLayout(filter)
	return keys, entries
}

func (td *TableData) snapshotEntriesAndKeysWithLayout(filter func(pk string) bool) ([]string, []shardEntry, *storage.TableSchema, *FlatRecordLayout) {
	td.ensureShardsInit()
	td.mu.RLock()
	defer td.mu.RUnlock()
	schemaSnap := td.getImmutableSchemaLocked()
	layoutSnap := td.layout
	// Strict canonical order (td.mu -> shards[0..255]) to prevent deadlocks ([RULE:LockHierarchy])
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

	type keyEntryItem struct {
		key   string
		entry shardEntry
	}
	items := make([]keyEntryItem, 0, total)
	for i := 0; i < numTableShards; i++ {
		for k, entry := range td.shards[i].entries {
			if filter == nil || filter(k) {
				items = append(items, keyEntryItem{key: k, entry: entry})
			}
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].key < items[j].key
	})

	keys := make([]string, len(items))
	entries := make([]shardEntry, len(items))
	for i, item := range items {
		keys[i] = item.key
		entries[i] = item.entry
	}
	return keys, entries, schemaSnap, layoutSnap
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

func (td *TableData) syncShardsToMapsLocked() error {
	td.mu.Lock()
	defer td.mu.Unlock()
	return td.syncShardsToMapsAlreadyLocked()
}

func (td *TableData) syncShardsToMapsAlreadyLocked() error {
	td.ensureShardsInit()
	td.Rows = make(map[string]storage.Row)
	td.Versions = make(map[string]int64)
	schemaSnap := td.getImmutableSchemaLocked()
	layoutSnap := td.layout
	for i := 0; i < numTableShards; i++ {
		sh := &td.shards[i]
		sh.mu.RLock()
		for k, entry := range sh.entries {
			row, err := entry.materializeWithLayout(td.mode, schemaSnap, layoutSnap, nil)
			if err != nil {
				sh.mu.RUnlock()
				td.Rows = nil
				td.Versions = nil
				return fmt.Errorf("failed to materialize flat record for table %q pk %q: %w", schemaSnap.Name, k, err)
			}
			td.Rows[k] = row
			td.Versions[k] = entry.ver
		}
		sh.mu.RUnlock()
	}
	return nil
}

func (td *TableData) clearShardsSyncMapsAlreadyLocked() {
	td.Rows = nil
	td.Versions = nil
}

func (td *TableData) clearShardsSyncMapsLocked() {
	td.mu.Lock()
	td.clearShardsSyncMapsAlreadyLocked()
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

func (td *TableData) addColumnValues(val storage.Value) error {
	td.ensureShardsInit()
	td.mu.Lock()
	defer td.mu.Unlock()

	var newLayout *FlatRecordLayout
	var oldSchema storage.TableSchema
	if td.mode == MemoryCompact {
		newLayout = BuildFlatRecordLayout(&td.Schema)
		oldSchema = td.Schema.Clone()
		if len(oldSchema.Columns) > 1 {
			oldSchema.Columns = oldSchema.Columns[:len(oldSchema.Columns)-1]
		}
	}

	var staged [numTableShards]map[string]shardEntry
	for i := 0; i < numTableShards; i++ {
		sh := &td.shards[i]
		sh.mu.RLock()
		staged[i] = make(map[string]shardEntry, len(sh.entries))
		var stageErr error
		for k, entry := range sh.entries {
			if td.mode == MemoryCompact {
				var origRow storage.Row
				if len(entry.flatBuf) > 0 && len(entry.row.Values) == 0 && td.layout != nil {
					var err error
					origRow, err = td.layout.DecodeFlatToRow(&oldSchema, entry.flatBuf)
					if err != nil {
						stageErr = err
						break
					}
				} else {
					origRow = entry.row
				}
				newVals := make([]storage.Value, len(origRow.Values)+1)
				copy(newVals, origRow.Values)
				newVals[len(origRow.Values)] = val
				newRow := storage.Row{Values: newVals}
				newBuf, err := newLayout.EncodeRowToFlat(&td.Schema, newRow)
				if err != nil {
					stageErr = err
					break
				}
				staged[i][k] = shardEntry{
					ver:     entry.ver,
					flatBuf: newBuf,
				}
			} else {
				newVals := make([]storage.Value, len(entry.row.Values)+1)
				copy(newVals, entry.row.Values)
				newVals[len(entry.row.Values)] = val
				staged[i][k] = shardEntry{
					ver: entry.ver,
					row: storage.Row{Values: newVals},
				}
			}
		}
		sh.mu.RUnlock()
		if stageErr != nil {
			return stageErr
		}
	}

	for i := 0; i < numTableShards; i++ {
		sh := &td.shards[i]
		sh.mu.Lock()
		sh.entries = staged[i]
		sh.mu.Unlock()
	}

	if newLayout != nil {
		td.layout = newLayout
	}
	td.refreshSnapSchemaLocked()
	return nil
}

// Engine implements storage.Engine in pure Go with optional atomic disk snapshot persistence and WAL (AOF) logging.
// Options defines Redis-style AOF and RDB persistence configuration.

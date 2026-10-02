package memengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"sqlion/pkg/storage"
)

// IndexEntry represents a sorted key entry in a secondary index.
type IndexEntry struct {
	Key storage.Value `json:"key"`
	PKs []string      `json:"pks"`
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

func (idx *IndexData) Insert(key storage.Value, pk string) error {
	return idx.getSkipList().Insert(key, pk)
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

// TableData stores rows in memory for a single table with OCC version tracking and secondary indexes.
type TableData struct {
	mu       sync.RWMutex
	Schema   storage.TableSchema       `json:"schema"`
	Rows     map[string]storage.Record `json:"rows"`
	Versions map[string]int64               `json:"versions"`
	Keys     []string                       `json:"keys"`
	Indexes  map[string]*IndexData          `json:"indexes,omitempty"`
	Partitions map[string]map[string]struct{} `json:"partitions,omitempty"`
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

// Engine implements storage.Engine in pure Go with optional atomic disk snapshot persistence and WAL (AOF) logging.
type Engine struct {
	mu           sync.RWMutex
	dataDir      string
	tables       map[string]*TableData
	wal          *WALWriter
	fsyncPolicy  FsyncPolicy
	nextLSN      atomic.Int64
	aofThreshold int64 // Size threshold in bytes to trigger AOF rewrite compaction
	closed       bool
}

// New creates a new in-memory storage engine.
func New() *Engine {
	return &Engine{
		tables:       make(map[string]*TableData),
		fsyncPolicy:  FsyncEverySec,
		aofThreshold: 10 * 1024 * 1024, // 10MB default compaction threshold
	}
}

// Type returns the human-readable storage engine backend name.
func (e *Engine) Type() string {
	return "In-Memory OCC"
}

// SetFsyncPolicy sets the disk synchronization policy for WAL/AOF.
func (e *Engine) SetFsyncPolicy(policy FsyncPolicy) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fsyncPolicy = policy
	if e.wal != nil {
		e.wal.policy = policy
	}
}

// SetAOFThreshold sets the byte threshold for automatic AOF rewrite compaction.
func (e *Engine) SetAOFThreshold(bytes int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.aofThreshold = bytes
}

func (e *Engine) Open(dir string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.closed = false
	if dir != "" {
		e.dataDir = filepath.Clean(dir)
		if err := os.MkdirAll(e.dataDir, 0755); err != nil {
			return fmt.Errorf("failed to create data directory: %w", err)
		}

		// 1. Load baseline snapshot (RDB) if present
		e.loadDiskLocked()

		// 2. Replay subsequent AOF log records if present
		aofPath := filepath.Join(e.dataDir, "wal.aof")
		maxLSN, err := ReplayWAL(aofPath, func(rec WALRecord) error {
			return e.applyWALRecordLocked(rec)
		})
		if err != nil {
			return fmt.Errorf("WAL replay error: %w", err)
		}
		e.nextLSN.Store(maxLSN)

		// 3. Open WAL writer for future append-only logging
		walWriter, err := NewWALWriter(aofPath, e.fsyncPolicy)
		if err != nil {
			return fmt.Errorf("failed to initialize WAL writer: %w", err)
		}
		e.wal = walWriter

		// If AOF is already larger than threshold on startup, compact it
		if e.aofThreshold > 0 && e.wal.Size() > e.aofThreshold {
			_ = e.rewriteAOFLocked()
		}
	}
	return nil
}

// ApplyWALRecord applies a single committed WAL record directly to the in-memory engine.
func (e *Engine) ApplyWALRecord(rec WALRecord) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.applyWALRecordLocked(rec)
}

func (e *Engine) applyWALRecordLocked(rec WALRecord) error {
	for _, op := range rec.Ops {
		switch op.Type {
		case OpCreateTable:
			if op.Schema == nil {
				continue
			}
			e.tables[op.Schema.Name] = &TableData{
				Schema:     *op.Schema,
				Rows:       make(map[string]storage.Record),
				Versions:   make(map[string]int64),
				Keys:       make([]string, 0),
				Indexes:    make(map[string]*IndexData),
				Partitions: make(map[string]map[string]struct{}),
			}
		case OpDropTable:
			delete(e.tables, op.Table)
		case OpTruncateTable:
			if td, exists := e.tables[op.Table]; exists {
				td.Rows = make(map[string]storage.Record)
				td.Versions = make(map[string]int64)
				td.Keys = make([]string, 0)
				td.Partitions = make(map[string]map[string]struct{})
				for _, idx := range td.Indexes {
					idx.sl = NewSkipList(idx.Schema.Unique)
					idx.Entries = nil
				}
			}
		case OpDropPartition:
			if td, exists := e.tables[op.Table]; exists {
				if pksSet, ok := td.Partitions[op.Key]; ok {
					for pk := range pksSet {
						oldRec, hadOld := td.Rows[pk]
						delete(td.Rows, pk)
						delete(td.Versions, pk)
						if hadOld {
							for _, idxData := range td.Indexes {
								oldKey := extractIndexKey(oldRec, idxData.Schema.Columns)
								idxData.Delete(oldKey, pk)
							}
						}
					}
					delete(td.Partitions, op.Key)
					newKeys := make([]string, 0, len(td.Rows))
					for k := range td.Rows {
						newKeys = append(newKeys, k)
					}
					td.Keys = newKeys
				}
			}
		case OpCreateIndex:
			if op.IndexSchema == nil {
				continue
			}
			td, exists := e.tables[op.IndexSchema.TableName]
			if !exists {
				continue
			}
			idxData := &IndexData{
				Schema:  *op.IndexSchema,
				Entries: make([]IndexEntry, 0),
			}
			for pk, rowRec := range td.Rows {
				keyVal := extractIndexKey(rowRec, op.IndexSchema.Columns)
				_ = idxData.Insert(keyVal, pk)
			}
			if td.Indexes == nil {
				td.Indexes = make(map[string]*IndexData)
			}
			td.Indexes[op.IndexSchema.Name] = idxData
			_, _, idxFound := td.Schema.FindIndex(op.IndexSchema.Name)
			if !idxFound {
				td.Schema.Indexes = append(td.Schema.Indexes, *op.IndexSchema)
			}
		case OpDropIndex:
			td, exists := e.tables[op.Table]
			if !exists {
				continue
			}
			delete(td.Indexes, op.IndexName)
			_, idxPos, idxFound := td.Schema.FindIndex(op.IndexName)
			if idxFound {
				td.Schema.Indexes = append(td.Schema.Indexes[:idxPos], td.Schema.Indexes[idxPos+1:]...)
			}
		case OpInsert:
			td, exists := e.tables[op.Table]
			if !exists {
				continue
			}
			if _, hasKey := td.Rows[op.Key]; !hasKey {
				td.Keys = append(td.Keys, op.Key)
			}
			td.Rows[op.Key] = op.Record
			td.Versions[op.Key] = 1
			if td.Schema.IsPartitioned() {
				if pKey, err := td.Schema.ComputePartitionKey(op.Record); err == nil {
					td.addPartitionKey(pKey, op.Key)
				}
			}
			for _, idxData := range td.Indexes {
				newKey := extractIndexKey(op.Record, idxData.Schema.Columns)
				_ = idxData.Insert(newKey, op.Key)
			}
		case OpUpdate:
			td, exists := e.tables[op.Table]
			if !exists {
				continue
			}
			oldRec, hadOld := td.Rows[op.Key]
			if !hadOld {
				td.Keys = append(td.Keys, op.Key)
			}
			td.Rows[op.Key] = op.Record
			td.Versions[op.Key] = op.BaseVer + 1
			if td.Schema.IsPartitioned() {
				var oldPKey, newPKey string
				if hadOld {
					oldPKey, _ = td.Schema.ComputePartitionKey(oldRec)
				}
				newPKey, _ = td.Schema.ComputePartitionKey(op.Record)
				if oldPKey != newPKey {
					if oldPKey != "" {
						td.removePartitionKey(oldPKey, op.Key)
					}
					if newPKey != "" {
						td.addPartitionKey(newPKey, op.Key)
					}
				}
			}
			for _, idxData := range td.Indexes {
				if hadOld {
					oldKey := extractIndexKey(oldRec, idxData.Schema.Columns)
					idxData.Delete(oldKey, op.Key)
				}
				newKey := extractIndexKey(op.Record, idxData.Schema.Columns)
				_ = idxData.Insert(newKey, op.Key)
			}
		case OpDelete:
			td, exists := e.tables[op.Table]
			if !exists {
				continue
			}
			oldRec, hadOld := td.Rows[op.Key]
			delete(td.Rows, op.Key)
			delete(td.Versions, op.Key)
			if td.Schema.IsPartitioned() && hadOld {
				if pKey, err := td.Schema.ComputePartitionKey(oldRec); err == nil {
					td.removePartitionKey(pKey, op.Key)
				}
			}
			for i, k := range td.Keys {
				if k == op.Key {
					td.Keys = append(td.Keys[:i], td.Keys[i+1:]...)
					break
				}
			}
			if hadOld {
				for _, idxData := range td.Indexes {
					oldKey := extractIndexKey(oldRec, idxData.Schema.Columns)
					idxData.Delete(oldKey, op.Key)
				}
			}
		}
	}
	return nil
}

// RewriteAOF generates a fresh snapshot (tables.json) and truncates the AOF log file.
func (e *Engine) RewriteAOF() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.rewriteAOFLocked()
}

func (e *Engine) rewriteAOFLocked() error {
	if e.dataDir == "" || e.wal == nil {
		return nil
	}
	// 1. Atomically save full current in-memory snapshot
	if err := e.saveDiskLocked(); err != nil {
		return fmt.Errorf("AOF rewrite snapshot error: %w", err)
	}
	// 2. Truncate AOF log file
	if err := e.wal.Truncate(); err != nil {
		return fmt.Errorf("AOF truncate error: %w", err)
	}
	return nil
}

func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return nil
	}
	e.closed = true

	var saveErr, walErr error
	if e.dataDir != "" {
		saveErr = e.saveDiskLocked()
	}
	if e.wal != nil {
		walErr = e.wal.Close()
		e.wal = nil
	}
	if saveErr != nil {
		return saveErr
	}
	return walErr
}

func (e *Engine) saveDiskLocked() error {
	if e.dataDir == "" {
		return nil
	}
	for _, td := range e.tables {
		for _, idx := range td.Indexes {
			idx.SyncEntries()
		}
	}
	data, err := MarshalMsgPack(e.tables)
	if err != nil {
		return fmt.Errorf("failed to encode tables to MessagePack: %w", err)
	}

	tmpFile := filepath.Join(e.dataDir, "tables.mpk.tmp")
	targetFile := filepath.Join(e.dataDir, "tables.mpk")

	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpFile, targetFile)
}

func (e *Engine) loadDiskLocked() {
	if e.dataDir == "" {
		return
	}

	// 1. Try standard MessagePack binary snapshot (tables.mpk)
	mpkFile := filepath.Join(e.dataDir, "tables.mpk")
	if data, err := os.ReadFile(mpkFile); err == nil && len(data) > 0 {
		var loaded map[string]*TableData
		if err := UnmarshalMsgPack(data, &loaded); err == nil && loaded != nil {
			e.tables = loaded
			for _, td := range e.tables {
				if td.Versions == nil {
					td.Versions = make(map[string]int64)
					for k := range td.Rows {
						td.Versions[k] = 1
					}
				}
				if td.Indexes == nil {
					td.Indexes = make(map[string]*IndexData)
				}
				for _, idx := range td.Indexes {
					idx.getSkipList()
				}
				if td.Schema.IsPartitioned() {
					if td.Partitions == nil {
						td.Partitions = make(map[string]map[string]struct{})
					}
					for k, rec := range td.Rows {
						if pKey, err := td.Schema.ComputePartitionKey(rec); err == nil {
							td.addPartitionKey(pKey, k)
						}
					}
				}
			}
			return
		}
	}

	// 2. Fallback to legacy JSON snapshot (tables.json) with auto-migration
	jsonFile := filepath.Join(e.dataDir, "tables.json")
	if data, err := os.ReadFile(jsonFile); err == nil && len(data) > 0 {
		var loaded map[string]*TableData
		if err := json.Unmarshal(data, &loaded); err == nil && loaded != nil {
			e.tables = loaded
			for _, td := range e.tables {
				if td.Versions == nil {
					td.Versions = make(map[string]int64)
					for k := range td.Rows {
						td.Versions[k] = 1
					}
				}
				if td.Indexes == nil {
					td.Indexes = make(map[string]*IndexData)
				}
				for _, idx := range td.Indexes {
					idx.getSkipList()
				}
				if td.Schema.IsPartitioned() {
					if td.Partitions == nil {
						td.Partitions = make(map[string]map[string]struct{})
					}
					for k, rec := range td.Rows {
						if pKey, err := td.Schema.ComputePartitionKey(rec); err == nil {
							td.addPartitionKey(pKey, k)
						}
					}
				}
			}
			// Migrate automatically to MessagePack format
			_ = e.saveDiskLocked()
			_ = os.Remove(jsonFile)
		}
	}
}

func (e *Engine) CreateTable(schema *storage.TableSchema) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.tables[schema.Name]; exists {
		return fmt.Errorf("%w: %s", storage.ErrTableExists, schema.Name)
	}

	if _, err := schema.PrimaryKeyColumn(); err != nil {
		return err
	}

	for i := range schema.ForeignKeys {
		fk := &schema.ForeignKeys[i]
		if fk.ChildTable == "" {
			fk.ChildTable = schema.Name
		}
		parentTd, exists := e.tables[fk.ParentTable]
		if !exists {
			return fmt.Errorf("%w: referenced parent table %q does not exist", storage.ErrTableNotFound, fk.ParentTable)
		}
		if len(fk.ParentColumns) == 0 {
			pkCol, err := parentTd.Schema.PrimaryKeyColumn()
			if err != nil || pkCol == nil {
				return fmt.Errorf("referenced parent table %q has no primary key", fk.ParentTable)
			}
			fk.ParentColumns = []string{pkCol.Name}
		}
	}

	td := &TableData{
		Schema:     *schema,
		Rows:       make(map[string]storage.Record),
		Versions:   make(map[string]int64),
		Keys:       make([]string, 0),
		Indexes:    make(map[string]*IndexData),
		Partitions: make(map[string]map[string]struct{}),
	}
	for _, idx := range schema.Indexes {
		td.Indexes[idx.Name] = &IndexData{
			Schema:  idx,
			Entries: make([]IndexEntry, 0),
		}
	}
	e.tables[schema.Name] = td

	if e.wal != nil {
		_ = e.wal.Append(WALRecord{
			LSN:       e.nextLSN.Add(1),
			Timestamp: time.Now().UnixNano(),
			Ops: []WALOp{{
				Type:   OpCreateTable,
				Table:  schema.Name,
				Schema: schema,
			}},
		})
	}
	return nil
}

func (e *Engine) CreateIndex(index *storage.IndexSchema) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	td, exists := e.tables[index.TableName]
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, index.TableName)
	}

	if _, _, exists := td.Schema.FindIndex(index.Name); exists {
		return fmt.Errorf("%w: %s", storage.ErrIndexExists, index.Name)
	}

	// Validate columns
	for _, colName := range index.Columns {
		if _, _, found := td.Schema.FindColumn(colName); !found {
			return fmt.Errorf("%w: %s on table %s", storage.ErrColumnNotFound, colName, index.TableName)
		}
	}

	idxData := &IndexData{
		Schema:  *index,
		Entries: make([]IndexEntry, 0),
	}

	// Populate index from existing rows
	for pk, rec := range td.Rows {
		keyVal := extractIndexKey(rec, index.Columns)
		if err := idxData.Insert(keyVal, pk); err != nil {
			return err
		}
	}

	if td.Indexes == nil {
		td.Indexes = make(map[string]*IndexData)
	}
	td.Indexes[index.Name] = idxData
	td.Schema.Indexes = append(td.Schema.Indexes, *index)

	if e.wal != nil {
		_ = e.wal.Append(WALRecord{
			LSN:       e.nextLSN.Add(1),
			Timestamp: time.Now().UnixNano(),
			Ops: []WALOp{{
				Type:        OpCreateIndex,
				Table:       index.TableName,
				IndexSchema: index,
			}},
		})
	}
	return nil
}

func (e *Engine) DropIndex(tableName, indexName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	td, exists := e.tables[tableName]
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	_, idxPos, exists := td.Schema.FindIndex(indexName)
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrIndexNotFound, indexName)
	}

	delete(td.Indexes, indexName)
	td.Schema.Indexes = append(td.Schema.Indexes[:idxPos], td.Schema.Indexes[idxPos+1:]...)

	if e.wal != nil {
		_ = e.wal.Append(WALRecord{
			LSN:       e.nextLSN.Add(1),
			Timestamp: time.Now().UnixNano(),
			Ops: []WALOp{{
				Type:      OpDropIndex,
				Table:     tableName,
				IndexName: indexName,
			}},
		})
	}
	return nil
}

func extractIndexKey(rec storage.Record, cols []string) storage.Value {
	if len(cols) == 0 {
		return storage.NullValue(storage.TypeUnknown)
	}
	if len(cols) == 1 {
		if v, ok := rec[cols[0]]; ok {
			return v
		}
		return storage.NullValue(storage.TypeUnknown)
	}
	// Composite key formatted as string
	var sb strings.Builder
	for i, c := range cols {
		if i > 0 {
			sb.WriteByte(0)
		}
		if v, ok := rec[c]; ok {
			sb.WriteString(storage.EncodeIndexKeyPart(v))
		} else {
			sb.WriteString("NULL")
		}
	}
	return storage.StringValue(sb.String())
}

func (e *Engine) DropTable(tableName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.tables[tableName]; !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	// Verify no other table has a foreign key referencing this table
	for otherName, otherTd := range e.tables {
		if strings.EqualFold(otherName, tableName) {
			continue
		}
		for _, fk := range otherTd.Schema.ForeignKeys {
			if strings.EqualFold(fk.ParentTable, tableName) {
				return fmt.Errorf("%w: table %q is referenced by foreign key constraint %q on table %q",
					storage.ErrDependentObjectsExist, tableName, fk.Name, otherName)
			}
		}
	}

	delete(e.tables, tableName)
	if e.wal != nil {
		_ = e.wal.Append(WALRecord{
			LSN:       e.nextLSN.Add(1),
			Timestamp: time.Now().UnixNano(),
			Ops: []WALOp{{
				Type:  OpDropTable,
				Table: tableName,
			}},
		})
	}
	return nil
}

func (e *Engine) TruncateTable(tableName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	td, exists := e.tables[tableName]
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	// Verify no other table has a foreign key referencing this table
	for otherName, otherTd := range e.tables {
		if strings.EqualFold(otherName, tableName) {
			continue
		}
		for _, fk := range otherTd.Schema.ForeignKeys {
			if strings.EqualFold(fk.ParentTable, tableName) {
				return fmt.Errorf("%w: cannot truncate table %q because it is referenced by foreign key %q on table %q",
					storage.ErrDependentObjectsExist, tableName, fk.Name, otherName)
			}
		}
	}

	td.mu.Lock()
	td.Rows = make(map[string]storage.Record)
	td.Versions = make(map[string]int64)
	td.Keys = make([]string, 0)
	td.Partitions = make(map[string]map[string]struct{})
	for _, idx := range td.Indexes {
		idx.sl = NewSkipList(idx.Schema.Unique)
		idx.Entries = nil
	}
	td.mu.Unlock()

	if e.wal != nil {
		_ = e.wal.Append(WALRecord{
			LSN:       e.nextLSN.Add(1),
			Timestamp: time.Now().UnixNano(),
			Ops: []WALOp{{
				Type:  OpTruncateTable,
				Table: tableName,
			}},
		})
	}
	return nil
}

func (e *Engine) DropPartition(tableName, partitionName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	td, exists := e.tables[tableName]
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	if !td.Schema.IsPartitioned() {
		return fmt.Errorf("%w: %s", storage.ErrTableNotPartitioned, tableName)
	}

	pksSet, ok := td.Partitions[partitionName]
	if !ok || len(pksSet) == 0 {
		return fmt.Errorf("%w: partition %q in table %q", storage.ErrPartitionNotFound, partitionName, tableName)
	}

	// Delete all rows in partition and purge secondary index references
	for pk := range pksSet {
		oldRec, hadOld := td.Rows[pk]
		delete(td.Rows, pk)
		delete(td.Versions, pk)
		if hadOld {
			for _, idxData := range td.Indexes {
				oldKey := extractIndexKey(oldRec, idxData.Schema.Columns)
				idxData.Delete(oldKey, pk)
			}
		}
	}
	delete(td.Partitions, partitionName)

	// Rebuild keys
	newKeys := make([]string, 0, len(td.Rows))
	for k := range td.Rows {
		newKeys = append(newKeys, k)
	}
	td.Keys = newKeys

	if e.wal != nil {
		_ = e.wal.Append(WALRecord{
			LSN:       e.nextLSN.Add(1),
			Timestamp: time.Now().UnixNano(),
			Ops: []WALOp{{
				Type:  OpDropPartition,
				Table: tableName,
				Key:   partitionName,
			}},
		})
	}

	return nil
}

func (e *Engine) GetPartitions(tableName string) ([]storage.PartitionInfo, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	td, exists := e.tables[tableName]
	if !exists {
		return nil, fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	td.mu.RLock()
	defer td.mu.RUnlock()

	if !td.Schema.IsPartitioned() {
		return nil, fmt.Errorf("%w: %s", storage.ErrTableNotPartitioned, tableName)
	}

	res := make([]storage.PartitionInfo, 0, len(td.Partitions))
	for name, set := range td.Partitions {
		res = append(res, storage.PartitionInfo{
			Name:     name,
			RowCount: int64(len(set)),
		})
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].Name < res[j].Name
	})
	return res, nil
}

func (e *Engine) GetTable(tableName string) (*storage.TableSchema, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	td, exists := e.tables[tableName]
	if !exists {
		return nil, fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}
	cp := td.Schema
	return &cp, nil
}

func (e *Engine) ListTables() ([]*storage.TableSchema, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]*storage.TableSchema, 0, len(e.tables))
	for _, td := range e.tables {
		cp := td.Schema
		result = append(result, &cp)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func (e *Engine) NewSession() (storage.Session, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return nil, fmt.Errorf("engine is closed")
	}
	return &Session{engine: e}, nil
}

// Session represents a client session.
type Session struct {
	engine *Engine
}

func (s *Session) BeginTx(opts storage.TxOptions) (storage.Transaction, error) {
	s.engine.mu.RLock()
	defer s.engine.mu.RUnlock()

	if s.engine.closed {
		return nil, fmt.Errorf("engine is closed")
	}

	return &Transaction{
		engine:   s.engine,
		opts:     opts,
		local:      make(map[string]map[string]storage.Record),
		baseVers:   make(map[string]map[string]int64),
		deleted:    make(map[string]map[string]bool),
		changes:    make(map[string]rowChange),
		savepoints: make(map[string]savepointState),
	}, nil
}

func (s *Session) Close() error {
	return nil
}

type changeType int

const (
	changeInsert changeType = iota
	changeUpdate
	changeDelete
)

type rowChange struct {
	kind    changeType
	table   string
	key     string
	baseVer int64
	rec     storage.Record
}

type savepointState struct {
	local    map[string]map[string]storage.Record
	baseVers map[string]map[string]int64
	deleted  map[string]map[string]bool
	changes  map[string]rowChange
}

// Transaction handles copy-on-write delta mutations with zero start overhead, OCC validation, and atomic commit.
type Transaction struct {
	mu         sync.Mutex
	engine     *Engine
	opts       storage.TxOptions
	local      map[string]map[string]storage.Record // tableName -> key -> record
	baseVers   map[string]map[string]int64          // tableName -> key -> baseVersion
	deleted    map[string]map[string]bool           // tableName -> key -> bool
	changes    map[string]rowChange                 // "table:key" -> rowChange
	savepoints map[string]savepointState
	spNames    []string
	closed     bool
}

func (tx *Transaction) Insert(tableName string, record storage.Record) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	if tx.opts.ReadOnly {
		return fmt.Errorf("cannot insert in read-only transaction")
	}

	return tx.insertLocked(tableName, record)
}

func (tx *Transaction) insertLocked(tableName string, record storage.Record) error {
	tx.engine.mu.RLock()
	td, exists := tx.engine.tables[tableName]
	if !exists {
		tx.engine.mu.RUnlock()
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}
	schema := td.Schema
	tx.engine.mu.RUnlock()

	pkCol, err := schema.PrimaryKeyColumn()
	if err != nil {
		return err
	}

	pkVal, hasPk := record[pkCol.Name]
	if !hasPk || pkVal.IsNull {
		return fmt.Errorf("%w: missing primary key %q", storage.ErrNotNullViolation, pkCol.Name)
	}

	pkKey := pkVal.String()

	// Check if key exists in local writes or engine
	if tx.isLocallyDeleted(tableName, pkKey) {
		// Was deleted locally, so insert is allowed
	} else if _, inLocal := tx.getLocal(tableName, pkKey); inLocal {
		return fmt.Errorf("%w: key %q in table %q", storage.ErrKeyExists, pkKey, tableName)
	} else {
		tx.engine.mu.RLock()
		if td, ok := tx.engine.tables[tableName]; ok {
			td.mu.RLock()
			_, inEngine := td.Rows[pkKey]
			td.mu.RUnlock()
			if inEngine {
				tx.engine.mu.RUnlock()
				return fmt.Errorf("%w: key %q in table %q", storage.ErrKeyExists, pkKey, tableName)
			}
		}
		tx.engine.mu.RUnlock()
	}

	// Validate unique index constraints
	tx.engine.mu.RLock()
	if td, ok := tx.engine.tables[tableName]; ok {
		td.mu.RLock()
		for _, idxData := range td.Indexes {
			if idxData.Schema.Unique {
				keyVal := extractIndexKey(record, idxData.Schema.Columns)
				existing := idxData.ScanRange(&keyVal, &keyVal)
				for _, epk := range existing {
					if epk != pkKey && !tx.isLocallyDeleted(tableName, epk) {
						td.mu.RUnlock()
						tx.engine.mu.RUnlock()
						return fmt.Errorf("%w: duplicate key %q in unique index %q", storage.ErrUniqueViolation, keyVal.String(), idxData.Schema.Name)
					}
				}
			}
		}
		td.mu.RUnlock()
	}
	tx.engine.mu.RUnlock()

	if localRows, ok := tx.local[tableName]; ok {
		for epk, locRec := range localRows {
			if epk != pkKey && !tx.isLocallyDeleted(tableName, epk) {
				for _, idx := range schema.Indexes {
					if idx.Unique {
						keyVal := extractIndexKey(record, idx.Columns)
						locVal := extractIndexKey(locRec, idx.Columns)
						if keyVal.Equal(locVal) {
							return fmt.Errorf("%w: duplicate key %q in unique index %q", storage.ErrUniqueViolation, keyVal.String(), idx.Name)
						}
					}
				}
			}
		}
	}

	// Validate columns and types
	for _, col := range schema.Columns {
		val, found := record[col.Name]
		if !found || val.IsNull {
			if !col.Nullable && !col.PrimaryKey {
				return fmt.Errorf("%w: column %q", storage.ErrNotNullViolation, col.Name)
			}
			continue
		}
		if val.Type != col.Type {
			if col.Type == storage.TypeFloat && val.Type == storage.TypeInt {
				val = storage.FloatValue(float64(val.IVal))
				record[col.Name] = val
			} else if col.Type == storage.TypeInt && val.Type == storage.TypeFloat && val.FVal == float64(int64(val.FVal)) {
				val = storage.IntValue(int64(val.FVal))
				record[col.Name] = val
			} else if col.Type == storage.TypeDecimal && (val.Type == storage.TypeInt || val.Type == storage.TypeFloat || val.Type == storage.TypeVarChar) {
				val = storage.DecimalValue(val.String())
				if col.Scale > 0 {
					val = storage.DecimalValueFromDecimal(val.DVal.Rescale(int32(col.Scale)))
				}
				record[col.Name] = val
			} else if col.Type == storage.TypeJSON && (val.Type == storage.TypeVarChar || val.Type == storage.TypeJSON) {
				if !storage.IsValidJSON(val.SVal) {
					return fmt.Errorf("%w: column %q has invalid JSON syntax", storage.ErrInvalidJSON, col.Name)
				}
				val = storage.JSONValue(val.SVal)
				record[col.Name] = val
			} else if col.Type == storage.TypeUUID && (val.Type == storage.TypeVarChar || val.Type == storage.TypeUUID) {
				val = storage.UUIDValue(val.SVal)
				record[col.Name] = val
			} else if col.Type == storage.TypeBinary && (val.Type == storage.TypeVarChar || val.Type == storage.TypeBinary) {
				val = storage.BinaryValueFromString(val.String())
				record[col.Name] = val
			} else if col.Type == storage.TypeDate && (val.Type == storage.TypeVarChar || val.Type == storage.TypeDateTime || val.Type == storage.TypeDate) {
				val = storage.DateValueFromAny(val)
				record[col.Name] = val
			} else if col.Type == storage.TypeTime && (val.Type == storage.TypeVarChar || val.Type == storage.TypeDateTime || val.Type == storage.TypeTime) {
				val = storage.TimeValueFromAny(val)
				record[col.Name] = val
			} else if col.Type == storage.TypeDateTime && (val.Type == storage.TypeVarChar || val.Type == storage.TypeDate) {
				dt, err := storage.DateTimeValueFromAny(val)
				if err != nil {
					return fmt.Errorf("%w: column %q expects %s, got %s",
						storage.ErrInvalidDataType, col.Name, col.Type, val.Type)
				}
				record[col.Name] = dt
			} else {
				return fmt.Errorf("%w: column %q expects %s, got %s",
					storage.ErrInvalidDataType, col.Name, col.Type, val.Type)
			}
		} else if col.Type == storage.TypeDecimal && col.Scale > 0 {
			val = storage.DecimalValueFromDecimal(val.DVal.Rescale(int32(col.Scale)))
			record[col.Name] = val
		} else if col.Type == storage.TypeJSON {
			if !storage.IsValidJSON(val.SVal) {
				return fmt.Errorf("%w: column %q has invalid JSON syntax", storage.ErrInvalidJSON, col.Name)
			}
		}
	}

	// Validate foreign keys
	if len(schema.ForeignKeys) > 0 {
		if err := tx.validateForeignKeysUnderLock(tableName, record, &schema); err != nil {
			return err
		}
	}

	recCopy := cloneRecord(record)
	tx.putLocal(tableName, pkKey, recCopy, 0)
	tx.unmarkDeleted(tableName, pkKey)

	changeKey := tableName + ":" + pkKey
	tx.changes[changeKey] = rowChange{
		kind:    changeInsert,
		table:   tableName,
		key:     pkKey,
		baseVer: 0,
		rec:     recCopy,
	}

	return nil
}

func (tx *Transaction) Update(tableName string, pk storage.Value, record storage.Record) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	if tx.opts.ReadOnly {
		return fmt.Errorf("cannot update in read-only transaction")
	}

	return tx.updateLocked(tableName, pk, record, 0)
}

func (tx *Transaction) updateLocked(tableName string, pk storage.Value, record storage.Record, depth int) error {
	if depth > 16 {
		return fmt.Errorf("foreign key cascade recursion limit exceeded (max 16)")
	}

	pkKey := pk.String()
	base, ver, err := tx.getRecordAndVerUnderLock(tableName, pkKey, false)
	if err != nil {
		return err
	}

	tx.engine.mu.RLock()
	td, exists := tx.engine.tables[tableName]
	if !exists {
		tx.engine.mu.RUnlock()
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}
	schema := td.Schema
	tx.engine.mu.RUnlock()

	updated := make(storage.Record, len(base))
	for col, val := range base {
		updated[col] = val
	}
	for col, val := range record {
		updated[col] = val
	}

	// Validate columns and types for updated row
	for _, col := range schema.Columns {
		val, found := updated[col.Name]
		if !found || val.IsNull {
			if !col.Nullable && !col.PrimaryKey {
				return fmt.Errorf("%w: column %q", storage.ErrNotNullViolation, col.Name)
			}
			continue
		}
		if val.Type != col.Type {
			if col.Type == storage.TypeFloat && val.Type == storage.TypeInt {
				val = storage.FloatValue(float64(val.IVal))
				updated[col.Name] = val
			} else if col.Type == storage.TypeInt && val.Type == storage.TypeFloat && val.FVal == float64(int64(val.FVal)) {
				val = storage.IntValue(int64(val.FVal))
				updated[col.Name] = val
			} else if col.Type == storage.TypeDecimal && (val.Type == storage.TypeInt || val.Type == storage.TypeFloat || val.Type == storage.TypeVarChar) {
				val = storage.DecimalValue(val.String())
				if col.Scale > 0 {
					val = storage.DecimalValueFromDecimal(val.DVal.Rescale(int32(col.Scale)))
				}
				updated[col.Name] = val
			} else if col.Type == storage.TypeJSON && (val.Type == storage.TypeVarChar || val.Type == storage.TypeJSON) {
				if !storage.IsValidJSON(val.SVal) {
					return fmt.Errorf("%w: column %q has invalid JSON syntax", storage.ErrInvalidJSON, col.Name)
				}
				val = storage.JSONValue(val.SVal)
				updated[col.Name] = val
			} else if col.Type == storage.TypeUUID && (val.Type == storage.TypeVarChar || val.Type == storage.TypeUUID) {
				val = storage.UUIDValue(val.SVal)
				updated[col.Name] = val
			} else if col.Type == storage.TypeBinary && (val.Type == storage.TypeVarChar || val.Type == storage.TypeBinary) {
				val = storage.BinaryValueFromString(val.String())
				updated[col.Name] = val
			} else if col.Type == storage.TypeDate && (val.Type == storage.TypeVarChar || val.Type == storage.TypeDateTime || val.Type == storage.TypeDate) {
				val = storage.DateValueFromAny(val)
				updated[col.Name] = val
			} else if col.Type == storage.TypeTime && (val.Type == storage.TypeVarChar || val.Type == storage.TypeDateTime || val.Type == storage.TypeTime) {
				val = storage.TimeValueFromAny(val)
				updated[col.Name] = val
			} else if col.Type == storage.TypeDateTime && (val.Type == storage.TypeVarChar || val.Type == storage.TypeDate) {
				dt, err := storage.DateTimeValueFromAny(val)
				if err != nil {
					return fmt.Errorf("%w: column %q expects %s, got %s",
						storage.ErrInvalidDataType, col.Name, col.Type, val.Type)
				}
				updated[col.Name] = dt
			} else {
				return fmt.Errorf("%w: column %q expects %s, got %s",
					storage.ErrInvalidDataType, col.Name, col.Type, val.Type)
			}
		} else if col.Type == storage.TypeDecimal && col.Scale > 0 {
			val = storage.DecimalValueFromDecimal(val.DVal.Rescale(int32(col.Scale)))
			updated[col.Name] = val
		} else if col.Type == storage.TypeJSON {
			if !storage.IsValidJSON(val.SVal) {
				return fmt.Errorf("%w: column %q has invalid JSON syntax", storage.ErrInvalidJSON, col.Name)
			}
		}
	}

	// Validate foreign keys on the updated record
	if len(schema.ForeignKeys) > 0 {
		if err := tx.validateForeignKeysUnderLock(tableName, updated, &schema); err != nil {
			return err
		}
	}

	// If this table is referenced by child tables, check if any referenced columns changed
	type childRef struct {
		childTable string
		fk         storage.ForeignKeySchema
	}
	var childRefs []childRef
	tx.engine.mu.RLock()
	for childName, otherTd := range tx.engine.tables {
		for _, fk := range otherTd.Schema.ForeignKeys {
			if strings.EqualFold(fk.ParentTable, tableName) {
				childRefs = append(childRefs, childRef{childTable: childName, fk: fk})
			}
		}
	}
	tx.engine.mu.RUnlock()

	// Stage local update before cascading so that child updates see the new parent row
	newPkKey := pkKey
	if pkCol, err := schema.PrimaryKeyColumn(); err == nil && pkCol != nil {
		if newPkVal, ok := updated[pkCol.Name]; ok && !newPkVal.IsNull {
			newPkKey = newPkVal.String()
		}
	}

	tx.setBaseVer(tableName, pkKey, ver)
	originalBaseVer := tx.getBaseVer(tableName, pkKey)

	if newPkKey != pkKey {
		tx.removeLocal(tableName, pkKey)
		tx.markDeleted(tableName, pkKey)
		tx.changes[tableName+":"+pkKey] = rowChange{
			kind:    changeDelete,
			table:   tableName,
			key:     pkKey,
			baseVer: originalBaseVer,
		}

		tx.putLocal(tableName, newPkKey, updated, 0)
		tx.unmarkDeleted(tableName, newPkKey)
		tx.changes[tableName+":"+newPkKey] = rowChange{
			kind:    changeInsert,
			table:   tableName,
			key:     newPkKey,
			baseVer: 0,
			rec:     updated,
		}
	} else {
		tx.putLocal(tableName, pkKey, updated, originalBaseVer)
		tx.unmarkDeleted(tableName, pkKey)

		changeKey := tableName + ":" + pkKey
		tx.changes[changeKey] = rowChange{
			kind:    changeUpdate,
			table:   tableName,
			key:     pkKey,
			baseVer: originalBaseVer,
			rec:     updated,
		}
	}

	// Handle cascading updates to child tables if any parent referenced column changed
	for _, cr := range childRefs {
		pCols := cr.fk.ParentColumns
		if len(pCols) == 0 {
			if pkCol, err := schema.PrimaryKeyColumn(); err == nil && pkCol != nil {
				pCols = []string{pkCol.Name}
			}
		}
		changed := false
		for _, pCol := range pCols {
			baseVal, bOk := base[pCol]
			newVal, nOk := updated[pCol]
			if bOk != nOk || !valuesEqual(baseVal, newVal) {
				changed = true
				break
			}
		}

		if changed {
			matchingChildPKs, err := tx.findMatchingChildPKsUnderLock(cr.childTable, cr.fk, base)
			if err != nil {
				return err
			}
			if len(matchingChildPKs) > 0 {
				switch cr.fk.OnUpdate {
				case storage.ActionCascade:
					for _, cpk := range matchingChildPKs {
						patch := make(storage.Record)
						for i, cCol := range cr.fk.ChildColumns {
							pCol := pCols[i]
							patch[cCol] = updated[pCol]
						}
						if err := tx.updateLocked(cr.childTable, cpk, patch, depth+1); err != nil {
							return err
						}
					}
				case storage.ActionSetNull:
					for _, cpk := range matchingChildPKs {
						patch := make(storage.Record)
						for _, cCol := range cr.fk.ChildColumns {
							colType := storage.TypeVarChar
							if childTd, ok := tx.engine.tables[cr.childTable]; ok {
								if col, _, found := childTd.Schema.FindColumn(cCol); found {
									colType = col.Type
								}
							}
							patch[cCol] = storage.NullValue(colType)
						}
						if err := tx.updateLocked(cr.childTable, cpk, patch, depth+1); err != nil {
							return err
						}
					}
				case storage.ActionRestrict, storage.ActionNoAction:
					return fmt.Errorf("%w: cannot update %q because %q has referencing rows (foreign key %q)",
						storage.ErrForeignKeyViolation, tableName, cr.childTable, cr.fk.Name)
				default:
					return fmt.Errorf("%w: cannot update %q because %q has referencing rows (foreign key %q)",
						storage.ErrForeignKeyViolation, tableName, cr.childTable, cr.fk.Name)
				}
			}
		}
	}

	return nil
}

func (tx *Transaction) Delete(tableName string, pk storage.Value) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	if tx.opts.ReadOnly {
		return fmt.Errorf("cannot delete in read-only transaction")
	}

	return tx.deleteLocked(tableName, pk, 0)
}

func (tx *Transaction) deleteLocked(tableName string, pk storage.Value, depth int) error {
	if depth > 16 {
		return fmt.Errorf("foreign key cascade recursion limit exceeded (max 16)")
	}

	pkKey := pk.String()
	base, ver, err := tx.getRecordAndVerUnderLock(tableName, pkKey, false)
	if err != nil {
		return err
	}

	tx.engine.mu.RLock()
	_, parentExists := tx.engine.tables[tableName]
	if !parentExists {
		tx.engine.mu.RUnlock()
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	// Find all child tables that have a foreign key referencing tableName
	type childRef struct {
		childTable string
		fk         storage.ForeignKeySchema
	}
	var childRefs []childRef
	for childName, td := range tx.engine.tables {
		for _, fk := range td.Schema.ForeignKeys {
			if strings.EqualFold(fk.ParentTable, tableName) {
				childRefs = append(childRefs, childRef{childTable: childName, fk: fk})
			}
		}
	}
	tx.engine.mu.RUnlock()

	// For each referencing child table, find rows that reference this parent row
	for _, cr := range childRefs {
		matchingChildPKs, err := tx.findMatchingChildPKsUnderLock(cr.childTable, cr.fk, base)
		if err != nil {
			return err
		}
		if len(matchingChildPKs) > 0 {
			switch cr.fk.OnDelete {
			case storage.ActionCascade:
				for _, cpk := range matchingChildPKs {
					if err := tx.deleteLocked(cr.childTable, cpk, depth+1); err != nil {
						return err
					}
				}
			case storage.ActionSetNull:
				for _, cpk := range matchingChildPKs {
					nullPatch := make(storage.Record)
					for _, cCol := range cr.fk.ChildColumns {
						colType := storage.TypeVarChar
						if childTd, ok := tx.engine.tables[cr.childTable]; ok {
							if col, _, found := childTd.Schema.FindColumn(cCol); found {
								colType = col.Type
							}
						}
						nullPatch[cCol] = storage.NullValue(colType)
					}
					if err := tx.updateLocked(cr.childTable, cpk, nullPatch, depth+1); err != nil {
						return err
					}
				}
			case storage.ActionRestrict, storage.ActionNoAction:
				return fmt.Errorf("%w: cannot delete from %q because %q has referencing rows (foreign key %q)",
					storage.ErrForeignKeyViolation, tableName, cr.childTable, cr.fk.Name)
			default:
				return fmt.Errorf("%w: cannot delete from %q because %q has referencing rows (foreign key %q)",
					storage.ErrForeignKeyViolation, tableName, cr.childTable, cr.fk.Name)
			}
		}
	}

	tx.setBaseVer(tableName, pkKey, ver)
	originalBaseVer := tx.getBaseVer(tableName, pkKey)

	tx.removeLocal(tableName, pkKey)
	tx.markDeleted(tableName, pkKey)

	changeKey := tableName + ":" + pkKey
	tx.changes[changeKey] = rowChange{
		kind:    changeDelete,
		table:   tableName,
		key:     pkKey,
		baseVer: originalBaseVer,
	}

	return nil
}

func (tx *Transaction) validateForeignKeysUnderLock(childTable string, record storage.Record, schema *storage.TableSchema) error {
	for _, fk := range schema.ForeignKeys {
		hasNull := false
		for _, cCol := range fk.ChildColumns {
			val, ok := record[cCol]
			if !ok || val.IsNull {
				hasNull = true
				break
			}
		}
		if hasNull {
			continue
		}

		exists, err := tx.parentRecordExistsUnderLock(fk, record)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: foreign key %q references non-existent row in %q",
				storage.ErrForeignKeyViolation, fk.Name, fk.ParentTable)
		}
	}
	return nil
}

func (tx *Transaction) parentRecordExistsUnderLock(fk storage.ForeignKeySchema, childRecord storage.Record) (bool, error) {
	tx.engine.mu.RLock()
	parentTd, ok := tx.engine.tables[fk.ParentTable]
	if !ok {
		tx.engine.mu.RUnlock()
		return false, fmt.Errorf("%w: referenced table %q not found", storage.ErrTableNotFound, fk.ParentTable)
	}

	parentCols := fk.ParentColumns
	if len(parentCols) == 0 {
		pkCol, err := parentTd.Schema.PrimaryKeyColumn()
		if err != nil || pkCol == nil {
			tx.engine.mu.RUnlock()
			return false, fmt.Errorf("referenced table %q has no primary key", fk.ParentTable)
		}
		parentCols = []string{pkCol.Name}
	}

	childVals := make([]storage.Value, len(fk.ChildColumns))
	for i, cCol := range fk.ChildColumns {
		val, ok := childRecord[cCol]
		if !ok || val.IsNull {
			tx.engine.mu.RUnlock()
			return true, nil
		}
		childVals[i] = val
	}

	parentPkCol, _ := parentTd.Schema.PrimaryKeyColumn()
	if parentPkCol != nil && len(parentCols) == 1 && strings.EqualFold(parentCols[0], parentPkCol.Name) {
		tx.engine.mu.RUnlock()
		parentKey := childVals[0].String()
		if tx.isLocallyDeleted(fk.ParentTable, parentKey) {
			return false, nil
		}
		if _, inLocal := tx.getLocal(fk.ParentTable, parentKey); inLocal {
			return true, nil
		}
		tx.engine.mu.RLock()
		if pTd, ok := tx.engine.tables[fk.ParentTable]; ok {
			pTd.mu.RLock()
			_, inEngine := pTd.Rows[parentKey]
			pTd.mu.RUnlock()
			tx.engine.mu.RUnlock()
			return inEngine, nil
		}
		tx.engine.mu.RUnlock()
		return false, nil
	}

	parentTd.mu.RLock()
	for pkStr, rec := range parentTd.Rows {
		if tx.isLocallyDeleted(fk.ParentTable, pkStr) {
			continue
		}
		if _, inLocal := tx.getLocal(fk.ParentTable, pkStr); inLocal {
			continue
		}
		if recordMatchesFK(rec, parentCols, childVals) {
			parentTd.mu.RUnlock()
			tx.engine.mu.RUnlock()
			return true, nil
		}
	}
	parentTd.mu.RUnlock()
	tx.engine.mu.RUnlock()

	if localRows, ok := tx.local[fk.ParentTable]; ok {
		for pkStr, rec := range localRows {
			if tx.isLocallyDeleted(fk.ParentTable, pkStr) {
				continue
			}
			if recordMatchesFK(rec, parentCols, childVals) {
				return true, nil
			}
		}
	}

	return false, nil
}

func (tx *Transaction) findMatchingChildPKsUnderLock(childTable string, fk storage.ForeignKeySchema, parentRecord storage.Record) ([]storage.Value, error) {
	tx.engine.mu.RLock()
	childTd, exists := tx.engine.tables[childTable]
	if !exists {
		tx.engine.mu.RUnlock()
		return nil, fmt.Errorf("%w: %s", storage.ErrTableNotFound, childTable)
	}
	childSchema := childTd.Schema
	childPkCol, err := childSchema.PrimaryKeyColumn()
	if err != nil {
		tx.engine.mu.RUnlock()
		return nil, err
	}

	pCols := fk.ParentColumns
	if len(pCols) == 0 {
		if parentTd, pExists := tx.engine.tables[fk.ParentTable]; pExists {
			if pkCol, err := parentTd.Schema.PrimaryKeyColumn(); err == nil && pkCol != nil {
				pCols = []string{pkCol.Name}
			}
		}
	}

	parentValues := make([]storage.Value, len(pCols))
	for i, pCol := range pCols {
		val, ok := parentRecord[pCol]
		if !ok || val.IsNull {
			tx.engine.mu.RUnlock()
			return nil, nil
		}
		parentValues[i] = val
	}

	var matchingPKs []storage.Value
	childTd.mu.RLock()
	for pkStr, rec := range childTd.Rows {
		if tx.isLocallyDeleted(childTable, pkStr) {
			continue
		}
		if _, inLocal := tx.getLocal(childTable, pkStr); inLocal {
			continue
		}
		if recordMatchesFK(rec, fk.ChildColumns, parentValues) {
			matchingPKs = append(matchingPKs, rec[childPkCol.Name])
		}
	}
	childTd.mu.RUnlock()
	tx.engine.mu.RUnlock()

	if localRows, ok := tx.local[childTable]; ok {
		for pkStr, rec := range localRows {
			if tx.isLocallyDeleted(childTable, pkStr) {
				continue
			}
			if recordMatchesFK(rec, fk.ChildColumns, parentValues) {
				matchingPKs = append(matchingPKs, rec[childPkCol.Name])
			}
		}
	}

	return matchingPKs, nil
}

func recordMatchesFK(rec storage.Record, cols []string, vals []storage.Value) bool {
	if len(cols) != len(vals) {
		return false
	}
	for i, col := range cols {
		v, ok := rec[col]
		if !ok || v.IsNull {
			return false
		}
		if !valuesEqual(v, vals[i]) {
			return false
		}
	}
	return true
}

func valuesEqual(a, b storage.Value) bool {
	if a.Equal(b) {
		return true
	}
	if a.Type == storage.TypeFloat && b.Type == storage.TypeInt {
		return a.FVal == float64(b.IVal)
	}
	if a.Type == storage.TypeInt && b.Type == storage.TypeFloat {
		return float64(a.IVal) == b.FVal
	}
	return false
}

func (tx *Transaction) Get(tableName string, pk storage.Value) (storage.Record, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return nil, storage.ErrTxClosed
	}

	pkKey := pk.String()
	rec, ver, err := tx.getRecordAndVerUnderLock(tableName, pkKey, true)
	if err == nil {
		tx.setBaseVer(tableName, pkKey, ver)
	}
	return rec, err
}

func (tx *Transaction) getRecordAndVerUnderLock(tableName, pkKey string, clone bool) (storage.Record, int64, error) {
	if tx.isLocallyDeleted(tableName, pkKey) {
		return nil, 0, fmt.Errorf("%w: key %q in table %q", storage.ErrKeyNotFound, pkKey, tableName)
	}

	if rec, found := tx.getLocal(tableName, pkKey); found {
		ver := tx.getBaseVer(tableName, pkKey)
		return rec, ver, nil
	}

	tx.engine.mu.RLock()
	td, exists := tx.engine.tables[tableName]
	if !exists {
		tx.engine.mu.RUnlock()
		return nil, 0, fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}
	td.mu.RLock()
	rec, exists := td.Rows[pkKey]
	ver := td.Versions[pkKey]
	td.mu.RUnlock()
	tx.engine.mu.RUnlock()

	if !exists {
		return nil, 0, fmt.Errorf("%w: key %q in table %q", storage.ErrKeyNotFound, pkKey, tableName)
	}

	if clone {
		return cloneRecord(rec), ver, nil
	}
	return rec, ver, nil
}

func (tx *Transaction) Scan(tableName string) (storage.Cursor, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return nil, storage.ErrTxClosed
	}

	tx.engine.mu.RLock()
	td, exists := tx.engine.tables[tableName]
	if !exists {
		tx.engine.mu.RUnlock()
		return nil, fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	td.mu.RLock()
	keys := make([]string, 0, len(td.Rows)+len(tx.local[tableName]))
	for k := range td.Rows {
		if !tx.isLocallyDeleted(tableName, k) {
			keys = append(keys, k)
		}
	}
	td.mu.RUnlock()
	tx.engine.mu.RUnlock()

	if localRows, ok := tx.local[tableName]; ok {
		for k := range localRows {
			if !tx.isLocallyDeleted(tableName, k) {
				keys = append(keys, k)
			}
		}
	}

	sort.Strings(keys)
	if len(keys) > 1 {
		w := 1
		for r := 1; r < len(keys); r++ {
			if keys[r] != keys[w-1] {
				keys[w] = keys[r]
				w++
			}
		}
		keys = keys[:w]
	}

	return &memCursor{
		tx:        tx,
		tableName: tableName,
		td:        td,
		keys:      keys,
		idx:       -1,
	}, nil
}

func (tx *Transaction) ScanPartitions(tableName string, partitions []string) (storage.Cursor, error) {
	if len(partitions) == 0 {
		return tx.Scan(tableName)
	}

	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return nil, storage.ErrTxClosed
	}

	tx.engine.mu.RLock()
	td, exists := tx.engine.tables[tableName]
	if !exists {
		tx.engine.mu.RUnlock()
		return nil, fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	if !td.Schema.IsPartitioned() {
		tx.engine.mu.RUnlock()
		return tx.Scan(tableName)
	}

	td.mu.RLock()
	partSet := make(map[string]bool, len(partitions))
	for _, p := range partitions {
		partSet[p] = true
	}

	keySet := make(map[string]struct{})
	for p := range partSet {
		if pks, ok := td.Partitions[p]; ok {
			for pk := range pks {
				if !tx.isLocallyDeleted(tableName, pk) {
					keySet[pk] = struct{}{}
				}
			}
		}
	}
	td.mu.RUnlock()
	tx.engine.mu.RUnlock()

	// Check uncommitted local rows in transaction
	if localRows, ok := tx.local[tableName]; ok {
		for pk, locRec := range localRows {
			if tx.isLocallyDeleted(tableName, pk) {
				continue
			}
			partID, err := td.Schema.ComputePartitionKey(locRec)
			if err == nil && partSet[partID] {
				keySet[pk] = struct{}{}
			}
		}
	}

	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	return &memCursor{
		tx:        tx,
		tableName: tableName,
		td:        td,
		keys:      keys,
		idx:       -1,
	}, nil
}

func (tx *Transaction) IndexScan(tableName string, indexName string, low, high *storage.Value) (storage.Cursor, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return nil, storage.ErrTxClosed
	}

	tx.engine.mu.RLock()
	td, exists := tx.engine.tables[tableName]
	if !exists {
		tx.engine.mu.RUnlock()
		return nil, fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	idxData, exists := td.Indexes[indexName]
	if !exists {
		tx.engine.mu.RUnlock()
		return nil, fmt.Errorf("%w: %s on table %s", storage.ErrIndexNotFound, indexName, tableName)
	}

	candidatePKs := idxData.ScanRange(low, high)
	tx.engine.mu.RUnlock()

	pks := make([]string, 0, len(candidatePKs))
	seen := make(map[string]bool, len(candidatePKs))

	for _, pk := range candidatePKs {
		if seen[pk] {
			continue
		}
		seen[pk] = true

		if tx.isLocallyDeleted(tableName, pk) {
			continue
		}

		if locRec, inLocal := tx.getLocal(tableName, pk); inLocal {
			idxVal := extractIndexKey(locRec, idxData.Schema.Columns)
			if (low == nil || idxVal.Compare(*low) >= 0) && (high == nil || idxVal.Compare(*high) <= 0) {
				pks = append(pks, pk)
			}
			continue
		}

		pks = append(pks, pk)
	}

	if localRows, ok := tx.local[tableName]; ok {
		for pk, locRec := range localRows {
			if seen[pk] || tx.isLocallyDeleted(tableName, pk) {
				continue
			}
			idxVal := extractIndexKey(locRec, idxData.Schema.Columns)
			if (low == nil || idxVal.Compare(*low) >= 0) && (high == nil || idxVal.Compare(*high) <= 0) {
				pks = append(pks, pk)
				seen[pk] = true
			}
		}
	}

	return &memCursor{
		tx:        tx,
		tableName: tableName,
		td:        td,
		keys:      pks,
		idx:       -1,
	}, nil
}

// IndexScanDesc scans the secondary index in descending order.
func (tx *Transaction) IndexScanDesc(tableName string, indexName string, low, high *storage.Value) (storage.Cursor, error) {
	cur, err := tx.IndexScan(tableName, indexName, low, high)
	if err != nil {
		return nil, err
	}
	mCur, ok := cur.(*memCursor)
	if !ok {
		return cur, nil
	}
	n := len(mCur.keys)
	revKeys := make([]string, n)
	for i, pk := range mCur.keys {
		revKeys[n-1-i] = pk
	}
	mCur.keys = revKeys
	return mCur, nil
}

// ParsePKValue parses a string-encoded primary key back into a storage.Value.
func ParsePKValue(s string, dt storage.DataType) storage.Value {
	switch dt {
	case storage.TypeInt:
		var n int64
		_, _ = fmt.Sscanf(s, "%d", &n)
		return storage.IntValue(n)
	case storage.TypeFloat:
		var f float64
		_, _ = fmt.Sscanf(s, "%f", &f)
		return storage.FloatValue(f)
	case storage.TypeDecimal:
		return storage.DecimalValue(s)
	case storage.TypeBool:
		return storage.BoolValue(strings.EqualFold(s, "true") || s == "1")
	case storage.TypeDateTime:
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return storage.DateTimeValue(t)
		}
		return storage.StringValue(s)
	case storage.TypeDate:
		return storage.DateValueFromString(s)
	case storage.TypeTime:
		return storage.TimeValueFromString(s)
	case storage.TypeUUID:
		return storage.UUIDValue(s)
	case storage.TypeBinary:
		return storage.BinaryValueFromString(s)
	default:
		return storage.StringValue(s)
	}
}

func (tx *Transaction) IndexOnlyScan(tableName string, indexName string, low, high *storage.Value) (storage.Cursor, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return nil, storage.ErrTxClosed
	}

	tx.engine.mu.RLock()
	td, exists := tx.engine.tables[tableName]
	if !exists {
		tx.engine.mu.RUnlock()
		return nil, fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	idxData, exists := td.Indexes[indexName]
	if !exists {
		tx.engine.mu.RUnlock()
		return nil, fmt.Errorf("%w: %s on table %s", storage.ErrIndexNotFound, indexName, tableName)
	}

	pkCol, _ := td.Schema.PrimaryKeyColumn()
	candidateEntries := idxData.ScanRangeEntries(low, high)
	tx.engine.mu.RUnlock()

	pks := make([]string, 0, len(candidateEntries))
	keyMap := make(map[string]storage.Value, len(candidateEntries))
	seen := make(map[string]bool, len(candidateEntries))

	for _, entry := range candidateEntries {
		pk := entry.PK
		if seen[pk] {
			continue
		}
		seen[pk] = true

		if tx.isLocallyDeleted(tableName, pk) {
			continue
		}

		if locRec, inLocal := tx.getLocal(tableName, pk); inLocal {
			idxVal := extractIndexKey(locRec, idxData.Schema.Columns)
			if (low == nil || idxVal.Compare(*low) >= 0) && (high == nil || idxVal.Compare(*high) <= 0) {
				pks = append(pks, pk)
			}
			continue
		}

		pks = append(pks, pk)
		keyMap[pk] = entry.Key
	}

	if localRows, ok := tx.local[tableName]; ok {
		for pk, locRec := range localRows {
			if seen[pk] || tx.isLocallyDeleted(tableName, pk) {
				continue
			}
			idxVal := extractIndexKey(locRec, idxData.Schema.Columns)
			if (low == nil || idxVal.Compare(*low) >= 0) && (high == nil || idxVal.Compare(*high) <= 0) {
				pks = append(pks, pk)
				seen[pk] = true
			}
		}
	}

	var colName string
	if len(idxData.Schema.Columns) > 0 {
		colName = idxData.Schema.Columns[0]
	}

	resolver := func(pk string) (storage.Record, bool) {
		keyVal, ok := keyMap[pk]
		if !ok {
			return nil, false
		}
		rec := make(storage.Record, 2)
		if colName != "" {
			rec[colName] = keyVal
		}
		if pkCol != nil {
			rec[pkCol.Name] = ParsePKValue(pk, pkCol.Type)
		}
		return rec, true
	}

	return &memCursor{
		tx:        tx,
		tableName: tableName,
		td:        td,
		keys:      pks,
		idx:       -1,
		resolver:  resolver,
	}, nil
}

func (tx *Transaction) Commit() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	tx.closed = true

	if tx.opts.ReadOnly || len(tx.changes) == 0 {
		return nil
	}

	// 1. Collect distinct table names affected by this transaction
	tableSet := make(map[string]struct{})
	for _, ch := range tx.changes {
		tableSet[ch.table] = struct{}{}
	}
	tableNames := make([]string, 0, len(tableSet))
	for name := range tableSet {
		tableNames = append(tableNames, name)
	}
	sort.Strings(tableNames) // Strict Lock Hierarchy to prevent deadlocks (Rule 10)

	// 2. Acquire shared read lock on engine catalog to resolve table pointers
	tx.engine.mu.RLock()
	defer tx.engine.mu.RUnlock()

	// 3. Acquire exclusive lock on each affected table in sorted canonical order
	affectedTables := make([]*TableData, 0, len(tableNames))
	for _, name := range tableNames {
		td, exists := tx.engine.tables[name]
		if exists {
			td.mu.Lock()
			affectedTables = append(affectedTables, td)
		}
	}
	defer func() {
		for i := len(affectedTables) - 1; i >= 0; i-- {
			affectedTables[i].mu.Unlock()
		}
	}()

	// 1. Verify OCC version conflicts and unique constraints
	for _, ch := range tx.changes {
		td, exists := tx.engine.tables[ch.table]
		if !exists {
			continue
		}
		curVer := td.Versions[ch.key]
		if ch.kind == changeInsert {
			if _, exists := td.Rows[ch.key]; exists {
				return storage.ErrSerializationFailure
			}
			for _, idxData := range td.Indexes {
				if idxData.Schema.Unique {
					keyVal := extractIndexKey(ch.rec, idxData.Schema.Columns)
					existing := idxData.ScanRange(&keyVal, &keyVal)
					for _, epk := range existing {
						if epk != ch.key {
							return fmt.Errorf("%w: duplicate key %q in unique index %q", storage.ErrUniqueViolation, keyVal.String(), idxData.Schema.Name)
						}
					}
				}
			}
		} else {
			if curVer != ch.baseVer {
				return storage.ErrSerializationFailure
			}
			if ch.kind == changeUpdate {
				for _, idxData := range td.Indexes {
					if idxData.Schema.Unique {
						keyVal := extractIndexKey(ch.rec, idxData.Schema.Columns)
						existing := idxData.ScanRange(&keyVal, &keyVal)
						for _, epk := range existing {
							if epk != ch.key {
								return fmt.Errorf("%w: duplicate key %q in unique index %q", storage.ErrUniqueViolation, keyVal.String(), idxData.Schema.Name)
							}
						}
					}
				}
			}
		}
	}

	// 2. Apply changes and update secondary indexes atomically
	for _, ch := range tx.changes {
		td, exists := tx.engine.tables[ch.table]
		if !exists {
			continue
		}
		oldRec, hadOld := td.Rows[ch.key]

		switch ch.kind {
		case changeInsert:
			td.Rows[ch.key] = ch.rec
			td.Versions[ch.key] = 1
			if td.Schema.IsPartitioned() {
				if partID, err := td.Schema.ComputePartitionKey(ch.rec); err == nil {
					td.addPartitionKey(partID, ch.key)
				}
			}
			for _, idxData := range td.Indexes {
				newKey := extractIndexKey(ch.rec, idxData.Schema.Columns)
				_ = idxData.Insert(newKey, ch.key)
			}
		case changeUpdate:
			td.Rows[ch.key] = ch.rec
			td.Versions[ch.key] = ch.baseVer + 1
			if td.Schema.IsPartitioned() {
				var oldPartID, newPartID string
				if hadOld {
					oldPartID, _ = td.Schema.ComputePartitionKey(oldRec)
				}
				newPartID, _ = td.Schema.ComputePartitionKey(ch.rec)
				if oldPartID != newPartID {
					if oldPartID != "" {
						td.removePartitionKey(oldPartID, ch.key)
					}
					if newPartID != "" {
						td.addPartitionKey(newPartID, ch.key)
					}
				}
			}
			for _, idxData := range td.Indexes {
				if hadOld {
					oldKey := extractIndexKey(oldRec, idxData.Schema.Columns)
					idxData.Delete(oldKey, ch.key)
				}
				newKey := extractIndexKey(ch.rec, idxData.Schema.Columns)
				_ = idxData.Insert(newKey, ch.key)
			}
		case changeDelete:
			delete(td.Rows, ch.key)
			delete(td.Versions, ch.key)
			if td.Schema.IsPartitioned() && hadOld {
				if partID, err := td.Schema.ComputePartitionKey(oldRec); err == nil {
					td.removePartitionKey(partID, ch.key)
				}
			}
			if hadOld {
				for _, idxData := range td.Indexes {
					oldKey := extractIndexKey(oldRec, idxData.Schema.Columns)
					idxData.Delete(oldKey, ch.key)
				}
			}
		}
	}

	// 3. Append atomic WAL record to AOF log if enabled
	if tx.engine.wal != nil && len(tx.changes) > 0 {
		ops := make([]WALOp, 0, len(tx.changes))
		for _, ch := range tx.changes {
			var opType WALOpType
			switch ch.kind {
			case changeInsert:
				opType = OpInsert
			case changeUpdate:
				opType = OpUpdate
			case changeDelete:
				opType = OpDelete
			}
			ops = append(ops, WALOp{
				Type:    opType,
				Table:   ch.table,
				Key:     ch.key,
				Record:  ch.rec,
				BaseVer: ch.baseVer,
			})
		}
		_ = tx.engine.wal.Append(WALRecord{
			LSN:       tx.engine.nextLSN.Add(1),
			Timestamp: time.Now().UnixNano(),
			Ops:       ops,
		})

		if tx.engine.aofThreshold > 0 && tx.engine.wal.Size() > tx.engine.aofThreshold {
			_ = tx.engine.rewriteAOFLocked()
		}
	}

	tx.savepoints = nil
	tx.spNames = nil
	return nil
}

func (tx *Transaction) Rollback() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	tx.closed = true
	tx.changes = nil
	tx.local = nil
	tx.baseVers = nil
	tx.deleted = nil
	tx.savepoints = nil
	tx.spNames = nil
	return nil
}

func (tx *Transaction) Savepoint(name string) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}

	normName := strings.ToLower(strings.TrimSpace(name))
	if normName == "" {
		return fmt.Errorf("savepoint name cannot be empty")
	}

	if tx.savepoints == nil {
		tx.savepoints = make(map[string]savepointState)
	}

	state := savepointState{
		local:    cloneLocalMap(tx.local),
		baseVers: cloneBaseVersMap(tx.baseVers),
		deleted:  cloneDeletedMap(tx.deleted),
		changes:  cloneChangesMap(tx.changes),
	}

	found := false
	for _, n := range tx.spNames {
		if n == normName {
			found = true
			break
		}
	}
	if !found {
		tx.spNames = append(tx.spNames, normName)
	}
	tx.savepoints[normName] = state
	return nil
}

func (tx *Transaction) RollbackTo(name string) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}

	normName := strings.ToLower(strings.TrimSpace(name))
	state, exists := tx.savepoints[normName]
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrSavepointNotFound, name)
	}

	// Restore snapshot state
	tx.local = cloneLocalMap(state.local)
	tx.baseVers = cloneBaseVersMap(state.baseVers)
	tx.deleted = cloneDeletedMap(state.deleted)
	tx.changes = cloneChangesMap(state.changes)

	// Invalidate subsequent savepoints defined after this savepoint
	idx := -1
	for i, n := range tx.spNames {
		if n == normName {
			idx = i
			break
		}
	}
	if idx >= 0 {
		for i := idx + 1; i < len(tx.spNames); i++ {
			delete(tx.savepoints, tx.spNames[i])
		}
		tx.spNames = tx.spNames[:idx+1]
	}

	return nil
}

func (tx *Transaction) ReleaseSavepoint(name string) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}

	normName := strings.ToLower(strings.TrimSpace(name))
	if _, exists := tx.savepoints[normName]; !exists {
		return fmt.Errorf("%w: %s", storage.ErrSavepointNotFound, name)
	}

	idx := -1
	for i, n := range tx.spNames {
		if n == normName {
			idx = i
			break
		}
	}
	if idx >= 0 {
		for i := idx; i < len(tx.spNames); i++ {
			delete(tx.savepoints, tx.spNames[i])
		}
		tx.spNames = tx.spNames[:idx]
	}

	return nil
}

func cloneLocalMap(m map[string]map[string]storage.Record) map[string]map[string]storage.Record {
	res := make(map[string]map[string]storage.Record, len(m))
	for tbl, rows := range m {
		tblMap := make(map[string]storage.Record, len(rows))
		for k, rec := range rows {
			tblMap[k] = cloneRecord(rec)
		}
		res[tbl] = tblMap
	}
	return res
}

func cloneBaseVersMap(m map[string]map[string]int64) map[string]map[string]int64 {
	res := make(map[string]map[string]int64, len(m))
	for tbl, rows := range m {
		tblMap := make(map[string]int64, len(rows))
		for k, v := range rows {
			tblMap[k] = v
		}
		res[tbl] = tblMap
	}
	return res
}

func cloneDeletedMap(m map[string]map[string]bool) map[string]map[string]bool {
	res := make(map[string]map[string]bool, len(m))
	for tbl, rows := range m {
		tblMap := make(map[string]bool, len(rows))
		for k, v := range rows {
			tblMap[k] = v
		}
		res[tbl] = tblMap
	}
	return res
}

func cloneChangesMap(m map[string]rowChange) map[string]rowChange {
	res := make(map[string]rowChange, len(m))
	for k, ch := range m {
		chCopy := ch
		if ch.rec != nil {
			chCopy.rec = cloneRecord(ch.rec)
		}
		res[k] = chCopy
	}
	return res
}

func (tx *Transaction) getLocal(table, key string) (storage.Record, bool) {
	if t, ok := tx.local[table]; ok {
		r, exists := t[key]
		return r, exists
	}
	return nil, false
}

func (tx *Transaction) getBaseVer(table, key string) int64 {
	if t, ok := tx.baseVers[table]; ok {
		return t[key]
	}
	return 0
}

func (tx *Transaction) setBaseVer(table, key string, ver int64) {
	v, ok := tx.baseVers[table]
	if !ok {
		v = make(map[string]int64)
		tx.baseVers[table] = v
	}
	if _, has := v[key]; !has {
		v[key] = ver
	}
}

func (tx *Transaction) putLocal(table, key string, rec storage.Record, baseVer int64) {
	t, ok := tx.local[table]
	if !ok {
		t = make(map[string]storage.Record)
		tx.local[table] = t
	}
	t[key] = rec
	tx.setBaseVer(table, key, baseVer)
}

func (tx *Transaction) removeLocal(table, key string) {
	if t, ok := tx.local[table]; ok {
		delete(t, key)
	}
}

func (tx *Transaction) markDeleted(table, key string) {
	d, ok := tx.deleted[table]
	if !ok {
		d = make(map[string]bool)
		tx.deleted[table] = d
	}
	d[key] = true
}

func (tx *Transaction) unmarkDeleted(table, key string) {
	if d, ok := tx.deleted[table]; ok {
		delete(d, key)
	}
}

func (tx *Transaction) isLocallyDeleted(table, key string) bool {
	if d, ok := tx.deleted[table]; ok {
		return d[key]
	}
	return false
}

func cloneRecord(r storage.Record) storage.Record {
	cp := make(storage.Record, len(r))
	for k, v := range r {
		cp[k] = v
	}
	return cp
}

type memCursor struct {
	tx        *Transaction
	tableName string
	td        *TableData
	keys      []string
	idx       int
	curRec    storage.Record
	closed    bool
	resolver  func(pk string) (storage.Record, bool)
}

func (c *memCursor) Next() bool {
	if c.closed {
		return false
	}
	for {
		c.idx++
		if c.idx >= len(c.keys) {
			c.curRec = nil
			return false
		}
		pk := c.keys[c.idx]
		if c.tx != nil {
			c.tx.mu.Lock()
			isDel := c.tx.isLocallyDeleted(c.tableName, pk)
			locRec, inLocal := c.tx.getLocal(c.tableName, pk)
			c.tx.mu.Unlock()
			if isDel {
				continue
			}
			if inLocal {
				c.curRec = locRec
				return true
			}
		}
		if c.resolver != nil {
			if rec, ok := c.resolver(pk); ok {
				c.curRec = rec
				return true
			}
			continue
		}
		if c.td != nil {
			c.td.mu.RLock()
			rec, exists := c.td.Rows[pk]
			c.td.mu.RUnlock()
			if exists {
				c.curRec = rec
				return true
			}
		}
	}
}

func (c *memCursor) Record() (storage.Record, error) {
	if c.closed || c.curRec == nil {
		return nil, fmt.Errorf("cursor out of range or closed")
	}
	return c.curRec, nil
}

func (c *memCursor) Close() error {
	c.closed = true
	c.keys = nil
	c.curRec = nil
	c.resolver = nil
	c.tx = nil
	c.td = nil
	return nil
}


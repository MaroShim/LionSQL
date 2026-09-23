package memengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// SyncEntries exports the in-memory SkipList into the Entries slice for disk snapshot persistence.
func (idx *IndexData) SyncEntries() {
	if idx.sl != nil {
		idx.Entries = idx.sl.ToEntries()
	}
}

// TableData stores rows in memory for a single table with OCC version tracking and secondary indexes.
type TableData struct {
	Schema   storage.TableSchema       `json:"schema"`
	Rows     map[string]storage.Record `json:"rows"`
	Versions map[string]int64          `json:"versions"`
	Keys     []string                  `json:"keys"`
	Indexes  map[string]*IndexData     `json:"indexes,omitempty"`
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

func (e *Engine) applyWALRecordLocked(rec WALRecord) error {
	for _, op := range rec.Ops {
		switch op.Type {
		case OpCreateTable:
			if op.Schema == nil {
				continue
			}
			e.tables[op.Schema.Name] = &TableData{
				Schema:   *op.Schema,
				Rows:     make(map[string]storage.Record),
				Versions: make(map[string]int64),
				Keys:     make([]string, 0),
				Indexes:  make(map[string]*IndexData),
			}
		case OpDropTable:
			delete(e.tables, op.Table)
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

	td := &TableData{
		Schema:   *schema,
		Rows:     make(map[string]storage.Record),
		Versions: make(map[string]int64),
		Keys:     make([]string, 0),
		Indexes:  make(map[string]*IndexData),
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
	var str string
	for i, c := range cols {
		if i > 0 {
			str += "\x00"
		}
		if v, ok := rec[c]; ok {
			str += v.String()
		} else {
			str += "NULL"
		}
	}
	return storage.StringValue(str)
}

func (e *Engine) DropTable(tableName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.tables[tableName]; !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
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
		local:    make(map[string]map[string]storage.Record),
		baseVers: make(map[string]map[string]int64),
		deleted:  make(map[string]map[string]bool),
		changes:  make(map[string]rowChange),
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

// Transaction handles copy-on-write delta mutations with zero start overhead, OCC validation, and atomic commit.
type Transaction struct {
	mu       sync.Mutex
	engine   *Engine
	opts     storage.TxOptions
	local    map[string]map[string]storage.Record // tableName -> key -> record
	baseVers map[string]map[string]int64          // tableName -> key -> baseVersion
	deleted  map[string]map[string]bool           // tableName -> key -> bool
	changes  map[string]rowChange                 // "table:key" -> rowChange
	closed   bool
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
			if _, inEngine := td.Rows[pkKey]; inEngine {
				tx.engine.mu.RUnlock()
				return fmt.Errorf("%w: key %q in table %q", storage.ErrKeyExists, pkKey, tableName)
			}
		}
		tx.engine.mu.RUnlock()
	}

	// Validate unique index constraints
	tx.engine.mu.RLock()
	if td, ok := tx.engine.tables[tableName]; ok {
		for _, idxData := range td.Indexes {
			if idxData.Schema.Unique {
				keyVal := extractIndexKey(record, idxData.Schema.Columns)
				existing := idxData.ScanRange(&keyVal, &keyVal)
				for _, epk := range existing {
					if epk != pkKey && !tx.isLocallyDeleted(tableName, epk) {
						tx.engine.mu.RUnlock()
						return fmt.Errorf("%w: duplicate key %q in unique index %q", storage.ErrUniqueViolation, keyVal.String(), idxData.Schema.Name)
					}
				}
			}
		}
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
			} else {
				return fmt.Errorf("%w: column %q expects %s, got %s",
					storage.ErrInvalidDataType, col.Name, col.Type, val.Type)
			}
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

	pkKey := pk.String()
	base, ver, err := tx.getRecordAndVerUnderLock(tableName, pkKey)
	if err != nil {
		return err
	}

	tx.setBaseVer(tableName, pkKey, ver)
	originalBaseVer := tx.getBaseVer(tableName, pkKey)

	updated := cloneRecord(base)
	for col, val := range record {
		updated[col] = val
	}

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

	pkKey := pk.String()
	_, ver, err := tx.getRecordAndVerUnderLock(tableName, pkKey)
	if err != nil {
		return err
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

func (tx *Transaction) Get(tableName string, pk storage.Value) (storage.Record, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return nil, storage.ErrTxClosed
	}

	pkKey := pk.String()
	rec, ver, err := tx.getRecordAndVerUnderLock(tableName, pkKey)
	if err == nil {
		tx.setBaseVer(tableName, pkKey, ver)
	}
	return rec, err
}

func (tx *Transaction) getRecordAndVerUnderLock(tableName, pkKey string) (storage.Record, int64, error) {
	if tx.isLocallyDeleted(tableName, pkKey) {
		return nil, 0, fmt.Errorf("%w: key %q in table %q", storage.ErrKeyNotFound, pkKey, tableName)
	}

	if rec, found := tx.getLocal(tableName, pkKey); found {
		ver := tx.getBaseVer(tableName, pkKey)
		return rec, ver, nil
	}

	tx.engine.mu.RLock()
	defer tx.engine.mu.RUnlock()

	td, exists := tx.engine.tables[tableName]
	if !exists {
		return nil, 0, fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	rec, exists := td.Rows[pkKey]
	if !exists {
		return nil, 0, fmt.Errorf("%w: key %q in table %q", storage.ErrKeyNotFound, pkKey, tableName)
	}

	ver := td.Versions[pkKey]
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

	// Zero-Copy Direct View: build merged map referencing rows directly without cloneRecord
	merged := make(map[string]storage.Record, len(td.Rows))
	for k, v := range td.Rows {
		if !tx.isLocallyDeleted(tableName, k) {
			merged[k] = v
		}
	}
	tx.engine.mu.RUnlock()

	if localRows, ok := tx.local[tableName]; ok {
		for k, v := range localRows {
			if !tx.isLocallyDeleted(tableName, k) {
				merged[k] = v
			}
		}
	}

	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	records := make([]storage.Record, len(keys))
	for i, k := range keys {
		records[i] = merged[k]
	}

	return &memCursor{
		records: records,
		idx:     -1,
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

	var records []storage.Record
	seen := make(map[string]bool)

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
				records = append(records, locRec)
			}
			continue
		}

		tx.engine.mu.RLock()
		if rec, exists := td.Rows[pk]; exists {
			records = append(records, rec)
		}
		tx.engine.mu.RUnlock()
	}

	if localRows, ok := tx.local[tableName]; ok {
		for pk, locRec := range localRows {
			if seen[pk] || tx.isLocallyDeleted(tableName, pk) {
				continue
			}
			idxVal := extractIndexKey(locRec, idxData.Schema.Columns)
			if (low == nil || idxVal.Compare(*low) >= 0) && (high == nil || idxVal.Compare(*high) <= 0) {
				records = append(records, locRec)
				seen[pk] = true
			}
		}
	}

	return &memCursor{
		records: records,
		idx:     -1,
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

	tx.engine.mu.Lock()
	defer tx.engine.mu.Unlock()

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
			for _, idxData := range td.Indexes {
				newKey := extractIndexKey(ch.rec, idxData.Schema.Columns)
				_ = idxData.Insert(newKey, ch.key)
			}
		case changeUpdate:
			td.Rows[ch.key] = ch.rec
			td.Versions[ch.key] = ch.baseVer + 1
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
	return nil
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
	records []storage.Record
	idx     int
}

func (c *memCursor) Next() bool {
	c.idx++
	return c.idx < len(c.records)
}

func (c *memCursor) Record() (storage.Record, error) {
	if c.idx < 0 || c.idx >= len(c.records) {
		return nil, fmt.Errorf("cursor out of range")
	}
	return c.records[c.idx], nil
}

func (c *memCursor) Close() error {
	c.records = nil
	return nil
}

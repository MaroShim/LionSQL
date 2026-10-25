package memengine

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

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
	if s.engine.isClosed.Load() {
		return nil, fmt.Errorf("engine is closed")
	}

	snapLSN := opts.SnapshotLSN
	if snapLSN == 0 {
		snapLSN = s.engine.CurrentLSN()
	}
	snapTS := opts.SnapshotTimestamp
	if snapTS == 0 {
		snapTS = time.Now().UnixNano()
	}

	tx := txPool.Get().(*Transaction)
	tx.mu.Lock()
	tx.engine = s.engine
	tx.opts = opts
	tx.snapshotLSN = snapLSN
	tx.snapshotTimestamp = snapTS
	tx.commitLSN = 0
	tx.commitTimestamp = 0
	tx.closed = false
	tx.inPool = false

	if tx.tableCache == nil || len(tx.tableCache) > 64 {
		tx.tableCache = make(map[string]*TableData, 4)
	} else {
		clear(tx.tableCache)
	}

	if !opts.ReadOnly {
		if tx.baseVers == nil {
			tx.baseVers = make(map[string]map[string]int64, 4)
		} else {
			for _, m := range tx.baseVers {
				clear(m)
			}
			clear(tx.baseVers)
		}

		if tx.local == nil {
			tx.local = make(map[string]map[string]storage.Row, 4)
		} else {
			for _, m := range tx.local {
				clear(m)
			}
			clear(tx.local)
		}

		if tx.deleted == nil {
			tx.deleted = make(map[string]map[string]bool, 4)
		} else {
			for _, m := range tx.deleted {
				clear(m)
			}
			clear(tx.deleted)
		}

		if tx.changes == nil || len(tx.changes) > 1024 {
			tx.changes = make(map[string]rowChange, 16)
		} else {
			clear(tx.changes)
		}

		if tx.savepoints == nil || len(tx.savepoints) > 16 {
			tx.savepoints = make(map[string]savepointState, 2)
		} else {
			clear(tx.savepoints)
		}
		tx.spNames = tx.spNames[:0]
	}
	tx.mu.Unlock()
	return tx, nil
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
	kind     changeType
	table    string
	key      string
	baseVer  int64
	row      storage.Row
	rec      storage.Record
	td       *TableData
	shardIdx int
	walData  []byte
}

type savepointState struct {
	local    map[string]map[string]storage.Row
	baseVers map[string]map[string]int64
	deleted  map[string]map[string]bool
	changes  map[string]rowChange
}

type shardRef struct {
	tableName string
	shardIdx  int
	td        *TableData
}

type shardMask [numTableShards / 64]uint64

var txPool = sync.Pool{
	New: func() any {
		return &Transaction{
			tableCache:   make(map[string]*TableData, 4),
			baseVers:     make(map[string]map[string]int64, 4),
			local:        make(map[string]map[string]storage.Row, 4),
			deleted:      make(map[string]map[string]bool, 4),
			changes:      make(map[string]rowChange, 16),
			savepoints:   make(map[string]savepointState, 2),
			tableSet:     make(map[string]*TableData, 4),
			shardMasks:   make(map[string]shardMask, 4),
			tableNames:   make([]string, 0, 4),
			neededShards: make([]shardRef, 0, 16),
		}
	},
}

// Transaction handles copy-on-write delta mutations with zero start overhead, OCC validation, and atomic commit.
type Transaction struct {
	mu                sync.Mutex
	engine            *Engine
	opts              storage.TxOptions
	snapshotLSN       int64
	snapshotTimestamp int64
	commitLSN         int64
	commitTimestamp   int64
	local             map[string]map[string]storage.Row // tableName -> key -> row
	baseVers          map[string]map[string]int64       // tableName -> key -> baseVersion
	deleted           map[string]map[string]bool        // tableName -> key -> bool
	changes           map[string]rowChange              // "table:key" -> rowChange
	tableCache        map[string]*TableData             // Cached table metadata pointers per transaction
	savepoints        map[string]savepointState
	spNames           []string
	tableSet          map[string]*TableData
	shardMasks        map[string]shardMask
	tableNames        []string
	neededShards      []shardRef
	closed            bool
	inPool            bool
}

func (tx *Transaction) releaseToPoolLocked() {
	if tx.inPool {
		return
	}
	tx.inPool = true
	txPool.Put(tx)
}

// SnapshotLSN returns the snapshot LSN bound at transaction start.
func (tx *Transaction) SnapshotLSN() int64 {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.snapshotLSN
}

// SnapshotTimestamp returns the snapshot unix nano timestamp bound at transaction start.
func (tx *Transaction) SnapshotTimestamp() int64 {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.snapshotTimestamp
}

// CommitLSN returns the committed LSN (0 if uncommitted or rolled back).
func (tx *Transaction) CommitLSN() int64 {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.commitLSN
}

// CommitTimestamp returns the committed unix nano timestamp (0 if uncommitted or rolled back).
func (tx *Transaction) CommitTimestamp() int64 {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.commitTimestamp
}

// getTableLocked retrieves a *TableData, caching it per-transaction using an atomic snapshot to eliminate engine.mu RLock contention.
func (tx *Transaction) getTableLocked(tableName string) (*TableData, error) {
	if td, cached := sqlutil.MapGetFold(tx.tableCache, tableName); cached {
		return td, nil
	}
	snap := tx.engine.tablesAtomic.Load()
	if snap != nil {
		if td, exists := sqlutil.MapGetFold(*snap, tableName); exists {
			tx.tableCache[tableName] = td
			return td, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
}

// getTableReadOnly retrieves a *TableData without acquiring tx.mu or engine.mu, safe for ReadOnly transactions.
func (tx *Transaction) getTableReadOnly(tableName string) (*TableData, error) {
	snap := tx.engine.tablesAtomic.Load()
	if snap != nil {
		if td, exists := sqlutil.MapGetFold(*snap, tableName); exists {
			return td, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
}

func (tx *Transaction) Insert(tableName string, record storage.Record) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	if tx.opts.ReadOnly {
		return storage.ErrReadOnlyTransaction
	}

	return tx.insertLocked(tableName, record)
}

func (tx *Transaction) InsertRow(tableName string, row storage.Row) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	if tx.opts.ReadOnly {
		return storage.ErrReadOnlyTransaction
	}

	td, err := tx.getTableLocked(tableName)
	if err != nil {
		return err
	}
	schema := td.Schema

	// Fast-path for tables without secondary indexes, foreign keys, or partitioning
	if len(schema.Indexes) == 0 && len(schema.ForeignKeys) == 0 && !schema.IsPartitioned() {
		pkIdx := schema.PrimaryKeyIndex()
		if pkIdx >= 0 && pkIdx < len(row.Values) {
			pkVal := row.Values[pkIdx]
			if !pkVal.IsNull {
				pkKey := pkVal.String()
				if tx.isLocallyDeleted(schema.Name, pkKey) {
				} else if _, inLocal := tx.getLocal(schema.Name, pkKey); inLocal {
					return fmt.Errorf("%w: key %q in table %q", storage.ErrKeyExists, pkKey, schema.Name)
				} else if td.hasKey(pkKey) {
					return fmt.Errorf("%w: key %q in table %q", storage.ErrKeyExists, pkKey, schema.Name)
				}
				rowClone := cloneRow(row)
				tx.putLocal(schema.Name, pkKey, rowClone, 0)
				tx.unmarkDeleted(schema.Name, pkKey)
				tx.changes[schema.Name+":"+pkKey] = rowChange{
					kind:     changeInsert,
					table:    schema.Name,
					key:      pkKey,
					baseVer:  0,
					row:      rowClone,
					td:       td,
					shardIdx: hashShard(pkKey),
				}
				return nil
			}
		}
	}

	rec := schema.RowToRecord(row)
	return tx.insertLocked(tableName, rec)
}

func (tx *Transaction) insertLocked(tableName string, record storage.Record) error {
	td, err := tx.getTableLocked(tableName)
	if err != nil {
		return err
	}
	tableName = td.Schema.Name
	schema := td.Schema

	normalized := make(storage.Record, len(record))
	for k, v := range record {
		if col, _, ok := schema.FindColumn(k); ok {
			normalized.Set(col.Name, v)
		} else {
			normalized.Set(k, v)
		}
	}
	record = normalized

	pkCol, err := schema.PrimaryKeyColumn()
	if err != nil {
		return err
	}

	pkVal, hasPk := record.Get(pkCol.Name)
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
		if td.hasKey(pkKey) {
			return fmt.Errorf("%w: key %q in table %q", storage.ErrKeyExists, pkKey, tableName)
		}
	}

	// Validate unique index constraints
	td.mu.RLock()
	for _, idxData := range td.Indexes {
		if idxData.Schema.Unique {
			keyVal := extractIndexKeyFromRecord(record, &idxData.Schema)
			if !keyVal.IsNull {
				existing := idxData.ScanRange(&keyVal, &keyVal)
				for _, epk := range existing {
					if epk != pkKey && !tx.isLocallyDeleted(tableName, epk) {
						td.mu.RUnlock()
						return fmt.Errorf("%w: duplicate key %q in unique index %q", storage.ErrUniqueViolation, keyVal.String(), idxData.Schema.Name)
					}
				}
			}
		}
	}
	td.mu.RUnlock()

	if localRows, ok := tx.local[tableName]; ok {
		for epk, locRow := range localRows {
			if epk != pkKey && !tx.isLocallyDeleted(tableName, epk) {
				for _, idx := range schema.Indexes {
					if idx.Unique {
						keyVal := extractIndexKeyFromRecord(record, &idx)
						locVal := extractIndexKeyFromRowWithSchema(&schema, locRow, &idx)
						if !keyVal.IsNull && !locVal.IsNull && keyVal.Equal(locVal) {
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
			} else if col.Type == storage.TypeBool && (val.Type == storage.TypeInt || val.Type == storage.TypeFloat || val.Type == storage.TypeVarChar || val.Type == storage.TypeDecimal) {
				val = storage.BoolValueFromAny(val)
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
	rowCopy := schema.RecordToRow(record)
	tx.putLocal(tableName, pkKey, rowCopy, 0)
	tx.unmarkDeleted(tableName, pkKey)

	changeKey := tableName + ":" + pkKey
	tx.changes[changeKey] = rowChange{
		kind:     changeInsert,
		table:    tableName,
		key:      pkKey,
		baseVer:  0,
		row:      rowCopy,
		rec:      recCopy,
		td:       td,
		shardIdx: hashShard(pkKey),
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
		return storage.ErrReadOnlyTransaction
	}

	return tx.updateLocked(tableName, pk, record, 0)
}

func (tx *Transaction) UpdateRow(tableName string, pk storage.Value, row storage.Row) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.ErrTxClosed
	}
	if tx.opts.ReadOnly {
		return storage.ErrReadOnlyTransaction
	}

	td, err := tx.getTableLocked(tableName)
	if err != nil {
		return err
	}
	schema := td.Schema

	// Fast-path for tables without secondary indexes, foreign keys, child FK refs, or partitioning
	if len(schema.Indexes) == 0 && len(schema.ForeignKeys) == 0 && len(tx.engine.GetChildFKRefs(schema.Name)) == 0 && !schema.IsPartitioned() {
		pkKey := pk.String()
		_, ver, err := tx.getRowAndVerUnderLock(schema.Name, pkKey, false)
		if err != nil {
			return err
		}
		tx.setBaseVer(schema.Name, pkKey, ver)
		originalBaseVer := tx.getBaseVer(schema.Name, pkKey)

		rowClone := cloneRow(row)
		for i, col := range schema.Columns {
			if i < len(rowClone.Values) && rowClone.Values[i].Type != col.Type && !rowClone.Values[i].IsNull {
				if col.Type == storage.TypeFloat && rowClone.Values[i].Type == storage.TypeInt {
					rowClone.Values[i] = storage.FloatValue(float64(rowClone.Values[i].IVal))
				} else if col.Type == storage.TypeInt && rowClone.Values[i].Type == storage.TypeFloat && rowClone.Values[i].FVal == float64(int64(rowClone.Values[i].FVal)) {
					rowClone.Values[i] = storage.IntValue(int64(rowClone.Values[i].FVal))
				}
			}
		}
		tx.putLocal(schema.Name, pkKey, rowClone, originalBaseVer)
		tx.unmarkDeleted(schema.Name, pkKey)
		tx.changes[schema.Name+":"+pkKey] = rowChange{
			kind:     changeUpdate,
			table:    schema.Name,
			key:      pkKey,
			baseVer:  originalBaseVer,
			row:      rowClone,
			td:       td,
			shardIdx: hashShard(pkKey),
		}
		return nil
	}

	rec := schema.RowToRecord(row)
	return tx.updateLocked(tableName, pk, rec, 0)
}

func (tx *Transaction) updateLocked(tableName string, pk storage.Value, record storage.Record, depth int) error {
	if depth > 16 {
		return fmt.Errorf("foreign key cascade recursion limit exceeded (max 16)")
	}

	td, err := tx.getTableLocked(tableName)
	if err != nil {
		return err
	}
	tableName = td.Schema.Name
	schema := td.Schema

	pkKey := pk.String()
	base, ver, err := tx.getRecordAndVerUnderLock(tableName, pkKey, false)
	if err != nil {
		return err
	}

	updated := make(storage.Record, len(base))
	for col, val := range base {
		updated[col] = val
	}
	for col, val := range record {
		if colSchema, _, ok := schema.FindColumn(col); ok {
			updated.Set(colSchema.Name, val)
		} else {
			updated.Set(col, val)
		}
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
			} else if col.Type == storage.TypeBool && (val.Type == storage.TypeInt || val.Type == storage.TypeFloat || val.Type == storage.TypeVarChar || val.Type == storage.TypeDecimal) {
				val = storage.BoolValueFromAny(val)
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

	// Validate unique index constraints on updated record
	td.mu.RLock()
	for _, idxData := range td.Indexes {
		if idxData.Schema.Unique {
			keyVal := extractIndexKeyFromRecord(updated, &idxData.Schema)
			if !keyVal.IsNull {
				existing := idxData.ScanRange(&keyVal, &keyVal)
				for _, epk := range existing {
					if epk != pkKey && !tx.isLocallyDeleted(tableName, epk) {
						td.mu.RUnlock()
						return fmt.Errorf("%w: duplicate key %q in unique index %q", storage.ErrUniqueViolation, keyVal.String(), idxData.Schema.Name)
					}
				}
			}
		}
	}
	td.mu.RUnlock()

	if localRows, ok := tx.local[tableName]; ok {
		for epk, locRow := range localRows {
			if epk != pkKey && !tx.isLocallyDeleted(tableName, epk) {
				for _, idx := range schema.Indexes {
					if idx.Unique {
						keyVal := extractIndexKeyFromRecord(updated, &idx)
						locVal := extractIndexKeyFromRowWithSchema(&schema, locRow, &idx)
						if !keyVal.IsNull && !locVal.IsNull && keyVal.Equal(locVal) {
							return fmt.Errorf("%w: duplicate key %q in unique index %q", storage.ErrUniqueViolation, keyVal.String(), idx.Name)
						}
					}
				}
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
	childRefs := tx.engine.GetChildFKRefs(tableName)

	// Stage local update before cascading so that child updates see the new parent row
	newPkKey := pkKey
	if pkCol, err := schema.PrimaryKeyColumn(); err == nil && pkCol != nil {
		if newPkVal, ok := updated[pkCol.Name]; ok && !newPkVal.IsNull {
			newPkKey = newPkVal.String()
		}
	}

	tx.setBaseVer(tableName, pkKey, ver)
	originalBaseVer := tx.getBaseVer(tableName, pkKey)

	updatedRow := schema.RecordToRow(updated)
	if newPkKey != pkKey {
		tx.removeLocal(tableName, pkKey)
		tx.markDeleted(tableName, pkKey)
		tx.changes[tableName+":"+pkKey] = rowChange{
			kind:     changeDelete,
			table:    tableName,
			key:      pkKey,
			baseVer:  originalBaseVer,
			td:       td,
			shardIdx: hashShard(pkKey),
		}

		tx.putLocal(tableName, newPkKey, updatedRow, 0)
		tx.unmarkDeleted(tableName, newPkKey)
		tx.changes[tableName+":"+newPkKey] = rowChange{
			kind:     changeInsert,
			table:    tableName,
			key:      newPkKey,
			baseVer:  0,
			row:      updatedRow,
			rec:      updated,
			td:       td,
			shardIdx: hashShard(newPkKey),
		}
	} else {
		tx.putLocal(tableName, pkKey, updatedRow, originalBaseVer)
		tx.unmarkDeleted(tableName, pkKey)

		changeKey := tableName + ":" + pkKey
		tx.changes[changeKey] = rowChange{
			kind:     changeUpdate,
			table:    tableName,
			key:      pkKey,
			baseVer:  originalBaseVer,
			row:      updatedRow,
			rec:      updated,
			td:       td,
			shardIdx: hashShard(pkKey),
		}
	}

	// Handle cascading updates to child tables if any parent referenced column changed
	for _, cr := range childRefs {
		pCols := cr.FK.ParentColumns
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
			matchingChildPKs, err := tx.findMatchingChildPKsUnderLock(cr.ChildTable, cr.FK, base)
			if err != nil {
				return err
			}
			if len(matchingChildPKs) > 0 {
				switch cr.FK.OnUpdate {
				case storage.ActionCascade:
					for _, cpk := range matchingChildPKs {
						patch := make(storage.Record)
						for i, cCol := range cr.FK.ChildColumns {
							pCol := pCols[i]
							patch[cCol] = updated[pCol]
						}
						if err := tx.updateLocked(cr.ChildTable, cpk, patch, depth+1); err != nil {
							return err
						}
					}
				case storage.ActionSetNull:
					for _, cpk := range matchingChildPKs {
						patch := make(storage.Record)
						for _, cCol := range cr.FK.ChildColumns {
							colType := storage.TypeVarChar
							if childTd, ok := tx.engine.tables[cr.ChildTable]; ok {
								if col, _, found := childTd.Schema.FindColumn(cCol); found {
									colType = col.Type
								}
							}
							patch[cCol] = storage.NullValue(colType)
						}
						if err := tx.updateLocked(cr.ChildTable, cpk, patch, depth+1); err != nil {
							return err
						}
					}
				case storage.ActionRestrict, storage.ActionNoAction:
					return fmt.Errorf("%w: cannot update %q because %q has referencing rows (foreign key %q)",
						storage.ErrForeignKeyViolation, tableName, cr.ChildTable, cr.FK.Name)
				default:
					return fmt.Errorf("%w: cannot update %q because %q has referencing rows (foreign key %q)",
						storage.ErrForeignKeyViolation, tableName, cr.ChildTable, cr.FK.Name)
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
		return storage.ErrReadOnlyTransaction
	}

	return tx.deleteLocked(tableName, pk, 0)
}

func (tx *Transaction) deleteLocked(tableName string, pk storage.Value, depth int) error {
	if depth > 16 {
		return fmt.Errorf("foreign key cascade recursion limit exceeded (max 16)")
	}

	td, err := tx.getTableLocked(tableName)
	if err != nil {
		return err
	}
	tableName = td.Schema.Name

	pkKey := pk.String()
	base, ver, err := tx.getRecordAndVerUnderLock(tableName, pkKey, false)
	if err != nil {
		return err
	}

	// Find all child tables that have a foreign key referencing tableName
	childRefs := tx.engine.GetChildFKRefs(tableName)

	// For each referencing child table, find rows that reference this parent row
	for _, cr := range childRefs {
		matchingChildPKs, err := tx.findMatchingChildPKsUnderLock(cr.ChildTable, cr.FK, base)
		if err != nil {
			return err
		}
		if len(matchingChildPKs) > 0 {
			switch cr.FK.OnDelete {
			case storage.ActionCascade:
				for _, cpk := range matchingChildPKs {
					if err := tx.deleteLocked(cr.ChildTable, cpk, depth+1); err != nil {
						return err
					}
				}
			case storage.ActionSetNull:
				for _, cpk := range matchingChildPKs {
					nullPatch := make(storage.Record)
					for _, cCol := range cr.FK.ChildColumns {
						colType := storage.TypeVarChar
						if childTd, ok := tx.engine.tables[cr.ChildTable]; ok {
							if col, _, found := childTd.Schema.FindColumn(cCol); found {
								colType = col.Type
							}
						}
						nullPatch[cCol] = storage.NullValue(colType)
					}
					if err := tx.updateLocked(cr.ChildTable, cpk, nullPatch, depth+1); err != nil {
						return err
					}
				}
			case storage.ActionRestrict, storage.ActionNoAction:
				return fmt.Errorf("%w: cannot delete from %q because %q has referencing rows (foreign key %q)",
					storage.ErrForeignKeyViolation, tableName, cr.ChildTable, cr.FK.Name)
			default:
				return fmt.Errorf("%w: cannot delete from %q because %q has referencing rows (foreign key %q)",
					storage.ErrForeignKeyViolation, tableName, cr.ChildTable, cr.FK.Name)
			}
		}
	}

	tx.setBaseVer(tableName, pkKey, ver)
	originalBaseVer := tx.getBaseVer(tableName, pkKey)

	tx.removeLocal(tableName, pkKey)
	tx.markDeleted(tableName, pkKey)

	changeKey := tableName + ":" + pkKey
	tx.changes[changeKey] = rowChange{
		kind:     changeDelete,
		table:    tableName,
		key:      pkKey,
		baseVer:  originalBaseVer,
		td:       td,
		shardIdx: hashShard(pkKey),
	}

	return nil
}

func (tx *Transaction) validateForeignKeysUnderLock(childTable string, record storage.Record, schema *storage.TableSchema) error {
	for _, fk := range schema.ForeignKeys {
		hasNull := false
		for _, cCol := range fk.ChildColumns {
			val, ok := record.Get(cCol)
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
		for name, t := range tx.engine.tables {
			if strings.EqualFold(name, fk.ParentTable) {
				parentTd = t
				ok = true
				break
			}
		}
	}
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
		val, ok := childRecord.Get(cCol)
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
			inEngine := pTd.hasKey(parentKey)
			tx.engine.mu.RUnlock()
			return inEngine, nil
		}
		tx.engine.mu.RUnlock()
		return false, nil
	}

	matched := false
	parentTd.forEachRow(func(pkStr string, row storage.Row) bool {
		if tx.isLocallyDeleted(fk.ParentTable, pkStr) {
			return true
		}
		if _, inLocal := tx.getLocal(fk.ParentTable, pkStr); inLocal {
			return true
		}
		if rowMatchesFK(&parentTd.Schema, row, parentCols, childVals) {
			matched = true
			return false
		}
		return true
	})
	tx.engine.mu.RUnlock()
	if matched {
		return true, nil
	}

	if localRows, ok := tx.local[fk.ParentTable]; ok {
		for pkStr, row := range localRows {
			if tx.isLocallyDeleted(fk.ParentTable, pkStr) {
				continue
			}
			if rowMatchesFK(&parentTd.Schema, row, parentCols, childVals) {
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
		for name, t := range tx.engine.tables {
			if strings.EqualFold(name, childTable) {
				childTd = t
				exists = true
				break
			}
		}
	}
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
	_, childPkIdx, _ := childSchema.FindColumn(childPkCol.Name)

	pCols := fk.ParentColumns
	if len(pCols) == 0 {
		var parentTd *TableData
		for name, t := range tx.engine.tables {
			if strings.EqualFold(name, fk.ParentTable) {
				parentTd = t
				break
			}
		}
		if parentTd != nil {
			if pkCol, err := parentTd.Schema.PrimaryKeyColumn(); err == nil && pkCol != nil {
				pCols = []string{pkCol.Name}
			}
		}
	}

	parentValues := make([]storage.Value, len(pCols))
	for i, pCol := range pCols {
		val, ok := parentRecord.Get(pCol)
		if !ok || val.IsNull {
			tx.engine.mu.RUnlock()
			return nil, nil
		}
		parentValues[i] = val
	}

	var matchingPKs []storage.Value
	childTd.forEachRow(func(pkStr string, row storage.Row) bool {
		if tx.isLocallyDeleted(childTable, pkStr) {
			return true
		}
		if _, inLocal := tx.getLocal(childTable, pkStr); inLocal {
			return true
		}
		if rowMatchesFK(&childSchema, row, fk.ChildColumns, parentValues) && childPkIdx >= 0 && childPkIdx < len(row.Values) {
			matchingPKs = append(matchingPKs, row.Values[childPkIdx])
		}
		return true
	})
	tx.engine.mu.RUnlock()

	if localRows, ok := tx.local[childTable]; ok {
		for pkStr, row := range localRows {
			if tx.isLocallyDeleted(childTable, pkStr) {
				continue
			}
			if rowMatchesFK(&childSchema, row, fk.ChildColumns, parentValues) && childPkIdx >= 0 && childPkIdx < len(row.Values) {
				matchingPKs = append(matchingPKs, row.Values[childPkIdx])
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
	if tx.opts.ReadOnly {
		tx.mu.Lock()
		closed := tx.closed
		tx.mu.Unlock()
		if closed {
			return nil, storage.ErrTxClosed
		}
		td, err := tx.getTableReadOnly(tableName)
		if err != nil {
			return nil, err
		}
		pkKey := pk.String()
		row, _, exists := td.getRowAndVer(pkKey)
		if !exists {
			return nil, fmt.Errorf("%w: key %q in table %q", storage.ErrKeyNotFound, pkKey, tableName)
		}
		return td.Schema.RowToRecord(row), nil
	}

	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return nil, storage.ErrTxClosed
	}

	pkKey := pk.String()
	clone := !tx.opts.ReadOnly
	rec, ver, err := tx.getRecordAndVerUnderLock(tableName, pkKey, clone)
	if err == nil && !tx.opts.ReadOnly {
		tx.setBaseVer(tableName, pkKey, ver)
	}
	return rec, err
}

func (tx *Transaction) GetRow(tableName string, pk storage.Value) (storage.Row, error) {
	if tx.opts.ReadOnly {
		tx.mu.Lock()
		closed := tx.closed
		tx.mu.Unlock()
		if closed {
			return storage.Row{}, storage.ErrTxClosed
		}
		td, err := tx.getTableReadOnly(tableName)
		if err != nil {
			return storage.Row{}, err
		}
		pkKey := pk.String()
		row, _, exists := td.getRowAndVer(pkKey)
		if !exists {
			return storage.Row{}, fmt.Errorf("%w: key %q in table %q", storage.ErrKeyNotFound, pkKey, tableName)
		}
		return row, nil
	}

	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return storage.Row{}, storage.ErrTxClosed
	}

	pkKey := pk.String()
	clone := !tx.opts.ReadOnly
	row, ver, err := tx.getRowAndVerUnderLock(tableName, pkKey, clone)
	if err == nil && !tx.opts.ReadOnly {
		tx.setBaseVer(tableName, pkKey, ver)
	}
	return row, err
}

func (tx *Transaction) getRowAndVerUnderLock(tableName, pkKey string, clone bool) (storage.Row, int64, error) {
	if !tx.opts.ReadOnly {
		if tx.isLocallyDeleted(tableName, pkKey) {
			return storage.Row{}, 0, fmt.Errorf("%w: key %q in table %q", storage.ErrKeyNotFound, pkKey, tableName)
		}

		if row, found := tx.getLocal(tableName, pkKey); found {
			ver := tx.getBaseVer(tableName, pkKey)
			if clone {
				return cloneRow(row), ver, nil
			}
			return row, ver, nil
		}
	}

	td, err := tx.getTableLocked(tableName)
	if err != nil {
		return storage.Row{}, 0, err
	}
	row, ver, exists := td.getRowAndVer(pkKey)

	if !exists {
		return storage.Row{}, 0, fmt.Errorf("%w: key %q in table %q", storage.ErrKeyNotFound, pkKey, tableName)
	}

	if clone {
		return cloneRow(row), ver, nil
	}
	return row, ver, nil
}

func (tx *Transaction) getRecordAndVerUnderLock(tableName, pkKey string, clone bool) (storage.Record, int64, error) {
	row, ver, err := tx.getRowAndVerUnderLock(tableName, pkKey, false)
	if err != nil {
		return nil, 0, err
	}
	td, err := tx.getTableLocked(tableName)
	if err != nil {
		return nil, 0, err
	}
	rec := td.Schema.RowToRecord(row)
	return rec, ver, nil
}

func (tx *Transaction) Scan(tableName string) (storage.Cursor, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.closed {
		return nil, storage.ErrTxClosed
	}

	td, err := tx.getTableLocked(tableName)
	if err != nil {
		return nil, err
	}
	tableName = td.Schema.Name

	tx.engine.mu.RLock()

	var keys []string
	if tx.opts.ReadOnly {
		keys = td.collectKeys(nil)
	} else {
		keys = td.collectKeys(func(k string) bool {
			return !tx.isLocallyDeleted(tableName, k)
		})
	}
	tx.engine.mu.RUnlock()

	if !tx.opts.ReadOnly {
		if localRows, ok := tx.local[tableName]; ok {
			for k := range localRows {
				if !tx.isLocallyDeleted(tableName, k) {
					keys = append(keys, k)
				}
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

	td, err := tx.getTableLocked(tableName)
	if err != nil {
		return nil, err
	}
	tableName = td.Schema.Name

	if !td.Schema.IsPartitioned() {
		return tx.Scan(tableName)
	}

	tx.engine.mu.RLock()

	td.partMu.RLock()
	partSet := make(map[string]bool, len(partitions))
	for _, p := range partitions {
		partSet[p] = true
	}

	keySet := make(map[string]struct{})
	for p := range partSet {
		if pks, ok := td.Partitions[p]; ok {
			for pk := range pks {
				if tx.opts.ReadOnly || !tx.isLocallyDeleted(tableName, pk) {
					keySet[pk] = struct{}{}
				}
			}
		}
	}
	td.partMu.RUnlock()
	tx.engine.mu.RUnlock()

	// Check uncommitted local rows in transaction
	if !tx.opts.ReadOnly {
		if localRows, ok := tx.local[tableName]; ok {
			for pk, locRow := range localRows {
				if tx.isLocallyDeleted(tableName, pk) {
					continue
				}
				partID, err := td.Schema.ComputePartitionKeyFromRow(locRow)
				if err == nil && partSet[partID] {
					keySet[pk] = struct{}{}
				}
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

	td, err := tx.getTableLocked(tableName)
	if err != nil {
		return nil, err
	}
	tableName = td.Schema.Name

	tx.engine.mu.RLock()
	idxData, exists := td.Indexes[indexName]
	if !exists {
		idxData, exists = sqlutil.MapGetFold(td.Indexes, indexName)
	}
	if !exists {
		tx.engine.mu.RUnlock()
		return nil, fmt.Errorf("%w: %s on table %s", storage.ErrIndexNotFound, indexName, tableName)
	}

	if tx.opts.ReadOnly || (len(tx.local[tableName]) == 0 && len(tx.deleted[tableName]) == 0) {
		candidatePKs := idxData.ScanRange(low, high)
		tx.engine.mu.RUnlock()
		return &memCursor{
			tx:        tx,
			tableName: tableName,
			td:        td,
			keys:      candidatePKs,
			idx:       -1,
		}, nil
	}

	candidateEntries := idxData.ScanRangeEntries(low, high)
	tx.engine.mu.RUnlock()

	type indexScanItem struct {
		pk     string
		idxKey storage.Value
	}

	items := make([]indexScanItem, 0, len(candidateEntries))
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

		if locRow, inLocal := tx.getLocal(tableName, pk); inLocal {
			idxVal := extractIndexKeyFromRowWithSchema(&td.Schema, locRow, &idxData.Schema)
			if (low == nil || idxVal.Compare(*low) >= 0) && (high == nil || idxVal.Compare(*high) <= 0) {
				items = append(items, indexScanItem{pk: pk, idxKey: idxVal})
			}
			continue
		}

		items = append(items, indexScanItem{pk: pk, idxKey: entry.Key})
	}

	if localRows, ok := tx.local[tableName]; ok {
		for pk, locRow := range localRows {
			if seen[pk] || tx.isLocallyDeleted(tableName, pk) {
				continue
			}
			seen[pk] = true
			idxVal := extractIndexKeyFromRowWithSchema(&td.Schema, locRow, &idxData.Schema)
			if (low == nil || idxVal.Compare(*low) >= 0) && (high == nil || idxVal.Compare(*high) <= 0) {
				items = append(items, indexScanItem{pk: pk, idxKey: idxVal})
			}
		}
	}

	sort.Slice(items, func(i, j int) bool {
		cmp := items[i].idxKey.Compare(items[j].idxKey)
		if cmp != 0 {
			return cmp < 0
		}
		return items[i].pk < items[j].pk
	})

	pks := make([]string, len(items))
	for i, item := range items {
		pks[i] = item.pk
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
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return storage.Value{Type: storage.TypeInt, IVal: n, FVal: float64(n)}
		}
		return storage.Value{Type: storage.TypeInt}
	case storage.TypeFloat:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return storage.Value{Type: storage.TypeFloat, FVal: f, IVal: int64(f)}
		}
		return storage.Value{Type: storage.TypeFloat}
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

	td, err := tx.getTableLocked(tableName)
	if err != nil {
		return nil, err
	}
	tableName = td.Schema.Name

	tx.engine.mu.RLock()
	idxData, exists := td.Indexes[indexName]
	if !exists {
		idxData, exists = sqlutil.MapGetFold(td.Indexes, indexName)
	}
	if !exists {
		tx.engine.mu.RUnlock()
		return nil, fmt.Errorf("%w: %s on table %s", storage.ErrIndexNotFound, indexName, tableName)
	}

	pkCol, _ := td.Schema.PrimaryKeyColumn()
	candidateEntries := idxData.ScanRangeEntries(low, high)
	tx.engine.mu.RUnlock()

	type indexOnlyPayload struct {
		key         storage.Value
		includeVals []storage.Value
	}

	pks := make([]string, 0, len(candidateEntries))
	keyMap := make(map[string]indexOnlyPayload, len(candidateEntries))
	seen := make(map[string]bool, len(candidateEntries))

	for _, entry := range candidateEntries {
		pk := entry.PK
		if seen[pk] {
			continue
		}
		seen[pk] = true

		if !tx.opts.ReadOnly {
			if tx.isLocallyDeleted(tableName, pk) {
				continue
			}

			if locRow, inLocal := tx.getLocal(tableName, pk); inLocal {
				idxVal := extractIndexKeyFromRowWithSchema(&td.Schema, locRow, &idxData.Schema)
				if (low == nil || idxVal.Compare(*low) >= 0) && (high == nil || idxVal.Compare(*high) <= 0) {
					pks = append(pks, pk)
					keyMap[pk] = indexOnlyPayload{
						key:         idxVal,
						includeVals: extractIncludeValuesFromRow(&td.Schema, locRow, idxData.Schema.IncludeCols),
					}
				}
				continue
			}
		}

		pks = append(pks, pk)
		keyMap[pk] = indexOnlyPayload{
			key:         entry.Key,
			includeVals: entry.IncludeValues,
		}
	}

	if !tx.opts.ReadOnly {
		if localRows, ok := tx.local[tableName]; ok {
			for pk, locRow := range localRows {
				if seen[pk] || tx.isLocallyDeleted(tableName, pk) {
					continue
				}
				idxVal := extractIndexKeyFromRowWithSchema(&td.Schema, locRow, &idxData.Schema)
				if (low == nil || idxVal.Compare(*low) >= 0) && (high == nil || idxVal.Compare(*high) <= 0) {
					pks = append(pks, pk)
					seen[pk] = true
					keyMap[pk] = indexOnlyPayload{
						key:         idxVal,
						includeVals: extractIncludeValuesFromRow(&td.Schema, locRow, idxData.Schema.IncludeCols),
					}
				}
			}
		}
	}

	if !tx.opts.ReadOnly && (len(tx.local[tableName]) > 0 || len(tx.deleted[tableName]) > 0) {
		sort.Slice(pks, func(i, j int) bool {
			cmp := keyMap[pks[i]].key.Compare(keyMap[pks[j]].key)
			if cmp != 0 {
				return cmp < 0
			}
			return pks[i] < pks[j]
		})
	}

	resolver := func(pk string) (storage.Record, bool) {
		payload, ok := keyMap[pk]
		if !ok {
			return nil, false
		}
		rec := make(storage.Record, len(idxData.Schema.Columns)+len(idxData.Schema.IncludeCols)+1)
		if len(idxData.Schema.Columns) == 1 {
			rec[idxData.Schema.Columns[0]] = payload.key
		}
		for i, incCol := range idxData.Schema.IncludeCols {
			if i < len(payload.includeVals) {
				rec[incCol] = payload.includeVals[i]
			}
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

	// 1. Collect distinct table names and affected shards using 64-bit masks (zero nested map allocations)
	if tx.tableSet == nil {
		tx.tableSet = make(map[string]*TableData, 4)
	} else {
		clear(tx.tableSet)
	}
	if tx.shardMasks == nil {
		tx.shardMasks = make(map[string]shardMask, 4)
	} else {
		clear(tx.shardMasks)
	}
	tx.tableNames = tx.tableNames[:0]
	tx.neededShards = tx.neededShards[:0]

	for _, ch := range tx.changes {
		td := ch.td
		sIdx := ch.shardIdx
		if td == nil {
			var err error
			td, err = tx.getTableLocked(ch.table)
			if err != nil {
				continue
			}
			sIdx = hashShard(ch.key)
		}
		tx.tableSet[td.Schema.Name] = td
		m := tx.shardMasks[td.Schema.Name]
		m[sIdx/64] |= (uint64(1) << (sIdx % 64))
		tx.shardMasks[td.Schema.Name] = m
	}

	for name := range tx.tableSet {
		tx.tableNames = append(tx.tableNames, name)
	}
	sort.Strings(tx.tableNames) // Strict Lock Hierarchy to prevent deadlocks (Rule 10)

	for _, tName := range tx.tableNames {
		td := tx.tableSet[tName]
		mask := tx.shardMasks[tName]
		// Invariant: Iterating bit 0 to numTableShards-1 naturally guarantees ascending canonical shard order (Rule 10)
		for sIdx := 0; sIdx < numTableShards; sIdx++ {
			if (mask[sIdx/64] & (uint64(1) << (sIdx % 64))) != 0 {
				tx.neededShards = append(tx.neededShards, shardRef{
					tableName: tName,
					shardIdx:  sIdx,
					td:        td,
				})
			}
		}
	}

	// 2. Acquire Intent-Shared RLock on affected tables (prevents concurrent DDL DROP/TRUNCATE)
	for _, tName := range tx.tableNames {
		tx.tableSet[tName].mu.RLock()
	}

	// 3.1 Pre-encode WAL payloads BEFORE acquiring shard exclusive locks (Stage 4 Out-of-Lock WAL Encoding)
	// Eliminates CPU-heavy MsgPack row serialization while table shards are locked.
	needWAL := (tx.engine.wal != nil || tx.engine.walHook != nil)
	if needWAL && len(tx.changes) > 0 {
		for chKey, ch := range tx.changes {
			if storage.IsTemporaryTable(ch.table) || ch.kind == changeDelete {
				continue
			}
			td := ch.td
			if td == nil {
				td = tx.tableSet[ch.table]
			}
			if td != nil {
				if rawData, err := storage.EncodeRow(&td.Schema, ch.row); err == nil {
					ch.walData = rawData
					tx.changes[chKey] = ch
				}
			}
		}
	}

	// 4. Acquire exclusive lock on each affected shard in strict canonical order
	for i := range tx.neededShards {
		tx.neededShards[i].td.shards[tx.neededShards[i].shardIdx].mu.Lock()
	}

	var unlocked bool
	unlockAll := func() {
		if unlocked {
			return
		}
		unlocked = true
		for i := len(tx.neededShards) - 1; i >= 0; i-- {
			tx.neededShards[i].td.shards[tx.neededShards[i].shardIdx].mu.Unlock()
		}
		for i := len(tx.tableNames) - 1; i >= 0; i-- {
			tx.tableSet[tx.tableNames[i]].mu.RUnlock()
		}
	}
	defer unlockAll()

	// 5. Verify OCC version conflicts and unique constraints
	var seenUniqueKeys map[string]map[string]string // lazy allocated only when unique indexes exist

	for _, ch := range tx.changes {
		td := ch.td
		sIdx := ch.shardIdx
		if td == nil {
			var err error
			td, err = tx.getTableLocked(ch.table)
			if err != nil {
				continue
			}
			sIdx = hashShard(ch.key)
		}
		sh := &td.shards[sIdx]
		entry, exists := sh.entries[ch.key]
		curVer := entry.ver
		if ch.kind == changeInsert {
			if exists {
				return storage.ErrSerializationFailure
			}
			for _, idxData := range td.Indexes {
				if idxData.Schema.Unique {
					keyVal := extractIndexKeyFromRowWithSchema(&td.Schema, ch.row, &idxData.Schema)
					if !keyVal.IsNull {
						if seenUniqueKeys == nil {
							seenUniqueKeys = make(map[string]map[string]string)
						}
						mapID := td.Schema.Name + ":" + idxData.Schema.Name
						if seenUniqueKeys[mapID] == nil {
							seenUniqueKeys[mapID] = make(map[string]string)
						}
						encodedKey := keyVal.String()
						if otherPK, collides := seenUniqueKeys[mapID][encodedKey]; collides && otherPK != ch.key {
							return fmt.Errorf("%w: duplicate key %q in unique index %q", storage.ErrUniqueViolation, keyVal.String(), idxData.Schema.Name)
						}
						seenUniqueKeys[mapID][encodedKey] = ch.key

						existing := idxData.ScanRange(&keyVal, &keyVal)
						for _, epk := range existing {
							if epk != ch.key {
								return fmt.Errorf("%w: duplicate key %q in unique index %q", storage.ErrUniqueViolation, keyVal.String(), idxData.Schema.Name)
							}
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
						keyVal := extractIndexKeyFromRowWithSchema(&td.Schema, ch.row, &idxData.Schema)
						if !keyVal.IsNull {
							if seenUniqueKeys == nil {
								seenUniqueKeys = make(map[string]map[string]string)
							}
							mapID := td.Schema.Name + ":" + idxData.Schema.Name
							if seenUniqueKeys[mapID] == nil {
								seenUniqueKeys[mapID] = make(map[string]string)
							}
							encodedKey := keyVal.String()
							if otherPK, collides := seenUniqueKeys[mapID][encodedKey]; collides && otherPK != ch.key {
								return fmt.Errorf("%w: duplicate key %q in unique index %q", storage.ErrUniqueViolation, keyVal.String(), idxData.Schema.Name)
							}
							seenUniqueKeys[mapID][encodedKey] = ch.key

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
	}

	// 5.1 Enforce in-memory engine storage memory limit guard (MEM-3)
	if maxLimit := tx.engine.maxDataBytes.Load(); maxLimit > 0 {
		hasInsertOrUpdate := false
		for _, ch := range tx.changes {
			if ch.kind == changeInsert || ch.kind == changeUpdate {
				hasInsertOrUpdate = true
				break
			}
		}
		if hasInsertOrUpdate {
			// Sample heap bytes periodically (every 128 commits) or use atomic cache (VPERF-1)
			cnt := tx.engine.commitCounter.Add(1)
			currHeap := tx.engine.cachedHeapBytes.Load()
			if cnt&127 == 0 || currHeap == 0 {
				currHeap = CurrentHeapBytes()
				tx.engine.cachedHeapBytes.Store(currHeap)
			}
			if currHeap >= maxLimit {
				// Re-verify accurately on threshold hit before failing
				currHeap = CurrentHeapBytes()
				tx.engine.cachedHeapBytes.Store(currHeap)
				if currHeap >= maxLimit {
					return fmt.Errorf("%w: in-memory engine storage memory limit exceeded (%s >= %s)",
						storage.ErrTableFull, formatBytes(currHeap), formatBytes(maxLimit))
				}
			}
		}
	}

	// 6. Apply changes and update secondary indexes atomically
	for _, ch := range tx.changes {
		td := ch.td
		sIdx := ch.shardIdx
		if td == nil {
			var err error
			td, err = tx.getTableLocked(ch.table)
			if err != nil {
				continue
			}
			sIdx = hashShard(ch.key)
		}
		sh := &td.shards[sIdx]
		oldEntry, hadOld := sh.entries[ch.key]
		oldRow := oldEntry.row

		switch ch.kind {
		case changeInsert:
			sh.entries[ch.key] = shardEntry{ver: 1, row: ch.row}
			if td.Schema.IsPartitioned() {
				if partID, err := td.Schema.ComputePartitionKeyFromRow(ch.row); err == nil {
					td.partMu.Lock()
					td.addPartitionKey(partID, ch.key)
					td.partMu.Unlock()
				}
			}
			for _, idxData := range td.Indexes {
				newKey := extractIndexKeyFromRowWithSchema(&td.Schema, ch.row, &idxData.Schema)
				incVals := extractIncludeValuesFromRow(&td.Schema, ch.row, idxData.Schema.IncludeCols)
				if err := idxData.Insert(newKey, ch.key, incVals); err != nil {
					return err
				}
			}
		case changeUpdate:
			sh.entries[ch.key] = shardEntry{ver: ch.baseVer + 1, row: ch.row}
			if td.Schema.IsPartitioned() {
				var oldPartID, newPartID string
				if hadOld {
					oldPartID, _ = td.Schema.ComputePartitionKeyFromRow(oldRow)
				}
				newPartID, _ = td.Schema.ComputePartitionKeyFromRow(ch.row)
				if oldPartID != newPartID {
					td.partMu.Lock()
					if oldPartID != "" {
						td.removePartitionKey(oldPartID, ch.key)
					}
					if newPartID != "" {
						td.addPartitionKey(newPartID, ch.key)
					}
					td.partMu.Unlock()
				}
			}
			for _, idxData := range td.Indexes {
				if hadOld {
					oldKey := extractIndexKeyFromRowWithSchema(&td.Schema, oldRow, &idxData.Schema)
					idxData.Delete(oldKey, ch.key)
				}
				newKey := extractIndexKeyFromRowWithSchema(&td.Schema, ch.row, &idxData.Schema)
				incVals := extractIncludeValuesFromRow(&td.Schema, ch.row, idxData.Schema.IncludeCols)
				if err := idxData.Insert(newKey, ch.key, incVals); err != nil {
					return err
				}
			}
		case changeDelete:
			delete(sh.entries, ch.key)
			if td.Schema.IsPartitioned() && hadOld {
				if partID, err := td.Schema.ComputePartitionKeyFromRow(oldRow); err == nil {
					td.partMu.Lock()
					td.removePartitionKey(partID, ch.key)
					td.partMu.Unlock()
				}
			}
			if hadOld {
				for _, idxData := range td.Indexes {
					oldKey := extractIndexKeyFromRowWithSchema(&td.Schema, oldRow, &idxData.Schema)
					idxData.Delete(oldKey, ch.key)
				}
			}
		}
	}

	// 7. Assign monotonic LSN and append WAL frame while shard locks are still held
	// so causal row version order and LSN order in the WAL file strictly match.
	var walRec WALRecord
	var walSeq int64
	if needWAL && len(tx.changes) > 0 {
		ops := make([]WALOp, 0, len(tx.changes))
		for _, ch := range tx.changes {
			if storage.IsTemporaryTable(ch.table) {
				continue
			}
			var opType WALOpType
			switch ch.kind {
			case changeInsert:
				opType = OpInsert
			case changeUpdate:
				opType = OpUpdate
			case changeDelete:
				opType = OpDelete
			}
			rawData := ch.walData
			if rawData == nil && ch.kind != changeDelete {
				td := ch.td
				if td == nil {
					td, _ = tx.getTableLocked(ch.table)
				}
				if td != nil {
					rawData, _ = storage.EncodeRow(&td.Schema, ch.row)
				}
			}
			ops = append(ops, WALOp{
				Type:    opType,
				Table:   ch.table,
				Key:     ch.key,
				Data:    rawData,
				BaseVer: ch.baseVer,
			})
		}
		if len(ops) > 0 {
			var err error
			walRec, walSeq, err = tx.engine.appendWALFrameLocked(ops)
			if err != nil {
				return err
			}
		} else {
			tx.commitLSN = tx.engine.nextLSN.Add(1)
			tx.commitTimestamp = time.Now().UnixNano()
		}
	} else {
		if len(tx.changes) > 0 {
			tx.engine.changesSinceSave.Add(int64(len(tx.changes)))
			tx.commitLSN = tx.engine.nextLSN.Add(1)
		} else {
			tx.commitLSN = tx.snapshotLSN
		}
		tx.commitTimestamp = time.Now().UnixNano()
	}

	// Release shard and table locks before WAL fsync / AOF compaction
	unlockAll()

	// 8. Perform group commit fsync and optional AOF compaction outside shard/table locks
	if needWAL && walSeq > 0 {
		if err := tx.engine.finishWALCommitLocked(walSeq); err != nil {
			return err
		}
		tx.commitLSN = walRec.LSN
		tx.commitTimestamp = walRec.Timestamp
	}

	return nil
}

func (tx *Transaction) Rollback() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.inPool {
		return storage.ErrTxClosed
	}
	tx.closed = true
	tx.releaseToPoolLocked()
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

func cloneLocalMap(m map[string]map[string]storage.Row) map[string]map[string]storage.Row {
	res := make(map[string]map[string]storage.Row, len(m))
	for tbl, rows := range m {
		tblMap := make(map[string]storage.Row, len(rows))
		for k, row := range rows {
			tblMap[k] = cloneRow(row)
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
		chCopy.row = cloneRow(ch.row)
		if ch.rec != nil {
			chCopy.rec = cloneRecord(ch.rec)
		}
		res[k] = chCopy
	}
	return res
}

func (tx *Transaction) getLocal(table, key string) (storage.Row, bool) {
	if tx.opts.ReadOnly || tx.local == nil {
		return storage.Row{}, false
	}
	if t, ok := tx.local[table]; ok {
		r, exists := t[key]
		return r, exists
	}
	return storage.Row{}, false
}

func (tx *Transaction) getBaseVer(table, key string) int64 {
	if tx.opts.ReadOnly || tx.baseVers == nil {
		return 0
	}
	if t, ok := tx.baseVers[table]; ok {
		return t[key]
	}
	return 0
}

func (tx *Transaction) setBaseVer(table, key string, ver int64) {
	if tx.opts.ReadOnly {
		return
	}
	if tx.baseVers == nil {
		tx.baseVers = make(map[string]map[string]int64, 4)
	}
	v, ok := tx.baseVers[table]
	if !ok {
		v = make(map[string]int64)
		tx.baseVers[table] = v
	}
	if _, has := v[key]; !has {
		v[key] = ver
	}
}

func (tx *Transaction) putLocal(table, key string, row storage.Row, baseVer int64) {
	t, ok := tx.local[table]
	if !ok {
		t = make(map[string]storage.Row)
		tx.local[table] = t
	}
	t[key] = row
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
	if tx.opts.ReadOnly || tx.deleted == nil {
		return false
	}
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
	curRow    storage.Row
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
			c.curRow = storage.Row{}
			return false
		}
		pk := c.keys[c.idx]
		if c.tx != nil {
			c.tx.mu.Lock()
			isDel := c.tx.isLocallyDeleted(c.tableName, pk)
			locRow, inLocal := c.tx.getLocal(c.tableName, pk)
			c.tx.mu.Unlock()
			if isDel {
				continue
			}
			if inLocal {
				c.curRow = locRow
				c.curRec = nil
				return true
			}
		}
		if c.resolver != nil {
			if rec, ok := c.resolver(pk); ok {
				c.curRec = rec
				if c.td != nil {
					c.curRow = c.td.Schema.RecordToRow(rec)
				}
				return true
			}
			continue
		}
		if c.td != nil {
			row, _, exists := c.td.getRowAndVer(pk)
			if exists {
				c.curRow = row
				c.curRec = nil
				return true
			}
		}
	}
}

func (c *memCursor) Record() (storage.Record, error) {
	if c.closed || (c.curRec == nil && len(c.curRow.Values) == 0) {
		return nil, fmt.Errorf("cursor out of range or closed")
	}
	if c.curRec == nil && c.td != nil {
		c.curRec = c.td.Schema.RowToRecord(c.curRow)
	}
	return c.curRec, nil
}

func (c *memCursor) Row() (storage.Row, error) {
	if c.closed || (c.curRec == nil && len(c.curRow.Values) == 0) {
		return storage.Row{}, fmt.Errorf("cursor out of range or closed")
	}
	return c.curRow, nil
}

func (c *memCursor) Close() error {
	c.closed = true
	c.keys = nil
	c.curRec = nil
	c.curRow = storage.Row{}
	c.resolver = nil
	c.tx = nil
	c.td = nil
	return nil
}


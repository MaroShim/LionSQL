package memengine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

func (e *Engine) bgSaveLoop(stopCh <-chan struct{}, interval time.Duration, minChanges int64) {
	defer e.bgWg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			e.mu.Lock()
			if e.closed {
				e.mu.Unlock()
				return
			}
			changes := e.changesSinceSave.Load()
			if changes >= minChanges {
				e.changesSinceSave.Store(0)
				e.mu.Unlock()
				if e.opts.EnableAOF && e.wal != nil {
					_ = e.RewriteAOF()
				} else if e.opts.EnableRDB {
					_ = e.SaveSnapshot()
				}
			} else {
				e.mu.Unlock()
			}
		}
	}
}

// ApplyWALRecord applies a single committed WAL record directly to the in-memory engine.
func (e *Engine) ApplyWALRecord(rec WALRecord) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.applyWALRecordLocked(rec)
}

func (e *Engine) applyWALRecordLocked(rec WALRecord) error {
	if rec.LSN > e.nextLSN.Load() {
		e.nextLSN.Store(rec.LSN)
	}
	hasDDL := false
	for _, op := range rec.Ops {
		if storage.IsTemporaryTable(op.Table) ||
			(op.Schema != nil && storage.IsTemporaryTable(op.Schema.Name)) ||
			(op.IndexSchema != nil && storage.IsTemporaryTable(op.IndexSchema.TableName)) {
			continue
		}
		switch op.Type {
		case OpCreateTable:
			if op.Schema == nil {
				continue
			}
			hasDDL = true
			td := &TableData{
				Schema:     *op.Schema,
				Rows:       make(map[string]storage.Row),
				Versions:   make(map[string]int64),
				Keys:       make([]string, 0),
				Indexes:    make(map[string]*IndexData),
				Partitions: make(map[string]map[string]struct{}),
				mode:       e.opts.Mode,
			}
			if e.opts.Mode == MemoryCompact {
				td.layout = BuildFlatRecordLayout(op.Schema)
			}
			td.refreshSnapSchemaLocked()
			td.initShards()
			for _, idx := range op.Schema.Indexes {
				td.Indexes[idx.Name] = &IndexData{
					Schema:  idx,
					Entries: make([]IndexEntry, 0),
				}
			}
			e.tables[op.Schema.Name] = td
		case OpDropTable:
			hasDDL = true
			sqlutil.MapDeleteFold(e.tables, op.Table)
		case OpRenameTable:
			hasDDL = true
			oldName := op.Key
			newName := op.Table
			if td, exists := sqlutil.MapGetFold(e.tables, oldName); exists {
				sqlutil.MapDeleteFold(e.tables, oldName)
				td.Schema.Name = newName
				td.refreshSnapSchemaLocked()
				e.tables[newName] = td
				for _, otherTd := range e.tables {
					changed := false
					for i := range otherTd.Schema.ForeignKeys {
						if strings.EqualFold(otherTd.Schema.ForeignKeys[i].ParentTable, oldName) {
							otherTd.Schema.ForeignKeys[i].ParentTable = newName
							changed = true
						}
					}
					if changed {
						otherTd.refreshSnapSchemaLocked()
					}
				}
				e.rebuildFKCacheLocked()
			}
		case OpAlterTable:
			if td, exists := sqlutil.MapGetFold(e.tables, op.Table); exists {
				if op.Schema != nil {
					if err := e.applySchemaMigrationLocked(td, op.Schema, false); err != nil {
						return err
					}
				} else if op.Column != nil {
					trialSchema := td.Schema.Clone()
					trialSchema.Columns = append(trialSchema.Columns, *op.Column)
					if err := e.applySchemaMigrationLocked(td, &trialSchema, false); err != nil {
						return err
					}
				}
			}
		case OpTruncateTable:
			if td, exists := sqlutil.MapGetFold(e.tables, op.Table); exists {
				td.truncateShardsLocked()
				td.Partitions = make(map[string]map[string]struct{})
				for _, idx := range td.Indexes {
					idx.sl = NewSkipList(idx.Schema.Unique)
					idx.Entries = nil
				}
			}
		case OpDropPartition:
			if td, exists := sqlutil.MapGetFold(e.tables, op.Table); exists {
				if pksSet, ok := td.Partitions[op.Key]; ok {
					for pk := range pksSet {
						oldRow, hadOld := td.deleteRowAndVer(pk)
						if hadOld {
							for _, idxData := range td.Indexes {
								oldKey := extractIndexKeyFromRowWithSchema(&td.Schema, oldRow, &idxData.Schema)
								idxData.Delete(oldKey, pk)
							}
						}
					}
					delete(td.Partitions, op.Key)
					td.Keys = td.collectKeys(nil)
				}
			}
		case OpCreateIndex:
			if op.IndexSchema == nil {
				continue
			}
			td, exists := sqlutil.MapGetFold(e.tables, op.IndexSchema.TableName)
			if !exists {
				continue
			}
			idxData := &IndexData{
				Schema:  *op.IndexSchema,
				Entries: make([]IndexEntry, 0),
			}
			var insertErr error
			scanErr := td.forEachRow(func(pk string, row storage.Row) bool {
				keyVal := extractIndexKeyFromRowWithSchema(&td.Schema, row, op.IndexSchema)
				incVals := extractIncludeValuesFromRow(&td.Schema, row, op.IndexSchema.IncludeCols)
				if err := idxData.Insert(keyVal, pk, incVals); err != nil {
					insertErr = err
					return false
				}
				return true
			})
			if scanErr != nil {
				return fmt.Errorf("memengine: replay OpCreateIndex failed on table %q index %q: %w", td.Schema.Name, op.IndexSchema.Name, scanErr)
			}
			if insertErr != nil {
				return fmt.Errorf("memengine: replay OpCreateIndex failed on table %q index %q: %w", td.Schema.Name, op.IndexSchema.Name, insertErr)
			}
			if td.Indexes == nil {
				td.Indexes = make(map[string]*IndexData)
			}
			td.Indexes[op.IndexSchema.Name] = idxData
			_, _, idxFound := td.Schema.FindIndex(op.IndexSchema.Name)
			if !idxFound {
				td.Schema.Indexes = append(td.Schema.Indexes, *op.IndexSchema)
			}
			td.refreshSnapSchemaLocked()
		case OpDropIndex:
			td, exists := sqlutil.MapGetFold(e.tables, op.Table)
			if !exists {
				continue
			}
			sqlutil.MapDeleteFold(td.Indexes, op.IndexName)
			_, idxPos, idxFound := td.Schema.FindIndex(op.IndexName)
			if idxFound {
				td.Schema.Indexes = append(td.Schema.Indexes[:idxPos], td.Schema.Indexes[idxPos+1:]...)
			}
			td.refreshSnapSchemaLocked()
		case OpInsert:
			td, exists := sqlutil.MapGetFold(e.tables, op.Table)
			if !exists {
				continue
			}
			var row storage.Row
			if len(op.Data) > 0 {
				var err error
				row, err = storage.DecodeTupleToRow(&td.Schema, op.Data)
				if err != nil {
					return fmt.Errorf("memengine: wal replay OpInsert tuple decode failed on table %q (pk=%s): %w", td.Schema.Name, op.Key, err)
				}
			} else if op.Record != nil {
				row = td.Schema.RecordToRow(op.Record)
			}
			if !td.hasKey(op.Key) {
				td.Keys = append(td.Keys, op.Key)
			}
			td.putRowAndVer(op.Key, row, 1)
			if td.Schema.IsPartitioned() {
				if pKey, err := td.Schema.ComputePartitionKeyFromRow(row); err == nil {
					td.addPartitionKey(pKey, op.Key)
				}
			}
			for _, idxData := range td.Indexes {
				if idxData.Schema.Disabled {
					continue
				}
				newKey := extractIndexKeyFromRowWithSchema(&td.Schema, row, &idxData.Schema)
				incVals := extractIncludeValuesFromRow(&td.Schema, row, idxData.Schema.IncludeCols)
				if err := idxData.Insert(newKey, op.Key, incVals); err != nil {
					return fmt.Errorf("memengine: wal replay insert index %q on table %q failed (pk=%s): %w", idxData.Schema.Name, td.Schema.Name, op.Key, err)
				}
			}
		case OpUpdate:
			td, exists := sqlutil.MapGetFold(e.tables, op.Table)
			if !exists {
				continue
			}
			oldRow, _, err := td.getRowAndVer(op.Key)
			hadOld := (err == nil)
			var row storage.Row
			if len(op.Data) > 0 {
				var err error
				row, err = storage.DecodeTupleToRow(&td.Schema, op.Data)
				if err != nil {
					return fmt.Errorf("memengine: wal replay OpUpdate tuple decode failed on table %q (pk=%s): %w", td.Schema.Name, op.Key, err)
				}
			} else if op.Record != nil {
				row = td.Schema.RecordToRow(op.Record)
			}
			if !hadOld {
				td.Keys = append(td.Keys, op.Key)
			}
			td.putRowAndVer(op.Key, row, op.BaseVer+1)
			if td.Schema.IsPartitioned() {
				var oldPKey, newPKey string
				if hadOld {
					oldPKey, _ = td.Schema.ComputePartitionKeyFromRow(oldRow)
				}
				newPKey, _ = td.Schema.ComputePartitionKeyFromRow(row)
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
				if idxData.Schema.Disabled {
					continue
				}
				if hadOld {
					oldKey := extractIndexKeyFromRowWithSchema(&td.Schema, oldRow, &idxData.Schema)
					idxData.Delete(oldKey, op.Key)
				}
				newKey := extractIndexKeyFromRowWithSchema(&td.Schema, row, &idxData.Schema)
				incVals := extractIncludeValuesFromRow(&td.Schema, row, idxData.Schema.IncludeCols)
				if err := idxData.Insert(newKey, op.Key, incVals); err != nil {
					return fmt.Errorf("memengine: wal replay update index %q on table %q failed (pk=%s): %w", idxData.Schema.Name, td.Schema.Name, op.Key, err)
				}
			}
		case OpDelete:
			td, exists := sqlutil.MapGetFold(e.tables, op.Table)
			if !exists {
				continue
			}
			oldRow, hadOld := td.deleteRowAndVer(op.Key)
			if td.Schema.IsPartitioned() && hadOld {
				if pKey, err := td.Schema.ComputePartitionKeyFromRow(oldRow); err == nil {
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
					if idxData.Schema.Disabled {
						continue
					}
					oldKey := extractIndexKeyFromRowWithSchema(&td.Schema, oldRow, &idxData.Schema)
					idxData.Delete(oldKey, op.Key)
				}
			}
		}
	}
	if hasDDL {
		e.rebuildFKCacheLocked()
	}
	return nil
}

// SnapshotContainer encapsulates in-memory tables and the snapshot LSN boundary.
type SnapshotContainer struct {
	SnapshotLSN int64                 `msgpack:"snapshot_lsn"`
	Tables      map[string]*TableData `msgpack:"tables"`
}

// RewriteAOF generates a fresh snapshot (tables.mpk) using SQLite WAL2-style Dual-WAL rotation,
// rotating active.wal to compacting.wal in ~1 microsecond so incoming transactions are never stalled ([발굴 결함 1-1]).
func (e *Engine) RewriteAOF() error {
	if e.dataDir == "" || e.wal == nil {
		return nil
	}
	compactingWAL := filepath.Join(e.dataDir, "wal.aof.compacting")

	// 1. Atomically rotate active WAL file under short-lived lock (WAL2 Ping-Pong)
	e.mu.Lock()
	e.walOrderMu.Lock()
	snapLSN := e.nextLSN.Load()
	err := e.wal.Rotate(compactingWAL)
	e.walOrderMu.Unlock()
	e.mu.Unlock()

	if err != nil {
		return fmt.Errorf("AOF rewrite rotate error: %w", err)
	}

	// 2. Dump in-memory snapshot with snapLSN without holding e.mu (non-blocking for incoming commits)
	if err := e.saveDiskSnapshotLocked(snapLSN); err != nil {
		return fmt.Errorf("AOF rewrite snapshot error: %w", err)
	}

	// 3. Remove the frozen compacting WAL file ONLY after snapshot file and parent directory are durably synced to disk
	if err := os.Remove(compactingWAL); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove compacting WAL after snapshot: %w", err)
	}
	if err := syncDir(e.dataDir); err != nil {
		return fmt.Errorf("failed to sync data directory after removing compacting WAL: %w", err)
	}
	return nil
}

func (e *Engine) rewriteAOFLocked() error {
	if e.dataDir == "" || e.wal == nil {
		return nil
	}
	compactingWAL := filepath.Join(e.dataDir, "wal.aof.compacting")
	e.walOrderMu.Lock()
	snapLSN := e.nextLSN.Load()
	err := e.wal.Rotate(compactingWAL)
	e.walOrderMu.Unlock()
	if err != nil {
		return fmt.Errorf("AOF rewrite rotate error: %w", err)
	}
	if err := e.saveDiskSnapshotLocked(snapLSN); err != nil {
		return fmt.Errorf("AOF rewrite snapshot error: %w", err)
	}
	if err := os.Remove(compactingWAL); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove compacting WAL after snapshot: %w", err)
	}
	if err := syncDir(e.dataDir); err != nil {
		return fmt.Errorf("failed to sync data directory after removing compacting WAL: %w", err)
	}
	return nil
}

// SaveSnapshot forces an immediate durable disk snapshot dump of all tables and returns any error.
func (e *Engine) SaveSnapshot() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.saveDiskLocked()
}

func (e *Engine) saveDiskLocked() error {
	return e.saveDiskSnapshotLocked(e.nextLSN.Load())
}

func (e *Engine) saveDiskSnapshotLocked(snapLSN int64) error {
	if e.dataDir == "" {
		return nil
	}
	e.diskMu.Lock()
	defer e.diskMu.Unlock()

	// [RULE:ConcurrencySafety] Load immutable atomic tables snapshot to prevent concurrent map iteration panic during DDL (AUDIT-FULL-004)
	var sourceTables map[string]*TableData
	if snapPtr := e.tablesAtomic.Load(); snapPtr != nil {
		sourceTables = *snapPtr
	} else {
		sourceTables = e.tables
	}

	persistTables := make(map[string]*TableData, len(sourceTables))
	for name, td := range sourceTables {
		if storage.IsTemporaryTable(name) {
			continue
		}
		persistTables[name] = td
	}

	tNames := make([]string, 0, len(persistTables))
	for name := range persistTables {
		tNames = append(tNames, name)
	}
	sort.Strings(tNames)

	// Acquire table locks in canonical sorted order ([RULE:LockHierarchy])
	for _, name := range tNames {
		td := persistTables[name]
		td.mu.Lock()
		td.partMu.RLock()
	}
	unlockTables := func() {
		for i := len(tNames) - 1; i >= 0; i-- {
			td := persistTables[tNames[i]]
			td.partMu.RUnlock()
			td.clearShardsSyncMapsAlreadyLocked()
			td.mu.Unlock()
		}
	}
	defer func() {
		if tNames != nil {
			unlockTables()
		}
	}()

	for _, name := range tNames {
		td := persistTables[name]
		if err := td.syncShardsToMapsAlreadyLocked(); err != nil {
			return err
		}
		for _, idx := range td.Indexes {
			idx.SyncEntries()
		}
	}

	container := SnapshotContainer{
		SnapshotLSN: snapLSN,
		Tables:      persistTables,
	}
	data, err := MarshalMsgPack(container)
	// Explicitly release table locks before disk write syscalls ([RULE:LatencyBudget], [RULE:LockHierarchy])
	unlockTables()
	tNames = nil // prevent double-unlock in defer

	if err != nil {
		return fmt.Errorf("failed to encode tables to MessagePack: %w", err)
	}

	tmpFile := filepath.Join(e.dataDir, "tables.mpk.tmp")
	targetFile := filepath.Join(e.dataDir, "tables.mpk")

	return writeFileAtomicDurable(tmpFile, targetFile, data)
}

// writeFileAtomicDurable writes data to tmpPath, fsyncs the temporary file so all blocks
// reach physical storage before the directory rename, atomically renames tmpPath to targetPath,
// and fsyncs the parent directory so the directory entry update is durable ([RULE:AtomicSaves]).
func writeFileAtomicDurable(tmpPath, targetPath string, data []byte) error {
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open temporary snapshot file %q: %w", tmpPath, err)
	}

	n, writeErr := f.Write(data)
	if writeErr == nil && n < len(data) {
		writeErr = fmt.Errorf("short write on temporary snapshot file %q (%d/%d bytes)", tmpPath, n, len(data))
	}
	if writeErr != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write temporary snapshot file %q: %w", tmpPath, writeErr)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to fsync temporary snapshot file %q: %w", tmpPath, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close temporary snapshot file %q: %w", tmpPath, err)
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to rename snapshot file %q -> %q: %w", tmpPath, targetPath, err)
	}

	if err := syncDir(filepath.Dir(targetPath)); err != nil {
		return fmt.Errorf("failed to fsync parent directory of %q: %w", targetPath, err)
	}
	return nil
}

// syncDir opens a directory and calls Sync() to flush directory entry metadata (rename/remove/create) to disk.
func syncDir(dirPath string) error {
	if dirPath == "" || runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(filepath.Clean(dirPath))
	if err != nil {
		return err
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func (e *Engine) loadDiskLocked() (int64, error) {
	if e.dataDir == "" {
		return 0, nil
	}

	mpkFile := filepath.Join(e.dataDir, "tables.mpk")
	data, err := os.ReadFile(mpkFile)
	if err != nil {
		if os.IsNotExist(err) || errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to read RDB snapshot file %q: %w", mpkFile, err)
	}
	if len(data) == 0 {
		return 0, fmt.Errorf("RDB snapshot file %q is empty or truncated", mpkFile)
	}

	var snapLSN int64
	var loaded map[string]*TableData

	var container SnapshotContainer
	if err := UnmarshalMsgPack(data, &container); err == nil && container.Tables != nil {
		snapLSN = container.SnapshotLSN
		loaded = container.Tables
	} else {
		if err := UnmarshalMsgPack(data, &loaded); err != nil || loaded == nil {
			if err != nil {
				return 0, fmt.Errorf("failed to unmarshal RDB snapshot file %q: %w", mpkFile, err)
			}
			return 0, fmt.Errorf("RDB snapshot file %q contains nil table map", mpkFile)
		}
	}

	e.tables = make(map[string]*TableData)
	for name, td := range loaded {
		if storage.IsTemporaryTable(name) {
			continue
		}
		if td == nil {
			return 0, fmt.Errorf("RDB snapshot file %q contains nil table entry for %q", mpkFile, name)
		}
		e.tables[name] = td
	}
	for _, td := range e.tables {
		// Unexported runtime layout metadata is not serialized by MsgPack; restore it before initShards()
		// so RDB rows re-enter shards in the engine's configured MemoryMode without stale heap duplicates.
		td.mode = e.opts.Mode
		if td.mode == MemoryCompact {
			td.layout = BuildFlatRecordLayout(&td.Schema)
		} else {
			td.layout = nil
		}
		td.refreshSnapSchemaLocked()
		if td.Versions == nil {
			td.Versions = make(map[string]int64)
			for k := range td.Rows {
				td.Versions[k] = 1
			}
		}
		td.initShards()
		if td.Indexes == nil {
			td.Indexes = make(map[string]*IndexData)
		}
		if err := td.restoreIndexesLocked(); err != nil {
			return 0, fmt.Errorf("failed to restore secondary indexes for table %q from RDB snapshot: %w", td.Schema.Name, err)
		}
		if td.Schema.IsPartitioned() {
			if td.Partitions == nil {
				td.Partitions = make(map[string]map[string]struct{})
			}
			for k, row := range td.Rows {
				if pKey, err := td.Schema.ComputePartitionKeyFromRow(row); err == nil {
					td.addPartitionKey(pKey, k)
				}
			}
		}
		td.Rows = nil
		td.Versions = nil
	}
	e.rebuildFKCacheLocked()
	return snapLSN, nil
}


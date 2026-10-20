package memengine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
				if e.opts.EnableAOF && e.wal != nil {
					// Redis AOF -> RDB compaction: dump snapshot to RDB and truncate AOF
					_ = e.rewriteAOFLocked()
				} else if e.opts.EnableRDB {
					// Pure RDB periodic snapshot
					_ = e.saveDiskLocked()
				}
				e.changesSinceSave.Store(0)
			}
			e.mu.Unlock()
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
	for _, op := range rec.Ops {
		switch op.Type {
		case OpCreateTable:
			if op.Schema == nil {
				continue
			}
			td := &TableData{
				Schema:     *op.Schema,
				Rows:       make(map[string]storage.Row),
				Versions:   make(map[string]int64),
				Keys:       make([]string, 0),
				Indexes:    make(map[string]*IndexData),
				Partitions: make(map[string]map[string]struct{}),
			}
			td.initShards()
			for _, idx := range op.Schema.Indexes {
				td.Indexes[idx.Name] = &IndexData{
					Schema:  idx,
					Entries: make([]IndexEntry, 0),
				}
			}
			e.tables[op.Schema.Name] = td
		case OpDropTable:
			delete(e.tables, op.Table)
		case OpAlterTable:
			if td, exists := e.tables[op.Table]; exists && op.Column != nil {
				td.Schema.Columns = append(td.Schema.Columns, *op.Column)
				fillVal := storage.NullValue(op.Column.Type)
				if strings.TrimSpace(op.Column.Default) != "" {
					fillVal = storage.ParseLiteralDefault(op.Column.Default, op.Column.Type)
				}
				td.addColumnValues(fillVal)
			}
		case OpTruncateTable:
			if td, exists := e.tables[op.Table]; exists {
				td.truncateShardsLocked()
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
			td, exists := e.tables[op.IndexSchema.TableName]
			if !exists {
				continue
			}
			idxData := &IndexData{
				Schema:  *op.IndexSchema,
				Entries: make([]IndexEntry, 0),
			}
			td.forEachRow(func(pk string, row storage.Row) bool {
				keyVal := extractIndexKeyFromRowWithSchema(&td.Schema, row, op.IndexSchema)
				_ = idxData.Insert(keyVal, pk)
				return true
			})
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
			var row storage.Row
			if len(op.Data) > 0 {
				var err error
				row, err = storage.DecodeTupleToRow(&td.Schema, op.Data)
				if err != nil {
					continue
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
				newKey := extractIndexKeyFromRowWithSchema(&td.Schema, row, &idxData.Schema)
				_ = idxData.Insert(newKey, op.Key)
			}
		case OpUpdate:
			td, exists := e.tables[op.Table]
			if !exists {
				continue
			}
			oldRow, _, hadOld := td.getRowAndVer(op.Key)
			if !hadOld {
				td.Keys = append(td.Keys, op.Key)
			}
			var row storage.Row
			if len(op.Data) > 0 {
				var err error
				row, err = storage.DecodeTupleToRow(&td.Schema, op.Data)
				if err != nil {
					continue
				}
			} else if op.Record != nil {
				row = td.Schema.RecordToRow(op.Record)
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
				if hadOld {
					oldKey := extractIndexKeyFromRowWithSchema(&td.Schema, oldRow, &idxData.Schema)
					idxData.Delete(oldKey, op.Key)
				}
				newKey := extractIndexKeyFromRowWithSchema(&td.Schema, row, &idxData.Schema)
				_ = idxData.Insert(newKey, op.Key)
			}
		case OpDelete:
			td, exists := e.tables[op.Table]
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
					oldKey := extractIndexKeyFromRowWithSchema(&td.Schema, oldRow, &idxData.Schema)
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

func (e *Engine) saveDiskLocked() error {
	if e.dataDir == "" {
		return nil
	}
	e.diskMu.Lock()
	defer e.diskMu.Unlock()
	for _, td := range e.tables {
		td.syncShardsToMapsLocked()
		td.mu.Lock()
		for _, idx := range td.Indexes {
			idx.SyncEntries()
		}
		td.mu.Unlock()
	}
	tNames := make([]string, 0, len(e.tables))
	for name := range e.tables {
		tNames = append(tNames, name)
	}
	sort.Strings(tNames)
	for _, name := range tNames {
		td := e.tables[name]
		td.mu.RLock()
		td.partMu.RLock()
	}
	data, err := MarshalMsgPack(e.tables)
	for i := len(tNames) - 1; i >= 0; i-- {
		td := e.tables[tNames[i]]
		td.partMu.RUnlock()
		td.mu.RUnlock()
	}
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

	mpkFile := filepath.Join(e.dataDir, "tables.mpk")
	data, err := os.ReadFile(mpkFile)
	if err != nil || len(data) == 0 {
		return
	}

	var loaded map[string]*TableData
	if err := UnmarshalMsgPack(data, &loaded); err != nil || loaded == nil {
		return
	}

	e.tables = loaded
	for _, td := range e.tables {
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
		for _, idx := range td.Indexes {
			idx.getSkipList()
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
	}
}


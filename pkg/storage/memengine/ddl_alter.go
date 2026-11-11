package memengine

import (
	"fmt"
	"strings"

	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

type schemaMigrationDiff struct {
	srcIdx          []int
	renamedCols     map[string]string
	droppedCols     map[string]bool
	alteredTypeCols map[string]bool
	needReencode    bool
	needNullCheck   bool
}

func (e *Engine) applySchemaMigrationLocked(td *TableData, trialSchema *storage.TableSchema, persistWAL bool) error {
	td.mu.Lock()
	defer td.mu.Unlock()

	oldSchema := td.Schema.Clone()
	nextSchema := trialSchema.Clone()

	diff := computeSchemaMigrationDiff(&oldSchema, &nextSchema)

	if err := validateDropDependenciesLocked(e, &oldSchema, &nextSchema, diff.droppedCols); err != nil {
		return err
	}

	droppedIdxNames := cascadeDropAndRenameMetadata(&nextSchema, &diff)

	var newLayout *FlatRecordLayout
	if td.mode == MemoryCompact {
		newLayout = BuildFlatRecordLayout(&nextSchema)
	}

	stagedShards, rebuiltIndexes, err := stageMigratedShardsLocked(td, &oldSchema, &nextSchema, newLayout, &diff)
	if err != nil {
		return err
	}

	if persistWAL {
		walSchema := nextSchema.Clone()
		if _, err := e.recordWALLocked([]WALOp{{
			Type:   OpAlterTable,
			Table:  oldSchema.Name,
			Schema: &walSchema,
		}}); err != nil {
			return err
		}
	}

	td.Schema = nextSchema
	if newLayout != nil {
		td.layout = newLayout
	}
	td.refreshSnapSchemaLocked()
	if diff.needReencode {
		for i := 0; i < numTableShards; i++ {
			sh := &td.shards[i]
			sh.mu.Lock()
			sh.entries = stagedShards[i]
			sh.mu.Unlock()
		}
	}

	for _, name := range droppedIdxNames {
		sqlutil.MapDeleteFold(td.Indexes, name)
	}
	for _, idxSchema := range td.Schema.Indexes {
		lowerName := strings.ToLower(idxSchema.Name)
		if sl, rebuilt := rebuiltIndexes[lowerName]; rebuilt {
			sqlutil.MapDeleteFold(td.Indexes, idxSchema.Name)
			if td.Indexes == nil {
				td.Indexes = make(map[string]*IndexData)
			}
			td.Indexes[idxSchema.Name] = &IndexData{
				Schema: idxSchema,
				sl:     sl,
			}
			continue
		}
		if idxData, ok := sqlutil.MapGetFold(td.Indexes, idxSchema.Name); ok && idxData != nil {
			idxData.Schema = idxSchema
		}
	}

	e.rebuildFKCacheLocked()
	return nil
}

func computeSchemaMigrationDiff(oldSchema, nextSchema *storage.TableSchema) schemaMigrationDiff {
	diff := schemaMigrationDiff{
		srcIdx:          make([]int, len(nextSchema.Columns)),
		renamedCols:     make(map[string]string),
		droppedCols:     make(map[string]bool),
		alteredTypeCols: make(map[string]bool),
	}

	positional := len(oldSchema.Columns) == len(nextSchema.Columns)
	if positional {
		for i := range oldSchema.Columns {
			oldName := oldSchema.Columns[i].Name
			nextName := nextSchema.Columns[i].Name
			if strings.EqualFold(oldName, nextName) {
				continue
			}
			if _, _, inNext := nextSchema.FindColumn(oldName); inNext {
				positional = false
				break
			}
			if _, _, inOld := oldSchema.FindColumn(nextName); inOld {
				positional = false
				break
			}
		}
	}

	if positional {
		for i := range nextSchema.Columns {
			diff.srcIdx[i] = i
			if !strings.EqualFold(oldSchema.Columns[i].Name, nextSchema.Columns[i].Name) {
				diff.renamedCols[strings.ToLower(oldSchema.Columns[i].Name)] = nextSchema.Columns[i].Name
			}
		}
	} else {
		referenced := make([]bool, len(oldSchema.Columns))
		for j, nextCol := range nextSchema.Columns {
			if _, oi, ok := oldSchema.FindColumn(nextCol.Name); ok {
				diff.srcIdx[j] = oi
				referenced[oi] = true
			} else {
				diff.srcIdx[j] = -1
			}
		}
		for oi, used := range referenced {
			if !used {
				diff.droppedCols[strings.ToLower(oldSchema.Columns[oi].Name)] = true
				diff.needReencode = true
			}
		}
	}

	for j, nextCol := range nextSchema.Columns {
		oi := diff.srcIdx[j]
		if oi == -1 {
			diff.needReencode = true
			if !nextCol.Nullable && !nextCol.PrimaryKey {
				diff.needNullCheck = true
			}
			continue
		}
		oldCol := oldSchema.Columns[oi]
		if oi != j {
			diff.needReencode = true
		}
		if oldCol.Type != nextCol.Type || (nextCol.Type == storage.TypeDecimal && oldCol.Scale != nextCol.Scale) {
			diff.needReencode = true
			diff.alteredTypeCols[strings.ToLower(nextCol.Name)] = true
		}
		if !nextCol.Nullable && !nextCol.PrimaryKey && oldCol.Nullable {
			diff.needNullCheck = true
		}
	}
	return diff
}

func validateDropDependenciesLocked(e *Engine, oldSchema, nextSchema *storage.TableSchema, droppedCols map[string]bool) error {
	if len(droppedCols) == 0 {
		return nil
	}

	for _, col := range oldSchema.Columns {
		if col.PrimaryKey && droppedCols[strings.ToLower(col.Name)] {
			return fmt.Errorf("%w: cannot drop primary key column %q on table %q",
				storage.ErrDependentObjectsExist, col.Name, oldSchema.Name)
		}
	}

	if oldSchema.IsPartitioned() && droppedCols[strings.ToLower(oldSchema.Partition.Column)] {
		return fmt.Errorf("%w: cannot drop partition key column %q on table %q",
			storage.ErrDependentObjectsExist, oldSchema.Partition.Column, oldSchema.Name)
	}

	for _, fk := range nextSchema.ForeignKeys {
		for _, cc := range fk.ChildColumns {
			if droppedCols[strings.ToLower(cc)] {
				return fmt.Errorf("%w: cannot drop column %q referenced by foreign key %q on table %q",
					storage.ErrDependentObjectsExist, cc, fk.Name, oldSchema.Name)
			}
		}
	}

	for _, otherTD := range e.tables {
		for _, fk := range otherTD.Schema.ForeignKeys {
			if !strings.EqualFold(fk.ParentTable, oldSchema.Name) {
				continue
			}
			for _, pc := range fk.ParentColumns {
				if droppedCols[strings.ToLower(pc)] {
					return fmt.Errorf("%w: cannot drop column %q referenced by foreign key %q on table %q",
						storage.ErrDependentObjectsExist, pc, fk.Name, otherTD.Schema.Name)
				}
			}
		}
	}
	return nil
}

func indexUsesAnyCol(idx *storage.IndexSchema, colSet map[string]bool) bool {
	if len(colSet) == 0 || idx == nil {
		return false
	}
	for _, c := range idx.Columns {
		if colSet[strings.ToLower(c)] {
			return true
		}
	}
	for _, c := range idx.IncludeCols {
		if colSet[strings.ToLower(c)] {
			return true
		}
	}
	if idx.ExprColumn != "" && colSet[strings.ToLower(idx.ExprColumn)] {
		return true
	}
	return false
}

func cascadeDropAndRenameMetadata(nextSchema *storage.TableSchema, diff *schemaMigrationDiff) []string {
	var droppedIdxNames []string
	if len(nextSchema.Indexes) > 0 && (len(diff.droppedCols) > 0 || len(diff.renamedCols) > 0) {
		kept := make([]storage.IndexSchema, 0, len(nextSchema.Indexes))
		for _, idx := range nextSchema.Indexes {
			if indexUsesAnyCol(&idx, diff.droppedCols) {
				droppedIdxNames = append(droppedIdxNames, idx.Name)
				continue
			}
			for k, c := range idx.Columns {
				if newName, ok := diff.renamedCols[strings.ToLower(c)]; ok {
					idx.Columns[k] = newName
				}
			}
			for k, c := range idx.IncludeCols {
				if newName, ok := diff.renamedCols[strings.ToLower(c)]; ok {
					idx.IncludeCols[k] = newName
				}
			}
			if idx.ExprColumn != "" {
				if newName, ok := diff.renamedCols[strings.ToLower(idx.ExprColumn)]; ok {
					idx.ExprColumn = newName
				}
			}
			kept = append(kept, idx)
		}
		nextSchema.Indexes = kept
	}

	if len(diff.renamedCols) > 0 {
		for k := range nextSchema.ForeignKeys {
			for m, c := range nextSchema.ForeignKeys[k].ChildColumns {
				if newName, ok := diff.renamedCols[strings.ToLower(c)]; ok {
					nextSchema.ForeignKeys[k].ChildColumns[m] = newName
				}
			}
		}
		if nextSchema.Partition != nil && nextSchema.Partition.Column != "" {
			if newName, ok := diff.renamedCols[strings.ToLower(nextSchema.Partition.Column)]; ok {
				nextSchema.Partition.Column = newName
			}
		}
	}
	return droppedIdxNames
}

func stageMigratedShardsLocked(
	td *TableData,
	oldSchema *storage.TableSchema,
	nextSchema *storage.TableSchema,
	newLayout *FlatRecordLayout,
	diff *schemaMigrationDiff,
) ([numTableShards]map[string]shardEntry, map[string]*SkipList, error) {
	var staged [numTableShards]map[string]shardEntry
	for i := 0; i < numTableShards; i++ {
		staged[i] = make(map[string]shardEntry)
	}

	rebuiltIndexes := make(map[string]*SkipList)
	var rebuiltList []struct {
		schema storage.IndexSchema
		sl     *SkipList
	}
	for _, idx := range nextSchema.Indexes {
		if indexUsesAnyCol(&idx, diff.alteredTypeCols) {
			sl := NewSkipList(idx.Unique)
			rebuiltIndexes[strings.ToLower(idx.Name)] = sl
			rebuiltList = append(rebuiltList, struct {
				schema storage.IndexSchema
				sl     *SkipList
			}{schema: idx, sl: sl})
		}
	}

	if !diff.needReencode && !diff.needNullCheck && len(rebuiltList) == 0 {
		return staged, rebuiltIndexes, nil
	}

	// If table has zero rows, no existing entries need re-encoding or NOT NULL validation.
	if td.rowCount() == 0 {
		return staged, rebuiltIndexes, nil
	}

	newDefaults := make([]storage.Value, len(nextSchema.Columns))
	for j, col := range nextSchema.Columns {
		if diff.srcIdx[j] != -1 {
			continue
		}
		defVal := storage.NullValue(col.Type)
		if strings.TrimSpace(col.Default) != "" {
			defVal = storage.ParseLiteralDefault(col.Default, col.Type)
		}
		convDef, err := storage.ConvertValueToColumn(defVal, col)
		if err != nil {
			return staged, nil, err
		}
		newDefaults[j] = convDef
	}

	for i := 0; i < numTableShards; i++ {
		sh := &td.shards[i]
		sh.mu.RLock()
		if diff.needReencode {
			staged[i] = make(map[string]shardEntry, len(sh.entries))
		}
		var stageErr error
		for pk, entry := range sh.entries {
			row, err := entry.materializeWithLayout(td.mode, oldSchema, td.layout, nil)
			if err != nil {
				stageErr = err
				break
			}
			newVals := make([]storage.Value, len(nextSchema.Columns))
			for j, nextCol := range nextSchema.Columns {
				oi := diff.srcIdx[j]
				var rawVal storage.Value
				if oi == -1 {
					rawVal = newDefaults[j]
				} else if oi >= 0 && oi < len(row.Values) {
					rawVal = row.Values[oi]
				} else {
					rawVal = storage.NullValue(nextCol.Type)
				}
				convVal, err := storage.ConvertValueToColumn(rawVal, nextCol)
				if err != nil {
					stageErr = err
					break
				}
				newVals[j] = convVal
			}
			if stageErr != nil {
				break
			}

			newRow := storage.Row{Values: newVals}
			for _, rb := range rebuiltList {
				keyVal := extractIndexKeyFromRowWithSchema(nextSchema, newRow, &rb.schema)
				incVals := extractIncludeValuesFromRow(nextSchema, newRow, rb.schema.IncludeCols)
				if err := rb.sl.Insert(keyVal, pk, incVals); err != nil {
					stageErr = err
					break
				}
			}
			if stageErr != nil {
				break
			}

			if diff.needReencode {
				if td.mode == MemoryCompact {
					flatBuf, err := newLayout.EncodeRowToFlat(nextSchema, newRow)
					if err != nil {
						stageErr = err
						break
					}
					staged[i][pk] = shardEntry{ver: entry.ver, flatBuf: flatBuf}
				} else {
					staged[i][pk] = shardEntry{ver: entry.ver, row: newRow}
				}
			}
		}
		sh.mu.RUnlock()
		if stageErr != nil {
			return staged, nil, stageErr
		}
	}

	return staged, rebuiltIndexes, nil
}

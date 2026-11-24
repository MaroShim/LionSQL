package memengine

import (
	"fmt"
	"sort"
	"strings"

	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

func (e *Engine) CreateTable(schema *storage.TableSchema) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(schema.Columns) > storage.MaxColumnsPerTable {
		return fmt.Errorf("%w: table %q has %d columns (max %d)", storage.ErrTooManyColumns, schema.Name, len(schema.Columns), storage.MaxColumnsPerTable)
	}
	if err := schema.ValidateRowSize(); err != nil {
		return err
	}

	if _, exists := sqlutil.MapGetFold(e.tables, schema.Name); exists {
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

		// Check for self-referencing foreign key first
		if strings.EqualFold(fk.ParentTable, schema.Name) ||
			strings.EqualFold(cleanTablePrefix(fk.ParentTable), cleanTablePrefix(schema.Name)) {
			fk.ParentTable = schema.Name
			if len(fk.ParentColumns) == 0 {
				pkCol, err := schema.PrimaryKeyColumn()
				if err != nil || pkCol == nil {
					return fmt.Errorf("referenced parent table %q has no primary key", fk.ParentTable)
				}
				fk.ParentColumns = []string{pkCol.Name}
			}
			continue
		}

		parentTd, exists := e.tables[fk.ParentTable]
		if !exists {
			for name, t := range e.tables {
				if strings.EqualFold(name, fk.ParentTable) ||
					strings.EqualFold(cleanTablePrefix(name), cleanTablePrefix(fk.ParentTable)) {
					parentTd = t
					exists = true
					break
				}
			}
		}
		if !exists {
			return fmt.Errorf("%w: referenced parent table %q does not exist", storage.ErrTableNotFound, fk.ParentTable)
		}
		fk.ParentTable = parentTd.Schema.Name
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
		Rows:       make(map[string]storage.Row),
		Versions:   make(map[string]int64),
		Keys:       make([]string, 0),
		Indexes:    make(map[string]*IndexData),
		Partitions: make(map[string]map[string]struct{}),
		mode:       e.opts.Mode,
	}
	td.layout = BuildFlatRecordLayout(schema)
	td.refreshSnapSchemaLocked()
	td.initShards()
	for _, idx := range schema.Indexes {
		td.Indexes[idx.Name] = &IndexData{
			Schema:  idx,
			Entries: make([]IndexEntry, 0),
		}
	}
	e.tables[schema.Name] = td
	e.rebuildFKCacheLocked()

	if !storage.IsTemporaryTable(schema.Name) {
		if _, err := e.recordWALLocked([]WALOp{{
			Type:   OpCreateTable,
			Table:  schema.Name,
			Schema: schema,
		}}); err != nil {
			delete(e.tables, schema.Name)
			e.rebuildFKCacheLocked()
			return err
		}
	}
	return nil
}

func (e *Engine) CreateIndex(index *storage.IndexSchema) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	td, exists := e.tables[index.TableName]
	if !exists {
		for name, t := range e.tables {
			if strings.EqualFold(name, index.TableName) ||
				strings.EqualFold(cleanTablePrefix(name), cleanTablePrefix(index.TableName)) {
				td = t
				exists = true
				break
			}
		}
	}
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, index.TableName)
	}
	index.TableName = td.Schema.Name

	if _, _, exists := td.Schema.FindIndex(index.Name); exists {
		return fmt.Errorf("%w: %s", storage.ErrIndexExists, index.Name)
	}
	if len(td.Schema.Indexes) >= storage.MaxIndexesPerTable {
		return fmt.Errorf("%w: table %q has reached maximum index limit (%d)", storage.ErrTooManyIndexes, index.TableName, storage.MaxIndexesPerTable)
	}

	// Validate columns
	if index.IsExpression() {
		colName := index.ExprColumn
		if colName == "" && len(index.Columns) > 0 {
			colName = index.Columns[0]
		}
		if colName != "" {
			if _, _, found := td.Schema.FindColumn(colName); !found {
				// For functional expression indexes, allow if column exists in table under normalized name
				foundCol := false
				for _, c := range td.Schema.Columns {
					if strings.EqualFold(c.Name, colName) || strings.EqualFold(cleanTablePrefix(c.Name), cleanTablePrefix(colName)) {
						foundCol = true
						break
					}
				}
				if !foundCol {
					// Fall back to tolerant expression index registration
					index.ExprColumn = ""
				}
			}
		}
	} else {
		if err := index.Validate(&td.Schema); err != nil {
			return err
		}
	}
	if index.IsExpression() {
		if err := storage.ValidateIdentifier(index.Name); err != nil {
			return err
		}
	}
	for _, colName := range index.IncludeCols {
		if _, _, found := td.Schema.FindColumn(colName); !found {
			return fmt.Errorf("%w: %s on table %s", storage.ErrColumnNotFound, colName, index.TableName)
		}
	}

	idxData := &IndexData{
		Schema:  *index,
		Entries: make([]IndexEntry, 0),
	}

	// Acquire table-level exclusive write lock across the entire index build and registration
	// to prevent concurrent transactions from committing and missing the new index ([RULE:ConcurrencySafety]).
	td.mu.Lock()

	// Populate index from existing rows
	var insertErr error
	scanErr := td.forEachRowLocked(func(pk string, row storage.Row) bool {
		keyVal := extractIndexKeyFromRowWithSchema(&td.Schema, row, index)
		incVals := extractIncludeValuesFromRow(&td.Schema, row, index.IncludeCols)
		if err := idxData.Insert(keyVal, pk, incVals); err != nil {
			insertErr = err
			return false
		}
		return true
	})
	if scanErr != nil {
		td.mu.Unlock()
		return scanErr
	}
	if insertErr != nil {
		td.mu.Unlock()
		return insertErr
	}

	if td.Indexes == nil {
		td.Indexes = make(map[string]*IndexData)
	}
	td.Indexes[index.Name] = idxData
	td.Schema.Indexes = append(td.Schema.Indexes, *index)
	td.refreshSnapSchemaLocked()
	td.mu.Unlock()

	if !storage.IsTemporaryTable(index.TableName) {
		if _, err := e.recordWALLocked([]WALOp{{
			Type:        OpCreateIndex,
			Table:       index.TableName,
			IndexSchema: index,
		}}); err != nil {
			td.mu.Lock()
			delete(td.Indexes, index.Name)
			for i := range td.Schema.Indexes {
				if td.Schema.Indexes[i].Name == index.Name {
					td.Schema.Indexes = append(td.Schema.Indexes[:i], td.Schema.Indexes[i+1:]...)
					break
				}
			}
			td.refreshSnapSchemaLocked()
			td.mu.Unlock()
			return err
		}
	}
	return nil
}

func (e *Engine) DropIndex(tableName, indexName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	td, exists := sqlutil.MapGetFold(e.tables, tableName)
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	foundIdx, idxPos, exists := td.Schema.FindIndex(indexName)
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrIndexNotFound, indexName)
	}

	backupIdxData := td.Indexes[foundIdx.Name]
	backupSchemaIdx := *foundIdx

	sqlutil.MapDeleteFold(td.Indexes, foundIdx.Name)
	td.Schema.Indexes = append(td.Schema.Indexes[:idxPos], td.Schema.Indexes[idxPos+1:]...)
	td.refreshSnapSchemaLocked()

	if !storage.IsTemporaryTable(tableName) {
		if _, err := e.recordWALLocked([]WALOp{{
			Type:      OpDropIndex,
			Table:     tableName,
			IndexName: indexName,
		}}); err != nil {
			if backupIdxData != nil {
				td.Indexes[backupSchemaIdx.Name] = backupIdxData
			}
			td.Schema.Indexes = append(td.Schema.Indexes[:idxPos], append([]storage.IndexSchema{backupSchemaIdx}, td.Schema.Indexes[idxPos:]...)...)
			td.refreshSnapSchemaLocked()
			return err
		}
	}
	return nil
}

func extractIndexKeyFromRecord(rec storage.Record, idx *storage.IndexSchema) storage.Value {
	return storage.ExtractIndexKeyFromRecord(rec, idx)
}

func extractIndexKeyFromRowWithSchema(schema *storage.TableSchema, row storage.Row, idx *storage.IndexSchema) storage.Value {
	if idx != nil && idx.IsExpression() {
		colName := idx.ExprColumn
		if colName == "" && len(idx.Columns) > 0 {
			colName = idx.Columns[0]
		}
		if schema != nil {
			if _, colIdx, found := schema.FindColumn(colName); found && colIdx >= 0 && colIdx < len(row.Values) {
				docVal := row.Values[colIdx]
				if docVal.IsNull {
					return storage.NullValue(storage.TypeVarChar)
				}
				return storage.ExtractJSONIndexKey(docVal.String(), idx.ExprPath)
			}
		}
		return storage.NullValue(storage.TypeVarChar)
	}
	if idx != nil {
		return extractIndexKeyFromRow(schema, row, idx.Columns)
	}
	return storage.NullValue(storage.TypeUnknown)
}

func extractIndexKey(rec storage.Record, cols []string) storage.Value {
	if len(cols) == 0 {
		return storage.NullValue(storage.TypeUnknown)
	}
	if len(cols) == 1 {
		if v, ok := rec.Get(cols[0]); ok {
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
		if v, ok := rec.Get(c); ok {
			sb.WriteString(storage.EncodeIndexKeyPart(v))
		} else {
			sb.WriteString("\x00NULL")
		}
	}
	return storage.StringValue(sb.String())
}

func extractIndexKeyFromRow(schema *storage.TableSchema, row storage.Row, cols []string) storage.Value {
	if len(cols) == 0 {
		return storage.NullValue(storage.TypeUnknown)
	}
	if len(cols) == 1 {
		if schema != nil {
			if _, idx, found := schema.FindColumn(cols[0]); found && idx >= 0 && idx < len(row.Values) {
				return row.Values[idx]
			}
		}
		return storage.NullValue(storage.TypeUnknown)
	}
	var sb strings.Builder
	for i, c := range cols {
		if i > 0 {
			sb.WriteByte(0)
		}
		if schema != nil {
			if _, idx, found := schema.FindColumn(c); found && idx >= 0 && idx < len(row.Values) {
				sb.WriteString(storage.EncodeIndexKeyPart(row.Values[idx]))
				continue
			}
		}
		sb.WriteString("\x00NULL")
	}
	return storage.StringValue(sb.String())
}

func extractIncludeValues(rec storage.Record, cols []string) []storage.Value {
	if len(cols) == 0 {
		return nil
	}
	vals := make([]storage.Value, len(cols))
	for i, c := range cols {
		if v, ok := rec[c]; ok {
			vals[i] = v
		} else {
			vals[i] = storage.NullValue(storage.TypeUnknown)
		}
	}
	return vals
}

func extractIncludeValuesFromRow(schema *storage.TableSchema, row storage.Row, cols []string) []storage.Value {
	if len(cols) == 0 {
		return nil
	}
	vals := make([]storage.Value, len(cols))
	for i, c := range cols {
		if schema != nil {
			if _, idx, found := schema.FindColumn(c); found && idx >= 0 && idx < len(row.Values) {
				vals[i] = row.Values[idx]
				continue
			}
		}
		vals[i] = storage.NullValue(storage.TypeUnknown)
	}
	return vals
}

func rowMatchesFK(schema *storage.TableSchema, row storage.Row, cols []string, vals []storage.Value) bool {
	if len(cols) != len(vals) || schema == nil {
		return false
	}
	for i, col := range cols {
		_, idx, found := schema.FindColumn(col)
		if !found || idx < 0 || idx >= len(row.Values) {
			return false
		}
		v := row.Values[idx]
		if v.IsNull {
			return false
		}
		if !valuesEqual(v, vals[i]) {
			return false
		}
	}
	return true
}

func (e *Engine) DropTable(tableName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	targetName := tableName
	td, exists := sqlutil.MapGetFold(e.tables, targetName)
	if !exists {
		cleaned := cleanTablePrefix(tableName)
		td, exists = sqlutil.MapGetFold(e.tables, cleaned)
		if exists {
			targetName = cleaned
		} else {
			for name, t := range e.tables {
				if strings.EqualFold(cleanTablePrefix(name), cleaned) {
					td = t
					targetName = name
					exists = true
					break
				}
			}
		}
	}
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	// Verify no other table has a foreign key referencing this table
	for otherName, otherTd := range e.tables {
		if strings.EqualFold(otherName, td.Schema.Name) {
			continue
		}
		for _, fk := range otherTd.Schema.ForeignKeys {
			if strings.EqualFold(fk.ParentTable, td.Schema.Name) {
				return fmt.Errorf("%w: table %q is referenced by foreign key constraint %q on table %q",
					storage.ErrDependentObjectsExist, tableName, fk.Name, otherName)
			}
		}
	}

	sqlutil.MapDeleteFold(e.tables, targetName)
	e.rebuildFKCacheLocked()
	if !storage.IsTemporaryTable(td.Schema.Name) {
		if _, err := e.recordWALLocked([]WALOp{{
			Type:  OpDropTable,
			Table: td.Schema.Name,
		}}); err != nil {
			e.tables[td.Schema.Name] = td
			e.rebuildFKCacheLocked()
			return err
		}
	}
	return nil
}

func (e *Engine) TruncateTables(tableNames []string, cascade bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	targetSet := make(map[string]*TableData, len(tableNames))
	for _, name := range tableNames {
		td, exists := sqlutil.MapGetFold(e.tables, name)
		if !exists {
			return fmt.Errorf("%w: %s", storage.ErrTableNotFound, name)
		}
		targetSet[td.Schema.Name] = td
	}

	if cascade {
		// Transitively find all referencing tables
		changed := true
		for changed {
			changed = false
			for _, otherTd := range e.tables {
				if _, already := targetSet[otherTd.Schema.Name]; already {
					continue
				}
				for _, fk := range otherTd.Schema.ForeignKeys {
					if _, parentInSet := targetSet[fk.ParentTable]; parentInSet {
						targetSet[otherTd.Schema.Name] = otherTd
						changed = true
						break
					}
				}
			}
		}
	}

	// Verify that any FK referencing any table in targetSet also belongs to targetSet
	for otherName, otherTd := range e.tables {
		if _, inSet := targetSet[otherTd.Schema.Name]; inSet {
			continue
		}
		for _, fk := range otherTd.Schema.ForeignKeys {
			if _, parentInSet := targetSet[fk.ParentTable]; parentInSet {
				return fmt.Errorf("%w: cannot truncate table %q because it is referenced by foreign key %q on table %q",
					storage.ErrDependentObjectsExist, fk.ParentTable, fk.Name, otherName)
			}
		}
	}

	// Record WAL operations before truncating in-memory shard entries
	var walOps []WALOp
	for _, td := range targetSet {
		if !storage.IsTemporaryTable(td.Schema.Name) {
			walOps = append(walOps, WALOp{
				Type:  OpTruncateTable,
				Table: td.Schema.Name,
			})
		}
	}
	if len(walOps) > 0 {
		if _, err := e.recordWALLocked(walOps); err != nil {
			return err
		}
	}

	for _, td := range targetSet {
		td.mu.Lock()
		td.truncateShardsLocked()
		td.Partitions = make(map[string]map[string]struct{})
		for _, idx := range td.Indexes {
			idx.sl = NewSkipList(idx.Schema.Unique)
			idx.Entries = nil
		}
		td.mu.Unlock()
	}
	return nil
}

func (e *Engine) TruncateTable(tableName string) error {
	return e.TruncateTables([]string{tableName}, false)
}

func (e *Engine) DropPartition(tableName, partitionName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	td, exists := sqlutil.MapGetFold(e.tables, tableName)
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	td.partMu.Lock()
	defer td.partMu.Unlock()

	if !td.Schema.IsPartitioned() {
		return fmt.Errorf("%w: %s", storage.ErrTableNotPartitioned, tableName)
	}

	pksSet, ok := td.Partitions[partitionName]
	if !ok || len(pksSet) == 0 {
		return fmt.Errorf("%w: partition %q in table %q", storage.ErrPartitionNotFound, partitionName, tableName)
	}

	if !storage.IsTemporaryTable(td.Schema.Name) {
		if _, err := e.recordWALLocked([]WALOp{{
			Type:  OpDropPartition,
			Table: td.Schema.Name,
			Key:   partitionName,
		}}); err != nil {
			return err
		}
	}

	// Delete all rows in partition and purge secondary index references
	for pk := range pksSet {
		oldRow, hadOld := td.deleteRowAndVerLocked(pk)
		if hadOld {
			for _, idxData := range td.Indexes {
				oldKey := extractIndexKeyFromRowWithSchema(&td.Schema, oldRow, &idxData.Schema)
				idxData.Delete(oldKey, pk)
			}
		}
	}
	delete(td.Partitions, partitionName)

	// Rebuild keys
	td.Keys = td.collectKeys(nil)

	return nil
}

func (e *Engine) GetPartitions(tableName string) ([]storage.PartitionInfo, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	td, exists := sqlutil.MapGetFold(e.tables, tableName)
	if !exists {
		return nil, fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	td.mu.RLock()
	defer td.mu.RUnlock()

	if !td.Schema.IsPartitioned() {
		return nil, fmt.Errorf("%w: %s", storage.ErrTableNotPartitioned, tableName)
	}

	td.partMu.RLock()
	defer td.partMu.RUnlock()

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

	td, exists := sqlutil.MapGetFold(e.tables, tableName)
	if !exists {
		for name, t := range e.tables {
			if strings.EqualFold(cleanTablePrefix(name), cleanTablePrefix(tableName)) {
				td = t
				exists = true
				break
			}
		}
	}
	if !exists {
		return nil, fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}
	td.mu.RLock()
	cp := td.Schema.Clone()
	td.mu.RUnlock()
	return &cp, nil
}

func (e *Engine) ListTables() ([]*storage.TableSchema, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]*storage.TableSchema, 0, len(e.tables))
	for _, td := range e.tables {
		td.mu.RLock()
		cp := td.Schema.Clone()
		td.mu.RUnlock()
		result = append(result, &cp)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func (e *Engine) AddColumn(tableName string, col *storage.Column) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	td, exists := sqlutil.MapGetFold(e.tables, tableName)
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	for _, c := range td.Schema.Columns {
		if strings.EqualFold(c.Name, col.Name) {
			return fmt.Errorf("column %q already exists in table %q", col.Name, tableName)
		}
	}

	if !col.Nullable && strings.TrimSpace(col.Default) == "" && td.rowCount() > 0 {
		return fmt.Errorf("cannot add NOT NULL column %q with no default value to non-empty table %q", col.Name, tableName)
	}

	trialSchema := td.Schema.Clone()
	trialSchema.Columns = append(trialSchema.Columns, *col)
	return e.applySchemaMigrationLocked(td, &trialSchema, !storage.IsTemporaryTable(td.Schema.Name))
}

func (e *Engine) RenameTable(oldName, newName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	targetOld := oldName
	td, exists := sqlutil.MapGetFold(e.tables, targetOld)
	if !exists {
		oldClean := cleanTablePrefix(oldName)
		td, exists = sqlutil.MapGetFold(e.tables, oldClean)
		if exists {
			targetOld = oldClean
		} else {
			for name, t := range e.tables {
				if strings.EqualFold(cleanTablePrefix(name), oldClean) {
					td = t
					targetOld = name
					exists = true
					break
				}
			}
		}
	}
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, oldName)
	}

	targetNew := newName
	if !strings.HasPrefix(targetNew, "__") {
		targetNew = cleanTablePrefix(newName)
	}

	if _, already := sqlutil.MapGetFold(e.tables, targetNew); already {
		return fmt.Errorf("%w: %s", storage.ErrTableExists, targetNew)
	}

	if !storage.IsTemporaryTable(targetNew) {
		if _, err := e.recordWALLocked([]WALOp{{
			Type:  OpRenameTable,
			Table: targetNew,
			Key:   targetOld,
		}}); err != nil {
			return err
		}
	}

	for k := range e.tables {
		if strings.EqualFold(k, targetOld) {
			delete(e.tables, k)
		}
	}

	td.mu.Lock()
	td.Schema.Name = targetNew
	td.refreshSnapSchemaLocked()
	td.mu.Unlock()
	e.tables[targetNew] = td

	// Update any foreign key definitions on other tables referencing targetOld
	for _, otherTd := range e.tables {
		otherTd.mu.Lock()
		changed := false
		for i := range otherTd.Schema.ForeignKeys {
			if strings.EqualFold(otherTd.Schema.ForeignKeys[i].ParentTable, targetOld) {
				otherTd.Schema.ForeignKeys[i].ParentTable = targetNew
				changed = true
			}
		}
		if changed {
			otherTd.refreshSnapSchemaLocked()
		}
		otherTd.mu.Unlock()
	}

	e.rebuildFKCacheLocked()

	return nil
}

func (e *Engine) AlterTable(tableName string, mutator func(schema *storage.TableSchema) error) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	td, exists := sqlutil.MapGetFold(e.tables, tableName)
	if !exists {
		return fmt.Errorf("%w: %s", storage.ErrTableNotFound, tableName)
	}

	trialSchema := td.Schema.Clone()
	if err := mutator(&trialSchema); err != nil {
		return err
	}
	if len(trialSchema.Columns) > storage.MaxColumnsPerTable {
		return fmt.Errorf("%w: altered table %q would have %d columns (max %d)", storage.ErrTooManyColumns, tableName, len(trialSchema.Columns), storage.MaxColumnsPerTable)
	}
	if err := trialSchema.ValidateRowSize(); err != nil {
		return err
	}

	return e.applySchemaMigrationLocked(td, &trialSchema, !storage.IsTemporaryTable(td.Schema.Name))
}

func cleanTablePrefix(s string) string {
	s = strings.Trim(strings.TrimSpace(s), "[]\"'`")
	if idx := strings.LastIndex(s, "."); idx != -1 {
		return strings.Trim(s[idx+1:], "[]\"'`")
	}
	return s
}



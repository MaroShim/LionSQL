package memengine

import (
	"fmt"
	"sort"
	"strings"

	"sqlion/pkg/storage"
)

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
		Rows:       make(map[string]storage.Row),
		Versions:   make(map[string]int64),
		Keys:       make([]string, 0),
		Indexes:    make(map[string]*IndexData),
		Partitions: make(map[string]map[string]struct{}),
	}
	td.initShards()
	for _, idx := range schema.Indexes {
		td.Indexes[idx.Name] = &IndexData{
			Schema:  idx,
			Entries: make([]IndexEntry, 0),
		}
	}
	e.tables[schema.Name] = td

	if _, err := e.recordWALLocked([]WALOp{{
		Type:   OpCreateTable,
		Table:  schema.Name,
		Schema: schema,
	}}); err != nil {
		return err
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
	if index.IsExpression() {
		colName := index.ExprColumn
		if colName == "" && len(index.Columns) > 0 {
			colName = index.Columns[0]
		}
		if _, _, found := td.Schema.FindColumn(colName); !found {
			return fmt.Errorf("%w: column %s on table %s", storage.ErrColumnNotFound, colName, index.TableName)
		}
	} else {
		for _, colName := range index.Columns {
			if _, _, found := td.Schema.FindColumn(colName); !found {
				return fmt.Errorf("%w: %s on table %s", storage.ErrColumnNotFound, colName, index.TableName)
			}
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

	// Populate index from existing rows
	var insertErr error
	td.forEachRow(func(pk string, row storage.Row) bool {
		keyVal := extractIndexKeyFromRowWithSchema(&td.Schema, row, index)
		incVals := extractIncludeValuesFromRow(&td.Schema, row, index.IncludeCols)
		if err := idxData.Insert(keyVal, pk, incVals); err != nil {
			insertErr = err
			return false
		}
		return true
	})
	if insertErr != nil {
		return insertErr
	}

	if td.Indexes == nil {
		td.Indexes = make(map[string]*IndexData)
	}
	td.Indexes[index.Name] = idxData
	td.Schema.Indexes = append(td.Schema.Indexes, *index)

	if _, err := e.recordWALLocked([]WALOp{{
		Type:        OpCreateIndex,
		Table:       index.TableName,
		IndexSchema: index,
	}}); err != nil {
		return err
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

	if _, err := e.recordWALLocked([]WALOp{{
		Type:      OpDropIndex,
		Table:     tableName,
		IndexName: indexName,
	}}); err != nil {
		return err
	}
	return nil
}

func extractIndexKeyFromRecord(rec storage.Record, idx *storage.IndexSchema) storage.Value {
	if idx != nil && idx.IsExpression() {
		colName := idx.ExprColumn
		if colName == "" && len(idx.Columns) > 0 {
			colName = idx.Columns[0]
		}
		if docVal, ok := rec[colName]; ok && !docVal.IsNull {
			return storage.ExtractJSONIndexKey(docVal.String(), idx.ExprPath)
		}
		return storage.NullValue(storage.TypeVarChar)
	}
	if idx != nil {
		return extractIndexKey(rec, idx.Columns)
	}
	return storage.NullValue(storage.TypeUnknown)
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
		sb.WriteString("NULL")
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
	if _, err := e.recordWALLocked([]WALOp{{
		Type:  OpDropTable,
		Table: tableName,
	}}); err != nil {
		return err
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
	td.truncateShardsLocked()
	td.Partitions = make(map[string]map[string]struct{})
	for _, idx := range td.Indexes {
		idx.sl = NewSkipList(idx.Schema.Unique)
		idx.Entries = nil
	}
	td.mu.Unlock()

	if _, err := e.recordWALLocked([]WALOp{{
		Type:  OpTruncateTable,
		Table: tableName,
	}}); err != nil {
		return err
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
		oldRow, hadOld := td.deleteRowAndVer(pk)
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

	if _, err := e.recordWALLocked([]WALOp{{
		Type:  OpDropPartition,
		Table: tableName,
		Key:   partitionName,
	}}); err != nil {
		return err
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


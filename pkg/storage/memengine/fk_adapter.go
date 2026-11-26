package memengine

import (
	"strings"

	"lionsql/pkg/storage"
	"lionsql/pkg/storage/fkengine"
)

// memFkAdapter adapts a memengine.Transaction to implement fkengine.StorageTx.
type memFkAdapter struct {
	tx *Transaction
}

func (a *memFkAdapter) GetSchema(tableName string) (*storage.TableSchema, error) {
	td, err := a.tx.getTableLocked(tableName)
	if err != nil {
		return nil, err
	}
	return td.SnapshotSchema(), nil
}

func (a *memFkAdapter) HasRowByPK(tableName string, pk string, lockParent bool) (bool, error) {
	if a.tx.isLocallyDeleted(tableName, pk) {
		return false, nil
	}
	if _, inLocal := a.tx.getLocal(tableName, pk); inLocal {
		return true, nil
	}
	td, err := a.tx.getTableLocked(tableName)
	if err != nil {
		return false, err
	}
	if !td.hasKey(pk) {
		return false, nil
	}
	if lockParent {
		if err := a.tx.tryAcquireRefLockLocked(td, pk); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (a *memFkAdapter) SeekUniqueIndex(tableName string, cols []string, vals []storage.Value, lockParent bool) (bool, error) {
	td, err := a.tx.getTableLocked(tableName)
	if err != nil {
		return false, err
	}

	var matchingIdx *IndexData
	for _, idxData := range td.Indexes {
		if idxData.Schema.Disabled {
			continue
		}
		if len(idxData.Schema.Columns) == len(cols) {
			matchAll := true
			for ci, col := range cols {
				if !strings.EqualFold(idxData.Schema.Columns[ci], col) {
					matchAll = false
					break
				}
			}
			if matchAll {
				matchingIdx = idxData
				break
			}
		}
	}

	if matchingIdx == nil {
		return false, storage.ErrIndexNotFound
	}

	var searchKey storage.Value
	if len(vals) == 1 {
		searchKey = vals[0]
	} else {
		var sb strings.Builder
		for i, v := range vals {
			if i > 0 {
				sb.WriteByte(0)
			}
			sb.WriteString(storage.EncodeIndexKeyPart(v))
		}
		searchKey = storage.StringValue(sb.String())
	}

	pks := matchingIdx.ScanRange(&searchKey, &searchKey)
	for _, parentKey := range pks {
		if a.tx.isLocallyDeleted(tableName, parentKey) {
			continue
		}
		if _, inLocal := a.tx.getLocal(tableName, parentKey); inLocal {
			return true, nil
		}
		if td.hasKey(parentKey) {
			if lockParent {
				if err := a.tx.tryAcquireRefLockLocked(td, parentKey); err != nil {
					return false, err
				}
			}
			return true, nil
		}
	}
	return false, nil
}

func (a *memFkAdapter) ScanRowsForFK(tableName string, matchFunc func(row storage.Row) bool, lockParent bool) (bool, error) {
	td, err := a.tx.getTableLocked(tableName)
	if err != nil {
		return false, err
	}

	var matchedPK string
	scanErr := td.forEachRow(func(pkStr string, row storage.Row) bool {
		if a.tx.isLocallyDeleted(tableName, pkStr) {
			return true
		}
		if _, inLocal := a.tx.getLocal(tableName, pkStr); inLocal {
			return true
		}
		if matchFunc(row) {
			matchedPK = pkStr
			return false
		}
		return true
	})
	if scanErr != nil {
		return false, scanErr
	}
	if matchedPK != "" {
		if lockParent {
			if err := a.tx.tryAcquireRefLockLocked(td, matchedPK); err != nil {
				return false, err
			}
		}
		return true, nil
	}

	if localRows, ok := a.tx.local[tableName]; ok {
		for pkStr, row := range localRows {
			if a.tx.isLocallyDeleted(tableName, pkStr) {
				continue
			}
			if matchFunc(row) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (a *memFkAdapter) FindChildPKs(childTable string, fk storage.ForeignKeySchema, parentValues []storage.Value) ([]storage.Value, error) {
	childTd, err := a.tx.getTableLocked(childTable)
	if err != nil {
		return nil, err
	}
	childSchema := childTd.SnapshotSchema()
	if _, err := childSchema.PrimaryKeyColumn(); err != nil {
		return nil, err
	}

	var matchingPKs []storage.Value
	scanErr := childTd.forEachRow(func(pkStr string, row storage.Row) bool {
		if a.tx.isLocallyDeleted(childTable, pkStr) {
			return true
		}
		if _, inLocal := a.tx.getLocal(childTable, pkStr); inLocal {
			return true
		}
		if fkengine.RowMatchesFK(childSchema, row, fk.ChildColumns, parentValues) {
			if childPK, ok := childSchema.ResolveRowPK(row); ok {
				matchingPKs = append(matchingPKs, childPK)
			}
		}
		return true
	})
	if scanErr != nil {
		return nil, scanErr
	}

	if localRows, ok := a.tx.local[childTable]; ok {
		for pkStr, row := range localRows {
			if a.tx.isLocallyDeleted(childTable, pkStr) {
				continue
			}
			if fkengine.RowMatchesFK(childSchema, row, fk.ChildColumns, parentValues) {
				if childPK, ok := childSchema.ResolveRowPK(row); ok {
					matchingPKs = append(matchingPKs, childPK)
				}
			}
		}
	}
	return matchingPKs, nil
}

func (a *memFkAdapter) CascadeDelete(childTable string, pk storage.Value, depth int) error {
	return a.tx.deleteLocked(childTable, pk, depth)
}

func (a *memFkAdapter) CascadeUpdate(childTable string, pk storage.Value, patch storage.Record, depth int) error {
	return a.tx.updateLocked(childTable, pk, patch, depth)
}

func toFkChildRefs(refs []ChildFKRef) []fkengine.ChildReference {
	out := make([]fkengine.ChildReference, len(refs))
	for i, r := range refs {
		out[i] = fkengine.ChildReference{
			ChildTable: r.ChildTable,
			FK:         r.FK,
		}
	}
	return out
}

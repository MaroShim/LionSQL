package wiredtiger

import (
	"strings"

	"lionsql/pkg/storage"
	"lionsql/pkg/storage/fkengine"
)

// wtFkAdapter adapts a WTTx to implement fkengine.StorageTx.
type wtFkAdapter struct {
	tx *WTTx
}

func (a *wtFkAdapter) GetSchema(tableName string) (*storage.TableSchema, error) {
	return a.tx.getTableLocked(tableName)
}

func (a *wtFkAdapter) HasRowByPK(tableName string, pk string, lockParent bool) (bool, error) {
	// In WiredTiger, check if the parent PK exists by seeking raw bytes
	_, err := a.tx.getRawBytesLocked(tableName, storage.StringValue(pk))
	if err != nil {
		return false, nil
	}
	return true, nil
}

func (a *wtFkAdapter) SeekUniqueIndex(tableName string, cols []string, vals []storage.Value, lockParent bool) (bool, error) {
	tbl, err := a.tx.getTableLocked(tableName)
	if err != nil {
		return false, err
	}

	var matchingIdx *storage.IndexSchema
	for i := range tbl.Indexes {
		idx := &tbl.Indexes[i]
		if len(idx.Columns) == len(cols) {
			match := true
			for cIdx, col := range idx.Columns {
				if !strings.EqualFold(col, cols[cIdx]) {
					match = false
					break
				}
			}
			if match {
				matchingIdx = idx
				break
			}
		}
	}

	if matchingIdx == nil {
		return false, storage.ErrIndexNotFound
	}

	idxKey := extractCompositeIndexKey(vals)
	var cur storage.Cursor
	if storage.IsTemporaryTable(tableName) {
		ttx, tErr := a.tx.ensureTempTxLocked()
		if tErr != nil {
			return false, tErr
		}
		cur, err = ttx.IndexScan(tableName, matchingIdx.Name, &idxKey, &idxKey)
	} else {
		cur, err = a.tx.indexScanBTreeLocked(tableName, matchingIdx.Name, &idxKey, &idxKey, false)
	}
	if err != nil {
		return false, err
	}
	defer cur.Close()
	return cur.Next(), nil
}

func (a *wtFkAdapter) ScanRowsForFK(tableName string, matchFunc func(row storage.Row) bool, lockParent bool) (bool, error) {
	cur, err := a.tx.scanLocked(tableName)
	if err != nil {
		return false, err
	}
	defer cur.Close()

	for cur.Next() {
		row, rErr := cur.Row()
		if rErr == nil && matchFunc(row) {
			return true, nil
		}
	}
	return false, nil
}

func (a *wtFkAdapter) FindChildPKs(childTable string, fk storage.ForeignKeySchema, parentValues []storage.Value) ([]storage.Value, error) {
	cTbl, err := a.tx.getTableLocked(childTable)
	if err != nil {
		return nil, err
	}
	if _, err := cTbl.PrimaryKeyColumn(); err != nil {
		return nil, err
	}

	cur, err := a.tx.scanLocked(childTable)
	if err != nil {
		return nil, err
	}
	defer cur.Close()

	var matchingPKs []storage.Value
	for cur.Next() {
		row, rErr := cur.Row()
		if rErr == nil && fkengine.RowMatchesFK(cTbl, row, fk.ChildColumns, parentValues) {
			if childPK, ok := cTbl.ResolveRowPK(row); ok && !childPK.IsNull {
				matchingPKs = append(matchingPKs, childPK)
			}
		}
	}
	return matchingPKs, nil
}

func (a *wtFkAdapter) CascadeDelete(childTable string, pk storage.Value, depth int) error {
	return a.tx.deleteLocked(childTable, pk)
}

func (a *wtFkAdapter) CascadeUpdate(childTable string, pk storage.Value, patch storage.Record, depth int) error {
	cTbl, err := a.tx.getTableLocked(childTable)
	if err != nil {
		return err
	}
	return a.tx.updateLocked(childTable, cTbl, pk, patch)
}

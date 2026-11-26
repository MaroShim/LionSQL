package fkengine

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"lionsql/pkg/storage"
)

// StorageTx is the abstraction contract required by ForeignKeyEngine to perform
// foreign key constraint validation, index lookups, and cascaded updates/deletions.
// Both memengine.Transaction and wiredtiger.WTTx implement this interface.
type StorageTx interface {
	// GetSchema retrieves the snapshot TableSchema for a table.
	GetSchema(tableName string) (*storage.TableSchema, error)

	// HasRowByPK checks if a row with the given string-encoded primary key exists in the parent table.
	// If lockParent is true, it acquires a reference share lock on the parent row.
	HasRowByPK(tableName string, pk string, lockParent bool) (bool, error)

	// SeekUniqueIndex checks if a row matching the composite searchKey exists in a secondary unique index.
	// Returns true if found and acquires a reference share lock if lockParent is true.
	SeekUniqueIndex(tableName string, cols []string, vals []storage.Value, lockParent bool) (bool, error)

	// ScanRowsForFK scans rows in tableName and checks if any row satisfies predicate.
	// If predicate returns true, scanning stops. Returns whether a match was found.
	ScanRowsForFK(tableName string, matchFunc func(row storage.Row) bool, lockParent bool) (bool, error)

	// FindChildPKs scans childTable and returns the primary key Values of all child rows referencing parentValues.
	FindChildPKs(childTable string, fk storage.ForeignKeySchema, parentValues []storage.Value) ([]storage.Value, error)

	// CascadeDelete executes a delete on a child row.
	CascadeDelete(childTable string, pk storage.Value, depth int) error

	// CascadeUpdate executes an update on a child row with a patch record.
	CascadeUpdate(childTable string, pk storage.Value, patch storage.Record, depth int) error
}

// ForeignKeyEngine coordinates foreign key constraint validation and cascade propagation.
type ForeignKeyEngine struct {
	tx StorageTx
}

// New creates a new ForeignKeyEngine bound to the specified StorageTx.
func New(tx StorageTx) *ForeignKeyEngine {
	return &ForeignKeyEngine{tx: tx}
}

// ValidateChildInsertOrUpdate validates that all non-null foreign keys in the given child record
// reference existing rows in their respective parent tables.
func (fke *ForeignKeyEngine) ValidateChildInsertOrUpdate(childSchema *storage.TableSchema, record storage.Record) error {
	if childSchema == nil || len(childSchema.ForeignKeys) == 0 {
		return nil
	}

	for _, fk := range childSchema.ForeignKeys {
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

		exists, err := fke.ParentRecordExists(fk, record)
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

// ParentRecordExists checks if the parent row referenced by the child record exists.
func (fke *ForeignKeyEngine) ParentRecordExists(fk storage.ForeignKeySchema, childRecord storage.Record) (bool, error) {
	parentSchema, err := fke.tx.GetSchema(fk.ParentTable)
	if err != nil {
		return false, fmt.Errorf("%w: referenced table %q not found", storage.ErrTableNotFound, fk.ParentTable)
	}

	parentCols := fk.ParentColumns
	if len(parentCols) == 0 {
		pkCol, err := parentSchema.PrimaryKeyColumn()
		if err != nil || pkCol == nil {
			return false, fmt.Errorf("referenced table %q has no primary key", fk.ParentTable)
		}
		parentCols = []string{pkCol.Name}
	}

	childVals := make([]storage.Value, len(fk.ChildColumns))
	for i, cCol := range fk.ChildColumns {
		val, ok := childRecord.Get(cCol)
		if !ok || val.IsNull {
			return true, nil
		}
		childVals[i] = val
	}

	// 1. Fast-Path: Referencing Parent Primary Key
	parentPkCol, _ := parentSchema.PrimaryKeyColumn()
	if parentPkCol != nil && len(parentCols) == 1 && strings.EqualFold(parentCols[0], parentPkCol.Name) {
		parentKey := childVals[0].String()
		return fke.tx.HasRowByPK(fk.ParentTable, parentKey, true)
	}

	// 2. Fast-Path: Referencing Parent Unique Secondary Index
	if len(parentCols) > 0 {
		found, err := fke.tx.SeekUniqueIndex(fk.ParentTable, parentCols, childVals, true)
		if err == nil && found {
			return true, nil
		}
		if err != nil && !errors.Is(err, storage.ErrIndexNotFound) {
			return false, err
		}
	}

	// 3. Fallback: Full Table Scan
	return fke.tx.ScanRowsForFK(fk.ParentTable, func(row storage.Row) bool {
		return RowMatchesFK(parentSchema, row, parentCols, childVals)
	}, true)
}

// HandleParentDelete checks all child tables referencing the deleted parent row and executes
// the configured ON DELETE action (RESTRICT / CASCADE / SET NULL).
func (fke *ForeignKeyEngine) HandleParentDelete(
	tableName string,
	oldRecord storage.Record,
	childRefs []ChildReference,
	depth int,
) error {
	for _, cr := range childRefs {
		matchingPKs, err := fke.tx.FindChildPKs(cr.ChildTable, cr.FK, resolveParentVals(oldRecord, cr.FK.ParentColumns))
		if err != nil {
			return err
		}
		if len(matchingPKs) == 0 {
			continue
		}

		switch cr.FK.OnDelete {
		case storage.ActionCascade:
			for _, cpk := range matchingPKs {
				if err := fke.tx.CascadeDelete(cr.ChildTable, cpk, depth+1); err != nil {
					return err
				}
			}
		case storage.ActionSetNull:
			childSchema, _ := fke.tx.GetSchema(cr.ChildTable)
			for _, cpk := range matchingPKs {
				nullPatch := make(storage.Record, len(cr.FK.ChildColumns))
				for _, cCol := range cr.FK.ChildColumns {
					colType := storage.TypeVarChar
					if childSchema != nil {
						if col, _, found := childSchema.FindColumn(cCol); found {
							colType = col.Type
						}
					}
					nullPatch[cCol] = storage.NullValue(colType)
				}
				if err := fke.tx.CascadeUpdate(cr.ChildTable, cpk, nullPatch, depth+1); err != nil {
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
	return nil
}

// HandleParentUpdate checks all child tables referencing the updated parent row when parent key columns change,
// executing ON UPDATE action (CASCADE / SET NULL / RESTRICT).
func (fke *ForeignKeyEngine) HandleParentUpdate(
	tableName string,
	oldRecord storage.Record,
	updatedRecord storage.Record,
	childRefs []ChildReference,
	depth int,
) error {
	for _, cr := range childRefs {
		pCols := cr.FK.ParentColumns
		if len(pCols) == 0 {
			pSchema, err := fke.tx.GetSchema(tableName)
			if err == nil && pSchema != nil {
				if pkCol, err := pSchema.PrimaryKeyColumn(); err == nil && pkCol != nil {
					pCols = []string{pkCol.Name}
				}
			}
		}

		keyChanged := false
		for _, pCol := range pCols {
			oldVal, hasOld := oldRecord.Get(pCol)
			newVal, hasNew := updatedRecord.Get(pCol)
			if hasOld != hasNew || !ValuesEqual(oldVal, newVal) {
				keyChanged = true
				break
			}
		}
		if !keyChanged {
			continue
		}

		oldParentVals := resolveParentVals(oldRecord, pCols)
		matchingChildPKs, err := fke.tx.FindChildPKs(cr.ChildTable, cr.FK, oldParentVals)
		if err != nil {
			return err
		}
		if len(matchingChildPKs) == 0 {
			continue
		}

		switch cr.FK.OnUpdate {
		case storage.ActionCascade:
			for _, cpk := range matchingChildPKs {
				patch := make(storage.Record, len(cr.FK.ChildColumns))
				for i, cCol := range cr.FK.ChildColumns {
					pCol := pCols[i]
					if nVal, ok := updatedRecord.Get(pCol); ok {
						patch[cCol] = nVal
					}
				}
				if err := fke.tx.CascadeUpdate(cr.ChildTable, cpk, patch, depth+1); err != nil {
					return err
				}
			}
		case storage.ActionSetNull:
			childSchema, _ := fke.tx.GetSchema(cr.ChildTable)
			for _, cpk := range matchingChildPKs {
				patch := make(storage.Record, len(cr.FK.ChildColumns))
				for _, cCol := range cr.FK.ChildColumns {
					colType := storage.TypeVarChar
					if childSchema != nil {
						if col, _, found := childSchema.FindColumn(cCol); found {
							colType = col.Type
						}
					}
					patch[cCol] = storage.NullValue(colType)
				}
				if err := fke.tx.CascadeUpdate(cr.ChildTable, cpk, patch, depth+1); err != nil {
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
	return nil
}

// ChildReference holds the child table name and corresponding foreign key definition.
type ChildReference struct {
	ChildTable string
	FK         storage.ForeignKeySchema
}

func resolveParentVals(rec storage.Record, cols []string) []storage.Value {
	vals := make([]storage.Value, len(cols))
	for i, col := range cols {
		if val, ok := rec.Get(col); ok {
			vals[i] = val
		} else {
			vals[i] = storage.NullValue(storage.TypeUnknown)
		}
	}
	return vals
}

// RowMatchesFK checks whether row values for the given columns match the provided target values.
func RowMatchesFK(schema *storage.TableSchema, row storage.Row, cols []string, vals []storage.Value) bool {
	if schema == nil || len(cols) != len(vals) {
		return false
	}
	for i, col := range cols {
		_, idx, found := schema.FindColumn(col)
		if !found || idx < 0 || idx >= len(row.Values) {
			return false
		}
		v := row.Values[idx]
		if v.IsNull || vals[i].IsNull {
			return false
		}
		if !ValuesEqual(v, vals[i]) {
			return false
		}
	}
	return true
}

// RecordMatchesFK checks whether a storage.Record matches the provided target values.
func RecordMatchesFK(rec storage.Record, cols []string, vals []storage.Value) bool {
	if len(cols) != len(vals) {
		return false
	}
	for i, col := range cols {
		v, ok := rec.Get(col)
		if !ok || v.IsNull || vals[i].IsNull {
			return false
		}
		if !ValuesEqual(v, vals[i]) {
			return false
		}
	}
	return true
}

// ValuesEqual compares two storage.Value items with numeric type coercion.
func ValuesEqual(a, b storage.Value) bool {
	if a.Equal(b) {
		return true
	}
	if (a.Type == storage.TypeFloat || a.Type == storage.TypeReal) && b.Type == storage.TypeInt {
		return a.FVal == float64(b.IVal)
	}
	if a.Type == storage.TypeInt && (b.Type == storage.TypeFloat || b.Type == storage.TypeReal) {
		return float64(a.IVal) == b.FVal
	}
	if (a.Type == storage.TypeFloat && b.Type == storage.TypeReal) || (a.Type == storage.TypeReal && b.Type == storage.TypeFloat) {
		return a.FVal == b.FVal || math.Abs(a.FVal-b.FVal) < 1e-9
	}
	return false
}

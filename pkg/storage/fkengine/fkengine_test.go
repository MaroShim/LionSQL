package fkengine_test

import (
	"errors"
	"testing"

	"lionsql/pkg/storage"
	"lionsql/pkg/storage/fkengine"
)

type mockStorageTx struct {
	schemas      map[string]*storage.TableSchema
	rows         map[string]map[string]storage.Row
	records      map[string]map[string]storage.Record
	deletedRows  map[string]map[string]bool
	updatedRows  map[string]map[string]storage.Record
}

func newMockStorageTx() *mockStorageTx {
	return &mockStorageTx{
		schemas:     make(map[string]*storage.TableSchema),
		rows:        make(map[string]map[string]storage.Row),
		records:     make(map[string]map[string]storage.Record),
		deletedRows: make(map[string]map[string]bool),
		updatedRows: make(map[string]map[string]storage.Record),
	}
}

func (m *mockStorageTx) GetSchema(tableName string) (*storage.TableSchema, error) {
	s, ok := m.schemas[tableName]
	if !ok {
		return nil, storage.ErrTableNotFound
	}
	return s, nil
}

func (m *mockStorageTx) HasRowByPK(tableName string, pk string, lockParent bool) (bool, error) {
	if m.deletedRows[tableName] != nil && m.deletedRows[tableName][pk] {
		return false, nil
	}
	if m.records[tableName] != nil {
		_, exists := m.records[tableName][pk]
		return exists, nil
	}
	return false, nil
}

func (m *mockStorageTx) SeekUniqueIndex(tableName string, cols []string, vals []storage.Value, lockParent bool) (bool, error) {
	return false, errors.New("index not found")
}

func (m *mockStorageTx) ScanRowsForFK(tableName string, matchFunc func(row storage.Row) bool, lockParent bool) (bool, error) {
	if m.rows[tableName] == nil {
		return false, nil
	}
	for pk, r := range m.rows[tableName] {
		if m.deletedRows[tableName] != nil && m.deletedRows[tableName][pk] {
			continue
		}
		if matchFunc(r) {
			return true, nil
		}
	}
	return false, nil
}

func (m *mockStorageTx) FindChildPKs(childTable string, fk storage.ForeignKeySchema, parentValues []storage.Value) ([]storage.Value, error) {
	var res []storage.Value
	s := m.schemas[childTable]
	if m.records[childTable] != nil {
		for pkStr, rec := range m.records[childTable] {
			if fkengine.RecordMatchesFK(rec, fk.ChildColumns, parentValues) {
				pkVal, _ := s.ResolveRecordPK(rec)
				if pkVal.IsNull {
					pkVal = storage.StringValue(pkStr)
				}
				res = append(res, pkVal)
			}
		}
	}
	return res, nil
}

func (m *mockStorageTx) CascadeDelete(childTable string, pk storage.Value, depth int) error {
	if m.deletedRows[childTable] == nil {
		m.deletedRows[childTable] = make(map[string]bool)
	}
	m.deletedRows[childTable][pk.String()] = true
	return nil
}

func (m *mockStorageTx) CascadeUpdate(childTable string, pk storage.Value, patch storage.Record, depth int) error {
	if m.updatedRows[childTable] == nil {
		m.updatedRows[childTable] = make(map[string]storage.Record)
	}
	m.updatedRows[childTable][pk.String()] = patch
	return nil
}

func TestForeignKeyEngine_ValidationAndCascade(t *testing.T) {
	mockTx := newMockStorageTx()
	fke := fkengine.New(mockTx)

	parentSchema := &storage.TableSchema{
		Name: "depts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	childSchema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "dept_id", Type: storage.TypeInt},
		},
		ForeignKeys: []storage.ForeignKeySchema{
			{
				Name:          "fk_user_dept",
				ChildTable:    "users",
				ChildColumns:  []string{"dept_id"},
				ParentTable:   "depts",
				ParentColumns: []string{"id"},
				OnDelete:      storage.ActionCascade,
				OnUpdate:      storage.ActionCascade,
			},
		},
	}
	mockTx.schemas["depts"] = parentSchema
	mockTx.schemas["users"] = childSchema

	mockTx.records["depts"] = map[string]storage.Record{
		"10": {"id": storage.IntValue(10), "name": storage.StringValue("Engineering")},
	}
	mockTx.records["users"] = map[string]storage.Record{
		"1": {"id": storage.IntValue(1), "dept_id": storage.IntValue(10)},
	}

	// 1. Valid child record
	validRec := storage.Record{"id": storage.IntValue(2), "dept_id": storage.IntValue(10)}
	if err := fke.ValidateChildInsertOrUpdate(childSchema, validRec); err != nil {
		t.Fatalf("expected valid child insert to succeed, got: %v", err)
	}

	// 2. Invalid child record with non-existent dept_id 999
	invalidRec := storage.Record{"id": storage.IntValue(3), "dept_id": storage.IntValue(999)}
	err := fke.ValidateChildInsertOrUpdate(childSchema, invalidRec)
	if err == nil || !errors.Is(err, storage.ErrForeignKeyViolation) {
		t.Fatalf("expected ErrForeignKeyViolation, got: %v", err)
	}

	// 3. Null foreign key is permitted
	nullRec := storage.Record{"id": storage.IntValue(4), "dept_id": storage.NullValue(storage.TypeInt)}
	if err := fke.ValidateChildInsertOrUpdate(childSchema, nullRec); err != nil {
		t.Fatalf("expected null fk to pass, got: %v", err)
	}

	// 4. Cascade Delete
	oldParentRec := storage.Record{"id": storage.IntValue(10), "name": storage.StringValue("Engineering")}
	childRefs := []fkengine.ChildReference{
		{ChildTable: "users", FK: childSchema.ForeignKeys[0]},
	}
	if err := fke.HandleParentDelete("depts", oldParentRec, childRefs, 0); err != nil {
		t.Fatalf("cascade delete failed: %v", err)
	}
	if !mockTx.deletedRows["users"]["1"] {
		t.Fatalf("expected child row 1 to be cascade deleted")
	}

	// 5. Cascade Update
	updatedParentRec := storage.Record{"id": storage.IntValue(20), "name": storage.StringValue("Engineering")}
	if err := fke.HandleParentUpdate("depts", oldParentRec, updatedParentRec, childRefs, 0); err != nil {
		t.Fatalf("cascade update failed: %v", err)
	}
	if patch := mockTx.updatedRows["users"]["1"]; patch == nil || patch["dept_id"].IVal != 20 {
		t.Fatalf("expected child row 1 dept_id to be updated to 20, got: %v", patch)
	}
}

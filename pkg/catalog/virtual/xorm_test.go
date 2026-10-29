package virtual_test

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/catalog/virtual"
	"lionsql/pkg/storage"
)

type mockVirtualSource struct {
	tables []*storage.TableSchema
}

func (m *mockVirtualSource) ListTables() ([]*storage.TableSchema, error) {
	return m.tables, nil
}

func (m *mockVirtualSource) GetTable(name string) (*storage.TableSchema, error) {
	for _, t := range m.tables {
		if t.Name == name {
			return t, nil
		}
	}
	return nil, nil
}

func (m *mockVirtualSource) ListProcedures() []*catalog.Procedure {
	return nil
}

func (m *mockVirtualSource) ListFunctions() []*catalog.Function {
	return nil
}

func (m *mockVirtualSource) ListViews() []*catalog.View {
	return nil
}

func (m *mockVirtualSource) GetView(name string) (*catalog.View, error) {
	return nil, nil
}

func (m *mockVirtualSource) GetSortedTables() []*storage.TableSchema {
	return m.tables
}

func (m *mockVirtualSource) GetSortedViews() []*catalog.View {
	return nil
}

func (m *mockVirtualSource) GetSortedProcedures() []*catalog.Procedure {
	return nil
}

func TestXORMMSSQL_ColumnIntrospection(t *testing.T) {
	userTbl := &storage.TableSchema{
		Name: "user",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true, Nullable: false},
			{Name: "name", Type: storage.TypeVarChar, Precision: 50, Nullable: false},
			{Name: "bio", Type: storage.TypeVarChar, Precision: 255, Nullable: true},
		},
	}
	src := &mockVirtualSource{
		tables: []*storage.TableSchema{userTbl},
	}

	q := `select a.name as name, b.name as ctype, a.max_length, a.precision, a.scale, 
a.is_nullable as nullable, replace(replace(isnull(c.text,''),'(',''),')','') as vdefault, 
case when ISNULL(ic.column_id, 0) > 0 then 1 else 0 end 
from sys.columns a 
left join sys.types b on a.user_type_id = b.user_type_id 
left join sys.syscomments c on a.default_object_id = c.id 
left outer join sys.indexes i ON i.object_id = a.object_id AND i.is_primary_key = 1 
left outer join sys.index_columns ic on ic.object_id = a.object_id and ic.column_id = a.column_id and i.index_id = ic.index_id 
where a.object_id = object_id('user')`

	if !virtual.IsVirtualCatalogJoinQuery(q) {
		t.Fatalf("expected IsVirtualCatalogJoinQuery to match XORM query")
	}

	res, err := virtual.Execute(src, q)
	if err != nil {
		t.Fatalf("virtual.Execute failed: %v", err)
	}

	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 column rows for table 'user', got %d", len(res.Rows))
	}

	for i, r := range res.Rows {
		maxLen, ok := r["max_length"]
		if !ok || maxLen == nil {
			t.Fatalf("row %d: max_length must not be nil, got %+v", i, r)
		}
		if _, isInt64 := maxLen.(int64); !isInt64 {
			t.Fatalf("row %d: max_length must be int64, got %T (%v)", i, maxLen, maxLen)
		}

		ctype, ok := r["ctype"]
		if !ok || ctype == nil || ctype == "" {
			t.Fatalf("row %d: ctype must be non-empty string, got %v", i, ctype)
		}

		nullable, ok := r["nullable"]
		if !ok || nullable == nil {
			t.Fatalf("row %d: nullable must not be nil, got %v", i, nullable)
		}
	}

	// Verify PK flag for 'id'
	idRow := res.Rows[0]
	if idRow["name"] != "id" {
		t.Errorf("expected row 0 to be 'id', got %v", idRow["name"])
	}
	isPKCol := res.Columns[len(res.Columns)-1]
	if idRow[isPKCol] != int64(1) {
		t.Errorf("expected id column to have is_pk = 1, got %v", idRow[isPKCol])
	}

	// Verify non-PK flag for 'name'
	nameRow := res.Rows[1]
	if nameRow[isPKCol] != int64(0) {
		t.Errorf("expected name column to have is_pk = 0, got %v", nameRow[isPKCol])
	}
}

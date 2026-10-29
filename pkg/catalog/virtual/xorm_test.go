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

func TestXORMMSSQL_GetColumnsExact_AndCollation(t *testing.T) {
	userTbl := &storage.TableSchema{
		Name: "user",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true, Nullable: false},
			{Name: "name", Type: storage.TypeVarChar, Precision: 50, Nullable: false},
		},
	}
	src := &mockVirtualSource{
		tables: []*storage.TableSchema{userTbl},
	}

	// 1. Exact query from xorm-io/xorm dialects/mssql.go GetColumns()
	qCols := `select a.name as name, b.name as ctype,a.max_length,a.precision,a.scale,a.is_nullable as nullable,
		  "default_is_null" = (CASE WHEN c.text is null THEN 1 ELSE 0 END),
	      replace(replace(isnull(c.text,''),'(',''),')','') as vdefault,
		  ISNULL(p.is_primary_key, 0), a.is_identity as is_identity
          from sys.columns a 
		  left join sys.types b on a.user_type_id=b.user_type_id
          left join sys.syscomments c on a.default_object_id=c.id
		  LEFT OUTER JOIN (SELECT i.object_id, ic.column_id, i.is_primary_key
			FROM sys.indexes i
		  LEFT JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
			WHERE i.is_primary_key = 1
		) as p on p.object_id = a.object_id AND p.column_id = a.column_id
          where a.object_id=object_id('user')`

	if !virtual.IsVirtualCatalogJoinQuery(qCols) {
		t.Fatalf("expected IsVirtualCatalogJoinQuery to match exact GetColumns query")
	}

	resCols, err := virtual.Execute(src, qCols)
	if err != nil {
		t.Fatalf("virtual.Execute failed: %v", err)
	}

	if len(resCols.Columns) != 10 {
		t.Fatalf("expected 10 columns, got %d: %v", len(resCols.Columns), resCols.Columns)
	}
	if resCols.Columns[6] != "default_is_null" {
		t.Fatalf("expected column 6 to be 'default_is_null', got %q", resCols.Columns[6])
	}
	if len(resCols.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(resCols.Rows))
	}

	// Verify simulated rows.Scan(&name, &ctype, &maxLen, &precision, &scale, &nullable, &defaultIsNull, &vdefault, &isPK, &isIncrement)
	for i, r := range resCols.Rows {
		defVal, ok := r["default_is_null"]
		if !ok || defVal == nil {
			t.Fatalf("row %d: default_is_null must not be nil, got %+v", i, r)
		}
		if bVal, ok := defVal.(bool); !ok || !bVal {
			t.Fatalf("row %d: default_is_null must be true (bool), got %T (%v)", i, defVal, defVal)
		}
	}

	// Verify PK indicator on column 8
	pkCol := resCols.Columns[8]
	if resCols.Rows[0][pkCol] != int64(1) {
		t.Errorf("expected id row pkCol to be 1, got %v", resCols.Rows[0][pkCol])
	}
	if resCols.Rows[1][pkCol] != int64(0) {
		t.Errorf("expected name row pkCol to be 0, got %v", resCols.Rows[1][pkCol])
	}

	// 2. Collation discovery query: SELECT * FROM sys.fn_helpcollations() WHERE name LIKE '%[_]CS[_]AS%'
	qColl := `SELECT * FROM sys.fn_helpcollations() WHERE name LIKE '%[_]CS[_]AS%'`
	if !virtual.IsVirtualCatalogJoinQuery(qColl) {
		t.Fatalf("expected IsVirtualCatalogJoinQuery to match fn_helpcollations query")
	}

	resColl, err := virtual.Execute(src, qColl)
	if err != nil {
		t.Fatalf("fn_helpcollations failed: %v", err)
	}
	if len(resColl.Rows) == 0 {
		t.Fatalf("expected fn_helpcollations to return matching collations, got 0 rows")
	}
	foundExpected := false
	for _, r := range resColl.Rows {
		collName, _ := r["name"].(string)
		if collName == "Latin1_General_CS_AS" {
			foundExpected = true
			break
		}
	}
	if !foundExpected {
		t.Errorf("expected Latin1_General_CS_AS in fn_helpcollations results, got: %+v", resColl.Rows)
	}
}

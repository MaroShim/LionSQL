package virtual_test

import (
	"testing"

	"sqlion/pkg/catalog"
	"sqlion/pkg/catalog/virtual"
	"sqlion/pkg/storage"
)

type mockSource struct {
	tables     []*storage.TableSchema
	procedures []*catalog.Procedure
	views      []*catalog.View
	functions  []*catalog.Function
}

func (m *mockSource) ListTables() ([]*storage.TableSchema, error) {
	return m.tables, nil
}

func (m *mockSource) GetTable(name string) (*storage.TableSchema, error) {
	for _, t := range m.tables {
		if t.Name == name {
			return t, nil
		}
	}
	return nil, storage.ErrTableNotFound
}

func (m *mockSource) ListProcedures() []*catalog.Procedure {
	return m.procedures
}

func (m *mockSource) ListFunctions() []*catalog.Function {
	return m.functions
}

func (m *mockSource) ListViews() []*catalog.View {
	return m.views
}

func (m *mockSource) GetView(name string) (*catalog.View, error) {
	for _, v := range m.views {
		if v.Name == name {
			return v, nil
		}
	}
	return nil, catalog.ErrViewNotFound
}

func (m *mockSource) GetSortedTables() []*storage.TableSchema {
	return m.tables
}

func (m *mockSource) GetSortedViews() []*catalog.View {
	return m.views
}

func (m *mockSource) GetSortedProcedures() []*catalog.Procedure {
	return m.procedures
}

func (m *mockSource) GetTableRowCount(name string) int64 {
	return 42
}

func newTestMockSource() *mockSource {
	return &mockSource{
		tables: []*storage.TableSchema{
			{
				Name: "users",
				Columns: []storage.Column{
					{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
					{Name: "username", Type: storage.TypeVarChar, Precision: 50, Nullable: false},
				},
			},
			{
				Name: "orders",
				Columns: []storage.Column{
					{Name: "order_id", Type: storage.TypeInt, PrimaryKey: true},
					{Name: "user_id", Type: storage.TypeInt, Nullable: false},
				},
				ForeignKeys: []storage.ForeignKeySchema{
					{
						Name:          "fk_orders_user",
						ChildTable:    "orders",
						ChildColumns:  []string{"user_id"},
						ParentTable:   "users",
						ParentColumns: []string{"id"},
					},
				},
			},
		},
		procedures: []*catalog.Procedure{
			{
				Name: "sp_calculate_total",
				Body: "CREATE PROCEDURE sp_calculate_total AS BEGIN SELECT 1; END",
			},
		},
		views: []*catalog.View{
			{
				Name:  "v_active_users",
				Query: "SELECT id, username FROM users",
			},
		},
	}
}

func TestIsVirtualCatalogJoinQuery(t *testing.T) {
	tests := []struct {
		sql      string
		expected bool
	}{
		{"SELECT * FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace", true},
		{"SELECT * FROM sys.columns c JOIN sys.types t ON c.user_type_id = t.user_type_id", true},
		{"SELECT * FROM sys.indexes i WHERE is_primary_key = 1", true},
		{"SELECT * FROM sys.index_columns", true},
		{"SELECT * FROM information_schema.referential_constraints r JOIN sys.schemas s ON 1=1", true},
		{"SELECT * FROM sys.parameters p JOIN sys.objects o ON p.object_id = o.object_id", true},
		{"SELECT * FROM pg_constraint con JOIN pg_class rel ON rel.oid = con.conrelid", true},
		{"SELECT * FROM pg_index i JOIN pg_namespace n ON 1=1", true},
		{"SELECT * FROM pg_trigger t JOIN pg_class c ON 1=1", true},
		{"SELECT * FROM pg_attribute a JOIN pg_class c ON a.attrelid = c.oid", true},
		{"SELECT * FROM pg_proc p JOIN pg_namespace n ON p.pronamespace = n.oid", true},
		{"SELECT * FROM pg_shdescription", true},
		{"SELECT * FROM pg_conversion", true},
		{"SELECT * FROM sys.all_objects", true},
		{"SELECT * FROM users WHERE id = 1", false},
		{"INSERT INTO users VALUES (1, 'alice')", false},
	}

	for _, tc := range tests {
		got := virtual.IsVirtualCatalogJoinQuery(tc.sql)
		if got != tc.expected {
			t.Errorf("IsVirtualCatalogJoinQuery(%q) = %v; want %v", tc.sql, got, tc.expected)
		}
	}
}

func TestExecute_PostgreSQLVirtualCatalog(t *testing.T) {
	src := newTestMockSource()

	// 1. Table list (pg_class JOIN pg_namespace)
	qClass := "SELECT c.relname, n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relkind = 'r'"
	res, err := virtual.Execute(src, qClass)
	if err != nil {
		t.Fatalf("Execute(pg_class) failed: %v", err)
	}
	if len(res.Rows) < 2 {
		t.Fatalf("expected at least 2 rows for pg_class, got %d", len(res.Rows))
	}

	// 2. Foreign keys (pg_constraint)
	qFK := "SELECT con.conname, src_tbl.relname FROM pg_constraint con JOIN pg_class src_tbl ON src_tbl.oid = con.conrelid WHERE con.contype = 'f'"
	resFK, err := virtual.Execute(src, qFK)
	if err != nil {
		t.Fatalf("Execute(pg_constraint) failed: %v", err)
	}
	if len(resFK.Rows) != 1 {
		t.Fatalf("expected 1 FK row, got %d", len(resFK.Rows))
	}
	if resFK.Rows[0]["constraint_name"] != "fk_orders_user" {
		t.Errorf("expected constraint_name fk_orders_user, got %v", resFK.Rows[0]["constraint_name"])
	}

	// 3. Procedures (pg_proc JOIN pg_namespace)
	qProc := "SELECT p.proname FROM pg_proc p JOIN pg_namespace n ON p.pronamespace = n.oid"
	resProc, err := virtual.Execute(src, qProc)
	if err != nil {
		t.Fatalf("Execute(pg_proc) failed: %v", err)
	}
	if len(resProc.Rows) != 1 {
		t.Fatalf("expected 1 procedure row, got %d", len(resProc.Rows))
	}
	if resProc.Rows[0]["proname"] != "sp_calculate_total" {
		t.Errorf("expected sp_calculate_total, got %v", resProc.Rows[0]["proname"])
	}

	// 4. Columns (pg_attribute JOIN pg_class)
	qAttr := "SELECT a.attname FROM pg_attribute a JOIN pg_class c ON a.attrelid = c.oid WHERE c.relname = 'users'"
	resAttr, err := virtual.Execute(src, qAttr)
	if err != nil {
		t.Fatalf("Execute(pg_attribute) failed: %v", err)
	}
	if len(resAttr.Rows) != 2 {
		t.Fatalf("expected 2 columns for users, got %d", len(resAttr.Rows))
	}
}

func TestExecute_MSSQLVirtualCatalog(t *testing.T) {
	src := newTestMockSource()

	// 1. MSSQL Columns (sys.columns JOIN sys.types)
	qCol := "SELECT c.name as columnName, t.name as dataType FROM sys.columns c JOIN sys.types t ON c.user_type_id = t.user_type_id"
	resCol, err := virtual.Execute(src, qCol)
	if err != nil {
		t.Fatalf("Execute(sys.columns) failed: %v", err)
	}
	if len(resCol.Rows) < 4 {
		t.Fatalf("expected at least 4 column rows, got %d", len(resCol.Rows))
	}

	// 2. MSSQL Primary Keys (sys.indexes WHERE is_primary_key = 1)
	qPK := "SELECT * FROM sys.indexes WHERE is_primary_key = 1"
	resPK, err := virtual.Execute(src, qPK)
	if err != nil {
		t.Fatalf("Execute(sys.indexes PK) failed: %v", err)
	}
	if len(resPK.Rows) != 2 {
		t.Fatalf("expected 2 primary keys, got %d", len(resPK.Rows))
	}

	// 3. MSSQL Row Count (sys.tables JOIN sys.partitions)
	qRC := "SELECT tableRowCount FROM sys.tables JOIN sys.partitions ON 1=1"
	resRC, err := virtual.Execute(src, qRC)
	if err != nil {
		t.Fatalf("Execute(sys.tables row count) failed: %v", err)
	}
	if len(resRC.Rows) != 2 {
		t.Fatalf("expected 2 table rows, got %d", len(resRC.Rows))
	}
	if resRC.Rows[0]["tableRowCount"] != int64(42) {
		t.Errorf("expected row count 42, got %v", resRC.Rows[0]["tableRowCount"])
	}
}

func TestIsVirtualCatalogJoinQuery_WhitespaceResilience(t *testing.T) {
	// Queries that should match regardless of whitespace variations around '='
	queries := []string{
		"SELECT * FROM sys.objects WHERE o.type = 'TR'",
		"SELECT * FROM sys.objects WHERE o.type= 'TR'",
		"SELECT * FROM sys.objects WHERE o.type ='TR'",
		"SELECT * FROM sys.objects WHERE o.type='TR'",
		"SELECT * FROM sys.objects WHERE o.type   =   'TR'",
		"SELECT * FROM sys.objects WHERE o.type\t=\t'TR'",
		"SELECT * FROM sys.indexes WHERE is_primary_key=1",
		"SELECT * FROM sys.indexes WHERE is_primary_key = 1",
		"SELECT * FROM sys.indexes WHERE is_primary_key= 1",
		"SELECT * FROM sys.indexes WHERE is_primary_key =1",
	}

	for _, q := range queries {
		if !virtual.IsVirtualCatalogJoinQuery(q) {
			t.Errorf("expected IsVirtualCatalogJoinQuery to return true for %q", q)
		}
	}
}

func BenchmarkIsVirtualCatalogJoinQuery_NormalOLTP(b *testing.B) {
	sql := "SELECT id, username, balance FROM users WHERE id = 12345"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = virtual.IsVirtualCatalogJoinQuery(sql)
	}
}

func BenchmarkIsVirtualCatalogJoinQuery_CatalogQuery(b *testing.B) {
	sql := "SELECT * FROM sys.columns c JOIN sys.types t ON c.user_type_id = t.user_type_id"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = virtual.IsVirtualCatalogJoinQuery(sql)
	}
}


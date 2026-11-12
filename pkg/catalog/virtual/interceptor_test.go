package virtual_test

import (
	"sort"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/catalog/virtual"
	"lionsql/pkg/storage"
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
	sorted := make([]*storage.TableSchema, len(m.tables))
	copy(sorted, m.tables)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	return sorted
}

func (m *mockSource) GetSortedViews() []*catalog.View {
	sorted := make([]*catalog.View, len(m.views))
	copy(sorted, m.views)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	return sorted
}

func (m *mockSource) GetSortedProcedures() []*catalog.Procedure {
	sorted := make([]*catalog.Procedure, len(m.procedures))
	copy(sorted, m.procedures)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	return sorted
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
				Params: []catalog.ParamDef{
					{Name: "@UserID", Type: storage.TypeInt},
					{Name: "@Total", Type: storage.TypeDecimal},
				},
				Body: "CREATE PROCEDURE sp_calculate_total @UserID INT, @Total DECIMAL(10,2) AS BEGIN SELECT 1; END",
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

func TestVirtualCatalog_LikeAndNotLikeFiltering(t *testing.T) {
	cols := []string{"table_name"}
	rows := []map[string]any{
		{"table_name": "app_users"},
		{"table_name": "app_orders"},
		{"table_name": "app_orders_tmp"},
		{"table_name": "col_literal"},
		{"table_name": "sys_config"},
	}

	// 1. LIKE filtering
	filtered1, err := virtual.FormatResultWithSQL(cols, rows, "SELECT table_name FROM t WHERE table_name LIKE 'app_%'")
	if err != nil {
		t.Fatalf("FormatResultWithSQL failed: %v", err)
	}
	if len(filtered1.Rows) != 3 {
		t.Fatalf("expected 3 rows for app_%%, got %d", len(filtered1.Rows))
	}

	// 2. NOT LIKE filtering
	filtered2, err := virtual.FormatResultWithSQL(cols, rows, "SELECT table_name FROM t WHERE table_name LIKE 'app_%' AND table_name NOT LIKE '%_tmp'")
	if err != nil {
		t.Fatalf("FormatResultWithSQL failed: %v", err)
	}
	if len(filtered2.Rows) != 2 {
		t.Fatalf("expected 2 rows for NOT LIKE, got %d", len(filtered2.Rows))
	}

	// 3. Literal bracket escape: col[_]literal
	filtered3, err := virtual.FormatResultWithSQL(cols, rows, "SELECT table_name FROM t WHERE table_name LIKE 'col[_]literal'")
	if err != nil {
		t.Fatalf("FormatResultWithSQL failed: %v", err)
	}
	if len(filtered3.Rows) != 1 || filtered3.Rows[0]["table_name"] != "col_literal" {
		t.Fatalf("expected 1 row for col[_]literal, got %v", filtered3.Rows)
	}
}

func TestSSMS_ObjectExplorer_SubnodeQueries(t *testing.T) {
	src := newTestMockSource()

	// 1. Columns for table 'users' with SSMS SMO aliases and Urn
	qCols := `SELECT
		clmns.name AS [Name],
		clmns.column_id AS [ID],
		clmns.is_nullable AS [Nullable],
		clmns.is_identity AS [Identity],
		clmns.max_length AS [Length],
		clmns.precision AS [Precision],
		clmns.scale AS [Scale],
		usrt.name AS [DataType],
		'Server[@Name=MSSQLSERVER]/Database[@Name=master]/Table[@Name=users and @Schema=dbo]/Column[@Name=' + clmns.name + ']' AS [Urn]
		FROM sys.tables AS tbl
		INNER JOIN sys.all_columns AS clmns ON clmns.object_id = tbl.object_id
		LEFT OUTER JOIN sys.types AS usrt ON usrt.user_type_id = clmns.user_type_id
		WHERE (tbl.name = 'users' and SCHEMA_NAME(tbl.schema_id) = 'dbo')
		ORDER BY [ID] ASC`

	if !virtual.IsVirtualCatalogJoinQuery(qCols) {
		t.Fatalf("expected IsVirtualCatalogJoinQuery to match SSMS Columns query")
	}
	resCols, err := virtual.Execute(src, qCols)
	if err != nil {
		t.Fatalf("Execute(SSMS Columns) failed: %v", err)
	}
	if len(resCols.Rows) != 2 {
		t.Fatalf("expected 2 columns for users, got %d", len(resCols.Rows))
	}
	if resCols.Rows[0]["Name"] != "id" || resCols.Rows[1]["Name"] != "username" {
		t.Errorf("unexpected column names: %+v", resCols.Rows)
	}
	if urn, ok := resCols.Rows[0]["Urn"].(string); !ok || urn == "" {
		t.Errorf("expected non-empty Urn for column id, got %v", resCols.Rows[0]["Urn"])
	}

	// 2. Indexes for table 'users'
	qIdx := `SELECT
		i.name AS [Name],
		i.index_id AS [ID],
		i.type AS [IndexType],
		i.type_desc AS [TypeDesc],
		i.is_primary_key AS [IsPrimaryKey],
		i.is_unique AS [IsUnique],
		'Urn' AS [Urn]
		FROM sys.tables AS tbl
		INNER JOIN sys.indexes AS i ON (i.index_id > 0 and i.is_hypothetical = 0) AND (i.object_id=tbl.object_id)
		WHERE (tbl.name = 'users' and SCHEMA_NAME(tbl.schema_id) = 'dbo')
		ORDER BY [ID] ASC`

	if !virtual.IsVirtualCatalogJoinQuery(qIdx) {
		t.Fatalf("expected IsVirtualCatalogJoinQuery to match SSMS Indexes query")
	}
	resIdx, err := virtual.Execute(src, qIdx)
	if err != nil {
		t.Fatalf("Execute(SSMS Indexes) failed: %v", err)
	}
	if len(resIdx.Rows) != 1 {
		t.Fatalf("expected 1 index (PK) for users, got %d", len(resIdx.Rows))
	}
	if resIdx.Rows[0]["Name"] != "PK_users" {
		t.Errorf("expected PK_users, got %v", resIdx.Rows[0]["Name"])
	}

	// 3. Index Columns for table 'users'
	qIndexCols := `SELECT
		ic.index_id AS [IndexID],
		ic.column_id AS [ColumnID],
		ic.key_ordinal AS [KeyOrdinal],
		clmns.name AS [Name],
		ic.is_descending_key AS [Descending]
		FROM sys.tables AS tbl
		INNER JOIN sys.indexes AS i ON i.object_id = tbl.object_id
		INNER JOIN sys.index_columns AS ic ON ic.object_id = tbl.object_id AND ic.index_id = i.index_id
		INNER JOIN sys.all_columns AS clmns ON clmns.object_id = tbl.object_id AND clmns.column_id = ic.column_id
		WHERE (tbl.name = 'users' and SCHEMA_NAME(tbl.schema_id) = 'dbo')`

	if !virtual.IsVirtualCatalogJoinQuery(qIndexCols) {
		t.Fatalf("expected IsVirtualCatalogJoinQuery to match SSMS Index Columns query")
	}
	resIndexCols, err := virtual.Execute(src, qIndexCols)
	if err != nil {
		t.Fatalf("Execute(SSMS Index Columns) failed: %v", err)
	}
	if len(resIndexCols.Rows) != 1 {
		t.Fatalf("expected 1 index column for PK_users, got %d", len(resIndexCols.Rows))
	}
	if resIndexCols.Rows[0]["Name"] != "id" {
		t.Errorf("expected index column name 'id', got %v", resIndexCols.Rows[0]["Name"])
	}

	// 4. Keys (sys.key_constraints) for table 'users'
	qKeys := `SELECT
		k.name AS [Name],
		k.type AS [KeyType],
		k.type_desc AS [TypeDesc],
		k.is_system_named AS [IsSystemNamed]
		FROM sys.tables AS tbl
		INNER JOIN sys.key_constraints AS k ON k.parent_object_id = tbl.object_id
		WHERE (tbl.name = 'users' and SCHEMA_NAME(tbl.schema_id) = 'dbo')`

	if !virtual.IsVirtualCatalogJoinQuery(qKeys) {
		t.Fatalf("expected IsVirtualCatalogJoinQuery to match SSMS Keys query")
	}
	resKeys, err := virtual.Execute(src, qKeys)
	if err != nil {
		t.Fatalf("Execute(SSMS Keys) failed: %v", err)
	}
	if len(resKeys.Rows) != 1 {
		t.Fatalf("expected 1 key constraint for users, got %d", len(resKeys.Rows))
	}
	if resKeys.Rows[0]["Name"] != "PK_users" || resKeys.Rows[0]["KeyType"] != "PK" {
		t.Errorf("unexpected key constraint: %+v", resKeys.Rows[0])
	}

	// 5. Foreign Keys for table 'orders'
	qFKs := `SELECT
		fk.name AS [Name],
		fk.object_id AS [ID]
		FROM sys.tables AS tbl
		INNER JOIN sys.foreign_keys AS fk ON fk.parent_object_id = tbl.object_id
		WHERE (tbl.name = 'orders' and SCHEMA_NAME(tbl.schema_id) = 'dbo')`

	if !virtual.IsVirtualCatalogJoinQuery(qFKs) {
		t.Fatalf("expected IsVirtualCatalogJoinQuery to match SSMS Foreign Keys query")
	}
	resFKs, err := virtual.Execute(src, qFKs)
	if err != nil {
		t.Fatalf("Execute(SSMS FKs) failed: %v", err)
	}
	if len(resFKs.Rows) != 1 {
		t.Fatalf("expected 1 foreign key for orders, got %d", len(resFKs.Rows))
	}
	if resFKs.Rows[0]["Name"] != "fk_orders_user" {
		t.Errorf("expected fk_orders_user, got %v", resFKs.Rows[0]["Name"])
	}

	// 6. Statistics for table 'users'
	qStats := `SELECT
		s.name AS [Name],
		s.stats_id AS [ID]
		FROM sys.tables AS tbl
		INNER JOIN sys.stats AS s ON s.object_id = tbl.object_id
		WHERE (tbl.name = 'users' and SCHEMA_NAME(tbl.schema_id) = 'dbo')`

	if !virtual.IsVirtualCatalogJoinQuery(qStats) {
		t.Fatalf("expected IsVirtualCatalogJoinQuery to match SSMS Statistics query")
	}
	resStats, err := virtual.Execute(src, qStats)
	if err != nil {
		t.Fatalf("Execute(SSMS Statistics) failed: %v", err)
	}
	if len(resStats.Rows) != 1 {
		t.Fatalf("expected 1 statistic for users, got %d", len(resStats.Rows))
	}
	if resStats.Rows[0]["Name"] != "PK_users" {
		t.Errorf("expected PK_users stats, got %v", resStats.Rows[0]["Name"])
	}
}

func TestSSMS_TablesListQuery_FromLog(t *testing.T) {
	src := newTestMockSource()

	q := `SELECT
tbl.name AS [Name],
SCHEMA_NAME(tbl.schema_id) AS [Schema],
'Server[@Name=' + quotename(CAST(serverproperty(N'Servername') AS sysname),'''') + ']' + '/Database[@Name=' + quotename(db_name(),'''') + ']' + '/Table[@Name=' + quotename(tbl.name,'''') + ' and @Schema=' + quotename(SCHEMA_NAME(tbl.schema_id),'''') + ']' AS [Urn],
tbl.create_date AS [CreateDate],
CAST(tbl.is_memory_optimized AS bit) AS [IsMemoryOptimized],
CAST(CASE idx.type WHEN 5 THEN 1 ELSE 0 END AS bit) AS [HasClusteredColumnStoreIndex],
CAST(0 AS bit) AS [RemoteDataArchiveEnabled],
tbl.temporal_type AS [TemporalType],
CAST(CASE WHEN 'PS'=dsidx.type THEN 1 ELSE 0 END AS bit) AS [IsPartitioned],
CAST(
        ISNULL((SELECT distinct 1 from sys.all_columns
        WHERE object_id = tbl.object_id
        AND is_sparse = 1), 0)
       AS bit) AS [HasSparseColumn],
CAST(tbl.is_node AS bit) AS [IsNode],
CAST(tbl.is_edge AS bit) AS [IsEdge],
CAST(ISNULL((select top 1 1 from sys.indexes ind where ind.object_id = tbl.object_id and ind.type > 1 and ind.is_hypothetical = 0 ), 0) AS bit) AS [HasNonClusteredIndex],
CAST(CASE idx.index_id WHEN 1 THEN 1 ELSE 0 END AS bit) AS [HasClusteredIndex],
CAST(case idx.index_id when 1 then case when (idx.is_primary_key + 2*idx.is_unique_constraint = 1) then 1 else 0 end else 0 end AS bit) AS [HasPrimaryClusteredIndex],
CAST(ISNULL((select top 1 1 from sys.indexes ind where ind.object_id = tbl.object_id and ind.type = 3 and ind.is_hypothetical = 0 ), 0) AS bit) AS [HasXmlIndex],
CAST(ISNULL((select top 1 1 from sys.all_columns as clmns join sys.types as usrt on usrt.user_type_id = clmns.user_type_id where clmns.object_id = tbl.object_id and usrt.name = N'xml'), 0) AS bit) AS [HasXmlData],
CAST(ISNULL((select top 1 1 from sys.all_columns as clmns join sys.types as usrt on usrt.user_type_id = clmns.user_type_id where clmns.object_id = tbl.object_id and usrt.name in (N'geometry', N'geography')), 0) AS bit) AS [HasSpatialData],
CAST(ISNULL((select top 1 1 from sys.indexes ind where ind.object_id = tbl.object_id and ind.type = 6 and ind.is_hypothetical = 0 ), 0) AS bit) AS [HasNonClusteredColumnStoreIndex]
FROM
sys.tables AS tbl
INNER JOIN sys.indexes AS idx ON
        idx.object_id = tbl.object_id and (idx.index_id < '2'  or (tbl.is_memory_optimized = 1 and idx.index_id = (select min(index_id) from sys.indexes where object_id = tbl.object_id)))

LEFT OUTER JOIN sys.data_spaces AS dsidx ON dsidx.data_space_id = idx.data_space_id
WHERE
(CAST(
 case
    when tbl.is_ms_shipped = 1 then 1
    when exists (
        select
            1
        from
            sys.extended_properties
        where
            major_id = tbl.object_id and
            minor_id = 0 and
            class = 1 and
            name = N'microsoft_database_tools_support')
        then 1
    else 0
end
             AS bit)='0' and tbl.is_filetable='0' and CAST(tbl.is_memory_optimized AS bit)='0' and tbl.temporal_type='0' and CAST(tbl.is_external AS bit)='0' and CAST(tbl.is_node AS bit)='0' and CAST(tbl.is_edge AS bit)='0')`

	if !virtual.IsVirtualCatalogJoinQuery(q) {
		t.Fatalf("expected IsVirtualCatalogJoinQuery to match SSMS Tables List query")
	}
	res, err := virtual.Execute(src, q)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	t.Logf("Returned rows: %d, rows: %+v", len(res.Rows), res.Rows)
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 tables (orders, users), got %d", len(res.Rows))
	}
}

func TestSSMS_StoredProcedures_ObjectExplorerQuery(t *testing.T) {
	src := newTestMockSource()

	// 1. Variant A: Standard SSMS Stored Procedures Query with sys.all_objects LEFT OUTER JOIN sys.procedures
	qA := `SELECT
sp.name AS [Name],
SCHEMA_NAME(sp.schema_id) AS [Schema],
sp.object_id AS [ID],
'Server[@Name=' + quotename(CAST(serverproperty(N'Servername') AS sysname),'''') + ']' + '/Database[@Name=' + quotename(db_name(),'''') + ']' + '/StoredProcedure[@Name=' + quotename(sp.name,'''') + ' and @Schema=' + quotename(SCHEMA_NAME(sp.schema_id),'''') + ']' AS [Urn],
sp.create_date AS [CreateDate],
sp.modify_date AS [DateLastModified],
ISNULL(ssp.is_execution_replicated, 0) AS [IsExecutionReplicated],
CAST(CASE WHEN sp.type = 'P' THEN 0 ELSE 1 END AS bit) AS [ImplementationType]
FROM
sys.all_objects AS sp
LEFT OUTER JOIN sys.procedures AS ssp ON ssp.object_id = sp.object_id
WHERE
(sp.type in ('P', 'PC')) and (sp.is_ms_shipped=0)
ORDER BY
[Schema] ASC,[Name] ASC`

	if !virtual.IsVirtualCatalogJoinQuery(qA) {
		t.Fatalf("expected IsVirtualCatalogJoinQuery to match SSMS Stored Procedures Variant A")
	}
	resA, err := virtual.Execute(src, qA)
	if err != nil {
		t.Fatalf("Execute(Variant A) failed: %v", err)
	}
	if len(resA.Rows) != 1 {
		t.Fatalf("expected 1 stored procedure, got %d", len(resA.Rows))
	}
	if resA.Rows[0]["Name"] != "sp_calculate_total" {
		t.Errorf("expected sp_calculate_total, got %v", resA.Rows[0]["Name"])
	}
	if resA.Rows[0]["Schema"] != "dbo" {
		t.Errorf("expected dbo schema, got %v", resA.Rows[0]["Schema"])
	}
	if resA.Rows[0]["ID"] != int32(3000) {
		t.Errorf("expected ID 3000, got %v", resA.Rows[0]["ID"])
	}
	if urn, ok := resA.Rows[0]["Urn"].(string); !ok || urn == "" {
		t.Errorf("expected non-empty Urn, got %v", resA.Rows[0]["Urn"])
	}

	// 2. Variant B: SSMS Object Explorer with Extended Properties & Native Compilation
	qB := `SELECT
sp.name AS [Name],
SCHEMA_NAME(sp.schema_id) AS [Schema],
'Server[@Name=' + quotename(CAST(serverproperty(N'Servername') AS sysname),'''') + ']' + '/Database[@Name=' + quotename(db_name(),'''') + ']' + '/StoredProcedure[@Name=' + quotename(sp.name,'''') + ' and @Schema=' + quotename(SCHEMA_NAME(sp.schema_id),'''') + ']' AS [Urn],
sp.create_date AS [CreateDate],
ISNULL(ssp.name, N'') AS [Owner],
CAST(CASE WHEN ISNULL(smsp.definition, ssmsp.definition) IS NULL THEN 1 ELSE 0 END AS bit) AS [IsEncrypted],
CASE WHEN sp.type = N'P' THEN 1 WHEN sp.type = N'PC' THEN 2 ELSE 1 END AS [ImplementationType],
ISNULL(sm.uses_native_compilation,0) AS [IsNativelyCompiled]
FROM
sys.all_objects AS sp
LEFT OUTER JOIN sys.database_principals AS ssp ON ssp.principal_id = ISNULL(sp.principal_id, (OBJECTPROPERTY(sp.object_id, 'OwnerId')))
LEFT OUTER JOIN sys.sql_modules AS smsp ON smsp.object_id = sp.object_id
LEFT OUTER JOIN sys.system_sql_modules AS ssmsp ON ssmsp.object_id = sp.object_id
LEFT OUTER JOIN sys.all_sql_modules AS sm ON sm.object_id = sp.object_id
WHERE
(sp.type = 'P' OR sp.type = 'RF' OR sp.type='PC')and(CAST(
 case
    when sp.is_ms_shipped = 1 then 1
    when exists (
        select
            1
        from
            sys.extended_properties
        where
            major_id = sp.object_id and
            minor_id = 0 and
            class = 1 and
            name = N'microsoft_database_tools_support')
        then 1
    else 0
end
             AS bit)='0' and ISNULL(sm.uses_native_compilation,0)='0')`

	if !virtual.IsVirtualCatalogJoinQuery(qB) {
		t.Fatalf("expected IsVirtualCatalogJoinQuery to match SSMS Stored Procedures Variant B")
	}
	resB, err := virtual.Execute(src, qB)
	if err != nil {
		t.Fatalf("Execute(Variant B) failed: %v", err)
	}
	if len(resB.Rows) != 1 {
		t.Fatalf("expected 1 stored procedure for Variant B, got %d", len(resB.Rows))
	}
	if resB.Rows[0]["Name"] != "sp_calculate_total" {
		t.Errorf("expected sp_calculate_total, got %v", resB.Rows[0]["Name"])
	}

	// 3. Stored Procedure Parameters Query
	qParams := `SELECT
param.name AS [Name],
param.parameter_id AS [ID],
'Server[@Name=' + quotename(CAST(serverproperty(N'Servername') AS sysname),'''') + ']' + '/Database[@Name=' + quotename(db_name(),'''') + ']' + '/StoredProcedure[@Name=' + quotename(sp.name,'''') + ' and @Schema=' + quotename(SCHEMA_NAME(sp.schema_id),'''') + ']' + '/Param[@Name=' + quotename(param.name,'''') + ']' AS [Urn],
param.is_output AS [IsOutputParameter],
TYPE_NAME(param.user_type_id) AS [DataType],
param.max_length AS [Length],
param.precision AS [NumericPrecision],
param.scale AS [NumericScale]
FROM
sys.all_objects AS sp
INNER JOIN sys.all_parameters AS param ON param.object_id = sp.object_id
WHERE
(sp.type = 'P' OR sp.type = 'RF' OR sp.type='PC')`

	if !virtual.IsVirtualCatalogJoinQuery(qParams) {
		t.Fatalf("expected IsVirtualCatalogJoinQuery to match SSMS Procedure Parameters")
	}
	resParams, err := virtual.Execute(src, qParams)
	if err != nil {
		t.Fatalf("Execute(Procedure Parameters) failed: %v", err)
	}
	if len(resParams.Rows) != 2 {
		t.Fatalf("expected 2 parameters for sp_calculate_total, got %d", len(resParams.Rows))
	}
	if resParams.Rows[0]["Name"] != "@UserID" || resParams.Rows[1]["Name"] != "@Total" {
		t.Errorf("unexpected parameter names: %+v", resParams.Rows)
	}

	// Test SSMS Procedure Scripting Parameters Query with [Number]
	qScriptParams := `SELECT
param.name AS [Name],
sp.object_id AS [IDText],
db_name() AS [DatabaseName],
param.name AS [ParamName],
1 AS [Number]
FROM sys.all_objects AS sp
INNER JOIN sys.all_parameters AS param ON param.object_id=sp.object_id
WHERE (sp.type = 'P' OR sp.type = 'RF' OR sp.type='PC')and(sp.name='sp_calculate_total' and SCHEMA_NAME(sp.schema_id)='dbo')`

	resScriptParams, err := virtual.Execute(src, qScriptParams)
	if err != nil {
		t.Fatalf("Execute(Procedure Scripting Parameters) failed: %v", err)
	}
	if len(resScriptParams.Rows) != 2 {
		t.Fatalf("expected 2 parameters for sp_calculate_total in scripting query, got %d", len(resScriptParams.Rows))
	}
	if resScriptParams.Rows[0]["Number"] != int16(1) {
		t.Errorf("expected Number to be int16(1), got %T (%v)", resScriptParams.Rows[0]["Number"], resScriptParams.Rows[0]["Number"])
	}
	if resScriptParams.Rows[0]["ParamName"] != "@UserID" {
		t.Errorf("expected ParamName to be @UserID, got %v", resScriptParams.Rows[0]["ParamName"])
	}
}


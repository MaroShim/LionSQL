package virtual_test

import (
	"fmt"
	"sync"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/catalog/virtual"
	"lionsql/pkg/storage"
)

func newRichTestMockSource() *mockSource {
	return &mockSource{
		tables: []*storage.TableSchema{
			{
				Name: "customers",
				Columns: []storage.Column{
					{Name: "customer_id", Type: storage.TypeInt, PrimaryKey: true},
					{Name: "company_name", Type: storage.TypeVarChar, Precision: 100, Nullable: false},
					{Name: "contact_email", Type: storage.TypeVarChar, Precision: 150, Nullable: true},
					{Name: "credit_limit", Type: storage.TypeDecimal, Precision: 18, Scale: 2},
					{Name: "is_active", Type: storage.TypeBool},
					{Name: "created_at", Type: storage.TypeDateTime},
				},
				Indexes: []storage.IndexSchema{
					{Name: "idx_cust_email", Columns: []string{"contact_email"}, Unique: true},
				},
			},
			{
				Name: "orders",
				Columns: []storage.Column{
					{Name: "order_id", Type: storage.TypeInt, PrimaryKey: true},
					{Name: "customer_id", Type: storage.TypeInt, Nullable: false},
					{Name: "order_date", Type: storage.TypeDate},
					{Name: "total_amount", Type: storage.TypeDecimal, Precision: 18, Scale: 2},
				},
				ForeignKeys: []storage.ForeignKeySchema{
					{
						Name:          "fk_orders_customer",
						ChildTable:    "orders",
						ChildColumns:  []string{"customer_id"},
						ParentTable:   "customers",
						ParentColumns: []string{"customer_id"},
						OnUpdate:      storage.ActionCascade,
						OnDelete:      storage.ActionRestrict,
					},
				},
			},
			{
				Name: "order_items",
				Columns: []storage.Column{
					{Name: "order_id", Type: storage.TypeInt, PrimaryKey: true},
					{Name: "item_id", Type: storage.TypeInt, PrimaryKey: true},
					{Name: "quantity", Type: storage.TypeInt},
					{Name: "unit_price", Type: storage.TypeDecimal, Precision: 10, Scale: 2},
				},
				ForeignKeys: []storage.ForeignKeySchema{
					{
						Name:          "fk_items_order",
						ChildTable:    "order_items",
						ChildColumns:  []string{"order_id"},
						ParentTable:   "orders",
						ParentColumns: []string{"order_id"},
						OnUpdate:      storage.ActionCascade,
						OnDelete:      storage.ActionCascade,
					},
				},
			},
		},
		views: []*catalog.View{
			{
				Name:  "v_active_customers",
				Query: "SELECT customer_id, company_name FROM customers WHERE is_active = 1",
			},
			{
				Name:  "v_order_summary",
				Query: "SELECT o.order_id, c.company_name, o.total_amount FROM orders o JOIN customers c ON o.customer_id = c.customer_id",
			},
		},
		procedures: []*catalog.Procedure{
			{
				Name: "sp_create_order",
				Body: "CREATE PROCEDURE sp_create_order @cust_id INT AS BEGIN SELECT 1; END",
			},
			{
				Name: "sp_calculate_totals",
				Body: "CREATE PROCEDURE sp_calculate_totals AS BEGIN SELECT 100; END",
			},
		},
	}
}

// 1. SSMS Object Explorer & Introspection Suite
func TestToolAudit_SSMS_Introspection(t *testing.T) {
	src := newRichTestMockSource()

	// 1.1. SSMS Tables and schemas join query
	qTables := "SELECT t.name AS name, s.name AS schema_name, t.object_id, t.type FROM sys.tables t JOIN sys.schemas s ON t.schema_id = s.schema_id ORDER BY s.name, t.name"
	if !virtual.IsVirtualCatalogJoinQuery(qTables) {
		t.Fatalf("expected IsVirtualCatalogJoinQuery to match SSMS tables join: %s", qTables)
	}
	resTables, err := virtual.Execute(src, qTables)
	if err != nil {
		t.Fatalf("Execute(SSMS tables) failed: %v", err)
	}
	if len(resTables.Rows) != 3 {
		t.Fatalf("expected 3 tables, got %d", len(resTables.Rows))
	}
	for _, row := range resTables.Rows {
		if row["schema_name"] != "dbo" {
			t.Errorf("expected schema_name dbo, got %v", row["schema_name"])
		}
		if row["object_id"] == nil || row["name"] == "" {
			t.Errorf("missing object_id or name in row: %+v", row)
		}
	}

	// 1.2. SSMS Columns query for a specific table by object_id
	qCols := "SELECT c.name, c.column_id, c.max_length, c.is_nullable, c.is_identity, t.name AS data_type FROM sys.columns c JOIN sys.types t ON c.user_type_id = t.user_type_id WHERE c.object_id = 1000 ORDER BY c.column_id"
	if !virtual.IsVirtualCatalogJoinQuery(qCols) {
		t.Fatalf("expected IsVirtualCatalogJoinQuery to match SSMS columns join: %s", qCols)
	}
	resCols, err := virtual.Execute(src, qCols)
	if err != nil {
		t.Fatalf("Execute(SSMS columns) failed: %v", err)
	}
	// Table 1000 is customers, which has 6 columns
	if len(resCols.Rows) != 6 {
		t.Fatalf("expected 6 columns for customers, got %d", len(resCols.Rows))
	}
	if resCols.Rows[0]["name"] != "customer_id" {
		t.Errorf("expected first column customer_id, got %v", resCols.Rows[0]["name"])
	}

	// 1.3. SSMS Primary Keys query
	qPK := "SELECT * FROM sys.indexes WHERE is_primary_key = 1"
	resPK, err := virtual.Execute(src, qPK)
	if err != nil {
		t.Fatalf("Execute(SSMS primary keys) failed: %v", err)
	}
	// customers (1), orders (1), order_items (2 composite) -> 4 PK columns total
	if len(resPK.Rows) != 4 {
		t.Fatalf("expected 4 primary key entries across 3 tables, got %d", len(resPK.Rows))
	}

	// 1.4. SSMS Secondary Indexes query
	qIdx := "SELECT * FROM sys.indexes WHERE is_primary_key = 0"
	resIdx, err := virtual.Execute(src, qIdx)
	if err != nil {
		t.Fatalf("Execute(SSMS secondary indexes) failed: %v", err)
	}
	if len(resIdx.Rows) != 1 {
		t.Fatalf("expected 1 secondary index (idx_cust_email), got %d", len(resIdx.Rows))
	}
	if resIdx.Rows[0]["name"] != "idx_cust_email" && resIdx.Rows[0]["constraint_name"] != "idx_cust_email" {
		t.Errorf("expected idx_cust_email index name, got %+v", resIdx.Rows[0])
	}

	// 1.5. SSMS Foreign Keys query
	qFK := "SELECT * FROM INFORMATION_SCHEMA.REFERENTIAL_CONSTRAINTS"
	resFK, err := virtual.Execute(src, qFK)
	if err != nil {
		t.Fatalf("Execute(SSMS foreign keys) failed: %v", err)
	}
	if len(resFK.Rows) != 2 {
		t.Fatalf("expected 2 foreign keys, got %d", len(resFK.Rows))
	}

	// 1.6. SSMS Views & Programmables
	qViews := "SELECT v.name AS pure_name, s.name AS schema_name, v.object_id, v.create_date FROM sys.objects v JOIN sys.schemas s ON v.schema_id = s.schema_id WHERE v.type = 'V'"
	resViews, err := virtual.Execute(src, qViews)
	if err != nil {
		t.Fatalf("Execute(SSMS views) failed: %v", err)
	}
	if len(resViews.Rows) != 2 {
		t.Fatalf("expected 2 views, got %d", len(resViews.Rows))
	}

	// 1.7. SSMS sys.sql_modules / syscomments definition query
	qDef := "SELECT definition FROM sys.sql_modules WHERE object_id = 3000"
	resDef, err := virtual.Execute(src, qDef)
	if err != nil {
		t.Fatalf("Execute(SSMS sql_modules) failed: %v", err)
	}
	if len(resDef.Rows) == 0 {
		t.Fatalf("expected at least 1 definition row, got 0")
	}
}

// 2. DBeaver Universal Database Tool Introspection Suite
func TestToolAudit_DBeaver_Introspection(t *testing.T) {
	src := newRichTestMockSource()

	// 2.1. DBeaver sys.tables with wildcard
	qTables := "SELECT t.* FROM sys.tables t ORDER BY t.name"
	if virtual.IsVirtualCatalogJoinQuery(qTables) {
		res, err := virtual.Execute(src, qTables)
		if err != nil {
			t.Fatalf("Execute(DBeaver tables) failed: %v", err)
		}
		if len(res.Rows) != 3 {
			t.Fatalf("expected 3 tables, got %d", len(res.Rows))
		}
	}

	// 2.2. DBeaver sys.columns joined with sys.types
	qCols := "SELECT c.*, t.name AS type_name FROM sys.columns c JOIN sys.types t ON c.user_type_id = t.user_type_id"
	resCols, err := virtual.Execute(src, qCols)
	if err != nil {
		t.Fatalf("Execute(DBeaver columns) failed: %v", err)
	}
	// Total columns: customers(6) + orders(4) + order_items(4) = 14
	if len(resCols.Rows) != 14 {
		t.Fatalf("expected 14 columns across all tables, got %d", len(resCols.Rows))
	}

	// 2.3. DBeaver PGWire pg_class JOIN pg_namespace
	qPgClass := "SELECT c.relname, n.nspname, c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relkind IN ('r', 'v')"
	resPg, err := virtual.Execute(src, qPgClass)
	if err != nil {
		t.Fatalf("Execute(DBeaver pg_class) failed: %v", err)
	}
	// 3 tables + 2 views across 2 schemas (public, dbo) = 10 rows
	if len(resPg.Rows) < 5 {
		t.Fatalf("expected at least 5 pg_class entries, got %d", len(resPg.Rows))
	}

	// 2.4. DBeaver PGWire pg_attribute column introspection
	qPgAttr := "SELECT a.attname, a.attnum FROM pg_attribute a JOIN pg_class c ON a.attrelid = c.oid WHERE c.relname = 'customers' AND a.attnum > 0"
	resAttr, err := virtual.Execute(src, qPgAttr)
	if err != nil {
		t.Fatalf("Execute(DBeaver pg_attribute) failed: %v", err)
	}
	if len(resAttr.Rows) != 6 {
		t.Fatalf("expected 6 attributes for customers, got %d", len(resAttr.Rows))
	}
}

// 3. DbGate 9-Step Choreography and Object ID Invariant
func TestToolAudit_DbGate_Choreography(t *testing.T) {
	src := newRichTestMockSource()

	// Step 1: modifications
	qMod := "SELECT o.object_id, o.modify_date, o.type, o.name AS pure_name, s.name AS schema_name FROM sys.objects o JOIN sys.schemas s ON o.schema_id = s.schema_id"
	resMod, err := virtual.Execute(src, qMod)
	if err != nil {
		t.Fatalf("DbGate step 1 (modifications) failed: %v", err)
	}
	if len(resMod.Rows) < 3 {
		t.Fatalf("expected at least 3 objects in modifications, got %d", len(resMod.Rows))
	}

	// Step 2: tables (maps table name to object_id)
	qTables := "SELECT t.name AS pure_name, s.name AS schema_name, t.object_id, t.create_date, t.modify_date, NULL AS object_comment FROM sys.tables t JOIN sys.schemas s ON t.schema_id = s.schema_id"
	resTables, err := virtual.Execute(src, qTables)
	if err != nil {
		t.Fatalf("DbGate step 2 (tables) failed: %v", err)
	}
	if len(resTables.Rows) != 3 {
		t.Fatalf("expected 3 tables in step 2, got %d", len(resTables.Rows))
	}

	tableIDMap := make(map[string]int)
	idTableMap := make(map[int]string)
	for _, row := range resTables.Rows {
		name := fmt.Sprintf("%v", row["pure_name"])
		id := row["object_id"].(int)
		tableIDMap[name] = id
		idTableMap[id] = name
	}

	// Invariant check: Sorted order must guarantee customers = 1000, order_items = 1001, orders = 1002
	if tableIDMap["customers"] != 1000 || tableIDMap["order_items"] != 1001 || tableIDMap["orders"] != 1002 {
		t.Fatalf("deterministic object_id invariant violated! got tableIDMap: %+v", tableIDMap)
	}

	// Step 3: columns (must match the exact table object_id)
	qCols := "SELECT c.name AS column_name, t.name AS data_type, c.object_id, c.is_identity, c.max_length, c.precision, c.scale, c.is_nullable, c.column_id FROM sys.columns c JOIN sys.types t ON c.user_type_id = t.user_type_id"
	resCols, err := virtual.Execute(src, qCols)
	if err != nil {
		t.Fatalf("DbGate step 3 (columns) failed: %v", err)
	}

	// Verify that customers columns have object_id 1000, and orders columns have object_id 1002
	for _, row := range resCols.Rows {
		colName := fmt.Sprintf("%v", row["column_name"])
		objID := row["object_id"].(int)
		tableName := idTableMap[objID]

		switch colName {
		case "customer_id", "company_name", "credit_limit":
			if tableName != "customers" && tableName != "orders" {
				t.Errorf("column %s has mismatched object_id %d belonging to %s", colName, objID, tableName)
			}
		case "total_amount", "order_date":
			if tableName != "orders" {
				t.Errorf("column %s has mismatched object_id %d belonging to %s", colName, objID, tableName)
			}
		case "quantity", "unit_price":
			if tableName != "order_items" {
				t.Errorf("column %s has mismatched object_id %d belonging to %s", colName, objID, tableName)
			}
		}
	}

	// Step 4: primaryKeys
	qPK := "SELECT * FROM sys.indexes WHERE is_primary_key = 1"
	resPK, err := virtual.Execute(src, qPK)
	if err != nil {
		t.Fatalf("DbGate step 4 (primaryKeys) failed: %v", err)
	}
	if len(resPK.Rows) != 4 {
		t.Fatalf("expected 4 primary key records, got %d", len(resPK.Rows))
	}

	// Step 5: indexes (secondary)
	qIdx := "SELECT * FROM sys.indexes WHERE is_primary_key = 0"
	resIdx, err := virtual.Execute(src, qIdx)
	if err != nil {
		t.Fatalf("DbGate step 5 (indexes) failed: %v", err)
	}
	if len(resIdx.Rows) != 1 {
		t.Fatalf("expected 1 secondary index, got %d", len(resIdx.Rows))
	}

	// Step 6: foreignKeys
	qFK := "SELECT * FROM INFORMATION_SCHEMA.REFERENTIAL_CONSTRAINTS"
	resFK, err := virtual.Execute(src, qFK)
	if err != nil {
		t.Fatalf("DbGate step 6 (foreignKeys) failed: %v", err)
	}
	if len(resFK.Rows) != 2 {
		t.Fatalf("expected 2 foreign keys, got %d", len(resFK.Rows))
	}

	// Step 7: tableSizes (row counts)
	qSizes := "SELECT tableRowCount FROM sys.tables JOIN sys.partitions ON 1=1"
	resSizes, err := virtual.Execute(src, qSizes)
	if err != nil {
		t.Fatalf("DbGate step 7 (tableSizes) failed: %v", err)
	}
	if len(resSizes.Rows) != 3 {
		t.Fatalf("expected 3 table size entries, got %d", len(resSizes.Rows))
	}

	// Step 8: views
	qViews := "SELECT v.name AS pure_name, s.name AS schema_name, v.object_id, v.create_date FROM sys.objects v JOIN sys.schemas s ON v.schema_id = s.schema_id WHERE v.type = 'V'"
	resViews, err := virtual.Execute(src, qViews)
	if err != nil {
		t.Fatalf("DbGate step 8 (views) failed: %v", err)
	}
	if len(resViews.Rows) != 2 {
		t.Fatalf("expected 2 views, got %d", len(resViews.Rows))
	}

	// Step 9: programmables
	qProcs := "SELECT p.name AS pure_name, s.name AS schema_name, p.object_id, p.create_date, p.type AS sql_object_type FROM sys.objects p JOIN sys.schemas s ON p.schema_id = s.schema_id WHERE p.type IN ('P', 'IF', 'FN', 'TF')"
	resProcs, err := virtual.Execute(src, qProcs)
	if err != nil {
		t.Fatalf("DbGate step 9 (programmables) failed: %v", err)
	}
	if len(resProcs.Rows) != 2 {
		t.Fatalf("expected 2 stored procedures, got %d", len(resProcs.Rows))
	}
}

// 4. Beekeeper Studio Introspection & Computed Columns
func TestToolAudit_Beekeeper_ComputedColumns(t *testing.T) {
	src := newRichTestMockSource()

	qBeekeeper := `SELECT
  ic.table_schema,
  ic.table_name,
  ic.column_name,
  ic.ordinal_position,
  ic.column_default,
  ic.data_type,
  ic.is_nullable
FROM INFORMATION_SCHEMA.COLUMNS ic
LEFT JOIN sys.computed_columns sc ON 1=1
WHERE ic.table_name = 'customers' AND ic.table_schema = 'dbo'`

	if !virtual.IsVirtualCatalogJoinQuery(qBeekeeper) {
		t.Fatalf("expected IsVirtualCatalogJoinQuery to match Beekeeper computed columns query")
	}

	res, err := virtual.Execute(src, qBeekeeper)
	if err != nil {
		t.Fatalf("Execute(Beekeeper computed columns) failed: %v", err)
	}
	if len(res.Rows) != 6 {
		t.Fatalf("expected 6 columns for customers, got %d", len(res.Rows))
	}
	if res.Rows[0]["column_name"] != "customer_id" {
		t.Errorf("expected first column customer_id, got %v", res.Rows[0]["column_name"])
	}
}

// 5. High-Concurrency Stress Test across Virtual Catalog
func TestToolAudit_VirtualCatalog_ConcurrentStress(t *testing.T) {
	src := newRichTestMockSource()

	queries := []string{
		"SELECT t.name AS name, s.name AS schema_name, t.object_id, t.type FROM sys.tables t JOIN sys.schemas s ON t.schema_id = s.schema_id",
		"SELECT c.name, c.column_id, t.name AS data_type FROM sys.columns c JOIN sys.types t ON c.user_type_id = t.user_type_id WHERE c.object_id = 1000",
		"SELECT * FROM sys.indexes WHERE is_primary_key = 1",
		"SELECT * FROM sys.indexes WHERE is_primary_key = 0",
		"SELECT * FROM INFORMATION_SCHEMA.REFERENTIAL_CONSTRAINTS",
		"SELECT v.name AS pure_name, s.name AS schema_name, v.object_id FROM sys.objects v JOIN sys.schemas s ON v.schema_id = s.schema_id WHERE v.type = 'V'",
		"SELECT c.relname, n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relkind = 'r'",
	}

	const numWorkers = 20
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for w := 0; w < numWorkers; w++ {
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				q := queries[(workerID+i)%len(queries)]
				res, err := virtual.Execute(src, q)
				if err != nil {
					t.Errorf("worker %d iteration %d failed query %s: %v", workerID, i, q, err)
					return
				}
				if len(res.Rows) == 0 {
					t.Errorf("worker %d iteration %d returned empty rows for query %s", workerID, i, q)
					return
				}
			}
		}(w)
	}

	wg.Wait()
}

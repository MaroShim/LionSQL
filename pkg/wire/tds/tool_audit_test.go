package tds_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	_ "github.com/microsoft/go-mssqldb"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/wire/tds"
)

func setupTestTDSServer(t *testing.T) (*tds.Server, *sql.DB, func()) {
	t.Helper()
	eng := memengine.New()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		eng.Close()
		t.Fatalf("failed to start TDS server: %v", err)
	}

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=5", port)

	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		srv.Close()
		eng.Close()
		t.Fatalf("sql.Open failed: %v", err)
	}

	cleanup := func() {
		db.Close()
		srv.Close()
		eng.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Seed test schema
	ddls := []string{
		"CREATE TABLE customers (customer_id INT PRIMARY KEY, name VARCHAR(100), email VARCHAR(100), credit_limit DECIMAL(18,2), is_active BIT, created_at DATETIME);",
		"CREATE TABLE orders (order_id INT PRIMARY KEY, customer_id INT, total_amount DECIMAL(18,2), order_date DATE);",
		"CREATE TABLE order_items (order_id INT, item_id INT, quantity INT, price DECIMAL(10,2), PRIMARY KEY (order_id, item_id));",
		"CREATE INDEX idx_orders_cust ON orders (customer_id);",
		"CREATE VIEW v_active_customers AS SELECT customer_id, name, email FROM customers WHERE is_active = 1;",
		"CREATE PROCEDURE sp_dummy_report AS BEGIN SELECT 42; END;",
	}
	for _, ddl := range ddls {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			cleanup()
			t.Fatalf("DDL execution failed %q: %v", ddl, err)
		}
	}

	// Seed sample records
	_, err = db.ExecContext(ctx, "INSERT INTO customers VALUES (1, 'Acme Corp', 'acme@test.com', 50000.00, 1, '2026-01-01 10:00:00'), (2, 'Globex Corp', 'globex@test.com', 25000.00, 1, '2026-02-01 12:00:00');")
	if err != nil {
		cleanup()
		t.Fatalf("seed customers failed: %v", err)
	}

	_, err = db.ExecContext(ctx, "INSERT INTO orders VALUES (101, 1, 1250.50, '2026-03-01'), (102, 1, 450.00, '2026-03-02'), (103, 2, 8900.00, '2026-03-03');")
	if err != nil {
		cleanup()
		t.Fatalf("seed orders failed: %v", err)
	}

	return srv, db, cleanup
}

// 1. SSMS Object Explorer & Discovery End-to-End Audit
func TestToolAudit_TDS_SSMS_DiscoverySuite(t *testing.T) {
	_, db, cleanup := setupTestTDSServer(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1.1. Handshake & Server Properties
	var version string
	if err := db.QueryRowContext(ctx, "SELECT @@VERSION").Scan(&version); err != nil {
		t.Fatalf("SELECT @@VERSION failed: %v", err)
	}
	if version == "" {
		t.Fatalf("expected non-empty @@VERSION")
	}

	var prodVer string
	var engEdition int
	var collation string
	qProps := "SELECT SERVERPROPERTY('ProductVersion') AS pv, CONVERT(INT, SERVERPROPERTY('EngineEdition')) AS ee, SERVERPROPERTY('Collation') AS col"
	if err := db.QueryRowContext(ctx, qProps).Scan(&prodVer, &engEdition, &collation); err != nil {
		t.Fatalf("SERVERPROPERTY query failed: %v", err)
	}
	if prodVer != "15.0.2000.5" || engEdition != 3 || collation == "" {
		t.Fatalf("unexpected SERVERPROPERTY values: pv=%s, ee=%d, col=%s", prodVer, engEdition, collation)
	}

	// 1.2. Database Name
	var dbName string
	if err := db.QueryRowContext(ctx, "SELECT DB_NAME()").Scan(&dbName); err != nil {
		t.Fatalf("SELECT DB_NAME() failed: %v", err)
	}
	if dbName == "" {
		t.Fatalf("expected non-empty DB_NAME()")
	}

	// 1.3. SSMS Tables joined with Schemas
	qTables := "SELECT t.name AS name, s.name AS schema_name, t.object_id, t.type FROM sys.tables t JOIN sys.schemas s ON t.schema_id = s.schema_id ORDER BY s.name, t.name"
	rows, err := db.QueryContext(ctx, qTables)
	if err != nil {
		t.Fatalf("SSMS tables join query failed: %v", err)
	}
	defer rows.Close()

	tableCount := 0
	for rows.Next() {
		var name, schemaName, tblType string
		var objID int
		if err := rows.Scan(&name, &schemaName, &objID, &tblType); err != nil {
			t.Fatalf("rows.Scan failed: %v", err)
		}
		if schemaName != "dbo" {
			t.Errorf("expected schema_name dbo, got %s", schemaName)
		}
		tableCount++
	}
	if tableCount != 3 {
		t.Fatalf("expected 3 tables, got %d", tableCount)
	}

	// 1.4. SSMS Columns query for a specific table (customers = 1000)
	qCols := "SELECT c.name, c.column_id, c.max_length, c.is_nullable, c.is_identity, TYPE_NAME(c.user_type_id) AS data_type FROM sys.columns c JOIN sys.types t ON c.user_type_id = t.user_type_id WHERE c.object_id = 1000 ORDER BY c.column_id"
	rowsCols, err := db.QueryContext(ctx, qCols)
	if err != nil {
		t.Fatalf("SSMS columns query failed: %v", err)
	}
	defer rowsCols.Close()

	colCount := 0
	for rowsCols.Next() {
		var colName, dataType string
		var colID, maxLen int
		var isNullable, isIdentity bool
		if err := rowsCols.Scan(&colName, &colID, &maxLen, &isNullable, &isIdentity, &dataType); err != nil {
			t.Fatalf("columns rows.Scan failed: %v", err)
		}
		colCount++
	}
	if colCount != 6 {
		t.Fatalf("expected 6 columns for customers, got %d", colCount)
	}

	// 1.5. SSMS Primary Keys and Secondary Indexes
	qPK := "SELECT * FROM sys.indexes WHERE is_primary_key = 1"
	rowsPK, err := db.QueryContext(ctx, qPK)
	if err != nil {
		t.Fatalf("SSMS primary keys query failed: %v", err)
	}
	defer rowsPK.Close()

	pkCount := 0
	for rowsPK.Next() {
		pkCount++
	}
	if pkCount < 3 {
		t.Fatalf("expected at least 3 PK entries, got %d", pkCount)
	}

	// 1.6. SSMS Data Grid Query with Bracketed Syntax
	qData := "SELECT TOP 50 * FROM [dbo].[customers]"
	rowsData, err := db.QueryContext(ctx, qData)
	if err != nil {
		t.Fatalf("SSMS TOP data query failed: %v", err)
	}
	defer rowsData.Close()

	rowCount := 0
	for rowsData.Next() {
		rowCount++
	}
	if rowCount != 2 {
		t.Fatalf("expected 2 customers rows, got %d", rowCount)
	}
}

// 2. DbGate 9-Step Choreography End-to-End Audit
func TestToolAudit_TDS_DbGate_ChoreographedE2E(t *testing.T) {
	_, db, cleanup := setupTestTDSServer(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Step 1: modifications
	rowsMod, err := db.QueryContext(ctx, "SELECT o.object_id, o.modify_date, o.type, o.name AS pure_name, s.name AS schema_name FROM sys.objects o JOIN sys.schemas s ON o.schema_id = s.schema_id")
	if err != nil {
		t.Fatalf("DbGate step 1 failed: %v", err)
	}
	rowsMod.Close()

	// Step 2: tables (maps table name to object_id)
	rowsTables, err := db.QueryContext(ctx, "SELECT t.name AS pure_name, s.name AS schema_name, t.object_id, t.create_date, t.modify_date, NULL AS object_comment FROM sys.tables t JOIN sys.schemas s ON t.schema_id = s.schema_id")
	if err != nil {
		t.Fatalf("DbGate step 2 failed: %v", err)
	}
	tableIDMap := make(map[string]int)
	for rowsTables.Next() {
		var pureName, schemaName string
		var objID int
		var createDate, modifyDate time.Time
		var objComment sql.NullString
		if err := rowsTables.Scan(&pureName, &schemaName, &objID, &createDate, &modifyDate, &objComment); err != nil {
			rowsTables.Close()
			t.Fatalf("step 2 scan failed: %v", err)
		}
		tableIDMap[pureName] = objID
	}
	rowsTables.Close()

	if len(tableIDMap) != 3 {
		t.Fatalf("expected 3 tables, got %d", len(tableIDMap))
	}
	// Verify deterministic sorted order
	if tableIDMap["customers"] != 1000 || tableIDMap["order_items"] != 1001 || tableIDMap["orders"] != 1002 {
		t.Fatalf("deterministic ID invariant violated in TDS! got %+v", tableIDMap)
	}

	// Step 3: columns
	rowsCols, err := db.QueryContext(ctx, "SELECT c.name AS column_name, t.name AS data_type, c.object_id, c.is_identity, c.max_length, c.precision, c.scale, c.is_nullable, c.column_id FROM sys.columns c JOIN sys.types t ON c.user_type_id = t.user_type_id")
	if err != nil {
		t.Fatalf("DbGate step 3 failed: %v", err)
	}
	for rowsCols.Next() {
		var colName, dataType string
		var objID, maxLen, prec, scale, colID int
		var isIdentity, isNullable bool
		if err := rowsCols.Scan(&colName, &dataType, &objID, &isIdentity, &maxLen, &prec, &scale, &isNullable, &colID); err != nil {
			rowsCols.Close()
			t.Fatalf("step 3 scan failed: %v", err)
		}
		if objID != 1000 && objID != 1001 && objID != 1002 {
			rowsCols.Close()
			t.Fatalf("column %s has invalid object_id %d", colName, objID)
		}
	}
	rowsCols.Close()

	// Step 4: primaryKeys
	rowsPK, err := db.QueryContext(ctx, "SELECT * FROM sys.indexes WHERE is_primary_key = 1")
	if err != nil {
		t.Fatalf("DbGate step 4 failed: %v", err)
	}
	rowsPK.Close()

	// Step 5: indexes (secondary)
	rowsIdx, err := db.QueryContext(ctx, "SELECT * FROM sys.indexes WHERE is_primary_key = 0")
	if err != nil {
		t.Fatalf("DbGate step 5 failed: %v", err)
	}
	rowsIdx.Close()

	// Step 6: foreignKeys
	rowsFK, err := db.QueryContext(ctx, "SELECT * FROM INFORMATION_SCHEMA.REFERENTIAL_CONSTRAINTS")
	if err != nil {
		t.Fatalf("DbGate step 6 failed: %v", err)
	}
	rowsFK.Close()

	// Step 7: tableSizes (row counts)
	rowsSizes, err := db.QueryContext(ctx, "SELECT tableRowCount FROM sys.tables JOIN sys.partitions ON 1=1")
	if err != nil {
		t.Fatalf("DbGate step 7 failed: %v", err)
	}
	rowsSizes.Close()

	// Step 8: views
	rowsViews, err := db.QueryContext(ctx, "SELECT v.name AS pure_name, s.name AS schema_name, v.object_id, v.create_date FROM sys.objects v JOIN sys.schemas s ON v.schema_id = s.schema_id WHERE v.type = 'V'")
	if err != nil {
		t.Fatalf("DbGate step 8 failed: %v", err)
	}
	rowsViews.Close()

	// Step 9: programmables
	rowsProcs, err := db.QueryContext(ctx, "SELECT p.name AS pure_name, s.name AS schema_name, p.object_id, p.create_date, p.type AS sql_object_type FROM sys.objects p JOIN sys.schemas s ON p.schema_id = s.schema_id WHERE p.type IN ('P', 'IF', 'FN', 'TF')")
	if err != nil {
		t.Fatalf("DbGate step 9 failed: %v", err)
	}
	rowsProcs.Close()

	// Step 10: DbGate Paged Data Grid Query
	qPaging := `SELECT [basetbl].*
FROM [dbo].[orders] AS [basetbl]
ORDER BY [order_id] ASC
OFFSET 1 ROWS FETCH NEXT 2 ROWS ONLY`

	rowsPage, err := db.QueryContext(ctx, qPaging)
	if err != nil {
		t.Fatalf("DbGate OFFSET FETCH paging query failed: %v", err)
	}
	defer rowsPage.Close()

	pagedOrders := 0
	for rowsPage.Next() {
		pagedOrders++
	}
	if pagedOrders != 2 {
		t.Fatalf("expected 2 paged orders with OFFSET 1 FETCH 2, got %d", pagedOrders)
	}
}

// 3. DBeaver Universal Database Tool End-to-End Audit
func TestToolAudit_TDS_DBeaver_E2E(t *testing.T) {
	_, db, cleanup := setupTestTDSServer(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 3.1. DBeaver EngineEdition
	var engineEdition int
	if err := db.QueryRowContext(ctx, "SELECT SERVERPROPERTY('EngineEdition')").Scan(&engineEdition); err != nil {
		t.Fatalf("DBeaver EngineEdition query failed: %v", err)
	}
	if engineEdition != 3 {
		t.Fatalf("expected EngineEdition 3, got %d", engineEdition)
	}

	// 3.2. DBeaver sys.databases with wildcard
	rowsDB, err := db.QueryContext(ctx, "SELECT db.* FROM sys.databases db ORDER BY db.name")
	if err != nil {
		t.Fatalf("DBeaver sys.databases query failed: %v", err)
	}
	defer rowsDB.Close()
	cols, err := rowsDB.Columns()
	if err != nil {
		t.Fatalf("Columns() failed: %v", err)
	}
	foundMaster := false
	for rowsDB.Next() {
		vals := make([]any, len(cols))
		valPtrs := make([]any, len(cols))
		for i := range vals {
			valPtrs[i] = &vals[i]
		}
		if err := rowsDB.Scan(valPtrs...); err != nil {
			t.Fatalf("Scan failed: %v", err)
		}
		for i, c := range cols {
			if c == "name" && fmt.Sprintf("%v", vals[i]) == "master" {
				foundMaster = true
			}
		}
	}
	if !foundMaster {
		t.Fatalf("expected to find master database in sys.databases")
	}

	// 3.3. DBeaver sys.tables with alias
	rowsTbl, err := db.QueryContext(ctx, "SELECT t.* FROM sys.tables t ORDER BY t.name")
	if err != nil {
		t.Fatalf("DBeaver sys.tables query failed: %v", err)
	}
	defer rowsTbl.Close()
	tblCount := 0
	for rowsTbl.Next() {
		tblCount++
	}
	if tblCount != 3 {
		t.Fatalf("expected 3 tables from DBeaver sys.tables, got %d", tblCount)
	}
}

// 4. Beekeeper Studio End-to-End Audit
func TestToolAudit_TDS_Beekeeper_E2E(t *testing.T) {
	_, db, cleanup := setupTestTDSServer(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 4.1. Version alias
	var version string
	if err := db.QueryRowContext(ctx, "SELECT @@VERSION AS version").Scan(&version); err != nil {
		t.Fatalf("Beekeeper version query failed: %v", err)
	}
	if version == "" {
		t.Fatalf("expected non-empty version")
	}

	// 4.2. Beekeeper Schema column introspection query with computed column join
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

	rowsCols, err := db.QueryContext(ctx, qBeekeeper)
	if err != nil {
		t.Fatalf("Beekeeper columns introspection failed: %v", err)
	}
	defer rowsCols.Close()

	colCount := 0
	for rowsCols.Next() {
		colCount++
	}
	if colCount != 6 {
		t.Fatalf("expected 6 columns for customers, got %d", colCount)
	}

	// 4.3. Beekeeper Bracketed TOP Query with AS alias
	qTop := "SELECT TOP 10 [basetbl].* FROM [dbo].[customers] AS [basetbl]"
	rowsTop, err := db.QueryContext(ctx, qTop)
	if err != nil {
		t.Fatalf("Beekeeper TOP bracket query failed: %v", err)
	}
	defer rowsTop.Close()

	topCount := 0
	for rowsTop.Next() {
		topCount++
	}
	if topCount != 2 {
		t.Fatalf("expected 2 customer records, got %d", topCount)
	}
}

// 5. Concurrent GUI Client Stress Test over TDS Socket
func TestToolAudit_TDS_ConcurrentGUIStress(t *testing.T) {
	_, db, cleanup := setupTestTDSServer(t)
	defer cleanup()

	const numWorkers = 8
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for w := 0; w < numWorkers; w++ {
		go func(workerID int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			for i := 0; i < iterations; i++ {
				switch (workerID + i) % 4 {
				case 0:
					// Metadata introspection
					rows, err := db.QueryContext(ctx, "SELECT t.name AS name, s.name AS schema_name, t.object_id FROM sys.tables t JOIN sys.schemas s ON t.schema_id = s.schema_id")
					if err != nil {
						t.Errorf("worker %d iteration %d metadata query failed: %v", workerID, i, err)
						return
					}
					count := 0
					for rows.Next() {
						count++
					}
					rows.Close()
					if count != 3 {
						t.Errorf("worker %d iteration %d expected 3 tables, got %d", workerID, i, count)
						return
					}

				case 1:
					// Paged data query
					rows, err := db.QueryContext(ctx, "SELECT TOP 10 * FROM [dbo].[orders]")
					if err != nil {
						t.Errorf("worker %d iteration %d TOP data query failed: %v", workerID, i, err)
						return
					}
					for rows.Next() {
					}
					rows.Close()

				case 2:
					// OFFSET ... FETCH paging
					rows, err := db.QueryContext(ctx, "SELECT [basetbl].* FROM [dbo].[customers] AS [basetbl] ORDER BY [customer_id] OFFSET 0 ROWS FETCH NEXT 5 ROWS ONLY")
					if err != nil {
						t.Errorf("worker %d iteration %d OFFSET FETCH query failed: %v", workerID, i, err)
						return
					}
					for rows.Next() {
					}
					rows.Close()

				case 3:
					// Concurrent DML write and commit
					newCustID := 10000 + workerID*1000 + i
					_, err := db.ExecContext(ctx, fmt.Sprintf("INSERT INTO customers VALUES (%d, 'StressCorp', 'stress@test.com', 1000.00, 1, '2026-01-01 00:00:00')", newCustID))
					if err != nil {
						t.Errorf("worker %d iteration %d INSERT failed: %v", workerID, i, err)
						return
					}
				}
			}
		}(w)
	}

	wg.Wait()
}

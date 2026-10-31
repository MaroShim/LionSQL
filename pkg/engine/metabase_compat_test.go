package engine_test

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestMetabaseTableSyncQuery(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})
	defer coord.Close()

	_, err := coord.ExecuteSessionSQL("s1", "CREATE TABLE customers (id INT PRIMARY KEY, name VARCHAR(100), country VARCHAR(50));")
	if err != nil {
		t.Fatalf("create table failed: %v", err)
	}
	_, err = coord.ExecuteSessionSQL("s1", "CREATE TABLE orders (id INT PRIMARY KEY, customer_id INT, total_amount DECIMAL(10,2));")
	if err != nil {
		t.Fatalf("create orders failed: %v", err)
	}
	_, _ = coord.ExecuteSessionSQL("s1", "INSERT INTO customers VALUES (1, 'Alice', 'USA'), (2, 'Bob', 'Korea');")
	_, _ = coord.ExecuteSessionSQL("s1", "INSERT INTO orders VALUES (101, 1, 100.0), (102, 1, 200.0), (103, 2, 50.0);")

	q := `SELECT "n"."nspname" AS "schema", "c"."relname" AS "name", CASE "c"."relkind" WHEN 'r' THEN 'TABLE' WHEN 'p' THEN 'PARTITIONED TABLE' WHEN 'v' THEN 'VIEW' WHEN 'f' THEN 'FOREIGN TABLE' WHEN 'm' THEN 'MATERIALIZED VIEW' ELSE NULL END AS "type", "d"."description" AS "description", NULLIF("stat"."n_live_tup", -1) AS "estimated_row_count" FROM "pg_catalog"."pg_class" AS "c" INNER JOIN "pg_catalog"."pg_namespace" AS "n" ON "c"."relnamespace" = "n"."oid" LEFT JOIN "pg_catalog"."pg_description" AS "d" ON ("c"."oid" = "d"."objoid") AND ("d"."objsubid" = 0) AND ("d"."classoid" = 'pg_class'::regclass) LEFT JOIN "pg_stat_user_tables" AS "stat" ON ("n"."nspname" = "stat"."schemaname") AND ("c"."relname" = "stat"."relname") WHERE ("c"."relnamespace" = "n"."oid") AND ("n"."nspname" !~ '^pg_') AND ("n"."nspname" <> 'information_schema') AND c.relkind in ('r', 'p', 'v', 'f', 'm') AND ("n"."nspname" IN ('public', 'dbo')) ORDER BY "type" ASC, "schema" ASC, "name" ASC`

	res, err := coord.ExecuteSessionSQL("s1", q)
	if err != nil {
		t.Fatalf("ExecuteSessionSQL failed: %v", err)
	}
	if len(res.Rows) == 0 {
		t.Fatalf("expected rows, got 0")
	}

	resSchema, err := coord.ExecuteSessionSQL("s1", "SELECT current_schema();")
	if err != nil {
		t.Fatalf("current_schema failed: %v", err)
	}
	if len(resSchema.Rows) != 1 {
		t.Fatalf("expected 1 row for current_schema, got %d", len(resSchema.Rows))
	}

	qJoin := `SELECT "Customers"."country" AS "Customers__country", COUNT(*) AS "count" FROM "orders" LEFT JOIN (SELECT "customers"."id" AS "id", "customers"."name" AS "name", "customers"."country" AS "country" FROM "customers") AS "Customers" ON "orders"."customer_id" = "Customers"."id" GROUP BY "Customers"."country" ORDER BY "Customers"."country" ASC`

	resJoin, err := coord.ExecuteSessionSQL("s1", qJoin)
	if err != nil {
		t.Fatalf("qJoin failed: %v", err)
	}
	t.Logf("Join Cols: %v", resJoin.Columns)
	for i, r := range resJoin.Rows {
		t.Logf("Join Row %d: %v", i, r)
	}

	// 4. MSSQL Table Discovery Query
	qMssqlTables := `SELECT
  NULL AS [role],
  s.name AS [schema],
  o.name AS [table],
  HAS_PERMS_BY_NAME(QUOTENAME(s.name) + '.' + QUOTENAME(o.name), 'OBJECT', 'SELECT') AS [select],
  HAS_PERMS_BY_NAME(QUOTENAME(s.name) + '.' + QUOTENAME(o.name), 'OBJECT', 'UPDATE') AS [update],
  HAS_PERMS_BY_NAME(QUOTENAME(s.name) + '.' + QUOTENAME(o.name), 'OBJECT', 'INSERT') AS [insert],
  HAS_PERMS_BY_NAME(QUOTENAME(s.name) + '.' + QUOTENAME(o.name), 'OBJECT', 'DELETE') AS [delete]
FROM sys.objects o
JOIN sys.schemas s ON o.schema_id = s.schema_id
WHERE o.type IN ('U', 'V')`

	resMssql, err := coord.ExecuteSessionSQL("s1", qMssqlTables)
	if err != nil {
		t.Fatalf("MSSQL table discovery failed: %v", err)
	}
	if len(resMssql.Rows) < 2 {
		t.Fatalf("expected at least 2 tables, got %d", len(resMssql.Rows))
	}
	if resMssql.Columns[1] != "schema" || resMssql.Columns[2] != "table" {
		t.Fatalf("unexpected columns: %v", resMssql.Columns)
	}

	// 5. MSSQL JDBC Metadata Temp Table Batch
	qMssqlColumns := `DECLARE @mssqljdbc_temp_sp_columns_result TABLE(TABLE_QUALIFIER SYSNAME, TABLE_OWNER SYSNAME, TABLE_NAME SYSNAME, COLUMN_NAME SYSNAME, DATA_TYPE SMALLINT, TYPE_NAME SYSNAME, PRECISION INT, LENGTH INT, SCALE SMALLINT, RADIX SMALLINT, NULLABLE SMALLINT, REMARKS VARCHAR(254), COLUMN_DEF NVARCHAR(4000), SQL_DATA_TYPE SMALLINT, SQL_DATETIME_SUB SMALLINT, CHAR_OCTET_LENGTH INT, ORDINAL_POSITION INT, IS_NULLABLE VARCHAR(254), SS_IS_SPARSE SMALLINT, SS_IS_COLUMN_SET SMALLINT, SS_IS_COMPUTED SMALLINT, SS_IS_IDENTITY SMALLINT, SS_UDT_CATALOG_NAME NVARCHAR(128), SS_UDT_SCHEMA_NAME NVARCHAR(128), SS_UDT_ASSEMBLY_TYPE_NAME NVARCHAR(max), SS_XML_SCHEMACOLLECTION_CATALOG_NAME NVARCHAR(128), SS_XML_SCHEMACOLLECTION_SCHEMA_NAME NVARCHAR(128), SS_XML_SCHEMACOLLECTION_NAME NVARCHAR(128), SS_DATA_TYPE TINYINT);
INSERT INTO @mssqljdbc_temp_sp_columns_result EXEC sp_columns_170  'orders' , 'dbo' , 'master' , '%' , 2 , 3 ;
SELECT TABLE_QUALIFIER AS TABLE_CAT, TABLE_OWNER AS TABLE_SCHEM, TABLE_NAME, COLUMN_NAME, CAST(DATA_TYPE AS INT) AS DATA_TYPE, TYPE_NAME, PRECISION AS COLUMN_SIZE, LENGTH AS BUFFER_LENGTH, CAST(SCALE AS INT) AS DECIMAL_DIGITS, CAST(RADIX AS INT) AS NUM_PREC_RADIX, CAST(NULLABLE AS INT) AS NULLABLE, CAST(REMARKS AS VARCHAR) AS REMARKS, COLUMN_DEF, CAST(SQL_DATA_TYPE AS INT) AS SQL_DATA_TYPE, CAST(SQL_DATETIME_SUB AS INT) AS SQL_DATETIME_SUB, CHAR_OCTET_LENGTH, ORDINAL_POSITION, IS_NULLABLE, CAST(NULL AS VARCHAR) AS SCOPE_CATALOG, CAST(NULL AS VARCHAR) AS SCOPE_SCHEMA, CAST(NULL AS VARCHAR) AS SCOPE_TABLE, CAST(SS_DATA_TYPE AS SMALLINT) AS SOURCE_DATA_TYPE, CASE SS_IS_IDENTITY WHEN 0 THEN 'NO' WHEN 1 THEN 'YES' WHEN '' THEN '' END AS IS_AUTOINCREMENT, CASE SS_IS_COMPUTED WHEN 0 THEN 'NO' WHEN 1 THEN 'YES' WHEN '' THEN '' END AS IS_GENERATEDCOLUMN, SS_IS_SPARSE, SS_IS_COLUMN_SET, SS_UDT_CATALOG_NAME, SS_UDT_SCHEMA_NAME, SS_UDT_ASSEMBLY_TYPE_NAME, SS_XML_SCHEMACOLLECTION_CATALOG_NAME, SS_XML_SCHEMACOLLECTION_SCHEMA_NAME, SS_XML_SCHEMACOLLECTION_NAME FROM @mssqljdbc_temp_sp_columns_result ORDER BY TABLE_CAT, TABLE_SCHEM, TABLE_NAME, ORDINAL_POSITION;`

	resMssqlCols, err := coord.ExecuteSessionSQL("s1", qMssqlColumns)
	if err != nil {
		t.Fatalf("MSSQL columns batch failed: %v", err)
	}
	if len(resMssqlCols.Rows) != 3 {
		t.Fatalf("expected 3 columns for orders, got %d", len(resMssqlCols.Rows))
	}

	// 6. MSSQL sp_pkeys
	resPkeys, err := coord.ExecuteSessionSQL("s1", "EXEC sp_pkeys 'orders', 'dbo', NULL")
	if err != nil {
		t.Fatalf("sp_pkeys failed: %v", err)
	}
	if len(resPkeys.Rows) != 1 {
		t.Fatalf("expected 1 PK for orders, got %d", len(resPkeys.Rows))
	}
}

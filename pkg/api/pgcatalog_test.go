package api_test

import (
	"fmt"
	"testing"

	"sqlion/pkg/api"
	"sqlion/pkg/catalog"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
)

func setupCatalogTestServer(t *testing.T) (*api.Server, storage.Engine, *catalog.Catalog) {
	eng := memengine.New()
	_ = eng.Open("")
	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	// Create parent table: departments
	deptSchema := &storage.TableSchema{
		Name: "departments",
		Columns: []storage.Column{
			{Name: "dept_id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "dept_name", Type: storage.TypeVarChar, Nullable: false, Precision: 100},
		},
	}
	if err := eng.CreateTable(deptSchema); err != nil {
		t.Fatalf("failed to create departments table: %v", err)
	}

	// Create child table: employees (with PK, Unique index, FK)
	empSchema := &storage.TableSchema{
		Name: "employees",
		Columns: []storage.Column{
			{Name: "emp_id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "emp_name", Type: storage.TypeVarChar, Nullable: false, Precision: 50},
			{Name: "dept_id", Type: storage.TypeInt, Nullable: true},
			{Name: "salary", Type: storage.TypeDecimal, Precision: 10, Scale: 2},
			{Name: "active", Type: storage.TypeBool},
		},
		Indexes: []storage.IndexSchema{
			{Name: "uk_emp_name", TableName: "employees", Columns: []string{"emp_name"}, Unique: true},
		},
		ForeignKeys: []storage.ForeignKeySchema{
			{
				Name:          "fk_emp_dept",
				ChildTable:    "employees",
				ChildColumns:  []string{"dept_id"},
				ParentTable:   "departments",
				ParentColumns: []string{"dept_id"},
				OnDelete:      storage.ActionCascade,
				OnUpdate:      storage.ActionNoAction,
			},
		},
	}
	if err := eng.CreateTable(empSchema); err != nil {
		t.Fatalf("failed to create employees table: %v", err)
	}

	// Create a view in catalog
	_ = cat.CreateView(&catalog.View{
		Name:  "emp_view",
		Query: "SELECT emp_id, emp_name FROM employees",
	})

	// Create a procedure in catalog
	_ = cat.CreateProcedure(&catalog.Procedure{
		Name: "sp_get_dept",
		Body: "SELECT * FROM departments",
	})

	return server, eng, cat
}

func TestPgCatalog_InformationSchema_TablesAndColumns(t *testing.T) {
	server, eng, _ := setupCatalogTestServer(t)
	defer eng.Close()

	// 1. information_schema.tables
	res, err := server.ExecuteSQL("SELECT table_name, table_type FROM information_schema.tables ORDER BY table_name;")
	if err != nil || !res.Success {
		t.Fatalf("failed to query information_schema.tables: %v", err)
	}
	foundDept, foundEmp, foundView := false, false, false
	for _, row := range res.Rows {
		name := fmt.Sprintf("%v", row["table_name"])
		tblType := fmt.Sprintf("%v", row["table_type"])
		if name == "departments" && tblType == "BASE TABLE" {
			foundDept = true
		}
		if name == "employees" && tblType == "BASE TABLE" {
			foundEmp = true
		}
		if name == "emp_view" && tblType == "VIEW" {
			foundView = true
		}
	}
	if !foundDept || !foundEmp || !foundView {
		t.Errorf("expected departments, employees, and emp_view in information_schema.tables; got rows: %+v", res.Rows)
	}

	// 2. information_schema.columns
	res, err = server.ExecuteSQL("SELECT column_name, data_type, is_nullable FROM information_schema.columns WHERE table_name = 'employees' ORDER BY ordinal_position;")
	if err != nil || !res.Success {
		t.Fatalf("failed to query information_schema.columns: %v", err)
	}
	if len(res.Rows) != 5 {
		t.Fatalf("expected 5 columns for employees, got %d", len(res.Rows))
	}
	// Check emp_id
	if fmt.Sprintf("%v", res.Rows[0]["column_name"]) != "emp_id" || fmt.Sprintf("%v", res.Rows[0]["is_nullable"]) != "NO" {
		t.Errorf("unexpected emp_id column: %+v", res.Rows[0])
	}
	// Check emp_name
	if fmt.Sprintf("%v", res.Rows[1]["column_name"]) != "emp_name" || fmt.Sprintf("%v", res.Rows[1]["data_type"]) != "varchar" {
		t.Errorf("unexpected emp_name column: %+v", res.Rows[1])
	}
}

func TestPgCatalog_InformationSchema_Constraints(t *testing.T) {
	server, eng, _ := setupCatalogTestServer(t)
	defer eng.Close()

	// 1. information_schema.table_constraints
	res, err := server.ExecuteSQL("SELECT constraint_name, constraint_type, table_name FROM information_schema.table_constraints WHERE table_name = 'employees';")
	if err != nil || !res.Success {
		t.Fatalf("failed to query information_schema.table_constraints: %v", err)
	}
	foundPK, foundUK, foundFK := false, false, false
	for _, row := range res.Rows {
		cType := fmt.Sprintf("%v", row["constraint_type"])
		if cType == "PRIMARY KEY" {
			foundPK = true
		}
		if cType == "UNIQUE" {
			foundUK = true
		}
		if cType == "FOREIGN KEY" {
			foundFK = true
		}
	}
	if !foundPK || !foundUK || !foundFK {
		t.Errorf("expected PK, UK, FK for employees; got %+v", res.Rows)
	}

	// 2. information_schema.key_column_usage
	res, err = server.ExecuteSQL("SELECT column_name, constraint_name FROM information_schema.key_column_usage WHERE table_name = 'employees';")
	if err != nil || !res.Success || len(res.Rows) < 3 {
		t.Fatalf("failed to query information_schema.key_column_usage: %v, rows: %+v", err, res.Rows)
	}

	// 3. information_schema.referential_constraints
	res, err = server.ExecuteSQL("SELECT constraint_name, unique_constraint_name FROM information_schema.referential_constraints;")
	if err != nil || !res.Success || len(res.Rows) != 1 {
		t.Fatalf("failed to query referential_constraints: %v, rows: %+v", err, res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["constraint_name"]) != "fk_emp_dept" {
		t.Errorf("expected fk_emp_dept, got %v", res.Rows[0]["constraint_name"])
	}
}

func TestPgCatalog_CoreViews(t *testing.T) {
	server, eng, _ := setupCatalogTestServer(t)
	defer eng.Close()

	// 1. pg_class
	res, err := server.ExecuteSQL("SELECT relname, relkind FROM pg_catalog.pg_class WHERE relkind = 'r' ORDER BY relname;")
	if err != nil || !res.Success || len(res.Rows) < 2 {
		t.Fatalf("failed to query pg_class: %v, rows: %+v", err, res.Rows)
	}

	// 2. pg_attribute (atttypmod, atttypid)
	res, err = server.ExecuteSQL("SELECT attname, atttypid, atttypmod, attnotnull FROM pg_catalog.pg_attribute WHERE attname = 'emp_name';")
	if err != nil || !res.Success || len(res.Rows) != 1 {
		t.Fatalf("failed to query pg_attribute: %v, rows: %+v", err, res.Rows)
	}
	// varchar OID is 1043
	if fmt.Sprintf("%v", res.Rows[0]["atttypid"]) != "1043" {
		t.Errorf("expected varchar OID 1043, got %v", res.Rows[0]["atttypid"])
	}
	// atttypmod should be Precision (50) + 4 = 54
	if fmt.Sprintf("%v", res.Rows[0]["atttypmod"]) != "54" {
		t.Errorf("expected atttypmod 54, got %v", res.Rows[0]["atttypmod"])
	}

	// 3. pg_type
	res, err = server.ExecuteSQL("SELECT typname, oid FROM pg_catalog.pg_type WHERE typname = 'varchar';")
	if err != nil || !res.Success || len(res.Rows) != 1 {
		t.Fatalf("failed to query pg_type: %v, rows: %+v", err, res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["oid"]) != "1043" {
		t.Errorf("expected oid 1043, got %v", res.Rows[0]["oid"])
	}

	// 4. pg_constraint
	res, err = server.ExecuteSQL("SELECT conname, contype FROM pg_catalog.pg_constraint WHERE contype = 'f';")
	if err != nil || !res.Success || len(res.Rows) != 1 {
		t.Fatalf("failed to query pg_constraint for FK: %v, rows: %+v", err, res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["conname"]) != "fk_emp_dept" {
		t.Errorf("expected fk_emp_dept, got %v", res.Rows[0]["conname"])
	}

	// 5. pg_index
	res, err = server.ExecuteSQL("SELECT indisunique, indisprimary FROM pg_catalog.pg_index;")
	if err != nil || !res.Success || len(res.Rows) < 2 {
		t.Fatalf("failed to query pg_index: %v, rows: %+v", err, res.Rows)
	}

	// 6. pg_stat_user_tables
	res, err = server.ExecuteSQL("SELECT relname, seq_scan, n_tup_ins FROM pg_catalog.pg_stat_user_tables WHERE relname = 'employees';")
	if err != nil || !res.Success || len(res.Rows) != 1 {
		t.Fatalf("failed to query pg_stat_user_tables: %v, rows: %+v", err, res.Rows)
	}

	// 7. pg_proc
	res, err = server.ExecuteSQL("SELECT proname, prokind FROM pg_catalog.pg_proc WHERE proname = 'sp_get_dept';")
	if err != nil || !res.Success || len(res.Rows) != 1 {
		t.Fatalf("failed to query pg_proc: %v, rows: %+v", err, res.Rows)
	}

	// 8. Without schema prefix: e.g. FROM pg_class
	res, err = server.ExecuteSQL("SELECT relname FROM pg_class WHERE relname = 'departments';")
	if err != nil || !res.Success || len(res.Rows) != 1 {
		t.Fatalf("failed to query pg_class without prefix: %v, rows: %+v", err, res.Rows)
	}
}

func TestPgCatalog_HeidiSQLForeignKeyQuery(t *testing.T) {
	server, eng, _ := setupCatalogTestServer(t)
	defer eng.Close()

	heidiFKQuery := `SELECT
    con.conname AS constraint_name,
    CASE con.confupdtype
        WHEN 'a' THEN 'NO ACTION'
        WHEN 'r' THEN 'RESTRICT'
        WHEN 'c' THEN 'CASCADE'
        WHEN 'n' THEN 'SET NULL'
        WHEN 'd' THEN 'SET DEFAULT'
    END AS update_rule,
    CASE con.confdeltype
        WHEN 'a' THEN 'NO ACTION'
        WHEN 'r' THEN 'RESTRICT'
        WHEN 'c' THEN 'CASCADE'
        WHEN 'n' THEN 'SET NULL'
        WHEN 'd' THEN 'SET DEFAULT'
    END AS delete_rule,
    src_ns.nspname  AS table_schema,
    src_tbl.relname AS table_name,
    string_agg(src_col.attname, ',' ORDER BY ord.pos) AS columns,
    ref_ns.nspname  AS ref_schema,
    ref_tbl.relname AS ref_table,
    string_agg(ref_col.attname, ',' ORDER BY ord.pos) AS ref_columns,
    string_agg(ord.pos::text, ',' ORDER BY ord.pos)   AS ord_position
FROM pg_constraint con
JOIN pg_class      src_tbl ON src_tbl.oid = con.conrelid
JOIN pg_namespace  src_ns  ON src_ns.oid  = src_tbl.relnamespace
JOIN LATERAL unnest(con.conkey) WITH ORDINALITY AS ord(attnum, pos) ON TRUE
JOIN pg_attribute  src_col ON src_col.attrelid = src_tbl.oid AND src_col.attnum = ord.attnum
JOIN pg_class      ref_tbl ON ref_tbl.oid = con.confrelid
JOIN pg_namespace  ref_ns  ON ref_ns.oid  = ref_tbl.relnamespace
JOIN LATERAL unnest(con.confkey) WITH ORDINALITY AS ref_ord(attnum, pos)        ON ref_ord.pos = ord.pos
JOIN pg_attribute  ref_col ON ref_col.attrelid = ref_tbl.oid AND ref_col.attnum = ref_ord.attnum
WHERE
    con.contype = 'f'
    AND src_ns.nspname  = 'dbo'
    AND src_tbl.relname = 'employees'
GROUP BY
    con.conname,
    con.confupdtype,
    con.confdeltype,
    src_ns.nspname,
    src_tbl.relname,
    ref_ns.nspname,
    ref_tbl.relname
ORDER BY
    MIN(ord.pos)`

	res, err := server.ExecuteSQL(heidiFKQuery)
	if err != nil {
		t.Fatalf("HeidiSQL FK query failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("HeidiSQL FK query was not successful")
	}

	expectedCols := []string{
		"constraint_name", "update_rule", "delete_rule", "table_schema", "table_name",
		"columns", "ref_schema", "ref_table", "ref_columns", "ord_position",
	}
	if len(res.Columns) != len(expectedCols) {
		t.Fatalf("expected %d columns, got %d: %+v", len(expectedCols), len(res.Columns), res.Columns)
	}
	for i, col := range expectedCols {
		if res.Columns[i] != col {
			t.Errorf("column %d: expected %s, got %s", i, col, res.Columns[i])
		}
	}

	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 FK row for employees, got %d: %+v", len(res.Rows), res.Rows)
	}
	row := res.Rows[0]
	if fmt.Sprintf("%v", row["constraint_name"]) != "fk_emp_dept" {
		t.Errorf("expected fk_emp_dept, got %v", row["constraint_name"])
	}
	if fmt.Sprintf("%v", row["columns"]) != "dept_id" {
		t.Errorf("expected dept_id column, got %v", row["columns"])
	}
	if fmt.Sprintf("%v", row["ref_table"]) != "departments" {
		t.Errorf("expected departments ref_table, got %v", row["ref_table"])
	}
}

func TestPgCatalog_BeekeeperStudioTableListQuery(t *testing.T) {
	server, _, _ := setupCatalogTestServer(t)

	beekeeperQuery := `
SELECT
  t.table_schema as schema,
  t.table_name as name,
  pc.relkind as tabletype,
  parent_pc.relkind as parenttype
FROM information_schema.tables AS t
JOIN pg_class AS pc
  ON t.table_name = pc.relname AND quote_ident(t.table_schema) = pc.relnamespace::regnamespace::text
LEFT OUTER JOIN pg_inherits AS i
  ON pc.oid = i.inhrelid
LEFT OUTER JOIN pg_class AS parent_pc
  ON parent_pc.oid = i.inhparent
WHERE t.table_type NOT LIKE '%VIEW%'
ORDER BY t.table_schema, t.table_name`

	res, err := server.ExecuteSQL(beekeeperQuery)
	if err != nil {
		t.Fatalf("Beekeeper table list query failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("Beekeeper table list query was not successful")
	}

	expectedCols := []string{"schema", "name", "tabletype", "parenttype"}
	if len(res.Columns) != len(expectedCols) {
		t.Fatalf("expected columns %v, got %v", expectedCols, res.Columns)
	}

	// Should contain departments and employees
	foundDept := false
	foundEmp := false
	for _, row := range res.Rows {
		name := fmt.Sprintf("%v", row["name"])
		if name == "departments" {
			foundDept = true
		} else if name == "employees" {
			foundEmp = true
		}
	}

	if !foundDept || !foundEmp {
		t.Errorf("expected both departments and employees in Beekeeper table list, got rows: %+v", res.Rows)
	}
}

func TestPgCatalog_BeekeeperStudioMSSQLColumnQuery(t *testing.T) {
	server, _, _ := setupCatalogTestServer(t)

	// Beekeeper Studio SQL Server column query
	sql := `SELECT
  table_schema as "table_schema",
  table_name as "table_name",
  column_name as "column_name",
  ordinal_position as "ordinal_position",
  column_default as "column_default",
  ic.is_nullable as "is_nullable",
  CASE
    WHEN sc.definition is not null THEN 'YES'
    ELSE 'NO'
  END as "is_generated",
  CASE
    WHEN character_maximum_length is not null AND data_type != 'text'
        THEN data_type + '(' + CAST(character_maximum_length AS VARCHAR(16)) + ')'
    WHEN numeric_precision is not null
        THEN data_type + '(' + CAST(numeric_precision AS VARCHAR(16)) + ')'
    WHEN datetime_precision is not null AND data_type != 'date'
        THEN data_type + '(' + CAST(datetime_precision AS VARCHAR(16)) + ')'
    ELSE data_type
  END as "data_type"
FROM INFORMATION_SCHEMA.COLUMNS ic
LEFT JOIN sys.computed_columns sc ON
  OBJECT_ID(QUOTENAME(ic.TABLE_SCHEMA) + '.' + QUOTENAME(ic.TABLE_NAME)) = sc.object_id AND
  ic.COLUMN_NAME = sc.name
WHERE table_name = 'employees' AND table_schema = 'dbo'
ORDER BY table_schema, table_name, ordinal_position;`

	res, err := server.ExecuteSQL(sql)
	if err != nil {
		t.Fatalf("failed to execute Beekeeper MSSQL column query: %v", err)
	}
	if !res.Success {
		t.Fatalf("Beekeeper MSSQL column query was not successful")
	}

	expectedCols := []string{
		"table_schema", "table_name", "column_name", "ordinal_position",
		"column_default", "is_nullable", "is_generated", "data_type",
	}
	if len(res.Columns) != len(expectedCols) {
		t.Fatalf("expected columns %v, got %v", expectedCols, res.Columns)
	}

	if len(res.Rows) == 0 {
		t.Fatalf("expected column rows for employees table, got 0")
	}

	colNames := make([]string, len(res.Rows))
	for i, r := range res.Rows {
		colNames[i] = fmt.Sprintf("%v", r["column_name"])
	}
	if colNames[0] != "emp_id" || colNames[1] != "emp_name" {
		t.Errorf("unexpected columns: %v", colNames)
	}
}

func TestMSSQL_PaginationAndBrackets(t *testing.T) {
	server, _, _ := setupCatalogTestServer(t)

	// Seed rows into departments and employees
	_, err := server.ExecuteSQL("INSERT INTO departments (dept_id, dept_name) VALUES (10, 'Engineering');")
	if err != nil {
		t.Fatalf("failed to insert department: %v", err)
	}
	_, err = server.ExecuteSQL("INSERT INTO employees (emp_id, emp_name, dept_id, salary, active) VALUES (1, 'Alice', 10, 50000.0, true);")
	if err != nil {
		t.Fatalf("failed to insert: %v", err)
	}
	_, err = server.ExecuteSQL("INSERT INTO employees (emp_id, emp_name, dept_id, salary, active) VALUES (2, 'Bob', 10, 60000.0, true);")
	if err != nil {
		t.Fatalf("failed to insert: %v", err)
	}

	// 1. SELECT * FROM [dbo].[employees] OFFSET 0 ROWS FETCH NEXT 100 ROWS ONLY
	res, err := server.ExecuteSQL("SELECT * FROM [dbo].[employees] OFFSET 0 ROWS FETCH NEXT 100 ROWS ONLY;")
	if err != nil {
		t.Fatalf("failed OFFSET FETCH query: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Errorf("expected 2 rows, got %d", len(res.Rows))
	}

	// 2. SELECT TOP 1 * FROM [dbo].[employees]
	resTop, err := server.ExecuteSQL("SELECT TOP 1 * FROM [dbo].[employees];")
	if err != nil {
		t.Fatalf("failed TOP 1 query: %v", err)
	}
	if len(resTop.Rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(resTop.Rows))
	}
}

func TestPgCatalog_DbGatePostgresIntrospection(t *testing.T) {
	server, eng, _ := setupCatalogTestServer(t)
	defer eng.Close()

	// 1. DbGate tableList.js query
	tableListQuery := `SELECT
		n.nspname AS "schema_name",
		c.relname AS "pure_name",
		pg_relation_size(c.oid) AS "size_bytes"
	FROM pg_class c JOIN pg_namespace n ON c.relnamespace = n.oid
	WHERE c.relkind IN ('r', 'p') AND n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
	ORDER BY n.nspname, c.relname;`

	res, err := server.ExecuteSQL(tableListQuery)
	if err != nil || !res.Success {
		t.Fatalf("DbGate tableList query failed: %v", err)
	}
	foundDept, foundEmp := false, false
	for _, row := range res.Rows {
		pureName := fmt.Sprintf("%v", row["pure_name"])
		schemaName := fmt.Sprintf("%v", row["schema_name"])
		if pureName == "departments" && schemaName == "public" {
			foundDept = true
		}
		if pureName == "employees" && schemaName == "public" {
			foundEmp = true
		}
	}
	if !foundDept || !foundEmp {
		t.Fatalf("DbGate tableList missing tables, rows: %+v", res.Rows)
	}

	// 2. DbGate columns.js query
	colQuery := `SELECT
		n.nspname AS "schema_name",
		c.relname AS "pure_name",
		c.oid AS "postgres_table_id",
		a.attname AS "column_name",
		a.attnum AS "postgres_column_id",
		CASE WHEN a.attnotnull THEN 'NO' ELSE 'YES' END AS "is_nullable"
	FROM pg_attribute a JOIN pg_class c ON a.attrelid = c.oid JOIN pg_namespace n ON c.relnamespace = n.oid
	WHERE c.relname = 'employees';`

	colRes, err := server.ExecuteSQL(colQuery)
	if err != nil || !colRes.Success {
		t.Fatalf("DbGate columns query failed: %v", err)
	}
	if len(colRes.Rows) == 0 {
		t.Fatalf("DbGate columns returned 0 rows for employees")
	}
	if colRes.Rows[0]["pure_name"] != "employees" || colRes.Rows[0]["column_name"] != "emp_id" {
		t.Errorf("unexpected first column in DbGate columns: %+v", colRes.Rows[0])
	}

	// 3. DbGate primaryKeys.js query
	pkQuery := `SELECT
		n.nspname AS "constraint_schema",
		c.conname AS "constraint_name",
		n.nspname AS "schema_name",
		t.relname AS "pure_name",
		a.attname AS "column_name"
	FROM pg_catalog.pg_constraint AS c
	JOIN pg_catalog.pg_class AS t ON t.oid = c.conrelid
	JOIN pg_catalog.pg_namespace AS n ON n.oid = t.relnamespace
	JOIN LATERAL unnest(c.conkey) AS u(attnum) ON TRUE;`

	pkRes, err := server.ExecuteSQL(pkQuery)
	if err != nil || !pkRes.Success {
		t.Fatalf("DbGate primaryKeys query failed: %v", err)
	}
	foundEmpPK := false
	for _, row := range pkRes.Rows {
		if row["pure_name"] == "employees" && row["column_name"] == "emp_id" {
			foundEmpPK = true
			break
		}
	}
	if !foundEmpPK {
		t.Fatalf("DbGate primaryKeys did not find employees PK, rows: %+v", pkRes.Rows)
	}

	// 4. DbGate views.js CTE query
	viewQuery := `WITH view_defs AS (
		SELECT
			c.relname AS pure_name,
			n.nspname AS schema_name,
			pg_get_viewdef(c.oid, true) AS viewdef
		FROM pg_catalog.pg_class c
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'v'
	) SELECT * FROM view_defs;`

	viewRes, err := server.ExecuteSQL(viewQuery)
	if err != nil || !viewRes.Success {
		t.Fatalf("DbGate views query failed: %v", err)
	}
	if len(viewRes.Rows) == 0 || viewRes.Rows[0]["pure_name"] != "emp_view" {
		t.Fatalf("DbGate views query unexpected result: %+v", viewRes.Rows)
	}

	// 5. DbGate indexes.js query
	idxQuery := `SELECT
		t.relname AS "table_name",
		c.nspname AS "schema_name",
		i.relname AS "index_name",
		ix.indisprimary AS "is_primary",
		ix.indisunique AS "is_unique"
	FROM pg_class t
	JOIN pg_index ix ON t.oid = ix.indrelid
	JOIN pg_class i ON i.oid = ix.indexrelid
	JOIN pg_namespace c ON c.oid = t.relnamespace;`

	idxRes, err := server.ExecuteSQL(idxQuery)
	if err != nil || !idxRes.Success {
		t.Fatalf("DbGate indexes query failed: %v", err)
	}
	if len(idxRes.Rows) == 0 {
		t.Fatalf("DbGate indexes returned 0 rows")
	}

	// 6. Scalar queries: CURRENT_SCHEMA() and CURRENT_SETTING(...)
	scalarRes, err := server.ExecuteSQL("SELECT CURRENT_SCHEMA(), CURRENT_SETTING('server_version'), CURRENT_SETTING('search_path');")
	if err != nil || !scalarRes.Success {
		t.Fatalf("scalar functions failed: %v", err)
	}
	if len(scalarRes.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(scalarRes.Rows))
	}
	if scalarRes.Rows[0]["current_schema"] != "dbo" {
		t.Errorf("expected current_schema = 'dbo', got %v", scalarRes.Rows[0]["current_schema"])
	}
	if scalarRes.Rows[0]["current_setting"] != "public" && scalarRes.Rows[0]["current_setting"] != "15.0" {
		t.Errorf("unexpected current_setting result: %+v", scalarRes.Rows[0])
	}
}

func TestPgCatalog_DbGateMSSQLIntrospection(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	if _, err := server.ExecuteSQL("CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(50), balance INT);"); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	// 1. DbGate MSSQL column query
	colQuery := `select c.name as columnName, t.name as dataType, c.object_id as objectId, c.is_identity as isIdentity,
    c.max_length as maxLength, c.precision, c.scale, c.is_nullable as isNullable,
    col.CHARACTER_MAXIMUM_LENGTH as charMaxLength,
    d.definition as defaultValue, d.name as defaultConstraint,
    m.definition as computedExpression, m.is_persisted as isPersisted, c.column_id as columnId, 
    col.NUMERIC_PRECISION as numericPrecision,
    col.NUMERIC_SCALE as numericScale,
    c.is_sparse as isSparse,
    ep.value as columnComment
from sys.columns c
inner join sys.types t on c.system_type_id = t.system_type_id and c.user_type_id = t.user_type_id
inner join sys.objects o on c.object_id = o.object_id
INNER JOIN sys.schemas u ON u.schema_id=o.schema_id 
INNER JOIN INFORMATION_SCHEMA.COLUMNS col ON col.TABLE_NAME = o.name AND col.TABLE_SCHEMA = u.name and col.COLUMN_NAME = c.name
left join sys.default_constraints d on c.default_object_id = d.object_id
left join sys.computed_columns m on m.object_id = c.object_id and m.column_id = c.column_id
left join sys.extended_properties ep on ep.major_id = c.object_id 
    and ep.minor_id = c.column_id 
    and ep.name = 'MS_Description'
    and ep.class = 1
where o.type = 'U' and o.object_id in ('1000') and u.name is not null 
order by c.column_id`

	colRes, err := server.ExecuteSQL(colQuery)
	if err != nil || !colRes.Success {
		t.Fatalf("DbGate MSSQL column query failed: %v", err)
	}
	if len(colRes.Rows) != 3 {
		t.Fatalf("expected 3 columns for users, got %d", len(colRes.Rows))
	}
	if colRes.Rows[0]["columnName"] != "id" || colRes.Rows[0]["dataType"] != "int" {
		t.Fatalf("unexpected col 0: %+v", colRes.Rows[0])
	}
	if colRes.Rows[1]["columnName"] != "name" || colRes.Rows[1]["dataType"] != "varchar" {
		t.Fatalf("unexpected col 1: %+v", colRes.Rows[1])
	}
	if colRes.Rows[2]["columnName"] != "balance" || colRes.Rows[2]["dataType"] != "int" {
		t.Fatalf("unexpected col 2: %+v", colRes.Rows[2])
	}

	// 2. DbGate MSSQL Primary Key query
	pkQuery := `SELECT 
    i.object_id AS objectId,
    o.name AS pureName,
    s.name AS schemaName,
    c.name AS columnName,
    i.name AS constraintName
FROM 
    sys.indexes i
INNER JOIN 
    sys.index_columns ic ON i.object_id = ic.object_id AND i.index_id = ic.index_id
INNER JOIN 
    sys.columns c ON ic.object_id = c.object_id AND ic.column_id = c.column_id
INNER JOIN
    sys.objects o ON i.object_id = o.object_id
INNER JOIN
    sys.schemas s ON o.schema_id = s.schema_id
WHERE 
    i.is_primary_key = 1
	and o.object_id in ('1000')
    and s.name is not null 
ORDER BY 
    ic.key_ordinal`

	pkRes, err := server.ExecuteSQL(pkQuery)
	if err != nil || !pkRes.Success {
		t.Fatalf("DbGate MSSQL PK query failed: %v", err)
	}
	if len(pkRes.Rows) != 1 || pkRes.Rows[0]["columnName"] != "id" {
		t.Fatalf("expected 1 PK row with columnName 'id', got %+v", pkRes.Rows)
	}

	// 3. DbGate MSSQL modifications query (sys.objects + sys.schemas)
	modQuery := `select o.object_id as objectId, o.modify_date as modifyDate, o.type, o.name as pureName, s.name as schemaName
from sys.objects o
inner join sys.schemas s on o.schema_id = s.schema_id
where o.type in ('U', 'V', 'P', 'IF', 'FN', 'TF', 'TR')
    and s.name  is not null`
	modRes, err := server.ExecuteSQL(modQuery)
	if err != nil || !modRes.Success {
		t.Fatalf("DbGate MSSQL modifications query failed: %v", err)
	}
	if len(modRes.Rows) != 1 {
		t.Fatalf("expected 1 object row, got %d", len(modRes.Rows))
	}
	if modRes.Rows[0]["pureName"] != "users" || modRes.Rows[0]["schemaName"] != "dbo" || modRes.Rows[0]["type"] != "U" {
		t.Fatalf("unexpected mod row: %+v", modRes.Rows[0])
	}
	if modRes.Rows[0]["objectId"] != 1000 {
		t.Fatalf("expected objectId 1000, got %v", modRes.Rows[0]["objectId"])
	}

	// 4. DbGate MSSQL tables query (sys.tables + sys.schemas)
	tblQuery := `select
	o.name as pureName, 
	s.name as schemaName, 
	o.object_id as objectId,
	o.create_date as createDate, 
	o.modify_date as modifyDate,
	ep.value as objectComment
from sys.tables o
inner join sys.schemas s on o.schema_id = s.schema_id
left join sys.extended_properties ep on ep.major_id = o.object_id and ep.minor_id = 0 and ep.name = 'MS_Description'`
	tblRes, err := server.ExecuteSQL(tblQuery)
	if err != nil || !tblRes.Success {
		t.Fatalf("DbGate MSSQL tables query failed: %v", err)
	}
	if len(tblRes.Rows) != 1 || tblRes.Rows[0]["pureName"] != "users" || tblRes.Rows[0]["schemaName"] != "dbo" {
		t.Fatalf("unexpected tbl row: %+v", tblRes.Rows)
	}

	// 5. DbGate MSSQL table row count query
	cntQuery := `SELECT distinct
    t.object_id as objectId,
    p.rows AS tableRowCount
FROM
    sys.tables t
INNER JOIN
    sys.indexes i ON t.OBJECT_ID = i.object_id
INNER JOIN
    sys.partitions p ON i.object_id = p.OBJECT_ID AND i.index_id = p.index_id
INNER JOIN
    sys.schemas s ON t.schema_id = s.schema_id
WHERE
    t.NAME NOT LIKE 'dt%'
    AND t.is_ms_shipped = 0
    AND i.OBJECT_ID > 255
    AND s.name  is not null`
	cntRes, err := server.ExecuteSQL(cntQuery)
	if err != nil || !cntRes.Success {
		t.Fatalf("DbGate MSSQL row count query failed: %v", err)
	}
	if len(cntRes.Rows) != 1 || cntRes.Rows[0]["objectId"] != 1000 {
		t.Fatalf("unexpected cnt row: %+v", cntRes.Rows)
	}

	// 6. DbGate MSSQL column comments query
	commentQuery := `select c.object_id as objectId,
	ep.value as columnComment,
  c.column_id as columnId
from sys.columns c
inner join sys.objects o on c.object_id = o.object_id
INNER JOIN sys.schemas u ON u.schema_id=o.schema_id
INNER JOIN sys.extended_properties ep on ep.major_id = c.object_id and ep.minor_id = c.column_id and ep.name = 'MS_Description'
where o.type IN ('U', 'V') and o.object_id  is not null and u.name  is not null
order by c.column_id`
	commentRes, err := server.ExecuteSQL(commentQuery)
	if err != nil || !commentRes.Success {
		t.Fatalf("DbGate MSSQL column comments query failed: %v", err)
	}
	if len(commentRes.Columns) != 3 || commentRes.Columns[0] != "objectId" {
		t.Fatalf("unexpected comment columns: %+v", commentRes.Columns)
	}

	// 7. Verify dynamic row count after inserting rows
	if _, err := server.ExecuteSQL("INSERT INTO users (id, name, balance) VALUES (1, 'Alice', 100);"); err != nil {
		t.Fatalf("failed to insert row 1: %v", err)
	}
	if _, err := server.ExecuteSQL("INSERT INTO users (id, name, balance) VALUES (2, 'Bob', 200);"); err != nil {
		t.Fatalf("failed to insert row 2: %v", err)
	}
	cntRes2, err := server.ExecuteSQL(cntQuery)
	if err != nil || !cntRes2.Success {
		t.Fatalf("DbGate MSSQL row count query 2 failed: %v", err)
	}
	if len(cntRes2.Rows) != 1 || cntRes2.Rows[0]["tableRowCount"] != int64(2) {
		t.Fatalf("expected tableRowCount 2, got: %+v", cntRes2.Rows)
	}

	// 8. DbGate single table ID lookup: OBJECT_ID('[dbo].[users]')
	objIDRes, err := server.ExecuteSQL("SELECT OBJECT_ID('[dbo].[users]') AS id")
	if err != nil || !objIDRes.Success {
		t.Fatalf("OBJECT_ID query failed: %v", err)
	}
	if len(objIDRes.Rows) != 1 || objIDRes.Rows[0]["id"] != int64(1000) {
		t.Fatalf("expected id 1000, got: %+v", objIDRes.Rows)
	}

	// 9. OBJECT_NAME(1000)
	objNameRes, err := server.ExecuteSQL("SELECT OBJECT_NAME(1000) AS name")
	if err != nil || !objNameRes.Success {
		t.Fatalf("OBJECT_NAME query failed: %v", err)
	}
	if len(objNameRes.Rows) != 1 || objNameRes.Rows[0]["name"] != "users" {
		t.Fatalf("expected 'users', got: %+v", objNameRes.Rows)
	}

	// 10. QUOTENAME('users')
	quoteRes, err := server.ExecuteSQL("SELECT QUOTENAME('users') AS q")
	if err != nil || !quoteRes.Success {
		t.Fatalf("QUOTENAME query failed: %v", err)
	}
	if len(quoteRes.Rows) != 1 || quoteRes.Rows[0]["q"] != "[users]" {
		t.Fatalf("expected '[users]', got: %+v", quoteRes.Rows)
	}
}

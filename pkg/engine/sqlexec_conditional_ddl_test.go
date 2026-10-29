package engine

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestConditionalDDL_GiteaXORMPattern(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	// 1. Initial table creation via XORM migration pattern
	q1 := `IF NOT EXISTS (SELECT [name] FROM sysobjects WHERE [name] = 'gitea_user' AND xtype = 'U')
CREATE TABLE [gitea_user] (
	id BIGINT PRIMARY KEY,
	name VARCHAR(50),
	email VARCHAR(100)
)`
	res1, err := coord.ExecuteSQL(q1)
	if err != nil {
		t.Fatalf("first conditional CREATE TABLE failed: %v", err)
	}
	if res1 == nil || !res1.Success {
		t.Fatalf("expected successful CREATE TABLE, got: %+v", res1)
	}

	// Verify table was created in storage engine
	tbl, err := eng.GetTable("gitea_user")
	if err != nil || tbl == nil {
		t.Fatalf("table gitea_user was not created in engine: %v", err)
	}

	// Insert a test row to verify table usability
	insertRes, err := coord.ExecuteSQL("INSERT INTO [gitea_user] (id, name, email) VALUES (1, 'gitea', 'admin@example.com')")
	if err != nil || !insertRes.Success {
		t.Fatalf("INSERT into gitea_user failed: %v", err)
	}

	// 2. Second execution of the exact same query (idempotent no-op)
	res2, err := coord.ExecuteSQL(q1)
	if err != nil {
		t.Fatalf("second conditional CREATE TABLE failed: %v", err)
	}
	if res2 == nil || !res2.Success {
		t.Fatalf("expected successful no-op on duplicate CREATE TABLE, got: %+v", res2)
	}

	// 3. Conditional DROP TABLE when table exists
	qDrop := `IF EXISTS (SELECT * FROM sysobjects WHERE name = 'gitea_user' AND xtype = 'U')
DROP TABLE [gitea_user]`
	resDrop1, err := coord.ExecuteSQL(qDrop)
	if err != nil {
		t.Fatalf("conditional DROP TABLE failed: %v", err)
	}
	if resDrop1 == nil || !resDrop1.Success {
		t.Fatalf("expected successful DROP TABLE, got: %+v", resDrop1)
	}

	// Verify table is dropped
	tblAfter, _ := eng.GetTable("gitea_user")
	if tblAfter != nil {
		t.Fatalf("table gitea_user should have been dropped")
	}

	// 4. Second DROP TABLE when table does NOT exist (idempotent no-op)
	resDrop2, err := coord.ExecuteSQL(qDrop)
	if err != nil {
		t.Fatalf("second conditional DROP TABLE failed: %v", err)
	}
	if resDrop2 == nil || !resDrop2.Success {
		t.Fatalf("expected successful no-op on duplicate DROP TABLE, got: %+v", resDrop2)
	}
}

func TestConditionalDDL_ObjectIDPattern(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	// 1. OBJECT_ID IS NULL pattern
	q1 := `IF OBJECT_ID('gitea_repo') IS NULL
CREATE TABLE [gitea_repo] (
	id INT PRIMARY KEY,
	name VARCHAR(50)
)`
	res1, err := coord.ExecuteSQL(q1)
	if err != nil {
		t.Fatalf("conditional CREATE TABLE with OBJECT_ID failed: %v", err)
	}
	if res1 == nil || !res1.Success {
		t.Fatalf("expected successful CREATE TABLE, got: %+v", res1)
	}

	// Verify table created
	tbl, err := eng.GetTable("gitea_repo")
	if err != nil || tbl == nil {
		t.Fatalf("table gitea_repo was not created: %v", err)
	}

	// 2. Second run when table exists (no-op)
	res2, err := coord.ExecuteSQL(q1)
	if err != nil {
		t.Fatalf("second OBJECT_ID CREATE TABLE failed: %v", err)
	}
	if res2 == nil || !res2.Success {
		t.Fatalf("expected successful no-op, got: %+v", res2)
	}

	// 3. OBJECT_ID IS NOT NULL pattern for DROP
	qDrop := `IF OBJECT_ID('gitea_repo') IS NOT NULL
DROP TABLE [gitea_repo]`
	resDrop, err := coord.ExecuteSQL(qDrop)
	if err != nil {
		t.Fatalf("conditional DROP TABLE with OBJECT_ID failed: %v", err)
	}
	if resDrop == nil || !resDrop.Success {
		t.Fatalf("expected successful DROP TABLE, got: %+v", resDrop)
	}

	// Verify dropped
	tblAfter, _ := eng.GetTable("gitea_repo")
	if tblAfter != nil {
		t.Fatalf("table gitea_repo should have been dropped")
	}
}

func TestConditionalDDL_BeginEndWrappedBlock(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	q := `IF NOT EXISTS (SELECT 1 FROM sysobjects WHERE [name] = 'org' AND xtype = 'U')
BEGIN
	CREATE TABLE [org] (
		id INT PRIMARY KEY,
		org_name VARCHAR(50)
	);
END`

	res, err := coord.ExecuteSQL(q)
	if err != nil {
		t.Fatalf("BEGIN...END wrapped conditional DDL failed: %v", err)
	}
	if res == nil || !res.Success {
		t.Fatalf("expected successful execution, got: %+v", res)
	}

	tbl, err := eng.GetTable("org")
	if err != nil || tbl == nil {
		t.Fatalf("table org was not created: %v", err)
	}
}

func TestConditionalDDL_ElseBranch(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	// Initial table
	_, err := coord.ExecuteSQL("CREATE TABLE master_tbl (id INT PRIMARY KEY)")
	if err != nil {
		t.Fatalf("CREATE master_tbl failed: %v", err)
	}

	// Condition evaluates to false (table exists), so ELSE branch executes
	q := `IF NOT EXISTS (SELECT 1 FROM sysobjects WHERE [name] = 'master_tbl' AND xtype = 'U')
CREATE TABLE should_not_exist (id INT)
ELSE
CREATE TABLE fallback_tbl (id INT PRIMARY KEY)`

	res, err := coord.ExecuteSQL(q)
	if err != nil {
		t.Fatalf("conditional DDL with ELSE failed: %v", err)
	}
	if res == nil || !res.Success {
		t.Fatalf("expected success, got: %+v", res)
	}

	if tbl, _ := eng.GetTable("should_not_exist"); tbl != nil {
		t.Fatalf("should_not_exist was created when condition was false")
	}
	if tbl, err := eng.GetTable("fallback_tbl"); err != nil || tbl == nil {
		t.Fatalf("fallback_tbl was not created: %v", err)
	}
}

func TestConditionalDDL_FallbackToProceduralIF(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	// Normal procedural IF without DDL body should fall through to anonymous block
	q := `IF 1 = 1
BEGIN
	PRINT 'procedural executed';
END`

	res, err := coord.ExecuteSQL(q)
	if err != nil {
		t.Fatalf("procedural IF failed: %v", err)
	}
	if res == nil || !res.Success {
		t.Fatalf("expected successful execution, got: %+v", res)
	}
}

func TestXORM_CollationAndIntrospectionE2E(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	// 1. Create a table
	_, err := coord.ExecuteSQL("CREATE TABLE [users] (id BIGINT PRIMARY KEY, name VARCHAR(50))")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 2. ALTER DATABASE CURRENT COLLATE (tolerant no-op)
	alterRes, err := coord.ExecuteSQL("ALTER DATABASE CURRENT COLLATE Latin1_General_CS_AS")
	if err != nil || !alterRes.Success {
		t.Fatalf("ALTER DATABASE CURRENT COLLATE failed: %v, res: %+v", err, alterRes)
	}

	// 3. fn_helpcollations() query
	collRes, err := coord.ExecuteSQL("SELECT * FROM sys.fn_helpcollations() WHERE name LIKE '%[_]CS[_]AS%'")
	if err != nil || !collRes.Success {
		t.Fatalf("fn_helpcollations query failed: %v", err)
	}
	if len(collRes.Rows) == 0 {
		t.Fatalf("expected at least 1 collation row, got 0")
	}

	// 4. Exact XORM GetColumns() query
	qGetCols := `select a.name as name, b.name as ctype,a.max_length,a.precision,a.scale,a.is_nullable as nullable,
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
          where a.object_id=object_id('users')`

	colsRes, err := coord.ExecuteSQL(qGetCols)
	if err != nil || !colsRes.Success {
		t.Fatalf("XORM GetColumns query failed: %v", err)
	}
	if len(colsRes.Rows) != 2 {
		t.Fatalf("expected 2 columns for users, got %d", len(colsRes.Rows))
	}
	if colsRes.Columns[6] != "default_is_null" {
		t.Fatalf("expected column index 6 to be 'default_is_null', got %q", colsRes.Columns[6])
	}
	for i, r := range colsRes.Rows {
		if r["default_is_null"] != true {
			t.Fatalf("row %d: expected default_is_null=true, got %v (%T)", i, r["default_is_null"], r["default_is_null"])
		}
	}
}

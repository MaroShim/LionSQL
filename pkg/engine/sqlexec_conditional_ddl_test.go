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

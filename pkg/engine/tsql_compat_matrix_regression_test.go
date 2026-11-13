package engine_test

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func newCompatMatrixTestCoord(t *testing.T) (*engine.Coordinator, func()) {
	t.Helper()
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	cleanup := func() {
		coord.Close()
		eng.Close()
	}
	return coord, cleanup
}

func TestTSQLCompatMatrix_InlineTVF(t *testing.T) {
	coord, cleanup := newCompatMatrixTestCoord(t)
	defer cleanup()

	setupQueries := []string{
		"CREATE TABLE compat_items (id INT PRIMARY KEY, name VARCHAR(50), price INT, active INT);",
		"INSERT INTO compat_items VALUES (1, 'Pen', 10, 1), (2, 'Notebook', 50, 1), (3, 'Monitor', 300, 0), (4, 'Keyboard', 120, 1);",
		"CREATE FUNCTION fn_ActiveItems() RETURNS TABLE AS RETURN (SELECT id, name FROM compat_items WHERE active = 1 ORDER BY id ASC);",
		"CREATE FUNCTION dbo.fn_ExpensiveItems(@minPrice INT) RETURNS TABLE AS RETURN (SELECT id, name, price FROM compat_items WHERE price >= @minPrice ORDER BY id ASC);",
	}
	for _, q := range setupQueries {
		if _, err := coord.ExecuteSQL(q); err != nil {
			t.Fatalf("setup query %q failed: %v", q, err)
		}
	}

	// 1. Parameterless inline TVF
	res, err := coord.ExecuteSQL("SELECT * FROM fn_ActiveItems();")
	if err != nil {
		t.Fatalf("SELECT from fn_ActiveItems() failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 active items from fn_ActiveItems(), got %d (%+v)", len(res.Rows), res.Rows)
	}
	if res.Rows[0]["name"] != "Pen" || res.Rows[2]["name"] != "Keyboard" {
		t.Fatalf("unexpected fn_ActiveItems() rows: %+v", res.Rows)
	}

	// 2. Parameterized inline TVF (both unqualified and dbo-qualified)
	for _, q := range []string{
		"SELECT * FROM fn_ExpensiveItems(100);",
		"SELECT * FROM dbo.fn_ExpensiveItems(100);",
	} {
		res, err = coord.ExecuteSQL(q)
		if err != nil {
			t.Fatalf("%q failed: %v", q, err)
		}
		if len(res.Rows) != 2 {
			t.Fatalf("%q expected 2 rows, got %d (%+v)", q, len(res.Rows), res.Rows)
		}
		if res.Rows[0]["name"] != "Monitor" || res.Rows[1]["name"] != "Keyboard" {
			t.Fatalf("%q unexpected rows: %+v", q, res.Rows)
		}
	}

	// 3. DROP FUNCTION removes TVF from catalog
	if _, err := coord.ExecuteSQL("DROP FUNCTION dbo.fn_ExpensiveItems;"); err != nil {
		t.Fatalf("DROP FUNCTION failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("SELECT * FROM fn_ExpensiveItems(100);"); err == nil {
		t.Fatalf("expected error querying dropped TVF fn_ExpensiveItems")
	}
}

func TestTSQLCompatMatrix_UseDatabaseAndSessionContext(t *testing.T) {
	coord, cleanup := newCompatMatrixTestCoord(t)
	defer cleanup()

	if _, err := coord.ExecuteSessionSQL("sess-a", "USE [SalesDB];"); err != nil {
		t.Fatalf("sess-a USE [SalesDB] failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL("sess-b", "USE HRDB;"); err != nil {
		t.Fatalf("sess-b USE HRDB failed: %v", err)
	}

	resA, err := coord.ExecuteSessionSQL("sess-a", "SELECT DB_NAME() AS db;")
	if err != nil || len(resA.Rows) != 1 || resA.Rows[0]["db"] != "SalesDB" {
		t.Fatalf("sess-a expected DB_NAME()=SalesDB, got err=%v rows=%+v", err, resA)
	}

	resB, err := coord.ExecuteSessionSQL("sess-b", "SELECT DB_NAME() AS db;")
	if err != nil || len(resB.Rows) != 1 || resB.Rows[0]["db"] != "HRDB" {
		t.Fatalf("sess-b expected DB_NAME()=HRDB, got err=%v rows=%+v", err, resB)
	}

	if _, err := coord.ExecuteSessionSQL("sess-a", "USE master;"); err != nil {
		t.Fatalf("sess-a USE master failed: %v", err)
	}
	resA2, err := coord.ExecuteSessionSQL("sess-a", "SELECT DB_NAME() AS db;")
	if err != nil || len(resA2.Rows) != 1 || resA2.Rows[0]["db"] != "master" {
		t.Fatalf("sess-a expected DB_NAME()=master, got err=%v rows=%+v", err, resA2)
	}
}

func TestTSQLCompatMatrix_SessionOptionsAndSystemVars(t *testing.T) {
	coord, cleanup := newCompatMatrixTestCoord(t)
	defer cleanup()

	sess := "sess-opts"
	for _, stmt := range []string{
		"SET NOCOUNT ON;",
		"SET ANSI_NULLS ON;",
		"SET QUOTED_IDENTIFIER ON;",
		"SET XACT_ABORT ON;",
	} {
		res, err := coord.ExecuteSessionSQL(sess, stmt)
		if err != nil || !res.Success {
			t.Fatalf("option statement %q failed: err=%v res=%+v", stmt, err, res)
		}
	}

	res, err := coord.ExecuteSessionSQL(sess, "SELECT @@TRANCOUNT AS tc, @@ERROR AS errnum;")
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("SELECT @@TRANCOUNT, @@ERROR failed: err=%v res=%+v", err, res)
	}
	if res.Rows[0]["tc"] != int64(0) || res.Rows[0]["errnum"] != int64(0) {
		t.Fatalf("expected @@TRANCOUNT=0, @@ERROR=0, got %+v", res.Rows[0])
	}
}

func TestTSQLCompatMatrix_ExecReturnVarCapture(t *testing.T) {
	coord, cleanup := newCompatMatrixTestCoord(t)
	defer cleanup()

	setupProcs := []string{
		`CREATE PROCEDURE sp_calc_status @a INT, @b INT OUTPUT AS
		 BEGIN
		     SET @b = @a * 10;
		     RETURN @a + 5;
		 END;`,
		`CREATE PROCEDURE sp_default_return AS
		 BEGIN
		     DECLARE @x INT = 1;
		 END;`,
		`CREATE PROCEDURE sp_caller_proc @in INT AS
		 BEGIN
		     DECLARE @rc1 INT;
		     DECLARE @rc2 INT;
		     DECLARE @outVal INT;
		     EXEC @rc1 = sp_calc_status @in, @outVal OUTPUT;
		     EXEC @rc2 = dbo.sp_default_return;
		     SELECT @rc1 AS rc1, @rc2 AS rc2, @outVal AS out_val;
		 END;`,
	}
	for _, q := range setupProcs {
		if _, err := coord.ExecuteSQL(q); err != nil {
			t.Fatalf("create proc failed: %v\nSQL: %s", err, q)
		}
	}

	// 1. Top-level batch: DECLARE @rc INT; EXEC @rc = sp_calc_status ...
	sess := "sess-exec-ret"
	batch1 := `
		DECLARE @rc INT;
		DECLARE @out INT;
		EXEC @rc = sp_calc_status 7, @out OUTPUT;
		SELECT @rc AS ret_code, @out AS out_param;
	`
	res1, err := coord.ExecuteSessionSQL(sess, batch1)
	if err != nil {
		t.Fatalf("top-level EXEC @rc = sp_calc_status failed: %v", err)
	}
	if len(res1.Rows) != 1 {
		t.Fatalf("expected 1 row from top-level batch, got %d (%+v)", len(res1.Rows), res1.Rows)
	}
	if res1.Rows[0]["ret_code"] != int64(12) || res1.Rows[0]["out_param"] != int64(70) {
		t.Fatalf("expected ret_code=12, out_param=70, got %+v", res1.Rows[0])
	}

	// 2. Top-level batch: default return status 0 when procedure has no explicit RETURN <int>
	batch2 := `
		DECLARE @rc_def INT = 99;
		EXEC @rc_def = sp_default_return;
		SELECT @rc_def AS def_code;
	`
	res2, err := coord.ExecuteSessionSQL(sess, batch2)
	if err != nil {
		t.Fatalf("top-level EXEC @rc_def = sp_default_return failed: %v", err)
	}
	if len(res2.Rows) != 1 || res2.Rows[0]["def_code"] != int64(0) {
		t.Fatalf("expected def_code=0, got %+v", res2.Rows)
	}

	// 3. Nested stored procedure call: EXEC @rc1 = sp_calc_status & EXEC @rc2 = dbo.sp_default_return inside sp_caller_proc
	res3, err := coord.ExecuteSessionSQL(sess, "EXEC sp_caller_proc 4;")
	if err != nil {
		t.Fatalf("nested EXEC @rc inside sp_caller_proc failed: %v", err)
	}
	if len(res3.Rows) != 1 {
		t.Fatalf("expected 1 row from sp_caller_proc, got %d (%+v)", len(res3.Rows), res3.Rows)
	}
	if res3.Rows[0]["rc1"] != int64(9) || res3.Rows[0]["rc2"] != int64(0) || res3.Rows[0]["out_val"] != int64(40) {
		t.Fatalf("expected rc1=9, rc2=0, out_val=40, got %+v", res3.Rows[0])
	}
}

func TestTSQLCompatMatrix_ParenthesizedSelectQueries(t *testing.T) {
	coord, cleanup := newCompatMatrixTestCoord(t)
	defer cleanup()

	// 1. Setup sample table
	setup := []string{
		"CREATE TABLE paren_items (id INT PRIMARY KEY, name VARCHAR(50), score INT);",
		"INSERT INTO paren_items VALUES (1, 'Alpha', 95), (2, 'Beta', 80), (3, 'Gamma', 90);",
	}
	for _, q := range setup {
		if _, err := coord.ExecuteSQL(q); err != nil {
			t.Fatalf("setup failed for %q: %v", q, err)
		}
	}

	// 2. Top-level parenthesized scalar & table SELECTs (single & multiple layers)
	scalarTests := []struct {
		sql      string
		expected any
	}{
		{"(SELECT 1 AS a);", int64(1)},
		{"(((SELECT 42 AS a)));", int64(42)},
		{"  (( SELECT 100 AS val ))  ", int64(100)},
		{"(SELECT score FROM paren_items WHERE id = 1);", int64(95)},
		{"(( (SELECT name FROM paren_items WHERE id = 2) ));", "Beta"},
	}
	for _, tt := range scalarTests {
		res, err := coord.ExecuteSQL(tt.sql)
		if err != nil {
			t.Fatalf("query %q failed: %v", tt.sql, err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("expected 1 row for %q, got %d", tt.sql, len(res.Rows))
		}
		t.Logf("query %q: Rows=%+v, Values=%+v", tt.sql, res.Rows, res.Values)
		var val any
		if len(res.Values) > 0 && len(res.Values[0]) > 0 {
			val = res.Values[0][0]
		} else {
			for _, v := range res.Rows[0] {
				val = v
				break
			}
		}
		if val != tt.expected {
			t.Fatalf("for %q, expected %v, got %v (%+v)", tt.sql, tt.expected, val, res.Rows)
		}
	}

	// 3. Parenthesized set operations (sibling branches & outer wrapped)
	setopQueries := []string{
		"(SELECT 1 AS a) UNION ALL (SELECT 2 AS a);",
		"((SELECT 1 AS a) UNION ALL (SELECT 2 AS a));",
		"((SELECT 1 AS a)) UNION ((SELECT 2 AS a));",
		"(((SELECT 1 AS a))) UNION ALL (SELECT 2 AS a);",
		"(SELECT id, score FROM paren_items WHERE id = 1) UNION ALL (SELECT id, score FROM paren_items WHERE id = 2) ORDER BY id ASC;",
		"((SELECT id FROM paren_items WHERE id <= 2) UNION (SELECT id FROM paren_items WHERE id >= 2)) ORDER BY id ASC;",
	}
	for _, q := range setopQueries {
		res, err := coord.ExecuteSQL(q)
		if err != nil {
			t.Fatalf("setop query %q failed: %v", q, err)
		}
		if len(res.Rows) < 2 {
			t.Fatalf("expected >= 2 rows for %q, got %d", q, len(res.Rows))
		}
	}

	// 4. CREATE VIEW with parenthesized SELECT body
	if _, err := coord.ExecuteSQL("CREATE VIEW v_paren_test AS (SELECT id, name FROM paren_items WHERE score >= 90);"); err != nil {
		t.Fatalf("CREATE VIEW with paren query failed: %v", err)
	}
	resView, err := coord.ExecuteSQL("SELECT * FROM v_paren_test ORDER BY id ASC;")
	if err != nil {
		t.Fatalf("SELECT from v_paren_test failed: %v", err)
	}
	if len(resView.Rows) != 2 {
		t.Fatalf("expected 2 rows from v_paren_test, got %d (%+v)", len(resView.Rows), resView.Rows)
	}

	// 5. CTE with parenthesized body
	cteSQL := "WITH cte_items AS ((SELECT id, name FROM paren_items WHERE id = 3)) SELECT name FROM cte_items;"
	resCTE, err := coord.ExecuteSQL(cteSQL)
	if err != nil {
		t.Fatalf("CTE with paren query failed: %v", err)
	}
	if len(resCTE.Rows) != 1 || resCTE.Rows[0]["name"] != "Gamma" {
		t.Fatalf("expected 1 row with Gamma, got %+v", resCTE.Rows)
	}

	// 6. Stored procedure with parenthesized SELECT statement
	procSQL := `
		CREATE PROCEDURE sp_test_paren_stmt AS
		BEGIN
			(SELECT 777 AS lucky);
		END;
	`
	if _, err := coord.ExecuteSQL(procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE with parenthesized statement failed: %v", err)
	}
	resProc, err := coord.ExecuteSQL("EXEC sp_test_paren_stmt;")
	if err != nil {
		t.Fatalf("EXEC sp_test_paren_stmt failed: %v", err)
	}
	if len(resProc.Rows) != 1 {
		t.Fatalf("expected 1 row from sp_test_paren_stmt, got %d (%+v)", len(resProc.Rows), resProc.Rows)
	}
	luckyVal := resProc.Rows[0]["lucky"]
	if luckyVal != int64(777) {
		t.Fatalf("expected lucky=777 from sp_test_paren_stmt, got %v (%+v)", luckyVal, resProc.Rows)
	}
}

// TestTSQLCompatMatrix_ProcedureInsertIntoTableWithoutExplicitPK verifies that executing
// INSERT INTO a table without an explicit PK from within a stored procedure correctly generates
// and assigns synthetic _row_id primary keys (FIX-VM-HIDDEN-ROWID-1).
func TestTSQLCompatMatrix_ProcedureInsertIntoTableWithoutExplicitPK(t *testing.T) {
	coord, cleanup := newCompatMatrixTestCoord(t)
	defer cleanup()

	// 1. Create table without explicit PK
	if _, err := coord.ExecuteSQL("CREATE TABLE test_sp_no_pk (msg VARCHAR(100), val INT);"); err != nil {
		t.Fatalf("CREATE TABLE test_sp_no_pk failed: %v", err)
	}

	// 2. Create procedure that inserts multiple rows
	procSQL := `
		CREATE PROCEDURE sp_insert_no_pk
		AS
		BEGIN
			INSERT INTO test_sp_no_pk (msg, val) VALUES ('first', 10);
			INSERT INTO test_sp_no_pk (msg, val) VALUES ('second', 20);
			INSERT INTO test_sp_no_pk (msg, val) VALUES ('third', 30);
		END;
	`
	if _, err := coord.ExecuteSQL(procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE sp_insert_no_pk failed: %v", err)
	}

	// 3. Execute procedure
	if _, err := coord.ExecuteSQL("EXEC sp_insert_no_pk;"); err != nil {
		t.Fatalf("EXEC sp_insert_no_pk failed: %v", err)
	}

	// 4. Verify all rows were inserted and can be queried
	res, err := coord.ExecuteSQL("SELECT msg, val FROM test_sp_no_pk ORDER BY val ASC;")
	if err != nil {
		t.Fatalf("SELECT from test_sp_no_pk failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d (%+v)", len(res.Rows), res.Rows)
	}
	if res.Rows[0]["msg"] != "first" || res.Rows[1]["msg"] != "second" || res.Rows[2]["msg"] != "third" {
		t.Fatalf("unexpected row contents: %+v", res.Rows)
	}

	// 5. Verify explicit query on synthetic _row_id
	resRowID, err := coord.ExecuteSQL("SELECT _row_id, msg FROM test_sp_no_pk ORDER BY _row_id ASC;")
	if err != nil {
		t.Fatalf("SELECT _row_id failed: %v", err)
	}
	if len(resRowID.Rows) != 3 {
		t.Fatalf("expected 3 rows for _row_id, got %d (%+v)", len(resRowID.Rows), resRowID.Rows)
	}

	// 6. Test INSERT INTO ... SELECT inside stored procedure for PK-less table
	if _, err := coord.ExecuteSQL("CREATE TABLE test_sp_no_pk_src (msg VARCHAR(100), val INT);"); err != nil {
		t.Fatalf("CREATE TABLE test_sp_no_pk_src failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO test_sp_no_pk_src VALUES ('src1', 100), ('src2', 200);"); err != nil {
		t.Fatalf("INSERT INTO test_sp_no_pk_src failed: %v", err)
	}

	procInsertSelectSQL := `
		CREATE PROCEDURE sp_insert_select_no_pk
		AS
		BEGIN
			INSERT INTO test_sp_no_pk (msg, val)
			SELECT msg, val FROM test_sp_no_pk_src;
		END;
	`
	if _, err := coord.ExecuteSQL(procInsertSelectSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE sp_insert_select_no_pk failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("EXEC sp_insert_select_no_pk;"); err != nil {
		t.Fatalf("EXEC sp_insert_select_no_pk failed: %v", err)
	}

	resTotal, err := coord.ExecuteSQL("SELECT _row_id, msg FROM test_sp_no_pk ORDER BY _row_id ASC;")
	if err != nil {
		t.Fatalf("SELECT _row_id after insert-select failed: %v", err)
	}
	if len(resTotal.Rows) != 5 {
		t.Fatalf("expected 5 rows total, got %d (%+v)", len(resTotal.Rows), resTotal.Rows)
	}
}

// TestTSQLCompatMatrix_InsertExecStoredProcedure verifies Task #7 (FIX-TSQL-INSERT-EXEC-1):
// Piping stored procedure execution result sets directly into target tables via INSERT INTO ... EXEC.
func TestTSQLCompatMatrix_InsertExecStoredProcedure(t *testing.T) {
	coord, cleanup := newCompatMatrixTestCoord(t)
	defer cleanup()

	// 1. Create source table and populate with data
	if _, err := coord.ExecuteSQL("CREATE TABLE src_products (id INT PRIMARY KEY, name VARCHAR(100), price INT);"); err != nil {
		t.Fatalf("CREATE TABLE src_products failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO src_products VALUES (1, 'Keyboard', 50), (2, 'Mouse', 30), (3, 'Monitor', 200);"); err != nil {
		t.Fatalf("INSERT INTO src_products failed: %v", err)
	}

	// 2. Create procedure that queries source table with a parameter
	procSQL := `
		CREATE PROCEDURE sp_get_discounted_products
			@max_price INT
		AS
		BEGIN
			SELECT id, name, price FROM src_products WHERE price <= @max_price ORDER BY id ASC;
		END;
	`
	if _, err := coord.ExecuteSQL(procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE sp_get_discounted_products failed: %v", err)
	}

	// 3. Create destination table
	if _, err := coord.ExecuteSQL("CREATE TABLE dest_discounted (prod_id INT, prod_name VARCHAR(100), prod_price INT);"); err != nil {
		t.Fatalf("CREATE TABLE dest_discounted failed: %v", err)
	}

	// 4. Test standalone INSERT INTO dest_discounted EXEC sp_get_discounted_products @max_price = 60;
	insertExecSQL := "INSERT INTO dest_discounted (prod_id, prod_name, prod_price) EXEC sp_get_discounted_products @max_price = 60;"
	if _, err := coord.ExecuteSQL(insertExecSQL); err != nil {
		t.Fatalf("standalone INSERT ... EXEC failed: %v", err)
	}

	res, err := coord.ExecuteSQL("SELECT prod_id, prod_name, prod_price FROM dest_discounted ORDER BY prod_id ASC;")
	if err != nil {
		t.Fatalf("SELECT dest_discounted failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows in dest_discounted, got %d (%+v)", len(res.Rows), res.Rows)
	}
	if res.Rows[0]["prod_name"] != "Keyboard" || res.Rows[1]["prod_name"] != "Mouse" {
		t.Fatalf("unexpected destination contents: %+v", res.Rows)
	}

	// 5. Test nested INSERT INTO ... EXEC inside another stored procedure
	procWrapperSQL := `
		CREATE PROCEDURE sp_load_items_into_dest
		AS
		BEGIN
			INSERT INTO dest_discounted (prod_id, prod_name, prod_price)
			EXEC sp_get_discounted_products @max_price = 200;
		END;
	`
	if _, err := coord.ExecuteSQL(procWrapperSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE sp_load_items_into_dest failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("EXEC sp_load_items_into_dest;"); err != nil {
		t.Fatalf("EXEC sp_load_items_into_dest failed: %v", err)
	}

	resTotal, err := coord.ExecuteSQL("SELECT COUNT(*) AS cnt FROM dest_discounted;")
	if err != nil {
		t.Fatalf("SELECT COUNT(*) failed: %v", err)
	}
	if resTotal.Rows[0]["cnt"] != int64(5) {
		t.Fatalf("expected 5 total rows, got %v", resTotal.Rows[0]["cnt"])
	}

	// 6. Test INSERT INTO #temp EXEC sp_get_discounted_products @max_price = 100;
	tempTableBatch := `
		CREATE TABLE #temp_discounted (prod_id INT, prod_name VARCHAR(100), prod_price INT);
		INSERT INTO #temp_discounted (prod_id, prod_name, prod_price)
		EXEC sp_get_discounted_products @max_price = 100;
		SELECT prod_name FROM #temp_discounted ORDER BY prod_id ASC;
	`
	sessID := "sess-temp-insert-exec"
	resTemp, err := coord.ExecuteSessionSQL(sessID, tempTableBatch)
	if err != nil {
		t.Fatalf("INSERT INTO #temp EXEC failed: %v", err)
	}
	if len(resTemp.Rows) != 2 {
		t.Fatalf("expected 2 rows in #temp_discounted, got %d (%+v)", len(resTemp.Rows), resTemp.Rows)
	}
	if resTemp.Rows[0]["prod_name"] != "Keyboard" || resTemp.Rows[1]["prod_name"] != "Mouse" {
		t.Fatalf("unexpected temp table contents: %+v", resTemp.Rows)
	}
}

func TestTSQLCompatMatrix_TriggerForKeyword(t *testing.T) {
	coord, cleanup := newCompatMatrixTestCoord(t)
	defer cleanup()

	setupSQL := []string{
		"CREATE TABLE orders_trg (id INT PRIMARY KEY, val VARCHAR(50));",
		"CREATE TABLE audit_trg (id INT PRIMARY KEY, msg VARCHAR(50));",
		`CREATE TRIGGER trg_orders_audit ON orders_trg FOR INSERT, UPDATE AS
		BEGIN
			DECLARE @oid INT;
			SET @oid = (SELECT id FROM inserted);
			INSERT INTO audit_trg VALUES (@oid, 'TRIGGER_FIRED');
		END;`,
	}
	for _, q := range setupSQL {
		if _, err := coord.ExecuteSQL(q); err != nil {
			t.Fatalf("setup query failed: %v\nSQL: %s", err, q)
		}
	}

	// Verify trigger metadata registered in catalog
	trg, err := coord.Catalog().GetTrigger("trg_orders_audit")
	if err != nil {
		t.Fatalf("GetTrigger failed: %v", err)
	}
	if trg.Timing != "AFTER" {
		t.Fatalf("expected trigger timing AFTER, got %s", trg.Timing)
	}
	if len(trg.Events) != 2 || trg.Events[0] != "INSERT" || trg.Events[1] != "UPDATE" {
		t.Fatalf("unexpected trigger events: %+v", trg.Events)
	}
}




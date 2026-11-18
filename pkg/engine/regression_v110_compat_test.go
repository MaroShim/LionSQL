package engine

import (
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/storage"
)

// TestRegression_V110_CompatSuite verifies critical defects
// and syntax enhancements fixed for v1.1.0 in a single hermetic regression suite.
func TestRegression_V110_CompatSuite(t *testing.T) {
	coord, cleanup := setupTestCoord(t)
	defer cleanup()

	sessID := "regress-sess"

	// =========================================================================
	// 1. Session Variable Expression Evaluation (SET @v = @v + 50)
	// =========================================================================
	t.Run("01_SessionVariableExpressionEval", func(t *testing.T) {
		if _, err := coord.ExecuteSessionSQL(sessID, "SET @counter = 100;"); err != nil {
			t.Fatalf("SET @counter failed: %v", err)
		}
		if _, err := coord.ExecuteSessionSQL(sessID, "SET @counter = @counter + 50;"); err != nil {
			t.Fatalf("SET @counter = @counter + 50 failed: %v", err)
		}
		res, err := coord.ExecuteSessionSQL(sessID, "SELECT @counter AS cnt;")
		if err != nil {
			t.Fatalf("SELECT @counter failed: %v", err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(res.Rows))
		}
		var cnt int64
		switch v := res.Rows[0]["cnt"].(type) {
		case int64:
			cnt = v
		case int:
			cnt = int64(v)
		case storage.Value:
			cnt = v.IVal
		default:
			t.Fatalf("unexpected type for @counter: %T (%v)", v, v)
		}
		if cnt != 150 {
			t.Fatalf("expected @counter = 150, got %d", cnt)
		}
	})

	// =========================================================================
	// 2. Postgres Syntax Sugar (LIMIT/OFFSET, CREATE TEMP TABLE, RETURNING)
	// =========================================================================
	t.Run("02_PostgresSyntaxSugar", func(t *testing.T) {
		// 2.1 CREATE TEMP TABLE
		_, err := coord.ExecuteSessionSQL(sessID, "CREATE TEMP TABLE pg_sugar (id INT PRIMARY KEY, name VARCHAR(50));")
		if err != nil {
			t.Fatalf("CREATE TEMP TABLE failed: %v", err)
		}

		// 2.2 INSERT ... RETURNING id, name
		resIns, err := coord.ExecuteSessionSQL(sessID, "INSERT INTO pg_sugar (id, name) VALUES (1, 'Alice'), (2, 'Bob'), (3, 'Charlie') RETURNING id, name;")
		if err != nil {
			t.Fatalf("INSERT RETURNING failed: %v", err)
		}
		if len(resIns.Rows) != 3 {
			t.Fatalf("expected 3 returned rows from INSERT RETURNING, got %d", len(resIns.Rows))
		}

		// 2.3 LIMIT 2 OFFSET 1
		resLim, err := coord.ExecuteSessionSQL(sessID, "SELECT id, name FROM pg_sugar ORDER BY id ASC LIMIT 2 OFFSET 1;")
		if err != nil {
			t.Fatalf("SELECT LIMIT OFFSET failed: %v", err)
		}
		if len(resLim.Rows) != 2 {
			t.Fatalf("expected 2 rows from LIMIT 2 OFFSET 1, got %d", len(resLim.Rows))
		}
		if fmt.Sprintf("%v", resLim.Rows[0]["name"]) != "Bob" || fmt.Sprintf("%v", resLim.Rows[1]["name"]) != "Charlie" {
			t.Fatalf("unexpected LIMIT OFFSET rows: %+v", resLim.Rows)
		}
	})

	// =========================================================================
	// 3. OPENJSON() Table-Valued Function (Default & WITH schema)
	// =========================================================================
	t.Run("03_OpenJSON_TVF", func(t *testing.T) {
		jsonStr := `{"sku": "LION-100", "price": 499, "tags": ["db", "fast"]}`

		// 3.1 Explicit WITH schema
		qWith := fmt.Sprintf(`SELECT sku, price FROM OPENJSON('%s') WITH (sku VARCHAR(50) '$.sku', price INT '$.price');`, jsonStr)
		resWith, err := coord.ExecuteSessionSQL(sessID, qWith)
		if err != nil {
			t.Fatalf("OPENJSON WITH failed: %v", err)
		}
		if len(resWith.Rows) != 1 {
			t.Fatalf("expected 1 row from OPENJSON WITH, got %d", len(resWith.Rows))
		}
		if fmt.Sprintf("%v", resWith.Rows[0]["sku"]) != "LION-100" {
			t.Fatalf("expected sku='LION-100', got %v", resWith.Rows[0]["sku"])
		}

		// 3.2 Default 3-column mode (key, value, type)
		qDef := fmt.Sprintf(`SELECT [key], [value], [type] FROM OPENJSON('%s');`, jsonStr)
		resDef, err := coord.ExecuteSessionSQL(sessID, qDef)
		if err != nil {
			t.Fatalf("OPENJSON default mode failed: %v", err)
		}
		if len(resDef.Rows) != 3 {
			t.Fatalf("expected 3 key-value rows from OPENJSON, got %d", len(resDef.Rows))
		}
	})

	// =========================================================================
	// 4. Concurrent Uncommitted Write-Write Conflict Prevention (Last-Write-Wins 차단)
	// =========================================================================
	t.Run("04_ConcurrentUncommittedWriteConflict", func(t *testing.T) {
		if _, err := coord.ExecuteSQL("CREATE TABLE accts (id INT PRIMARY KEY, bal INT);"); err != nil {
			t.Fatalf("CREATE TABLE accts failed: %v", err)
		}
		if _, err := coord.ExecuteSQL("INSERT INTO accts VALUES (1, 1000);"); err != nil {
			t.Fatalf("INSERT accts failed: %v", err)
		}

		s1 := "writer-1"
		s2 := "writer-2"

		// Session 1 updates row inside active transaction (UNCOMMITTED)
		if _, err := coord.ExecuteSessionSQL(s1, "BEGIN TRANSACTION;"); err != nil {
			t.Fatalf("s1 BEGIN TRAN failed: %v", err)
		}
		if _, err := coord.ExecuteSessionSQL(s1, "UPDATE accts SET bal = 900 WHERE id = 1;"); err != nil {
			t.Fatalf("s1 UPDATE failed: %v", err)
		}

		// Session 2 attempts concurrent write on SAME uncommitted row: MUST FAIL with conflict!
		_, err2 := coord.ExecuteSessionSQL(s2, "UPDATE accts SET bal = 800 WHERE id = 1;")
		if err2 == nil {
			t.Fatalf("CRITICAL BUG: Session 2 update succeeded on uncommitted row! Last-Write-Wins occurred!")
		}
		if !storage.IsWriteConflict(err2) {
			t.Fatalf("expected serialization write conflict error, got: %v", err2)
		}

		// Clean up s1
		if _, err := coord.ExecuteSessionSQL(s1, "COMMIT;"); err != nil {
			t.Fatalf("s1 COMMIT failed: %v", err)
		}
	})

	// =========================================================================
	// 5. Computed Column Dynamic Evaluation (total AS (qty * price))
	// =========================================================================
	t.Run("05_ComputedColumnDynamicEvaluation", func(t *testing.T) {
		// Temporary table #computed_t
		if _, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE #computed_t (qty INT, price INT, total AS (qty * price));"); err != nil {
			t.Fatalf("CREATE TABLE #computed_t failed: %v", err)
		}
		if _, err := coord.ExecuteSessionSQL(sessID, "INSERT INTO #computed_t (qty, price) VALUES (7, 6);"); err != nil {
			t.Fatalf("INSERT into #computed_t failed: %v", err)
		}
		resComp, err := coord.ExecuteSessionSQL(sessID, "SELECT qty, price, total FROM #computed_t;")
		if err != nil {
			t.Fatalf("SELECT from #computed_t failed: %v", err)
		}
		if len(resComp.Rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(resComp.Rows))
		}
		totVal := resComp.Rows[0]["total"]
		if totVal == nil {
			t.Fatalf("CRITICAL BUG: computed column total returned NULL!")
		}
		var total int64
		switch v := totVal.(type) {
		case int64:
			total = v
		case int:
			total = int64(v)
		case storage.Value:
			total = v.IVal
		default:
			t.Fatalf("unexpected type for total: %T (%v)", v, v)
		}
		if total != 42 {
			t.Fatalf("expected total = 42 (7 * 6), got %d", total)
		}
	})

	// =========================================================================
	// 6. Cursor DECLARE -> OPEN -> FETCH Lifecycle
	// =========================================================================
	t.Run("06_CursorDeclareOpenFetchLifecycle", func(t *testing.T) {
		if _, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE cur_data (id INT PRIMARY KEY, val VARCHAR(20));"); err != nil {
			t.Fatalf("CREATE TABLE cur_data failed: %v", err)
		}
		if _, err := coord.ExecuteSessionSQL(sessID, "INSERT INTO cur_data VALUES (1, 'One'), (2, 'Two');"); err != nil {
			t.Fatalf("INSERT cur_data failed: %v", err)
		}

		// 6.1 DECLARE cursor
		if _, err := coord.ExecuteSessionSQL(sessID, "DECLARE cur_test CURSOR FOR SELECT id, val FROM cur_data ORDER BY id ASC;"); err != nil {
			t.Fatalf("DECLARE CURSOR failed: %v", err)
		}

		// 6.2 OPEN cursor: must succeed without 'already open' error!
		if _, err := coord.ExecuteSessionSQL(sessID, "OPEN cur_test;"); err != nil {
			t.Fatalf("OPEN cur_test failed immediately after DECLARE: %v", err)
		}

		// 6.3 Duplicate OPEN: must correctly fail with 'already open'
		_, errDupOpen := coord.ExecuteSessionSQL(sessID, "OPEN cur_test;")
		if errDupOpen == nil || !strings.Contains(errDupOpen.Error(), "already open") {
			t.Fatalf("expected 'already open' error on second OPEN, got: %v", errDupOpen)
		}

		// 6.4 FETCH NEXT
		resFetch, err := coord.ExecuteSessionSQL(sessID, "FETCH NEXT FROM cur_test;")
		if err != nil {
			t.Fatalf("FETCH NEXT failed: %v", err)
		}
		if len(resFetch.Rows) != 1 || fmt.Sprintf("%v", resFetch.Rows[0]["val"]) != "One" {
			t.Fatalf("expected 'One', got %+v", resFetch.Rows)
		}

		// 6.5 CLOSE & DEALLOCATE
		if _, err := coord.ExecuteSessionSQL(sessID, "CLOSE cur_test;"); err != nil {
			t.Fatalf("CLOSE cur_test failed: %v", err)
		}
		if _, err := coord.ExecuteSessionSQL(sessID, "DEALLOCATE cur_test;"); err != nil {
			t.Fatalf("DEALLOCATE cur_test failed: %v", err)
		}
	})

	// =========================================================================
	// 7. Trigger INSERTED Virtual Table Hidden Column Filtering
	// =========================================================================
	t.Run("07_TriggerInsertedHiddenColumnFiltering", func(t *testing.T) {
		// Table without explicit PK (forces internal _row_id hidden column generation)
		if _, err := coord.ExecuteSQL("CREATE TABLE audit_src (name VARCHAR(50), qty INT);"); err != nil {
			t.Fatalf("CREATE TABLE audit_src failed: %v", err)
		}
		if _, err := coord.ExecuteSQL("CREATE TABLE audit_log (logged_name VARCHAR(50), logged_qty INT, col_count INT);"); err != nil {
			t.Fatalf("CREATE TABLE audit_log failed: %v", err)
		}

		// Trigger that reads from INSERTED
		trgSQL := `
		CREATE TRIGGER trg_audit_src_insert ON audit_src AFTER INSERT AS
		BEGIN
			DECLARE @N VARCHAR(50);
			SET @N = (SELECT name FROM inserted);
			DECLARE @Q INT;
			SET @Q = (SELECT qty FROM inserted);
			INSERT INTO audit_log (logged_name, logged_qty, col_count) VALUES (@N, @Q, 2);
		END;`
		if _, err := coord.ExecuteSQL(trgSQL); err != nil {
			t.Fatalf("CREATE TRIGGER failed: %v", err)
		}

		// Perform INSERT to fire trigger
		if _, err := coord.ExecuteSQL("INSERT INTO audit_src (name, qty) VALUES ('Widget', 10);"); err != nil {
			t.Fatalf("INSERT into audit_src failed: %v", err)
		}

		// Verify trigger log
		resLog, err := coord.ExecuteSQL("SELECT logged_name, logged_qty, col_count FROM audit_log;")
		if err != nil {
			t.Fatalf("SELECT audit_log failed: %v", err)
		}
		if len(resLog.Rows) != 1 {
			t.Fatalf("expected 1 log row from trigger, got %d", len(resLog.Rows))
		}
		if fmt.Sprintf("%v", resLog.Rows[0]["logged_name"]) != "Widget" {
			t.Fatalf("expected logged_name='Widget', got %v", resLog.Rows[0]["logged_name"])
		}
	})
}

package engine_test

import (
	"fmt"
	"testing"
)

// TestTxRigor_P2_1_CreateTableAndDropTableRollback verifies that both CREATE TABLE and DROP TABLE
// are fully transactional and roll back cleanly in both catalog and underlying storage.
func TestTxRigor_P2_1_CreateTableAndDropTableRollback(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_p2_1"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	// 1. CREATE TABLE inside transaction and ROLLBACK
	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE t_created_in_tx (id INT PRIMARY KEY, name VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "INSERT INTO t_created_in_tx VALUES (1, 'Alice');"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}
	// Verify row visible inside tx
	res, err := coord.ExecuteSessionSQL(sess, "SELECT count(*) AS cnt FROM t_created_in_tx;")
	if err != nil || fmt.Sprintf("%v", res.Rows[0]["cnt"]) != "1" {
		t.Fatalf("expected count 1 inside tx, got %+v", res)
	}

	if _, err := coord.ExecuteSessionSQL(sess, "ROLLBACK TRANSACTION;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// Verify table is completely gone
	_, err = coord.ExecuteSessionSQL(sess, "SELECT * FROM t_created_in_tx;")
	if err == nil {
		t.Fatalf("expected error querying rolled back table, but query succeeded")
	}

	// 2. DROP TABLE inside transaction and ROLLBACK
	if _, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE t_to_be_dropped (id INT PRIMARY KEY, val VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE outside tx failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "INSERT INTO t_to_be_dropped VALUES (10, 'keep_after_rollback');"); err != nil {
		t.Fatalf("INSERT outside tx failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "DROP TABLE t_to_be_dropped;"); err != nil {
		t.Fatalf("DROP TABLE in tx failed: %v", err)
	}

	// Verify table dropped inside tx
	_, err = coord.ExecuteSessionSQL(sess, "SELECT * FROM t_to_be_dropped;")
	if err == nil {
		t.Fatalf("expected dropped table to be invisible inside tx")
	}

	if _, err := coord.ExecuteSessionSQL(sess, "ROLLBACK TRANSACTION;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// Verify table and data are 100% restored
	resRestored, err := coord.ExecuteSessionSQL(sess, "SELECT val FROM t_to_be_dropped WHERE id = 10;")
	if err != nil || len(resRestored.Rows) != 1 || resRestored.Rows[0]["val"] != "keep_after_rollback" {
		t.Fatalf("failed to restore dropped table after rollback: res=%+v, err=%v", resRestored, err)
	}
}

// TestTxRigor_P2_2_AlterTableRollback verifies that ALTER TABLE ADD COLUMN inside a transaction
// cleanly rolls back both metadata and decoder offsets on ROLLBACK.
func TestTxRigor_P2_2_AlterTableRollback(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_p2_2"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	if _, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE t_alter_tx (id INT PRIMARY KEY, col1 VARCHAR(20));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "INSERT INTO t_alter_tx VALUES (1, 'initial');"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "ALTER TABLE t_alter_tx ADD col2 INT;"); err != nil {
		t.Fatalf("ALTER TABLE ADD failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "UPDATE t_alter_tx SET col2 = 999 WHERE id = 1;"); err != nil {
		t.Fatalf("UPDATE col2 failed: %v", err)
	}

	resInside, err := coord.ExecuteSessionSQL(sess, "SELECT col2 FROM t_alter_tx WHERE id = 1;")
	if err != nil || len(resInside.Rows) != 1 || fmt.Sprintf("%v", resInside.Rows[0]["col2"]) != "999" {
		t.Fatalf("expected col2 = 999 inside tx, got %+v", resInside)
	}

	if _, err := coord.ExecuteSessionSQL(sess, "ROLLBACK TRANSACTION;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// Verify col2 no longer exists
	_, err = coord.ExecuteSessionSQL(sess, "SELECT col2 FROM t_alter_tx;")
	if err == nil {
		t.Fatalf("expected error referencing col2 after rollback, but query succeeded")
	}

	// Verify original row is preserved
	resAfter, err := coord.ExecuteSessionSQL(sess, "SELECT col1 FROM t_alter_tx WHERE id = 1;")
	if err != nil || len(resAfter.Rows) != 1 || resAfter.Rows[0]["col1"] != "initial" {
		t.Fatalf("unexpected row data after rollback: %+v", resAfter)
	}

	// Scenario B: ALTER TABLE DROP COLUMN inside a transaction and ROLLBACK
	if _, err := coord.ExecuteSessionSQL(sess, "ALTER TABLE t_alter_tx ADD col_drop INT;"); err != nil {
		t.Fatalf("setup ADD col_drop failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "UPDATE t_alter_tx SET col_drop = 42 WHERE id = 1;"); err != nil {
		t.Fatalf("setup UPDATE col_drop failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "ALTER TABLE t_alter_tx DROP COLUMN col_drop;"); err != nil {
		t.Fatalf("ALTER TABLE DROP COLUMN failed: %v", err)
	}
	// Column must appear dropped inside tx
	_, err = coord.ExecuteSessionSQL(sess, "SELECT col_drop FROM t_alter_tx;")
	if err == nil {
		t.Fatalf("expected error querying dropped column inside tx")
	}

	if _, err := coord.ExecuteSessionSQL(sess, "ROLLBACK TRANSACTION;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// Dropped column schema and decoder must be 100% restored after rollback!
	resRestoredCol, err := coord.ExecuteSessionSQL(sess, "SELECT col_drop FROM t_alter_tx WHERE id = 1;")
	if err != nil || len(resRestoredCol.Rows) != 1 || len(resRestoredCol.Columns) != 1 || resRestoredCol.Columns[0] != "col_drop" {
		t.Fatalf("failed to restore dropped column schema after rollback: %+v (err=%v)", resRestoredCol, err)
	}
}

// TestTxRigor_P2_3_TempTableRollbackParticipation verifies that local temporary tables (#temp)
// 100% participate in transaction rollback per T-SQL enterprise specification.
func TestTxRigor_P2_3_TempTableRollbackParticipation(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_p2_3"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	// Scenario A: Existing #temp table has rows inserted in a transaction, then rolled back
	if _, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE #t_temp_data (id INT PRIMARY KEY, note VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE #temp failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "INSERT INTO #t_temp_data VALUES (1, 'persisted');"); err != nil {
		t.Fatalf("INSERT #temp outside tx failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "INSERT INTO #t_temp_data VALUES (2, 'in_tx_revert');"); err != nil {
		t.Fatalf("INSERT #temp in tx failed: %v", err)
	}
	resInside, err := coord.ExecuteSessionSQL(sess, "SELECT count(*) AS cnt FROM #t_temp_data;")
	if err != nil || fmt.Sprintf("%v", resInside.Rows[0]["cnt"]) != "2" {
		t.Fatalf("expected count 2 inside tx, got %+v", resInside)
	}

	if _, err := coord.ExecuteSessionSQL(sess, "ROLLBACK TRANSACTION;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// Row 2 MUST be rolled back, row 1 MUST remain!
	resAfter, err := coord.ExecuteSessionSQL(sess, "SELECT id, note FROM #t_temp_data ORDER BY id;")
	if err != nil || len(resAfter.Rows) != 1 || fmt.Sprintf("%v", resAfter.Rows[0]["id"]) != "1" {
		t.Fatalf("expected only row 1 in #temp after rollback, got %+v (err=%v)", resAfter, err)
	}

	// Scenario B: CREATE TABLE #temp inside a transaction, then rolled back
	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE #t_created_in_tx (id INT PRIMARY KEY);"); err != nil {
		t.Fatalf("CREATE #temp in tx failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "ROLLBACK TRANSACTION;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// #t_created_in_tx MUST be completely destroyed
	_, err = coord.ExecuteSessionSQL(sess, "SELECT * FROM #t_created_in_tx;")
	if err == nil {
		t.Fatalf("expected error querying #temp created inside rolled back tx, but query succeeded")
	}

	// Scenario C: UPDATE #temp inside transaction, then ROLLBACK
	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "UPDATE #t_temp_data SET note = 'mutated_in_tx' WHERE id = 1;"); err != nil {
		t.Fatalf("UPDATE #temp in tx failed: %v", err)
	}
	resMutated, err := coord.ExecuteSessionSQL(sess, "SELECT note FROM #t_temp_data WHERE id = 1;")
	if err != nil || len(resMutated.Rows) != 1 || resMutated.Rows[0]["note"] != "mutated_in_tx" {
		t.Fatalf("expected mutated note inside tx, got %+v", resMutated)
	}

	if _, err := coord.ExecuteSessionSQL(sess, "ROLLBACK TRANSACTION;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}
	resRevertedNote, err := coord.ExecuteSessionSQL(sess, "SELECT note FROM #t_temp_data WHERE id = 1;")
	if err != nil || len(resRevertedNote.Rows) != 1 || resRevertedNote.Rows[0]["note"] != "persisted" {
		t.Fatalf("expected note to revert back to 'persisted', got %+v", resRevertedNote)
	}

	// Scenario D: DELETE FROM #temp inside transaction, then ROLLBACK
	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "DELETE FROM #t_temp_data WHERE id = 1;"); err != nil {
		t.Fatalf("DELETE #temp in tx failed: %v", err)
	}
	resDelInside, err := coord.ExecuteSessionSQL(sess, "SELECT count(*) AS cnt FROM #t_temp_data;")
	if err != nil || fmt.Sprintf("%v", resDelInside.Rows[0]["cnt"]) != "0" {
		t.Fatalf("expected 0 rows inside tx after delete, got %+v", resDelInside)
	}

	if _, err := coord.ExecuteSessionSQL(sess, "ROLLBACK TRANSACTION;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}
	resDelReverted, err := coord.ExecuteSessionSQL(sess, "SELECT count(*) AS cnt FROM #t_temp_data;")
	if err != nil || fmt.Sprintf("%v", resDelReverted.Rows[0]["cnt"]) != "1" {
		t.Fatalf("expected deleted row to be restored in #temp after rollback, got %+v", resDelReverted)
	}

	// Scenario E: DROP TABLE #temp inside transaction, then ROLLBACK
	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "DROP TABLE #t_temp_data;"); err != nil {
		t.Fatalf("DROP #temp in tx failed: %v", err)
	}
	_, err = coord.ExecuteSessionSQL(sess, "SELECT * FROM #t_temp_data;")
	if err == nil {
		t.Fatalf("expected dropped #temp to be invisible inside tx")
	}

	if _, err := coord.ExecuteSessionSQL(sess, "ROLLBACK TRANSACTION;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}
	resDropReverted, err := coord.ExecuteSessionSQL(sess, "SELECT count(*) AS cnt FROM #t_temp_data;")
	if err != nil || fmt.Sprintf("%v", resDropReverted.Rows[0]["cnt"]) != "1" {
		t.Fatalf("expected dropped #temp table to be restored after rollback, got %+v (err=%v)", resDropReverted, err)
	}
}

// TestTxRigor_P2_4_TableVariableRollbackExemption verifies the T-SQL enterprise invariant:
// Table variables (@table_var) are strictly EXEMPT from transaction rollback.
// Rows inserted into table variables survive ROLLBACK TRANSACTION unharmed.
func TestTxRigor_P2_4_TableVariableRollbackExemption(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_p2_4"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	// In T-SQL batch:
	// DECLARE @errLog TABLE (err_code INT, err_msg VARCHAR(100));
	// BEGIN TRANSACTION;
	// INSERT INTO regular_table VALUES (1);
	// INSERT INTO @errLog VALUES (50001, 'something failed');
	// ROLLBACK TRANSACTION;
	// SELECT * FROM @errLog;
	batch := `
CREATE TABLE t_reg (id INT PRIMARY KEY);
DECLARE @errLog TABLE (code INT PRIMARY KEY, msg VARCHAR(100));
BEGIN TRANSACTION;
INSERT INTO t_reg VALUES (10);
INSERT INTO @errLog VALUES (50001, 'critical failure logged');
ROLLBACK TRANSACTION;
SELECT code, msg FROM @errLog;
`
	res, err := coord.ExecuteSessionSQL(sess, batch)
	if err != nil {
		t.Fatalf("batch execution failed: %v", err)
	}

	// @errLog MUST retain row (50001, 'critical failure logged') despite the transaction rollback!
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["code"]) != "50001" {
		t.Fatalf("T-SQL VIOLATION: expected table variable rows to survive ROLLBACK, got %+v", res.Rows)
	}

	// Regular table t_reg MUST be completely empty (rolled back)
	resReg, err := coord.ExecuteSessionSQL(sess, "SELECT count(*) AS cnt FROM t_reg;")
	if err != nil || fmt.Sprintf("%v", resReg.Rows[0]["cnt"]) != "0" {
		t.Fatalf("expected regular table to be rolled back (count 0), got %+v", resReg)
	}
}

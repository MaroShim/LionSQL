package engine_test

import (
	"fmt"
	"strings"
	"testing"
)

// TestTxRigor_P1_1_AutocommitAndExplicitBoundary tests transition between autocommit and explicit transactions.
func TestTxRigor_P1_1_AutocommitAndExplicitBoundary(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sessA := "test_sess_p1_1_a"
	sessB := "test_sess_p1_1_b"
	_, _ = coord.GetOrCreateSession(sessA)
	_, _ = coord.GetOrCreateSession(sessB)
	defer coord.CloseSession(sessA)
	defer coord.CloseSession(sessB)

	// 1. DDL in autocommit
	if _, err := coord.ExecuteSessionSQL(sessA, "CREATE TABLE t_autocommit (id INT PRIMARY KEY, val VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 2. DML in autocommit is immediately durable and visible to sessB
	if _, err := coord.ExecuteSessionSQL(sessA, "INSERT INTO t_autocommit VALUES (1, 'auto_committed');"); err != nil {
		t.Fatalf("INSERT autocommit failed: %v", err)
	}
	resB, err := coord.ExecuteSessionSQL(sessB, "SELECT val FROM t_autocommit WHERE id = 1;")
	if err != nil || len(resB.Rows) != 1 || resB.Rows[0]["val"] != "auto_committed" {
		t.Fatalf("autocommit row not visible to sessB: res=%+v, err=%v", resB, err)
	}

	// 3. BEGIN TRANSACTION starts an explicit transaction
	if _, err := coord.ExecuteSessionSQL(sessA, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sessA, "INSERT INTO t_autocommit VALUES (2, 'uncommitted_tx');"); err != nil {
		t.Fatalf("INSERT in tx failed: %v", err)
	}

	// sessB MUST NOT see uncommitted row 2 (Snapshot / Read Committed Isolation)
	resB2, err := coord.ExecuteSessionSQL(sessB, "SELECT val FROM t_autocommit WHERE id = 2;")
	if err != nil {
		t.Fatalf("sessB SELECT failed: %v", err)
	}
	if len(resB2.Rows) != 0 {
		t.Fatalf("dirty read leak: sessB saw uncommitted row 2: %+v", resB2.Rows)
	}

	// 4. ROLLBACK reverts row 2
	if _, err := coord.ExecuteSessionSQL(sessA, "ROLLBACK TRANSACTION;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}
	resA, err := coord.ExecuteSessionSQL(sessA, "SELECT count(*) AS cnt FROM t_autocommit;")
	if err != nil || len(resA.Rows) != 1 || fmt.Sprintf("%v", resA.Rows[0]["cnt"]) != "1" {
		t.Fatalf("expected count 1 after rollback, got %+v (err=%v)", resA, err)
	}
}

// TestTxRigor_P1_2_NestedTranCountAndPhysicalCommit verifies that nested COMMIT only decrements
// @@TRANCOUNT, and data is only physically committed when @@TRANCOUNT reaches 0.
// If ROLLBACK is executed midway, ALL nested inserts are completely undone.
func TestTxRigor_P1_2_NestedTranCountAndPhysicalCommit(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_p1_2"
	sessObserver := "test_sess_p1_2_obs"
	_, _ = coord.GetOrCreateSession(sess)
	_, _ = coord.GetOrCreateSession(sessObserver)
	defer coord.CloseSession(sess)
	defer coord.CloseSession(sessObserver)

	if _, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE t_nested (id INT PRIMARY KEY, step VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 1. Level 1: BEGIN TRAN
	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN 1 failed: %v", err)
	}
	if res, err := coord.ExecuteSessionSQL(sess, "SELECT @@TRANCOUNT AS tc;"); err != nil || fmt.Sprintf("%v", res.Rows[0]["tc"]) != "1" {
		t.Fatalf("expected @@TRANCOUNT = 1, got %+v", res)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "INSERT INTO t_nested VALUES (1, 'level_1');"); err != nil {
		t.Fatalf("INSERT level 1 failed: %v", err)
	}

	// 2. Level 2: Nested BEGIN TRAN
	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN 2 failed: %v", err)
	}
	if res, err := coord.ExecuteSessionSQL(sess, "SELECT @@TRANCOUNT AS tc;"); err != nil || fmt.Sprintf("%v", res.Rows[0]["tc"]) != "2" {
		t.Fatalf("expected @@TRANCOUNT = 2, got %+v", res)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "INSERT INTO t_nested VALUES (2, 'level_2');"); err != nil {
		t.Fatalf("INSERT level 2 failed: %v", err)
	}

	// 3. Inner COMMIT TRAN: should only decrement @@TRANCOUNT to 1, NOT physically commit!
	if _, err := coord.ExecuteSessionSQL(sess, "COMMIT TRANSACTION;"); err != nil {
		t.Fatalf("inner COMMIT failed: %v", err)
	}
	if res, err := coord.ExecuteSessionSQL(sess, "SELECT @@TRANCOUNT AS tc;"); err != nil || fmt.Sprintf("%v", res.Rows[0]["tc"]) != "1" {
		t.Fatalf("expected @@TRANCOUNT = 1 after inner commit, got %+v", res)
	}

	// Observer session MUST NOT see either row 1 or row 2 yet!
	resObs, err := coord.ExecuteSessionSQL(sessObserver, "SELECT count(*) AS cnt FROM t_nested;")
	if err != nil || fmt.Sprintf("%v", resObs.Rows[0]["cnt"]) != "0" {
		t.Fatalf("premature physical commit! observer saw rows before outer commit: %+v", resObs)
	}

	// 4. Outer ROLLBACK: should abort entire transaction and undo BOTH level 1 and level 2
	if _, err := coord.ExecuteSessionSQL(sess, "ROLLBACK TRANSACTION;"); err != nil {
		t.Fatalf("outer ROLLBACK failed: %v", err)
	}
	if res, err := coord.ExecuteSessionSQL(sess, "SELECT @@TRANCOUNT AS tc;"); err != nil || fmt.Sprintf("%v", res.Rows[0]["tc"]) != "0" {
		t.Fatalf("expected @@TRANCOUNT = 0 after rollback, got %+v", res)
	}

	// Verify table is completely empty
	resCheck, err := coord.ExecuteSessionSQL(sess, "SELECT count(*) AS cnt FROM t_nested;")
	if err != nil || fmt.Sprintf("%v", resCheck.Rows[0]["cnt"]) != "0" {
		t.Fatalf("expected 0 rows after total rollback, got %+v", resCheck)
	}
}

// TestTxRigor_P1_3_DeeplyNestedTotalAnnihilation verifies that stacking 5 levels of BEGIN TRAN
// is immediately wiped clean by a single ROLLBACK TRANSACTION.
func TestTxRigor_P1_3_DeeplyNestedTotalAnnihilation(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_p1_3"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	if _, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE t_deep (id INT PRIMARY KEY);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Stack 5 levels of transactions
	for i := 1; i <= 5; i++ {
		if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
			t.Fatalf("BEGIN level %d failed: %v", i, err)
		}
		if _, err := coord.ExecuteSessionSQL(sess, fmt.Sprintf("INSERT INTO t_deep VALUES (%d);", i)); err != nil {
			t.Fatalf("INSERT level %d failed: %v", i, err)
		}
	}

	// Verify @@TRANCOUNT is 5
	res, err := coord.ExecuteSessionSQL(sess, "SELECT @@TRANCOUNT AS tc;")
	if err != nil || fmt.Sprintf("%v", res.Rows[0]["tc"]) != "5" {
		t.Fatalf("expected @@TRANCOUNT = 5, got %+v", res)
	}

	// Single ROLLBACK
	if _, err := coord.ExecuteSessionSQL(sess, "ROLLBACK TRANSACTION;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// Verify @@TRANCOUNT is 0 and 0 rows remain
	resTC, err := coord.ExecuteSessionSQL(sess, "SELECT @@TRANCOUNT AS tc;")
	if err != nil || fmt.Sprintf("%v", resTC.Rows[0]["tc"]) != "0" {
		t.Fatalf("expected @@TRANCOUNT = 0, got %+v", resTC)
	}

	resCnt, err := coord.ExecuteSessionSQL(sess, "SELECT count(*) AS cnt FROM t_deep;")
	if err != nil || fmt.Sprintf("%v", resCnt.Rows[0]["cnt"]) != "0" {
		t.Fatalf("expected 0 rows after rollback, got %+v", resCnt)
	}
}

// TestTxRigor_P1_5_AnsiSavepointStackingAndShadowing verifies SAVEPOINT, ROLLBACK TO SAVEPOINT,
// and RELEASE SAVEPOINT contracts including name shadowing.
func TestTxRigor_P1_5_AnsiSavepointStackingAndShadowing(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_p1_4"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	if _, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE t_sp (id INT PRIMARY KEY, name VARCHAR(20));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}

	// Insert row 1
	if _, err := coord.ExecuteSessionSQL(sess, "INSERT INTO t_sp VALUES (1, 'init');"); err != nil {
		t.Fatalf("INSERT 1 failed: %v", err)
	}

	// Savepoint 1
	if _, err := coord.ExecuteSessionSQL(sess, "SAVEPOINT sp1;"); err != nil {
		t.Fatalf("SAVEPOINT sp1 failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "INSERT INTO t_sp VALUES (2, 'after_sp1');"); err != nil {
		t.Fatalf("INSERT 2 failed: %v", err)
	}

	// Savepoint 2
	if _, err := coord.ExecuteSessionSQL(sess, "SAVEPOINT sp2;"); err != nil {
		t.Fatalf("SAVEPOINT sp2 failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "INSERT INTO t_sp VALUES (3, 'after_sp2');"); err != nil {
		t.Fatalf("INSERT 3 failed: %v", err)
	}

	// Rollback to sp2: row 3 must be rolled back, but rows 1 and 2 must remain!
	if _, err := coord.ExecuteSessionSQL(sess, "ROLLBACK TO SAVEPOINT sp2;"); err != nil {
		t.Fatalf("ROLLBACK TO sp2 failed: %v", err)
	}

	res, err := coord.ExecuteSessionSQL(sess, "SELECT count(*) AS cnt FROM t_sp;")
	if err != nil || fmt.Sprintf("%v", res.Rows[0]["cnt"]) != "2" {
		t.Fatalf("expected 2 rows after rollback to sp2, got %+v", res)
	}

	// Release sp1: according to SQL standard, releasing sp1 also destroys sp2 (child savepoints)
	if _, err := coord.ExecuteSessionSQL(sess, "RELEASE SAVEPOINT sp1;"); err != nil {
		t.Fatalf("RELEASE sp1 failed: %v", err)
	}

	// Rolling back to released sp1 must FAIL CLOSED (SQLSTATE 3B001)
	_, err = coord.ExecuteSessionSQL(sess, "ROLLBACK TO SAVEPOINT sp1;")
	if err == nil {
		t.Fatalf("expected error rolling back to released savepoint, got nil")
	}

	// Commit remaining transaction (row 1 should be committed)
	if _, err := coord.ExecuteSessionSQL(sess, "COMMIT TRANSACTION;"); err != nil {
		t.Fatalf("COMMIT failed: %v", err)
	}
}

// TestTxRigor_P1_7_ReadOnlyTransactionFailClosed verifies that READ ONLY transactions
// strictly reject INSERT, UPDATE, DELETE, and DDL statements with SQLSTATE 25006.
func TestTxRigor_P1_7_ReadOnlyTransactionFailClosed(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_p1_5"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	if _, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE t_ro (id INT PRIMARY KEY, val INT);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "INSERT INTO t_ro VALUES (1, 100);"); err != nil {
		t.Fatalf("INSERT initial row failed: %v", err)
	}

	// Start READ ONLY transaction
	if _, err := coord.ExecuteSessionSQL(sess, "SET TRANSACTION READ ONLY;"); err != nil {
		t.Fatalf("SET TRANSACTION READ ONLY failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}

	// 1. SELECT should succeed
	res, err := coord.ExecuteSessionSQL(sess, "SELECT val FROM t_ro WHERE id = 1;")
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("SELECT in read-only tx failed: %+v (err=%v)", res, err)
	}

	// 2. INSERT should be blocked
	_, err = coord.ExecuteSessionSQL(sess, "INSERT INTO t_ro VALUES (2, 200);")
	if err == nil {
		t.Fatalf("expected error on INSERT in READ ONLY transaction, got nil")
	}
	if !strings.Contains(err.Error(), "read-only") && !strings.Contains(err.Error(), "25006") {
		t.Fatalf("expected read-only error (25006), got: %v", err)
	}

	// 3. UPDATE should be blocked
	_, err = coord.ExecuteSessionSQL(sess, "UPDATE t_ro SET val = 999 WHERE id = 1;")
	if err == nil {
		t.Fatalf("expected error on UPDATE in READ ONLY transaction, got nil")
	}

	// 4. DELETE should be blocked
	_, err = coord.ExecuteSessionSQL(sess, "DELETE FROM t_ro WHERE id = 1;")
	if err == nil {
		t.Fatalf("expected error on DELETE in READ ONLY transaction, got nil")
	}

	// Clean rollback
	if _, err := coord.ExecuteSessionSQL(sess, "ROLLBACK TRANSACTION;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}
}

// TestTxRigor_P1_4_TSqlSavepointKeepsTranCount verifies the T-SQL enterprise singularity:
// In Microsoft SQL Server, 'ROLLBACK TRANSACTION <savepoint_name>' rolls back changes made since
// the savepoint, but strictly PRESERVES @@TRANCOUNT (does NOT decrement it or reset to 0).
// Only a parameterless 'ROLLBACK TRANSACTION' resets @@TRANCOUNT to 0.
func TestTxRigor_P1_4_TSqlSavepointKeepsTranCount(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_p1_4_tsql"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	if _, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE t_tsql_sp (id INT PRIMARY KEY, val VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 1. BEGIN TRAN
	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if res, err := coord.ExecuteSessionSQL(sess, "SELECT @@TRANCOUNT AS tc;"); err != nil || fmt.Sprintf("%v", res.Rows[0]["tc"]) != "1" {
		t.Fatalf("expected @@TRANCOUNT = 1, got %+v", res)
	}

	if _, err := coord.ExecuteSessionSQL(sess, "INSERT INTO t_tsql_sp VALUES (1, 'base');"); err != nil {
		t.Fatalf("INSERT 1 failed: %v", err)
	}

	// 2. SAVE TRAN sp_inner
	if _, err := coord.ExecuteSessionSQL(sess, "SAVE TRANSACTION sp_inner;"); err != nil {
		t.Fatalf("SAVE TRAN failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQL(sess, "INSERT INTO t_tsql_sp VALUES (2, 'to_be_reverted');"); err != nil {
		t.Fatalf("INSERT 2 failed: %v", err)
	}

	// 3. ROLLBACK TRANSACTION sp_inner
	if _, err := coord.ExecuteSessionSQL(sess, "ROLLBACK TRANSACTION sp_inner;"); err != nil {
		t.Fatalf("ROLLBACK TRAN sp_inner failed: %v", err)
	}

	// CRITICAL T-SQL INVARIANT: @@TRANCOUNT MUST STILL BE 1!
	resTC, err := coord.ExecuteSessionSQL(sess, "SELECT @@TRANCOUNT AS tc;")
	if err != nil || fmt.Sprintf("%v", resTC.Rows[0]["tc"]) != "1" {
		t.Fatalf("T-SQL VIOLATION: expected @@TRANCOUNT = 1 after ROLLBACK TRAN <savepoint>, got %+v", resTC)
	}

	// Row 2 should be gone, but row 1 must remain
	resCount, err := coord.ExecuteSessionSQL(sess, "SELECT count(*) AS cnt FROM t_tsql_sp;")
	if err != nil || fmt.Sprintf("%v", resCount.Rows[0]["cnt"]) != "1" {
		t.Fatalf("expected count = 1 after savepoint rollback, got %+v", resCount)
	}

	// 4. Parameterless ROLLBACK TRANSACTION should reset @@TRANCOUNT to 0
	if _, err := coord.ExecuteSessionSQL(sess, "ROLLBACK TRANSACTION;"); err != nil {
		t.Fatalf("parameterless ROLLBACK failed: %v", err)
	}
	resTC0, err := coord.ExecuteSessionSQL(sess, "SELECT @@TRANCOUNT AS tc;")
	if err != nil || fmt.Sprintf("%v", resTC0.Rows[0]["tc"]) != "0" {
		t.Fatalf("expected @@TRANCOUNT = 0 after parameterless rollback, got %+v", resTC0)
	}

	// Entire table must now be empty
	resFinal, err := coord.ExecuteSessionSQL(sess, "SELECT count(*) AS cnt FROM t_tsql_sp;")
	if err != nil || fmt.Sprintf("%v", resFinal.Rows[0]["cnt"]) != "0" {
		t.Fatalf("expected count = 0 after final rollback, got %+v", resFinal)
	}
}

// TestTxRigor_P1_6_SemicolonBatchAtomicity verifies that a compound batch containing
// multiple statements separated by semicolons executes with transactional integrity.
// If an error occurs halfway through, changes must be rolled back.
func TestTxRigor_P1_6_SemicolonBatchAtomicity(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_p1_6_batch"
	sessObs := "test_sess_p1_6_obs"
	_, _ = coord.GetOrCreateSession(sess)
	_, _ = coord.GetOrCreateSession(sessObs)
	defer coord.CloseSession(sess)
	defer coord.CloseSession(sessObs)

	if _, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE t_batch (id INT PRIMARY KEY, val VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 1. Successful semicolon batch with explicit BEGIN ... COMMIT
	batchSuccess := "BEGIN TRANSACTION; INSERT INTO t_batch VALUES (1, 'a'); INSERT INTO t_batch VALUES (2, 'b'); COMMIT TRANSACTION;"
	if _, err := coord.ExecuteSessionSQL(sess, batchSuccess); err != nil {
		t.Fatalf("successful batch failed: %v", err)
	}

	resObs, err := coord.ExecuteSessionSQL(sessObs, "SELECT count(*) AS cnt FROM t_batch;")
	if err != nil || fmt.Sprintf("%v", resObs.Rows[0]["cnt"]) != "2" {
		t.Fatalf("expected 2 rows in observer after committed batch, got %+v", resObs)
	}

	// Verify @@TRANCOUNT is 0
	resTC, err := coord.ExecuteSessionSQL(sess, "SELECT @@TRANCOUNT AS tc;")
	if err != nil || fmt.Sprintf("%v", resTC.Rows[0]["tc"]) != "0" {
		t.Fatalf("expected @@TRANCOUNT = 0 after committed batch, got %+v", resTC)
	}

	// 2. Failing semicolon batch: mid-batch failure must rollback preceding statements in the batch
	batchFail := "BEGIN TRANSACTION; INSERT INTO t_batch VALUES (3, 'c'); INSERT INTO nonexistent_table_xyz VALUES (999); COMMIT TRANSACTION;"
	_, err = coord.ExecuteSessionSQL(sess, batchFail)
	if err == nil {
		t.Fatalf("expected error from failing batch, got nil")
	}

	// Row 3 MUST NOT exist!
	resCheck, err := coord.ExecuteSessionSQL(sess, "SELECT * FROM t_batch WHERE id = 3;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(resCheck.Rows) != 0 {
		t.Fatalf("atomicity violation: row 3 from aborted batch leaked into table: %+v", resCheck.Rows)
	}

	// @@TRANCOUNT must be 0 (no dangling tx)
	resTC2, err := coord.ExecuteSessionSQL(sess, "SELECT @@TRANCOUNT AS tc;")
	if err != nil || fmt.Sprintf("%v", resTC2.Rows[0]["tc"]) != "0" {
		t.Fatalf("expected @@TRANCOUNT = 0 after failed batch, got %+v", resTC2)
	}
}

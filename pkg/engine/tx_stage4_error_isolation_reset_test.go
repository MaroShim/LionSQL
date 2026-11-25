package engine_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"lionsql/pkg/engine"
	"lionsql/pkg/storage"
)

// TestTxRigor_P4_1_PostgresAbortedTransactionGuard25P02 verifies that in PostgreSQL dialect mode,
// an error inside an active transaction marks the transaction as aborted, causing all subsequent
// statements (except ROLLBACK or ROLLBACK TO SAVEPOINT) to be rejected with SQLSTATE 25P02
// ("current transaction is aborted, commands ignored until end of transaction block").
func TestTxRigor_P4_1_PostgresAbortedTransactionGuard25P02(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "pg-sess-p4-1"
	cs, _ := coord.GetOrCreateSession(sess)
	cs.Dialect = engine.DialectPostgres
	defer coord.CloseSession(sess)

	initSQL := `
CREATE TABLE t_pg_guard (id INT PRIMARY KEY, val VARCHAR(50));
INSERT INTO t_pg_guard VALUES (1, 'initial');
`
	if _, err := coord.ExecuteSessionSQL(sess, initSQL); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// 1. Begin transaction in Postgres mode
	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}

	// 2. Perform a successful write
	if _, err := coord.ExecuteSessionSQL(sess, "INSERT INTO t_pg_guard VALUES (2, 'in_tx');"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 3. Trigger a query error inside transaction (non-existent table)
	_, err := coord.ExecuteSessionSQL(sess, "SELECT * FROM nonexistent_table_p4_1;")
	if err == nil {
		t.Fatalf("expected error on querying nonexistent table, but got nil")
	}

	// 4. Subsequent DQL/DML MUST BE REJECTED with 25P02 aborted transaction guard
	_, err = coord.ExecuteSessionSQL(sess, "SELECT * FROM t_pg_guard;")
	if err == nil {
		t.Fatalf("expected 25P02 error for query in aborted transaction, but succeeded")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "current transaction is aborted") {
		t.Fatalf("expected 'current transaction is aborted' error message, got: %v", err)
	}

	_, err = coord.ExecuteSessionSQL(sess, "INSERT INTO t_pg_guard VALUES (3, 'should_fail');")
	if err == nil {
		t.Fatalf("expected 25P02 error for INSERT in aborted transaction, but succeeded")
	}

	// 5. ROLLBACK must succeed and recover the session out of aborted state
	if _, err := coord.ExecuteSessionSQL(sess, "ROLLBACK;"); err != nil {
		t.Fatalf("ROLLBACK on aborted transaction failed: %v", err)
	}

	// 6. After ROLLBACK, session must be back to healthy autocommit state
	res, err := coord.ExecuteSessionSQL(sess, "SELECT val FROM t_pg_guard WHERE id = 1;")
	if err != nil || len(res.Rows) != 1 || res.Rows[0]["val"] != "initial" {
		t.Fatalf("query after rollback failed: res=%+v, err=%v", res, err)
	}

	// Uncommitted row 2 must not exist
	res2, err := coord.ExecuteSessionSQL(sess, "SELECT val FROM t_pg_guard WHERE id = 2;")
	if err != nil || len(res2.Rows) != 0 {
		t.Fatalf("uncommitted row 2 leaked after rollback: res=%+v", res2)
	}
}

// TestTxRigor_P4_2_TSqlXactState3StateTransition verifies T-SQL XACT_STATE() function transitions:
//   0: No active transaction
//   1: Active committable user transaction
//  -1: Active uncommittable (doomed) transaction requiring ROLLBACK
//   0: Restored to 0 after ROLLBACK
func TestTxRigor_P4_2_TSqlXactState3StateTransition(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_p4_2_tsql"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	initSQL := `
CREATE TABLE t_xact_state (id INT PRIMARY KEY, val INT);
INSERT INTO t_xact_state VALUES (1, 100);
`
	if _, err := coord.ExecuteSessionSQL(sess, initSQL); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// 1. In autocommit state, XACT_STATE() must be 0
	res0, err := coord.ExecuteSessionSQL(sess, "SELECT XACT_STATE() AS xs;")
	if err != nil || len(res0.Rows) != 1 || fmt.Sprintf("%v", res0.Rows[0]["xs"]) != "0" {
		t.Fatalf("expected XACT_STATE() = 0 in autocommit, got %+v (err=%v)", res0.Rows, err)
	}

	// 2. In active transaction, XACT_STATE() must be 1
	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN TRANSACTION failed: %v", err)
	}
	res1, err := coord.ExecuteSessionSQL(sess, "SELECT XACT_STATE() AS xs;")
	if err != nil || len(res1.Rows) != 1 || fmt.Sprintf("%v", res1.Rows[0]["xs"]) != "1" {
		t.Fatalf("expected XACT_STATE() = 1 in active transaction, got %+v (err=%v)", res1.Rows, err)
	}

	// 3. Set XACT_ABORT ON and trigger a fatal runtime error (table not found)
	if _, err := coord.ExecuteSessionSQL(sess, "SET XACT_ABORT ON;"); err != nil {
		t.Fatalf("SET XACT_ABORT ON failed: %v", err)
	}
	_, err = coord.ExecuteSessionSQL(sess, "SELECT * FROM nonexistent_table_p4_2;")
	if err == nil {
		t.Fatalf("expected query error, got nil")
	}

	// With XACT_ABORT ON and an error, XACT_STATE() must become -1 (uncommittable)
	resDoomed, err := coord.ExecuteSessionSQL(sess, "SELECT XACT_STATE() AS xs;")
	if err != nil || len(resDoomed.Rows) != 1 || fmt.Sprintf("%v", resDoomed.Rows[0]["xs"]) != "-1" {
		t.Fatalf("expected XACT_STATE() = -1 on doomed transaction, got %+v (err=%v)", resDoomed.Rows, err)
	}

	// 4. In doomed state (-1), COMMIT must fail or trigger auto-rollback
	_, commitErr := coord.ExecuteSessionSQL(sess, "COMMIT TRANSACTION;")
	// Either commit fails or rolls back, XACT_STATE() must return to 0
	if commitErr == nil {
		// If COMMIT was accepted, it should have been converted to rollback
		resAfterCommit, _ := coord.ExecuteSessionSQL(sess, "SELECT XACT_STATE() AS xs;")
		if len(resAfterCommit.Rows) > 0 && fmt.Sprintf("%v", resAfterCommit.Rows[0]["xs"]) != "0" {
			t.Fatalf("expected XACT_STATE() = 0 after COMMIT on doomed transaction, got %v", resAfterCommit.Rows)
		}
	} else {
		// Explicit ROLLBACK required
		if _, err := coord.ExecuteSessionSQL(sess, "ROLLBACK TRANSACTION;"); err != nil {
			t.Fatalf("ROLLBACK failed: %v", err)
		}
	}

	// 5. Restored to 0
	resRestored, err := coord.ExecuteSessionSQL(sess, "SELECT XACT_STATE() AS xs, @@TRANCOUNT AS tc;")
	if err != nil || len(resRestored.Rows) != 1 || fmt.Sprintf("%v", resRestored.Rows[0]["xs"]) != "0" || fmt.Sprintf("%v", resRestored.Rows[0]["tc"]) != "0" {
		t.Fatalf("expected XACT_STATE() = 0 and @@TRANCOUNT = 0 after cleanup, got: %+v", resRestored.Rows)
	}
}

// TestTxRigor_P4_3_ConnectionPoolResetSpResetConnection verifies that when a client session returns
// a dangling uncommitted transaction and open cursors to the connection pool, invoking
// sp_reset_connection (or ResetSessionWithOptions) cleanly rolls back all uncommitted mutations,
// deallocates all cursors, and resets @@TRANCOUNT and XACT_STATE() to 0.
func TestTxRigor_P4_3_ConnectionPoolResetSpResetConnection(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_p4_3_pool"
	cs, _ := coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	initSQL := `
CREATE TABLE t_pool_reset (id INT PRIMARY KEY, name VARCHAR(50));
INSERT INTO t_pool_reset VALUES (1, 'base_record');
`
	if _, err := coord.ExecuteSessionSQL(sess, initSQL); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// 1. Start explicit transaction and insert uncommitted record
	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "INSERT INTO t_pool_reset VALUES (2, 'dangling_uncommitted');"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 2. Declare and open a cursor
	curSQL := `
DECLARE cur_dangling CURSOR FOR SELECT id, name FROM t_pool_reset;
OPEN cur_dangling;
`
	if _, err := coord.ExecuteSessionSQL(sess, curSQL); err != nil {
		t.Fatalf("cursor setup failed: %v", err)
	}

	// Confirm cursor is open in session
	cs.Mu.Lock()
	cur, curExists := cs.Cursors["cur_dangling"]
	cs.Mu.Unlock()
	if !curExists || !cur.IsOpen {
		t.Fatalf("expected cur_dangling to be open before reset")
	}

	// 3. Client returns connection to pool without committing: pool executes sp_reset_connection
	if _, err := coord.ExecuteSessionSQL(sess, "EXEC sp_reset_connection;"); err != nil {
		t.Fatalf("sp_reset_connection failed: %v", err)
	}

	// 4. Verify post-reset guarantees:
	// a) @@TRANCOUNT == 0
	// b) XACT_STATE() == 0
	resCheck, err := coord.ExecuteSessionSQL(sess, "SELECT @@TRANCOUNT AS tc, XACT_STATE() AS xs;")
	if err != nil || len(resCheck.Rows) != 1 || fmt.Sprintf("%v", resCheck.Rows[0]["tc"]) != "0" || fmt.Sprintf("%v", resCheck.Rows[0]["xs"]) != "0" {
		t.Fatalf("expected @@TRANCOUNT=0 and XACT_STATE()=0 after reset, got %+v (err=%v)", resCheck.Rows, err)
	}

	// c) Cursors must be deallocated
	cs.Mu.Lock()
	_, stillExists := cs.Cursors["cur_dangling"]
	cs.Mu.Unlock()
	if stillExists {
		t.Fatalf("cur_dangling still exists in session after sp_reset_connection")
	}

	// d) Uncommitted row 2 must be completely rolled back
	resData, err := coord.ExecuteSessionSQL(sess, "SELECT id, name FROM t_pool_reset ORDER BY id;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(resData.Rows) != 1 || fmt.Sprintf("%v", resData.Rows[0]["id"]) != "1" {
		t.Fatalf("expected only base record (1) after reset, but got: %+v", resData.Rows)
	}
}

// TestTxRigor_P4_4_ClientDisconnectForcesRollbackAndReleasesLocks verifies that when a client abruptly
// disconnects (e.g. TCP reset, process crash) while holding an active transaction with uncommitted updates,
// CloseSession guarantees immediate rollback and row lock release within 1.5s, allowing other sessions
// to query intact data and mutate the row without deadlock.
func TestTxRigor_P4_4_ClientDisconnectForcesRollbackAndReleasesLocks(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sessOwner := "sess_p4_4_owner"
	sessObserver := "sess_p4_4_observer"
	_, _ = coord.GetOrCreateSession(sessOwner)
	_, _ = coord.GetOrCreateSession(sessObserver)
	defer coord.CloseSession(sessObserver)

	initSQL := `
CREATE TABLE t_disc_test (id INT PRIMARY KEY, balance INT);
INSERT INTO t_disc_test VALUES (1, 1000);
`
	if _, err := coord.ExecuteSessionSQL(sessOwner, initSQL); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// 1. Owner begins transaction and modifies balance
	if _, err := coord.ExecuteSessionSQL(sessOwner, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sessOwner, "UPDATE t_disc_test SET balance = 9999 WHERE id = 1;"); err != nil {
		t.Fatalf("UPDATE failed: %v", err)
	}

	// 2. Simulate abrupt client disconnect
	startClose := time.Now()
	coord.CloseSession(sessOwner)
	closeDuration := time.Since(startClose)
	if closeDuration > 1500*time.Millisecond {
		t.Fatalf("CloseSession took %v, exceeded 1.5s SLA", closeDuration)
	}

	// 3. Observer queries the table: balance must be 1000 (uncommitted 9999 was rolled back)
	res, err := coord.ExecuteSessionSQL(sessObserver, "SELECT balance FROM t_disc_test WHERE id = 1;")
	if err != nil || len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["balance"]) != "1000" {
		t.Fatalf("uncommitted balance leaked or query failed: res=%+v (err=%v)", res, err)
	}

	// 4. Observer must be able to immediately write to row 1 without being blocked
	if _, err := coord.ExecuteSessionSQL(sessObserver, "UPDATE t_disc_test SET balance = 2000 WHERE id = 1;"); err != nil {
		t.Fatalf("observer UPDATE failed (row lock was not released): %v", err)
	}
	resUpdated, err := coord.ExecuteSessionSQL(sessObserver, "SELECT balance FROM t_disc_test WHERE id = 1;")
	if err != nil || len(resUpdated.Rows) != 1 || fmt.Sprintf("%v", resUpdated.Rows[0]["balance"]) != "2000" {
		t.Fatalf("expected balance=2000, got: %+v", resUpdated.Rows)
	}
}

// TestTxRigor_P4_5_IdleInTransactionSessionTimeout verifies that sessions configured with
// idle_in_transaction_session_timeout have their idle transactions automatically rolled back
// by the background sweeper (ReapIdleClientSessions).
func TestTxRigor_P4_5_IdleInTransactionSessionTimeout(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sessShort := "sess_p4_5_short"
	sessLong := "sess_p4_5_long"
	sShort, _ := coord.GetOrCreateSession(sessShort)
	sLong, _ := coord.GetOrCreateSession(sessLong)
	defer coord.CloseSession(sessShort)
	defer coord.CloseSession(sessLong)

	initSQL := `
CREATE TABLE t_idle_test (id INT PRIMARY KEY, val VARCHAR(20));
INSERT INTO t_idle_test VALUES (1, 'init');
`
	if _, err := coord.ExecuteSessionSQL(sessShort, initSQL); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// 1. Set explicit low timeout (50ms) on sessShort
	if _, err := coord.ExecuteSessionSQL(sessShort, "SET idle_in_transaction_session_timeout = '50ms';"); err != nil {
		t.Fatalf("SET timeout failed: %v", err)
	}

	// 2. Start transaction on both sessions
	if _, err := coord.ExecuteSessionSQL(sessShort, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN sessShort failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sessShort, "UPDATE t_idle_test SET val = 'short_in_flight' WHERE id = 1;"); err != nil {
		t.Fatalf("UPDATE sessShort failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQL(sessLong, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN sessLong failed: %v", err)
	}

	// Verify both transactions are active
	if sShort.Tx() == nil || sLong.Tx() == nil {
		t.Fatalf("expected both sessions to have active tx")
	}

	// 3. Simulate passage of 60ms idle time on sessShort and sessLong
	sShort.SetLastUsedAtForTest(time.Now().Add(-60 * time.Millisecond))
	sLong.SetLastUsedAtForTest(time.Now().Add(-60 * time.Millisecond))

	// 4. Trigger reaper with default 10m TTL
	reaped := coord.ReapIdleClientSessions(10 * time.Minute)
	if reaped < 1 {
		t.Fatalf("expected at least 1 session tx reaped, got %d", reaped)
	}

	// 5. sessShort must have its transaction forcibly rolled back
	if sShort.Tx() != nil {
		t.Fatalf("expected sessShort transaction to be rolled back after 50ms timeout")
	}

	// 6. sessLong must STILL have its transaction active (10m default threshold not reached)
	if sLong.Tx() == nil {
		t.Fatalf("expected sessLong transaction to remain active")
	}
	_ = coord.RollbackSessionTx(sessLong)
}

// TestTxRigor_P4_6_DoubleCloseAndTeardownIdempotency verifies [RULE:DoubleCloseSafety] and
// [RULE:CleanTeardown]: multiple calls to CloseSession and redundant ROLLBACK commands
// must be completely idempotent and never cause panics or nil dereferences.
func TestTxRigor_P4_6_DoubleCloseAndTeardownIdempotency(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_p4_6_idempotent"
	_, _ = coord.GetOrCreateSession(sess)

	// 1. Redundant rollback on autocommit session must return clean descriptive error without panicking
	for i := 0; i < 3; i++ {
		_, err := coord.ExecuteSessionSQL(sess, "ROLLBACK TRANSACTION;")
		if err == nil || !strings.Contains(err.Error(), "no active transaction") {
			t.Fatalf("iteration %d: expected 'no active transaction' error on redundant autocommit ROLLBACK, got: %v", i, err)
		}
	}

	// 2. Start and rollback transaction
	_ = coord.BeginSessionTx(sess, storage.TxOptions{})
	_ = coord.RollbackSessionTx(sess)
	_ = coord.RollbackSessionTx(sess) // double rollback

	// 3. CloseSession called multiple times in a row
	coord.CloseSession(sess)
	coord.CloseSession(sess)
	coord.CloseSession(sess)

	// 4. Close on non-existent session
	coord.CloseSession("non_existent_sess_xyz")
}

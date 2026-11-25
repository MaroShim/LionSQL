package engine_test

import (
	"fmt"
	"testing"
)

// TestTxRigor_P3_1_WithoutHoldCursorClosedOnCommit verifies that standard cursors (WITHOUT HOLD)
// are automatically invalidated and closed upon transaction COMMIT per SQL standards.
func TestTxRigor_P3_1_WithoutHoldCursorClosedOnCommit(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_p3_1"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	initSQL := `
CREATE TABLE t_cur_p3_1 (id INT PRIMARY KEY, name VARCHAR(20));
INSERT INTO t_cur_p3_1 VALUES (1, 'Alice'), (2, 'Bob'), (3, 'Charlie');
`
	if _, err := coord.ExecuteSessionSQL(sess, initSQL); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// 1. Begin transaction and declare WITHOUT HOLD cursor
	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "DECLARE cur_std CURSOR WITHOUT HOLD FOR SELECT id, name FROM t_cur_p3_1 ORDER BY id;"); err != nil {
		t.Fatalf("DECLARE CURSOR failed: %v", err)
	}

	// Fetch 1 row inside transaction
	res1, err := coord.ExecuteSessionSQL(sess, "FETCH 1 FROM cur_std;")
	if err != nil || len(res1.Rows) != 1 || res1.Rows[0]["name"] != "Alice" {
		t.Fatalf("FETCH before commit failed: res=%+v, err=%v", res1, err)
	}

	// 2. COMMIT TRANSACTION
	if _, err := coord.ExecuteSessionSQL(sess, "COMMIT TRANSACTION;"); err != nil {
		t.Fatalf("COMMIT failed: %v", err)
	}

	// 3. FETCH after COMMIT MUST FAIL CLOSED because cur_std was NOT declared WITH HOLD
	_, err = coord.ExecuteSessionSQL(sess, "FETCH 1 FROM cur_std;")
	if err == nil {
		t.Fatalf("expected error fetching from WITHOUT HOLD cursor after COMMIT, but query succeeded")
	}

	// Verify session cursor internal state
	cs, err := coord.GetOrCreateSession(sess)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	cs.Mu.Lock()
	cur, exists := cs.Cursors["cur_std"]
	cs.Mu.Unlock()
	if !exists {
		t.Fatalf("expected cur_std to remain registered in session map")
	}
	if cur.IsOpen || !cur.IsClosed || cur.Rows != nil {
		t.Fatalf("cursor state invariant broken: IsOpen=%v, IsClosed=%v, Rows=%v", cur.IsOpen, cur.IsClosed, cur.Rows)
	}
}

// TestTxRigor_P3_2_WithHoldCursorSurvivesCommitAndContinuousFetch verifies that cursors
// explicitly declared WITH HOLD survive COMMIT and continue traversal seamlessly.
func TestTxRigor_P3_2_WithHoldCursorSurvivesCommitAndContinuousFetch(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_p3_2"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	initSQL := `
CREATE TABLE t_cur_p3_2 (id INT PRIMARY KEY, val INT);
INSERT INTO t_cur_p3_2 VALUES (1, 10), (2, 20), (3, 30), (4, 40), (5, 50);
`
	if _, err := coord.ExecuteSessionSQL(sess, initSQL); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// 1. Begin transaction, declare WITH HOLD cursor, and fetch 1 row
	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "DECLARE cur_hold CURSOR WITH HOLD FOR SELECT id, val FROM t_cur_p3_2 ORDER BY id;"); err != nil {
		t.Fatalf("DECLARE WITH HOLD failed: %v", err)
	}

	res1, err := coord.ExecuteSessionSQL(sess, "FETCH 1 FROM cur_hold;")
	if err != nil || len(res1.Rows) != 1 || fmt.Sprintf("%v", res1.Rows[0]["val"]) != "10" {
		t.Fatalf("FETCH 1 failed: %+v (err=%v)", res1, err)
	}

	// 2. COMMIT TRANSACTION: cursor MUST survive
	if _, err := coord.ExecuteSessionSQL(sess, "COMMIT TRANSACTION;"); err != nil {
		t.Fatalf("COMMIT failed: %v", err)
	}

	// 3. FETCH after COMMIT: must fetch next 2 rows (id=2, id=3)
	res2, err := coord.ExecuteSessionSQL(sess, "FETCH 2 FROM cur_hold;")
	if err != nil || len(res2.Rows) != 2 {
		t.Fatalf("FETCH 2 after commit failed: res=%+v, err=%v", res2, err)
	}
	if fmt.Sprintf("%v", res2.Rows[0]["val"]) != "20" || fmt.Sprintf("%v", res2.Rows[1]["val"]) != "30" {
		t.Fatalf("unexpected rows after commit: %+v", res2.Rows)
	}

	// 4. Start second transaction, perform another FETCH, then COMMIT again
	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN 2 failed: %v", err)
	}
	res3, err := coord.ExecuteSessionSQL(sess, "FETCH 1 FROM cur_hold;")
	if err != nil || len(res3.Rows) != 1 || fmt.Sprintf("%v", res3.Rows[0]["val"]) != "40" {
		t.Fatalf("FETCH in tx2 failed: res=%+v, err=%v", res3, err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "COMMIT TRANSACTION;"); err != nil {
		t.Fatalf("COMMIT 2 failed: %v", err)
	}

	// 5. Final fetch to EOF
	res4, err := coord.ExecuteSessionSQL(sess, "FETCH 1 FROM cur_hold;")
	if err != nil || len(res4.Rows) != 1 || fmt.Sprintf("%v", res4.Rows[0]["val"]) != "50" {
		t.Fatalf("FETCH 4 failed: %+v", res4)
	}

	// Explicit CLOSE
	if _, err := coord.ExecuteSessionSQL(sess, "CLOSE cur_hold;"); err != nil {
		t.Fatalf("CLOSE cur_hold failed: %v", err)
	}
}

// TestTxRigor_P3_3_AllCursorsAnnihilatedOnRollback verifies that on ROLLBACK TRANSACTION,
// ALL cursors (including WITH HOLD cursors) are unconditionally destroyed to prevent snapshot contamination.
func TestTxRigor_P3_3_AllCursorsAnnihilatedOnRollback(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_p3_3"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	initSQL := `
CREATE TABLE t_cur_p3_3 (id INT PRIMARY KEY, name VARCHAR(20));
INSERT INTO t_cur_p3_3 VALUES (1, 'Alpha'), (2, 'Beta');
`
	if _, err := coord.ExecuteSessionSQL(sess, initSQL); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// 1. Begin transaction, declare BOTH standard and WITH HOLD cursors
	if _, err := coord.ExecuteSessionSQL(sess, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "DECLARE cur_normal CURSOR FOR SELECT id FROM t_cur_p3_3;"); err != nil {
		t.Fatalf("DECLARE normal failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "DECLARE cur_hold_target CURSOR WITH HOLD FOR SELECT id FROM t_cur_p3_3;"); err != nil {
		t.Fatalf("DECLARE WITH HOLD failed: %v", err)
	}

	// Mutate table in tx
	if _, err := coord.ExecuteSessionSQL(sess, "INSERT INTO t_cur_p3_3 VALUES (3, 'Gamma');"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 2. ROLLBACK TRANSACTION
	if _, err := coord.ExecuteSessionSQL(sess, "ROLLBACK TRANSACTION;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// 3. Both cursors MUST be closed/invalidated
	_, err := coord.ExecuteSessionSQL(sess, "FETCH 1 FROM cur_normal;")
	if err == nil {
		t.Fatalf("expected error fetching from cur_normal after rollback, got nil")
	}

	_, err = coord.ExecuteSessionSQL(sess, "FETCH 1 FROM cur_hold_target;")
	if err == nil {
		t.Fatalf("CRITICAL: expected error fetching from WITH HOLD cursor after ROLLBACK (snapshot contamination protection), but query succeeded")
	}

	// Verify session cursor internal state: both must have IsOpen=false, IsClosed=true
	cs, err := coord.GetOrCreateSession(sess)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	cs.Mu.Lock()
	defer cs.Mu.Unlock()
	for name, cur := range cs.Cursors {
		if cur.IsOpen || !cur.IsClosed || cur.Rows != nil {
			t.Fatalf("cursor %s not cleanly destroyed on rollback: IsOpen=%v, IsClosed=%v, Rows=%v", name, cur.IsOpen, cur.IsClosed, cur.Rows)
		}
	}
}

// TestTxRigor_P3_4_StorageCursorHandleHygieneAndDoubleCloseSafety verifies [RULE:HandleHygiene]
// and [RULE:DoubleCloseSafety] across session reset, close all, and cursor deallocation.
func TestTxRigor_P3_4_StorageCursorHandleHygieneAndDoubleCloseSafety(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_p3_4"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	initSQL := `
CREATE TABLE t_cur_p3_4 (id INT PRIMARY KEY);
INSERT INTO t_cur_p3_4 VALUES (1), (2), (3);
`
	if _, err := coord.ExecuteSessionSQL(sess, initSQL); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// 1. Declare 3 cursors
	if _, err := coord.ExecuteSessionSQL(sess, "DECLARE c1 CURSOR FOR SELECT id FROM t_cur_p3_4;"); err != nil {
		t.Fatalf("DECLARE c1 failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "DECLARE c2 CURSOR FOR SELECT id FROM t_cur_p3_4;"); err != nil {
		t.Fatalf("DECLARE c2 failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "DECLARE c3 CURSOR FOR SELECT id FROM t_cur_p3_4;"); err != nil {
		t.Fatalf("DECLARE c3 failed: %v", err)
	}

	// 2. Double CLOSE c1: should be idempotent and must not panic
	if _, err := coord.ExecuteSessionSQL(sess, "CLOSE c1;"); err != nil {
		t.Fatalf("first CLOSE c1 failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "CLOSE c1;"); err != nil {
		t.Fatalf("second CLOSE c1 (double close) failed: %v", err)
	}

	// 3. CLOSE ALL: should close c2 and c3 cleanly
	if _, err := coord.ExecuteSessionSQL(sess, "CLOSE ALL;"); err != nil {
		t.Fatalf("CLOSE ALL failed: %v", err)
	}

	// 4. Session Close idempotency: closing session with open/closed cursors multiple times
	coord.CloseSession(sess)
	coord.CloseSession(sess)
}

// TestTxRigor_P3_5_TSqlCursorSyntaxOpenFetchCloseDeallocate verifies full T-SQL cursor
// syntax execution: DECLARE ... CURSOR, OPEN, FETCH NEXT INTO @var, CLOSE, and DEALLOCATE.
func TestTxRigor_P3_5_TSqlCursorSyntaxOpenFetchCloseDeallocate(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_p3_5_tsql"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	initSQL := `
CREATE TABLE t_cur_p3_5 (id INT PRIMARY KEY, title VARCHAR(50));
INSERT INTO t_cur_p3_5 VALUES (1, 'Book A'), (2, 'Book B');
`
	if _, err := coord.ExecuteSessionSQL(sess, initSQL); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	tsqlBatch := `
DECLARE @bid INT, @btitle VARCHAR(50);
DECLARE cur_book CURSOR FOR SELECT id, title FROM t_cur_p3_5 ORDER BY id;
OPEN cur_book;
FETCH NEXT FROM cur_book INTO @bid, @btitle;
CLOSE cur_book;
DEALLOCATE cur_book;
SELECT @bid AS fetched_id, @btitle AS fetched_title;
`
	res, err := coord.ExecuteSessionSQL(sess, tsqlBatch)
	if err != nil {
		t.Fatalf("T-SQL cursor batch failed: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["fetched_id"]) != "1" || fmt.Sprintf("%v", res.Rows[0]["fetched_title"]) != "Book A" {
		t.Fatalf("unexpected fetched variables from T-SQL cursor: %+v", res.Rows)
	}

	// Deallocated cursor must no longer exist in session
	cs, err := coord.GetOrCreateSession(sess)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	cs.Mu.Lock()
	_, exists := cs.Cursors["cur_book"]
	cs.Mu.Unlock()
	if exists {
		t.Fatalf("cursor cur_book still exists after DEALLOCATE")
	}
}

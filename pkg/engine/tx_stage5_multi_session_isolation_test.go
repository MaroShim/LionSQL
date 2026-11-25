package engine_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"lionsql/pkg/storage"
)

// TestTxRigor_P5_1_SingleSessionConcurrencySerialization verifies that when multiple goroutines
// concurrently execute queries on the SAME session ID, ClientSession.Mu strictly serializes execution
// so that no updates are lost (Lost Update = 0).
func TestTxRigor_P5_1_SingleSessionConcurrencySerialization(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sessID := "sess_p5_1_shared"
	if _, err := coord.GetOrCreateSession(sessID); err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	defer coord.CloseSession(sessID)

	initSQL := `
CREATE TABLE t_p5_counter (id INT PRIMARY KEY, val INT);
INSERT INTO t_p5_counter VALUES (1, 0);
`
	if _, err := coord.ExecuteSessionSQL(sessID, initSQL); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	const numGoroutines = 50
	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines)

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			_, err := coord.ExecuteSessionSQL(sessID, "UPDATE t_p5_counter SET val = val + 1 WHERE id = 1;")
			if err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent update on shared session failed: %v", err)
	}

	// Verify final value is exactly 50
	res, err := coord.ExecuteSessionSQL(sessID, "SELECT val FROM t_p5_counter WHERE id = 1;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	gotVal := fmt.Sprintf("%v", res.Rows[0]["val"])
	if gotVal != "50" {
		t.Fatalf("expected val=50 after 50 concurrent updates, got val=%s (lost update detected!)", gotVal)
	}
}

// TestTxRigor_P5_2_NonBlockingReadSnapshotIsolation verifies that a concurrent reader in Session 2
// is never blocked by uncommitted writes in Session 1, and always reads the committed snapshot.
func TestTxRigor_P5_2_NonBlockingReadSnapshotIsolation(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess1 := "sess_p5_2_writer"
	sess2 := "sess_p5_2_reader"
	if _, err := coord.GetOrCreateSession(sess1); err != nil {
		t.Fatalf("GetOrCreateSession sess1 failed: %v", err)
	}
	defer coord.CloseSession(sess1)

	if _, err := coord.GetOrCreateSession(sess2); err != nil {
		t.Fatalf("GetOrCreateSession sess2 failed: %v", err)
	}
	defer coord.CloseSession(sess2)

	initSQL := `
CREATE TABLE t_p5_nblocks (id INT PRIMARY KEY, val VARCHAR(50));
INSERT INTO t_p5_nblocks VALUES (1, 'orig_1'), (2, 'orig_2');
`
	if _, err := coord.ExecuteSessionSQL(sess1, initSQL); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// 1. Session 1 begins transaction and mutates data (UNCOMMITTED)
	if _, err := coord.ExecuteSessionSQL(sess1, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess1, "UPDATE t_p5_nblocks SET val = 'mod_1' WHERE id = 1;"); err != nil {
		t.Fatalf("UPDATE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess1, "INSERT INTO t_p5_nblocks VALUES (3, 'uncommitted_3');"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 2. Session 2 reads data concurrently: must NOT block, must see only committed snapshot (rows 1 & 2)
	res2, err := coord.ExecuteSessionSQL(sess2, "SELECT id, val FROM t_p5_nblocks ORDER BY id;")
	if err != nil {
		t.Fatalf("Session 2 SELECT failed: %v", err)
	}
	if len(res2.Rows) != 2 {
		t.Fatalf("Session 2 expected 2 committed rows, got %d: %+v", len(res2.Rows), res2.Rows)
	}
	val1 := fmt.Sprintf("%v", res2.Rows[0]["val"])
	if val1 != "orig_1" {
		t.Fatalf("Session 2 expected 'orig_1', got uncommitted value: %v", val1)
	}
	val2 := fmt.Sprintf("%v", res2.Rows[1]["val"])
	if val2 != "orig_2" {
		t.Fatalf("Session 2 expected 'orig_2', got: %v", val2)
	}

	// 3. Session 1 commits
	if _, err := coord.ExecuteSessionSQL(sess1, "COMMIT TRANSACTION;"); err != nil {
		t.Fatalf("COMMIT failed: %v", err)
	}

	// 4. Session 2 reads again: now sees all 3 rows with updated values
	res2After, err := coord.ExecuteSessionSQL(sess2, "SELECT id, val FROM t_p5_nblocks ORDER BY id;")
	if err != nil {
		t.Fatalf("Session 2 SELECT after commit failed: %v", err)
	}
	if len(res2After.Rows) != 3 {
		t.Fatalf("Session 2 expected 3 rows after commit, got %d: %+v", len(res2After.Rows), res2After.Rows)
	}
	if fmt.Sprintf("%v", res2After.Rows[0]["val"]) != "mod_1" {
		t.Fatalf("Session 2 expected row 1 to be 'mod_1', got: %v", res2After.Rows[0]["val"])
	}
	if fmt.Sprintf("%v", res2After.Rows[2]["val"]) != "uncommitted_3" {
		t.Fatalf("Session 2 expected row 3 to be 'uncommitted_3', got: %v", res2After.Rows[2]["val"])
	}
}

// TestTxRigor_P5_3_WriteWriteConflictFastFail verifies that when Session 1 holds an uncommitted modification
// on a row, Session 2's attempt to modify the same row fails fast with ErrWriteConflict without deadlocking.
func TestTxRigor_P5_3_WriteWriteConflictFastFail(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess1 := "sess_p5_3_tx1"
	sess2 := "sess_p5_3_tx2"
	if _, err := coord.GetOrCreateSession(sess1); err != nil {
		t.Fatalf("GetOrCreateSession sess1 failed: %v", err)
	}
	defer coord.CloseSession(sess1)

	if _, err := coord.GetOrCreateSession(sess2); err != nil {
		t.Fatalf("GetOrCreateSession sess2 failed: %v", err)
	}
	defer coord.CloseSession(sess2)

	initSQL := `
CREATE TABLE t_p5_conflict (id INT PRIMARY KEY, val INT);
INSERT INTO t_p5_conflict VALUES (1, 100);
`
	if _, err := coord.ExecuteSessionSQL(sess1, initSQL); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// 1. Session 1 begins transaction and updates row 1
	if _, err := coord.ExecuteSessionSQL(sess1, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess1, "UPDATE t_p5_conflict SET val = 101 WHERE id = 1;"); err != nil {
		t.Fatalf("UPDATE failed: %v", err)
	}

	// 2. Session 2 attempts to UPDATE the same row: must fail fast with write conflict
	_, err2 := coord.ExecuteSessionSQL(sess2, "UPDATE t_p5_conflict SET val = 102 WHERE id = 1;")
	if err2 == nil {
		t.Fatalf("expected write conflict error on Session 2, but UPDATE succeeded")
	}
	errStr := strings.ToLower(err2.Error())
	isConflict := storage.IsWriteConflict(err2) ||
		strings.Contains(errStr, "conflict") ||
		strings.Contains(errStr, "serialization") ||
		strings.Contains(errStr, "cannot serialize")
	if !isConflict {
		t.Fatalf("expected write conflict error on Session 2, got: %v", err2)
	}

	// 3. Session 1 rolls back, leaving initial row (1, 100)
	if _, err := coord.ExecuteSessionSQL(sess1, "ROLLBACK TRANSACTION;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	res, err := coord.ExecuteSessionSQL(sess2, "SELECT val FROM t_p5_conflict WHERE id = 1;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if fmt.Sprintf("%v", res.Rows[0]["val"]) != "100" {
		t.Fatalf("expected val=100 after rollback, got: %v", res.Rows[0]["val"])
	}
}

// TestTxRigor_P5_4_IsolationLevelTransitionAndInspection verifies that SET TRANSACTION ISOLATION LEVEL
// supports valid levels (READ COMMITTED, SNAPSHOT, etc.), reflects in DBCC USEROPTIONS and SHOW commands,
// and rejects invalid isolation level keywords.
func TestTxRigor_P5_4_IsolationLevelTransitionAndInspection(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sessID := "sess_p5_4_iso"
	if _, err := coord.GetOrCreateSession(sessID); err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	defer coord.CloseSession(sessID)

	// 1. SET TRANSACTION ISOLATION LEVEL READ COMMITTED
	if _, err := coord.ExecuteSessionSQL(sessID, "SET TRANSACTION ISOLATION LEVEL READ COMMITTED;"); err != nil {
		t.Fatalf("SET READ COMMITTED failed: %v", err)
	}

	// Check DBCC USEROPTIONS
	dbccRes, err := coord.ExecuteSessionSQL(sessID, "DBCC USEROPTIONS;")
	if err != nil {
		t.Fatalf("DBCC USEROPTIONS failed: %v", err)
	}
	var foundIso string
	for _, row := range dbccRes.Rows {
		if fmt.Sprintf("%v", row["Set Option"]) == "isolation level" {
			foundIso = strings.ToLower(fmt.Sprintf("%v", row["Value"]))
		}
	}
	if foundIso != "read committed" {
		t.Fatalf("expected DBCC isolation level 'read committed', got: %s", foundIso)
	}

	// 2. SET TRANSACTION ISOLATION LEVEL SNAPSHOT
	if _, err := coord.ExecuteSessionSQL(sessID, "SET TRANSACTION ISOLATION LEVEL SNAPSHOT;"); err != nil {
		t.Fatalf("SET SNAPSHOT failed: %v", err)
	}

	showRes, err := coord.ExecuteSessionSQL(sessID, "SHOW TRANSACTION ISOLATION LEVEL;")
	if err != nil {
		t.Fatalf("SHOW TRANSACTION ISOLATION LEVEL failed: %v", err)
	}
	if len(showRes.Rows) > 0 {
		got := strings.ToLower(fmt.Sprintf("%v", showRes.Rows[0]["transaction isolation level"]))
		if got != "snapshot" {
			t.Fatalf("expected SHOW isolation level 'snapshot', got: %s", got)
		}
	}

	// 3. Invalid isolation level must fail
	_, err = coord.ExecuteSessionSQL(sessID, "SET TRANSACTION ISOLATION LEVEL UNKNOWN_LEVEL_XYZ;")
	if err == nil {
		t.Fatalf("expected error on invalid isolation level, but succeeded")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "invalid transaction isolation level") {
		t.Fatalf("expected invalid isolation level error message, got: %v", err)
	}
}

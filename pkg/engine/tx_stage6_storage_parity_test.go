package engine_test

import (
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// runTxParityTestSuite executes identical multi-statement transaction, savepoint rollback,
// non-blocking snapshot read, and write-write conflict scenarios against an abstract storage.Engine
// to prove 100% behavioral parity between In-Memory OCC and WiredTiger MVCC.
func runTxParityTestSuite(t *testing.T, eng storage.Engine) {
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sess1 := "parity_sess1"
	sess2 := "parity_sess2"
	if _, err := coord.GetOrCreateSession(sess1); err != nil {
		t.Fatalf("GetOrCreateSession sess1 failed: %v", err)
	}
	defer coord.CloseSession(sess1)
	if _, err := coord.GetOrCreateSession(sess2); err != nil {
		t.Fatalf("GetOrCreateSession sess2 failed: %v", err)
	}
	defer coord.CloseSession(sess2)

	initSQL := `
CREATE TABLE t_parity (id INT PRIMARY KEY, val INT, note VARCHAR(50));
INSERT INTO t_parity VALUES (1, 100, 'initial');
`
	if _, err := coord.ExecuteSessionSQL(sess1, initSQL); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// 1. Scenario 1: Multi-statement transaction rollback atomicity
	if _, err := coord.ExecuteSessionSQL(sess1, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess1, "UPDATE t_parity SET val = 200, note = 'uncommitted' WHERE id = 1;"); err != nil {
		t.Fatalf("UPDATE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess1, "INSERT INTO t_parity VALUES (2, 300, 'uncommitted_row');"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess1, "ROLLBACK TRANSACTION;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// Verify rollback restored initial state
	res1, err := coord.ExecuteSessionSQL(sess1, "SELECT id, val, note FROM t_parity ORDER BY id;")
	if err != nil {
		t.Fatalf("SELECT after rollback failed: %v", err)
	}
	if len(res1.Rows) != 1 {
		t.Fatalf("expected 1 row after rollback, got %d", len(res1.Rows))
	}
	if fmt.Sprintf("%v", res1.Rows[0]["val"]) != "100" || fmt.Sprintf("%v", res1.Rows[0]["note"]) != "initial" {
		t.Fatalf("expected initial values (100, 'initial'), got %+v", res1.Rows[0])
	}

	// 2. Scenario 2: Savepoint partial rollback
	if _, err := coord.ExecuteSessionSQL(sess1, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess1, "UPDATE t_parity SET val = 500 WHERE id = 1;"); err != nil {
		t.Fatalf("UPDATE before savepoint failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess1, "SAVEPOINT sp1;"); err != nil {
		t.Fatalf("SAVEPOINT failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess1, "UPDATE t_parity SET val = 999 WHERE id = 1;"); err != nil {
		t.Fatalf("UPDATE after savepoint failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess1, "INSERT INTO t_parity VALUES (3, 777, 'should_be_undone');"); err != nil {
		t.Fatalf("INSERT after savepoint failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess1, "ROLLBACK TRANSACTION TO SAVEPOINT sp1;"); err != nil {
		t.Fatalf("ROLLBACK TO SAVEPOINT failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess1, "COMMIT TRANSACTION;"); err != nil {
		t.Fatalf("COMMIT failed: %v", err)
	}

	res2, err := coord.ExecuteSessionSQL(sess1, "SELECT id, val FROM t_parity ORDER BY id;")
	if err != nil {
		t.Fatalf("SELECT after savepoint commit failed: %v", err)
	}
	if len(res2.Rows) != 1 {
		t.Fatalf("expected 1 row (row 3 undone), got %d", len(res2.Rows))
	}
	if fmt.Sprintf("%v", res2.Rows[0]["val"]) != "500" {
		t.Fatalf("expected row 1 val=500 after savepoint rollback, got: %v", res2.Rows[0]["val"])
	}

	// 3. Scenario 3: Non-blocking read snapshot isolation
	if _, err := coord.ExecuteSessionSQL(sess1, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess1, "UPDATE t_parity SET val = 600 WHERE id = 1;"); err != nil {
		t.Fatalf("UPDATE in tx failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess1, "INSERT INTO t_parity VALUES (4, 400, 'in_flight');"); err != nil {
		t.Fatalf("INSERT in tx failed: %v", err)
	}

	// Session 2 reads concurrently: must see committed state (only row 1 with val=500)
	resReader, err := coord.ExecuteSessionSQL(sess2, "SELECT id, val FROM t_parity ORDER BY id;")
	if err != nil {
		t.Fatalf("concurrent reader SELECT failed: %v", err)
	}
	if len(resReader.Rows) != 1 {
		t.Fatalf("reader expected 1 committed row, got %d", len(resReader.Rows))
	}
	if fmt.Sprintf("%v", resReader.Rows[0]["val"]) != "500" {
		t.Fatalf("reader expected val=500, got %v", resReader.Rows[0]["val"])
	}

	// 4. Scenario 4: Write-write conflict detection
	_, errConflict := coord.ExecuteSessionSQL(sess2, "UPDATE t_parity SET val = 888 WHERE id = 1;")
	if errConflict == nil {
		t.Fatalf("expected write conflict error on Session 2, but succeeded")
	}
	isConflict := storage.IsWriteConflict(errConflict) ||
		strings.Contains(strings.ToLower(errConflict.Error()), "conflict") ||
		strings.Contains(strings.ToLower(errConflict.Error()), "serialization")
	if !isConflict {
		t.Fatalf("expected write conflict error, got: %v", errConflict)
	}

	// Session 1 commits
	if _, err := coord.ExecuteSessionSQL(sess1, "COMMIT TRANSACTION;"); err != nil {
		t.Fatalf("COMMIT failed: %v", err)
	}

	// Now Session 2 reads and sees updated state (row 1 is 600, row 4 is 400)
	resFinal, err := coord.ExecuteSessionSQL(sess2, "SELECT id, val FROM t_parity ORDER BY id;")
	if err != nil {
		t.Fatalf("final SELECT failed: %v", err)
	}
	if len(resFinal.Rows) != 2 {
		t.Fatalf("expected 2 rows after commit, got %d", len(resFinal.Rows))
	}
	if fmt.Sprintf("%v", resFinal.Rows[0]["val"]) != "600" || fmt.Sprintf("%v", resFinal.Rows[1]["val"]) != "400" {
		t.Fatalf("unexpected final row values: %+v", resFinal.Rows)
	}
}

// TestTxRigor_P6_1_StorageBackendParity_MemEngine verifies the transaction parity suite on In-Memory OCC.
func TestTxRigor_P6_1_StorageBackendParity_MemEngine(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	runTxParityTestSuite(t, eng)
}

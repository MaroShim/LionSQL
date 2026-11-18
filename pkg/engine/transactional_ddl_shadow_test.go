package engine

import (
	"fmt"
	"testing"
)

// TestTransactionalDDL_ZeroDirtyDDLRead verifies that when Session 1 drops a table
// within an uncommitted transaction:
// 1) Session 1 sees the table as dropped (via ShadowCatalog).
// 2) Concurrent Session 2 STILL sees and queries the table cleanly (Zero Dirty DDL Read).
// 3) Session 1 rollback restores visibility immediately without physical disk renames.
// 4) Session 1 commit flushes the deferred drop to physical storage.
func TestTransactionalDDL_ZeroDirtyDDLRead(t *testing.T) {
	coord, cleanup := setupTestCoord(t)
	defer cleanup()

	sess1 := "ddl-sess-1"
	sess2 := "ddl-sess-2"

	// 1. Create table and populate sample rows
	if _, err := coord.ExecuteSessionSQL(sess1, "CREATE TABLE ddl_orders (id INT PRIMARY KEY, amount INT);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess1, "INSERT INTO ddl_orders VALUES (1, 100), (2, 200);"); err != nil {
		t.Fatalf("INSERT into ddl_orders failed: %v", err)
	}

	// Verify both sessions see ddl_orders initially
	resInit1, err := coord.ExecuteSessionSQL(sess1, "SELECT COUNT(*) AS cnt FROM ddl_orders;")
	if err != nil || fmt.Sprintf("%v", resInit1.Rows[0]["cnt"]) != "2" {
		t.Fatalf("sess1 initial query failed: err=%v res=%+v", err, resInit1)
	}
	resInit2, err := coord.ExecuteSessionSQL(sess2, "SELECT COUNT(*) AS cnt FROM ddl_orders;")
	if err != nil || fmt.Sprintf("%v", resInit2.Rows[0]["cnt"]) != "2" {
		t.Fatalf("sess2 initial query failed: err=%v res=%+v", err, resInit2)
	}

	// 2. Session 1 begins transaction and drops ddl_orders
	if _, err := coord.ExecuteSessionSQL(sess1, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("sess1 BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess1, "DROP TABLE ddl_orders;"); err != nil {
		t.Fatalf("sess1 DROP TABLE failed: %v", err)
	}

	// 2.1 Session 1 must perceive ddl_orders as dropped (via ShadowCatalog)
	_, errSess1Query := coord.ExecuteSessionSQL(sess1, "SELECT * FROM ddl_orders;")
	if errSess1Query == nil {
		t.Fatalf("expected error querying dropped table in sess1, got nil")
	}

	// 2.2 CRITICAL: Concurrent Session 2 MUST STILL SEE ddl_orders! (Zero Dirty DDL Read)
	resSess2, errSess2Query := coord.ExecuteSessionSQL(sess2, "SELECT COUNT(*) AS cnt FROM ddl_orders;")
	if errSess2Query != nil {
		t.Fatalf("CRITICAL BUG (Dirty DDL Read): sess2 failed to query uncommitted dropped table: %v", errSess2Query)
	}
	if len(resSess2.Rows) != 1 || fmt.Sprintf("%v", resSess2.Rows[0]["cnt"]) != "2" {
		t.Fatalf("sess2 expected 2 rows, got %+v", resSess2.Rows)
	}

	// 3. Session 1 rolls back: table is restored for Session 1 instantly (O(1) memory discard)
	if _, err := coord.ExecuteSessionSQL(sess1, "ROLLBACK;"); err != nil {
		t.Fatalf("sess1 ROLLBACK failed: %v", err)
	}

	resSess1Restored, err := coord.ExecuteSessionSQL(sess1, "SELECT COUNT(*) AS cnt FROM ddl_orders;")
	if err != nil || fmt.Sprintf("%v", resSess1Restored.Rows[0]["cnt"]) != "2" {
		t.Fatalf("sess1 after rollback expected 2 rows, got err=%v res=%+v", err, resSess1Restored)
	}

	// 4. Session 1 begins transaction, drops, and COMMITS
	if _, err := coord.ExecuteSessionSQL(sess1, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("sess1 second BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess1, "DROP TABLE ddl_orders;"); err != nil {
		t.Fatalf("sess1 second DROP TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess1, "COMMIT;"); err != nil {
		t.Fatalf("sess1 second COMMIT failed: %v", err)
	}

	// 5. Post-commit: table is now physically dropped for ALL sessions
	_, errPostCommit1 := coord.ExecuteSessionSQL(sess1, "SELECT * FROM ddl_orders;")
	if errPostCommit1 == nil {
		t.Fatalf("expected error in sess1 after commit, got nil")
	}
	_, errPostCommit2 := coord.ExecuteSessionSQL(sess2, "SELECT * FROM ddl_orders;")
	if errPostCommit2 == nil {
		t.Fatalf("expected error in sess2 after commit, got nil")
	}
}

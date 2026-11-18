//go:build cgo && wiredtiger

package engine

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/wiredtiger"
)

func TestWT_Concurrent_UncommittedWrite_ConflictFailsFast(t *testing.T) {
	dir := t.TempDir()
	eng := wiredtiger.New()
	if err := eng.Open(dir); err != nil {
		t.Fatalf("wiredtiger.Open failed: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE wt_t (id INT PRIMARY KEY, val INT)"); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO wt_t (id, val) VALUES (1, 10)"); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	sess1 := "sess1"
	sess2 := "sess2"

	// 1. Session 1 starts transaction and updates row 1 (UNCOMMITTED)
	if _, err := coord.ExecuteSessionSQL(sess1, "BEGIN TRANSACTION"); err != nil {
		t.Fatalf("sess1 BeginTx failed: %v", err)
	}
	res1, err := coord.ExecuteSessionSQL(sess1, "UPDATE wt_t SET val = 20 WHERE id = 1")
	if err != nil {
		t.Fatalf("sess1 update failed: %v", err)
	}
	if res1.RowsAffected != 1 {
		t.Fatalf("sess1 expected 1 row affected, got %d", res1.RowsAffected)
	}

	// 2. Session 2 attempts autocommit UPDATE on the SAME row while Session 1 is uncommitted.
	res2, err2 := coord.ExecuteSessionSQL(sess2, "UPDATE wt_t SET val = 30 WHERE id = 1")
	if err2 == nil {
		t.Fatalf("CRITICAL BUG IN WT: Session 2 update succeeded on an uncommitted row! Result: %+v", res2)
	}
	if !storage.IsWriteConflict(err2) {
		t.Fatalf("expected write conflict error (ErrSerializationFailure), got: %v", err2)
	}

	// 3. Session 2 attempts autocommit DELETE on the SAME row while Session 1 is uncommitted.
	resDel, errDel := coord.ExecuteSessionSQL(sess2, "DELETE FROM wt_t WHERE id = 1")
	if errDel == nil {
		t.Fatalf("CRITICAL BUG IN WT: Session 2 delete succeeded on an uncommitted row! Result: %+v", resDel)
	}
	if !storage.IsWriteConflict(errDel) {
		t.Fatalf("expected write conflict error (ErrSerializationFailure) on DELETE, got: %v", errDel)
	}

	// 4. Session 1 commits
	if _, err := coord.ExecuteSessionSQL(sess1, "COMMIT"); err != nil {
		t.Fatalf("sess1 Commit failed: %v", err)
	}

	// 5. Session 2 retries autocommit UPDATE after Session 1 committed -> MUST SUCCEED
	res2Retry, err2Retry := coord.ExecuteSessionSQL(sess2, "UPDATE wt_t SET val = 40 WHERE id = 1")
	if err2Retry != nil {
		t.Fatalf("Session 2 retry update failed after session 1 commit: %v", err2Retry)
	}
	if res2Retry.RowsAffected != 1 {
		t.Fatalf("Session 2 expected 1 row affected, got %d", res2Retry.RowsAffected)
	}

	// 6. Verify final value is 40
	resVal, err := coord.ExecuteSQL("SELECT val FROM wt_t WHERE id = 1")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(resVal.Rows) != 1 || resVal.Rows[0]["val"] != int64(40) {
		t.Fatalf("expected final val = 40, got: %+v", resVal.Rows)
	}
}

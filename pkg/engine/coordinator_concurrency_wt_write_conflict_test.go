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

	// 2. Session 2 attempts to update the SAME row while Session 1 is uncommitted.
	_, err2 := coord.ExecuteSessionSQL(sess2, "UPDATE wt_t SET val = 30 WHERE id = 1")
	if err2 == nil {
		t.Fatalf("CRITICAL BUG IN WT: Session 2 update succeeded on a row with an active uncommitted writer! Expected serialization failure, got nil")
	}

	t.Logf("WT err2: %v", err2); if !storage.IsWriteConflict(err2) {
		t.Fatalf("expected write conflict error (ErrSerializationFailure), got: %v", err2)
	}

	// 3. Session 1 commits
	if _, err := coord.ExecuteSessionSQL(sess1, "COMMIT"); err != nil {
		t.Fatalf("sess1 Commit failed: %v", err)
	}
}

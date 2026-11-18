package engine

import (
	"testing"

	"lionsql/pkg/storage"
)

// TestConcurrent_UncommittedWrite_ConflictFailsFast verifies that when Session 1
// has updated a row inside an active uncommitted transaction, a concurrent write by Session 2
// on the same row MUST fail immediately with a serialization/write conflict error,
// preventing Last-Write-Wins and uncommitted dirty overwrites.
func TestConcurrent_UncommittedWrite_ConflictFailsFast(t *testing.T) {
	coord, cleanup := setupTestCoord(t)
	defer cleanup()

	if _, err := coord.ExecuteSQL("CREATE TABLE t (id INT PRIMARY KEY, val INT)"); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO t (id, val) VALUES (1, 10)"); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	sess1 := "sess1"
	sess2 := "sess2"

	// 1. Session 1 starts transaction and updates row 1 (UNCOMMITTED)
	if _, err := coord.ExecuteSessionSQL(sess1, "BEGIN TRANSACTION"); err != nil {
		t.Fatalf("sess1 BeginTx failed: %v", err)
	}
	res1, err := coord.ExecuteSessionSQL(sess1, "UPDATE t SET val = 20 WHERE id = 1")
	if err != nil {
		t.Fatalf("sess1 update failed: %v", err)
	}
	if res1.RowsAffected != 1 {
		t.Fatalf("sess1 expected 1 row affected, got %d", res1.RowsAffected)
	}

	// 2. Session 2 attempts to update the SAME row while Session 1 is uncommitted.
	// This MUST NOT succeed silently (Last-Write-Wins). It must return a serialization failure!
	_, err2 := coord.ExecuteSessionSQL(sess2, "UPDATE t SET val = 30 WHERE id = 1")
	if err2 == nil {
		t.Fatalf("CRITICAL BUG: Session 2 update succeeded on a row with an active uncommitted writer! Expected serialization failure, got nil")
	}

	if !storage.IsWriteConflict(err2) {
		t.Fatalf("expected write conflict error (ErrSerializationFailure), got: %v", err2)
	}

	// 3. Session 1 should still be able to commit cleanly
	if _, err := coord.ExecuteSessionSQL(sess1, "COMMIT"); err != nil {
		t.Fatalf("sess1 Commit failed: %v", err)
	}

	// 4. Verify final value is Session 1's committed value (val = 20)
	res, err := coord.ExecuteSQL("SELECT val FROM t WHERE id = 1")
	if err != nil {
		t.Fatalf("select failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	var val int64
	switch v := res.Rows[0]["val"].(type) {
	case int64:
		val = v
	case int:
		val = int64(v)
	case storage.Value:
		val = v.IVal
	}
	if val != 20 {
		t.Fatalf("expected val = 20 from sess1 commit, got %d", val)
	}
}

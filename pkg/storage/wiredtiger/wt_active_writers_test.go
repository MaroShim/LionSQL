//go:build cgo && wiredtiger

package wiredtiger_test

import (
	"errors"
	"os"
	"testing"

	"lionsql/pkg/storage"
	"lionsql/pkg/storage/wiredtiger"
)

func TestWT_ActiveWriters_FailFastConflict(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_active_writers_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := wiredtiger.New()
	if err := eng.Open(tmpDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess1, err := eng.NewSession()
	if err != nil {
		t.Fatalf("sess1 NewSession failed: %v", err)
	}
	defer sess1.Close()

	sess2, err := eng.NewSession()
	if err != nil {
		t.Fatalf("sess2 NewSession failed: %v", err)
	}
	defer sess2.Close()

	// Initial insert
	txInit, err := sess1.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("txInit BeginTx failed: %v", err)
	}
	if err := txInit.Insert("users", storage.Record{"id": storage.IntValue(1), "name": storage.StringValue("initial")}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := txInit.Commit(); err != nil {
		t.Fatalf("txInit Commit failed: %v", err)
	}

	// 1. Session 1 updates row 1 but DOES NOT COMMIT
	tx1, err := sess1.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("tx1 BeginTx failed: %v", err)
	}
	defer tx1.Rollback()

	if err := tx1.Update("users", storage.IntValue(1), storage.Record{"name": storage.StringValue("sess1_val")}); err != nil {
		t.Fatalf("tx1 update failed: %v", err)
	}

	// 2. Session 2 attempts to UPDATE the SAME row while tx1 is uncommitted -> MUST FAIL FAST
	tx2, err := sess2.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("tx2 BeginTx failed: %v", err)
	}
	defer tx2.Rollback()

	errConflict := tx2.Update("users", storage.IntValue(1), storage.Record{"name": storage.StringValue("sess2_val")})
	if errConflict == nil {
		t.Fatalf("CRITICAL: Session 2 Update succeeded on an uncommitted row! Expected ErrSerializationFailure, got nil")
	}
	if !errors.Is(errConflict, storage.ErrSerializationFailure) {
		t.Fatalf("expected ErrSerializationFailure, got: %v", errConflict)
	}

	// 3. Session 2 attempts to DELETE the SAME row -> MUST ALSO FAIL FAST
	errDelConflict := tx2.Delete("users", storage.IntValue(1))
	if errDelConflict == nil {
		t.Fatalf("CRITICAL: Session 2 Delete succeeded on an uncommitted row! Expected ErrSerializationFailure, got nil")
	}
	if !errors.Is(errDelConflict, storage.ErrSerializationFailure) {
		t.Fatalf("expected ErrSerializationFailure for Delete, got: %v", errDelConflict)
	}

	// 4. Session 1 rolls back, releasing the active lock
	if err := tx1.Rollback(); err != nil {
		t.Fatalf("tx1 Rollback failed: %v", err)
	}

	// 5. Now Session 2 update MUST succeed without conflict
	if err := tx2.Update("users", storage.IntValue(1), storage.Record{"name": storage.StringValue("sess2_now_succeeds")}); err != nil {
		t.Fatalf("expected tx2 update to succeed after tx1 rollback, got: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("tx2 Commit failed: %v", err)
	}

	// 6. Test direct Session.Close() / Session.Reset() cleanup when tx is abandoned without Rollback()
	txAbandoned, err := sess1.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("txAbandoned BeginTx failed: %v", err)
	}
	if err := txAbandoned.Update("users", storage.IntValue(1), storage.Record{"name": storage.StringValue("abandoned_val")}); err != nil {
		t.Fatalf("txAbandoned update failed: %v", err)
	}
	// Close sess1 directly without calling txAbandoned.Rollback() (simulating abrupt session teardown)
	if err := sess1.Close(); err != nil {
		t.Fatalf("sess1.Close() failed: %v", err)
	}

	// Session 2 must immediately be able to update row 1 because sess1.Close() cleaned up activeWriters
	tx3, err := sess2.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("tx3 BeginTx failed: %v", err)
	}
	defer tx3.Rollback()
	if err := tx3.Update("users", storage.IntValue(1), storage.Record{"name": storage.StringValue("recovered_after_sess_close")}); err != nil {
		t.Fatalf("expected tx3 update to succeed after sess1.Close(), got: %v", err)
	}
	if err := tx3.Commit(); err != nil {
		t.Fatalf("tx3 Commit failed: %v", err)
	}
}

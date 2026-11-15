//go:build cgo && wiredtiger

package wiredtiger

import (
	"errors"
	"fmt"
	"testing"

	"lionsql/pkg/storage"
)

// TestWiredTiger_FsyncFailure_TaintIsolation verifies that when a disk fsync or I/O failure occurs,
// the engine enters a fail-closed Tainted state, blocking all subsequent write transactions
// and mutations to prevent split-brain data corruption ([FIX-WT-FSYNC-FAIL-CLOSED-1]).
func TestWiredTiger_FsyncFailure_TaintIsolation(t *testing.T) {
	homeDir := t.TempDir()
	eng := New()
	if err := eng.Open(homeDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "balance", Type: storage.TypeInt, Nullable: false},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	// Initial normal insert and commit
	tx1, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := tx1.Insert("accounts", storage.Record{"id": storage.IntValue(1), "balance": storage.IntValue(100)}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 1. Simulate an unrecoverable fsync/I/O failure marking the engine tainted
	simulatedErr := fmt.Errorf("simulated EIO disk write failure")
	eng.markTainted(simulatedErr)

	if !eng.IsTainted() {
		t.Fatalf("expected engine to be tainted")
	}
	if !errors.Is(eng.checkTainted(), storage.ErrEngineTainted) {
		t.Fatalf("expected checkTainted to return ErrEngineTainted, got: %v", eng.checkTainted())
	}

	// 2. New write transaction must be rejected immediately at BeginTx
	_, err = sess.BeginTx(storage.TxOptions{})
	if err == nil {
		t.Fatalf("expected BeginTx write to fail on tainted engine, got nil")
	}
	if !errors.Is(err, storage.ErrEngineTainted) {
		t.Fatalf("expected ErrEngineTainted on BeginTx, got: %v", err)
	}

	// 3. Read-only transaction should still be allowed to inspect state or drain
	txRO, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("expected BeginTx read-only to succeed, got: %v", err)
	}
	defer txRO.Rollback()

	rec, err := txRO.Get("accounts", storage.IntValue(1))
	if err != nil {
		t.Fatalf("Get on read-only tx failed: %v", err)
	}
	if rec["balance"].IVal != 100 {
		t.Fatalf("expected balance=100, got: %v", rec["balance"].IVal)
	}
}

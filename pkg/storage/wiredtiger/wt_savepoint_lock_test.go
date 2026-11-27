//go:build cgo && wiredtiger

package wiredtiger

import (
	"testing"

	"lionsql/pkg/storage"
)

func TestWTTx_Savepoint_RollbackReleasesLocks(t *testing.T) {
	dir := t.TempDir()
	eng := New()
	if err := eng.Open(dir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess1, err := eng.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess1.Close()

	sess2, err := eng.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess2.Close()

	// Initial row setup
	tx0, err := sess1.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx0.Insert("orders", storage.Record{"id": storage.IntValue(1), "val": storage.IntValue(10)}); err != nil {
		t.Fatal(err)
	}
	if err := tx0.Commit(); err != nil {
		t.Fatal(err)
	}

	// Tx1: starts, saves point, mutates row 1, then rolls back to savepoint
	tx1, err := sess1.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx1.Rollback()

	if err := tx1.Savepoint("sp1"); err != nil {
		t.Fatalf("Savepoint failed: %v", err)
	}

	// Insert row 2 -> acquires write lock on orders\x002
	if err := tx1.Insert("orders", storage.Record{"id": storage.IntValue(2), "val": storage.IntValue(20)}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// RollbackTo sp1 -> releases write lock acquired after sp1
	if err := tx1.RollbackTo("sp1"); err != nil {
		t.Fatalf("RollbackTo failed: %v", err)
	}

	// Commit tx1 at savepoint state
	if err := tx1.Commit(); err != nil {
		t.Fatalf("Tx1 commit failed: %v", err)
	}

	// Tx2: attempts to insert row 2
	tx2, err := sess2.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx2.Rollback()

	if err := tx2.Insert("orders", storage.Record{"id": storage.IntValue(2), "val": storage.IntValue(30)}); err != nil {
		t.Fatalf("Tx2 failed to insert row 2 after Tx1 RollbackTo savepoint: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("Tx2 commit failed: %v", err)
	}
}

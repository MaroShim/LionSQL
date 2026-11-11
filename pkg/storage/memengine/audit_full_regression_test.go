package memengine

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"lionsql/pkg/storage"
)

// TestRegression_AUDIT_FULL_003_WALAppendFailureRollback tests that if WAL append fails during Commit,
// already-applied in-memory shard changes are rolled back via rollbackApplied() so no phantom rows leak.
func TestRegression_AUDIT_FULL_003_WALAppendFailureRollback(t *testing.T) {
	tempDir := t.TempDir()
	eng := New()
	if err := eng.Open(tempDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := createTestSchema()
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	// 1. Insert a baseline row
	tx1, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := tx1.Insert("accounts", storage.Record{
		"id":      storage.IntValue(1),
		"holder":  storage.StringValue("Alice"),
		"balance": storage.IntValue(1000),
	}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Verify row 1 exists
	txRead, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	rec1, err := txRead.Get("accounts", storage.IntValue(1))
	_ = txRead.Rollback()
	if err != nil || rec1 == nil {
		t.Fatalf("expected row 1, got err: %v", err)
	}

	// 2. Begin a new transaction that inserts row 2
	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := tx2.Insert("accounts", storage.Record{
		"id":      storage.IntValue(2),
		"holder":  storage.StringValue("Bob"),
		"balance": storage.IntValue(2000),
	}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Simulate WAL append failure by closing the underlying WAL file
	if eng.wal != nil && eng.wal.file != nil {
		_ = eng.wal.file.Close()
		walPath := filepath.Join(tempDir, "wal.log")
		_ = os.Chmod(walPath, 0400)
	}

	// Commit should fail due to WAL write failure
	commitErr := tx2.Commit()
	if commitErr == nil {
		t.Fatalf("expected Commit to fail after WAL corruption, but it succeeded")
	}

	// 3. Verify row 2 was NOT committed to in-memory engine (no phantom commit)
	txVerify, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx verify failed: %v", err)
	}
	defer txVerify.Rollback()

	rec2, err2 := txVerify.Get("accounts", storage.IntValue(2))
	if err2 == nil && rec2 != nil {
		t.Fatalf("AUDIT-FULL-003 violation: uncommitted row 2 leaked into in-memory table after commit failure")
	}
}

// TestRegression_AUDIT_FULL_004_ConcurrentSnapshotAndDDL verifies that concurrent DDL operations
// (CreateTable, DropTable) and disk snapshotting (SaveSnapshot) execute simultaneously without triggering
// Go runtime "fatal error: concurrent map iteration and map write" panics.
func TestRegression_AUDIT_FULL_004_ConcurrentSnapshotAndDDL(t *testing.T) {
	tempDir := t.TempDir()
	eng := New()
	if err := eng.Open(tempDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	// Worker 1: Rapidly creates and drops tables (mutates e.tables under e.mu.Lock())
	wg.Add(1)
	go func() {
		defer wg.Done()
		iter := 0
		for {
			select {
			case <-stopCh:
				return
			default:
				tblName := fmt.Sprintf("tbl_concur_%d", iter%10)
				schema := &storage.TableSchema{
					Name: tblName,
					Columns: []storage.Column{
						{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
					},
				}
				_ = eng.CreateTable(schema)
				_ = eng.DropTable(tblName)
				iter++
			}
		}
	}()

	// Worker 2: Concurrently dumps snapshots (reads table catalog)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stopCh:
				return
			default:
				_ = eng.SaveSnapshot()
			}
		}
	}()

	// Let them contend concurrently
	time.Sleep(200 * time.Millisecond)
	close(stopCh)
	wg.Wait()
}

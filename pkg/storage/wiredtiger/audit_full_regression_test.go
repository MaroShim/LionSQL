//go:build cgo && wiredtiger

package wiredtiger

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"lionsql/pkg/storage"
)

// TestRegression_AUDIT_FULL_010_SessionCloseAndReleaseCursor tests that releasing a cursor
// concurrently with or after WTSession.Close() does not cause double-free or CGo SIGSEGV.
func TestRegression_AUDIT_FULL_010_SessionCloseAndReleaseCursor(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_audit_010_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(filepath.Join(tmpDir, "db")); err != nil {
		t.Fatalf("failed to open WT engine: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "test_cur_safe",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	for round := 0; round < 20; round++ {
		sess, err := eng.NewSession()
		if err != nil {
			t.Fatalf("NewSession failed: %v", err)
		}
		wtSess := sess.(*WTSession)

		cur, err := wtSess.acquireCursor("table:test_cur_safe")
		if err != nil {
			t.Fatalf("acquireCursor failed: %v", err)
		}

		var wg sync.WaitGroup
		wg.Add(2)

		// Goroutine 1: Close the session
		go func() {
			defer wg.Done()
			_ = wtSess.Close()
		}()

		// Goroutine 2: Release the cursor
		go func() {
			defer wg.Done()
			wtSess.releaseCursor("table:test_cur_safe", cur)
		}()

		wg.Wait()
	}
}

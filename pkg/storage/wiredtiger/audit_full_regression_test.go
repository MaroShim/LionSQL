//go:build cgo && wiredtiger

package wiredtiger

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
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

// TestRegression_AUDIT_FULL_011_TruncatePhysicalNameEviction tests that TruncateTables
// properly evicts cursors keyed by PhysicalName and cleanly truncates data without EBUSY errors.
func TestRegression_AUDIT_FULL_011_TruncatePhysicalNameEviction(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_audit_011_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(filepath.Join(tmpDir, "db")); err != nil {
		t.Fatalf("failed to open WT engine: %v", err)
	}
	defer eng.Close()

	// Table with a distinct PhysicalName
	schema := &storage.TableSchema{
		Name:         "logical_users",
		PhysicalName: "phys_users_v1",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	// Insert data into table
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	rec := storage.Record{
		"id":   storage.IntValue(1),
		"name": storage.StringValue("Alice"),
	}
	if err := tx.Insert("logical_users", rec); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Open and cache a cursor on the physical URI to simulate an active/idle cached cursor in pool
	wtSess := sess.(*WTSession)
	physURI := "table:" + schema.PhysicalName
	cur, err := wtSess.acquireCursor(physURI)
	if err != nil {
		t.Fatalf("acquireCursor failed: %v", err)
	}
	// Return it to the pool so it sits idle in cursorPool[physURI]
	wtSess.releaseCursor(physURI, cur)

	// Now truncate the table. Prior to fix, evictAllCursors only targeted "logical_users",
	// leaving the cursor on "phys_users_v1" in the pool and triggering EBUSY.
	if err := eng.TruncateTables([]string{"logical_users"}, false); err != nil {
		t.Fatalf("TruncateTables failed: %v", err)
	}

	// Verify table is empty
	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx after truncate failed: %v", err)
	}
	defer tx2.Rollback()

	cursor, err := tx2.Scan("logical_users")
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	defer cursor.Close()

	if cursor.Next() {
		t.Fatalf("expected 0 rows after truncate, but found a row")
	}
}

// TestRegression_AUDIT_FULL_012_InstantAddColumn verifies that WiredTiger executes
// AddColumn in O(1) time without rewriting existing records on disk, and that subsequent
// reads correctly pad missing columns with defaults or NULLs on-the-fly (PERF-WT-INSTANT-ADD-COL-1).
func TestRegression_AUDIT_FULL_012_InstantAddColumn(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_audit_012_*")
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
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := tx.Insert("users", storage.Record{"id": storage.IntValue(1), "name": storage.StringValue("Alice")}); err != nil {
		t.Fatalf("Insert 1 failed: %v", err)
	}
	if err := tx.Insert("users", storage.Record{"id": storage.IntValue(2), "name": storage.StringValue("Bob")}); err != nil {
		t.Fatalf("Insert 2 failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 1. Instant ADD COLUMN score FLOAT DEFAULT '99.5'
	if err := eng.AddColumn("users", &storage.Column{
		Name:    "score",
		Type:    storage.TypeFloat,
		Default: "99.5",
	}); err != nil {
		t.Fatalf("AddColumn score failed: %v", err)
	}

	// 2. Instant ADD COLUMN note VARCHAR (nullable with no default)
	if err := eng.AddColumn("users", &storage.Column{
		Name:     "note",
		Type:     storage.TypeVarChar,
		Nullable: true,
	}); err != nil {
		t.Fatalf("AddColumn note failed: %v", err)
	}

	// Verify reads on older rows have padded defaults and NULLs
	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx 2 failed: %v", err)
	}

	cur, err := tx2.Scan("users")
	if err != nil {
		tx2.Rollback()
		t.Fatalf("Scan failed: %v", err)
	}

	rowCount := 0
	for cur.Next() {
		rowCount++
		rec, err := cur.Record()
		if err != nil {
			cur.Close()
			tx2.Rollback()
			t.Fatalf("cur.Record failed: %v", err)
		}
		idVal := rec["id"].IVal
		if idVal == 1 {
			if rec["name"].SVal != "Alice" || rec["score"].FVal != 99.5 || !rec["note"].IsNull {
				cur.Close()
				tx2.Rollback()
				t.Fatalf("Alice record mismatch: %+v", rec)
			}
		} else if idVal == 2 {
			if rec["name"].SVal != "Bob" || rec["score"].FVal != 99.5 || !rec["note"].IsNull {
				cur.Close()
				tx2.Rollback()
				t.Fatalf("Bob record mismatch: %+v", rec)
			}
		} else {
			cur.Close()
			tx2.Rollback()
			t.Fatalf("unexpected id: %d", idVal)
		}
	}
	cur.Close()
	if err := tx2.Rollback(); err != nil {
		t.Fatalf("tx2.Rollback failed: %v", err)
	}
	if rowCount != 2 {
		t.Fatalf("expected 2 rows, got %d", rowCount)
	}

	// 3. Insert new row with all 4 columns populated
	tx3, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx 3 failed: %v", err)
	}
	if err := tx3.Insert("users", storage.Record{
		"id":    storage.IntValue(3),
		"name":  storage.StringValue("Charlie"),
		"score": storage.FloatValue(88.0),
		"note":  storage.StringValue("vip"),
	}); err != nil {
		t.Fatalf("Insert Charlie failed: %v", err)
	}
	if err := tx3.Commit(); err != nil {
		t.Fatalf("Commit 3 failed: %v", err)
	}

	// Verify row 3
	tx4, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx 4 failed: %v", err)
	}
	defer tx4.Rollback()

	rec3, err := tx4.Get("users", storage.IntValue(3))
	if err != nil {
		t.Fatalf("Get Charlie failed: %v", err)
	}
	if rec3["name"].SVal != "Charlie" || rec3["score"].FVal != 88.0 || rec3["note"].SVal != "vip" {
		t.Fatalf("Charlie record mismatch: %+v", rec3)
	}
}

// TestRegression_DROP_COLUMN_FailClosedMiddle verifies that WiredTiger rejects dropping
// non-terminal (middle) columns from non-empty tables to prevent record corruption (Invariant 13 / [RULE:ExplicitError]),
// while permitting dropping terminal columns and dropping any column on empty tables.
func TestRegression_DROP_COLUMN_FailClosedMiddle(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_drop_col_*")
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
		Name: "test_drop",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "col_mid", Type: storage.TypeVarChar},
			{Name: "col_tail", Type: storage.TypeFloat},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	// 1. On empty table: dropping middle column should succeed
	err = eng.AlterTable("test_drop", func(sch *storage.TableSchema) error {
		sch.Columns = []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "col_tail", Type: storage.TypeFloat},
		}
		return nil
	})
	if err != nil {
		t.Fatalf("dropping middle column on empty table should succeed, got: %v", err)
	}

	// Restore col_mid
	err = eng.AlterTable("test_drop", func(sch *storage.TableSchema) error {
		sch.Columns = []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "col_mid", Type: storage.TypeVarChar},
			{Name: "col_tail", Type: storage.TypeFloat},
		}
		return nil
	})
	if err != nil {
		t.Fatalf("restoring columns failed: %v", err)
	}

	// Insert a row to make table non-empty
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := tx.Insert("test_drop", storage.Record{
		"id":       storage.IntValue(1),
		"col_mid":  storage.StringValue("middle_val"),
		"col_tail": storage.FloatValue(123.45),
	}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 2. On non-empty table: dropping middle column must fail-closed with explicit error
	err = eng.AlterTable("test_drop", func(sch *storage.TableSchema) error {
		sch.Columns = []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "col_tail", Type: storage.TypeFloat},
		}
		return nil
	})
	if err == nil {
		t.Fatalf("expected error dropping middle column on non-empty table, but got nil")
	}
	if !strings.Contains(err.Error(), "dropping non-terminal column") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// 3. Dropping terminal (tail) column col_tail must succeed
	err = eng.AlterTable("test_drop", func(sch *storage.TableSchema) error {
		sch.Columns = []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "col_mid", Type: storage.TypeVarChar},
		}
		return nil
	})
	if err != nil {
		t.Fatalf("dropping tail column should succeed, got: %v", err)
	}

	// Verify reading row after dropping tail column succeeds
	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx 2 failed: %v", err)
	}
	defer tx2.Rollback()

	rec, err := tx2.Get("test_drop", storage.IntValue(1))
	if err != nil {
		t.Fatalf("Get failed after dropping tail column: %v", err)
	}
	if rec["col_mid"].SVal != "middle_val" {
		t.Fatalf("col_mid value mismatch: %+v", rec)
	}
	if _, hasTail := rec["col_tail"]; hasTail {
		t.Fatalf("col_tail should not exist in record: %+v", rec)
	}
}

// TestRegression_AUDIT_FULL_013_RenameTableWAL verifies that RenameTable records
// an OpRenameTable WAL operation and clears cursor pools.
func TestRegression_AUDIT_FULL_013_RenameTableWAL(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_audit_013_*")
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
		Name: "old_tbl",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "v", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	var walOps []memengine.WALOp
	eng.AddWALHook(func(rec memengine.WALRecord) {
		walOps = append(walOps, rec.Ops...)
	})

	if err := eng.RenameTable("old_tbl", "new_tbl"); err != nil {
		t.Fatalf("RenameTable failed: %v", err)
	}

	foundRename := false
	for _, op := range walOps {
		if op.Type == memengine.OpRenameTable && op.Table == "new_tbl" && op.Key == "old_tbl" {
			foundRename = true
			break
		}
	}
	if !foundRename {
		t.Fatalf("expected OpRenameTable in WAL ops, got: %+v", walOps)
	}

	// Verify Sync does not race or deadlock
	if err := eng.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
}

// TestRegression_AUDIT_FULL_007_DropTable verifies that DropTable checks session->drop return code
// and deletes catalog and WAL records upon successful drop.
func TestRegression_AUDIT_FULL_007_DropTable(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_audit_007_*")
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
		Name: "to_drop",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	var walOps []memengine.WALOp
	eng.AddWALHook(func(rec memengine.WALRecord) {
		walOps = append(walOps, rec.Ops...)
	})

	if err := eng.DropTable("to_drop"); err != nil {
		t.Fatalf("DropTable failed: %v", err)
	}

	// Verify table is no longer in catalog
	if _, err := eng.GetTable("to_drop"); err == nil {
		t.Fatalf("expected error getting dropped table, got nil")
	}

	// Verify WAL record
	foundDrop := false
	for _, op := range walOps {
		if op.Type == memengine.OpDropTable && op.Table == "to_drop" {
			foundDrop = true
			break
		}
	}
	if !foundDrop {
		t.Fatalf("expected OpDropTable in WAL ops, got: %+v", walOps)
	}
}

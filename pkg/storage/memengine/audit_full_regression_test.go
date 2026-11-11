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

// TestRegression_AUDIT_FULL_001_InstantDDL_And_ConcurrentReadRace tests that:
// 1) DecodeFlatToRowWithArena gracefully decodes a buffer encoded with an older (N-1) layout
//    against a newer (N) column schema, populating the new column with NULL rather than panicking with index-out-of-range.
// 2) Concurrent SELECT queries during ALTER TABLE ADD COLUMN run without panics or data races.
func TestRegression_AUDIT_FULL_001_InstantDDL_And_ConcurrentReadRace(t *testing.T) {
	// 1. Direct Instant DDL decoding test
	oldSchema := storage.TableSchema{
		Name: "t_instant",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	oldLayout := BuildFlatRecordLayout(&oldSchema)

	rowOld := storage.Row{
		Values: []storage.Value{
			storage.IntValue(42),
			storage.StringValue("LionSQL"),
		},
	}
	bufOld, err := oldLayout.EncodeRowToFlat(&oldSchema, rowOld)
	if err != nil {
		t.Fatalf("EncodeRowToFlat failed: %v", err)
	}

	// Now simulate schema evolution to N=3 columns
	newSchema := storage.TableSchema{
		Name: "t_instant",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "extra_col", Type: storage.TypeInt}, // new column not in bufOld
		},
	}

	// Decoding bufOld with oldLayout against newSchema should succeed with Instant DDL null fallback
	decodedRow, err := oldLayout.DecodeFlatToRow(&newSchema, bufOld)
	if err != nil {
		t.Fatalf("DecodeFlatToRow failed on expanded schema: %v", err)
	}
	if len(decodedRow.Values) != 3 {
		t.Fatalf("expected 3 values in decoded row, got %d", len(decodedRow.Values))
	}
	if decodedRow.Values[0].IVal != 42 {
		t.Fatalf("expected id=42, got %v", decodedRow.Values[0])
	}
	if decodedRow.Values[1].SVal != "LionSQL" {
		t.Fatalf("expected name=LionSQL, got %v", decodedRow.Values[1])
	}
	if !decodedRow.Values[2].IsNull {
		t.Fatalf("expected extra_col to be NULL via Instant DDL fallback, got %v", decodedRow.Values[2])
	}

	// 2. High-concurrency read vs ALTER TABLE ADD COLUMN race test
	tempDir := t.TempDir()
	eng := NewWithOptions(Options{Mode: MemoryCompact})
	if err := eng.Open(tempDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	initialSchema := &storage.TableSchema{
		Name: "accounts_alter_race",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(initialSchema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, _ := eng.NewSession()
	defer sess.Close()

	// Seed 200 rows
	txSeed, _ := sess.BeginTx(storage.TxOptions{})
	for i := 0; i < 200; i++ {
		_ = txSeed.Insert("accounts_alter_race", storage.Record{
			"id":  storage.IntValue(int64(i)),
			"val": storage.IntValue(int64(i * 10)),
		})
	}
	_ = txSeed.Commit()

	var wg sync.WaitGroup
	stopReaders := make(chan struct{})

	// Spawn 4 concurrent readers
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			readSess, _ := eng.NewSession()
			defer readSess.Close()

			for {
				select {
				case <-stopReaders:
					return
				default:
					txRead, err := readSess.BeginTx(storage.TxOptions{ReadOnly: true})
					if err != nil {
						continue
					}
					for k := 0; k < 50; k++ {
						_, _ = txRead.Get("accounts_alter_race", storage.IntValue(int64(k)))
					}
					_ = txRead.Rollback()
				}
			}
		}(r)
	}

	// Add 3 columns sequentially while readers are active
	time.Sleep(20 * time.Millisecond)
	for colIdx := 1; colIdx <= 3; colIdx++ {
		newCol := storage.Column{
			Name:     fmt.Sprintf("added_%d", colIdx),
			Type:     storage.TypeInt,
			Nullable: true,
		}
		if err := eng.AddColumn("accounts_alter_race", &newCol); err != nil {
			t.Fatalf("AddColumn added_%d failed: %v", colIdx, err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	close(stopReaders)
	wg.Wait()
}

// TestRegression_AUDIT_FULL_009_FKCascadeSetNull_ConcurrentDDL verifies that foreign key
// ON DELETE SET NULL cascade operations do not access unsynchronized tx.engine.tables directly,
// preventing concurrent map read and map write panics when DDL runs concurrently.
func TestRegression_AUDIT_FULL_009_FKCascadeSetNull_ConcurrentDDL(t *testing.T) {
	tempDir := t.TempDir()
	eng := New()
	if err := eng.Open(tempDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	// 1. Create parent table
	parentSchema := &storage.TableSchema{
		Name: "fk_parent",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(parentSchema); err != nil {
		t.Fatalf("CreateTable parent failed: %v", err)
	}

	// 2. Create child table with ON DELETE SET NULL
	childSchema := &storage.TableSchema{
		Name: "fk_child",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "parent_id", Type: storage.TypeInt, Nullable: true},
			{Name: "child_name", Type: storage.TypeVarChar},
		},
		ForeignKeys: []storage.ForeignKeySchema{
			{
				Name:          "fk_child_parent",
				ChildColumns:  []string{"parent_id"},
				ParentTable:   "fk_parent",
				ParentColumns: []string{"id"},
				OnDelete:      storage.ActionSetNull,
				OnUpdate:      storage.ActionSetNull,
			},
		},
	}
	if err := eng.CreateTable(childSchema); err != nil {
		t.Fatalf("CreateTable child failed: %v", err)
	}

	// 3. Populate rows
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	txInit, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx init failed: %v", err)
	}
	for i := int64(1); i <= 50; i++ {
		if err := txInit.Insert("fk_parent", storage.Record{
			"id":   storage.IntValue(i),
			"name": storage.StringValue(fmt.Sprintf("Parent_%d", i)),
		}); err != nil {
			t.Fatalf("Insert parent %d failed: %v", i, err)
		}
		if err := txInit.Insert("fk_child", storage.Record{
			"id":         storage.IntValue(i),
			"parent_id":  storage.IntValue(i),
			"child_name": storage.StringValue(fmt.Sprintf("Child_%d", i)),
		}); err != nil {
			t.Fatalf("Insert child %d failed: %v", i, err)
		}
	}
	if err := txInit.Commit(); err != nil {
		t.Fatalf("Commit init failed: %v", err)
	}

	// 4. Concurrently run DDL while deleting parents to trigger SET NULL
	stopDDL := make(chan struct{})
	var wg sync.WaitGroup

	// DDL goroutine rapidly mutating e.tables
	wg.Add(1)
	go func() {
		defer wg.Done()
		ddlIdx := 0
		for {
			select {
			case <-stopDDL:
				return
			default:
				tblName := fmt.Sprintf("temp_ddl_churn_%d", ddlIdx%10)
				_ = eng.CreateTable(&storage.TableSchema{
					Name: tblName,
					Columns: []storage.Column{
						{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
					},
				})
				_ = eng.DropTable(tblName)
				ddlIdx++
			}
		}
	}()

	// Deleter deleting parent rows (triggers SET NULL on child rows)
	for i := int64(1); i <= 50; i++ {
		txDel, err := sess.BeginTx(storage.TxOptions{})
		if err != nil {
			t.Fatalf("BeginTx del failed: %v", err)
		}
		if err := txDel.Delete("fk_parent", storage.IntValue(i)); err != nil {
			t.Fatalf("Delete parent %d failed: %v", i, err)
		}
		if err := txDel.Commit(); err != nil {
			t.Fatalf("Commit del %d failed: %v", i, err)
		}
	}

	close(stopDDL)
	wg.Wait()

	// Verify all child rows now have NULL parent_id
	txVerify, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx verify failed: %v", err)
	}
	defer txVerify.Rollback()

	for i := int64(1); i <= 50; i++ {
		childRec, err := txVerify.Get("fk_child", storage.IntValue(i))
		if err != nil {
			t.Fatalf("Get child %d failed: %v", i, err)
		}
		pVal, exists := childRec.Get("parent_id")
		if !exists || !pVal.IsNull {
			t.Fatalf("Child %d parent_id expected NULL, got %+v", i, pVal)
		}
	}
}

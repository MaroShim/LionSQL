package backup_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lionsql/pkg/backup"
	"lionsql/pkg/catalog"
	"lionsql/pkg/planner"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestWALArchiver_Lifecycle(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_archiver_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	archiveDir := filepath.Join(tmpDir, "wal_archive")
	walPath := filepath.Join(tmpDir, "active.wal")

	archiver, err := backup.NewWALArchiver(archiveDir)
	if err != nil {
		t.Fatalf("NewWALArchiver failed: %v", err)
	}

	// 1. Write records to WAL
	writer, err := memengine.NewWALWriter(walPath, memengine.FsyncAlways)
	if err != nil {
		t.Fatalf("NewWALWriter failed: %v", err)
	}

	t0 := time.Now().UnixNano()
	for i := int64(1); i <= 5; i++ {
		err := writer.Append(memengine.WALRecord{
			LSN:       i,
			Timestamp: t0 + i*1000,
			Ops: []memengine.WALOp{
				{
					Type:  memengine.OpInsert,
					Table: "users",
					Key:   fmt.Sprintf("%d", i),
					Record: storage.Record{
						"id":   storage.IntValue(i),
						"name": storage.StringValue(fmt.Sprintf("user_%d", i)),
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	// 2. Archive and rotate
	seg, err := archiver.RotateAndArchive(writer, walPath)
	if err != nil {
		t.Fatalf("RotateAndArchive failed: %v", err)
	}
	defer writer.Close()

	if seg.MinLSN != 1 || seg.MaxLSN != 5 || seg.RecordCount != 5 {
		t.Errorf("Unexpected segment bounds: min=%d, max=%d, count=%d", seg.MinLSN, seg.MaxLSN, seg.RecordCount)
	}

	// Active WAL should now be truncated to 0 bytes
	if writer.Size() != 0 {
		t.Errorf("Expected truncated WAL size 0, got %d", writer.Size())
	}

	// 3. Write second batch of records
	for i := int64(6); i <= 10; i++ {
		_ = writer.Append(memengine.WALRecord{
			LSN:       i,
			Timestamp: t0 + i*1000,
			Ops: []memengine.WALOp{
				{
					Type:  memengine.OpInsert,
					Table: "users",
					Key:   fmt.Sprintf("%d", i),
					Record: storage.Record{
						"id":   storage.IntValue(i),
						"name": storage.StringValue(fmt.Sprintf("user_%d", i)),
					},
				},
			},
		})
	}

	seg2, err := archiver.RotateAndArchive(writer, walPath)
	if err != nil {
		t.Fatalf("RotateAndArchive segment 2 failed: %v", err)
	}
	if seg2.MinLSN != 6 || seg2.MaxLSN != 10 || seg2.RecordCount != 5 {
		t.Errorf("Unexpected segment 2 bounds: min=%d, max=%d, count=%d", seg2.MinLSN, seg2.MaxLSN, seg2.RecordCount)
	}

	// 4. Verify ListSegments
	segments, err := archiver.ListSegments()
	if err != nil {
		t.Fatalf("ListSegments failed: %v", err)
	}
	if len(segments) != 2 {
		t.Fatalf("Expected 2 archived segments, got %d", len(segments))
	}
	if segments[0].MinLSN != 1 || segments[1].MinLSN != 6 {
		t.Errorf("Segments not sorted by MinLSN: [0]=%d, [1]=%d", segments[0].MinLSN, segments[1].MinLSN)
	}
}

func TestPITR_RestoreToTimestampAndLSN(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_pitr_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbDir := filepath.Join(tmpDir, "data")
	archiveDir := filepath.Join(tmpDir, "wal_archive")
	backupFile := filepath.Join(tmpDir, "base_backup.tar.gz")

	eng := memengine.New()
	if err := eng.Open(dbDir); err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	cat := catalog.New(eng)

	// Step 1: Initialize table and seed Alice ($1,000)
	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "holder", Type: storage.TypeVarChar},
			{Name: "balance", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, _ := eng.NewSession()
	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(1),
		"holder":  storage.StringValue("Alice"),
		"balance": storage.IntValue(1000),
	})
	_ = tx.Commit()
	sess.Close()

	// Step 2: Create base snapshot backup
	f, err := os.Create(backupFile)
	if err != nil {
		t.Fatalf("Failed to create backup file: %v", err)
	}
	_, err = backup.CreateBackup(f, eng, cat)
	_ = f.Close()
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}

	archiver, err := backup.NewWALArchiver(archiveDir)
	if err != nil {
		t.Fatalf("NewWALArchiver failed: %v", err)
	}

	// Step 3: Transaction 1 at Time T1: Alice balance = $1,200
	time.Sleep(5 * time.Millisecond)
	sess, _ = eng.NewSession()
	tx, _ = sess.BeginTx(storage.TxOptions{})
	_ = tx.Update("accounts", storage.IntValue(1), storage.Record{
		"id":      storage.IntValue(1),
		"holder":  storage.StringValue("Alice"),
		"balance": storage.IntValue(1200),
	})
	_ = tx.Commit()
	sess.Close()

	t1 := time.Now().UTC()
	time.Sleep(5 * time.Millisecond)

	// Step 4: Transaction 2 at Time T2: Alice balance = $1,500 and insert Bob ($500)
	sess, _ = eng.NewSession()
	tx, _ = sess.BeginTx(storage.TxOptions{})
	_ = tx.Update("accounts", storage.IntValue(1), storage.Record{
		"id":      storage.IntValue(1),
		"holder":  storage.StringValue("Alice"),
		"balance": storage.IntValue(1500),
	})
	_ = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(2),
		"holder":  storage.StringValue("Bob"),
		"balance": storage.IntValue(500),
	})
	_ = tx.Commit()
	sess.Close()

	t2 := time.Now().UTC()
	time.Sleep(5 * time.Millisecond)

	// Step 5: Transaction 3 at Time T3: Disastrous DROP TABLE
	_ = eng.DropTable("accounts")

	// Archive the current WAL containing T1, T2, and T3
	activeWALPath := filepath.Join(dbDir, "wal.aof")
	_ = eng.Close()

	_, err = archiver.ArchiveSegment(activeWALPath)
	if err != nil {
		t.Fatalf("ArchiveSegment failed: %v", err)
	}

	// -------------------------------------------------------------
	// Verification A: Recover to T2 (Point-In-Time before DROP TABLE)
	// -------------------------------------------------------------
	pitrDirA := filepath.Join(tmpDir, "pitr_target_t2")
	engA := memengine.New()
	_ = engA.Open(pitrDirA)
	defer engA.Close()
	catA := catalog.New(engA)

	resA, err := backup.RestorePITR(backupFile, archiveDir, engA, catA, backup.PITROptions{
		RestoreOptions: backup.DefaultRestoreOptions(),
		TargetTime:     t2,
	})
	if err != nil {
		t.Fatalf("RestorePITR to T2 failed: %v", err)
	}

	if resA.BaseResult == nil || resA.BaseResult.TablesRestored != 1 {
		t.Errorf("Expected 1 base table restored, got %v", resA.BaseResult)
	}
	if !resA.TargetReached {
		t.Errorf("Expected TargetReached to be true for T2 recovery")
	}

	// Verify database state: accounts table exists! Alice = $1500, Bob = $500
	sessA, _ := engA.NewSession()
	txA, _ := sessA.BeginTx(storage.TxOptions{ReadOnly: true})
	aliceA, err := txA.Get("accounts", storage.IntValue(1))
	if err != nil {
		t.Fatalf("Expected Alice record in accounts table, got err: %v", err)
	}
	if aliceA["balance"].IVal != 1500 {
		t.Errorf("Expected Alice balance 1500 at T2, got %d", aliceA["balance"].IVal)
	}

	bobA, err := txA.Get("accounts", storage.IntValue(2))
	if err != nil {
		t.Fatalf("Expected Bob record in accounts table, got err: %v", err)
	}
	if bobA["balance"].IVal != 500 {
		t.Errorf("Expected Bob balance 500 at T2, got %d", bobA["balance"].IVal)
	}
	_ = txA.Rollback()
	sessA.Close()

	// -------------------------------------------------------------
	// Verification B: Recover to T1 (Alice = $1200, Bob does not exist yet)
	// -------------------------------------------------------------
	pitrDirB := filepath.Join(tmpDir, "pitr_target_t1")
	engB := memengine.New()
	_ = engB.Open(pitrDirB)
	defer engB.Close()
	catB := catalog.New(engB)

	resB, err := backup.RestorePITR(backupFile, archiveDir, engB, catB, backup.PITROptions{
		RestoreOptions: backup.DefaultRestoreOptions(),
		TargetTime:     t1,
	})
	if err != nil {
		t.Fatalf("RestorePITR to T1 failed: %v", err)
	}

	sessB, _ := engB.NewSession()
	txB, _ := sessB.BeginTx(storage.TxOptions{ReadOnly: true})
	aliceB, err := txB.Get("accounts", storage.IntValue(1))
	if err != nil {
		t.Fatalf("Expected Alice record at T1, got err: %v", err)
	}
	if aliceB["balance"].IVal != 1200 {
		t.Errorf("Expected Alice balance 1200 at T1, got %d", aliceB["balance"].IVal)
	}

	_, err = txB.Get("accounts", storage.IntValue(2))
	if err == nil {
		t.Errorf("Expected Bob not to exist at T1")
	}
	_ = txB.Rollback()
	sessB.Close()

	t.Logf("PITR tests successfully passed: T2 replayed %d records, T1 replayed %d records",
		resA.WALRecordsReplayed, resB.WALRecordsReplayed)
}

// TestP1_High_Defect9_5_RestorePITR_DDLSyncCatalog verifies that RestorePITR synchronizes DDL
// mutations with catalog.Catalog, calling InvalidateTableStats on DDL operations ([발굴 결함 9-5]).
func TestP1_High_Defect9_5_RestorePITR_DDLSyncCatalog(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_pitr_ddl_sync_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	archiveDir := filepath.Join(tmpDir, "archive")
	walPath := filepath.Join(tmpDir, "active.wal")

	archiver, err := backup.NewWALArchiver(archiveDir)
	if err != nil {
		t.Fatalf("NewWALArchiver failed: %v", err)
	}

	writer, err := memengine.NewWALWriter(walPath, memengine.FsyncAlways)
	if err != nil {
		t.Fatalf("NewWALWriter failed: %v", err)
	}

	t0 := time.Now().UnixNano()
	// Record 1: CreateTable "products"
	prodSchema := &storage.TableSchema{
		Name: "products",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "price", Type: storage.TypeFloat},
		},
	}
	_ = writer.Append(memengine.WALRecord{
		LSN:       1,
		Timestamp: t0 + 1000,
		Ops: []memengine.WALOp{
			{
				Type:   memengine.OpCreateTable,
				Table:  "products",
				Schema: prodSchema,
			},
		},
	})

	// Record 2: DropTable "customers"
	_ = writer.Append(memengine.WALRecord{
		LSN:       2,
		Timestamp: t0 + 2000,
		Ops: []memengine.WALOp{
			{
				Type:  memengine.OpDropTable,
				Table: "customers",
			},
		},
	})

	_, err = archiver.RotateAndArchive(writer, walPath)
	if err != nil {
		t.Fatalf("RotateAndArchive failed: %v", err)
	}
	writer.Close()

	// Target engine and catalog
	targetEng := memengine.New()
	_ = targetEng.Open("")
	defer targetEng.Close()
	targetCat := catalog.New(targetEng)

	// Pre-seed customers table and stats in catalog
	custSchema := &storage.TableSchema{
		Name: "customers",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
		},
	}
	_ = targetEng.CreateTable(custSchema)
	targetCat.SetTableStats("customers", planner.NewTableStats("customers", 50))
	if _, ok := targetCat.ListTableStats()["customers"]; !ok {
		t.Fatalf("expected customers stats to be cached before PITR")
	}

	// Run RestorePITR with cat passed
	res, err := backup.RestorePITR("", archiveDir, targetEng, targetCat, backup.PITROptions{
		RestoreOptions: backup.DefaultRestoreOptions(),
	})
	if err != nil {
		t.Fatalf("RestorePITR failed: %v", err)
	}
	if res.WALRecordsReplayed != 2 {
		t.Errorf("expected 2 WAL records replayed, got %d", res.WALRecordsReplayed)
	}

	// Verify catalog stats for "customers" were invalidated upon OpDropTable
	if _, ok := targetCat.ListTableStats()["customers"]; ok {
		t.Errorf("expected customers table stats to be invalidated in catalog after PITR OpDropTable")
	}

	// Verify "products" table was created and accessible in targetEng
	if _, err := targetEng.GetTable("products"); err != nil {
		t.Errorf("expected products table to exist in engine after PITR OpCreateTable, got: %v", err)
	}
}

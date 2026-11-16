package backup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lionsql/pkg/backup"
	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// TestPITR_Regression_TargetTimeBeforeBaseBackup asserts that requesting a recovery
// target time earlier than the base backup's creation timestamp fails fast with an explicit error,
// rather than silently succeeding and leaving the database in a newer state.
func TestPITR_Regression_TargetTimeBeforeBaseBackup(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_pitr_target_time_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbDir := filepath.Join(tmpDir, "db")
	archiveDir := filepath.Join(tmpDir, "wal_archive")
	backupFile := filepath.Join(tmpDir, "base.tar.gz")

	eng := memengine.New()
	if err := eng.Open(dbDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	cat := catalog.New(eng)

	tbl := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "balance", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(tbl); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, _ := eng.NewSession()
	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(1),
		"balance": storage.IntValue(1000),
	})
	_ = tx.Commit()
	sess.Close()

	// Create base backup
	f, err := os.Create(backupFile)
	if err != nil {
		t.Fatalf("Create backup file failed: %v", err)
	}
	manifest, err := backup.CreateBackup(f, eng, cat)
	_ = f.Close()
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}
	_ = eng.Close()

	if _, err := backup.NewWALArchiver(archiveDir); err != nil {
		t.Fatalf("NewWALArchiver failed: %v", err)
	}

	// Attempt PITR with TargetTime 1 hour BEFORE the base backup creation time
	targetTime := manifest.CreatedAt.Add(-1 * time.Hour)

	targetEng := memengine.New()
	targetDir := filepath.Join(tmpDir, "target_db")
	_ = targetEng.Open(targetDir)
	defer targetEng.Close()
	targetCat := catalog.New(targetEng)

	res, err := backup.RestorePITR(backupFile, archiveDir, targetEng, targetCat, backup.PITROptions{
		RestoreOptions: backup.DefaultRestoreOptions(),
		TargetTime:     targetTime,
	})

	if err == nil {
		t.Fatalf("expected RestorePITR to fail when TargetTime is before base backup CreatedAt, but got nil (res: %+v)", res)
	}
	if !strings.Contains(err.Error(), "before base backup") {
		t.Errorf("expected error message to mention 'before base backup', got: %v", err)
	}
}

// TestPITR_Regression_TargetLSNBeforeBaseBackup asserts that requesting a recovery
// target LSN lower than the base backup's SnapshotLSN fails fast with an explicit error.
func TestPITR_Regression_TargetLSNBeforeBaseBackup(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_pitr_target_lsn_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbDir := filepath.Join(tmpDir, "db")
	archiveDir := filepath.Join(tmpDir, "wal_archive")
	backupFile := filepath.Join(tmpDir, "base.tar.gz")

	eng := memengine.New()
	if err := eng.Open(dbDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	cat := catalog.New(eng)

	tbl := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "balance", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(tbl); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, _ := eng.NewSession()
	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(1),
		"balance": storage.IntValue(1000),
	})
	_ = tx.Commit()
	sess.Close()

	f, err := os.Create(backupFile)
	if err != nil {
		t.Fatalf("Create backup file failed: %v", err)
	}
	manifest, err := backup.CreateBackup(f, eng, cat)
	_ = f.Close()
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}
	_ = eng.Close()

	if _, err := backup.NewWALArchiver(archiveDir); err != nil {
		t.Fatalf("NewWALArchiver failed: %v", err)
	}

	targetEng := memengine.New()
	targetDir := filepath.Join(tmpDir, "target_db")
	_ = targetEng.Open(targetDir)
	defer targetEng.Close()
	targetCat := catalog.New(targetEng)

	// Attempt PITR with TargetLSN lower than SnapshotLSN
	targetLSN := manifest.SnapshotLSN - 1
	if targetLSN <= 0 {
		targetLSN = 1
	}

	res, err := backup.RestorePITR(backupFile, archiveDir, targetEng, targetCat, backup.PITROptions{
		RestoreOptions: backup.DefaultRestoreOptions(),
		TargetLSN:      targetLSN,
	})

	if err == nil {
		t.Fatalf("expected RestorePITR to fail when TargetLSN is before base backup SnapshotLSN, but got nil (res: %+v)", res)
	}
	if !strings.Contains(err.Error(), "before base backup") {
		t.Errorf("expected error message to mention 'before base backup', got: %v", err)
	}
}

// TestPITR_Regression_BaseBackupBoundarySkipsDuplicateReplay asserts that WAL records
// whose LSN <= base backup's SnapshotLSN are safely skipped during recovery rather than replayed redundantly.
func TestPITR_Regression_BaseBackupBoundarySkipsDuplicateReplay(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_pitr_boundary_skip_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbDir := filepath.Join(tmpDir, "db")
	archiveDir := filepath.Join(tmpDir, "wal_archive")
	backupFile := filepath.Join(tmpDir, "base.tar.gz")

	eng := memengine.New()
	if err := eng.Open(dbDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	cat := catalog.New(eng)

	tbl := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "balance", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(tbl); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, _ := eng.NewSession()
	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(1),
		"balance": storage.IntValue(1000),
	})
	_ = tx.Commit()
	sess.Close()

	// Base snapshot taken here (records account 1 = $1000)
	f, err := os.Create(backupFile)
	if err != nil {
		t.Fatalf("Create backup file failed: %v", err)
	}
	manifest, err := backup.CreateBackup(f, eng, cat)
	_ = f.Close()
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}

	if manifest.SnapshotLSN <= 0 {
		t.Fatalf("expected base backup Manifest to record SnapshotLSN > 0, got %d", manifest.SnapshotLSN)
	}

	archiver, err := backup.NewWALArchiver(archiveDir)
	if err != nil {
		t.Fatalf("NewWALArchiver failed: %v", err)
	}

	// Now execute transaction 2: update Alice balance to $1500
	sess, _ = eng.NewSession()
	tx, _ = sess.BeginTx(storage.TxOptions{})
	_ = tx.Update("accounts", storage.IntValue(1), storage.Record{
		"id":      storage.IntValue(1),
		"balance": storage.IntValue(1500),
	})
	_ = tx.Commit()
	sess.Close()

	// Archive the full active WAL which contains both Tx1 (in base backup) and Tx2 (after base backup)
	activeWALPath := filepath.Join(dbDir, "wal.aof")
	_ = eng.Close()

	if _, err := archiver.ArchiveSegment(activeWALPath); err != nil {
		t.Fatalf("ArchiveSegment failed: %v", err)
	}

	// Restore via PITR
	targetEng := memengine.New()
	targetDir := filepath.Join(tmpDir, "target_db")
	_ = targetEng.Open(targetDir)
	defer targetEng.Close()
	targetCat := catalog.New(targetEng)

	res, err := backup.RestorePITR(backupFile, archiveDir, targetEng, targetCat, backup.PITROptions{
		RestoreOptions: backup.DefaultRestoreOptions(),
	})
	if err != nil {
		t.Fatalf("RestorePITR failed: %v", err)
	}

	// Tx 1 was already in base backup, only Tx 2 should have been replayed from WAL
	if res.WALRecordsReplayed != 1 {
		t.Errorf("expected exactly 1 WAL record replayed after base backup boundary, got %d", res.WALRecordsReplayed)
	}

	// Verify Alice balance is $1500
	sessA, _ := targetEng.NewSession()
	txA, _ := sessA.BeginTx(storage.TxOptions{ReadOnly: true})
	alice, err := txA.Get("accounts", storage.IntValue(1))
	if err != nil {
		t.Fatalf("Get account failed: %v", err)
	}
	if alice["balance"].IVal != 1500 {
		t.Errorf("expected balance 1500, got %d", alice["balance"].IVal)
	}
	txA.Rollback()
	sessA.Close()
}

// TestPITR_Regression_DetectLSNGap asserts that missing WAL records (LSN gap)
// are detected and trigger an explicit failure rather than silently completing.
func TestPITR_Regression_DetectLSNGap(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_pitr_lsn_gap_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	archiveDir := filepath.Join(tmpDir, "wal_archive")
	archiver, err := backup.NewWALArchiver(archiveDir)
	if err != nil {
		t.Fatalf("NewWALArchiver failed: %v", err)
	}

	// Segment 1: LSN 1..2
	seg1Path := filepath.Join(tmpDir, "seg1.wal")
	w1, err := memengine.NewWALWriter(seg1Path, memengine.FsyncNone)
	if err != nil {
		t.Fatalf("NewWALWriter 1 failed: %v", err)
	}
	t0 := time.Now().UnixNano()
	_ = w1.Append(memengine.WALRecord{
		LSN:       1,
		Timestamp: t0,
		Ops: []memengine.WALOp{
			{Type: memengine.OpCreateTable, Table: "t1", Schema: &storage.TableSchema{
				Name: "t1",
				Columns: []storage.Column{
					{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
				},
			}},
		},
	})
	_ = w1.Append(memengine.WALRecord{
		LSN:       2,
		Timestamp: t0 + 1000,
		Ops: []memengine.WALOp{
			{Type: memengine.OpInsert, Table: "t1", Key: "1", Record: storage.Record{
				"id": storage.IntValue(1),
			}},
		},
	})
	_ = w1.Close()
	if _, err := archiver.ArchiveSegment(seg1Path); err != nil {
		t.Fatalf("Archive segment 1 failed: %v", err)
	}

	// Segment 2 intentionally has an LSN gap!
	// It jumps from LSN 2 to LSN 5 (LSN 3 and 4 are missing!)
	seg2Path := filepath.Join(tmpDir, "seg2.wal")
	w2, err := memengine.NewWALWriter(seg2Path, memengine.FsyncNone)
	if err != nil {
		t.Fatalf("NewWALWriter 2 failed: %v", err)
	}
	_ = w2.Append(memengine.WALRecord{
		LSN:       5,
		Timestamp: t0 + 5000,
		Ops: []memengine.WALOp{
			{Type: memengine.OpInsert, Table: "t1", Key: "5", Record: storage.Record{
				"id": storage.IntValue(5),
			}},
		},
	})
	_ = w2.Close()
	if _, err := archiver.ArchiveSegment(seg2Path); err != nil {
		t.Fatalf("Archive segment 2 failed: %v", err)
	}

	targetEng := memengine.New()
	targetDir := filepath.Join(tmpDir, "target_db")
	_ = targetEng.Open(targetDir)
	defer targetEng.Close()
	targetCat := catalog.New(targetEng)

	res, err := backup.RestorePITR("", archiveDir, targetEng, targetCat, backup.PITROptions{
		RestoreOptions: backup.DefaultRestoreOptions(),
	})

	if err == nil {
		t.Fatalf("expected RestorePITR to detect LSN gap (LSN 2 -> 5) and fail, but got nil (res: %+v)", res)
	}
	if !strings.Contains(err.Error(), "gap") && !strings.Contains(err.Error(), "missing") && !strings.Contains(err.Error(), "LSN") {
		t.Errorf("expected error to indicate LSN gap or missing LSN, got: %v", err)
	}
}

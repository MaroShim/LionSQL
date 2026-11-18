package backup_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"lionsql/pkg/backup"
	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestPITR_CompositePK_UpdateAndDelete(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_pitr_comp_pk_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbDir := filepath.Join(tmpDir, "db")
	archiveDir := filepath.Join(tmpDir, "wal_archive")
	backupFile := filepath.Join(tmpDir, "base.lionsql")

	eng := memengine.New()
	if err := eng.Open(dbDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	cat := catalog.New(eng)

	schema := storage.TableSchema{
		Name: "comp_items",
		Columns: []storage.Column{
			{Name: "k1", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "k2", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(&schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, _ := eng.NewSession()
	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("comp_items", storage.Record{"k1": storage.IntValue(1), "k2": storage.IntValue(10), "val": storage.IntValue(100)})
	_ = tx.Insert("comp_items", storage.Record{"k1": storage.IntValue(1), "k2": storage.IntValue(20), "val": storage.IntValue(200)})
	_ = tx.Commit()
	sess.Close()

	// Base backup
	f, err := os.Create(backupFile)
	if err != nil {
		t.Fatalf("create backup file failed: %v", err)
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

	// Mutate composite PK rows in WAL: update (1, 10) -> 999, delete (1, 20)
	sess, _ = eng.NewSession()
	tx, _ = sess.BeginTx(storage.TxOptions{})
	_ = tx.Update("comp_items", storage.StringValue("1\x1f10"), storage.Record{"k1": storage.IntValue(1), "k2": storage.IntValue(10), "val": storage.IntValue(999)})
	_ = tx.Delete("comp_items", storage.StringValue("1\x1f20"))
	_ = tx.Commit()
	sess.Close()

	targetTime := time.Now().UTC().Add(time.Second)
	activeWALPath := filepath.Join(dbDir, "wal.aof")
	_ = eng.Close()

	if _, err := archiver.ArchiveSegment(activeWALPath); err != nil {
		t.Fatalf("ArchiveSegment failed: %v", err)
	}

	// Restore PITR into fresh instance
	restoreDir := filepath.Join(tmpDir, "restore")
	engRestore := memengine.New()
	_ = engRestore.Open(restoreDir)
	defer engRestore.Close()
	catRestore := catalog.New(engRestore)

	if _, err := backup.RestorePITR(backupFile, archiveDir, engRestore, catRestore, backup.PITROptions{
		RestoreOptions: backup.DefaultRestoreOptions(),
		TargetTime:     targetTime,
	}); err != nil {
		t.Fatalf("RestorePITR failed: %v", err)
	}

	rSess, _ := engRestore.NewSession()
	defer rSess.Close()
	rTx, _ := rSess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer rTx.Rollback()

	rec10, err := rTx.Get("comp_items", storage.StringValue("1\x1f10"))
	if err != nil {
		t.Fatalf("expected (1, 10) to exist after PITR, got err: %v", err)
	}
	if rec10["val"].IVal != 999 {
		t.Errorf("expected (1, 10) val=999 after PITR update replay, got %d", rec10["val"].IVal)
	}

	if _, err := rTx.Get("comp_items", storage.StringValue("1\x1f20")); err == nil {
		t.Errorf("expected (1, 20) to be deleted after PITR delete replay, but still exists")
	}
}

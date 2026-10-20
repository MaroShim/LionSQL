package backup

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func setupTestDB(t *testing.T) (storage.Engine, *catalog.Catalog) {
	t.Helper()
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("failed to open memengine: %v", err)
	}

	cat := catalog.New(eng)

	// Create users table
	usersSchema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "username", Type: storage.TypeVarChar, Nullable: false},
			{Name: "score", Type: storage.TypeFloat, Nullable: true},
			{Name: "is_active", Type: storage.TypeBool, Nullable: false},
			{Name: "created_at", Type: storage.TypeDateTime, Nullable: false},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_users_username", TableName: "users", Columns: []string{"username"}, Unique: true},
		},
	}
	if err := eng.CreateTable(usersSchema); err != nil {
		t.Fatalf("failed to create users table: %v", err)
	}

	// Create orders table
	ordersSchema := &storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "order_id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "user_id", Type: storage.TypeInt, Nullable: false},
			{Name: "amount", Type: storage.TypeFloat, Nullable: false},
		},
	}
	if err := eng.CreateTable(ordersSchema); err != nil {
		t.Fatalf("failed to create orders table: %v", err)
	}

	// Populate initial records
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("failed to open session: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}

	baseTime := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 50; i++ {
		err := tx.Insert("users", storage.Record{
			"id":         storage.IntValue(int64(i)),
			"username":   storage.StringValue(fmt.Sprintf("user_%03d", i)),
			"score":      storage.FloatValue(float64(i) * 1.5),
			"is_active":  storage.BoolValue(i%2 == 0),
			"created_at": storage.DateTimeValue(baseTime.Add(time.Duration(i) * time.Minute)),
		})
		if err != nil {
			t.Fatalf("insert user %d error: %v", i, err)
		}

		err = tx.Insert("orders", storage.Record{
			"order_id": storage.IntValue(int64(i * 10)),
			"user_id":  storage.IntValue(int64(i)),
			"amount":   storage.FloatValue(float64(i * 25)),
		})
		if err != nil {
			t.Fatalf("insert order %d error: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit error: %v", err)
	}

	// Register stored procedure in catalog
	_ = cat.RegisterProcedure(&catalog.Procedure{
		Name: "GetUserInfo",
		Params: []catalog.ParamDef{
			{Name: "@user_id", Type: storage.TypeInt},
		},
		Body: "SELECT * FROM users WHERE id = @user_id;",
	})

	return eng, cat
}

func TestBackupAndRestore_MemoryEngine(t *testing.T) {
	eng, cat := setupTestDB(t)
	defer eng.Close()

	var buf bytes.Buffer
	manifest, err := CreateBackup(&buf, eng, cat)
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}

	if manifest.TablesCount != 2 {
		t.Errorf("expected 2 tables, got %d", manifest.TablesCount)
	}
	if manifest.TotalRecords != 100 {
		t.Errorf("expected 100 total records, got %d", manifest.TotalRecords)
	}
	if manifest.ProceduresCount != 1 {
		t.Errorf("expected 1 procedure, got %d", manifest.ProceduresCount)
	}

	// Restore into a fresh target engine
	targetEng := memengine.New()
	if err := targetEng.Open(""); err != nil {
		t.Fatalf("failed to open target engine: %v", err)
	}
	defer targetEng.Close()

	targetCat := catalog.New(targetEng)

	res, err := RestoreBackup(&buf, targetEng, targetCat, DefaultRestoreOptions())
	if err != nil {
		t.Fatalf("RestoreBackup failed: %v", err)
	}

	if res.TablesRestored != 2 {
		t.Errorf("expected 2 tables restored, got %d", res.TablesRestored)
	}
	if res.RecordsRestored != 100 {
		t.Errorf("expected 100 records restored, got %d", res.RecordsRestored)
	}
	if res.ProceduresRestored != 1 {
		t.Errorf("expected 1 procedure restored, got %d", res.ProceduresRestored)
	}

	// Verify records in restored target engine
	sess, err := targetEng.NewSession()
	if err != nil {
		t.Fatalf("failed to open session on restored db: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}
	defer tx.Rollback()

	rec, err := tx.Get("users", storage.IntValue(10))
	if err != nil {
		t.Fatalf("failed to get user 10 from restored db: %v", err)
	}
	if rec["username"].SVal != "user_010" {
		t.Errorf("expected user_010, got %s", rec["username"].SVal)
	}
	if rec["score"].FVal != 15.0 {
		t.Errorf("expected score 15.0, got %f", rec["score"].FVal)
	}
	if !rec["is_active"].BVal {
		t.Errorf("expected is_active true for user 10")
	}

	// Verify catalog procedure
	proc, err := targetCat.GetProcedure("GetUserInfo")
	if err != nil {
		t.Fatalf("failed to get restored procedure: %v", err)
	}
	if proc.Name != "GetUserInfo" {
		t.Errorf("expected procedure name GetUserInfo, got %s", proc.Name)
	}
}

func TestBackupFileAndRestoreFile(t *testing.T) {
	eng, cat := setupTestDB(t)
	defer eng.Close()

	tmpDir, err := os.MkdirTemp("", "lionsql-test-backup-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	backupPath := filepath.Join(tmpDir, "snapshot.tar.gz")

	// Create backup file atomically
	manifest, err := CreateBackupFile(backupPath, eng, cat)
	if err != nil {
		t.Fatalf("CreateBackupFile failed: %v", err)
	}
	if manifest.TotalRecords != 100 {
		t.Errorf("expected 100 records, got %d", manifest.TotalRecords)
	}

	fi, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("backup file not created: %v", err)
	}
	if fi.Size() == 0 {
		t.Errorf("backup file is empty")
	}

	// Restore from file
	targetEng := memengine.New()
	if err := targetEng.Open(""); err != nil {
		t.Fatalf("failed to open target engine: %v", err)
	}
	defer targetEng.Close()

	targetCat := catalog.New(targetEng)

	res, err := RestoreBackupFile(backupPath, targetEng, targetCat, DefaultRestoreOptions())
	if err != nil {
		t.Fatalf("RestoreBackupFile failed: %v", err)
	}
	if res.RecordsRestored != 100 {
		t.Errorf("expected 100 records, got %d", res.RecordsRestored)
	}
}

func TestConcurrentWritesDuringBackup(t *testing.T) {
	eng, cat := setupTestDB(t)
	defer eng.Close()

	stopCh := make(chan struct{})
	var wg sync.WaitGroup

	// Concurrently write new records to the database
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			sess, err := eng.NewSession()
			if err != nil {
				return
			}
			defer sess.Close()

			counter := 1000 + workerID*10000
			for {
				select {
				case <-stopCh:
					return
				default:
					tx, err := sess.BeginTx(storage.TxOptions{})
					if err == nil {
						_ = tx.Insert("orders", storage.Record{
							"order_id": storage.IntValue(int64(counter)),
							"user_id":  storage.IntValue(1),
							"amount":   storage.FloatValue(99.9),
						})
						_ = tx.Commit()
						counter++
					}
					time.Sleep(1 * time.Millisecond)
				}
			}
		}(w)
	}

	// Run backup during concurrent writes
	var buf bytes.Buffer
	manifest, err := CreateBackup(&buf, eng, cat)
	if err != nil {
		t.Fatalf("CreateBackup under concurrent writes failed: %v", err)
	}

	// Stop writers
	close(stopCh)
	wg.Wait()

	if manifest.TotalRecords < 100 {
		t.Errorf("expected at least baseline 100 records, got %d", manifest.TotalRecords)
	}

	// Verify restored archive parses cleanly without errors
	targetEng := memengine.New()
	_ = targetEng.Open("")
	defer targetEng.Close()
	targetCat := catalog.New(targetEng)

	res, err := RestoreBackup(&buf, targetEng, targetCat, DefaultRestoreOptions())
	if err != nil {
		t.Fatalf("failed to restore snapshot taken during concurrent writes: %v", err)
	}
	if res.RecordsRestored != manifest.TotalRecords {
		t.Errorf("restored records mismatch: manifest=%d restored=%d", manifest.TotalRecords, res.RecordsRestored)
	}
}

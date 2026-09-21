package memengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sqlion/pkg/storage"
)

func TestMemEngine_WAL_CrashRecoveryWithoutGracefulClose(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sqlion_wal_crash_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 1. Initial run: Create table, insert records, create index, update, delete
	eng := New()
	eng.SetFsyncPolicy(FsyncAlways)
	if err := eng.Open(tempDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "username", Type: storage.TypeVarChar, Nullable: false},
			{Name: "email", Type: storage.TypeVarChar, Nullable: false},
			{Name: "score", Type: storage.TypeInt, Nullable: false},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	idxSchema := &storage.IndexSchema{
		Name:      "idx_users_email",
		TableName: "users",
		Columns:   []string{"email"},
		Unique:    true,
	}
	if err := eng.CreateIndex(idxSchema); err != nil {
		t.Fatalf("CreateIndex failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	// Insert 3 users
	tx1, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx1.Insert("users", storage.Record{
		"id":       storage.IntValue(1),
		"username": storage.StringValue("alice"),
		"email":    storage.StringValue("alice@example.com"),
		"score":    storage.IntValue(100),
	})
	_ = tx1.Insert("users", storage.Record{
		"id":       storage.IntValue(2),
		"username": storage.StringValue("bob"),
		"email":    storage.StringValue("bob@example.com"),
		"score":    storage.IntValue(200),
	})
	_ = tx1.Insert("users", storage.Record{
		"id":       storage.IntValue(3),
		"username": storage.StringValue("charlie"),
		"email":    storage.StringValue("charlie@example.com"),
		"score":    storage.IntValue(300),
	})
	if err := tx1.Commit(); err != nil {
		t.Fatalf("tx1 commit failed: %v", err)
	}

	// Update user 1, delete user 2
	tx2, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx2.Update("users", storage.IntValue(1), storage.Record{
		"id":       storage.IntValue(1),
		"username": storage.StringValue("alice_updated"),
		"email":    storage.StringValue("alice_new@example.com"),
		"score":    storage.IntValue(150),
	})
	_ = tx2.Delete("users", storage.IntValue(2))
	if err := tx2.Commit(); err != nil {
		t.Fatalf("tx2 commit failed: %v", err)
	}

	// CRASH SIMULATION: Do NOT call eng.Close() or saveDiskLocked().
	// Instead, explicitly close the WAL file descriptor to release OS handles without dumping snapshot.
	if eng.wal != nil {
		_ = eng.wal.Close()
	}

	// Ensure wal.aof exists on disk
	aofPath := filepath.Join(tempDir, "wal.aof")
	if _, err := os.Stat(aofPath); os.IsNotExist(err) {
		t.Fatalf("expected wal.aof to exist on disk")
	}

	// 2. Recovery run: Open a brand new engine on the same directory
	recoveredEng := New()
	if err := recoveredEng.Open(tempDir); err != nil {
		t.Fatalf("Recovery Open failed: %v", err)
	}
	defer recoveredEng.Close()

	recSess, err := recoveredEng.NewSession()
	if err != nil {
		t.Fatalf("NewSession on recovered engine failed: %v", err)
	}
	defer recSess.Close()

	readTx, _ := recSess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer readTx.Rollback()

	// Verify User 1 (Updated)
	u1, err := readTx.Get("users", storage.IntValue(1))
	if err != nil || u1["username"].SVal != "alice_updated" || u1["email"].SVal != "alice_new@example.com" || u1["score"].IVal != 150 {
		t.Fatalf("User 1 recovery failed, got: %+v, err: %v", u1, err)
	}

	// Verify User 2 (Deleted)
	_, err = readTx.Get("users", storage.IntValue(2))
	if err == nil {
		t.Fatalf("expected user 2 to be deleted in recovered engine")
	}

	// Verify User 3 (Intact)
	u3, err := readTx.Get("users", storage.IntValue(3))
	if err != nil || u3["username"].SVal != "charlie" || u3["score"].IVal != 300 {
		t.Fatalf("User 3 recovery failed, got: %+v, err: %v", u3, err)
	}

	// Verify Secondary Index Scan on recovered engine
	emailKey := storage.StringValue("alice_new@example.com")
	cursor, err := readTx.IndexScan("users", "idx_users_email", &emailKey, &emailKey)
	if err != nil {
		t.Fatalf("IndexScan on recovered engine failed: %v", err)
	}
	count := 0
	for cursor.Next() {
		count++
		rec, _ := cursor.Record()
		if rec["id"].IVal != 1 {
			t.Errorf("unexpected index record: %+v", rec)
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 record from index scan, got %d", count)
	}
}

func TestMemEngine_WAL_AOFRewriteAndCompaction(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sqlion_aof_rewrite_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	eng := New()
	eng.SetFsyncPolicy(FsyncAlways)
	// Set very small threshold (300 bytes) to force automatic compaction
	eng.SetAOFThreshold(300)

	if err := eng.Open(tempDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	schema := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeInt},
		},
	}
	_ = eng.CreateTable(schema)

	sess, _ := eng.NewSession()

	// Insert 20 records in separate transactions to generate AOF growth
	for i := 1; i <= 20; i++ {
		tx, _ := sess.BeginTx(storage.TxOptions{})
		_ = tx.Insert("items", storage.Record{
			"id":  storage.IntValue(int64(i)),
			"val": storage.IntValue(int64(i * 10)),
		})
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit %d failed: %v", i, err)
		}
	}

	// Verify tables.mpk snapshot was created by compaction
	snapshotPath := filepath.Join(tempDir, "tables.mpk")
	if _, err := os.Stat(snapshotPath); os.IsNotExist(err) {
		t.Fatalf("expected tables.mpk snapshot to be generated by AOF compaction")
	}

	// Trigger explicit rewrite
	if err := eng.RewriteAOF(); err != nil {
		t.Fatalf("explicit RewriteAOF failed: %v", err)
	}

	// Crash close without graceful Close()
	if eng.wal != nil {
		_ = eng.wal.Close()
	}

	// Reopen and verify data integrity
	reopened := New()
	if err := reopened.Open(tempDir); err != nil {
		t.Fatalf("Reopen failed: %v", err)
	}
	defer reopened.Close()

	rSess, _ := reopened.NewSession()
	rTx, _ := rSess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer rTx.Rollback()

	cursor, err := rTx.Scan("items")
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	rowCount := 0
	for cursor.Next() {
		rowCount++
	}
	if rowCount != 20 {
		t.Fatalf("expected 20 items after compaction recovery, got %d", rowCount)
	}
}

func TestMemEngine_WAL_FsyncPolicies(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sqlion_fsync_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	policies := []FsyncPolicy{FsyncAlways, FsyncEverySec, FsyncNone}
	for i, policy := range policies {
		subDir := filepath.Join(tempDir, fmt.Sprintf("sub_%d", i))
		eng := New()
		eng.SetFsyncPolicy(policy)
		if err := eng.Open(subDir); err != nil {
			t.Fatalf("Open with policy %v failed: %v", policy, err)
		}

		schema := &storage.TableSchema{
			Name: "logs",
			Columns: []storage.Column{
				{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
				{Name: "msg", Type: storage.TypeVarChar},
			},
		}
		_ = eng.CreateTable(schema)

		sess, _ := eng.NewSession()
		tx, _ := sess.BeginTx(storage.TxOptions{})
		_ = tx.Insert("logs", storage.Record{
			"id":  storage.IntValue(1),
			"msg": storage.StringValue("hello"),
		})
		_ = tx.Commit()

		if policy == FsyncEverySec {
			time.Sleep(50 * time.Millisecond)
		}

		if err := eng.Close(); err != nil {
			t.Fatalf("Close failed: %v", err)
		}
	}
}

func TestMemEngine_LegacyJSON_AutoMigrationToMessagePack(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sqlion_json_migration_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create legacy tables.json manually
	legacyTables := map[string]*TableData{
		"legacy_users": {
			Schema: storage.TableSchema{
				Name: "legacy_users",
				Columns: []storage.Column{
					{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
					{Name: "name", Type: storage.TypeVarChar},
				},
			},
			Rows: map[string]storage.Record{
				"1": {
					"id":   storage.IntValue(1),
					"name": storage.StringValue("OldUser"),
				},
			},
			Versions: map[string]int64{"1": 1},
			Keys:     []string{"1"},
			Indexes:  make(map[string]*IndexData),
		},
	}
	jsonData, _ := json.Marshal(legacyTables)
	jsonPath := filepath.Join(tempDir, "tables.json")
	_ = os.WriteFile(jsonPath, jsonData, 0644)

	// Open Engine -> should automatically load tables.json and migrate to tables.mpk
	eng := New()
	if err := eng.Open(tempDir); err != nil {
		t.Fatalf("Open failed on legacy directory: %v", err)
	}
	_ = eng.Close()

	// Verify tables.mpk was created and tables.json removed
	mpkPath := filepath.Join(tempDir, "tables.mpk")
	if _, err := os.Stat(mpkPath); os.IsNotExist(err) {
		t.Fatalf("expected tables.mpk to be created after migration")
	}
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy tables.json to be cleaned up")
	}

	// Reopen with new MessagePack format and verify record
	reopened := New()
	if err := reopened.Open(tempDir); err != nil {
		t.Fatalf("Reopen failed: %v", err)
	}
	defer reopened.Close()

	sess, _ := reopened.NewSession()
	tx, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer tx.Rollback()

	rec, err := tx.Get("legacy_users", storage.IntValue(1))
	if err != nil || rec["name"].SVal != "OldUser" {
		t.Fatalf("failed to retrieve migrated record: %+v, err: %v", rec, err)
	}
}

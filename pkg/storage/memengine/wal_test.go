package memengine

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lionsql/pkg/storage"
)

func TestMemEngine_WAL_CrashRecoveryWithoutGracefulClose(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lionsql_wal_crash_*")
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
	tempDir, err := os.MkdirTemp("", "lionsql_aof_rewrite_*")
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

func TestMemEngine_WAL2_DualWAL_CompactionAndCrashRecovery(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lionsql_wal2_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	eng := New()
	eng.SetFsyncPolicy(FsyncAlways)
	if err := eng.Open(tempDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	_ = eng.CreateTable(schema)

	sess, _ := eng.NewSession()
	for i := int64(1); i <= 10; i++ {
		tx, _ := sess.BeginTx(storage.TxOptions{})
		_ = tx.Insert("users", storage.Record{"id": storage.IntValue(i), "name": storage.StringValue(fmt.Sprintf("user_%d", i))})
		_ = tx.Commit()
	}

	// Trigger RewriteAOF (which rotates active.wal -> compacting.wal)
	if err := eng.RewriteAOF(); err != nil {
		t.Fatalf("RewriteAOF failed: %v", err)
	}

	// Insert more records after compaction (writes to new active.wal)
	for i := int64(11); i <= 20; i++ {
		tx, _ := sess.BeginTx(storage.TxOptions{})
		_ = tx.Insert("users", storage.Record{"id": storage.IntValue(i), "name": storage.StringValue(fmt.Sprintf("user_%d", i))})
		_ = tx.Commit()
	}

	// Close engine without saving disk (simulating abrupt exit)
	_ = eng.wal.Close()

	// Reopen engine
	reopened := New()
	if err := reopened.Open(tempDir); err != nil {
		t.Fatalf("Reopen failed: %v", err)
	}
	defer reopened.Close()

	rSess, _ := reopened.NewSession()
	rTx, _ := rSess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer rTx.Rollback()

	cur, err := rTx.Scan("users")
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	cnt := 0
	for cur.Next() {
		cnt++
	}
	if cnt != 20 {
		t.Fatalf("expected 20 users across WAL2 generation boundary, got %d", cnt)
	}
}

func TestMemEngine_WAL_FsyncPolicies(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lionsql_fsync_*")
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

func TestMemEngine_SnapshotPersistence_MessagePack(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lionsql_mpk_snapshot_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	eng := New()
	if err := eng.Open(tempDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, _ := eng.NewSession()
	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("users", storage.Record{
		"id":   storage.IntValue(1),
		"name": storage.StringValue("Alice"),
	})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	if err := eng.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify tables.mpk was created
	mpkPath := filepath.Join(tempDir, "tables.mpk")
	if _, err := os.Stat(mpkPath); os.IsNotExist(err) {
		t.Fatalf("expected tables.mpk to exist")
	}

	// Reopen with new engine instance and verify data from tables.mpk
	reopened := New()
	if err := reopened.Open(tempDir); err != nil {
		t.Fatalf("Reopen failed: %v", err)
	}
	defer reopened.Close()

	sess2, _ := reopened.NewSession()
	tx2, _ := sess2.BeginTx(storage.TxOptions{ReadOnly: true})
	defer tx2.Rollback()

	rec, err := tx2.Get("users", storage.IntValue(1))
	if err != nil || rec["name"].SVal != "Alice" {
		t.Fatalf("failed to retrieve record from tables.mpk: %+v, err: %v", rec, err)
	}
}

func TestMemEngine_WAL_CompactBinaryTupleZeroCopy(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lionsql_wal_tuple_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	eng := New()
	eng.SetFsyncPolicy(FsyncAlways)
	if err := eng.Open(tempDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	var capturedOps []WALOp
	eng.SetWALHook(func(rec WALRecord) {
		capturedOps = append(capturedOps, rec.Ops...)
	})

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "holder", Type: storage.TypeVarChar},
			{Name: "balance", Type: storage.TypeFloat},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(100),
		"holder":  storage.StringValue("ZeroCopy Test"),
		"balance": storage.FloatValue(5000.5),
	})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Verify captured WALOp contains raw compact binary tuple Data (0x01 prefix) and nil Record
	var insertOp *WALOp
	for i := range capturedOps {
		if capturedOps[i].Type == OpInsert {
			insertOp = &capturedOps[i]
			break
		}
	}
	if insertOp == nil {
		t.Fatalf("expected OpInsert WALOp to be captured")
	}
	if len(insertOp.Data) == 0 {
		t.Fatalf("expected insertOp.Data to be populated with raw binary tuple bytes")
	}
	if insertOp.Data[0] != storage.TupleCodecVersion {
		t.Errorf("expected version 0x%02x, got 0x%02x", storage.TupleCodecVersion, insertOp.Data[0])
	}
	if insertOp.Record != nil {
		t.Errorf("expected insertOp.Record to be nil for zero-allocation commit, got: %+v", insertOp.Record)
	}

	// Verify decoding directly into Row
	row, err := storage.DecodeTupleToRow(schema, insertOp.Data)
	if err != nil {
		t.Fatalf("DecodeTupleToRow failed: %v", err)
	}
	if len(row.Values) != 3 || row.Values[0].IVal != 100 || row.Values[1].SVal != "ZeroCopy Test" || row.Values[2].FVal != 5000.5 {
		t.Fatalf("decoded row mismatch: %+v", row.Values)
	}

	// Crash engine without snapshot and verify recovery purely from WAL with op.Data
	if eng.wal != nil {
		_ = eng.wal.Close()
	}

	recoveredEng := New()
	if err := recoveredEng.Open(tempDir); err != nil {
		t.Fatalf("failed to recover engine: %v", err)
	}
	defer recoveredEng.Close()

	recSess, _ := recoveredEng.NewSession()
	defer recSess.Close()

	readTx, _ := recSess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer readTx.Rollback()

	rec, err := readTx.Get("accounts", storage.IntValue(100))
	if err != nil {
		t.Fatalf("failed to get recovered record: %v", err)
	}
	if rec["holder"].SVal != "ZeroCopy Test" || rec["balance"].FVal != 5000.5 {
		t.Fatalf("recovered record mismatch: %+v", rec)
	}
}

func TestWAL_Regression_P0_03_MidFileCorruptionDoesNotTruncateValidTail(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lionsql_wal_mid_corrupt_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	aofPath := filepath.Join(tempDir, "wal.aof")
	wal, err := NewWALWriter(aofPath, FsyncAlways)
	if err != nil {
		t.Fatalf("NewWALWriter failed: %v", err)
	}

	// Write 3 records
	for i := int64(1); i <= 3; i++ {
		rec := WALRecord{
			LSN: i,
			Ops: []WALOp{
				{Type: OpInsert, Table: "t", Record: storage.Record{"id": storage.IntValue(i)}},
			},
		}
		if err := wal.Append(rec); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}
	if err := wal.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	origStat, err := os.Stat(aofPath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	origSize := origStat.Size()

	// Read entire file to find record boundaries
	data, err := os.ReadFile(aofPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	// Record 1 starts at offset 0. Frame header is 8 bytes (4-byte length + 4-byte CRC32).
	rec1Len := binary.BigEndian.Uint32(data[0:4])
	rec2Offset := walFrameHeaderSize + rec1Len

	// Corrupt record 2's frame length to 0xFFFFFFFF (> 64MB)
	binary.BigEndian.PutUint32(data[rec2Offset:rec2Offset+4], 0xFFFFFFFF)
	if err := os.WriteFile(aofPath, data, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Now try to replay WAL
	var appliedLSNs []int64
	_, replayErr := ReplayWAL(aofPath, func(rec WALRecord) error {
		appliedLSNs = append(appliedLSNs, rec.LSN)
		return nil
	})

	// P0-03 BUG: Previously replayErr was nil, and file was truncated to rec2Offset!
	if replayErr == nil {
		t.Fatalf("expected error on mid-file corruption, got nil (silent recovery illusion!)")
	}

	// Check file size: MUST NOT be truncated!
	afterStat, err := os.Stat(aofPath)
	if err != nil {
		t.Fatalf("Stat after replay failed: %v", err)
	}
	if afterStat.Size() != origSize {
		t.Fatalf("file was illegally truncated on mid-file corruption! origSize=%d, afterSize=%d", origSize, afterStat.Size())
	}
}

func TestWAL_Regression_P0_03_TailTornWriteTruncatesCleanly(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lionsql_wal_tail_torn_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	aofPath := filepath.Join(tempDir, "wal.aof")
	wal, err := NewWALWriter(aofPath, FsyncAlways)
	if err != nil {
		t.Fatalf("NewWALWriter failed: %v", err)
	}

	// Write 2 valid records
	for i := int64(1); i <= 2; i++ {
		rec := WALRecord{
			LSN: i,
			Ops: []WALOp{
				{Type: OpInsert, Table: "t", Record: storage.Record{"id": storage.IntValue(i)}},
			},
		}
		if err := wal.Append(rec); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}
	if err := wal.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	validStat, err := os.Stat(aofPath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	validSize := validStat.Size()

	// Append 2 bytes of torn write garbage at tail
	f, err := os.OpenFile(aofPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("OpenFile failed: %v", err)
	}
	if _, err := f.Write([]byte{0xDE, 0xAD}); err != nil {
		t.Fatalf("Write garbage failed: %v", err)
	}
	f.Close()

	// Replay WAL: Should successfully recover records 1 and 2, and cleanly truncate the 2 garbage bytes
	var appliedLSNs []int64
	maxLSN, replayErr := ReplayWAL(aofPath, func(rec WALRecord) error {
		appliedLSNs = append(appliedLSNs, rec.LSN)
		return nil
	})
	if replayErr != nil {
		t.Fatalf("expected clean tail recovery, got err: %v", replayErr)
	}
	if maxLSN != 2 {
		t.Fatalf("expected maxLSN=2, got %d", maxLSN)
	}
	if len(appliedLSNs) != 2 || appliedLSNs[0] != 1 || appliedLSNs[1] != 2 {
		t.Fatalf("unexpected applied LSNs: %+v", appliedLSNs)
	}

	// File size should now be back to validSize
	afterStat, err := os.Stat(aofPath)
	if err != nil {
		t.Fatalf("Stat after replay failed: %v", err)
	}
	if afterStat.Size() != validSize {
		t.Fatalf("tail torn garbage was not truncated cleanly! want %d, got %d", validSize, afterStat.Size())
	}

	// Subcase B: Append a full 8-byte header (declaring 64 bytes payload) + only 5 payload bytes (incomplete payload write)
	f2, err := os.OpenFile(aofPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("OpenFile failed: %v", err)
	}
	var tornFrame [walFrameHeaderSize + 5]byte
	binary.BigEndian.PutUint32(tornFrame[0:4], 64)
	binary.BigEndian.PutUint32(tornFrame[4:8], 0xCAFEBABE)
	copy(tornFrame[8:], []byte{1, 2, 3, 4, 5})
	if _, err := f2.Write(tornFrame[:]); err != nil {
		t.Fatalf("Write torn payload failed: %v", err)
	}
	f2.Close()

	maxLSN2, replayErr2 := ReplayWAL(aofPath, func(rec WALRecord) error {
		return nil
	})
	if replayErr2 != nil {
		t.Fatalf("expected clean recovery on incomplete tail payload, got err: %v", replayErr2)
	}
	if maxLSN2 != 2 {
		t.Fatalf("expected maxLSN=2, got %d", maxLSN2)
	}
	afterStat2, err := os.Stat(aofPath)
	if err != nil {
		t.Fatalf("Stat after second replay failed: %v", err)
	}
	if afterStat2.Size() != validSize {
		t.Fatalf("incomplete tail payload was not truncated cleanly! want %d, got %d", validSize, afterStat2.Size())
	}
}

func TestWAL_CorruptedLastFrameFullLengthFailsClosed(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lionsql_wal_last_corrupt_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	aofPath := filepath.Join(tempDir, "wal.aof")
	wal, err := NewWALWriter(aofPath, FsyncAlways)
	if err != nil {
		t.Fatalf("NewWALWriter failed: %v", err)
	}

	// Write 2 valid records
	for i := int64(1); i <= 2; i++ {
		rec := WALRecord{
			LSN: i,
			Ops: []WALOp{
				{Type: OpInsert, Table: "t", Record: storage.Record{"id": storage.IntValue(i), "name": storage.StringValue("intact_value")}},
			},
		}
		if err := wal.Append(rec); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}
	if err := wal.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	origData, err := os.ReadFile(aofPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	origSize := int64(len(origData))

	rec1Len := binary.BigEndian.Uint32(origData[0:4])
	rec2Offset := int(walFrameHeaderSize + rec1Len)
	rec2Len := binary.BigEndian.Uint32(origData[rec2Offset : rec2Offset+4])
	if int64(rec2Offset+walFrameHeaderSize+int(rec2Len)) != origSize {
		t.Fatalf("expected rec2 to end exactly at fileSize %d", origSize)
	}

	// Case 1: Bit-rot inside the last frame's payload (full frame length present on disk -> CRC32 mismatch).
	// Must fail-closed and MUST NOT truncate the last frame.
	corruptCRCData := append([]byte(nil), origData...)
	corruptCRCData[len(corruptCRCData)-1] ^= 0xFF
	if err := os.WriteFile(aofPath, corruptCRCData, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	_, err = ReplayWAL(aofPath, func(rec WALRecord) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch error on corrupted last frame, got: %v", err)
	}
	stat1, err := os.Stat(aofPath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if stat1.Size() != origSize {
		t.Fatalf("file was illegally truncated on last-frame CRC corruption! want %d, got %d", origSize, stat1.Size())
	}

	// Case 2: Invalid MessagePack payload on the last frame with matching CRC32 (full frame length present on disk).
	// Must still fail-closed on unmarshal error and MUST NOT truncate the last frame.
	corruptMsgPackData := append([]byte(nil), origData...)
	payloadStart := rec2Offset + walFrameHeaderSize
	for i := payloadStart; i < len(corruptMsgPackData); i++ {
		corruptMsgPackData[i] = 0xC1 // 0xC1 is the permanently never-used byte in MessagePack spec
	}
	forgedCRC := crc32.ChecksumIEEE(corruptMsgPackData[payloadStart:])
	binary.BigEndian.PutUint32(corruptMsgPackData[rec2Offset+4:rec2Offset+8], forgedCRC)
	if err := os.WriteFile(aofPath, corruptMsgPackData, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	_, err = ReplayWAL(aofPath, func(rec WALRecord) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "corrupted payload unmarshal") {
		t.Fatalf("expected corrupted payload unmarshal error on last frame, got: %v", err)
	}
	stat2, err := os.Stat(aofPath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if stat2.Size() != origSize {
		t.Fatalf("file was illegally truncated on last-frame unmarshal corruption! want %d, got %d", origSize, stat2.Size())
	}

	// Case 3: Complete 8-byte header at physical tail with invalid frameLen == 0.
	// Must fail-closed with corrupted frame length error and MUST NOT truncate.
	corruptHdrData := append([]byte(nil), origData[:rec2Offset]...)
	var badHdr [walFrameHeaderSize]byte
	binary.BigEndian.PutUint32(badHdr[0:4], 0)
	binary.BigEndian.PutUint32(badHdr[4:8], 0)
	corruptHdrData = append(corruptHdrData, badHdr[:]...)
	if err := os.WriteFile(aofPath, corruptHdrData, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	_, err = ReplayWAL(aofPath, func(rec WALRecord) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "corrupted frame length") {
		t.Fatalf("expected corrupted frame length error, got: %v", err)
	}
	stat3, err := os.Stat(aofPath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if stat3.Size() != int64(len(corruptHdrData)) {
		t.Fatalf("file was illegally truncated on corrupt tail header! want %d, got %d", len(corruptHdrData), stat3.Size())
	}
}

func TestMemEngine_DoubleCrashDuringCompactionRecoveryPreservesWAL(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lionsql_wal_double_crash_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	eng := New()
	eng.SetFsyncPolicy(FsyncAlways)
	if err := eng.Open(tempDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	schema := &storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "amount", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, _ := eng.NewSession()
	for i := int64(1); i <= 10; i++ {
		tx, _ := sess.BeginTx(storage.TxOptions{})
		_ = tx.Insert("orders", storage.Record{"id": storage.IntValue(i), "amount": storage.IntValue(i * 100)})
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit %d failed: %v", i, err)
		}
	}

	// Simulate Crash #1 occurring during RewriteAOF right after rotating wal.aof -> wal.aof.compacting
	// (before tables.mpk snapshot is written) and after 5 more commits (11..15) land in the new wal.aof.
	compactingPath := filepath.Join(tempDir, "wal.aof.compacting")
	aofPath := filepath.Join(tempDir, "wal.aof")
	if err := eng.wal.Rotate(compactingPath); err != nil {
		t.Fatalf("Rotate failed: %v", err)
	}
	for i := int64(11); i <= 15; i++ {
		tx, _ := sess.BeginTx(storage.TxOptions{})
		_ = tx.Insert("orders", storage.Record{"id": storage.IntValue(i), "amount": storage.IntValue(i * 100)})
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit %d failed: %v", i, err)
		}
	}
	// Release OS handles without calling eng.Close() (so tables.mpk is NOT written yet)
	_ = eng.wal.Close()
	if eng.dirLock != nil {
		_ = eng.dirLock.Release()
	}

	// Subcase A: Suppose wal.aof has a corrupted frame so recovery attempt #1 fails midway after replaying wal.aof.compacting.
	// wal.aof.compacting MUST NOT be deleted before recovery and snapshot persistence succeed!
	validAOFBytes, err := os.ReadFile(aofPath)
	if err != nil {
		t.Fatalf("ReadFile(wal.aof) failed: %v", err)
	}
	corruptedAOF := append([]byte(nil), validAOFBytes...)
	corruptedAOF[len(corruptedAOF)-1] ^= 0xFF
	if err := os.WriteFile(aofPath, corruptedAOF, 0644); err != nil {
		t.Fatalf("WriteFile corrupted wal.aof failed: %v", err)
	}

	failedEng := New()
	if err := failedEng.Open(tempDir); err == nil {
		_ = failedEng.Close()
		t.Fatalf("expected Open to fail on corrupted wal.aof")
	}
	// Verify wal.aof.compacting still exists on disk and was NOT prematurely deleted!
	if _, err := os.Stat(compactingPath); err != nil {
		t.Fatalf("wal.aof.compacting was prematurely deleted before recovery completed: %v", err)
	}

	// Restore intact wal.aof and run Recovery #1
	if err := os.WriteFile(aofPath, validAOFBytes, 0644); err != nil {
		t.Fatalf("WriteFile restored wal.aof failed: %v", err)
	}
	recEng1 := New()
	if err := recEng1.Open(tempDir); err != nil {
		t.Fatalf("Recovery #1 Open failed: %v", err)
	}

	// Verify tables.mpk was durably created during Open() before wal.aof.compacting was removed
	if _, err := os.Stat(filepath.Join(tempDir, "tables.mpk")); err != nil {
		t.Fatalf("expected tables.mpk to be persisted before retiring wal.aof.compacting: %v", err)
	}
	if _, err := os.Stat(compactingPath); !os.IsNotExist(err) {
		t.Fatalf("expected wal.aof.compacting to be retired after tables.mpk was persisted")
	}

	// Simulate Crash #2 immediately after Open() returns (without calling recEng1.Close()!)
	if recEng1.wal != nil {
		_ = recEng1.wal.Close()
	}
	if recEng1.dirLock != nil {
		_ = recEng1.dirLock.Release()
	}

	// Recovery #2: Reopen a 3rd engine instance and verify all 15 orders (1..15) are 100% preserved!
	recEng2 := New()
	if err := recEng2.Open(tempDir); err != nil {
		t.Fatalf("Recovery #2 Open after double crash failed: %v", err)
	}
	defer recEng2.Close()

	rSess, _ := recEng2.NewSession()
	defer rSess.Close()
	rTx, _ := rSess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer rTx.Rollback()

	cur, err := rTx.Scan("orders")
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	count := 0
	for cur.Next() {
		count++
	}
	if count != 15 {
		t.Fatalf("expected all 15 orders to survive double crash during compaction recovery, got %d", count)
	}
}

func TestWAL_RotateAndTruncate_KeepOldFileOpenOnFailure(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lionsql_wal_rot_trunc_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	aofPath := filepath.Join(tempDir, "wal.aof")
	w, err := NewWALWriter(aofPath, FsyncAlways)
	if err != nil {
		t.Fatalf("NewWALWriter failed: %v", err)
	}
	defer w.Close()

	// Write record 1
	if err := w.Append(WALRecord{LSN: 1, Ops: []WALOp{{Type: OpInsert, Table: "t", Key: "1"}}}); err != nil {
		t.Fatalf("Append 1 failed: %v", err)
	}

	// 1. Force Rotate to fail by specifying an invalid target directory path
	invalidRotateTarget := filepath.Join(tempDir, "nonexistent_subdir", "wal.aof.compacting")
	if err := w.Rotate(invalidRotateTarget); err == nil {
		t.Fatalf("expected Rotate to fail when target directory does not exist")
	}

	// Verify w.file was NOT closed and can still append record 2 cleanly
	if err := w.Append(WALRecord{LSN: 2, Ops: []WALOp{{Type: OpInsert, Table: "t", Key: "2"}}}); err != nil {
		t.Fatalf("Append 2 after failed Rotate should succeed on preserved descriptor, got: %v", err)
	}

	// 2. Force Truncate to fail by making wal.aof read-only (0400) so os.OpenFile(O_WRONLY|O_TRUNC) fails,
	// while the already-open w.file descriptor remains writable!
	if err := os.Chmod(aofPath, 0400); err != nil {
		t.Fatalf("Chmod 0400 failed: %v", err)
	}
	if err := w.Truncate(); err == nil {
		_ = os.Chmod(aofPath, 0644)
		t.Fatalf("expected Truncate to fail on read-only file path")
	}
	_ = os.Chmod(aofPath, 0644)

	// Verify w.file was NOT closed by the failed Truncate and can still append record 3 cleanly
	if err := w.Append(WALRecord{LSN: 3, Ops: []WALOp{{Type: OpInsert, Table: "t", Key: "3"}}}); err != nil {
		t.Fatalf("Append 3 after failed Truncate should succeed on preserved descriptor, got: %v", err)
	}

	var replayed []int64
	maxLSN, err := ReplayWAL(aofPath, func(rec WALRecord) error {
		replayed = append(replayed, rec.LSN)
		return nil
	})
	if err != nil {
		t.Fatalf("ReplayWAL failed: %v", err)
	}
	if maxLSN != 3 || len(replayed) != 3 {
		t.Fatalf("expected all 3 records to be intact and replayable, got maxLSN=%d, replayed=%v", maxLSN, replayed)
	}
}






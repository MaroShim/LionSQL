package memengine

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lionsql/pkg/storage"
)

// TestMemEngine_WAL_ReplayIndexErrorPropagation verifies that secondary index insertion
// failures during WAL crash recovery replay are propagated cleanly (fail-closed) rather than silently ignored.
func TestMemEngine_WAL_ReplayIndexErrorPropagation(t *testing.T) {
	dataDir := t.TempDir()
	opts := DefaultOptions(dataDir)
	opts.EnableAOF = true

	eng := New()
	if err := eng.OpenWithOptions(opts); err != nil {
		t.Fatalf("OpenWithOptions failed: %v", err)
	}

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "email", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{
				Name:      "idx_accounts_email",
				TableName: "accounts",
				Columns:   []string{"email"},
				Unique:    true,
			},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	// 1. Insert initial row: id=1, email="alice@example.com"
	tx1, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := tx1.Insert("accounts", storage.Record{"id": storage.IntValue(1), "email": storage.StringValue("alice@example.com")}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	sess.Close()
	eng.Close()

	// 2. Artificially append a WAL record to wal.aof containing a duplicate unique key (id=2, email="alice@example.com")
	aofPath := filepath.Join(dataDir, "wal.aof")

	dupRow := storage.Row{
		Values: []storage.Value{storage.IntValue(2), storage.StringValue("alice@example.com")},
	}
	dupTuple, err := storage.EncodeRow(schema, dupRow)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	rec := WALRecord{
		LSN:       9999,
		TxID:      1234,
		Timestamp: time.Now().UnixNano(),
		Ops: []WALOp{
			{
				Type:  OpInsert,
				Table: "accounts",
				Key:   "2",
				Data:  dupTuple,
			},
		},
	}

	writer, err := NewWALWriter(aofPath, FsyncAlways)
	if err != nil {
		t.Fatalf("NewWALWriter failed: %v", err)
	}
	if err := writer.Append(rec); err != nil {
		writer.Close()
		t.Fatalf("writer.Append failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close failed: %v", err)
	}

	// 3. Restart engine: WAL replay MUST detect the secondary index violation and fail closed!
	eng2 := New()
	openErr := eng2.OpenWithOptions(opts)
	if openErr == nil {
		eng2.Close()
		t.Fatalf("expected OpenWithOptions to fail due to secondary index duplicate key on WAL replay, got nil")
	}

	if !strings.Contains(openErr.Error(), "wal replay insert index") && !strings.Contains(openErr.Error(), "unique constraint violation on index") {
		t.Fatalf("expected WAL replay index failure error, got: %v", openErr)
	}
}

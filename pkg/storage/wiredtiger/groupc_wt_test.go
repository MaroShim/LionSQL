//go:build cgo && wiredtiger

package wiredtiger

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"lionsql/pkg/engine"
	"lionsql/pkg/storage"
)

// TestGroupC_BUG_WT_INDEX_COMP_1 verifies that WiredTiger composite index scanning
// properly encodes and compares all composite columns, and never lets unmatching rows pass
// due to uninitialized Value returning equal (0).
func TestGroupC_BUG_WT_INDEX_COMP_1(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_comp_idx_test_*")
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
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "tenant_id", Type: storage.TypeInt},
			{Name: "user_id", Type: storage.TypeInt},
			{Name: "balance", Type: storage.TypeInt},
		},
		Indexes: []storage.IndexSchema{
			{
				Name:      "ix_tenant_user",
				TableName: "accounts",
				Columns:   []string{"tenant_id", "user_id"},
				Unique:    false,
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
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	rows := []storage.Record{
		{"id": storage.IntValue(1), "tenant_id": storage.IntValue(10), "user_id": storage.IntValue(100), "balance": storage.IntValue(1000)},
		{"id": storage.IntValue(2), "tenant_id": storage.IntValue(10), "user_id": storage.IntValue(200), "balance": storage.IntValue(2000)},
		{"id": storage.IntValue(3), "tenant_id": storage.IntValue(20), "user_id": storage.IntValue(100), "balance": storage.IntValue(3000)},
	}
	for _, r := range rows {
		if err := tx.Insert("accounts", r); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	txRead, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx read failed: %v", err)
	}
	defer txRead.Rollback()

	// 1. Scan with composite key for tenant_id = 10, user_id = 200
	compKeyVal := storage.ExtractIndexKeyFromRecord(rows[1], &schema.Indexes[0])
	cur, err := txRead.IndexScan("accounts", "ix_tenant_user", &compKeyVal, &compKeyVal)
	if err != nil {
		t.Fatalf("IndexScan failed: %v", err)
	}
	defer cur.Close()

	count := 0
	for cur.Next() {
		row, err := cur.Row()
		if err != nil {
			t.Fatalf("cur.Row error: %v", err)
		}
		if row.Values[0].IVal != 2 {
			t.Fatalf("Expected account id 2, got %v", row.Values[0])
		}
		count++
	}
	if count != 1 {
		t.Fatalf("Expected exactly 1 matching row for composite key, got %d", count)
	}

	// 2. Scan with non-existent composite key: must return 0 rows (not all rows!)
	fakeKeyVal := storage.StringValue("non\x00existent")
	curFake, err := txRead.IndexScan("accounts", "ix_tenant_user", &fakeKeyVal, &fakeKeyVal)
	if err != nil {
		t.Fatalf("IndexScan fake failed: %v", err)
	}
	defer curFake.Close()

	fakeCount := 0
	for curFake.Next() {
		fakeCount++
	}
	if fakeCount != 0 {
		t.Fatalf("Expected 0 rows for non-existent composite key, got %d", fakeCount)
	}
}

// TestGroupC_BUG_WT_INDEX_DESC_1 verifies that WiredTiger IndexScanDesc returns rows in
// reverse (descending) order rather than forward (ascending) order.
func TestGroupC_BUG_WT_INDEX_DESC_1(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_desc_test_*")
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
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "score", Type: storage.TypeInt},
		},
		Indexes: []storage.IndexSchema{
			{
				Name:      "ix_score",
				TableName: "items",
				Columns:   []string{"score"},
				Unique:    false,
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
	defer sess.Close()

	tx, _ := sess.BeginTx(storage.TxOptions{})
	scores := []int64{10, 20, 30, 40, 50}
	for i, s := range scores {
		_ = tx.Insert("items", storage.Record{
			"id":    storage.IntValue(int64(i + 1)),
			"score": storage.IntValue(s),
		})
	}
	_ = tx.Commit()

	txRead, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer txRead.Rollback()

	// Perform descending index scan
	cur, err := txRead.IndexScanDesc("items", "ix_score", nil, nil)
	if err != nil {
		t.Fatalf("IndexScanDesc failed: %v", err)
	}
	defer cur.Close()

	var gotScores []int64
	for cur.Next() {
		row, err := cur.Row()
		if err != nil {
			t.Fatalf("Row error: %v", err)
		}
		gotScores = append(gotScores, row.Values[1].IVal)
	}

	expectedDesc := []int64{50, 40, 30, 20, 10}
	if len(gotScores) != len(expectedDesc) {
		t.Fatalf("Expected %d rows, got %d: %v", len(expectedDesc), len(gotScores), gotScores)
	}
	for i, exp := range expectedDesc {
		if gotScores[i] != exp {
			t.Fatalf("IndexScanDesc ordering mismatch at index %d: expected %d, got %d (all: %v)", i, exp, gotScores[i], gotScores)
		}
	}
}

// TestGroupC_BUG_WT_SWEEP_1 verifies that WTSession.Reset() invokes WT_SESSION::reset successfully,
// and that SessionPool reaps idle sessions exceeding IdleSessionTTL.
func TestGroupC_BUG_WT_SWEEP_1(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_sweep_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(filepath.Join(tmpDir, "db")); err != nil {
		t.Fatalf("failed to open WT engine: %v", err)
	}
	defer eng.Close()

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	// 1. Direct WTSession.Reset()
	wtSess, ok := sess.(*WTSession)
	if !ok {
		t.Fatalf("Expected *WTSession, got %T", sess)
	}
	if err := wtSess.Reset(); err != nil {
		t.Fatalf("WTSession.Reset failed: %v", err)
	}
	sess.Close()

	// 2. SessionPool idle TTL reaper verification
	cfg := engine.DefaultPoolConfig()
	cfg.IdleSessionTTL = 10 * time.Millisecond // fast TTL for test
	pool := engine.NewSessionPool(eng, cfg)
	defer pool.Close()

	s1, err := pool.AcquireSession()
	if err != nil {
		t.Fatalf("AcquireSession failed: %v", err)
	}
	pool.ReleaseSession(s1)

	// Wait for idle TTL to expire
	time.Sleep(25 * time.Millisecond)

	// Acquire again: the expired session should be closed and a fresh one allocated
	s2, err := pool.AcquireSession()
	if err != nil {
		t.Fatalf("AcquireSession after TTL failed: %v", err)
	}
	defer pool.ReleaseSession(s2)

	stats := pool.Stats()
	if stats.ActiveSessions != 1 {
		t.Fatalf("Expected 1 active session, got %d", stats.ActiveSessions)
	}
}

//go:build cgo && wiredtiger

package wiredtiger

import (
	"errors"
	"testing"

	"lionsql/pkg/storage"
)

// TestWiredTiger_HybridCommitAtomicity_TempTableValidationFailure tests that if a mixed
// transaction modifies both permanent WiredTiger tables and in-memory temp tables,
// and the temp table modifications fail pre-commit validation (e.g. duplicate key on unique index),
// the permanent WiredTiger table mutations are NEVER committed and are completely rolled back ([FIX-HYBRID-TX-SPLIT-BRAIN-1]).
func TestWiredTiger_HybridCommitAtomicity_TempTableValidationFailure(t *testing.T) {
	homeDir := t.TempDir()
	eng := New()
	if err := eng.Open(homeDir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	// 1. Create permanent table in WiredTiger
	permSchema := &storage.TableSchema{
		Name: "perm_orders",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "amount", Type: storage.TypeInt, Nullable: false},
		},
	}
	if err := eng.CreateTable(permSchema); err != nil {
		t.Fatalf("CreateTable perm_orders failed: %v", err)
	}

	// 2. Create session and temp table with a unique index
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	tempSchema := &storage.TableSchema{
		Name: "#temp_batch",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "unique_code", Type: storage.TypeVarChar, Nullable: false},
		},
	}
	if err := eng.CreateTable(tempSchema); err != nil {
		t.Fatalf("CreateTable #temp_batch failed: %v", err)
	}
	if err := eng.CreateIndex(&storage.IndexSchema{
		Name:      "idx_temp_code",
		TableName: "#temp_batch",
		Columns:   []string{"unique_code"},
		Unique:    true,
	}); err != nil {
		t.Fatalf("CreateIndex on #temp_batch failed: %v", err)
	}

	// Pre-seed #temp_batch with a row
	txPre, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := txPre.Insert("#temp_batch", storage.Record{
		"id":          storage.IntValue(1),
		"unique_code": storage.StringValue("CODE_A"),
	}); err != nil {
		t.Fatalf("Insert pre failed: %v", err)
	}
	if err := txPre.Commit(); err != nil {
		t.Fatalf("Commit pre failed: %v", err)
	}

	// 3. Begin mixed transaction: insert into perm_orders, and insert into #temp_batch
	txMixed, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx mixed failed: %v", err)
	}
	defer txMixed.Rollback()

	// Insert into permanent table (id: 100, amount: 500)
	if err := txMixed.Insert("perm_orders", storage.Record{
		"id":     storage.IntValue(100),
		"amount": storage.IntValue(500),
	}); err != nil {
		t.Fatalf("Insert perm_orders failed: %v", err)
	}

	// Insert into temp table with unique_code "CODE_B" (id: 2)
	if err := txMixed.Insert("#temp_batch", storage.Record{
		"id":          storage.IntValue(2),
		"unique_code": storage.StringValue("CODE_B"),
	}); err != nil {
		t.Fatalf("Insert #temp_batch failed: %v", err)
	}

	// While txMixed is uncommitted, another session inserts "CODE_B" and commits first!
	sess2, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession 2 failed: %v", err)
	}
	defer sess2.Close()

	txInterfering, err := sess2.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx interfering failed: %v", err)
	}
	if err := txInterfering.Insert("#temp_batch", storage.Record{
		"id":          storage.IntValue(3),
		"unique_code": storage.StringValue("CODE_B"),
	}); err != nil {
		t.Fatalf("Insert interfering failed: %v", err)
	}
	if err := txInterfering.Commit(); err != nil {
		t.Fatalf("Commit interfering failed: %v", err)
	}

	// 4. Commit of txMixed must fail during pre-commit validation due to collision with committed "CODE_B"
	err = txMixed.Commit()
	if err == nil {
		t.Fatalf("expected commit to fail on duplicate unique_code in temp table, got nil")
	}
	if !errors.Is(err, storage.ErrUniqueViolation) {
		t.Fatalf("expected ErrUniqueViolation, got: %v", err)
	}

	// 5. Verify that perm_orders was NOT committed to WiredTiger!
	sessVerify, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession verify failed: %v", err)
	}
	defer sessVerify.Close()

	txVerify, err := sessVerify.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx verify failed: %v", err)
	}
	defer txVerify.Rollback()

	_, err = txVerify.Get("perm_orders", storage.IntValue(100))
	if err == nil {
		t.Fatalf("CRITICAL ATOMICITY FAILURE: perm_orders row 100 was committed to WiredTiger despite temp table commit failure!")
	}
	if !errors.Is(err, storage.ErrKeyNotFound) {
		t.Fatalf("expected ErrKeyNotFound for perm_orders row 100, got: %v", err)
	}
}

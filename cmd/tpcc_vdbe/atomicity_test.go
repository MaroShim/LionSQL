package main

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/storage/wiredtiger"
)

func runAtomicityTestSuite(t *testing.T, eng storage.Engine) {
	createTPCCTables(eng)
	seedTPCCData(eng, 1, 50, 10)

	cache, err := compileTPCCProcedures()
	if err != nil {
		t.Fatalf("failed to compile procedures: %v", err)
	}

	exec := runtime.NewExecutor(cache)
	exec.SetTableGetter(eng.GetTable)

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	defer sess.Close()

	// 1. Test Success Path Atomicity
	rng := rand.New(rand.NewSource(42))
	ok := runVDBENewOrder(exec, cache, sess, rng, 1, 1, 1, 50)
	if !ok {
		t.Fatalf("expected runVDBENewOrder to succeed")
	}

	// Verify all mutations committed atomically
	func() {
		tx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatalf("failed to begin tx: %v", err)
		}
		defer tx.Rollback()

		dist, err := tx.Get("district", storage.StringValue("1_1"))
		if err != nil {
			t.Fatalf("failed to get district: %v", err)
		}
		if dist["d_next_o_id"].IVal != 1002 {
			t.Errorf("expected d_next_o_id to be 1002, got %d", dist["d_next_o_id"].IVal)
		}

		ordersCur, err := tx.Scan("orders")
		if err != nil {
			t.Fatalf("failed to scan orders: %v", err)
		}
		defer ordersCur.Close()
		orderCount := 0
		for ordersCur.Next() {
			orderCount++
		}
		if orderCount != 1 {
			t.Errorf("expected exactly 1 orders record, got %d", orderCount)
		}

		noCur, err := tx.Scan("new_order")
		if err != nil {
			t.Fatalf("failed to scan new_order: %v", err)
		}
		defer noCur.Close()
		noCount := 0
		for noCur.Next() {
			noCount++
		}
		if noCount != 1 {
			t.Errorf("expected exactly 1 new_order record, got %d", noCount)
		}

		olCur, err := tx.Scan("order_line")
		if err != nil {
			t.Fatalf("failed to scan order_line: %v", err)
		}
		defer olCur.Close()
		olCount := 0
		for olCur.Next() {
			olCount++
		}
		if olCount < 5 || olCount > 10 {
			t.Errorf("expected 5..10 order_line records, got %d", olCount)
		}
	}()

	// 2. Test Rollback Atomicity (All-or-Nothing)
	rollbackTx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("failed to begin rollbackTx: %v", err)
	}

	progHeader, _ := cache.GetByName("TPCC_NewOrderHeader")
	_, _, err = exec.RunInTx(progHeader, map[string]any{
		"w_id":      1,
		"d_key":     "1_1",
		"c_key":     "1_1_1",
		"o_key":     "1_1_9999",
		"next_o_id": int64(9999),
		"d_id":      1,
		"c_id":      1,
		"ol_cnt":    int64(5),
	}, rollbackTx, nil)
	if err != nil {
		t.Fatalf("unexpected header execution error: %v", err)
	}

	// Explicit Rollback
	if err := rollbackTx.Rollback(); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	// Verify that district was not mutated and 1_1_9999 was not committed
	func() {
		checkTx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatalf("failed to begin checkTx: %v", err)
		}
		defer checkTx.Rollback()

		distAfter, err := checkTx.Get("district", storage.StringValue("1_1"))
		if err != nil {
			t.Fatalf("failed to get district: %v", err)
		}
		if distAfter["d_next_o_id"].IVal != 1002 {
			t.Errorf("atomicity violation: expected d_next_o_id to remain 1002, got %d", distAfter["d_next_o_id"].IVal)
		}

		_, err = checkTx.Get("orders", storage.StringValue("1_1_9999"))
		if err == nil {
			t.Errorf("atomicity violation: order 1_1_9999 should not exist after rollback")
		}

		_, err = checkTx.Get("new_order", storage.StringValue("1_1_9999"))
		if err == nil {
			t.Errorf("atomicity violation: new_order 1_1_9999 should not exist after rollback")
		}
	}()
}

func TestVDBEAtomicityMemEngine(t *testing.T) {
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("failed to open memengine: %v", err)
	}
	defer eng.Close()

	runAtomicityTestSuite(t, eng)
}

func TestVDBEAtomicityWiredTiger(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sqlion_atomicity_wt_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := wiredtiger.New()
	if err := eng.Open(filepath.Clean(tmpDir)); err != nil {
		t.Fatalf("failed to open wiredtiger: %v", err)
	}
	defer eng.Close()

	runAtomicityTestSuite(t, eng)
}

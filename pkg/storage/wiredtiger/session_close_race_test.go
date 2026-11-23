//go:build cgo && wiredtiger

package wiredtiger

import (
	"sync"
	"testing"

	"lionsql/pkg/storage"
)

func TestWTSession_CloseInvalidatesOpenCursorsAndTransactions(t *testing.T) {
	dir := t.TempDir()
	eng := New()
	if err := eng.Open(dir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "t_close_race",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_val", TableName: "t_close_race", Columns: []string{"val"}},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	// Seed rows
	seedSess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	seedTx, err := seedSess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	for i := 1; i <= 50; i++ {
		if err := seedTx.Insert("t_close_race", storage.Record{
			"id":  storage.IntValue(int64(i)),
			"val": storage.StringValue("v"),
		}); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}
	if err := seedTx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
	_ = seedSess.Close()

	// 1. Open cursor, position it, then close session before calling Next/Row/Record
	sess1, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession 1 failed: %v", err)
	}
	tx1, err := sess1.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx 1 failed: %v", err)
	}
	cur, err := tx1.Scan("t_close_race")
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	if !cur.Next() {
		t.Fatalf("expected initial cur.Next() to succeed before session close")
	}

	idxVal := storage.StringValue("v")
	idxCur, err := tx1.IndexScan("t_close_race", "idx_val", &idxVal, &idxVal)
	if err != nil {
		t.Fatalf("IndexScan failed: %v", err)
	}
	if !idxCur.Next() {
		t.Fatalf("expected initial idxCur.Next() to succeed before session close")
	}

	// Close the session while tx1, cur, and idxCur are still open!
	if err := sess1.Close(); err != nil {
		t.Fatalf("sess1.Close failed: %v", err)
	}

	// After sess1.Close(), cur.Next(), cur.Record(), cur.Row(), idxCur.Next(), and tx1.Get must NOT touch freed C memory
	// and must safely return false / error!
	if cur.Next() {
		t.Fatalf("expected cur.Next() to return false after sess1.Close(), but returned true (C use-after-free!)")
	}
	if _, err := cur.Record(); err == nil {
		t.Fatalf("expected cur.Record() to return error after sess1.Close(), got nil")
	}
	if _, err := cur.Row(); err == nil {
		t.Fatalf("expected cur.Row() to return error after sess1.Close(), got nil")
	}
	if idxCur.Next() {
		t.Fatalf("expected idxCur.Next() to return false after sess1.Close(), but returned true (C use-after-free!)")
	}
	if _, err := tx1.Get("t_close_race", storage.IntValue(1)); err == nil {
		t.Fatalf("expected tx1.Get() to return error after sess1.Close(), got nil")
	}
	_ = cur.Close()
	_ = idxCur.Close()

	// 2. Concurrent stress: hammer WTTx and WTCursor while concurrently closing session
	for round := 0; round < 20; round++ {
		s, err := eng.NewSession()
		if err != nil {
			t.Fatalf("NewSession failed: %v", err)
		}
		tx, err := s.BeginTx(storage.TxOptions{})
		if err != nil {
			t.Fatalf("BeginTx failed: %v", err)
		}
		scanCur, err := tx.Scan("t_close_race")
		if err != nil {
			t.Fatalf("Scan failed: %v", err)
		}

		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				if !scanCur.Next() {
					break
				}
				_, _ = scanCur.Row()
			}
			_ = scanCur.Close()
		}()
		go func() {
			defer wg.Done()
			for i := 1; i <= 30; i++ {
				_, _ = tx.Get("t_close_race", storage.IntValue(int64(i)))
			}
		}()
		go func() {
			defer wg.Done()
			_ = s.Close()
		}()
		wg.Wait()
	}
}

func TestWTSession_TxCommitOrRollbackInvalidatesAndReclaimsOpenCursors(t *testing.T) {
	dir := t.TempDir()
	eng := New()
	if err := eng.Open(dir); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "t_tx_cursor_lifecycle",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_val", TableName: "t_tx_cursor_lifecycle", Columns: []string{"val"}},
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

	txSeed, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	for i := 1; i <= 10; i++ {
		if err := txSeed.Insert("t_tx_cursor_lifecycle", storage.Record{
			"id":  storage.IntValue(int64(i)),
			"val": storage.StringValue("a"),
		}); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}
	if err := txSeed.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 1. Open table cursor and index cursor in tx1, advance once, then Commit tx1 WITHOUT calling cur.Close()
	tx1, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx tx1 failed: %v", err)
	}
	cur1, err := tx1.Scan("t_tx_cursor_lifecycle")
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	if !cur1.Next() {
		t.Fatalf("expected cur1.Next() to succeed inside tx1")
	}
	v := storage.StringValue("a")
	idxCur1, err := tx1.IndexScan("t_tx_cursor_lifecycle", "idx_val", &v, &v)
	if err != nil {
		t.Fatalf("IndexScan failed: %v", err)
	}
	if !idxCur1.Next() {
		t.Fatalf("expected idxCur1.Next() to succeed inside tx1")
	}

	if err := tx1.Commit(); err != nil {
		t.Fatalf("tx1.Commit failed: %v", err)
	}

	// Start tx2 on the same session
	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx tx2 failed: %v", err)
	}
	defer tx2.Rollback()

	// Stale cursors from tx1 MUST be invalidated and return false/error even though sess is open and tx2 is active!
	if cur1.Next() {
		t.Fatalf("expected stale cur1.Next() from committed tx1 to return false after tx1.Commit(), but returned true!")
	}
	if _, err := cur1.Row(); err == nil {
		t.Fatalf("expected stale cur1.Row() from committed tx1 to return error, got nil")
	}
	if idxCur1.Next() {
		t.Fatalf("expected stale idxCur1.Next() from committed tx1 to return false after tx1.Commit(), but returned true!")
	}
}


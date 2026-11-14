//go:build cgo && wiredtiger

package wiredtiger

import (
	"os"
	"path/filepath"
	"testing"

	"lionsql/pkg/storage"
)

func TestWTCursor_ErrPropagation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_cursor_err_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := New()
	if err := eng.Open(filepath.Join(tmpDir, "data")); err != nil {
		t.Fatalf("failed to open WT engine: %v", err)
	}
	defer eng.Close()

	schema := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	// 1. Test empty table scan: Next() == false, Err() == nil
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}
	cur, err := tx.Scan("items")
	if err != nil {
		t.Fatalf("failed to scan items: %v", err)
	}
	if cur.Next() {
		t.Fatalf("expected Next() to be false on empty table")
	}
	if err := cur.Err(); err != nil {
		t.Fatalf("expected nil Err() on normal EOF, got: %v", err)
	}
	_ = cur.Close()
	_ = tx.Commit()

	// 2. Insert records and scan: Next() iterates all rows, final Err() == nil
	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}
	for i := 1; i <= 5; i++ {
		rec := storage.Record{
			"id":  storage.IntValue(int64(i)),
			"val": storage.StringValue("item"),
		}
		if err := tx2.Insert("items", rec); err != nil {
			t.Fatalf("failed to insert record: %v", err)
		}
	}
	_ = tx2.Commit()

	tx3, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}
	cur3, err := tx3.Scan("items")
	if err != nil {
		t.Fatalf("failed to scan items: %v", err)
	}
	count := 0
	for cur3.Next() {
		count++
	}
	if count != 5 {
		t.Fatalf("expected 5 rows, got %d", count)
	}
	if err := cur3.Err(); err != nil {
		t.Fatalf("expected nil Err() after scanning all rows, got: %v", err)
	}
	_ = cur3.Close()
	_ = tx3.Commit()

	// 3. Test explicit error latching on synthetic WTCursor
	errCur := &WTCursor{
		eof: true,
		err: errSimulatedWTError,
	}
	if errCur.Next() {
		t.Fatalf("expected Next() to be false when err is latched")
	}
	if errCur.Err() == nil {
		t.Fatalf("expected Err() to return non-nil error")
	}
}

var errSimulatedWTError = &testError{msg: "simulated wiredtiger read failure"}

type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}

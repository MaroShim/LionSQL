package memengine

import (
	"strings"
	"testing"

	"lionsql/pkg/storage"
)

// TestTableVariable_RollbackExemption verifies that T-SQL table variables (__tv_*)
// are exempt from transaction rollback per MS SQL Server standards.
func TestTableVariable_RollbackExemption(t *testing.T) {
	eng := New()
	defer eng.Close()

	// 1. Create a regular table
	ordersSchema := &storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "amount", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(ordersSchema); err != nil {
		t.Fatalf("CreateTable orders failed: %v", err)
	}

	// 2. Create a table variable
	tvSchema := &storage.TableSchema{
		Name: "__tv_sess1_errlog",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "msg", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(tvSchema); err != nil {
		t.Fatalf("CreateTable __tv_sess1_errlog failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	// 3. Begin transaction
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	// 4. Insert into orders and table variable inside the transaction
	if err := tx.Insert("orders", storage.Record{"id": storage.IntValue(1), "amount": storage.IntValue(100)}); err != nil {
		t.Fatalf("Insert orders failed: %v", err)
	}
	if err := tx.Insert("__tv_sess1_errlog", storage.Record{"id": storage.IntValue(1), "msg": storage.StringValue("attempted order 1")}); err != nil {
		t.Fatalf("Insert __tv_sess1_errlog failed: %v", err)
	}

	// 5. Rollback transaction
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	// 6. Verify regular table orders has 0 rows (rolled back!)
	tx2, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx read failed: %v", err)
	}
	defer tx2.Rollback()

	curOrders, err := tx2.Scan("orders")
	if err != nil {
		t.Fatalf("Scan orders failed: %v", err)
	}
	orderCount := 0
	for curOrders.Next() {
		orderCount++
	}
	curOrders.Close()
	if orderCount != 0 {
		t.Fatalf("expected orders table to be rolled back to 0 rows, got %d", orderCount)
	}

	// 7. Verify table variable __tv_sess1_errlog STILL HAS 1 row (exempt from rollback!)
	curTV, err := tx2.Scan("__tv_sess1_errlog")
	if err != nil {
		t.Fatalf("Scan __tv_sess1_errlog failed: %v", err)
	}
	tvCount := 0
	var msgVal string
	for curTV.Next() {
		tvCount++
		rec, _ := curTV.Record()
		msgVal = rec["msg"].SVal
	}
	curTV.Close()

	if tvCount != 1 || msgVal != "attempted order 1" {
		t.Fatalf("expected table variable to retain 1 row ('attempted order 1') after rollback, got %d rows (msg: %q)", tvCount, msgVal)
	}
}

// TestTableVariable_SavepointRollbackExemption verifies that table variables
// retain modifications even when rolling back to a SAVEPOINT.
func TestTableVariable_SavepointRollbackExemption(t *testing.T) {
	eng := New()
	defer eng.Close()

	tvSchema := &storage.TableSchema{
		Name: "__tv_sess1_sp_log",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "step", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(tvSchema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	ordersSchema := &storage.TableSchema{
		Name: "sp_orders",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
		},
	}
	if err := eng.CreateTable(ordersSchema); err != nil {
		t.Fatalf("CreateTable orders failed: %v", err)
	}

	sess, _ := eng.NewSession()
	defer sess.Close()

	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("__tv_sess1_sp_log", storage.Record{"id": storage.IntValue(1), "step": storage.StringValue("before savepoint")})

	_ = tx.Savepoint("sp1")
	_ = tx.Insert("sp_orders", storage.Record{"id": storage.IntValue(10)})
	_ = tx.Insert("__tv_sess1_sp_log", storage.Record{"id": storage.IntValue(2), "step": storage.StringValue("inside savepoint")})

	// Rollback to sp1: sp_orders should be rolled back, but __tv_sess1_sp_log MUST keep step 2!
	if err := tx.RollbackTo("sp1"); err != nil {
		t.Fatalf("RollbackTo failed: %v", err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Verify sp_orders has 0 rows
	txRead, _ := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer txRead.Rollback()

	curOrders, _ := txRead.Scan("sp_orders")
	orderCount := 0
	for curOrders.Next() {
		orderCount++
	}
	curOrders.Close()
	if orderCount != 0 {
		t.Fatalf("expected sp_orders to have 0 rows after savepoint rollback, got %d", orderCount)
	}

	// Verify table variable has BOTH steps 1 and 2!
	curTV, _ := txRead.Scan("__tv_sess1_sp_log")
	tvCount := 0
	for curTV.Next() {
		tvCount++
	}
	curTV.Close()
	if tvCount != 2 {
		t.Fatalf("expected table variable to have 2 rows, got %d", tvCount)
	}
}

// TestTableVariable_RollbackIndexErrorPropagation verifies that unique index errors
// occurring while committing table variable changes during rollback are not silently swallowed.
func TestTableVariable_RollbackIndexErrorPropagation(t *testing.T) {
	eng := New()
	defer eng.Close()

	tvSchema := &storage.TableSchema{
		Name: "__tv_sess1_concurr",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "code", Type: storage.TypeVarChar},
		},
		Indexes: []storage.IndexSchema{
			{
				Name:      "idx_tv_code",
				TableName: "__tv_sess1_concurr",
				Columns:   []string{"code"},
				Unique:    true,
			},
		},
	}
	if err := eng.CreateTable(tvSchema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess1, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession 1 failed: %v", err)
	}
	defer sess1.Close()

	sess2, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession 2 failed: %v", err)
	}
	defer sess2.Close()

	// tx1 inserts code "SHARED_CODE"
	tx1, err := sess1.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx 1 failed: %v", err)
	}
	if err := tx1.Insert("__tv_sess1_concurr", storage.Record{"id": storage.IntValue(1), "code": storage.StringValue("SHARED_CODE")}); err != nil {
		t.Fatalf("tx1 Insert failed: %v", err)
	}

	// Concurrent tx2 inserts and commits the same unique code "SHARED_CODE"
	tx2, err := sess2.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx 2 failed: %v", err)
	}
	if err := tx2.Insert("__tv_sess1_concurr", storage.Record{"id": storage.IntValue(2), "code": storage.StringValue("SHARED_CODE")}); err != nil {
		t.Fatalf("tx2 Insert failed: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("tx2 Commit failed: %v", err)
	}

	// tx1 rolls back, which attempts to commit its table variable change with duplicate unique code.
	// This MUST return an explicit error and not be silently swallowed!
	rbErr := tx1.Rollback()
	if rbErr == nil {
		t.Fatalf("expected Rollback() to fail due to unique index collision on table variable, got nil")
	}
	if !strings.Contains(rbErr.Error(), "unique constraint violation on index") && !strings.Contains(rbErr.Error(), "duplicate key") {
		t.Fatalf("expected error mentioning unique violation or duplicate key, got: %v", rbErr)
	}
}

// TestTableVariable_CommitThenDeferRollback verifies that idiomatic defer tx.Rollback()
// following a successful tx.Commit() does not double-apply table variable modifications
// or corrupt commitLSN / commitTimestamp.
func TestTableVariable_CommitThenDeferRollback(t *testing.T) {
	eng := New()
	defer eng.Close()

	tvSchema := &storage.TableSchema{
		Name: "__tv_sess1_double_apply",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(tvSchema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	doTx := func() (int64, error) {
		tx, err := sess.BeginTx(storage.TxOptions{})
		if err != nil {
			return 0, err
		}
		defer func() {
			_ = tx.Rollback()
		}()

		if err := tx.Insert("__tv_sess1_double_apply", storage.Record{
			"id":  storage.IntValue(1),
			"val": storage.StringValue("hello"),
		}); err != nil {
			return 0, err
		}

		if err := tx.Commit(); err != nil {
			return 0, err
		}
		return tx.CommitLSN(), nil
	}

	lsn, err := doTx()
	if err != nil {
		t.Fatalf("doTx failed: %v", err)
	}
	if lsn <= 0 {
		t.Fatalf("expected positive commitLSN after commit, got %d", lsn)
	}

	// Verify table variable has exactly 1 row (no double application!)
	txRead, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx read failed: %v", err)
	}
	defer txRead.Rollback()

	cur, err := txRead.Scan("__tv_sess1_double_apply")
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	defer cur.Close()

	cnt := 0
	for cur.Next() {
		cnt++
	}
	if cnt != 1 {
		t.Fatalf("expected table variable to have exactly 1 row, got %d (double applied!)", cnt)
	}
}

// TestTableVariable_ReadYourOwnWritesInsideActiveTx verifies that uncommitted
// table variable writes are immediately visible to subsequent reads within the same tx.
func TestTableVariable_ReadYourOwnWritesInsideActiveTx(t *testing.T) {
	eng := New()
	defer eng.Close()

	tvSchema := &storage.TableSchema{
		Name: "__tv_sess1_ryow",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(tvSchema); err != nil {
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
	defer tx.Rollback()

	if err := tx.Insert("__tv_sess1_ryow", storage.Record{
		"id":  storage.IntValue(100),
		"val": storage.StringValue("uncommitted_yet_visible"),
	}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// 1. tx.Get inside active transaction
	rec, err := tx.Get("__tv_sess1_ryow", storage.IntValue(100))
	if err != nil {
		t.Fatalf("tx.Get failed to find uncommitted table variable write: %v", err)
	}
	if rec["val"].SVal != "uncommitted_yet_visible" {
		t.Fatalf("expected 'uncommitted_yet_visible', got: %v", rec["val"])
	}

	// 2. tx.Scan inside active transaction
	cur, err := tx.Scan("__tv_sess1_ryow")
	if err != nil {
		t.Fatalf("tx.Scan failed: %v", err)
	}
	cnt := 0
	for cur.Next() {
		cnt++
	}
	cur.Close()
	if cnt != 1 {
		t.Fatalf("tx.Scan expected 1 row, got %d", cnt)
	}
}



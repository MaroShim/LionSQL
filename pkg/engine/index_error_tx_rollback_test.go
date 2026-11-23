package engine

import (
	"context"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

// TestCoordinator_ExplicitTx_MultiRowDML_IndexErrorAtomicRollback verifies that when a
// multi-row INSERT, UPDATE, or DELETE statement executed inside an explicit transaction (BEGIN)
// encounters an index constraint error (or FK/Check failure) on the N-th row, the error is
// propagated to the caller AND all rows 1..(N-1) mutated by that failed statement are
// rolled back (Statement-Level Atomicity), leaving prior committed/uncommitted statements in
// the outer transaction intact.
func TestCoordinator_ExplicitTx_MultiRowDML_IndexErrorAtomicRollback(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	ctx := context.Background()
	sessID := "sess_idx_atomic_dml"

	setupSQL := `
		CREATE TABLE accounts (
			id INT PRIMARY KEY,
			tag VARCHAR(50) NOT NULL,
			email VARCHAR(100) NOT NULL
		);
		CREATE INDEX idx_accounts_tag ON accounts(tag);
		CREATE UNIQUE INDEX idx_accounts_email ON accounts(email);

		CREATE TABLE orders (
			order_id INT PRIMARY KEY,
			account_id INT NOT NULL REFERENCES accounts(id)
		);

		INSERT INTO accounts (id, tag, email) VALUES (1, 't1', 'a@example.com');
		INSERT INTO accounts (id, tag, email) VALUES (2, 't2', 'b@example.com');
		INSERT INTO accounts (id, tag, email) VALUES (3, 't3', 'c@example.com');
		INSERT INTO orders (order_id, account_id) VALUES (100, 3);
	`
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, setupSQL); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Start explicit transaction and perform a valid baseline mutation (row 10)
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO accounts (id, tag, email) VALUES (10, 't10', 'ten@example.com');"); err != nil {
		t.Fatalf("baseline insert failed: %v", err)
	}

	// 1. Multi-row INSERT where 1st tuple (id=20) succeeds and 2nd tuple (id=21) violates unique index idx_accounts_email ('a@example.com')
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID,
		"INSERT INTO accounts (id, tag, email) VALUES (20, 't20', 'twenty@example.com'), (21, 't21', 'a@example.com');"); err == nil {
		t.Fatalf("expected multi-row INSERT with duplicate email to fail, but it succeeded")
	}

	// Row 20 MUST have been rolled back by statement-level atomicity (both primary table and idx_accounts_tag)
	res20, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT COUNT(*) AS cnt FROM accounts WHERE id = 20 OR tag = 't20';")
	if err != nil {
		t.Fatalf("SELECT row 20 failed: %v", err)
	}
	if res20.Rows[0]["cnt"] != int64(0) {
		t.Fatalf("CRITICAL: partial row 20 from failed multi-row INSERT was not rolled back! cnt=%v", res20.Rows[0]["cnt"])
	}

	// 2. Multi-row UPDATE where 1st row (id=1) updates to 'dup@example.com' and 2nd row (id=2) fails on unique index idx_accounts_email ('dup@example.com')
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID,
		"UPDATE accounts SET tag = 't_dup', email = 'dup@example.com' WHERE id IN (1, 2);"); err == nil {
		t.Fatalf("expected multi-row UPDATE setting duplicate email on 2 rows to fail, but it succeeded")
	}

	// Row 1 MUST still have its original tag='t1' and email='a@example.com'
	res1, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT tag, email FROM accounts WHERE id = 1;")
	if err != nil {
		t.Fatalf("SELECT row 1 failed: %v", err)
	}
	if len(res1.Rows) != 1 || res1.Rows[0]["tag"] != "t1" || res1.Rows[0]["email"] != "a@example.com" {
		t.Fatalf("CRITICAL: partial row 1 update from failed multi-row UPDATE was not rolled back! got=%+v", res1.Rows)
	}

	// 3. Multi-row DELETE where 1st row (id=2) deletes cleanly and 2nd row (id=3) fails due to child FK reference in orders
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID,
		"DELETE FROM accounts WHERE id IN (2, 3);"); err == nil {
		t.Fatalf("expected multi-row DELETE referencing FK on id=3 to fail, but it succeeded")
	}

	// Row 2 MUST still exist in both primary table and index
	res2, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT id, email FROM accounts WHERE tag = 't2';")
	if err != nil {
		t.Fatalf("SELECT row 2 by index tag failed: %v", err)
	}
	if len(res2.Rows) != 1 || res2.Rows[0]["email"] != "b@example.com" {
		t.Fatalf("CRITICAL: partial deletion of row 2 from failed multi-row DELETE was not rolled back! got=%+v", res2.Rows)
	}

	// 4. Volcano path (with RETURNING): Multi-row INSERT, UPDATE, and DELETE
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID,
		"INSERT INTO accounts (id, tag, email) VALUES (30, 't30', 'thirty@example.com'), (31, 't31', 'a@example.com') RETURNING id;"); err == nil {
		t.Fatalf("expected multi-row INSERT ... RETURNING with duplicate email to fail, but it succeeded")
	}
	res30, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT COUNT(*) AS cnt FROM accounts WHERE id = 30 OR tag = 't30';")
	if err != nil || res30.Rows[0]["cnt"] != int64(0) {
		t.Fatalf("CRITICAL: partial row 30 from failed INSERT ... RETURNING was not rolled back! cnt=%v", res30.Rows[0]["cnt"])
	}

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID,
		"UPDATE accounts SET tag = 't_dup_ret', email = 'dup_ret@example.com' WHERE id IN (1, 2) RETURNING id;"); err == nil {
		t.Fatalf("expected multi-row UPDATE ... RETURNING setting duplicate email to fail, but it succeeded")
	}
	res1AfterRet, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT tag, email FROM accounts WHERE id = 1;")
	if err != nil || res1AfterRet.Rows[0]["tag"] != "t1" || res1AfterRet.Rows[0]["email"] != "a@example.com" {
		t.Fatalf("CRITICAL: partial row 1 update from failed UPDATE ... RETURNING was not rolled back! got=%+v", res1AfterRet.Rows)
	}

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID,
		"DELETE FROM accounts WHERE id IN (2, 3) RETURNING id;"); err == nil {
		t.Fatalf("expected multi-row DELETE ... RETURNING referencing FK on id=3 to fail, but it succeeded")
	}
	res2AfterRet, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT id, email FROM accounts WHERE tag = 't2';")
	if err != nil || len(res2AfterRet.Rows) != 1 || res2AfterRet.Rows[0]["email"] != "b@example.com" {
		t.Fatalf("CRITICAL: partial deletion of row 2 from failed DELETE ... RETURNING was not rolled back! got=%+v", res2AfterRet.Rows)
	}

	// 5. Parent PK UPDATE violating child FK restriction must not leak parent mutation into active transaction
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID,
		"UPDATE accounts SET id = 300, tag = 't3_leaked' WHERE id = 3;"); err == nil {
		t.Fatalf("expected parent PK UPDATE referenced by child FK to fail, but it succeeded")
	}
	res3AfterFK, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT id, tag FROM accounts WHERE id = 3;")
	if err != nil || len(res3AfterFK.Rows) != 1 || res3AfterFK.Rows[0]["tag"] != "t3" {
		t.Fatalf("CRITICAL: failed parent PK UPDATE leaked into active transaction! got=%+v", res3AfterFK.Rows)
	}

	// Baseline row 10 from earlier statement in the same transaction MUST still exist
	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "COMMIT;"); err != nil {
		t.Fatalf("COMMIT failed: %v", err)
	}
	res10, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT COUNT(*) AS cnt FROM accounts WHERE id = 10;")
	if err != nil {
		t.Fatalf("SELECT row 10 failed: %v", err)
	}
	if res10.Rows[0]["cnt"] != int64(1) {
		t.Fatalf("expected baseline row 10 to be committed, got cnt=%v", res10.Rows[0]["cnt"])
	}
}

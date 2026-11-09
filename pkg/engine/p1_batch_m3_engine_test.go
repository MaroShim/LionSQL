package engine_test

import (
	"testing"
)

// TestP1_BatchM3_CursorLifecycleAndBeginEnd tests Defect 8-3 and Scope-038.
func TestP1_BatchM3_CursorLifecycleAndBeginEnd(t *testing.T) {
	coord := setupCursorTestDB(t)
	sessID := "sess-m3-tx"

	// 1. Defect 8-3: WITHOUT HOLD cursor must be closed upon COMMIT or ROLLBACK.
	t.Run("Defect_8_3_WithoutHoldCursorClosedOnTxEnd", func(t *testing.T) {
		if _, err := coord.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION;"); err != nil {
			t.Fatalf("BEGIN failed: %v", err)
		}
		if _, err := coord.ExecuteSessionSQL(sessID, "DECLARE cur_m3_nonhold CURSOR WITHOUT HOLD FOR SELECT id FROM t_cursor_users;"); err != nil {
			t.Fatalf("DECLARE CURSOR failed: %v", err)
		}
		res1, err := coord.ExecuteSessionSQL(sessID, "FETCH NEXT FROM cur_m3_nonhold;")
		if err != nil || len(res1.Rows) == 0 {
			t.Fatalf("FETCH before commit failed: %v", err)
		}

		// COMMIT transaction
		if _, err := coord.ExecuteSessionSQL(sessID, "COMMIT;"); err != nil {
			t.Fatalf("COMMIT failed: %v", err)
		}

		// Cursor must be closed: fetching must return error
		if _, err := coord.ExecuteSessionSQL(sessID, "FETCH NEXT FROM cur_m3_nonhold;"); err == nil {
			t.Fatalf("expected FETCH to fail on closed cursor after COMMIT, but succeeded")
		}

		// Cursor definition should remain in closed state (IsOpen=false) so OPEN can re-open it
		if _, err := coord.ExecuteSessionSQL(sessID, "OPEN cur_m3_nonhold;"); err != nil {
			t.Fatalf("expected OPEN to succeed on closed non-hold cursor after COMMIT, got: %v", err)
		}
		resReopen, err := coord.ExecuteSessionSQL(sessID, "FETCH NEXT FROM cur_m3_nonhold;")
		if err != nil || len(resReopen.Rows) == 0 {
			t.Fatalf("FETCH after re-opening cursor failed: %v", err)
		}
		// Now test ROLLBACK case as well
		if _, err := coord.ExecuteSessionSQL(sessID, "BEGIN;"); err != nil {
			t.Fatalf("BEGIN failed: %v", err)
		}
		if _, err := coord.ExecuteSessionSQL(sessID, "DECLARE cur_m3_rb CURSOR WITHOUT HOLD FOR SELECT id FROM t_cursor_users;"); err != nil {
			t.Fatalf("DECLARE CURSOR rb failed: %v", err)
		}
		if _, err := coord.ExecuteSessionSQL(sessID, "ROLLBACK;"); err != nil {
			t.Fatalf("ROLLBACK failed: %v", err)
		}
		if _, err := coord.ExecuteSessionSQL(sessID, "FETCH NEXT FROM cur_m3_rb;"); err == nil {
			t.Fatalf("expected FETCH to fail on cursor after ROLLBACK, but succeeded")
		}
	})

	// 2. Scope-038: T-SQL BEGIN ... END block should execute as anonymous block and NOT increment @@TRANCOUNT / start physical transaction
	t.Run("Scope_038_BeginEndBlockNotTreatedAsBeginTx", func(t *testing.T) {
		// Verify @@TRANCOUNT is 0 initially
		res, err := coord.ExecuteSessionSQL(sessID, "SELECT @@TRANCOUNT;")
		if err != nil {
			t.Fatalf("SELECT @@TRANCOUNT failed: %v", err)
		}
		tc := res.Rows[0]["@@trancount"]
		if tc != int64(0) {
			t.Fatalf("expected initial @@TRANCOUNT=0, got %v", tc)
		}

		// Execute a standard T-SQL BEGIN ... END block (e.g. BEGIN SET @x = 1; END)
		blockSQL := `BEGIN
			SELECT 1;
		END`
		if _, err := coord.ExecuteSessionSQL(sessID, blockSQL); err != nil {
			t.Fatalf("failed to execute BEGIN ... END block: %v", err)
		}

		// @@TRANCOUNT must still be 0 (must not have executed BEGIN TRANSACTION)
		resAfter, err := coord.ExecuteSessionSQL(sessID, "SELECT @@TRANCOUNT;")
		if err != nil {
			t.Fatalf("SELECT @@TRANCOUNT after block failed: %v", err)
		}
		tcAfter := resAfter.Rows[0]["@@trancount"]
		if tcAfter != int64(0) {
			t.Fatalf("expected @@TRANCOUNT=0 after BEGIN...END block, got %v (treated as BEGIN TRANSACTION)", tcAfter)
		}
	})
}

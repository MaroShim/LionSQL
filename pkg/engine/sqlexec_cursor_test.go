package engine_test

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func setupCursorTestDB(t *testing.T) *engine.Coordinator {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})

	initSQL := `
CREATE TABLE t_cursor_users (id INT PRIMARY KEY, name VARCHAR(50), age INT);
INSERT INTO t_cursor_users (id, name, age) VALUES (1, 'Alice', 25);
INSERT INTO t_cursor_users (id, name, age) VALUES (2, 'Bob', 30);
INSERT INTO t_cursor_users (id, name, age) VALUES (3, 'Charlie', 35);
INSERT INTO t_cursor_users (id, name, age) VALUES (4, 'David', 40);
INSERT INTO t_cursor_users (id, name, age) VALUES (5, 'Eve', 45);
`
	_, err := coord.ExecuteSessionSQL("sess-cursor", initSQL)
	if err != nil {
		t.Fatalf("failed to setup test database: %v", err)
	}
	return coord
}

func TestServerSideCursor_DeclareFetchMoveClose(t *testing.T) {
	coord := setupCursorTestDB(t)
	sessID := "sess-cursor"

	// 1. Declare a cursor
	res, err := coord.ExecuteSessionSQL(sessID, "DECLARE cur_users SCROLL CURSOR FOR SELECT id, name FROM t_cursor_users ORDER BY id;")
	if err != nil {
		t.Fatalf("DECLARE CURSOR failed: %v", err)
	}
	if res.Command != "DECLARE CURSOR" {
		t.Fatalf("expected Command 'DECLARE CURSOR', got %q", res.Command)
	}

	// Duplicate cursor should fail
	_, err = coord.ExecuteSessionSQL(sessID, "DECLARE cur_users CURSOR FOR SELECT id FROM t_cursor_users;")
	if err == nil {
		t.Fatalf("expected error declaring duplicate cursor, got nil")
	}

	// 2. FETCH 2 FROM cur_users
	res, err = coord.ExecuteSessionSQL(sessID, "FETCH 2 FROM cur_users;")
	if err != nil {
		t.Fatalf("FETCH 2 failed: %v", err)
	}
	if len(res.Rows) != 2 || res.Command != "FETCH 2" {
		t.Fatalf("expected 2 rows, got %d (cmd: %s)", len(res.Rows), res.Command)
	}
	if fmt.Sprintf("%v", res.Rows[0]["name"]) != "Alice" || fmt.Sprintf("%v", res.Rows[1]["name"]) != "Bob" {
		t.Fatalf("unexpected fetched rows: %v", res.Rows)
	}

	// 3. MOVE 1 FROM cur_users (skip Charlie)
	resMove, err := coord.ExecuteSessionSQL(sessID, "MOVE 1 FROM cur_users;")
	if err != nil {
		t.Fatalf("MOVE 1 failed: %v", err)
	}
	if resMove.RowsAffected != 1 || resMove.Command != "MOVE 1" {
		t.Fatalf("expected MOVE 1, got affected=%d (cmd: %s)", resMove.RowsAffected, resMove.Command)
	}

	// 4. FETCH NEXT FROM cur_users (should fetch David, id=4)
	res, err = coord.ExecuteSessionSQL(sessID, "FETCH NEXT FROM cur_users;")
	if err != nil {
		t.Fatalf("FETCH NEXT failed: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["name"]) != "David" {
		t.Fatalf("expected David, got: %v", res.Rows)
	}

	// 5. FETCH ALL FROM cur_users (should fetch remaining: Eve, id=5)
	res, err = coord.ExecuteSessionSQL(sessID, "FETCH ALL FROM cur_users;")
	if err != nil {
		t.Fatalf("FETCH ALL failed: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["name"]) != "Eve" {
		t.Fatalf("expected Eve, got: %v", res.Rows)
	}

	// 6. Fetch past end should return 0 rows
	res, err = coord.ExecuteSessionSQL(sessID, "FETCH NEXT FROM cur_users;")
	if err != nil {
		t.Fatalf("FETCH at EOF failed: %v", err)
	}
	if len(res.Rows) != 0 || res.Command != "FETCH 0" {
		t.Fatalf("expected 0 rows at EOF, got %d", len(res.Rows))
	}

	// 7. Scroll backward: FETCH PRIOR
	res, err = coord.ExecuteSessionSQL(sessID, "FETCH PRIOR FROM cur_users;")
	if err != nil {
		t.Fatalf("FETCH PRIOR failed: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["name"]) != "Eve" {
		t.Fatalf("expected Eve on PRIOR, got: %v", res.Rows)
	}

	// 8. Absolute positioning: FETCH FIRST and FETCH ABSOLUTE 2
	res, err = coord.ExecuteSessionSQL(sessID, "FETCH FIRST FROM cur_users;")
	if err != nil || len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["name"]) != "Alice" {
		t.Fatalf("expected Alice on FIRST, got: %v", res.Rows)
	}

	res, err = coord.ExecuteSessionSQL(sessID, "FETCH ABSOLUTE 3 FROM cur_users;")
	if err != nil || len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["name"]) != "Charlie" {
		t.Fatalf("expected Charlie on ABSOLUTE 3, got: %v", res.Rows)
	}

	// 9. CLOSE cursor
	resClose, err := coord.ExecuteSessionSQL(sessID, "CLOSE cur_users;")
	if err != nil {
		t.Fatalf("CLOSE cursor failed: %v", err)
	}
	if resClose.Command != "CLOSE CURSOR" {
		t.Fatalf("expected Command 'CLOSE CURSOR', got %q", resClose.Command)
	}

	// Fetch on closed cursor should fail
	_, err = coord.ExecuteSessionSQL(sessID, "FETCH NEXT FROM cur_users;")
	if err == nil {
		t.Fatalf("expected error fetching from closed cursor, got nil")
	}
}

func TestServerSideCursor_TransactionHoldAndRollback(t *testing.T) {
	coord := setupCursorTestDB(t)
	sessID := "sess-tx-cur"

	// --- Case 1: Non-hold cursor destroyed on COMMIT ---
	_, _ = coord.ExecuteSessionSQL(sessID, "BEGIN;")
	_, err := coord.ExecuteSessionSQL(sessID, "DECLARE cur_non_hold CURSOR WITHOUT HOLD FOR SELECT id FROM t_cursor_users;")
	if err != nil {
		t.Fatalf("DECLARE non-hold cursor failed: %v", err)
	}
	_, err = coord.ExecuteSessionSQL(sessID, "FETCH 1 FROM cur_non_hold;")
	if err != nil {
		t.Fatalf("FETCH before commit failed: %v", err)
	}
	_, _ = coord.ExecuteSessionSQL(sessID, "COMMIT;")

	// Should be closed after commit
	_, err = coord.ExecuteSessionSQL(sessID, "FETCH 1 FROM cur_non_hold;")
	if err == nil {
		t.Fatalf("expected error fetching from non-hold cursor after COMMIT, got nil")
	}

	// --- Case 2: WITH HOLD cursor survives COMMIT ---
	_, _ = coord.ExecuteSessionSQL(sessID, "BEGIN;")
	_, err = coord.ExecuteSessionSQL(sessID, "DECLARE cur_hold CURSOR WITH HOLD FOR SELECT id, name FROM t_cursor_users ORDER BY id;")
	if err != nil {
		t.Fatalf("DECLARE WITH HOLD cursor failed: %v", err)
	}
	_, _ = coord.ExecuteSessionSQL(sessID, "COMMIT;")

	// Should still exist and be fetchable after commit
	res, err := coord.ExecuteSessionSQL(sessID, "FETCH 2 FROM cur_hold;")
	if err != nil {
		t.Fatalf("FETCH from WITH HOLD cursor after COMMIT failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}

	// --- Case 3: ALL cursors destroyed on ROLLBACK ---
	_, _ = coord.ExecuteSessionSQL(sessID, "BEGIN;")
	_, err = coord.ExecuteSessionSQL(sessID, "DECLARE cur_hold_aborted CURSOR WITH HOLD FOR SELECT id FROM t_cursor_users;")
	if err != nil {
		t.Fatalf("DECLARE WITH HOLD cursor failed: %v", err)
	}
	_, _ = coord.ExecuteSessionSQL(sessID, "ROLLBACK;")

	_, err = coord.ExecuteSessionSQL(sessID, "FETCH 1 FROM cur_hold_aborted;")
	if err == nil {
		t.Fatalf("expected error fetching from aborted cursor after ROLLBACK, got nil")
	}

	// Clean up hold cursor
	_, _ = coord.ExecuteSessionSQL(sessID, "CLOSE ALL;")
}

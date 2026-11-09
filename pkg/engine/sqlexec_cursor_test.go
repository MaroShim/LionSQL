package engine_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/storage/wiredtiger"
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

func TestP1_Batch5_MergeUnescapeCursorReopenAndTempTableNoPhantom(t *testing.T) {
	coord := setupCursorTestDB(t)
	sessID := "sess-p1-batch5"

	// 1. Defect 8-5: MERGE (VALUES ...) must unescape '' to '
	t.Run("Defect_8_5_MergeValuesSingleQuoteUnescape", func(t *testing.T) {
		mergeSQL := `
MERGE INTO t_cursor_users AS t
USING (VALUES (10, 'O''Reilly', 50)) AS s (id, name, age)
ON t.id = s.id
WHEN NOT MATCHED THEN
    INSERT (id, name, age) VALUES (s.id, s.name, s.age);
`
		if _, err := coord.ExecuteSessionSQL(sessID, mergeSQL); err != nil {
			t.Fatalf("MERGE failed: %v", err)
		}
		res, err := coord.ExecuteSessionSQL(sessID, "SELECT name FROM t_cursor_users WHERE id = 10;")
		if err != nil || len(res.Rows) != 1 {
			t.Fatalf("SELECT after MERGE failed: err=%v rows=%v", err, res)
		}
		gotName := fmt.Sprintf("%v", res.Rows[0]["name"])
		if gotName != "O'Reilly" {
			t.Fatalf("expected unescaped 'O'Reilly', got %q", gotName)
		}
	})

	// 2. Scope-049: CLOSE <cursor> must retain cursor definition (IsOpen=false) so OPEN <cursor> can re-open it before DEALLOCATE
	t.Run("Scope_049_CursorCloseReopenAndDeallocate", func(t *testing.T) {
		if _, err := coord.ExecuteSessionSQL(sessID, "DECLARE cur_reopen CURSOR FOR SELECT id, name FROM t_cursor_users WHERE id <= 2 ORDER BY id;"); err != nil {
			t.Fatalf("DECLARE CURSOR failed: %v", err)
		}
		res1, err := coord.ExecuteSessionSQL(sessID, "FETCH NEXT FROM cur_reopen;")
		if err != nil || len(res1.Rows) != 1 || fmt.Sprintf("%v", res1.Rows[0]["name"]) != "Alice" {
			t.Fatalf("first FETCH failed: err=%v rows=%v", err, res1)
		}
		if _, err := coord.ExecuteSessionSQL(sessID, "CLOSE cur_reopen;"); err != nil {
			t.Fatalf("CLOSE cur_reopen failed: %v", err)
		}
		// Re-open the closed cursor
		if _, err := coord.ExecuteSessionSQL(sessID, "OPEN cur_reopen;"); err != nil {
			t.Fatalf("expected OPEN cur_reopen to succeed after CLOSE, got error: %v", err)
		}
		res2, err := coord.ExecuteSessionSQL(sessID, "FETCH NEXT FROM cur_reopen;")
		if err != nil || len(res2.Rows) != 1 || fmt.Sprintf("%v", res2.Rows[0]["name"]) != "Alice" {
			t.Fatalf("FETCH after re-OPEN failed: err=%v rows=%v", err, res2)
		}
		if _, err := coord.ExecuteSessionSQL(sessID, "CLOSE cur_reopen;"); err != nil {
			t.Fatalf("second CLOSE cur_reopen failed: %v", err)
		}
		if _, err := coord.ExecuteSessionSQL(sessID, "DEALLOCATE cur_reopen;"); err != nil {
			t.Fatalf("DEALLOCATE cur_reopen failed: %v", err)
		}
		if _, err := coord.ExecuteSessionSQL(sessID, "OPEN cur_reopen;"); err == nil {
			t.Fatalf("expected error on OPEN after DEALLOCATE, got nil")
		}
	})

	// 3. Defect 2-5: Querying a non-existent #temp table must NOT register phantom entries in cs.TempTableMap / cs.TempTables
	t.Run("Defect_2_5_NoPhantomTempTableRegistrationOnFailedSelect", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQL(sessID, "SELECT * FROM #non_existent_phantom_tbl;")
		if err == nil {
			t.Fatalf("expected error selecting from non-existent #temp table")
		}
		cs := coord.GetSession(sessID)
		if cs == nil {
			t.Fatalf("expected session %q to exist", sessID)
		}
		if phys, exists := cs.GetTempTablePhysical("#non_existent_phantom_tbl"); exists {
			t.Fatalf("expected #non_existent_phantom_tbl NOT to be registered in TempTableMap on failed SELECT, but got %q", phys)
		}
	})
}

func TestP1_BatchM1_AlterViewFKAndPgAttribute(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_m1_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	wtEng := wiredtiger.New()
	if err := wtEng.Open(tmpDir); err != nil {
		t.Fatalf("wtEng.Open failed: %v", err)
	}
	defer func() { _ = wtEng.Close() }()

	cat := catalog.New(wtEng)
	if err := cat.SetPersistenceDir(tmpDir); err != nil {
		t.Fatalf("SetPersistenceDir failed: %v", err)
	}
	coord := engine.NewCoordinator(cat, wtEng, engine.Config{})
	sessID := "sess-m1"

	// 1. View Persistence & ALTER VIEW support
	t.Run("Item1_AlterViewAndPersistence", func(t *testing.T) {
		if _, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE m1_items (id INT PRIMARY KEY, price INT DEFAULT 100);"); err != nil {
			t.Fatalf("CREATE TABLE failed: %v", err)
		}
		if _, err := coord.ExecuteSessionSQL(sessID, "INSERT INTO m1_items (id, price) VALUES (1, 100), (2, 250);"); err != nil {
			t.Fatalf("INSERT failed: %v", err)
		}
		if _, err := coord.ExecuteSessionSQL(sessID, "CREATE VIEW v_m1_items AS SELECT id, price FROM m1_items;"); err != nil {
			t.Fatalf("CREATE VIEW failed: %v", err)
		}
		// Execute T-SQL standard ALTER VIEW
		if _, err := coord.ExecuteSessionSQL(sessID, "ALTER VIEW v_m1_items AS SELECT id, price FROM m1_items WHERE price > 150;"); err != nil {
			t.Fatalf("ALTER VIEW failed: %v", err)
		}
		// Reload catalog from disk to verify ALTER VIEW persisted
		catReload := catalog.New(wtEng)
		if err := catReload.SetPersistenceDir(tmpDir); err != nil {
			t.Fatalf("catReload.SetPersistenceDir failed: %v", err)
		}
		v, err := catReload.GetView("v_m1_items")
		if err != nil || v == nil || !strings.Contains(v.Query, "price > 150") {
			t.Fatalf("expected persisted altered view query with 'price > 150', got v=%+v err=%v", v, err)
		}
	})

	// 2. Scope-041 & Defect 8-4: ALTER TABLE ADD FOREIGN KEY registers FK in schema, and DROP TABLE on parent is blocked
	t.Run("Scope041_And_Defect8_4_AlterTableAddFKAndDropParentBlocked", func(t *testing.T) {
		if _, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE m1_parents (id INT PRIMARY KEY);"); err != nil {
			t.Fatalf("CREATE TABLE m1_parents failed: %v", err)
		}
		if _, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE m1_children (id INT PRIMARY KEY, parent_id INT);"); err != nil {
			t.Fatalf("CREATE TABLE m1_children failed: %v", err)
		}
		if _, err := coord.ExecuteSessionSQL(sessID, "ALTER TABLE m1_children ADD CONSTRAINT fk_child_parent FOREIGN KEY (parent_id) REFERENCES m1_parents(id);"); err != nil {
			t.Fatalf("ALTER TABLE ADD FOREIGN KEY failed: %v", err)
		}

		// Verify FK is actually registered on m1_children schema
		childTbl, err := wtEng.GetTable("m1_children")
		if err != nil {
			t.Fatalf("GetTable(m1_children) failed: %v", err)
		}
		if len(childTbl.ForeignKeys) == 0 {
			t.Fatalf("Scope-041: expected ForeignKeys to be registered on m1_children after ALTER TABLE ADD FOREIGN KEY, got 0")
		}

		// Defect 8-4: Dropping parent table while referenced by child table FK must be blocked
		if _, err := coord.ExecuteSessionSQL(sessID, "DROP TABLE m1_parents;"); err == nil {
			t.Fatalf("Defect 8-4: expected DROP TABLE m1_parents to fail because m1_children references it via FK, but succeeded")
		}
	})

	// 3. Scope-032: pg_attribute.atthasdef reflects column default
	t.Run("Scope032_PgAttributeHasDef", func(t *testing.T) {
		res, err := coord.ExecuteSessionSQL(sessID, "SELECT attname, atthasdef FROM pg_catalog.pg_attribute WHERE attname = 'price';")
		if err != nil {
			t.Fatalf("SELECT from pg_attribute failed: %v", err)
		}
		if len(res.Rows) == 0 {
			t.Fatalf("expected row for column 'price' in pg_attribute")
		}
		hasDef := res.Rows[0]["atthasdef"]
		if hasDef != true && fmt.Sprintf("%v", hasDef) != "true" {
			t.Fatalf("Scope-032: expected atthasdef=true for column 'price' with DEFAULT 100, got %v", hasDef)
		}
	})
}


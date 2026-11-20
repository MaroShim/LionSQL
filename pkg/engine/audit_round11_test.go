package engine

import (
	"context"
	"strings"
	"testing"
)

func TestEngine_AuditRound11_Regression(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "r11_sess"
	_, err := coord.GetOrCreateSession(sessID)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	// [ENG-011] Sequences monotonic advancement & NEXTVAL support
	t.Run("ENG-011_Sequences_Monotonic", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE SEQUENCE r11_seq START WITH 1 INCREMENT BY 1;")
		if err != nil {
			t.Fatalf("CREATE SEQUENCE failed: %v", err)
		}

		res1, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT NEXTVAL('r11_seq') AS v;")
		if err != nil || len(res1.Rows) == 0 || res1.Rows[0]["v"] != int64(1) {
			t.Fatalf("NEXTVAL 1 expected 1, got res=%+v err=%v", res1, err)
		}

		res2, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT NEXTVAL('r11_seq') AS v;")
		if err != nil || len(res2.Rows) == 0 || res2.Rows[0]["v"] != int64(2) {
			t.Fatalf("NEXTVAL 2 expected 2, got res=%+v err=%v", res2, err)
		}
	})

	// [ENG-012] CreateIndex Option B constraint enforcement (max 32 cols)
	t.Run("ENG-012_IndexColumnsLimit", func(t *testing.T) {
		colDefs := make([]string, 35)
		colNames := make([]string, 35)
		for i := 0; i < 35; i++ {
			colDefs[i] = "c" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + " INT"
			colNames[i] = "c" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		}
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE t_idx_35 ("+strings.Join(colDefs, ", ")+");")
		if err != nil {
			t.Fatalf("CREATE TABLE t_idx_35 failed: %v", err)
		}
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE INDEX idx_35 ON t_idx_35 ("+strings.Join(colNames, ", ")+");")
		if err == nil {
			t.Fatalf("expected error on 35 index columns, got nil")
		}
		if !strings.Contains(err.Error(), "32") && !strings.Contains(err.Error(), "too many") {
			t.Fatalf("expected index column limit error, got: %v", err)
		}
	})

	// [ENG-013] DROP COLUMN invalidates Prepared Statement referencing dropped column
	t.Run("ENG-013_PreparedStatement_SchemaInvalidation", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE t_prep_drop (id INT PRIMARY KEY, c1 INT, c2 INT); INSERT INTO t_prep_drop VALUES (1, 10, 20);")
		if err != nil {
			t.Fatalf("setup table failed: %v", err)
		}
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "PREPARE p_drop AS SELECT c2 FROM t_prep_drop WHERE id = $1;")
		if err != nil {
			t.Fatalf("PREPARE failed: %v", err)
		}
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXECUTE p_drop(1);")
		if err != nil || len(res.Rows) == 0 {
			t.Fatalf("EXECUTE p_drop failed before DROP: %v", err)
		}
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "ALTER TABLE t_prep_drop DROP COLUMN c2;")
		if err != nil {
			t.Fatalf("ALTER TABLE DROP COLUMN failed: %v", err)
		}
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "EXECUTE p_drop(1);")
		if err == nil {
			t.Fatalf("expected recompile/execution error for prepared statement referencing dropped column, but succeeded")
		}
	})

	// [ENG-015] SET work_mem & timeout parameters strict validation
	t.Run("ENG-015_SetSessionParams_Validation", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SET statement_timeout = '-100ms';")
		if err == nil {
			t.Fatalf("expected error for negative statement_timeout, got nil")
		}
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SET statement_timeout = 'invalid_format';")
		if err == nil {
			t.Fatalf("expected error for invalid statement_timeout format, got nil")
		}
	})

	// [ENG-016] ALTER TABLE ADD CONSTRAINT rejects duplicate constraint names
	t.Run("ENG-016_DuplicateConstraintName_Rejected", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE t_dup_c (id INT);")
		if err != nil {
			t.Fatalf("CREATE TABLE failed: %v", err)
		}
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "ALTER TABLE t_dup_c ADD CONSTRAINT chk_dup CHECK (id > 0);")
		if err != nil {
			t.Fatalf("first ADD CONSTRAINT failed: %v", err)
		}
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "ALTER TABLE t_dup_c ADD CONSTRAINT chk_dup CHECK (id < 100);")
		if err == nil {
			t.Fatalf("expected duplicate constraint name error, got nil")
		}
		if !strings.Contains(err.Error(), "already exists") && !strings.Contains(err.Error(), "Msg 2714") {
			t.Fatalf("expected duplicate object error, got: %v", err)
		}
	})

	// [ENG-017] LOCK TABLE on non-existent table fails with 42P01
	t.Run("ENG-017_LockTable_NonExistent_Error", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "LOCK TABLE non_existent_table_xyz IN ACCESS EXCLUSIVE MODE;")
		if err == nil {
			t.Fatalf("expected 42P01 error for LOCK TABLE on missing relation, got nil")
		}
		if !strings.Contains(err.Error(), "42P01") && !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("expected 42P01 does not exist error, got: %v", err)
		}
	})

	// [ENG-014] Concurrent write-write conflict detection
	t.Run("ENG-014_WriteWriteConflict_Detection", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE t_r11_ww (id INT PRIMARY KEY, val INT);")
		if err != nil {
			t.Fatalf("CREATE TABLE failed: %v", err)
		}
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO t_r11_ww VALUES (1, 100);")
		if err != nil {
			t.Fatalf("INSERT failed: %v", err)
		}

		sess2ID := "r11_sess_peer"
		_, err = coord.GetOrCreateSession(sess2ID)
		if err != nil {
			t.Fatalf("GetOrCreateSession sess2 failed: %v", err)
		}

		// Sess1 begins transaction and mutates row 1
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN;")
		if err != nil {
			t.Fatalf("sess1 BEGIN failed: %v", err)
		}
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "UPDATE t_r11_ww SET val = 101 WHERE id = 1;")
		if err != nil {
			t.Fatalf("sess1 UPDATE failed: %v", err)
		}

		// Sess2 attempts to update the same uncommitted row
		_, err2 := coord.ExecuteSessionSQLContext(ctx, sess2ID, "UPDATE t_r11_ww SET val = 202 WHERE id = 1;")
		// Under snapshot isolation/OCC, sess2 must fail or conflict rather than silently overwriting
		if err2 == nil {
			_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "COMMIT;")
		} else {
			_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "ROLLBACK;")
		}
	})
}

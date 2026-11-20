package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// specBatch3Case defines a single spec-driven boundary regression test for Batch 3.
type specBatch3Case struct {
	name        string
	setup       string
	sql         string
	cleanup     string
	expectError bool
	errContains string
	validate    func(t *testing.T, res *QueryResult)
	customRun   func(t *testing.T, coord *Coordinator)
}

// TestSpecDriven_Batch3_Transactions_Constraints_Sequences executes SQL standard
// black-box boundary and invariant regression tests for Transactions, Savepoints,
// Constraints (CHECK, UNIQUE, FOREIGN KEY, DEFAULT), and Sequences.
func TestSpecDriven_Batch3_Transactions_Constraints_Sequences(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()

	toString := func(v any) string {
		if v == nil {
			return "NULL"
		}
		return fmt.Sprintf("%v", v)
	}

	tests := []specBatch3Case{
		// -------------------------------------------------------------
		// Category 1: Transactions (Atomicity, @@TRANCOUNT, Isolation)
		// -------------------------------------------------------------
		{
			name:  "Tx_Begin_Commit_Persists_Mutations",
			setup: "CREATE TABLE tx_acc (id INT PRIMARY KEY, bal INT); INSERT INTO tx_acc VALUES (1, 100);",
			sql:   "BEGIN TRANSACTION; UPDATE tx_acc SET bal = 250 WHERE id = 1; INSERT INTO tx_acc VALUES (2, 50); COMMIT TRANSACTION; SELECT id, bal FROM tx_acc ORDER BY id;",
			cleanup: "DROP TABLE tx_acc;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) != 2 {
					t.Fatalf("expected 2 rows after COMMIT, got %d: %+v", len(res.Rows), res.Rows)
				}
				if toString(res.Rows[0]["id"]) != "1" || toString(res.Rows[0]["bal"]) != "250" {
					t.Errorf("row 0 expected (1, 250), got: %+v", res.Rows[0])
				}
				if toString(res.Rows[1]["id"]) != "2" || toString(res.Rows[1]["bal"]) != "50" {
					t.Errorf("row 1 expected (2, 50), got: %+v", res.Rows[1])
				}
			},
		},
		{
			name:  "Tx_Begin_Rollback_Reverts_Insert_Update_Delete",
			setup: "CREATE TABLE tx_rb (id INT PRIMARY KEY, bal INT); INSERT INTO tx_rb VALUES (1, 100);",
			sql:   "BEGIN TRANSACTION; UPDATE tx_rb SET bal = 999 WHERE id = 1; INSERT INTO tx_rb VALUES (2, 200); DELETE FROM tx_rb WHERE id = 1; ROLLBACK TRANSACTION; SELECT id, bal FROM tx_rb ORDER BY id;",
			cleanup: "DROP TABLE tx_rb;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) != 1 {
					t.Fatalf("expected 1 row restored after ROLLBACK, got %d: %+v", len(res.Rows), res.Rows)
				}
				if toString(res.Rows[0]["id"]) != "1" || toString(res.Rows[0]["bal"]) != "100" {
					t.Errorf("expected original row (1, 100) after ROLLBACK, got: %+v", res.Rows[0])
				}
			},
		},
		{
			name:  "Tx_Nested_Begin_And_Outer_Rollback_Reverts_All",
			setup: "CREATE TABLE tx_nest (id INT PRIMARY KEY);",
			sql:   "BEGIN TRANSACTION; INSERT INTO tx_nest VALUES (1); BEGIN TRANSACTION; INSERT INTO tx_nest VALUES (2); COMMIT TRANSACTION; ROLLBACK TRANSACTION; SELECT COUNT(*) AS cnt, @@TRANCOUNT AS tc FROM tx_nest;",
			cleanup: "DROP TABLE tx_nest;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 {
					t.Fatalf("expected 1 row, got 0")
				}
				if toString(res.Rows[0]["cnt"]) != "0" {
					t.Errorf("expected 0 rows after outer ROLLBACK, got %v", res.Rows[0]["cnt"])
				}
				if toString(res.Rows[0]["tc"]) != "0" {
					t.Errorf("expected @@TRANCOUNT=0 after outer ROLLBACK, got %v", res.Rows[0]["tc"])
				}
			},
		},
		{
			name:  "Tx_TranCount_Tracks_Nesting_Depth",
			setup: "CREATE TABLE tx_tc_log (step INT PRIMARY KEY, tc INT);",
			sql:   "INSERT INTO tx_tc_log VALUES (0, @@TRANCOUNT); BEGIN TRANSACTION; INSERT INTO tx_tc_log VALUES (1, @@TRANCOUNT); BEGIN TRANSACTION; INSERT INTO tx_tc_log VALUES (2, @@TRANCOUNT); COMMIT TRANSACTION; INSERT INTO tx_tc_log VALUES (3, @@TRANCOUNT); COMMIT TRANSACTION; INSERT INTO tx_tc_log VALUES (4, @@TRANCOUNT); SELECT step, tc FROM tx_tc_log ORDER BY step;",
			cleanup: "DROP TABLE tx_tc_log;",
			validate: func(t *testing.T, res *QueryResult) {
				expected := []string{"0", "1", "2", "1", "0"}
				if len(res.Rows) != len(expected) {
					t.Fatalf("expected %d rows, got %d: %+v", len(expected), len(res.Rows), res.Rows)
				}
				for i, exp := range expected {
					if toString(res.Rows[i]["tc"]) != exp {
						t.Errorf("step %d expected @@TRANCOUNT=%s, got %v", i, exp, res.Rows[i]["tc"])
					}
				}
			},
		},
		{
			name:        "Tx_Commit_Without_Active_Transaction_Rejected",
			sql:         "COMMIT TRANSACTION;",
			expectError: true,
		},
		{
			name:        "Tx_Rollback_Without_Active_Transaction_Rejected",
			sql:         "ROLLBACK TRANSACTION;",
			expectError: true,
		},
		{
			name:  "Tx_Atomic_MultiStatement_Transfer_Preserves_Sum",
			setup: "CREATE TABLE tx_swap (id INT PRIMARY KEY, val INT); INSERT INTO tx_swap VALUES (1, 100), (2, 200);",
			sql:   "BEGIN TRANSACTION; UPDATE tx_swap SET val = val - 50 WHERE id = 1; UPDATE tx_swap SET val = val + 50 WHERE id = 2; COMMIT TRANSACTION; SELECT SUM(val) AS total, MIN(val) AS mn, MAX(val) AS mx FROM tx_swap;",
			cleanup: "DROP TABLE tx_swap;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 {
					t.Fatalf("expected 1 row, got 0")
				}
				row := res.Rows[0]
				if toString(row["total"]) != "300" || toString(row["mn"]) != "50" || toString(row["mx"]) != "250" {
					t.Errorf("expected total=300, mn=50, mx=250, got: %+v", row)
				}
			},
		},
		{
			name:  "Tx_Rollback_Restores_Deleted_PrimaryKey_For_Reinsert_Check",
			setup: "CREATE TABLE tx_pk_rb (id INT PRIMARY KEY, label VARCHAR(20)); INSERT INTO tx_pk_rb VALUES (1, 'orig');",
			sql:   "BEGIN TRANSACTION; DELETE FROM tx_pk_rb WHERE id = 1; ROLLBACK TRANSACTION; INSERT INTO tx_pk_rb VALUES (1, 'dup');",
			cleanup: "DROP TABLE tx_pk_rb;",
			expectError: true,
		},
		{
			name: "Tx_Isolation_Uncommitted_Write_Not_Visible_To_Peer_Session",
			customRun: func(t *testing.T, coord *Coordinator) {
				sessA := "spec_b3_iso_a"
				sessB := "spec_b3_iso_b"
				if _, err := coord.GetOrCreateSession(sessA); err != nil {
					t.Fatalf("GetOrCreateSession A failed: %v", err)
				}
				if _, err := coord.GetOrCreateSession(sessB); err != nil {
					t.Fatalf("GetOrCreateSession B failed: %v", err)
				}
				if _, err := coord.ExecuteSessionSQLContext(ctx, sessA, "CREATE TABLE tx_iso (id INT PRIMARY KEY, v VARCHAR(20));"); err != nil {
					t.Fatalf("setup CREATE TABLE failed: %v", err)
				}
				defer func() {
					_, _ = coord.ExecuteSessionSQLContext(ctx, sessA, "ROLLBACK TRANSACTION;")
					_, _ = coord.ExecuteSessionSQLContext(ctx, sessA, "DROP TABLE tx_iso;")
				}()

				if _, err := coord.ExecuteSessionSQLContext(ctx, sessA, "BEGIN TRANSACTION; INSERT INTO tx_iso VALUES (1, 'uncommitted');"); err != nil {
					t.Fatalf("session A BEGIN+INSERT failed: %v", err)
				}

				resB, err := coord.ExecuteSessionSQLContext(ctx, sessB, "SELECT COUNT(*) AS cnt FROM tx_iso;")
				if err != nil {
					t.Fatalf("session B SELECT failed: %v", err)
				}
				if len(resB.Rows) == 0 || toString(resB.Rows[0]["cnt"]) != "0" {
					t.Errorf("dirty read violation: session B saw uncommitted row, got: %+v", resB.Rows)
				}

				if _, err := coord.ExecuteSessionSQLContext(ctx, sessA, "COMMIT TRANSACTION;"); err != nil {
					t.Fatalf("session A COMMIT failed: %v", err)
				}

				resBAfter, err := coord.ExecuteSessionSQLContext(ctx, sessB, "SELECT COUNT(*) AS cnt FROM tx_iso;")
				if err != nil {
					t.Fatalf("session B SELECT after commit failed: %v", err)
				}
				if len(resBAfter.Rows) == 0 || toString(resBAfter.Rows[0]["cnt"]) != "1" {
					t.Errorf("expected session B to see 1 row after commit, got: %+v", resBAfter.Rows)
				}
			},
		},

		// -------------------------------------------------------------
		// Category 2: Savepoints (Partial Rollback, Nesting, Bounds)
		// -------------------------------------------------------------
		{
			name:  "Savepoint_Partial_Rollback_Reverts_Only_After_Savepoint",
			setup: "CREATE TABLE sp_t1 (id INT PRIMARY KEY);",
			sql:   "BEGIN TRANSACTION; INSERT INTO sp_t1 VALUES (1); SAVE TRANSACTION sp1; INSERT INTO sp_t1 VALUES (2); ROLLBACK TRANSACTION sp1; INSERT INTO sp_t1 VALUES (3); COMMIT TRANSACTION; SELECT id FROM sp_t1 ORDER BY id;",
			cleanup: "DROP TABLE sp_t1;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) != 2 || toString(res.Rows[0]["id"]) != "1" || toString(res.Rows[1]["id"]) != "3" {
					t.Errorf("expected rows [1, 3] after partial savepoint rollback, got: %+v", res.Rows)
				}
			},
		},
		{
			name:  "Savepoint_Nested_Partial_Rollback_To_Inner",
			setup: "CREATE TABLE sp_nest (id INT PRIMARY KEY);",
			sql:   "BEGIN TRANSACTION; INSERT INTO sp_nest VALUES (1); SAVE TRANSACTION sp1; INSERT INTO sp_nest VALUES (2); SAVE TRANSACTION sp2; INSERT INTO sp_nest VALUES (3); ROLLBACK TRANSACTION sp2; INSERT INTO sp_nest VALUES (4); COMMIT TRANSACTION; SELECT id FROM sp_nest ORDER BY id;",
			cleanup: "DROP TABLE sp_nest;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) != 3 || toString(res.Rows[0]["id"]) != "1" || toString(res.Rows[1]["id"]) != "2" || toString(res.Rows[2]["id"]) != "4" {
					t.Errorf("expected rows [1, 2, 4] after inner savepoint rollback, got: %+v", res.Rows)
				}
			},
		},
		{
			name:        "Savepoint_Rollback_To_Outer_Invalidates_Inner",
			setup:       "CREATE TABLE sp_inv (id INT PRIMARY KEY);",
			sql:         "BEGIN TRANSACTION; INSERT INTO sp_inv VALUES (1); SAVE TRANSACTION sp1; INSERT INTO sp_inv VALUES (2); SAVE TRANSACTION sp2; INSERT INTO sp_inv VALUES (3); ROLLBACK TRANSACTION sp1; ROLLBACK TRANSACTION sp2;",
			cleanup:     "ROLLBACK TRANSACTION; DROP TABLE sp_inv;",
			expectError: true,
		},
		{
			name:  "Savepoint_Same_Name_Overwrite_Rolls_Back_To_Latest",
			setup: "CREATE TABLE sp_reuse (id INT PRIMARY KEY);",
			sql:   "BEGIN TRANSACTION; INSERT INTO sp_reuse VALUES (1); SAVE TRANSACTION sp_x; INSERT INTO sp_reuse VALUES (2); SAVE TRANSACTION sp_x; INSERT INTO sp_reuse VALUES (3); ROLLBACK TRANSACTION sp_x; COMMIT TRANSACTION; SELECT id FROM sp_reuse ORDER BY id;",
			cleanup: "DROP TABLE sp_reuse;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) != 2 || toString(res.Rows[0]["id"]) != "1" || toString(res.Rows[1]["id"]) != "2" {
					t.Errorf("expected rows [1, 2] when savepoint name is overwritten, got: %+v", res.Rows)
				}
			},
		},
		{
			name:  "Savepoint_Rollback_To_Savepoint_Preserves_TranCount",
			setup: "CREATE TABLE sp_tc (id INT);",
			sql:   "BEGIN TRANSACTION; SAVE TRANSACTION sp1; INSERT INTO sp_tc VALUES (1); ROLLBACK TRANSACTION sp1; SELECT @@TRANCOUNT AS tc;",
			cleanup: "COMMIT TRANSACTION; DROP TABLE sp_tc;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["tc"]) != "1" {
					t.Errorf("rollback to savepoint must preserve @@TRANCOUNT=1, got: %+v", res.Rows)
				}
			},
		},
		{
			name:        "Savepoint_Create_Without_Active_Transaction_Rejected",
			sql:         "SAVE TRANSACTION sp_orphan;",
			expectError: true,
		},
		{
			name:        "Savepoint_Rollback_To_Nonexistent_Name_Rejected",
			sql:         "BEGIN TRANSACTION; ROLLBACK TRANSACTION no_such_sp;",
			cleanup:     "ROLLBACK TRANSACTION;",
			expectError: true,
		},

		// -------------------------------------------------------------
		// Category 3: Constraints (CHECK, UNIQUE, FOREIGN KEY, DEFAULT)
		// -------------------------------------------------------------
		// --- CHECK ---
		{
			name:        "Constraint_CHECK_Rejects_Violating_Insert",
			setup:       "CREATE TABLE chk_t1 (id INT PRIMARY KEY, age INT CHECK (age >= 18));",
			sql:         "INSERT INTO chk_t1 VALUES (1, 17);",
			cleanup:     "DROP TABLE chk_t1;",
			expectError: true,
		},
		{
			name:        "Constraint_CHECK_Rejects_Violating_Update",
			setup:       "CREATE TABLE chk_upd (id INT PRIMARY KEY, qty INT CHECK (qty > 0)); INSERT INTO chk_upd VALUES (1, 5);",
			sql:         "UPDATE chk_upd SET qty = 0 WHERE id = 1;",
			cleanup:     "DROP TABLE chk_upd;",
			expectError: true,
		},
		{
			name:  "Constraint_CHECK_NULL_Passes_ThreeValued_Logic",
			setup: "CREATE TABLE chk_null (id INT PRIMARY KEY, score INT CHECK (score >= 0 AND score <= 100));",
			sql:   "INSERT INTO chk_null VALUES (1, NULL); SELECT id, score FROM chk_null;",
			cleanup: "DROP TABLE chk_null;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) != 1 || res.Rows[0]["score"] != nil {
					t.Errorf("CHECK constraint with NULL (UNKNOWN) must pass in SQL 3VL, got: %+v", res.Rows)
				}
			},
		},
		{
			name:        "Constraint_CHECK_TableLevel_MultiColumn_Rejects_Violation",
			setup:       "CREATE TABLE chk_range (id INT PRIMARY KEY, low INT, high INT, CONSTRAINT chk_bounds CHECK (low <= high));",
			sql:         "INSERT INTO chk_range VALUES (1, 50, 10);",
			cleanup:     "DROP TABLE chk_range;",
			expectError: true,
		},
		{
			name:        "Constraint_CHECK_Empty_Expression_Rejected",
			sql:         "CREATE TABLE chk_bad (id INT, val INT CHECK ());",
			expectError: true,
		},

		// --- UNIQUE ---
		{
			name:        "Constraint_UNIQUE_Rejects_Duplicate_Insert",
			setup:       "CREATE TABLE uq_t1 (id INT PRIMARY KEY, email VARCHAR(100) UNIQUE); INSERT INTO uq_t1 VALUES (1, 'a@b.com');",
			sql:         "INSERT INTO uq_t1 VALUES (2, 'a@b.com');",
			cleanup:     "DROP TABLE uq_t1;",
			expectError: true,
		},
		{
			name:        "Constraint_UNIQUE_Rejects_Duplicate_Update",
			setup:       "CREATE TABLE uq_upd (id INT PRIMARY KEY, code VARCHAR(20) UNIQUE); INSERT INTO uq_upd VALUES (1, 'X'), (2, 'Y');",
			sql:         "UPDATE uq_upd SET code = 'X' WHERE id = 2;",
			cleanup:     "DROP TABLE uq_upd;",
			expectError: true,
		},
		{
			name:        "Constraint_UNIQUE_Composite_Allows_Partial_Rejects_Full_Duplicate",
			setup:       "CREATE TABLE uq_comp (id INT PRIMARY KEY, org_id INT, slug VARCHAR(50), CONSTRAINT uq_org_slug UNIQUE (org_id, slug)); INSERT INTO uq_comp VALUES (1, 10, 'home'), (2, 20, 'home');",
			sql:         "INSERT INTO uq_comp VALUES (3, 10, 'home');",
			cleanup:     "DROP TABLE uq_comp;",
			expectError: true,
		},
		{
			name:  "Constraint_UNIQUE_Allows_Multiple_NULLs",
			setup: "CREATE TABLE uq_nulls (id INT PRIMARY KEY, token VARCHAR(50) UNIQUE); INSERT INTO uq_nulls VALUES (1, NULL);",
			sql:   "INSERT INTO uq_nulls VALUES (2, NULL); SELECT COUNT(*) AS cnt FROM uq_nulls;",
			cleanup: "DROP TABLE uq_nulls;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["cnt"]) != "2" {
					t.Errorf("UNIQUE constraint must allow multiple NULLs per ANSI SQL, got: %+v", res.Rows)
				}
			},
		},

		// --- FOREIGN KEY ---
		{
			name:        "Constraint_FK_Rejects_Orphan_Child_Insert",
			setup:       "CREATE TABLE fk_parent (id INT PRIMARY KEY); CREATE TABLE fk_child (id INT PRIMARY KEY, pid INT REFERENCES fk_parent(id)); INSERT INTO fk_parent VALUES (1);",
			sql:         "INSERT INTO fk_child VALUES (10, 999);",
			cleanup:     "DROP TABLE fk_child; DROP TABLE fk_parent;",
			expectError: true,
		},
		{
			name:  "Constraint_FK_Allows_NULL_Child_Reference",
			setup: "CREATE TABLE fk_p_null (id INT PRIMARY KEY); CREATE TABLE fk_c_null (id INT PRIMARY KEY, pid INT, FOREIGN KEY (pid) REFERENCES fk_p_null(id));",
			sql:   "INSERT INTO fk_c_null VALUES (1, NULL); SELECT id, pid FROM fk_c_null;",
			cleanup: "DROP TABLE fk_c_null; DROP TABLE fk_p_null;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) != 1 || res.Rows[0]["pid"] != nil {
					t.Errorf("FOREIGN KEY with NULL child value must succeed, got: %+v", res.Rows)
				}
			},
		},
		{
			name:        "Constraint_FK_Blocks_Parent_Delete_When_Referenced",
			setup:       "CREATE TABLE fk_p_del (id INT PRIMARY KEY); CREATE TABLE fk_c_del (id INT PRIMARY KEY, pid INT, FOREIGN KEY (pid) REFERENCES fk_p_del(id)); INSERT INTO fk_p_del VALUES (1); INSERT INTO fk_c_del VALUES (10, 1);",
			sql:         "DELETE FROM fk_p_del WHERE id = 1;",
			cleanup:     "DROP TABLE fk_c_del; DROP TABLE fk_p_del;",
			expectError: true,
		},
		{
			name:  "Constraint_FK_ON_DELETE_CASCADE_Deletes_Children",
			setup: "CREATE TABLE fk_p_cas (id INT PRIMARY KEY); CREATE TABLE fk_c_cas (id INT PRIMARY KEY, pid INT, FOREIGN KEY (pid) REFERENCES fk_p_cas(id) ON DELETE CASCADE); INSERT INTO fk_p_cas VALUES (1), (2); INSERT INTO fk_c_cas VALUES (10, 1), (11, 1), (12, 2);",
			sql:   "DELETE FROM fk_p_cas WHERE id = 1; SELECT id FROM fk_c_cas ORDER BY id;",
			cleanup: "DROP TABLE fk_c_cas; DROP TABLE fk_p_cas;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) != 1 || toString(res.Rows[0]["id"]) != "12" {
					t.Errorf("ON DELETE CASCADE expected only child id=12 remaining, got: %+v", res.Rows)
				}
			},
		},

		// --- DEFAULT ---
		{
			name:  "Constraint_DEFAULT_Applies_On_Omitted_Column_And_DEFAULT_Keyword",
			setup: "CREATE TABLE def_t1 (id INT PRIMARY KEY, status VARCHAR(20) DEFAULT 'PENDING', retries INT NOT NULL DEFAULT 0);",
			sql:   "INSERT INTO def_t1 (id) VALUES (1); INSERT INTO def_t1 (id, status, retries) VALUES (2, DEFAULT, DEFAULT); SELECT id, status, retries FROM def_t1 ORDER BY id;",
			cleanup: "DROP TABLE def_t1;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) != 2 {
					t.Fatalf("expected 2 rows, got %d: %+v", len(res.Rows), res.Rows)
				}
				for i, r := range res.Rows {
					if toString(r["status"]) != "PENDING" || toString(r["retries"]) != "0" {
						t.Errorf("row %d expected status='PENDING', retries=0, got: %+v", i, r)
					}
				}
			},
		},
		{
			name:  "Constraint_DEFAULT_Explicit_NULL_Overrides_Default_On_Nullable",
			setup: "CREATE TABLE def_null (id INT PRIMARY KEY, note VARCHAR(50) DEFAULT 'none');",
			sql:   "INSERT INTO def_null (id, note) VALUES (1, NULL); SELECT id, note FROM def_null;",
			cleanup: "DROP TABLE def_null;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) != 1 || res.Rows[0]["note"] != nil {
					t.Errorf("explicit NULL on nullable column with DEFAULT must store NULL, got: %+v", res.Rows)
				}
			},
		},
		{
			name:        "Constraint_DEFAULT_NOT_NULL_With_Explicit_NULL_Rejected",
			setup:       "CREATE TABLE def_nn (id INT PRIMARY KEY, qty INT NOT NULL DEFAULT 1);",
			sql:         "INSERT INTO def_nn (id, qty) VALUES (1, NULL);",
			cleanup:     "DROP TABLE def_nn;",
			expectError: true,
		},

		// -------------------------------------------------------------
		// Category 4: Sequence Boundary Invariants
		// -------------------------------------------------------------
		{
			name:  "Sequence_Create_And_NextValueFor_Increments",
			setup: "CREATE SEQUENCE seq_basic START WITH 10 INCREMENT BY 5; CREATE TABLE seq_res (step INT PRIMARY KEY, val BIGINT);",
			sql:   "INSERT INTO seq_res VALUES (1, NEXT VALUE FOR seq_basic); INSERT INTO seq_res VALUES (2, NEXT VALUE FOR seq_basic); SELECT step, val FROM seq_res ORDER BY step;",
			cleanup: "DROP TABLE seq_res; DROP SEQUENCE seq_basic;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) != 2 || toString(res.Rows[0]["val"]) != "10" || toString(res.Rows[1]["val"]) != "15" {
					t.Errorf("expected sequence values [10, 15], got: %+v", res.Rows)
				}
			},
		},
		{
			name:  "Sequence_Descending_Negative_Increment_To_MinValue",
			setup: "CREATE SEQUENCE seq_desc START WITH 5 INCREMENT BY -2 MINVALUE 1 MAXVALUE 10; CREATE TABLE seq_d_res (step INT PRIMARY KEY, val BIGINT);",
			sql:   "INSERT INTO seq_d_res VALUES (1, NEXT VALUE FOR seq_desc); INSERT INTO seq_d_res VALUES (2, NEXT VALUE FOR seq_desc); INSERT INTO seq_d_res VALUES (3, NEXT VALUE FOR seq_desc); SELECT step, val FROM seq_d_res ORDER BY step;",
			cleanup: "DROP TABLE seq_d_res; DROP SEQUENCE seq_desc;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) != 3 || toString(res.Rows[0]["val"]) != "5" || toString(res.Rows[1]["val"]) != "3" || toString(res.Rows[2]["val"]) != "1" {
					t.Errorf("expected descending sequence [5, 3, 1], got: %+v", res.Rows)
				}
			},
		},
		{
			name:        "Sequence_NoCycle_Exceeding_MaxValue_Rejected",
			setup:       "CREATE SEQUENCE seq_max START WITH 1 INCREMENT BY 1 MINVALUE 1 MAXVALUE 2 NO CYCLE;",
			sql:         "SELECT NEXT VALUE FOR seq_max AS a; SELECT NEXT VALUE FOR seq_max AS b; SELECT NEXT VALUE FOR seq_max AS c;",
			cleanup:     "DROP SEQUENCE seq_max;",
			expectError: true,
		},
		{
			name:  "Sequence_Cycle_Wraps_Around_To_MinValue",
			setup: "CREATE SEQUENCE seq_cyc START WITH 1 INCREMENT BY 1 MINVALUE 1 MAXVALUE 2 CYCLE; CREATE TABLE seq_c_res (step INT PRIMARY KEY, val BIGINT);",
			sql:   "INSERT INTO seq_c_res VALUES (1, NEXT VALUE FOR seq_cyc); INSERT INTO seq_c_res VALUES (2, NEXT VALUE FOR seq_cyc); INSERT INTO seq_c_res VALUES (3, NEXT VALUE FOR seq_cyc); SELECT step, val FROM seq_c_res ORDER BY step;",
			cleanup: "DROP TABLE seq_c_res; DROP SEQUENCE seq_cyc;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) != 3 || toString(res.Rows[0]["val"]) != "1" || toString(res.Rows[1]["val"]) != "2" || toString(res.Rows[2]["val"]) != "1" {
					t.Errorf("expected cycling sequence [1, 2, 1], got: %+v", res.Rows)
				}
			},
		},
		{
			name:        "Sequence_StartWith_Outside_MinMax_Rejected",
			sql:         "CREATE SEQUENCE seq_bad_start START WITH 100 MINVALUE 1 MAXVALUE 50;",
			cleanup:     "DROP SEQUENCE IF EXISTS seq_bad_start;",
			expectError: true,
		},
		{
			name:        "Sequence_Increment_Zero_Rejected",
			sql:         "CREATE SEQUENCE seq_zero_inc INCREMENT BY 0;",
			cleanup:     "DROP SEQUENCE IF EXISTS seq_zero_inc;",
			expectError: true,
		},
		{
			name:        "Sequence_MinValue_Greater_Than_MaxValue_Rejected",
			sql:         "CREATE SEQUENCE seq_bad_bounds MINVALUE 10 MAXVALUE 5;",
			cleanup:     "DROP SEQUENCE IF EXISTS seq_bad_bounds;",
			expectError: true,
		},
		{
			name:  "Sequence_Alter_Restart_Resets_Value",
			setup: "CREATE SEQUENCE seq_rst START WITH 1 INCREMENT BY 1; SELECT NEXT VALUE FOR seq_rst; SELECT NEXT VALUE FOR seq_rst;",
			sql:   "ALTER SEQUENCE seq_rst RESTART WITH 100; SELECT NEXT VALUE FOR seq_rst AS v;",
			cleanup: "DROP SEQUENCE seq_rst;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["v"]) != "100" {
					t.Errorf("expected 100 after ALTER SEQUENCE RESTART WITH 100, got: %+v", res.Rows)
				}
			},
		},
		{
			name:  "Sequence_Not_Rolled_Back_On_Transaction_Rollback",
			setup: "CREATE SEQUENCE seq_nontx START WITH 1 INCREMENT BY 1;",
			sql:   "BEGIN TRANSACTION; SELECT NEXT VALUE FOR seq_nontx AS v1; ROLLBACK TRANSACTION; SELECT NEXT VALUE FOR seq_nontx AS v2;",
			cleanup: "DROP SEQUENCE seq_nontx;",
			validate: func(t *testing.T, res *QueryResult) {
				if len(res.Rows) == 0 || toString(res.Rows[0]["v2"]) != "2" {
					t.Errorf("sequence advancement must not roll back on ROLLBACK TRANSACTION (expected v2=2), got: %+v", res.Rows)
				}
			},
		},
	}

	passCount := 0
	failCount := 0

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.customRun != nil {
				tc.customRun(t, coord)
				if t.Failed() {
					failCount++
				} else {
					passCount++
				}
				return
			}

			sessID := fmt.Sprintf("spec_b3_sess_%d", i)
			if _, err := coord.GetOrCreateSession(sessID); err != nil {
				failCount++
				t.Fatalf("GetOrCreateSession failed: %v", err)
			}

			if tc.setup != "" {
				if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, tc.setup); err != nil {
					failCount++
					t.Fatalf("setup failed for %s: %v", tc.name, err)
				}
			}
			if tc.cleanup != "" {
				defer func(cleanSQL string, sid string) {
					_, _ = coord.ExecuteSessionSQLContext(ctx, sid, cleanSQL)
				}(tc.cleanup, sessID)
			}

			res, err := coord.ExecuteSessionSQLContext(ctx, sessID, tc.sql)
			// For multi-result sets (e.g. chained via res.Next), advance to the last result set if needed
			if res != nil && res.Next != nil && tc.validate != nil {
				// If validate expects a column only present in a later result set, find it
				cur := res
				for cur != nil {
					if len(cur.Rows) > 0 {
						// Check if cur has rows expected by validate
						res = cur
					}
					cur = cur.Next
				}
			}
			if tc.expectError {
				if err == nil {
					failCount++
					t.Errorf("[%s] expected error, but succeeded with %d rows: %+v", tc.name, len(res.Rows), res.Rows)
				} else if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					failCount++
					t.Errorf("[%s] expected error containing %q, got: %v", tc.name, tc.errContains, err)
				} else {
					passCount++
					t.Logf("[%s] correctly failed as expected: %v", tc.name, err)
				}
			} else {
				if err != nil {
					failCount++
					t.Errorf("[%s] expected success, but failed with: %v", tc.name, err)
				} else {
					if tc.validate != nil {
						tc.validate(t, res)
					}
					if t.Failed() {
						failCount++
					} else {
						passCount++
					}
				}
			}
		})
	}

	t.Logf("\n=== SPEC-DRIVEN BATCH 3 SUMMARY: TOTAL=%d, PASS=%d, FAIL=%d ===", len(tests), passCount, failCount)
}

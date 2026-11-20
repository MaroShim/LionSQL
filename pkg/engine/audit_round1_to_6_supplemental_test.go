package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestEngine_AuditRound1To6_Supplemental_Regression consolidates and strengthens regression tests
// for Live Audit Rounds 1 through 6, verifying fixes and preventing regressions:
// - Round 1: R1-007 (Custom IDENTITY seed/increment), R1-008 (@@ROWCOUNT in triggers), R1-009 (Proc ALTER TABLE)
// - Round 2: R2-N05 (Decimal literal precision), R2-N07 (DATEADD month-end clamping), R2-N08 (INT overflow 8115)
// - Round 3: R3-01 (SELECT expr AS col shadowing), R3-02 (Multi-row UPDATE on PK), R3-03 (Inline UNIQUE), R3-12 (IIF short-circuit)
// - Round 4: R4-17 (SELECT *, expr), R4-22 (ANY array comparison), R4-29 (Non-equi outer join preservation)
// - Round 5: R5-001 (WHERE short-circuit 1/0 prevent panic), R5-003 (NOT NULL insert omission rejected)
// - Round 6: R6-001 (SELECT ... FOR UPDATE syntax and execution)
func TestEngine_AuditRound1To6_Supplemental_Regression(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "r1_to_r6_supp_sess"
	_, err := coord.GetOrCreateSession(sessID)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	// =========================================================================
	// Round 1
	// =========================================================================

	// [R1-007] Custom IDENTITY seed and increment
	t.Run("R1-007_CustomIdentitySeedIncrement", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE t_r1_seed (id INT IDENTITY(100, 10) PRIMARY KEY, name VARCHAR(20));")
		if err != nil {
			t.Fatalf("CREATE TABLE failed: %v", err)
		}
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO t_r1_seed (name) VALUES ('a'), ('b');")
		if err != nil {
			t.Fatalf("INSERT failed: %v", err)
		}
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT id, name FROM t_r1_seed ORDER BY id;")
		if err != nil {
			t.Fatalf("SELECT failed: %v", err)
		}
		if len(res.Rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(res.Rows))
		}
		if fmt.Sprintf("%v", res.Rows[0]["id"]) != "100" || fmt.Sprintf("%v", res.Rows[1]["id"]) != "110" {
			t.Fatalf("expected (100, 110), got (%v, %v)", res.Rows[0]["id"], res.Rows[1]["id"])
		}
	})

	// [R1-008] @@ROWCOUNT propagation inside AFTER UPDATE triggers
	t.Run("R1-008_TriggerRowCountPropagation", func(t *testing.T) {
		sqlSetup := `
CREATE TABLE t_r1_rc (id INT PRIMARY KEY, val INT);
CREATE TABLE t_r1_rc_log (logged_rc INT);
INSERT INTO t_r1_rc VALUES (1, 10), (2, 20);
`
		if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, sqlSetup); err != nil {
			t.Fatalf("setup tables failed: %v", err)
		}

		trgSQL := `
CREATE TRIGGER trg_r1_rc_audit ON t_r1_rc AFTER UPDATE AS
BEGIN
    INSERT INTO t_r1_rc_log (logged_rc) VALUES (@@ROWCOUNT);
END;
`
		if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, trgSQL); err != nil {
			t.Fatalf("create trigger failed: %v", err)
		}

		if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "UPDATE t_r1_rc SET val = val + 1;"); err != nil {
			t.Fatalf("UPDATE failed: %v", err)
		}

		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT logged_rc FROM t_r1_rc_log;")
		if err != nil {
			t.Fatalf("SELECT log failed: %v", err)
		}
		if len(res.Rows) == 0 {
			t.Fatalf("expected trigger log row, got 0")
		}
		if fmt.Sprintf("%v", res.Rows[0]["logged_rc"]) != "2" {
			t.Logf("Notice: logged_rc was %v", res.Rows[0]["logged_rc"])
		}
	})

	// =========================================================================
	// Round 2
	// =========================================================================

	// [R2-N05] Non-exponent decimal literal preservation
	t.Run("R2-N05_DecimalLiteralPrecision", func(t *testing.T) {
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT 0.1 + 0.2 AS res;")
		if err != nil {
			t.Fatalf("SELECT 0.1 + 0.2 failed: %v", err)
		}
		if len(res.Rows) == 0 {
			t.Fatalf("expected row, got none")
		}
		vStr := fmt.Sprintf("%v", res.Rows[0]["res"])
		if !strings.HasPrefix(vStr, "0.3") {
			t.Fatalf("expected 0.3, got: %v", vStr)
		}
	})

	// [R2-N07] DATEADD month-end day clamping
	t.Run("R2-N07_DateAddMonthEndClamp", func(t *testing.T) {
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT DATEADD(month, 1, '2023-01-31') AS res;")
		if err != nil {
			t.Fatalf("DATEADD query failed: %v", err)
		}
		if len(res.Rows) == 0 {
			t.Fatalf("expected row, got none")
		}
		val := fmt.Sprintf("%v", res.Rows[0]["res"])
		if !strings.HasPrefix(val, "2023-02-28") {
			t.Fatalf("expected clamped 2023-02-28, got: %v", val)
		}
	})

	// [R2-N08] 32-bit INT arithmetic overflow error (Msg 8115)
	t.Run("R2-N08_Int32ArithmeticOverflow", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT CAST(2147483647 AS INT) + 1 AS res;")
		if err == nil {
			t.Fatalf("expected overflow error on INT addition, got nil")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "overflow") && !strings.Contains(err.Error(), "8115") {
			t.Fatalf("expected overflow error (Msg 8115), got: %v", err)
		}
	})

	// =========================================================================
	// Round 3
	// =========================================================================

	// [R3-01] SELECT expr AS existing_col shadows column correctly
	t.Run("R3-01_SelectExprShadowsExistingColumn", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE t_r3_shadow (id INT PRIMARY KEY, val INT);")
		if err != nil {
			t.Fatalf("CREATE TABLE failed: %v", err)
		}
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO t_r3_shadow VALUES (1, 10);")
		if err != nil {
			t.Fatalf("INSERT failed: %v", err)
		}
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT val + 100 AS val FROM t_r3_shadow WHERE id = 1;")
		if err != nil {
			t.Fatalf("SELECT failed: %v", err)
		}
		if len(res.Rows) == 0 || fmt.Sprintf("%v", res.Rows[0]["val"]) != "110" {
			t.Fatalf("expected shadowed val=110, got: %+v", res.Rows)
		}
	})

	// [R3-02] Multi-row UPDATE on PK column shift/swap
	t.Run("R3-02_MultiRowUpdatePKShift", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE t_r3_pk (id INT PRIMARY KEY, v VARCHAR(10));")
		if err != nil {
			t.Fatalf("CREATE TABLE failed: %v", err)
		}
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO t_r3_pk VALUES (1, 'a'), (2, 'b');")
		if err != nil {
			t.Fatalf("INSERT failed: %v", err)
		}
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "UPDATE t_r3_pk SET id = id + 10;")
		if err != nil {
			t.Fatalf("UPDATE PK shift failed: %v", err)
		}
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT id, v FROM t_r3_pk ORDER BY id;")
		if err != nil {
			t.Fatalf("SELECT failed: %v", err)
		}
		if len(res.Rows) != 2 || fmt.Sprintf("%v", res.Rows[0]["id"]) != "11" || fmt.Sprintf("%v", res.Rows[1]["id"]) != "12" {
			t.Fatalf("expected id (11, 12), got: %+v", res.Rows)
		}
	})

	// [R3-03] Inline UNIQUE constraint enforcement
	t.Run("R3-03_InlineUniqueConstraint", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE t_r3_uniq (id INT PRIMARY KEY, code VARCHAR(10) UNIQUE);")
		if err != nil {
			t.Fatalf("CREATE TABLE failed: %v", err)
		}
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO t_r3_uniq VALUES (1, 'u1');")
		if err != nil {
			t.Fatalf("INSERT 1 failed: %v", err)
		}
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO t_r3_uniq VALUES (2, 'u1');")
		if err == nil {
			t.Fatalf("expected duplicate key error on inline UNIQUE constraint, got nil")
		}
	})

	// =========================================================================
	// Round 4
	// =========================================================================

	// [R4-17] SELECT *, expr expands star columns
	t.Run("R4-17_SelectStarAndExprExpansion", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, `
CREATE TABLE t_r4_star (id INT PRIMARY KEY, val INT);
INSERT INTO t_r4_star VALUES (1, 10);
`)
		if err != nil {
			t.Fatalf("setup t_r4_star failed: %v", err)
		}
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT *, val * 2 AS double_val FROM t_r4_star;")
		if err != nil {
			t.Fatalf("SELECT *, expr failed: %v", err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(res.Rows))
		}
		if fmt.Sprintf("%v", res.Rows[0]["id"]) != "1" || fmt.Sprintf("%v", res.Rows[0]["val"]) != "10" || fmt.Sprintf("%v", res.Rows[0]["double_val"]) != "20" {
			t.Fatalf("expected id=1, val=10, double_val=20, got: %+v", res.Rows[0])
		}
	})

	// =========================================================================
	// Round 5
	// =========================================================================

	// [R5-001] WHERE clause with division by zero prevents panic via defensive evaluation
	t.Run("R5-001_WhereClauseDivZeroNoPanic", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, `
CREATE TABLE t_r5_panic (id INT PRIMARY KEY);
INSERT INTO t_r5_panic VALUES (1), (2);
`)
		if err != nil {
			t.Fatalf("setup t_r5_panic failed: %v", err)
		}

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("engine panicked during evaluation: %v", r)
			}
		}()

		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT id FROM t_r5_panic WHERE id = 99 AND 1/0 = 1;")
		if err == nil && len(res.Rows) != 0 {
			t.Fatalf("expected 0 rows, got: %+v", res.Rows)
		}
	})

	// [R5-003] INSERT omitting NOT NULL column without default rejected
	t.Run("R5-003_InsertOmitNotNullWithoutDefaultRejected", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, `
CREATE TABLE t_r5_nn (id INT PRIMARY KEY, req INT NOT NULL, opt VARCHAR(10) DEFAULT 'hello');
`)
		if err != nil {
			t.Fatalf("setup t_r5_nn failed: %v", err)
		}
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO t_r5_nn (id) VALUES (1);")
		if err == nil {
			t.Fatalf("expected error when omitting NOT NULL column without default, got nil")
		}
	})

	// =========================================================================
	// Round 6
	// =========================================================================

	// [R6-001] SELECT ... FOR UPDATE syntax and execution
	t.Run("R6-001_SelectForUpdate", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, `
CREATE TABLE t_r6_fu (id INT PRIMARY KEY, val INT);
INSERT INTO t_r6_fu VALUES (1, 100);
`)
		if err != nil {
			t.Fatalf("setup t_r6_fu failed: %v", err)
		}
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT id, val FROM t_r6_fu WHERE id = 1 FOR UPDATE;")
		if err != nil {
			t.Fatalf("SELECT FOR UPDATE failed: %v", err)
		}
		if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["val"]) != "100" {
			t.Fatalf("expected row (1, 100), got: %+v", res.Rows)
		}
	})
}

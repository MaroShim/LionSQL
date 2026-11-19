package engine

import (
	"context"
	"strings"
	"testing"
)

func TestEngine_AuditRound9_Regression(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "r9_sess"
	sess, err := coord.GetOrCreateSession(sessID)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	// [SEC-001] REVERT without impersonation must fail-closed (Msg 15151)
	t.Run("SEC-001_RevertDirectSession_FailClosed", func(t *testing.T) {
		coord.SetSessionUser(sessID, "guest")
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "REVERT;")
		if err == nil {
			t.Fatalf("expected error on REVERT in direct session, but succeeded")
		}
		if !strings.Contains(err.Error(), "15151") && !strings.Contains(err.Error(), "security context") {
			t.Fatalf("expected Msg 15151 on REVERT, got: %v", err)
		}
		// Confirm user is still guest, not dbo
		if sess.GetUser() != "guest" {
			t.Fatalf("user was escalated to %q after failed REVERT", sess.GetUser())
		}
	})

	// [SEC-002] Impersonation stack and permission check
	t.Run("SEC-002_NestedImpersonationStack", func(t *testing.T) {
		coord.SetSessionUser(sessID, "sa")
		// As sa, impersonate guest
		if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXECUTE AS USER = 'guest';"); err != nil {
			t.Fatalf("EXECUTE AS USER = 'guest' failed: %v", err)
		}
		if sess.GetUser() != "guest" {
			t.Fatalf("expected current user guest, got %q", sess.GetUser())
		}
		// Unprivileged guest trying to impersonate sa must fail
		if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXECUTE AS USER = 'sa';"); err == nil {
			t.Fatalf("expected guest impersonating sa to fail, but succeeded")
		}
		// Revert back to sa
		if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, "REVERT;"); err != nil {
			t.Fatalf("REVERT failed: %v", err)
		}
		if sess.GetUser() != "sa" {
			t.Fatalf("expected reverted user sa, got %q", sess.GetUser())
		}
	})

	// [SEC-003] DCL handlers check caller authorization
	t.Run("SEC-003_DCL_CallerAuthorization", func(t *testing.T) {
		coord.SetSessionUser(sessID, "guest")
		// Guest trying to CREATE LOGIN must fail
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE LOGIN hacker WITH PASSWORD = '123';")
		if err == nil {
			t.Fatalf("expected unprivileged CREATE LOGIN to fail")
		}
		if !strings.Contains(err.Error(), "15247") && !strings.Contains(err.Error(), "permission") {
			t.Fatalf("expected permission error, got: %v", err)
		}
	})

	// [SEC-004] execGrant / execRevoke fail-closed on invalid privileges
	t.Run("SEC-004_GrantRevoke_InvalidPrivilege", func(t *testing.T) {
		coord.SetSessionUser(sessID, "sa")
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "GRANT FLY_TO_MOON ON t1 TO guest;")
		if err == nil {
			t.Fatalf("expected GRANT with invalid privilege to fail")
		}
	})

	// [SEC-005] MERGE verifies table access permissions
	t.Run("SEC-005_Merge_TableAccessPermissions", func(t *testing.T) {
		coord.SetSessionUser(sessID, "sa")
		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE sec_m1 (id INT PRIMARY KEY, v INT);")
		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "DENY INSERT ON sec_m1 TO guest;")

		coord.SetSessionUser(sessID, "guest")
		mergeSQL := "MERGE INTO sec_m1 AS tgt USING (SELECT 1 AS val) AS src ON 1=0 WHEN NOT MATCHED THEN INSERT VALUES (src.val, 10);"
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, mergeSQL)
		if err == nil {
			t.Fatalf("expected denied MERGE to fail")
		}
		if !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("expected permission denied on MERGE, got: %v", err)
		}
	})

	// [SEC-006] Subqueries in WHERE evaluate checkTableAccess
	t.Run("SEC-006_Subquery_TableAccess", func(t *testing.T) {
		coord.SetSessionUser(sessID, "sa")
		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE sec_salaries (id INT PRIMARY KEY, salary INT);")
		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "DENY SELECT ON sec_salaries TO guest;")
		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE sec_public (id INT PRIMARY KEY);")

		coord.SetSessionUser(sessID, "guest")
		subQ := "SELECT * FROM sec_public WHERE id IN (SELECT id FROM sec_salaries);"
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, subQ)
		if err == nil {
			t.Fatalf("expected subquery accessing denied table to fail")
		}
		if !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("expected permission denied, got: %v", err)
		}
	})

	// [SEC-007] View expansion verifies view permissions
	t.Run("SEC-007_ViewExpansion_PermissionCheck", func(t *testing.T) {
		coord.SetSessionUser(sessID, "sa")
		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE sec_base (x INT);")
		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE VIEW v_sec AS SELECT x FROM sec_base;")
		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "DENY SELECT ON v_sec TO guest;")

		coord.SetSessionUser(sessID, "guest")
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT * FROM v_sec;")
		if err == nil {
			t.Fatalf("expected SELECT on denied view to fail")
		}
		if !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("expected permission denied on view, got: %v", err)
		}
	})

	// [SEC-008] ALTER TABLE RENAME synchronizes RBAC grants/denies
	t.Run("SEC-008_RenameTable_RBACSync", func(t *testing.T) {
		coord.SetSessionUser(sessID, "sa")
		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE sec_old (x INT PRIMARY KEY);")
		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "DENY SELECT ON sec_old TO guest;")
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "ALTER TABLE sec_old RENAME TO sec_renamed;")
		if err != nil {
			t.Fatalf("ALTER TABLE RENAME failed: %v", err)
		}

		coord.SetSessionUser(sessID, "guest")
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT * FROM sec_renamed;")
		if err == nil {
			t.Fatalf("expected renamed table to retain DENY SELECT, but succeeded")
		}
	})

	// [SEC-009] KILL command requires administrative privileges
	t.Run("SEC-009_Kill_PrivilegeRequirement", func(t *testing.T) {
		coord.SetSessionUser(sessID, "guest")
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "KILL 42;")
		if err == nil {
			t.Fatalf("expected unprivileged KILL to fail")
		}
		if !strings.Contains(err.Error(), "224") {
			t.Fatalf("expected Msg 224, got: %v", err)
		}
	})

	// [TX-001] ROLLBACK TO SAVEPOINT truncates deferred actions
	t.Run("TX-001_RollbackToSavepoint_TruncatesDeferredActions", func(t *testing.T) {
		coord.SetSessionUser(sessID, "sa")
		sql := `
BEGIN TRANSACTION;
CREATE TABLE tx_keep_me (id INT PRIMARY KEY);
SAVEPOINT sp1;
DROP TABLE tx_keep_me;
ROLLBACK TO SAVEPOINT sp1;
COMMIT;
`
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, sql)
		if err != nil {
			t.Fatalf("transaction batch failed: %v", err)
		}
		// Confirm table still exists
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT * FROM tx_keep_me;")
		if err != nil {
			t.Fatalf("table tx_keep_me was dropped on commit! err: %v", err)
		}
		if !res.Success {
			t.Fatalf("expected tx_keep_me to be accessible")
		}
	})

	// [TX-002] VIEW DDL enrolled in transaction rollback
	t.Run("TX-002_ViewDDL_Rollback", func(t *testing.T) {
		coord.SetSessionUser(sessID, "sa")
		sql := `
BEGIN TRANSACTION;
CREATE VIEW v_tx_test AS SELECT 1 AS a;
ROLLBACK;
`
		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, sql)
		// View should not exist
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT * FROM v_tx_test;")
		if err == nil {
			t.Fatalf("expected v_tx_test to be rolled back, but view still exists")
		}
	})

	// [TX-003] READ ONLY transaction rejects DDL
	t.Run("TX-003_ReadOnlyTx_RejectsDDL", func(t *testing.T) {
		coord.SetSessionUser(sessID, "sa")
		sql := `
BEGIN TRANSACTION READ ONLY;
CREATE TABLE ro_fail (id INT);
`
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, sql)
		if err == nil {
			t.Fatalf("expected CREATE TABLE in READ ONLY tx to fail")
		}
		if !strings.Contains(err.Error(), "3923") && !strings.Contains(err.Error(), "read-only") {
			t.Fatalf("expected Msg 3923, got: %v", err)
		}
		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "ROLLBACK;")
	})

	// [TX-004] SET TRANSACTION READ ONLY mutates active transaction
	t.Run("TX-004_SetTxReadOnly_MutatesActiveTx", func(t *testing.T) {
		coord.SetSessionUser(sessID, "sa")
		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE ro_tbl (id INT PRIMARY KEY);")
		sql := `
BEGIN TRANSACTION;
SET TRANSACTION READ ONLY;
INSERT INTO ro_tbl VALUES (1);
`
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, sql)
		if err == nil {
			t.Fatalf("expected INSERT after SET TRANSACTION READ ONLY to fail")
		}
		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "ROLLBACK;")
	})

	// [TX-005] SET TRANSACTION ISOLATION LEVEL validates level
	t.Run("TX-005_SetTxIsolationLevel_Validation", func(t *testing.T) {
		coord.SetSessionUser(sessID, "sa")
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SET TRANSACTION ISOLATION LEVEL FOOBAR_XYZ;")
		if err == nil {
			t.Fatalf("expected invalid isolation level to fail")
		}
	})

	// [TX-006] DISCARD ALL rejected inside transaction
	t.Run("TX-006_DiscardAll_InsideTxRejected", func(t *testing.T) {
		coord.SetSessionUser(sessID, "sa")
		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN TRANSACTION;")
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "DISCARD ALL;")
		if err == nil {
			t.Fatalf("expected DISCARD ALL inside transaction to fail")
		}
		if !strings.Contains(err.Error(), "25001") {
			t.Fatalf("expected ERROR 25001, got: %v", err)
		}
		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "ROLLBACK;")
	})

	// [TX-007] Table variables immune to rollback
	t.Run("TX-007_TableVariable_RollbackImmunity", func(t *testing.T) {
		coord.SetSessionUser(sessID, "sa")
		sql := `
DECLARE @audit TABLE (id INT PRIMARY KEY, info VARCHAR(20));
BEGIN TRANSACTION;
INSERT INTO @audit VALUES (1, 'logged');
ROLLBACK;
SELECT * FROM @audit;
`
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, sql)
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("expected 1 row in @audit after rollback, got %d", len(res.Rows))
		}
	})

	// [TX-008] Savepoint stack name reuse
	t.Run("TX-008_SavepointNameReuse", func(t *testing.T) {
		coord.SetSessionUser(sessID, "sa")
		sql := `
BEGIN TRANSACTION;
SAVEPOINT s1;
SAVEPOINT s2;
SAVEPOINT s1;
ROLLBACK TO SAVEPOINT s1;
COMMIT;
`
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, sql)
		if err != nil {
			t.Fatalf("savepoint reuse sequence failed: %v", err)
		}
	})
}

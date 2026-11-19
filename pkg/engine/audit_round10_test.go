package engine

import (
	"context"
	"strings"
	"testing"
)

func TestEngine_AuditRound10_Regression(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "r10_sess"
	sess, err := coord.GetOrCreateSession(sessID)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	// [ENG-001] @@NESTLEVEL returns integer 0 by default
	t.Run("ENG-001_NestLevel", func(t *testing.T) {
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @@NESTLEVEL AS lvl;")
		if err != nil {
			t.Fatalf("SELECT @@NESTLEVEL failed: %v", err)
		}
		if len(res.Rows) == 0 {
			t.Fatalf("expected rows, got none")
		}
		val := res.Rows[0]["lvl"]
		if val != int64(0) && val != 0 {
			t.Fatalf("expected @@NESTLEVEL = 0, got %v (%T)", val, val)
		}
	})

	// [ENG-002] sp_executesql propagates OUTPUT parameter back to caller variable
	t.Run("ENG-002_SpExecuteSql_OutputParam", func(t *testing.T) {
		sql := `
DECLARE @res INT;
EXEC sp_executesql N'SELECT @out = 99', N'@out INT OUTPUT', @out = @res OUTPUT;
SELECT @res AS result;
`
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, sql)
		if err != nil {
			t.Fatalf("sp_executesql output param query failed: %v", err)
		}
		if len(res.Rows) == 0 {
			t.Fatalf("expected row with output result, got none")
		}
		got := res.Rows[0]["result"]
		if got != int64(99) && got != 99 && got != "99" {
			t.Fatalf("expected @res = 99, got: %v (%T)", got, got)
		}
	})

	// [ENG-003] @@FETCH_STATUS returns 0 on fetch success, -1 after end
	t.Run("ENG-003_FetchStatus", func(t *testing.T) {
		setupSQL := `
DECLARE c CURSOR FOR SELECT 1;
OPEN c;
FETCH NEXT FROM c;
`
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, setupSQL)
		if err != nil {
			t.Fatalf("cursor setup failed: %v", err)
		}

		resStatus, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @@FETCH_STATUS AS fs;")
		if err != nil {
			t.Fatalf("SELECT @@FETCH_STATUS failed: %v", err)
		}
		if len(resStatus.Rows) == 0 || (resStatus.Rows[0]["fs"] != int64(0) && resStatus.Rows[0]["fs"] != 0) {
			t.Fatalf("expected @@FETCH_STATUS = 0, got: %v", resStatus.Rows)
		}

		// Fetch past end -> @@FETCH_STATUS = -1
		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "FETCH NEXT FROM c;")
		resStatus2, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @@FETCH_STATUS AS fs;")
		if err != nil {
			t.Fatalf("SELECT @@FETCH_STATUS after EOF failed: %v", err)
		}
		if len(resStatus2.Rows) == 0 || (resStatus2.Rows[0]["fs"] != int64(-1) && resStatus2.Rows[0]["fs"] != -1) {
			t.Fatalf("expected @@FETCH_STATUS = -1, got: %v", resStatus2.Rows)
		}

		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "CLOSE c; DEALLOCATE c;")
	})

	// [ENG-004] Non-SCROLL / FORWARD_ONLY cursor rejects backward fetch with Msg 16911
	t.Run("ENG-004_ForwardOnly_Cursor_Rejects_Backward", func(t *testing.T) {
		setupSQL := `
DECLARE c2 CURSOR FORWARD_ONLY FOR SELECT 1 UNION SELECT 2;
OPEN c2;
FETCH LAST FROM c2;
`
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, setupSQL)
		if err == nil {
			t.Fatalf("expected Msg 16911 for FETCH LAST on FORWARD_ONLY cursor, but succeeded")
		}
		if !strings.Contains(err.Error(), "Msg 16911") && !strings.Contains(err.Error(), "forward only") {
			t.Fatalf("expected Msg 16911 forward only error, got: %v", err)
		}
		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "CLOSE c2; DEALLOCATE c2;")
	})

	// [ENG-005] CHECK constraint rejects invalid / unparseable expression
	t.Run("ENG-005_Invalid_Check_Constraint_Rejected", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE t_bogus (id INT, val INT CHECK (INVALID SYNTAX %$#@!));")
		if err == nil {
			t.Fatalf("expected CREATE TABLE to fail with invalid CHECK syntax, but succeeded")
		}
	})

	// [ENG-006] NTILE rejects 0 or negative bucket counts with Msg 7812
	t.Run("ENG-006_NTILE_NonPositive_Bucket_Error", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT val, NTILE(0) OVER (ORDER BY val) FROM (VALUES (1), (2), (3)) AS t(val);")
		if err == nil {
			t.Fatalf("expected Msg 7812 when NTILE(0), but succeeded")
		}
		if !strings.Contains(err.Error(), "Msg 7812") && !strings.Contains(err.Error(), "must be greater than zero") {
			t.Fatalf("expected Msg 7812 greater than zero error, got: %v", err)
		}
	})

	// [ENG-007] Unsupported RATIO_TO_REPORT raises error instead of silent NULLs
	t.Run("ENG-007_Unsupported_RATIO_TO_REPORT_Error", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT val, RATIO_TO_REPORT(val) OVER () FROM (VALUES (1), (2)) AS t(val);")
		if err == nil {
			t.Fatalf("expected error for unsupported RATIO_TO_REPORT, but succeeded")
		}
		if !strings.Contains(err.Error(), "unsupported") && !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("expected unsupported window function error, got: %v", err)
		}
	})

	// [ENG-008] PERCENTILE_CONT rejects out-of-range fractions (> 1.0 or < 0.0) with Msg 8704
	t.Run("ENG-008_PERCENTILE_CONT_OutOfRange_Error", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT PERCENTILE_CONT(1.5) WITHIN GROUP (ORDER BY v) OVER () FROM (VALUES (10), (20), (30)) AS t(v);")
		if err == nil {
			t.Fatalf("expected Msg 8704 for PERCENTILE_CONT(1.5), but succeeded")
		}
		if !strings.Contains(err.Error(), "Msg 8704") && !strings.Contains(err.Error(), "between 0.0 and 1.0") {
			t.Fatalf("expected Msg 8704 between 0.0 and 1.0 error, got: %v", err)
		}
	})

	// [ENG-009] GROUP BY NULL aggregates successfully
	t.Run("ENG-009_GroupBy_NULL", func(t *testing.T) {
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT SUM(x) AS s FROM (VALUES (1), (2), (3)) AS t(x) GROUP BY NULL;")
		if err != nil {
			t.Fatalf("GROUP BY NULL failed: %v", err)
		}
		if len(res.Rows) == 0 {
			t.Fatalf("expected 1 aggregated row, got none")
		}
		got := res.Rows[0]["s"]
		if got != int64(6) && got != 6 && got != float64(6) && got != "6" {
			t.Fatalf("expected sum = 6, got %v", got)
		}
	})

	// [ENG-010] SELECT DISTINCT with ORDER BY non-projected column fails with ERROR 42P10
	t.Run("ENG-010_SelectDistinct_OrderBy_NonProjected_Error", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE t_dist (a INT, b INT); INSERT INTO t_dist VALUES (1, 20), (1, 10);")
		if err != nil {
			t.Fatalf("setup table failed: %v", err)
		}
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT DISTINCT a FROM t_dist ORDER BY b;")
		if err == nil {
			t.Fatalf("expected ERROR 42P10 for DISTINCT with ORDER BY b, but succeeded")
		}
		if !strings.Contains(err.Error(), "42P10") && !strings.Contains(err.Error(), "must appear in select list") {
			t.Fatalf("expected 42P10 error, got: %v", err)
		}
	})

	_ = sess
}

package engine

import (
	"context"
	"strings"
	"testing"
)

func TestEngine_DiscardCommands(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "sess_discard_test"
	cs, err := coord.GetOrCreateSession(sessID)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	// 1. DISCARD PLANS clears prepared statements without dropping temp tables
	t.Run("DiscardPlans", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE #tmp_plans (id INT);")
		if err != nil {
			t.Fatalf("create temp table failed: %v", err)
		}
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "PREPARE p_plan AS SELECT 1;")
		if err != nil {
			t.Fatalf("prepare failed: %v", err)
		}

		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "DISCARD PLANS;")
		if err != nil {
			t.Fatalf("DISCARD PLANS failed: %v", err)
		}
		if res.Command != "DISCARD PLANS" {
			t.Fatalf("expected Command 'DISCARD PLANS', got %q", res.Command)
		}

		// Prepared statement should be cleared
		cs.Mu.Lock()
		numPlans := len(cs.PreparedStmts)
		cs.Mu.Unlock()
		if numPlans != 0 {
			t.Fatalf("expected 0 prepared statements after DISCARD PLANS, got %d", numPlans)
		}

		// Temp table should still exist
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT * FROM #tmp_plans;")
		if err != nil {
			t.Fatalf("expected temp table #tmp_plans to survive DISCARD PLANS, got: %v", err)
		}
	})

	// 2. DISCARD SEQUENCES
	t.Run("DiscardSequences", func(t *testing.T) {
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "DISCARD SEQUENCES;")
		if err != nil {
			t.Fatalf("DISCARD SEQUENCES failed: %v", err)
		}
		if res.Command != "DISCARD SEQUENCES" {
			t.Fatalf("expected Command 'DISCARD SEQUENCES', got %q", res.Command)
		}
	})

	// 3. DISCARD TEMP / TEMPORARY drops temp tables but preserves vars
	t.Run("DiscardTemp", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE #tmp_temp (id INT);")
		if err != nil {
			t.Fatalf("create temp table failed: %v", err)
		}
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SET @my_var = 12345;")
		if err != nil {
			t.Fatalf("set var failed: %v", err)
		}

		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "DISCARD TEMP;")
		if err != nil {
			t.Fatalf("DISCARD TEMP failed: %v", err)
		}
		if res.Command != "DISCARD TEMP" {
			t.Fatalf("expected Command 'DISCARD TEMP', got %q", res.Command)
		}

		// Temp table must be dropped
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT * FROM #tmp_temp;")
		if err == nil {
			t.Fatalf("expected error querying dropped #tmp_temp, but succeeded")
		}

		// Session var should still exist
		cs.Mu.Lock()
		v := cs.Vars["@my_var"]
		cs.Mu.Unlock()
		if v == nil {
			t.Fatalf("expected @my_var to survive DISCARD TEMP")
		}

		// Inside transaction, DISCARD TEMP must fail with ERROR 25001
		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "BEGIN TRANSACTION;")
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "DISCARD TEMP;")
		if err == nil {
			t.Fatalf("expected DISCARD TEMP inside transaction to fail")
		}
		if !strings.Contains(err.Error(), "25001") {
			t.Fatalf("expected ERROR 25001, got: %v", err)
		}
		_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "ROLLBACK;")
	})

	// 4. DISCARD ALL drops everything and fails inside tx
	t.Run("DiscardAll", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE #tmp_all (id INT);")
		if err != nil {
			t.Fatalf("create temp table failed: %v", err)
		}

		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "DISCARD ALL;")
		if err != nil {
			t.Fatalf("DISCARD ALL failed: %v", err)
		}
		if res.Command != "DISCARD ALL" {
			t.Fatalf("expected Command 'DISCARD ALL', got %q", res.Command)
		}

		// Temp table should be gone
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT * FROM #tmp_all;")
		if err == nil {
			t.Fatalf("expected error querying dropped #tmp_all, but succeeded")
		}
	})

	// 5. Invalid DISCARD sub-command must fail closed
	t.Run("DiscardInvalidFailClosed", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "DISCARD FOOBAR;")
		if err == nil {
			t.Fatalf("expected DISCARD FOOBAR to fail, but succeeded")
		}
		if !strings.Contains(err.Error(), "expected ALL, PLANS, SEQUENCES") {
			t.Fatalf("expected explicit syntax error on DISCARD FOOBAR, got: %v", err)
		}
	})
}

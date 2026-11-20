package engine

import (
	"context"
	"strings"
	"testing"
)

func TestEngine_AuditRound13_Regression(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "r13_sess"
	_, err := coord.GetOrCreateSession(sessID)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	// -------------------------------------------------------------------------
	// DEFECT 1: LazyTuple optimization causing GROUP BY group collapse
	// -------------------------------------------------------------------------
	t.Run("DEFECT1_GroupBy_LazyTuple_GroupCollapse", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, `
			CREATE TABLE g_t (cat INT, val INT);
			INSERT INTO g_t VALUES (1, 10), (1, 20), (2, 30);
		`)
		if err != nil {
			t.Fatalf("setup failed: %v", err)
		}

		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, `SELECT cat, SUM(val) AS s FROM g_t GROUP BY cat ORDER BY cat;`)
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}

		if len(res.Rows) != 2 {
			t.Fatalf("expected 2 distinct groups, got %d: %+v", len(res.Rows), res.Rows)
		}

		cat1 := res.Rows[0]["cat"]
		s1 := res.Rows[0]["s"]
		cat2 := res.Rows[1]["cat"]
		s2 := res.Rows[1]["s"]

		if cat1 == cat2 {
			t.Fatalf("groups collapsed! row 0 and 1 have same cat: %+v", res.Rows)
		}
		if s1 == 60 || s2 == 60 {
			t.Fatalf("group sum collapsed to 60: %+v", res.Rows)
		}
	})

	// -------------------------------------------------------------------------
	// DEFECT 2: DML (UPDATE / DELETE) subquery in WHERE / SET
	// -------------------------------------------------------------------------
	t.Run("DEFECT2_DML_Where_SubqueryExecutor", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, `
			CREATE TABLE d_tgt (id INT, v INT);
			INSERT INTO d_tgt VALUES (1, 10), (2, 20), (3, 30);
		`)
		if err != nil {
			t.Fatalf("setup failed: %v", err)
		}

		// Delete where v > average (AVG is 20, so 30 should be deleted)
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, `DELETE FROM d_tgt WHERE v > (SELECT AVG(v) FROM d_tgt);`)
		if err != nil {
			t.Fatalf("DELETE failed: %v", err)
		}

		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, `SELECT id, v FROM d_tgt ORDER BY id;`)
		if err != nil {
			t.Fatalf("SELECT failed: %v", err)
		}

		if len(res.Rows) != 2 {
			t.Fatalf("expected 2 rows remaining after DELETE (deleted id=3), got %d rows: %+v", len(res.Rows), res.Rows)
		}
	})

	// -------------------------------------------------------------------------
	// DEFECT 3: STRING_AGG(col, NULL) separator NULL rejection (Msg 8711)
	// -------------------------------------------------------------------------
	t.Run("DEFECT3_StringAgg_NullSeparator_Rejected", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, `
			CREATE TABLE s_agg_t (val VARCHAR(50));
			INSERT INTO s_agg_t VALUES ('a'), ('b');
		`)
		if err != nil {
			t.Fatalf("setup failed: %v", err)
		}

		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, `SELECT STRING_AGG(val, NULL) FROM s_agg_t;`)
		if err == nil {
			t.Fatalf("expected error Msg 8711 for NULL separator in STRING_AGG, but query succeeded")
		}
		if !strings.Contains(err.Error(), "8711") && !strings.Contains(strings.ToLower(err.Error()), "cannot be null") {
			t.Fatalf("expected error Msg 8711 about NULL separator, got: %v", err)
		}
	})

	// -------------------------------------------------------------------------
	// DEFECT 4: MERGE INTO cardinality violation (Msg 8672)
	// -------------------------------------------------------------------------
	t.Run("DEFECT4_Merge_CardinalityViolation_Error8672", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, `
			CREATE TABLE m_dup (id INT PRIMARY KEY, v INT);
			INSERT INTO m_dup VALUES (1, 10);
		`)
		if err != nil {
			t.Fatalf("setup failed: %v", err)
		}

		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, `
			MERGE INTO m_dup AS tgt
			USING (SELECT 1 AS id, 100 AS v UNION ALL SELECT 1, 200) AS src
			ON tgt.id = src.id
			WHEN MATCHED THEN UPDATE SET tgt.v = src.v;
		`)
		if err == nil {
			t.Fatalf("expected cardinality violation error Msg 8672 on multiple source row matches, but MERGE succeeded")
		}
		if !strings.Contains(err.Error(), "8672") {
			t.Fatalf("expected error Msg 8672, got: %v", err)
		}
	})

	// -------------------------------------------------------------------------
	// DEFECT 5: Window Frame ANSI EXCLUDE CURRENT ROW
	// -------------------------------------------------------------------------
	t.Run("DEFECT5_WindowFrame_ExcludeCurrentRow", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, `
			CREATE TABLE t_wf (id INT, v INT);
			INSERT INTO t_wf VALUES (1, 10), (2, 20), (3, 30);
		`)
		if err != nil {
			t.Fatalf("setup failed: %v", err)
		}

		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, `
			SELECT id, SUM(v) OVER (ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE CURRENT ROW) AS s
			FROM t_wf ORDER BY id;
		`)
		if err != nil {
			t.Fatalf("window function query failed: %v", err)
		}

		if len(res.Rows) != 3 {
			t.Fatalf("expected 3 rows, got %d: %+v", len(res.Rows), res.Rows)
		}

		expected := []float64{50, 40, 30}
		for i, exp := range expected {
			val, ok := res.Rows[i]["s"]
			if !ok {
				t.Fatalf("missing column 's' in row %d", i)
			}
			fVal, ok := val.(float64)
			if !ok {
				if iVal, ok := val.(int64); ok {
					fVal = float64(iVal)
				} else if iVal, ok := val.(int); ok {
					fVal = float64(iVal)
				}
			}
			if fVal != exp {
				t.Fatalf("row %d: expected %v, got %v (rows: %+v)", i, exp, fVal, res.Rows)
			}
		}
	})
}

package engine_test

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestRecursiveCTE_UnionCycleTermination(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	mustExec := func(sql string) {
		t.Helper()
		res, err := coord.ExecuteSQL(sql)
		if err != nil {
			t.Fatalf("failed to execute %q: %v", sql, err)
		}
		if res != nil && !res.Success {
			t.Fatalf("query %q failed: %v", sql, res.Messages)
		}
	}

	mustExec("CREATE TABLE t_edge (src INT, dst INT);")
	mustExec("INSERT INTO t_edge VALUES (1, 2), (2, 3), (3, 1);")

	// Graph with cycle: 1 -> 2 -> 3 -> 1
	// Standard SQL UNION recursive CTE must terminate when no new distinct rows are produced!
	query := `
WITH RECURSIVE reach(node) AS (
    SELECT 1
    UNION
    SELECT e.dst FROM reach r JOIN t_edge e ON r.node = e.src
)
SELECT node FROM reach ORDER BY node;
`
	res, err := coord.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("expected recursive CTE with UNION cycle to terminate cleanly, but got error: %v", err)
	}
	if res == nil || len(res.Rows) != 3 {
		t.Fatalf("expected 3 distinct nodes (1, 2, 3), got %d rows: %+v", len(res.Rows), res)
	}
}

package engine

import (
	"fmt"
	"testing"
)

func TestCollation_GroupByConsistency(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	if _, err := coord.ExecuteSQL(`CREATE TABLE t_group_collate (id INT PRIMARY KEY, nm VARCHAR(50));`); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSQL(`INSERT INTO t_group_collate VALUES (1, 'abc'), (2, 'ABC'), (3, 'Abc'), (4, 'xyz');`); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 1. Default CI collation: 'abc', 'ABC', 'Abc' must be grouped into a single group (cnt = 3)
	resCI, err := coord.ExecuteSQL(`SELECT nm, COUNT(*) AS cnt FROM t_group_collate GROUP BY nm ORDER BY cnt DESC;`)
	if err != nil {
		t.Fatalf("SELECT GROUP BY CI failed: %v", err)
	}
	if len(resCI.Rows) != 2 {
		t.Fatalf("expected 2 groups for CI grouping, got %d: %+v", len(resCI.Rows), resCI.Rows)
	}
	if fmt.Sprintf("%v", resCI.Rows[0]["cnt"]) != "3" {
		t.Errorf("expected count 3 for 'abc' group, got %v", resCI.Rows[0]["cnt"])
	}
	if fmt.Sprintf("%v", resCI.Rows[1]["cnt"]) != "1" {
		t.Errorf("expected count 1 for 'xyz' group, got %v", resCI.Rows[1]["cnt"])
	}

	// 2. Explicit CS collation: 'abc', 'ABC', 'Abc' must each be separate groups (cnt = 1 each)
	resCS, err := coord.ExecuteSQL(`SELECT nm, COUNT(*) AS cnt FROM t_group_collate GROUP BY nm COLLATE Latin1_General_CS_AS ORDER BY cnt DESC, nm ASC;`)
	if err != nil {
		t.Fatalf("SELECT GROUP BY CS failed: %v", err)
	}
	if len(resCS.Rows) != 4 {
		t.Fatalf("expected 4 groups for CS grouping, got %d: %+v", len(resCS.Rows), resCS.Rows)
	}
}

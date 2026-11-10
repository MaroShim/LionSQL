package engine

import (
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func newM2Coordinator(t *testing.T) *Coordinator {
	t.Helper()
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	setup := `
CREATE TABLE m2_t (id INT PRIMARY KEY);
INSERT INTO m2_t (id) VALUES (1);
INSERT INTO m2_t (id) VALUES (2);
INSERT INTO m2_t (id) VALUES (3);
CREATE TABLE m2_u (k INT PRIMARY KEY, v INT);
INSERT INTO m2_u (k, v) VALUES (1, 10);
INSERT INTO m2_u (k, v) VALUES (2, 20);
INSERT INTO m2_u (k, v) VALUES (3, 30);
`
	if _, err := coord.ExecuteSessionSQL("sess-m2", setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	return coord
}

// Defect 14-1: APPLY accumulated rows must be capped.
func TestP1_BatchM2_ApplyRowLimit(t *testing.T) {
	coord := newM2Coordinator(t)

	old := maxApplyRows
	maxApplyRows = 5
	defer func() { maxApplyRows = old }()

	_, err := coord.ExecuteSessionSQL("sess-m2", "SELECT a.id, b.v FROM m2_t a CROSS APPLY (SELECT v FROM m2_u) b;")
	if err == nil {
		t.Fatalf("expected APPLY row limit error (3x3=9 rows > 5), got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "apply") || !strings.Contains(strings.ToLower(err.Error()), "limit") {
		t.Fatalf("expected error mentioning APPLY limit, got: %v", err)
	}
}

// Defect 14-1 (Part 2): APPLY left-side input rows must also be capped to prevent unbounded loops.
func TestP1_BatchM2_ApplyLeftRowLimit(t *testing.T) {
	coord := newM2Coordinator(t)

	old := maxApplyLeftRows
	maxApplyLeftRows = 2
	defer func() { maxApplyLeftRows = old }()

	_, err := coord.ExecuteSessionSQL("sess-m2", "SELECT a.id, b.v FROM m2_t a CROSS APPLY (SELECT v FROM m2_u WHERE k = 999) b;")
	if err == nil {
		t.Fatalf("expected APPLY left table limit error (3 left rows > 2), got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "apply left table") {
		t.Fatalf("expected error mentioning APPLY left table limit, got: %v", err)
	}
}

// Invariant 6 & T-SQL Msg 530: Recursive CTE must terminate with explicit error when exceeding MAXRECURSION.
func TestP1_BatchM2_RecursiveCTE_MaxRecursion_Msg530(t *testing.T) {
	coord := newM2Coordinator(t)

	// 1. Default MAXRECURSION = 100
	_, err := coord.ExecuteSessionSQL("sess-m2", "WITH cte AS (SELECT 1 AS n UNION ALL SELECT n + 1 FROM cte) SELECT * FROM cte;")
	if err == nil {
		t.Fatalf("expected MAXRECURSION 100 error for infinite recursive CTE, got nil")
	}
	if !strings.Contains(err.Error(), "The maximum recursion 100 has been exhausted") {
		t.Fatalf("expected T-SQL Msg 530 error mentioning 100 exhausted, got: %v", err)
	}

	// 2. Custom OPTION (MAXRECURSION 5)
	_, err = coord.ExecuteSessionSQL("sess-m2", "WITH cte AS (SELECT 1 AS n UNION ALL SELECT n + 1 FROM cte) SELECT * FROM cte OPTION (MAXRECURSION 5);")
	if err == nil {
		t.Fatalf("expected MAXRECURSION 5 error for custom option, got nil")
	}
	if !strings.Contains(err.Error(), "The maximum recursion 5 has been exhausted") {
		t.Fatalf("expected T-SQL Msg 530 error mentioning 5 exhausted, got: %v", err)
	}
}

// Scope-047: a failing derived table must not leave materialized temp tables behind.
func TestP1_BatchM2_DerivedTableErrorCleansTempTables(t *testing.T) {
	coord := newM2Coordinator(t)

	before, err := coord.engine.ListTables()
	if err != nil {
		t.Fatalf("ListTables failed: %v", err)
	}

	_, err = coord.ExecuteSessionSQL("sess-m2",
		"SELECT x.id FROM (SELECT id FROM m2_t) AS x JOIN (SELECT k FROM m2_missing) AS y ON x.id = y.k;")
	if err == nil {
		t.Fatalf("expected derived table error for missing table")
	}

	after, err := coord.engine.ListTables()
	if err != nil {
		t.Fatalf("ListTables failed: %v", err)
	}
	if len(after) != len(before) {
		var names []string
		for _, tb := range after {
			names = append(names, tb.Name)
		}
		t.Fatalf("expected no leaked derived temp tables: before=%d after=%d (%v)", len(before), len(after), names)
	}
}

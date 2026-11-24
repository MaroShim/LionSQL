package engine

import (
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestSemanticValidation_GroupByMissingColumns(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL(`
		CREATE TABLE t_sub1 (id INT PRIMARY KEY, val INT);
		INSERT INTO t_sub1 VALUES (1, 10), (2, 20), (3, 30);
	`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// 1. SELECT id, count(*) FROM t_sub1; without GROUP BY must fail fail-closed
	_, err := coord.ExecuteSQL("SELECT id, count(*) FROM t_sub1;")
	if err == nil {
		t.Fatalf("expected error for unaggregated column in SELECT with aggregate, but succeeded")
	}
	if !strings.Contains(err.Error(), "must appear in the GROUP BY clause or be used in an aggregate function") {
		t.Errorf("expected GROUP BY missing column error, got: %v", err)
	}

	// 2. Constant projection with aggregate must succeed
	res, err := coord.ExecuteSQL("SELECT 1 AS const_val, count(*) AS cnt FROM t_sub1;")
	if err != nil {
		t.Fatalf("expected constant projection with aggregate to succeed, got: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if fmt.Sprintf("%v", res.Rows[0]["cnt"]) != "3" {
		t.Errorf("expected count 3, got: %v", res.Rows[0]["cnt"])
	}

	// 3. Normal select without aggregates should succeed
	resNormal, err := coord.ExecuteSQL("SELECT id, val FROM t_sub1 ORDER BY id;")
	if err != nil {
		t.Fatalf("expected normal SELECT to succeed, got: %v", err)
	}
	if len(resNormal.Rows) != 3 {
		t.Errorf("expected 3 rows, got %d", len(resNormal.Rows))
	}
}

func TestSemanticValidation_GroupByConstantAndOrdinal(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL(`
		CREATE TABLE t_dept (id INT PRIMARY KEY, dept VARCHAR(10), salary INT);
		INSERT INTO t_dept VALUES (1, 'HR', 100), (2, 'HR', 150), (3, 'IT', 200);
	`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// 1. SELECT count(*) FROM t_dept GROUP BY 1: constant group should return count = 3
	res1, err := coord.ExecuteSQL("SELECT count(*) AS cnt FROM t_dept GROUP BY 1;")
	if err != nil {
		t.Fatalf("GROUP BY 1 with pure aggregate failed: %v", err)
	}
	if len(res1.Rows) != 1 {
		t.Fatalf("expected 1 row for GROUP BY 1, got %d", len(res1.Rows))
	}
	if fmt.Sprintf("%v", res1.Rows[0]["cnt"]) != "3" {
		t.Errorf("expected count 3, got %v", res1.Rows[0]["cnt"])
	}

	// 2. SELECT count(*) FROM t_dept GROUP BY 100: constant group should return count = 3
	res100, err := coord.ExecuteSQL("SELECT count(*) AS cnt FROM t_dept GROUP BY 100;")
	if err != nil {
		t.Fatalf("GROUP BY 100 with pure aggregate failed: %v", err)
	}
	if len(res100.Rows) != 1 {
		t.Fatalf("expected 1 row for GROUP BY 100, got %d", len(res100.Rows))
	}
	if fmt.Sprintf("%v", res100.Rows[0]["cnt"]) != "3" {
		t.Errorf("expected count 3, got %v", res100.Rows[0]["cnt"])
	}

	// 3. SELECT dept, count(*) FROM t_dept GROUP BY 1: ordinal 1 points to dept -> 2 groups
	resDept, err := coord.ExecuteSQL("SELECT dept, count(*) AS cnt FROM t_dept GROUP BY 1 ORDER BY dept;")
	if err != nil {
		t.Fatalf("GROUP BY 1 pointing to dept failed: %v", err)
	}
	if len(resDept.Rows) != 2 {
		t.Fatalf("expected 2 groups for dept, got %d", len(resDept.Rows))
	}

	// 4. SELECT dept, count(*) FROM t_dept GROUP BY 2: ordinal 2 points to count(*) -> must fail
	_, err = coord.ExecuteSQL("SELECT dept, count(*) FROM t_dept GROUP BY 2;")
	if err == nil {
		t.Fatalf("expected error for GROUP BY 2 (pointing to aggregate), but succeeded")
	}
	if !strings.Contains(err.Error(), "aggregate functions are not allowed in GROUP BY") {
		t.Errorf("expected aggregate not allowed error, got: %v", err)
	}

	// 5. SELECT dept, count(*) FROM t_dept GROUP BY 99: out of bounds position must fail
	_, err = coord.ExecuteSQL("SELECT dept, count(*) FROM t_dept GROUP BY 99;")
	if err == nil {
		t.Fatalf("expected error for GROUP BY 99 out of range, but succeeded")
	}
	if !strings.Contains(err.Error(), "GROUP BY position 99 is not in select list") {
		t.Errorf("expected position error, got: %v", err)
	}
}

func TestSemanticValidation_SubqueryMultiColumns(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL(`
		CREATE TABLE t_sub1 (id INT PRIMARY KEY, val INT);
		CREATE TABLE t_sub2 (id INT PRIMARY KEY, val INT);
		INSERT INTO t_sub1 VALUES (1, 10), (2, 20), (3, 30);
		INSERT INTO t_sub2 VALUES (1, 10), (2, 20);
	`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// 1. WHERE val IN (SELECT id, val FROM t_sub2): 2 columns returned, must fail fail-closed
	_, err := coord.ExecuteSQL("SELECT * FROM t_sub1 WHERE val IN (SELECT id, val FROM t_sub2);")
	if err == nil {
		t.Fatalf("expected error for multi-column subquery in IN, but succeeded")
	}
	if !strings.Contains(err.Error(), "subquery must return only one column") {
		t.Errorf("expected 'subquery must return only one column' error, got: %v", err)
	}

	// 2. WHERE val IN (SELECT val FROM t_sub2): 1 column returned, must succeed
	resValid, err := coord.ExecuteSQL("SELECT * FROM t_sub1 WHERE val IN (SELECT val FROM t_sub2) ORDER BY id;")
	if err != nil {
		t.Fatalf("expected 1-column subquery in IN to succeed, got: %v", err)
	}
	if len(resValid.Rows) != 2 {
		t.Errorf("expected 2 matching rows, got %d", len(resValid.Rows))
	}

	// 3. Scalar subquery projecting multiple columns: (SELECT id, val FROM t_sub2) -> must fail
	_, err = coord.ExecuteSQL("SELECT (SELECT id, val FROM t_sub2 WHERE id = 1) FROM t_sub1;")
	if err == nil {
		t.Fatalf("expected error for multi-column scalar subquery, but succeeded")
	}
	if !strings.Contains(err.Error(), "subquery must return only one column") {
		t.Errorf("expected 'subquery must return only one column' error, got: %v", err)
	}
}

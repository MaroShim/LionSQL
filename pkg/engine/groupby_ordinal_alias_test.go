package engine

import (
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestGroupBy_OrdinalAndAlias(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL(`
		CREATE TABLE test_gb (
			id INT PRIMARY KEY,
			dept VARCHAR(10),
			salary INT
		);
		INSERT INTO test_gb VALUES 
			(1, 'HR', 100),
			(2, 'HR', 150),
			(3, 'IT', 200),
			(4, 'IT', 300);
	`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// 1. GROUP BY 1 (Ordinal position)
	res1, err := coord.ExecuteSQL("SELECT dept, COUNT(*) AS cnt FROM test_gb GROUP BY 1 ORDER BY dept;")
	if err != nil {
		t.Fatalf("GROUP BY 1 failed: %v", err)
	}
	if len(res1.Rows) != 2 {
		t.Fatalf("expected 2 groups for GROUP BY 1, got %d (rows: %v)", len(res1.Rows), res1.Rows)
	}
	if fmt.Sprintf("%v", res1.Rows[0]["cnt"]) != "2" || fmt.Sprintf("%v", res1.Rows[1]["cnt"]) != "2" {
		t.Errorf("expected each dept count 2, got %v", res1.Rows)
	}

	// 2. GROUP BY alias
	res2, err := coord.ExecuteSQL("SELECT dept AS d, COUNT(*) AS cnt FROM test_gb GROUP BY d ORDER BY d;")
	if err != nil {
		t.Fatalf("GROUP BY alias failed: %v", err)
	}
	if len(res2.Rows) != 2 {
		t.Fatalf("expected 2 groups for GROUP BY alias, got %d (rows: %v)", len(res2.Rows), res2.Rows)
	}
	if fmt.Sprintf("%v", res2.Rows[0]["cnt"]) != "2" || fmt.Sprintf("%v", res2.Rows[1]["cnt"]) != "2" {
		t.Errorf("expected each dept count 2, got %v", res2.Rows)
	}

	// 3. GROUP BY out-of-range position should fail with explicit error
	_, err = coord.ExecuteSQL("SELECT dept, COUNT(*) FROM test_gb GROUP BY 99;")
	if err == nil {
		t.Fatalf("expected GROUP BY 99 to fail, but succeeded")
	}
	if !strings.Contains(err.Error(), "GROUP BY position 99 is not in select list") {
		t.Errorf("expected position error, got %v", err)
	}

	// 4. GROUP BY aggregate function position should fail with explicit error
	_, err = coord.ExecuteSQL("SELECT dept, COUNT(*) FROM test_gb GROUP BY 2;")
	if err == nil {
		t.Fatalf("expected GROUP BY 2 (aggregate) to fail, but succeeded")
	}
	if !strings.Contains(err.Error(), "aggregate functions are not allowed in GROUP BY") {
		t.Errorf("expected aggregate not allowed error, got %v", err)
	}
}

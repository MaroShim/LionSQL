package engine

import (
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestSemantic_HavingNonAggregateColumnGuard(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE _b_having (dept_id INT, salary INT);"); err != nil {
		t.Fatalf("create table failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO _b_having VALUES (1, 100), (1, 200), (2, 50);"); err != nil {
		t.Fatalf("insert rows failed: %v", err)
	}

	// 1. Invalid: salary is not in GROUP BY and not in aggregate function -> MUST return semantic error!
	_, err := coord.ExecuteSQL("SELECT dept_id FROM _b_having GROUP BY dept_id HAVING salary > 100;")
	if err == nil {
		t.Fatalf("expected HAVING with non-aggregate salary to fail, but succeeded")
	}
	if !strings.Contains(err.Error(), "must appear in the GROUP BY clause or be used in an aggregate function") {
		t.Errorf("expected standard GROUP BY error message, got: %v", err)
	}

	// 2. Valid: dept_id is in GROUP BY
	res, err := coord.ExecuteSQL("SELECT dept_id FROM _b_having GROUP BY dept_id HAVING dept_id = 1;")
	if err != nil {
		t.Fatalf("expected HAVING dept_id = 1 to succeed, got: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}

	// 3. Valid: SUM(salary) is aggregate function
	res, err = coord.ExecuteSQL("SELECT dept_id FROM _b_having GROUP BY dept_id HAVING SUM(salary) > 250;")
	if err != nil {
		t.Fatalf("expected HAVING SUM(salary) > 250 to succeed, got: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row passing HAVING sum > 250, got %d", len(res.Rows))
	}
}

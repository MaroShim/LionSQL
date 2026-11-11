package engine

import (
	"fmt"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func newSubqueryTestCoordinator(t *testing.T) (*Coordinator, func()) {
	t.Helper()
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("failed to open memengine: %v", err)
	}
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	return coord, func() {
		coord.Close()
		eng.Close()
	}
}

func TestSubqueryMemo_CorrelatedAggregateFilter(t *testing.T) {
	c, cleanup := newSubqueryTestCoordinator(t)
	defer cleanup()

	_, err := c.ExecuteSQL("CREATE TABLE emp (id INT PRIMARY KEY, dept_id INT, name VARCHAR(50), salary DECIMAL(10,2));")
	if err != nil {
		t.Fatalf("CREATE TABLE emp failed: %v", err)
	}

	// 500 employees across 5 departments (100 per dept, salary 1..100)
	for i := 1; i <= 500; i++ {
		deptID := ((i - 1) % 5) + 1
		salary := float64(((i - 1) % 100) + 1)
		_, err := c.ExecuteSQL(fmt.Sprintf("INSERT INTO emp VALUES (%d, %d, 'user_%d', %.2f);", i, deptID, i, salary))
		if err != nil {
			t.Fatalf("INSERT emp failed: %v", err)
		}
	}

	start := time.Now()
	// Correlated subquery: salary > AVG(salary) of employee's department
	// Dept avg = 50.5. Exactly 50 employees per dept (51..100) have salary > 50.5.
	// Total expected matching rows: 50 * 5 = 250 rows.
	query := "SELECT id, name, salary FROM emp e WHERE salary > (SELECT AVG(salary) FROM emp WHERE dept_id = e.dept_id) ORDER BY id;"
	res, err := c.ExecuteSQL(query)
	duration := time.Since(start)
	if err != nil {
		t.Fatalf("Correlated query failed: %v", err)
	}

	t.Logf("Correlated subquery with memoization took: %v (rows=%d)", duration, len(res.Rows))
	if len(res.Rows) != 250 {
		t.Fatalf("expected 250 rows returned, got %d", len(res.Rows))
	}
}

func TestSubqueryMemo_CorrelatedScalarProjection(t *testing.T) {
	c, cleanup := newSubqueryTestCoordinator(t)
	defer cleanup()

	_, err := c.ExecuteSQL("CREATE TABLE dept (id INT PRIMARY KEY, name VARCHAR(50));")
	if err != nil {
		t.Fatalf("CREATE TABLE dept failed: %v", err)
	}
	_, err = c.ExecuteSQL("CREATE TABLE emp_proj (id INT PRIMARY KEY, dept_id INT, name VARCHAR(50));")
	if err != nil {
		t.Fatalf("CREATE TABLE emp_proj failed: %v", err)
	}

	_, _ = c.ExecuteSQL("INSERT INTO dept VALUES (1, 'Engineering');")
	_, _ = c.ExecuteSQL("INSERT INTO dept VALUES (2, 'Marketing');")

	for i := 1; i <= 20; i++ {
		d := 1
		if i > 10 {
			d = 2
		}
		_, err := c.ExecuteSQL(fmt.Sprintf("INSERT INTO emp_proj VALUES (%d, %d, 'user_%d');", i, d, i))
		if err != nil {
			t.Fatalf("INSERT failed: %v", err)
		}
	}

	// Subquery in projection: SELECT name, (SELECT name FROM dept WHERE id = e.dept_id) AS dname
	query := "SELECT id, name, (SELECT name FROM dept WHERE id = e.dept_id) AS dname FROM emp_proj e ORDER BY id;"
	exp, _ := c.ExecuteSQL("EXPLAIN " + query)
	for _, r := range exp.Rows {
		t.Logf("PLAN: %v", r)
	}

	res, err := c.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("Projection subquery failed: %v", err)
	}

	if len(res.Rows) != 20 {
		t.Fatalf("expected 20 rows, got %d", len(res.Rows))
	}

	for i, r := range res.Rows {
		expectedDept := "Engineering"
		if i >= 10 {
			expectedDept = "Marketing"
		}
		if r["dname"] != expectedDept {
			t.Errorf("row %d: expected dname %q, got %v", i+1, expectedDept, r["dname"])
		}
	}
}

func TestSubqueryMemo_CountBugSafety(t *testing.T) {
	c, cleanup := newSubqueryTestCoordinator(t)
	defer cleanup()

	_, err := c.ExecuteSQL("CREATE TABLE parent (id INT PRIMARY KEY, name VARCHAR(50));")
	if err != nil {
		t.Fatalf("CREATE TABLE parent failed: %v", err)
	}
	_, err = c.ExecuteSQL("CREATE TABLE child (id INT PRIMARY KEY, parent_id INT);")
	if err != nil {
		t.Fatalf("CREATE TABLE child failed: %v", err)
	}

	_, _ = c.ExecuteSQL("INSERT INTO parent VALUES (1, 'Alice');")
	_, _ = c.ExecuteSQL("INSERT INTO parent VALUES (2, 'Bob');")
	_, _ = c.ExecuteSQL("INSERT INTO parent VALUES (3, 'Charlie');")

	// Only parent 1 has children
	_, _ = c.ExecuteSQL("INSERT INTO child VALUES (10, 1);")
	_, _ = c.ExecuteSQL("INSERT INTO child VALUES (11, 1);")

	// Correlated subquery checking COUNT(*) = 0
	// Parents 2 and 3 have 0 children and MUST be returned.
	query := "SELECT id, name FROM parent p WHERE (SELECT COUNT(*) FROM child WHERE parent_id = p.id) = 0 ORDER BY id;"
	exp, _ := c.ExecuteSQL("EXPLAIN " + query)
	for _, r := range exp.Rows {
		t.Logf("PLAN: %v", r)
	}

	res, err := c.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("COUNT(*) = 0 subquery failed: %v", err)
	}

	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows (Bob, Charlie), got %d: %+v", len(res.Rows), res.Rows)
	}

	if res.Rows[0]["name"] != "Bob" || res.Rows[1]["name"] != "Charlie" {
		t.Fatalf("unexpected rows: %+v", res.Rows)
	}
}

func TestSubqueryMemo_VolatileBypass(t *testing.T) {
	c, cleanup := newSubqueryTestCoordinator(t)
	defer cleanup()

	_, err := c.ExecuteSQL("CREATE TABLE nums (id INT PRIMARY KEY);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	for i := 1; i <= 5; i++ {
		_, _ = c.ExecuteSQL(fmt.Sprintf("INSERT INTO nums VALUES (%d);", i))
	}

	// Subquery with NEWID() must NOT be cached across rows
	query := "SELECT id, (SELECT NEWID()) AS u FROM nums ORDER BY id;"
	exp, _ := c.ExecuteSQL("EXPLAIN " + query)
	for _, r := range exp.Rows {
		t.Logf("PLAN: %v", r)
	}

	res, err := c.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("NEWID subquery failed: %v", err)
	}

	if len(res.Rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(res.Rows))
	}

	uuids := make(map[string]bool)
	for _, r := range res.Rows {
		u := fmt.Sprint(r["u"])
		if uuids[u] {
			t.Fatalf("duplicate UUID generated across rows; volatile subquery was illegally cached: %s", u)
		}
		uuids[u] = true
	}
}

func TestSubqueryMemo_BoundedCap4096(t *testing.T) {
	c, cleanup := newSubqueryTestCoordinator(t)
	defer cleanup()

	_, err := c.ExecuteSQL("CREATE TABLE big_keys (id INT PRIMARY KEY, val INT);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Insert 4,500 distinct keys (exceeds maxSubqueryMemoEntries = 4096)
	// We do it in small transaction batches for fast insertion
	_, _ = c.ExecuteSQL("BEGIN TRANSACTION;")
	for i := 1; i <= 4500; i++ {
		_, _ = c.ExecuteSQL(fmt.Sprintf("INSERT INTO big_keys VALUES (%d, %d);", i, i*10))
	}
	_, _ = c.ExecuteSQL("COMMIT;")

	// Query each key via correlated subquery: SELECT id, (SELECT val FROM big_keys WHERE id = b.id)
	query := "SELECT id, (SELECT val FROM big_keys WHERE id = b.id) AS v FROM big_keys b WHERE id <= 4500 ORDER BY id;"
	exp, _ := c.ExecuteSQL("EXPLAIN " + query)
	for _, r := range exp.Rows {
		t.Logf("PLAN: %v", r)
	}

	res, err := c.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("Subquery exceeding cap failed: %v", err)
	}

	if len(res.Rows) != 4500 {
		t.Fatalf("expected 4500 rows, got %d", len(res.Rows))
	}

	// Verify boundary check: id=1 should have v=10, id=4500 should have v=45000
	if fmt.Sprint(res.Rows[0]["v"]) != "10" {
		t.Errorf("row 1: expected 10, got %v", res.Rows[0]["v"])
	}
	if fmt.Sprint(res.Rows[4499]["v"]) != "45000" {
		t.Errorf("row 4500: expected 45000, got %v", res.Rows[4499]["v"])
	}
}

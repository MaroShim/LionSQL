package api_test

import (
	"strings"
	"testing"

	"sqlion/pkg/api"
	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
)

func TestSQLExecutor_PredicatePushdownInJoins(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	_, err := server.ExecuteSQL("CREATE TABLE depts (dept_id INT PRIMARY KEY, dept_name VARCHAR(50));")
	if err != nil {
		t.Fatalf("create table depts failed: %v", err)
	}

	_, err = server.ExecuteSQL("CREATE TABLE emps (emp_id INT PRIMARY KEY, dept_id INT, name VARCHAR(50), salary INT, status VARCHAR(20));")
	if err != nil {
		t.Fatalf("create table emps failed: %v", err)
	}

	_, err = server.ExecuteSQL("INSERT INTO depts VALUES (10, 'Engineering'), (20, 'Sales'), (30, 'Marketing');")
	if err != nil {
		t.Fatalf("insert into depts failed: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO emps VALUES
		(1, 10, 'Alice', 95000, 'ACTIVE'),
		(2, 10, 'Bob', 70000, 'ACTIVE'),
		(3, 10, 'Charlie', 105000, 'INACTIVE'),
		(4, 20, 'Dave', 85000, 'ACTIVE'),
		(5, 30, 'Eve', 90000, 'ACTIVE');`)
	if err != nil {
		t.Fatalf("insert into emps failed: %v", err)
	}

	// 1. Verify Join Query with Predicates on both Left and Right tables
	query := "SELECT e.name, d.dept_name, e.salary FROM emps e JOIN depts d ON e.dept_id = d.dept_id WHERE e.status = 'ACTIVE' AND d.dept_name = 'Engineering' AND e.salary > 80000 ORDER BY e.emp_id ASC;"
	res, err := server.ExecuteSQL(query)
	if err != nil || !res.Success {
		t.Fatalf("query failed: %v", err)
	}

	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row (Alice), got %d rows: %+v", len(res.Rows), res.Rows)
	}
	if res.Rows[0]["name"] != "Alice" || res.Rows[0]["dept_name"] != "Engineering" {
		t.Errorf("unexpected row: %+v", res.Rows[0])
	}

	// 2. EXPLAIN query verification: check that filters are pushed down
	explainRes, err := server.ExecuteSQL("EXPLAIN " + query)
	if err != nil || !explainRes.Success {
		t.Fatalf("EXPLAIN failed: %v", err)
	}

	planStr := ""
	for _, r := range explainRes.Rows {
		if p, ok := r["QUERY PLAN"].(string); ok {
			planStr += p + "\n"
		}
	}

	if !strings.Contains(planStr, "Filter") {
		t.Errorf("expected plan to contain Filter, got:\n%s", planStr)
	}
	if !strings.Contains(planStr, "Hash Join") && !strings.Contains(planStr, "Nested Loop") {
		t.Errorf("expected plan to contain Join, got:\n%s", planStr)
	}
}

func TestSQLExecutor_PredicatePushdownIndexScanInJoin(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	_, err := server.ExecuteSQL("CREATE TABLE users (id INT PRIMARY KEY, email VARCHAR(100), tier INT);")
	if err != nil {
		t.Fatalf("create table users failed: %v", err)
	}
	_, err = server.ExecuteSQL("CREATE INDEX idx_users_email ON users(email);")
	if err != nil {
		t.Fatalf("create index failed: %v", err)
	}

	_, err = server.ExecuteSQL("CREATE TABLE orders (order_id INT PRIMARY KEY, user_id INT, amount INT);")
	if err != nil {
		t.Fatalf("create table orders failed: %v", err)
	}

	_, err = server.ExecuteSQL("INSERT INTO users VALUES (1, 'alice@example.com', 1), (2, 'bob@example.com', 2);")
	if err != nil {
		t.Fatalf("insert into users failed: %v", err)
	}
	_, err = server.ExecuteSQL("INSERT INTO orders VALUES (101, 1, 500), (102, 1, 700), (103, 2, 300);")
	if err != nil {
		t.Fatalf("insert into orders failed: %v", err)
	}

	query := "SELECT u.email, o.amount FROM users u JOIN orders o ON u.id = o.user_id WHERE u.email = 'alice@example.com' ORDER BY o.order_id ASC;"
	res, err := server.ExecuteSQL(query)
	if err != nil || !res.Success {
		t.Fatalf("query failed: %v", err)
	}

	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 orders for Alice, got %d: %+v", len(res.Rows), res.Rows)
	}

	explainRes, err := server.ExecuteSQL("EXPLAIN " + query)
	if err != nil || !explainRes.Success {
		t.Fatalf("EXPLAIN failed: %v", err)
	}

	planStr := ""
	for _, r := range explainRes.Rows {
		if p, ok := r["QUERY PLAN"].(string); ok {
			planStr += p + "\n"
		}
	}

	// Predicate pushdown should enable Index Scan on users inside the join!
	if !strings.Contains(planStr, "Index Scan using idx_users_email on users") {
		t.Errorf("expected plan to use Index Scan on users, got:\n%s", planStr)
	}
}

func TestSQLExecutor_PredicatePushdownLeftJoin(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	_, err := server.ExecuteSQL("CREATE TABLE depts (dept_id INT PRIMARY KEY, dept_name VARCHAR(50));")
	if err != nil {
		t.Fatalf("create depts: %v", err)
	}
	_, err = server.ExecuteSQL("CREATE TABLE emps (emp_id INT PRIMARY KEY, dept_id INT, name VARCHAR(50));")
	if err != nil {
		t.Fatalf("create emps: %v", err)
	}

	_, err = server.ExecuteSQL("INSERT INTO depts VALUES (10, 'Engineering'), (20, 'Research');")
	if err != nil {
		t.Fatalf("insert depts: %v", err)
	}
	_, err = server.ExecuteSQL("INSERT INTO emps VALUES (1, 10, 'Alice');")
	if err != nil {
		t.Fatalf("insert emps: %v", err)
	}

	// Left join with predicate pushed down to primary table (depts)
	query := "SELECT d.dept_name, e.name FROM depts d LEFT JOIN emps e ON d.dept_id = e.dept_id WHERE d.dept_name = 'Research';"
	res, err := server.ExecuteSQL(query)
	if err != nil || !res.Success {
		t.Fatalf("query failed: %v", err)
	}

	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row for Research (with NULL name), got %d rows: %+v", len(res.Rows), res.Rows)
	}
	if res.Rows[0]["dept_name"] != "Research" {
		t.Errorf("expected dept_name Research, got %v", res.Rows[0]["dept_name"])
	}
}

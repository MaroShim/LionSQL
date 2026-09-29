package api_test

import (
	"strings"
	"testing"

	"sqlion/pkg/api"
	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
)

func TestSQLExecutor_ConstantFoldingInQueries(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	_, err := server.ExecuteSQL("CREATE TABLE employees (id INT PRIMARY KEY, name VARCHAR(50), salary INT);")
	if err != nil {
		t.Fatalf("create table failed: %v", err)
	}

	_, err = server.ExecuteSQL("INSERT INTO employees VALUES (1, 'Alice', 45000), (2, 'Bob', 35000), (3, 'Charlie', 25000);")
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// 1. Constant folding in arithmetic expression: 3000 * 12 -> 36000
	query := "SELECT name, salary FROM employees WHERE salary > 3000 * 12 ORDER BY id ASC;"
	res, err := server.ExecuteSQL(query)
	if err != nil || !res.Success {
		t.Fatalf("query failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Alice" {
		t.Fatalf("expected 1 row (Alice), got %d rows: %+v", len(res.Rows), res.Rows)
	}

	// 2. EXPLAIN output verification: should display folded constant 36000
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
	if !strings.Contains(planStr, "36000") {
		t.Errorf("expected plan to contain folded literal '36000', got:\n%s", planStr)
	}

	// 3. Constant folding with logical boolean AND: 1 = 1 AND salary > 3000 * 10 (30000)
	queryBool := "SELECT name FROM employees WHERE 1 = 1 AND salary > 3000 * 10 ORDER BY id ASC;"
	resBool, err := server.ExecuteSQL(queryBool)
	if err != nil || !resBool.Success {
		t.Fatalf("query with 1=1 AND failed: %v", err)
	}
	// Alice (45000) and Bob (35000) are > 30000
	if len(resBool.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %+v", len(resBool.Rows), resBool.Rows)
	}

	// 4. Constant folding with false short-circuit: 1 = 0 AND salary > 1000
	queryFalse := "SELECT name FROM employees WHERE 1 = 0 AND salary > 1000;"
	resFalse, err := server.ExecuteSQL(queryFalse)
	if err != nil || !resFalse.Success {
		t.Fatalf("query with 1=0 AND failed: %v", err)
	}
	if len(resFalse.Rows) != 0 {
		t.Fatalf("expected 0 rows for false short-circuit, got %d", len(resFalse.Rows))
	}

	// 5. Constant folding with deterministic built-in functions: UPPER('alice')
	queryFn := "SELECT name FROM employees WHERE name = UPPER('alice');"
	resFn, err := server.ExecuteSQL(queryFn)
	if err != nil || !resFn.Success {
		t.Fatalf("query with UPPER() failed: %v", err)
	}
	// Name in DB is 'Alice', but case-insensitive or folded to 'ALICE'
	// In SQLion string equality is case-insensitive
	if len(resFn.Rows) != 1 || resFn.Rows[0]["name"] != "Alice" {
		t.Fatalf("expected 1 row for Alice with UPPER(), got %+v", resFn.Rows)
	}
}

func TestSQLExecutor_ConstantFoldingIndexScan(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	_, _ = server.ExecuteSQL("CREATE TABLE items (id INT PRIMARY KEY, price INT);")
	_, _ = server.ExecuteSQL("CREATE INDEX idx_items_price ON items(price);")
	_, _ = server.ExecuteSQL("INSERT INTO items VALUES (1, 100), (2, 200), (3, 300), (4, 400), (5, 500);")
	_, _ = server.ExecuteSQL("ANALYZE TABLE items;")

	// Query: WHERE price = 100 * 3 (300).
	// With constant folding, price = 300 matches the index on price!
	explainRes, err := server.ExecuteSQL("EXPLAIN SELECT id, price FROM items WHERE price = 100 * 3;")
	if err != nil || !explainRes.Success {
		t.Fatalf("EXPLAIN failed: %v", err)
	}
	planStr := ""
	for _, r := range explainRes.Rows {
		if p, ok := r["QUERY PLAN"].(string); ok {
			planStr += p + "\n"
		}
	}
	if !strings.Contains(planStr, "Index Scan") && !strings.Contains(planStr, "Index Only Scan") {
		t.Errorf("expected plan to utilize Index Scan after constant folding, got:\n%s", planStr)
	}
	if !strings.Contains(planStr, "300") {
		t.Errorf("expected plan to contain folded literal 300, got:\n%s", planStr)
	}

	// Verify query execution returns item 3
	res, err := server.ExecuteSQL("SELECT id, price FROM items WHERE price = 100 * 3;")
	if err != nil || !res.Success {
		t.Fatalf("execution failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["id"] != int64(3) {
		t.Fatalf("expected id=3, got %+v", res.Rows)
	}
}

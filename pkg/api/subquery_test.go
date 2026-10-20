package api_test

import (
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/api"
	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestSQLExecutor_CorrelatedSubqueryAndSemiJoin(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	// 1. Setup tables and data
	ddl := []string{
		"CREATE TABLE test_users (id INT PRIMARY KEY, name VARCHAR(50), status VARCHAR(20));",
		"CREATE TABLE test_orders (id INT PRIMARY KEY, user_id INT, amount FLOAT);",
		"INSERT INTO test_users VALUES (1, 'Alice', 'active');",
		"INSERT INTO test_users VALUES (2, 'Bob', 'active');",
		"INSERT INTO test_users VALUES (3, 'Charlie', 'inactive');",
		"INSERT INTO test_orders VALUES (101, 1, 150.0);",
		"INSERT INTO test_orders VALUES (102, 1, 600.0);", // Alice has 2 orders
		"INSERT INTO test_orders VALUES (103, 2, 200.0);", // Bob has 1 order
	}
	for _, sql := range ddl {
		if _, err := server.ExecuteSQL(sql); err != nil {
			t.Fatalf("setup failed on %q: %v", sql, err)
		}
	}

	// 2. Correlated EXISTS (Semi-Join)
	// Alice has 2 orders, but SEMI join must emit Alice exactly once.
	qExists := "SELECT u.name FROM test_users u WHERE EXISTS (SELECT 1 FROM test_orders o WHERE o.user_id = u.id) ORDER BY u.name ASC;"
	res, err := server.ExecuteSQL(qExists)
	if err != nil {
		t.Fatalf("correlated EXISTS failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows for correlated EXISTS, got %d: %+v", len(res.Rows), res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["name"]) != "Alice" || fmt.Sprintf("%v", res.Rows[1]["name"]) != "Bob" {
		t.Errorf("expected [Alice, Bob], got [%v, %v]", res.Rows[0]["name"], res.Rows[1]["name"])
	}

	// 3. Correlated NOT EXISTS (Anti-Join)
	// Charlie has 0 orders.
	qNotExists := "SELECT u.name FROM test_users u WHERE NOT EXISTS (SELECT 1 FROM test_orders o WHERE o.user_id = u.id);"
	res, err = server.ExecuteSQL(qNotExists)
	if err != nil {
		t.Fatalf("correlated NOT EXISTS failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row for NOT EXISTS, got %d: %+v", len(res.Rows), res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["name"]) != "Charlie" {
		t.Errorf("expected Charlie, got %v", res.Rows[0]["name"])
	}

	// 4. Correlated EXISTS with extra inner filter (amount > 500)
	// Only Alice has an order > 500.
	qFilter := "SELECT u.name FROM test_users u WHERE EXISTS (SELECT 1 FROM test_orders o WHERE o.user_id = u.id AND o.amount > 500);"
	res, err = server.ExecuteSQL(qFilter)
	if err != nil {
		t.Fatalf("correlated EXISTS with filter failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row for EXISTS with filter, got %d: %+v", len(res.Rows), res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["name"]) != "Alice" {
		t.Errorf("expected Alice, got %v", res.Rows[0]["name"])
	}

	// 5. IN subquery
	qIn := "SELECT name FROM test_users WHERE id IN (SELECT user_id FROM test_orders WHERE amount >= 200) ORDER BY name ASC;"
	res, err = server.ExecuteSQL(qIn)
	if err != nil {
		t.Fatalf("IN subquery failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows for IN subquery, got %d: %+v", len(res.Rows), res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["name"]) != "Alice" || fmt.Sprintf("%v", res.Rows[1]["name"]) != "Bob" {
		t.Errorf("expected [Alice, Bob], got [%v, %v]", res.Rows[0]["name"], res.Rows[1]["name"])
	}

	// 6. NOT IN subquery
	qNotIn := "SELECT name FROM test_users WHERE id NOT IN (SELECT user_id FROM test_orders);"
	res, err = server.ExecuteSQL(qNotIn)
	if err != nil {
		t.Fatalf("NOT IN subquery failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row for NOT IN subquery, got %d: %+v", len(res.Rows), res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["name"]) != "Charlie" {
		t.Errorf("expected Charlie, got %v", res.Rows[0]["name"])
	}

	// 7. EXPLAIN verifies SEMI join in execution plan
	qExplain := "EXPLAIN SELECT u.name FROM test_users u WHERE EXISTS (SELECT 1 FROM test_orders o WHERE o.user_id = u.id);"
	resExp, err := server.ExecuteSQL(qExplain)
	if err != nil {
		t.Fatalf("EXPLAIN failed: %v", err)
	}
	planText := fmt.Sprintf("%+v", resExp.Rows)
	if !strings.Contains(planText, "SEMI") && !strings.Contains(planText, "Hash Join") {
		t.Errorf("expected EXPLAIN plan to contain SEMI join or Hash Join, got: %s", planText)
	}
}

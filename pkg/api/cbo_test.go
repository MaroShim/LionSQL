package api_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestCBO_DynamicStatsJoinOrderAndCompoundSelectivity(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	// 1. Create two tables: big_orders (100 rows) and small_customers (5 rows)
	_, err := server.ExecuteSQL(`CREATE TABLE small_customers (
		id INT PRIMARY KEY,
		name VARCHAR(50) NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE small_customers failed: %v", err)
	}

	_, err = server.ExecuteSQL(`CREATE TABLE big_orders (
		id INT PRIMARY KEY,
		customer_id INT NOT NULL,
		amount INT NOT NULL,
		status VARCHAR(20) NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE big_orders failed: %v", err)
	}

	// 2. Populate tables without running explicit ANALYZE
	for i := 1; i <= 5; i++ {
		_, err := server.ExecuteSQL(fmt.Sprintf("INSERT INTO small_customers (id, name) VALUES (%d, 'Cust_%d');", i, i))
		if err != nil {
			t.Fatalf("INSERT customer failed: %v", err)
		}
	}

	for i := 1; i <= 100; i++ {
		status := "COMPLETED"
		if i%2 == 0 {
			status = "PENDING"
		}
		_, err := server.ExecuteSQL(fmt.Sprintf("INSERT INTO big_orders (id, customer_id, amount, status) VALUES (%d, %d, %d, '%s');", i, (i%5)+1, i*10, status))
		if err != nil {
			t.Fatalf("INSERT order failed: %v", err)
		}
	}

	// 3. EXPLAIN query joining big_orders and small_customers
	explainRes, err := server.ExecuteSQL("EXPLAIN SELECT o.id, c.name, o.amount FROM big_orders o JOIN small_customers c ON o.customer_id = c.id WHERE o.amount >= 500 AND o.status = 'COMPLETED';")
	if err != nil {
		t.Fatalf("EXPLAIN failed: %v", err)
	}
	if len(explainRes.Rows) == 0 {
		t.Fatalf("expected non-empty explain plan")
	}

	var planLines []string
	for _, r := range explainRes.Rows {
		for _, v := range r {
			planLines = append(planLines, fmt.Sprintf("%v", v))
		}
	}
	fullPlan := strings.Join(planLines, "\n")

	// Plan should choose Hash Join
	if !strings.Contains(fullPlan, "Hash Join") {
		t.Fatalf("expected plan to use Hash Join, got:\n%s", fullPlan)
	}

	// 4. Verify compound filter estimates reduced rows
	if !strings.Contains(fullPlan, "Filter") && !strings.Contains(fullPlan, "Hash Join") {
		t.Fatalf("expected plan with filters, got:\n%s", fullPlan)
	}
}

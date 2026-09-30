package api_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestMultiTableChainedJoins_3And4Tables(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	// 1. Create 4 relational tables: customers, orders, order_items, products
	stmts := []string{
		"CREATE TABLE customers (id INT PRIMARY KEY, name VARCHAR(50), city VARCHAR(50))",
		"CREATE TABLE orders (id INT PRIMARY KEY, customer_id INT, order_date VARCHAR(20))",
		"CREATE TABLE order_items (id INT PRIMARY KEY, order_id INT, product_id INT, quantity INT)",
		"CREATE TABLE products (id INT PRIMARY KEY, name VARCHAR(50), price INT)",

		// Customers: 3 customers
		"INSERT INTO customers VALUES (1, 'Alice', 'New York')",
		"INSERT INTO customers VALUES (2, 'Bob', 'London')",
		"INSERT INTO customers VALUES (3, 'Charlie', 'Tokyo')",

		// Orders: 4 orders
		"INSERT INTO orders VALUES (101, 1, '2024-01-15')",
		"INSERT INTO orders VALUES (102, 1, '2024-02-20')",
		"INSERT INTO orders VALUES (103, 2, '2024-03-01')",
		"INSERT INTO orders VALUES (104, 3, '2024-04-10')",

		// Products: 3 products
		"INSERT INTO products VALUES (501, 'Keyboard', 80)",
		"INSERT INTO products VALUES (502, 'Mouse', 40)",
		"INSERT INTO products VALUES (503, 'Monitor', 300)",

		// Order Items: 5 items
		"INSERT INTO order_items VALUES (1001, 101, 501, 2)", // Alice: Keyboard (2)
		"INSERT INTO order_items VALUES (1002, 101, 502, 1)", // Alice: Mouse (1)
		"INSERT INTO order_items VALUES (1003, 102, 503, 1)", // Alice: Monitor (1)
		"INSERT INTO order_items VALUES (1004, 103, 501, 1)", // Bob: Keyboard (1)
		"INSERT INTO order_items VALUES (1005, 104, 502, 3)", // Charlie: Mouse (3)
	}

	for _, stmt := range stmts {
		if _, err := server.ExecuteSQL(stmt); err != nil {
			t.Fatalf("setup statement failed (%q): %v", stmt, err)
		}
	}

	t.Run("3-table chained join query", func(t *testing.T) {
		sql := `SELECT c.name, o.id, oi.quantity FROM customers c JOIN orders o ON c.id = o.customer_id JOIN order_items oi ON o.id = oi.order_id WHERE c.name = 'Alice'`
		res, err := server.ExecuteSQL(sql)
		if err != nil {
			t.Fatalf("3-table join failed: %v", err)
		}
		if len(res.Rows) != 3 {
			t.Fatalf("expected 3 items for Alice, got %d: %+v", len(res.Rows), res.Rows)
		}
	})

	t.Run("4-table chained join query with product details", func(t *testing.T) {
		sql := `SELECT c.name, p.name AS prod_name, p.price, oi.quantity FROM customers c JOIN orders o ON c.id = o.customer_id JOIN order_items oi ON o.id = oi.order_id JOIN products p ON oi.product_id = p.id WHERE c.city = 'London'`
		res, err := server.ExecuteSQL(sql)
		if err != nil {
			t.Fatalf("4-table join failed: %v", err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("expected 1 row for Bob in London, got %d: %+v", len(res.Rows), res.Rows)
		}
		row := res.Rows[0]
		if fmt.Sprintf("%v", row["name"]) != "Bob" {
			t.Errorf("expected customer Bob, got %v", row["name"])
		}
		if fmt.Sprintf("%v", row["prod_name"]) != "Keyboard" {
			t.Errorf("expected product Keyboard, got %v", row["prod_name"])
		}
	})

	t.Run("3-table join explain verification", func(t *testing.T) {
		expSQL := `EXPLAIN SELECT c.name, o.id, oi.quantity FROM customers c JOIN orders o ON c.id = o.customer_id JOIN order_items oi ON o.id = oi.order_id`
		expRes, err := server.ExecuteSQL(expSQL)
		if err != nil {
			t.Fatalf("EXPLAIN failed: %v", err)
		}
		if len(expRes.Rows) == 0 {
			t.Fatalf("expected non-empty explain output")
		}
		var lines []string
		for _, r := range expRes.Rows {
			for _, v := range r {
				lines = append(lines, fmt.Sprintf("%v", v))
			}
		}
		fullOutput := strings.Join(lines, "\n")
		if !strings.Contains(fullOutput, "Join") {
			t.Errorf("expected Join node in explain output, got:\n%s", fullOutput)
		}
	})
}

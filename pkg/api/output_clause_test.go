package api_test

import (
	"fmt"
	"testing"
)

func TestOutputClause_Insert(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL("CREATE TABLE employees (id INT PRIMARY KEY, name VARCHAR(50), dept VARCHAR(50), salary INT)")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	t.Run("INSERT with explicit INSERTED column projections", func(t *testing.T) {
		sql := "INSERT INTO employees (id, name, dept, salary) OUTPUT INSERTED.id, INSERTED.name, INSERTED.salary VALUES (1, 'Alice', 'Engineering', 100000)"
		res, err := server.ExecuteSQL(sql)
		if err != nil || !res.Success {
			t.Fatalf("INSERT with OUTPUT failed: %v", err)
		}
		if res.RowsAffected != 1 {
			t.Errorf("expected 1 row affected, got %d", res.RowsAffected)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("expected 1 output row, got %d", len(res.Rows))
		}
		row := res.Rows[0]
		if fmt.Sprintf("%v", row["id"]) != "1" {
			t.Errorf("expected id 1, got %v", row["id"])
		}
		if fmt.Sprintf("%v", row["name"]) != "Alice" {
			t.Errorf("expected name Alice, got %v", row["name"])
		}
		if fmt.Sprintf("%v", row["salary"]) != "100000" {
			t.Errorf("expected salary 100000, got %v", row["salary"])
		}
	})

	t.Run("INSERT with wildcard INSERTED.* and alias", func(t *testing.T) {
		sql := "INSERT INTO employees (id, name, dept, salary) OUTPUT INSERTED.* VALUES (2, 'Bob', 'Marketing', 80000)"
		res, err := server.ExecuteSQL(sql)
		if err != nil || !res.Success {
			t.Fatalf("INSERT with OUTPUT * failed: %v", err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("expected 1 output row, got %d", len(res.Rows))
		}
		row := res.Rows[0]
		if fmt.Sprintf("%v", row["dept"]) != "Marketing" {
			t.Errorf("expected dept Marketing, got %v", row["dept"])
		}
	})

	t.Run("Multi-row INSERT with OUTPUT", func(t *testing.T) {
		sql := "INSERT INTO employees (id, name, dept, salary) OUTPUT INSERTED.id, INSERTED.name VALUES (3, 'Charlie', 'Sales', 75000), (4, 'Diana', 'HR', 70000)"
		res, err := server.ExecuteSQL(sql)
		if err != nil || !res.Success {
			t.Fatalf("Multi-row INSERT with OUTPUT failed: %v", err)
		}
		if len(res.Rows) != 2 {
			t.Fatalf("expected 2 output rows, got %d", len(res.Rows))
		}
		if fmt.Sprintf("%v", res.Rows[0]["name"]) != "Charlie" || fmt.Sprintf("%v", res.Rows[1]["name"]) != "Diana" {
			t.Errorf("unexpected multi-row output: %+v", res.Rows)
		}
	})
}

func TestOutputClause_Update(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	stmts := []string{
		"CREATE TABLE products (id INT PRIMARY KEY, name VARCHAR(50), price INT)",
		"INSERT INTO products VALUES (1, 'Laptop', 1000)",
		"INSERT INTO products VALUES (2, 'Mouse', 50)",
		"INSERT INTO products VALUES (3, 'Keyboard', 80)",
	}
	for _, stmt := range stmts {
		if _, err := server.ExecuteSQL(stmt); err != nil {
			t.Fatalf("setup failed (%q): %v", stmt, err)
		}
	}

	t.Run("UPDATE with DELETED and INSERTED projections and aliases", func(t *testing.T) {
		sql := "UPDATE products SET price = 1200 OUTPUT DELETED.price AS old_price, INSERTED.price AS new_price, INSERTED.name WHERE id = 1"
		res, err := server.ExecuteSQL(sql)
		if err != nil || !res.Success {
			t.Fatalf("UPDATE with OUTPUT failed: %v", err)
		}
		if res.RowsAffected != 1 {
			t.Errorf("expected 1 row affected, got %d", res.RowsAffected)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("expected 1 output row, got %d", len(res.Rows))
		}
		row := res.Rows[0]
		if fmt.Sprintf("%v", row["old_price"]) != "1000" {
			t.Errorf("expected old_price 1000, got %v", row["old_price"])
		}
		if fmt.Sprintf("%v", row["new_price"]) != "1200" {
			t.Errorf("expected new_price 1200, got %v", row["new_price"])
		}
		if fmt.Sprintf("%v", row["name"]) != "Laptop" {
			t.Errorf("expected name Laptop, got %v", row["name"])
		}
	})

	t.Run("Multi-row UPDATE with OUTPUT", func(t *testing.T) {
		sql := "UPDATE products SET price = price + 10 OUTPUT DELETED.name, DELETED.price AS old_price, INSERTED.price AS new_price WHERE price < 100"
		res, err := server.ExecuteSQL(sql)
		if err != nil || !res.Success {
			t.Fatalf("Multi-row UPDATE with OUTPUT failed: %v", err)
		}
		if len(res.Rows) != 2 {
			t.Fatalf("expected 2 output rows, got %d: %+v", len(res.Rows), res.Rows)
		}
	})
}

func TestOutputClause_Delete(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	stmts := []string{
		"CREATE TABLE tasks (id INT PRIMARY KEY, title VARCHAR(50), status VARCHAR(20))",
		"INSERT INTO tasks VALUES (1, 'Task A', 'completed')",
		"INSERT INTO tasks VALUES (2, 'Task B', 'pending')",
		"INSERT INTO tasks VALUES (3, 'Task C', 'completed')",
	}
	for _, stmt := range stmts {
		if _, err := server.ExecuteSQL(stmt); err != nil {
			t.Fatalf("setup failed (%q): %v", stmt, err)
		}
	}

	t.Run("DELETE with DELETED projections", func(t *testing.T) {
		sql := "DELETE FROM tasks OUTPUT DELETED.id, DELETED.title WHERE id = 2"
		res, err := server.ExecuteSQL(sql)
		if err != nil || !res.Success {
			t.Fatalf("DELETE with OUTPUT failed: %v", err)
		}
		if res.RowsAffected != 1 {
			t.Errorf("expected 1 row affected, got %d", res.RowsAffected)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("expected 1 output row, got %d", len(res.Rows))
		}
		row := res.Rows[0]
		if fmt.Sprintf("%v", row["id"]) != "2" || fmt.Sprintf("%v", row["title"]) != "Task B" {
			t.Errorf("unexpected deleted row: %+v", row)
		}
	})

	t.Run("Multi-row DELETE with DELETED.*", func(t *testing.T) {
		sql := "DELETE FROM tasks OUTPUT DELETED.* WHERE status = 'completed'"
		res, err := server.ExecuteSQL(sql)
		if err != nil || !res.Success {
			t.Fatalf("Multi-row DELETE with OUTPUT * failed: %v", err)
		}
		if len(res.Rows) != 2 {
			t.Fatalf("expected 2 output rows, got %d: %+v", len(res.Rows), res.Rows)
		}
	})
}

package api_test

import (
	"fmt"
	"testing"
)

func TestPreparedStatement_BasicSelect(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(50), age INT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	_, err = server.ExecuteSQL(`INSERT INTO users (id, name, age) VALUES (1, 'Alice', 30), (2, 'Bob', 25);`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 1. PREPARE with $1 placeholder
	_, err = server.ExecuteSessionSQL("sess1", `PREPARE get_user (int) AS SELECT id, name, age FROM users WHERE id = $1;`)
	if err != nil {
		t.Fatalf("PREPARE get_user failed: %v", err)
	}

	// 2. EXECUTE with positional argument
	res, err := server.ExecuteSessionSQL("sess1", `EXECUTE get_user(1);`)
	if err != nil {
		t.Fatalf("EXECUTE get_user failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if res.Rows[0]["name"] != "Alice" {
		t.Fatalf("expected 'Alice', got %v", res.Rows[0]["name"])
	}

	// 3. EXECUTE for Bob
	res, err = server.ExecuteSessionSQL("sess1", `EXECUTE get_user(2);`)
	if err != nil {
		t.Fatalf("EXECUTE get_user(2) failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Bob" {
		t.Fatalf("expected 'Bob', got %v", res.Rows[0]["name"])
	}

	// 4. DEALLOCATE PREPARE
	_, err = server.ExecuteSessionSQL("sess1", `DEALLOCATE PREPARE get_user;`)
	if err != nil {
		t.Fatalf("DEALLOCATE PREPARE failed: %v", err)
	}

	// 5. Verify it cannot be executed anymore
	_, err = server.ExecuteSessionSQL("sess1", `EXECUTE get_user(1);`)
	if err == nil {
		t.Fatalf("expected error executing deallocated statement")
	}
}

func TestPreparedStatement_MySQLStyleFrom(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE products (id INT PRIMARY KEY, title VARCHAR(50), price INT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	_, err = server.ExecuteSQL(`INSERT INTO products (id, title, price) VALUES (10, 'Book', 15), (20, 'Pen', 3);`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 1. PREPARE stmt FROM 'SELECT ... WHERE id = ?'
	_, err = server.ExecuteSessionSQL("sess1", `PREPARE get_prod FROM 'SELECT id, title, price FROM products WHERE id = ?';`)
	if err != nil {
		t.Fatalf("PREPARE get_prod failed: %v", err)
	}

	res, err := server.ExecuteSessionSQL("sess1", `EXECUTE get_prod(10);`)
	if err != nil {
		t.Fatalf("EXECUTE failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["title"] != "Book" {
		t.Fatalf("expected 'Book', got %v", res.Rows)
	}

	// 2. MySQL USING syntax with session variables
	_, err = server.ExecuteSessionSQL("sess1", `SET @target_id = 20;`)
	if err != nil {
		t.Fatalf("SET failed: %v", err)
	}
	res, err = server.ExecuteSessionSQL("sess1", `EXECUTE get_prod USING @target_id;`)
	if err != nil {
		t.Fatalf("EXECUTE USING failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["title"] != "Pen" {
		t.Fatalf("expected 'Pen', got %v", res.Rows)
	}

	// 3. PREPARE from session variable string
	_, err = server.ExecuteSessionSQL("sess1", `SET @sql_query = 'SELECT price FROM products WHERE id = ?';`)
	if err != nil {
		t.Fatalf("SET @sql_query failed: %v", err)
	}
	_, err = server.ExecuteSessionSQL("sess1", `PREPARE dyn_stmt FROM @sql_query;`)
	if err != nil {
		t.Fatalf("PREPARE from variable failed: %v", err)
	}
	res, err = server.ExecuteSessionSQL("sess1", `EXECUTE dyn_stmt(20);`)
	if err != nil {
		t.Fatalf("EXECUTE dyn_stmt failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}

	// 4. DEALLOCATE ALL
	_, err = server.ExecuteSessionSQL("sess1", `DEALLOCATE ALL;`)
	if err != nil {
		t.Fatalf("DEALLOCATE ALL failed: %v", err)
	}
}

func TestPreparedStatement_InsertUpdateDelete(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE items (id INT PRIMARY KEY, name VARCHAR(50), qty INT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 1. Prepared INSERT
	_, err = server.ExecuteSessionSQL("sess1", `PREPARE insert_item FROM 'INSERT INTO items (id, name, qty) VALUES (?, ?, ?)';`)
	if err != nil {
		t.Fatalf("PREPARE insert_item failed: %v", err)
	}

	for i := 1; i <= 5; i++ {
		_, err = server.ExecuteSessionSQL("sess1", fmt.Sprintf("EXECUTE insert_item(%d, 'Item_%d', %d);", i, i, i*10))
		if err != nil {
			t.Fatalf("EXECUTE insert_item failed at %d: %v", i, err)
		}
	}

	// Verify row count
	res, err := server.ExecuteSessionSQL("sess1", `SELECT COUNT(*) AS total FROM items;`)
	if err != nil || len(res.Rows) == 0 {
		t.Fatalf("SELECT failed: %v", err)
	}
	if fmt.Sprintf("%v", res.Rows[0]["total"]) != "5" {
		t.Fatalf("expected 5 items, got %v", res.Rows[0]["total"])
	}

	// 2. Prepared UPDATE
	_, err = server.ExecuteSessionSQL("sess1", `PREPARE update_item (int, int) AS UPDATE items SET qty = $1 WHERE id = $2;`)
	if err != nil {
		t.Fatalf("PREPARE update_item failed: %v", err)
	}
	_, err = server.ExecuteSessionSQL("sess1", `EXECUTE update_item(999, 1);`)
	if err != nil {
		t.Fatalf("EXECUTE update_item failed: %v", err)
	}

	res, err = server.ExecuteSessionSQL("sess1", `SELECT qty FROM items WHERE id = 1;`)
	if err != nil || len(res.Rows) == 0 || fmt.Sprintf("%v", res.Rows[0]["qty"]) != "999" {
		t.Fatalf("expected qty 999, got %v", res.Rows)
	}

	// 3. Prepared DELETE
	_, err = server.ExecuteSessionSQL("sess1", `PREPARE delete_item FROM 'DELETE FROM items WHERE id = ?';`)
	if err != nil {
		t.Fatalf("PREPARE delete_item failed: %v", err)
	}
	_, err = server.ExecuteSessionSQL("sess1", `EXECUTE delete_item(1);`)
	if err != nil {
		t.Fatalf("EXECUTE delete_item failed: %v", err)
	}

	res, err = server.ExecuteSessionSQL("sess1", `SELECT COUNT(*) AS total FROM items;`)
	if err != nil || len(res.Rows) == 0 || fmt.Sprintf("%v", res.Rows[0]["total"]) != "4" {
		t.Fatalf("expected 4 items after delete, got %v", res.Rows)
	}
}

func TestPreparedStatement_TransactionAtomicity(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE tx_test (id INT PRIMARY KEY, val VARCHAR(50));`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = server.ExecuteSessionSQL("tx_sess", `PREPARE ins (int, text) AS INSERT INTO tx_test (id, val) VALUES ($1, $2);`)
	if err != nil {
		t.Fatalf("PREPARE ins failed: %v", err)
	}

	// Start explicit transaction and rollback
	_, err = server.ExecuteSessionSQL("tx_sess", `BEGIN TRANSACTION;`)
	if err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	_, err = server.ExecuteSessionSQL("tx_sess", `EXECUTE ins(100, 'RollbackMe');`)
	if err != nil {
		t.Fatalf("EXECUTE ins failed: %v", err)
	}
	_, err = server.ExecuteSessionSQL("tx_sess", `ROLLBACK;`)
	if err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// Verify row does not exist
	res, err := server.ExecuteSessionSQL("tx_sess", `SELECT * FROM tx_test WHERE id = 100;`)
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows after rollback, got %d", len(res.Rows))
	}
}

func TestPlanCache_RecurringExecutionThroughput(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE cache_test (id INT PRIMARY KEY, score INT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Repeated raw INSERTs should hit s.executor.Cache()
	for i := 1; i <= 50; i++ {
		_, err := server.ExecuteSQL(fmt.Sprintf("INSERT INTO cache_test (id, score) VALUES (%d, %d);", i, i*10))
		if err != nil {
			t.Fatalf("INSERT failed at %d: %v", i, err)
		}
	}

	// Verify plan cache contains compiled bytecode programs
	cache := server.BytecodePlanCache()
	if cache == nil {
		t.Fatalf("BytecodePlanCache is nil")
	}

	res, err := server.ExecuteSQL(`SELECT COUNT(*) AS total FROM cache_test;`)
	if err != nil || len(res.Rows) == 0 || fmt.Sprintf("%v", res.Rows[0]["total"]) != "50" {
		t.Fatalf("expected 50 rows, got %v", res.Rows)
	}
}

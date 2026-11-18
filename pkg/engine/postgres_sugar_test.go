package engine

import (
	"testing"
)

func TestPostgresSugar_CreateTempTableAndSessionIsolation(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess1 := "sess_pg_1"
	sess2 := "sess_pg_2"

	// 1. CREATE TEMP TABLE in sess1
	_, err := coord.ExecuteSessionSQL(sess1, "CREATE TEMP TABLE pg_tmp (id INT PRIMARY KEY, name VARCHAR(50));")
	if err != nil {
		t.Fatalf("CREATE TEMP TABLE failed: %v", err)
	}

	// 2. Insert into temp table in sess1
	_, err = coord.ExecuteSessionSQL(sess1, "INSERT INTO pg_tmp (id, name) VALUES (1, 'Alice'), (2, 'Bob');")
	if err != nil {
		t.Fatalf("INSERT into pg_tmp failed: %v", err)
	}

	// 3. Query temp table in sess1
	res, err := coord.ExecuteSessionSQL(sess1, "SELECT id, name FROM pg_tmp ORDER BY id;")
	if err != nil {
		t.Fatalf("SELECT from pg_tmp failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}
	if res.Rows[0]["name"] != "Alice" || res.Rows[1]["name"] != "Bob" {
		t.Fatalf("unexpected rows: %v", res.Rows)
	}

	// 4. Session isolation: sess2 must not see pg_tmp
	_, err = coord.ExecuteSessionSQL(sess2, "SELECT * FROM pg_tmp;")
	if err == nil {
		t.Fatalf("expected error querying sess1's temp table from sess2, got nil")
	}

	// 5. CREATE TEMPORARY TABLE in sess2 with same name without collision
	_, err = coord.ExecuteSessionSQL(sess2, "CREATE TEMPORARY TABLE pg_tmp (id INT PRIMARY KEY, name VARCHAR(50));")
	if err != nil {
		t.Fatalf("CREATE TEMPORARY TABLE in sess2 failed: %v", err)
	}
	_, err = coord.ExecuteSessionSQL(sess2, "INSERT INTO pg_tmp (id, name) VALUES (99, 'Charlie');")
	if err != nil {
		t.Fatalf("INSERT into sess2 pg_tmp failed: %v", err)
	}
	res2, err := coord.ExecuteSessionSQL(sess2, "SELECT name FROM pg_tmp;")
	if err != nil {
		t.Fatalf("SELECT from sess2 pg_tmp failed: %v", err)
	}
	if len(res2.Rows) != 1 || res2.Rows[0]["name"] != "Charlie" {
		t.Fatalf("expected 1 row ('Charlie') in sess2, got %v", res2.Rows)
	}
}

func TestPostgresSugar_ReturningClause(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sessID := "sess_pg_returning"

	// Create test table
	_, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE users_ret (id INT PRIMARY KEY, score INT, tag VARCHAR(20));")
	if err != nil {
		t.Fatalf("CREATE TABLE users_ret failed: %v", err)
	}

	// 1. INSERT ... RETURNING id, score
	res, err := coord.ExecuteSessionSQL(sessID, "INSERT INTO users_ret (id, score, tag) VALUES (1, 100, 'gold') RETURNING id, score;")
	if err != nil {
		t.Fatalf("INSERT ... RETURNING failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row in RETURNING result, got %d", len(res.Rows))
	}
	if res.Rows[0]["score"] != int64(100) && res.Rows[0]["score"] != 100 {
		t.Fatalf("expected score=100, got %v (%T)", res.Rows[0]["score"], res.Rows[0]["score"])
	}

	// 2. INSERT ... RETURNING *
	res, err = coord.ExecuteSessionSQL(sessID, "INSERT INTO users_ret (id, score, tag) VALUES (2, 200, 'silver') RETURNING *;")
	if err != nil {
		t.Fatalf("INSERT ... RETURNING * failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row in RETURNING * result, got %d", len(res.Rows))
	}
	if res.Rows[0]["tag"] != "silver" {
		t.Fatalf("expected tag='silver', got %v", res.Rows[0]["tag"])
	}

	// 3. UPDATE ... RETURNING score
	res, err = coord.ExecuteSessionSQL(sessID, "UPDATE users_ret SET score = 300 WHERE id = 1 RETURNING score;")
	if err != nil {
		t.Fatalf("UPDATE ... RETURNING failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row in UPDATE RETURNING result, got %d", len(res.Rows))
	}
	if res.Rows[0]["score"] != int64(300) && res.Rows[0]["score"] != 300 {
		t.Fatalf("expected updated score=300, got %v", res.Rows[0]["score"])
	}

	// 4. DELETE ... RETURNING id, score
	res, err = coord.ExecuteSessionSQL(sessID, "DELETE FROM users_ret WHERE id = 1 RETURNING id, score;")
	if err != nil {
		t.Fatalf("DELETE ... RETURNING failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row in DELETE RETURNING result, got %d", len(res.Rows))
	}
	if res.Rows[0]["score"] != int64(300) && res.Rows[0]["score"] != 300 {
		t.Fatalf("expected deleted score=300, got %v", res.Rows[0]["score"])
	}
}

func TestPostgresSugar_LimitOffset(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sessID := "sess_pg_limit"

	_, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE items_lim (id INT PRIMARY KEY, val INT);")
	if err != nil {
		t.Fatalf("CREATE TABLE items_lim failed: %v", err)
	}
	_, err = coord.ExecuteSessionSQL(sessID, "INSERT INTO items_lim VALUES (1, 10), (2, 20), (3, 30), (4, 40), (5, 50);")
	if err != nil {
		t.Fatalf("INSERT into items_lim failed: %v", err)
	}

	// LIMIT 2 OFFSET 1
	res, err := coord.ExecuteSessionSQL(sessID, "SELECT id, val FROM items_lim ORDER BY id LIMIT 2 OFFSET 1;")
	if err != nil {
		t.Fatalf("SELECT LIMIT 2 OFFSET 1 failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}
	if res.Rows[0]["id"] != int64(2) && res.Rows[0]["id"] != 2 {
		t.Fatalf("expected first row id=2, got %v", res.Rows[0]["id"])
	}
	if res.Rows[1]["id"] != int64(3) && res.Rows[1]["id"] != 3 {
		t.Fatalf("expected second row id=3, got %v", res.Rows[1]["id"])
	}
}

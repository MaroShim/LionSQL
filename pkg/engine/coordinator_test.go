package engine_test

import (
	"context"
	"fmt"
	"testing"

	"sqlion/pkg/catalog"
	"sqlion/pkg/engine"
	"sqlion/pkg/storage/memengine"
)

func TestCoordinator_DirectExecutionLifecycle(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. Direct DDL
	createSQL := "CREATE TABLE accounts (id INT PRIMARY KEY, name VARCHAR(100), balance FLOAT);"
	res, err := coord.ExecuteSQL(createSQL)
	if err != nil || !res.Success {
		t.Fatalf("CREATE TABLE failed: %v (res: %+v)", err, res)
	}

	// 2. Direct DML
	insertSQL := "INSERT INTO accounts (id, name, balance) VALUES (1, 'Alice', 1500.5), (2, 'Bob', 2500.0);"
	res, err = coord.ExecuteSQL(insertSQL)
	if err != nil || !res.Success {
		t.Fatalf("INSERT failed: %v", err)
	}
	if res.RowsAffected != 2 {
		t.Fatalf("expected 2 rows affected, got %d", res.RowsAffected)
	}

	// 3. Direct DQL
	selectSQL := "SELECT id, name, balance FROM accounts WHERE balance > 2000 ORDER BY id ASC;"
	res, err = coord.ExecuteSQL(selectSQL)
	if err != nil || !res.Success {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if res.Rows[0]["name"] != "Bob" {
		t.Fatalf("expected 'Bob', got %v", res.Rows[0]["name"])
	}

	// 4. Session Transactions: Explicit Begin, Insert, Rollback
	sessID := "test-sess-1"
	if _, err := coord.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION;"); err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sessID, "INSERT INTO accounts VALUES (3, 'Charlie', 500.0);"); err != nil {
		t.Fatalf("Session INSERT failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sessID, "ROLLBACK;"); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// Verify Charlie is not present
	checkRes, err := coord.ExecuteSQL("SELECT * FROM accounts WHERE id = 3;")
	if err != nil || !checkRes.Success {
		t.Fatalf("verify query failed: %v", err)
	}
	if len(checkRes.Rows) != 0 {
		t.Fatalf("expected 0 rows after rollback, got %d", len(checkRes.Rows))
	}

	// 5. Context Cancellation
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately
	_, err = coord.ExecuteSessionSQLContext(ctx, "cancel-sess", "SELECT * FROM accounts;")
	if err == nil {
		t.Fatalf("expected error on cancelled context, got nil")
	}
}

func TestCoordinator_InsertIntoSelect(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. Setup Source Table
	_, err := coord.ExecuteSQL(`CREATE TABLE src_users (
		id INT PRIMARY KEY,
		username VARCHAR(50),
		score INT,
		active INT
	);`)
	if err != nil {
		t.Fatalf("CREATE src_users failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`INSERT INTO src_users (id, username, score, active) VALUES
		(1, 'Alice', 95, 1),
		(2, 'Bob', 80, 1),
		(3, 'Charlie', 40, 0),
		(4, 'David', 88, 1);`)
	if err != nil {
		t.Fatalf("INSERT src_users failed: %v", err)
	}

	// 2. INSERT INTO target (cols...) SELECT ... WHERE active = 1
	_, err = coord.ExecuteSQL(`CREATE TABLE dst_active (
		user_id INT PRIMARY KEY,
		user_name VARCHAR(50),
		user_score INT
	);`)
	if err != nil {
		t.Fatalf("CREATE dst_active failed: %v", err)
	}

	res, err := coord.ExecuteSQL(`INSERT INTO dst_active (user_id, user_name, user_score)
		SELECT id, username, score FROM src_users WHERE active = 1;`)
	if err != nil || !res.Success {
		t.Fatalf("INSERT INTO ... SELECT failed: %v (res: %+v)", err, res)
	}
	if res.RowsAffected != 3 {
		t.Fatalf("expected 3 rows affected, got %d", res.RowsAffected)
	}

	chk, err := coord.ExecuteSQL(`SELECT * FROM dst_active ORDER BY user_id ASC;`)
	if err != nil || len(chk.Rows) != 3 {
		t.Fatalf("expected 3 rows in dst_active, got %d (err: %v)", len(chk.Rows), err)
	}

	// 3. Whole table copy: INSERT INTO dst_all SELECT * FROM src_users
	_, err = coord.ExecuteSQL(`CREATE TABLE dst_all (
		id INT PRIMARY KEY,
		username VARCHAR(50),
		score INT,
		active INT
	);`)
	if err != nil {
		t.Fatalf("CREATE dst_all failed: %v", err)
	}

	res, err = coord.ExecuteSQL(`INSERT INTO dst_all SELECT * FROM src_users;`)
	if err != nil || !res.Success {
		t.Fatalf("INSERT INTO dst_all SELECT * failed: %v", err)
	}
	if res.RowsAffected != 4 {
		t.Fatalf("expected 4 rows affected, got %d", res.RowsAffected)
	}

	// 4. Empty Result Set Copy (0 rows affected)
	res, err = coord.ExecuteSQL(`INSERT INTO dst_active (user_id, user_name, user_score)
		SELECT id, username, score FROM src_users WHERE score > 999;`)
	if err != nil || !res.Success {
		t.Fatalf("INSERT INTO ... SELECT empty failed: %v", err)
	}
	if res.RowsAffected != 0 {
		t.Fatalf("expected 0 rows affected, got %d", res.RowsAffected)
	}

	// 5. Column Count Mismatch Error
	_, err = coord.ExecuteSQL(`INSERT INTO dst_active (user_id, user_name)
		SELECT id, username, score FROM src_users;`)
	if err == nil {
		t.Fatalf("expected error on column count mismatch, got nil")
	}

	// 6. Identity Column Handling
	_, err = coord.ExecuteSQL(`CREATE TABLE dst_identity (
		seq_id INT IDENTITY PRIMARY KEY,
		info VARCHAR(50)
	);`)
	if err != nil {
		t.Fatalf("CREATE dst_identity failed: %v", err)
	}

	// 6a. Auto-generate identity: omit identity column in target
	res, err = coord.ExecuteSQL(`INSERT INTO dst_identity (info)
		SELECT username FROM src_users WHERE active = 1 ORDER BY id ASC;`)
	if err != nil || !res.Success {
		t.Fatalf("INSERT INTO dst_identity auto failed: %v", err)
	}
	if res.RowsAffected != 3 {
		t.Fatalf("expected 3 rows affected, got %d", res.RowsAffected)
	}

	idChk, err := coord.ExecuteSQL(`SELECT * FROM dst_identity ORDER BY seq_id ASC;`)
	if err != nil || len(idChk.Rows) != 3 {
		t.Fatalf("expected 3 rows in dst_identity, got %d", len(idChk.Rows))
	}
	if idChk.Rows[0]["seq_id"] != int64(1) || idChk.Rows[2]["seq_id"] != int64(3) {
		t.Fatalf("unexpected generated identity seq_ids: %+v", idChk.Rows)
	}

	// 6b. Attempt explicit identity when IDENTITY_INSERT is OFF -> should fail
	_, err = coord.ExecuteSQL(`INSERT INTO dst_identity (seq_id, info)
		SELECT id, username FROM src_users;`)
	if err == nil {
		t.Fatalf("expected error when inserting explicit identity without IDENTITY_INSERT ON, got nil")
	}

	// 6c. With SET IDENTITY_INSERT ON
	sessID := "id-insert-sess"
	if _, err := coord.ExecuteSessionSQL(sessID, `SET IDENTITY_INSERT dst_identity ON;`); err != nil {
		t.Fatalf("SET IDENTITY_INSERT ON failed: %v", err)
	}
	res, err = coord.ExecuteSessionSQL(sessID, `INSERT INTO dst_identity (seq_id, info)
		SELECT id + 10, username FROM src_users WHERE id = 1;`)
	if err != nil || !res.Success {
		t.Fatalf("INSERT with IDENTITY_INSERT ON failed: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("expected 1 row affected, got %d", res.RowsAffected)
	}
	if _, err := coord.ExecuteSessionSQL(sessID, `SET IDENTITY_INSERT dst_identity OFF;`); err != nil {
		t.Fatalf("SET IDENTITY_INSERT OFF failed: %v", err)
	}

	// 7. Temporary Table Support
	tempSess := "temp-sess"
	if _, err := coord.ExecuteSessionSQL(tempSess, `CREATE TABLE #temp_users (uid INT PRIMARY KEY, uname VARCHAR(50));`); err != nil {
		t.Fatalf("CREATE #temp_users failed: %v", err)
	}
	res, err = coord.ExecuteSessionSQL(tempSess, `INSERT INTO #temp_users (uid, uname)
		SELECT id, username FROM src_users WHERE score > 80;`)
	if err != nil || !res.Success {
		t.Fatalf("INSERT INTO #temp_users failed: %v", err)
	}
	if res.RowsAffected != 2 {
		t.Fatalf("expected 2 rows in #temp_users, got %d", res.RowsAffected)
	}

	// 8. ON CONFLICT DO UPDATE
	_, err = coord.ExecuteSQL(`INSERT INTO dst_active (user_id, user_name, user_score)
		SELECT id, username, score + 10 FROM src_users WHERE active = 1
		ON CONFLICT (user_id) DO UPDATE SET user_score = EXCLUDED.user_score;`)
	if err != nil {
		t.Fatalf("INSERT INTO ... ON CONFLICT DO UPDATE failed: %v", err)
	}
	chkUpd, err := coord.ExecuteSQL(`SELECT user_score FROM dst_active WHERE user_id = 1;`)
	if err != nil || len(chkUpd.Rows) != 1 {
		t.Fatalf("SELECT dst_active user_id=1 failed: %v", err)
	}
	if chkUpd.Rows[0]["user_score"] != int64(105) {
		t.Fatalf("expected updated score 105, got %v", chkUpd.Rows[0]["user_score"])
	}

	// 9. OUTPUT Clause with INSERT INTO ... SELECT
	_, err = coord.ExecuteSQL(`CREATE TABLE dst_output (id INT PRIMARY KEY, name VARCHAR(50));`)
	if err != nil {
		t.Fatalf("CREATE dst_output failed: %v", err)
	}
	outRes, err := coord.ExecuteSQL(`INSERT INTO dst_output (id, name)
		OUTPUT INSERTED.id, INSERTED.name
		SELECT id, username FROM src_users WHERE id <= 2;`)
	if err != nil || !outRes.Success {
		t.Fatalf("INSERT OUTPUT failed: %v", err)
	}
	if len(outRes.Rows) != 2 {
		t.Fatalf("expected 2 output rows, got %d", len(outRes.Rows))
	}
}

func TestCoordinator_DerivedTables(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. Setup base tables
	_, err := coord.ExecuteSQL(`CREATE TABLE employees (id INT PRIMARY KEY, name VARCHAR(50), dept VARCHAR(20), salary FLOAT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE employees failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO employees VALUES
		(1, 'Alice', 'Engineering', 4000.0),
		(2, 'Bob', 'Engineering', 3500.0),
		(3, 'Charlie', 'Sales', 2000.0),
		(4, 'Diana', 'Sales', 2500.0);`)
	if err != nil {
		t.Fatalf("INSERT employees failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`CREATE TABLE projects (id INT PRIMARY KEY, emp_id INT, name VARCHAR(50), budget FLOAT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE projects failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO projects VALUES
		(101, 1, 'Alpha', 50000.0),
		(102, 1, 'Beta', 30000.0),
		(103, 3, 'Gamma', 15000.0);`)
	if err != nil {
		t.Fatalf("INSERT projects failed: %v", err)
	}

	// 2. Simple FROM derived table
	res, err := coord.ExecuteSQL(`SELECT t.id, t.name, t.salary FROM (SELECT id, name, salary FROM employees WHERE salary > 2000) AS t WHERE t.salary < 4000;`)
	if err != nil || !res.Success {
		t.Fatalf("FROM derived table failed: %v (res: %+v)", err, res)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %+v", len(res.Rows), res.Rows)
	}

	// 3. Derived table with SELECT *
	res, err = coord.ExecuteSQL(`SELECT * FROM (SELECT id, name FROM employees WHERE dept = 'Engineering') AS t ORDER BY id ASC;`)
	if err != nil || !res.Success {
		t.Fatalf("SELECT * FROM derived table failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}
	if res.Rows[0]["name"] != "Alice" || res.Rows[1]["name"] != "Bob" {
		t.Fatalf("unexpected rows: %+v", res.Rows)
	}

	// 4. Derived table with aggregation
	res, err = coord.ExecuteSQL(`SELECT t.dept, t.total FROM (SELECT dept, SUM(salary) AS total FROM employees GROUP BY dept) AS t WHERE t.total > 5000;`)
	if err != nil || !res.Success {
		t.Fatalf("derived table with aggregation failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %+v", len(res.Rows), res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["dept"]) != "Engineering" {
		t.Fatalf("expected Engineering, got %v", res.Rows[0]["dept"])
	}

	// 5. JOIN with derived table
	res, err = coord.ExecuteSQL(`SELECT e.name, p.total_budget FROM employees e JOIN (SELECT emp_id, SUM(budget) AS total_budget FROM projects GROUP BY emp_id) AS p ON e.id = p.emp_id ORDER BY e.id ASC;`)
	if err != nil || !res.Success {
		t.Fatalf("JOIN with derived table failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %+v", len(res.Rows), res.Rows)
	}
	if res.Rows[0]["name"] != "Alice" || res.Rows[1]["name"] != "Charlie" {
		t.Fatalf("unexpected joined rows: %+v", res.Rows)
	}

	// 6. Nested derived tables
	res, err = coord.ExecuteSQL(`SELECT sub2.id, sub2.name FROM (SELECT sub1.id, sub1.name FROM (SELECT id, name FROM employees WHERE id <= 2) AS sub1) AS sub2;`)
	if err != nil || !res.Success {
		t.Fatalf("nested derived tables failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows from nested derived tables, got %d", len(res.Rows))
	}

	// 7. EXPLAIN on derived table query
	expRes, err := coord.ExecuteSQL(`EXPLAIN SELECT * FROM (SELECT id, name FROM employees) AS t;`)
	if err != nil || !expRes.Success {
		t.Fatalf("EXPLAIN derived table failed: %v", err)
	}
	if len(expRes.Rows) == 0 {
		t.Fatalf("expected EXPLAIN rows, got 0")
	}
}

func TestCoordinator_WhereExists(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. Setup base tables
	_, err := coord.ExecuteSQL(`CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(50), active INT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE users failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO users VALUES
		(1, 'Alice', 1),
		(2, 'Bob', 1),
		(3, 'Charlie', 0);`)
	if err != nil {
		t.Fatalf("INSERT users failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`CREATE TABLE orders (id INT PRIMARY KEY, user_id INT, amount FLOAT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE orders failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO orders VALUES
		(101, 1, 150.0),
		(102, 1, 600.0),
		(103, 2, 200.0);`)
	if err != nil {
		t.Fatalf("INSERT orders failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`CREATE TABLE settings (k VARCHAR(20) PRIMARY KEY, val INT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE settings failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO settings VALUES ('enabled', 1);`)
	if err != nil {
		t.Fatalf("INSERT settings failed: %v", err)
	}

	// 2. Correlated WHERE EXISTS (Semi-Join)
	res, err := coord.ExecuteSQL(`SELECT u.name FROM users u WHERE EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id) ORDER BY u.name ASC;`)
	if err != nil || !res.Success {
		t.Fatalf("correlated EXISTS failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %+v", len(res.Rows), res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["name"]) != "Alice" || fmt.Sprintf("%v", res.Rows[1]["name"]) != "Bob" {
		t.Fatalf("expected [Alice, Bob], got %+v", res.Rows)
	}

	// 3. Correlated WHERE NOT EXISTS (Anti-Join)
	res, err = coord.ExecuteSQL(`SELECT u.name FROM users u WHERE NOT EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id);`)
	if err != nil || !res.Success {
		t.Fatalf("correlated NOT EXISTS failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %+v", len(res.Rows), res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["name"]) != "Charlie" {
		t.Fatalf("expected Charlie, got %v", res.Rows[0]["name"])
	}

	// 4. Correlated EXISTS with additional filters
	res, err = coord.ExecuteSQL(`SELECT u.name FROM users u WHERE EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id AND o.amount > 500);`)
	if err != nil || !res.Success {
		t.Fatalf("correlated EXISTS with filter failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %+v", len(res.Rows), res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["name"]) != "Alice" {
		t.Fatalf("expected Alice, got %v", res.Rows[0]["name"])
	}

	// 5. Uncorrelated WHERE EXISTS
	res, err = coord.ExecuteSQL(`SELECT u.name FROM users u WHERE EXISTS (SELECT 1 FROM settings WHERE k = 'enabled' AND val = 1) ORDER BY u.name ASC;`)
	if err != nil || !res.Success {
		t.Fatalf("uncorrelated EXISTS failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected all 3 users when uncorrelated EXISTS is true, got %d", len(res.Rows))
	}

	// 6. Uncorrelated EXISTS false
	res, err = coord.ExecuteSQL(`SELECT u.name FROM users u WHERE EXISTS (SELECT 1 FROM settings WHERE k = 'disabled');`)
	if err != nil || !res.Success {
		t.Fatalf("uncorrelated EXISTS false query failed: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows when uncorrelated EXISTS is false, got %d", len(res.Rows))
	}
}



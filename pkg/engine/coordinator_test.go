package engine_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/proc"
	"lionsql/pkg/replication"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
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

	// 6d. Next auto-identity insert should increment from cached maxID (11 -> 12)
	res, err = coord.ExecuteSQL(`INSERT INTO dst_identity (info) VALUES ('cached_auto');`)
	if err != nil || !res.Success {
		t.Fatalf("INSERT cached_auto failed: %v", err)
	}
	idChk2, err := coord.ExecuteSQL(`SELECT seq_id FROM dst_identity WHERE info = 'cached_auto';`)
	if err != nil || len(idChk2.Rows) != 1 || idChk2.Rows[0]["seq_id"] != int64(12) {
		t.Fatalf("expected seq_id=12 for cached_auto, got: %+v", idChk2.Rows)
	}

	// 6e. TRUNCATE TABLE resets identity counter to 0 (VPERF-3)
	if _, err := coord.ExecuteSQL(`TRUNCATE TABLE dst_identity;`); err != nil {
		t.Fatalf("TRUNCATE TABLE dst_identity failed: %v", err)
	}
	res, err = coord.ExecuteSQL(`INSERT INTO dst_identity (info) VALUES ('first_after_truncate');`)
	if err != nil || !res.Success {
		t.Fatalf("INSERT after truncate failed: %v", err)
	}
	idChk3, err := coord.ExecuteSQL(`SELECT seq_id FROM dst_identity WHERE info = 'first_after_truncate';`)
	if err != nil || len(idChk3.Rows) != 1 || idChk3.Rows[0]["seq_id"] != int64(1) {
		t.Fatalf("expected seq_id=1 after truncate, got: %+v", idChk3.Rows)
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

	// Verify idempotent re-creation of session-scoped temp table
	if _, err := coord.ExecuteSessionSQL(tempSess, `CREATE TABLE #temp_users (uid INT PRIMARY KEY, uname VARCHAR(50));`); err != nil {
		t.Fatalf("Idempotent CREATE #temp_users re-creation failed: %v", err)
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

func setupTestCoordinator(t *testing.T) (*engine.Coordinator, *catalog.Catalog, func()) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	cleanup := func() {
		coord.Close()
		eng.Close()
	}
	return coord, cat, cleanup
}

func TestCoordinator_SessionIsolatedTempTables(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	if _, err := coord.GetOrCreateSession("sess_a"); err != nil {
		t.Fatalf("CreateSession A failed: %v", err)
	}
	defer coord.CloseSession("sess_a")

	if _, err := coord.GetOrCreateSession("sess_b"); err != nil {
		t.Fatalf("CreateSession B failed: %v", err)
	}
	defer coord.CloseSession("sess_b")

	// Session A creates #data
	if _, err := coord.ExecuteSessionSQL("sess_a", `CREATE TABLE #data (id INT PRIMARY KEY, val VARCHAR(50));`); err != nil {
		t.Fatalf("Session A CREATE #data failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL("sess_a", `INSERT INTO #data VALUES (1, 'from A'), (2, 'from A');`); err != nil {
		t.Fatalf("Session A INSERT #data failed: %v", err)
	}

	// Session B creates #data simultaneously (no collision!)
	if _, err := coord.ExecuteSessionSQL("sess_b", `CREATE TABLE #data (id INT PRIMARY KEY, val VARCHAR(50));`); err != nil {
		t.Fatalf("Session B CREATE #data failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL("sess_b", `INSERT INTO #data VALUES (10, 'from B'), (20, 'from B'), (30, 'from B');`); err != nil {
		t.Fatalf("Session B INSERT #data failed: %v", err)
	}

	// Verify Session A sees only its 2 rows
	resA, err := coord.ExecuteSessionSQL("sess_a", `SELECT * FROM #data;`)
	if err != nil || !resA.Success {
		t.Fatalf("Session A SELECT failed: %v", err)
	}
	if len(resA.Rows) != 2 {
		t.Fatalf("expected 2 rows in Session A, got %d", len(resA.Rows))
	}

	// Verify Session B sees only its 3 rows
	resB, err := coord.ExecuteSessionSQL("sess_b", `SELECT * FROM #data;`)
	if err != nil || !resB.Success {
		t.Fatalf("Session B SELECT failed: %v", err)
	}
	if len(resB.Rows) != 3 {
		t.Fatalf("expected 3 rows in Session B, got %d", len(resB.Rows))
	}

	// Session A drops its #data
	if _, err := coord.ExecuteSessionSQL("sess_a", `DROP TABLE #data;`); err != nil {
		t.Fatalf("Session A DROP #data failed: %v", err)
	}

	// Session B still has its #data intact
	resB2, err := coord.ExecuteSessionSQL("sess_b", `SELECT * FROM #data;`)
	if err != nil || !resB2.Success {
		t.Fatalf("Session B SELECT after A drop failed: %v", err)
	}
	if len(resB2.Rows) != 3 {
		t.Fatalf("expected 3 rows in Session B after A drop, got %d", len(resB2.Rows))
	}
}

func TestCoordinator_Apply_CrossAndOuter(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	_, err := coord.ExecuteSQL(`CREATE TABLE depts (dept_id INT PRIMARY KEY, dept_name VARCHAR(50));`)
	if err != nil {
		t.Fatalf("CREATE TABLE depts failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO depts VALUES
		(1, 'Engineering'),
		(2, 'Sales'),
		(3, 'EmptyDept');`)
	if err != nil {
		t.Fatalf("INSERT depts failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`CREATE TABLE emps (id INT PRIMARY KEY, dept_id INT, name VARCHAR(50), salary FLOAT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE emps failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO emps VALUES
		(101, 1, 'Alice', 100000.0),
		(102, 1, 'Bob', 90000.0),
		(103, 2, 'Charlie', 80000.0);`)
	if err != nil {
		t.Fatalf("INSERT emps failed: %v", err)
	}

	// 1. CROSS APPLY: TOP 1 highest paid per department (EmptyDept omitted)
	resCross, err := coord.ExecuteSQL(`SELECT d.dept_name, ca.name, ca.salary FROM depts d CROSS APPLY (SELECT TOP 1 name, salary FROM emps e WHERE e.dept_id = d.dept_id ORDER BY salary DESC) AS ca ORDER BY d.dept_name ASC;`)
	if err != nil || !resCross.Success {
		t.Fatalf("CROSS APPLY failed: %v", err)
	}
	if len(resCross.Rows) != 2 {
		t.Fatalf("expected 2 rows for CROSS APPLY, got %d: %+v", len(resCross.Rows), resCross.Rows)
	}
	if fmt.Sprintf("%v", resCross.Rows[0]["dept_name"]) != "Engineering" || fmt.Sprintf("%v", resCross.Rows[0]["name"]) != "Alice" {
		t.Errorf("unexpected row 0: %+v", resCross.Rows[0])
	}
	if fmt.Sprintf("%v", resCross.Rows[1]["dept_name"]) != "Sales" || fmt.Sprintf("%v", resCross.Rows[1]["name"]) != "Charlie" {
		t.Errorf("unexpected row 1: %+v", resCross.Rows[1])
	}

	// 2. OUTER APPLY: TOP 1 highest paid per department (EmptyDept preserved with NULLs)
	resOuter, err := coord.ExecuteSQL(`SELECT d.dept_name, oa.name, oa.salary FROM depts d OUTER APPLY (SELECT TOP 1 name, salary FROM emps e WHERE e.dept_id = d.dept_id ORDER BY salary DESC) AS oa ORDER BY d.dept_name ASC;`)
	if err != nil || !resOuter.Success {
		t.Fatalf("OUTER APPLY failed: %v", err)
	}
	if len(resOuter.Rows) != 3 {
		t.Fatalf("expected 3 rows for OUTER APPLY, got %d: %+v", len(resOuter.Rows), resOuter.Rows)
	}
	if fmt.Sprintf("%v", resOuter.Rows[0]["dept_name"]) != "EmptyDept" || resOuter.Rows[0]["name"] != nil {
		t.Errorf("expected EmptyDept with nil name, got %+v", resOuter.Rows[0])
	}

	// 3. EXPLAIN on APPLY
	resExp, err := coord.ExecuteSQL(`EXPLAIN SELECT d.dept_name, ca.name FROM depts d CROSS APPLY (SELECT name FROM emps e WHERE e.dept_id = d.dept_id) AS ca;`)
	if err != nil || !resExp.Success {
		t.Fatalf("EXPLAIN APPLY failed: %v", err)
	}
	if len(resExp.Rows) == 0 {
		t.Errorf("expected non-empty explain rows")
	}
}

func TestCoordinator_TableAndQueryHints(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	_, err := coord.ExecuteSQL(`CREATE TABLE items (id INT PRIMARY KEY, name VARCHAR(50), price FLOAT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE items failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO items VALUES (1, 'Pen', 1.5), (2, 'Notebook', 4.5);`)
	if err != nil {
		t.Fatalf("INSERT items failed: %v", err)
	}

	// WITH (NOLOCK) and OPTION (RECOMPILE)
	res, err := coord.ExecuteSQL(`SELECT * FROM items WITH (NOLOCK) WHERE id = 1 OPTION (RECOMPILE);`)
	if err != nil || !res.Success {
		t.Fatalf("SELECT with NOLOCK and RECOMPILE failed: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["name"]) != "Pen" {
		t.Fatalf("expected Pen, got %+v", res.Rows)
	}

	// WITH (INDEX(0)) - Force Table Scan
	res, err = coord.ExecuteSQL(`SELECT * FROM items WITH (INDEX(0)) WHERE id = 1;`)
	if err != nil || !res.Success {
		t.Fatalf("SELECT with INDEX(0) failed: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["name"]) != "Pen" {
		t.Fatalf("expected Pen with INDEX(0), got %+v", res.Rows)
	}

	// Secondary Index & WITH (INDEX(idx_items_name))
	_, err = coord.ExecuteSQL(`CREATE INDEX idx_items_name ON items (name);`)
	if err != nil {
		t.Fatalf("CREATE INDEX failed: %v", err)
	}
	res, err = coord.ExecuteSQL(`SELECT * FROM items WITH (INDEX(idx_items_name)) WHERE name = 'Notebook';`)
	if err != nil || !res.Success {
		t.Fatalf("SELECT with INDEX(idx_items_name) failed: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["name"]) != "Notebook" {
		t.Fatalf("expected Notebook with INDEX(idx_items_name), got %+v", res.Rows)
	}

	// Covering Index with INCLUDE
	_, err = coord.ExecuteSQL(`CREATE INDEX idx_items_covering ON items (name) INCLUDE (price);`)
	if err != nil {
		t.Fatalf("CREATE INDEX with INCLUDE failed: %v", err)
	}
	res, err = coord.ExecuteSQL(`SELECT name, price FROM items WHERE name = 'Notebook';`)
	if err != nil || !res.Success {
		t.Fatalf("SELECT with covering index failed: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["name"]) != "Notebook" {
		t.Fatalf("expected Notebook with covering index, got %+v", res.Rows)
	}

	// Comma-separated hints: WITH (NOLOCK, INDEX(0))
	res, err = coord.ExecuteSQL(`SELECT * FROM items WITH (NOLOCK, INDEX(0)) WHERE id = 2;`)
	if err != nil || !res.Success {
		t.Fatalf("SELECT with comma-separated hints failed: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["name"]) != "Notebook" {
		t.Fatalf("expected Notebook with NOLOCK, INDEX(0), got %+v", res.Rows)
	}

	// Joins with query hints and inline join hints
	_, err = coord.ExecuteSQL(`CREATE TABLE categories (id INT PRIMARY KEY, item_id INT, cat VARCHAR(50));`)
	if err != nil {
		t.Fatalf("CREATE TABLE categories failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO categories VALUES (10, 1, 'Stationery'), (20, 2, 'Office');`)
	if err != nil {
		t.Fatalf("INSERT categories failed: %v", err)
	}

	// OPTION (FORCE ORDER)
	res, err = coord.ExecuteSQL(`SELECT items.name, categories.cat FROM items JOIN categories ON items.id = categories.item_id OPTION (FORCE ORDER);`)
	if err != nil || !res.Success {
		t.Fatalf("SELECT with OPTION (FORCE ORDER) failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows with FORCE ORDER, got %d", len(res.Rows))
	}

	// OPTION (HASH JOIN)
	res, err = coord.ExecuteSQL(`SELECT items.name, categories.cat FROM items JOIN categories ON items.id = categories.item_id OPTION (HASH JOIN);`)
	if err != nil || !res.Success {
		t.Fatalf("SELECT with OPTION (HASH JOIN) failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows with HASH JOIN, got %d", len(res.Rows))
	}

	// Inline join hint: INNER HASH JOIN
	res, err = coord.ExecuteSQL(`SELECT items.name, categories.cat FROM items INNER HASH JOIN categories ON items.id = categories.item_id;`)
	if err != nil || !res.Success {
		t.Fatalf("SELECT with INNER HASH JOIN failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows with INNER HASH JOIN, got %d", len(res.Rows))
	}

	// OPTION (MERGE JOIN)
	res, err = coord.ExecuteSQL(`SELECT items.name, categories.cat FROM items JOIN categories ON items.id = categories.item_id OPTION (MERGE JOIN);`)
	if err != nil || !res.Success {
		t.Fatalf("SELECT with OPTION (MERGE JOIN) failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows with OPTION (MERGE JOIN), got %d", len(res.Rows))
	}

	// Inline join hint: INNER MERGE JOIN
	res, err = coord.ExecuteSQL(`SELECT items.name, categories.cat FROM items INNER MERGE JOIN categories ON items.id = categories.item_id;`)
	if err != nil || !res.Success {
		t.Fatalf("SELECT with INNER MERGE JOIN failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows with INNER MERGE JOIN, got %d", len(res.Rows))
	}
}

func TestCoordinator_DML_Output(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	_, err := coord.ExecuteSQL(`CREATE TABLE accounts (id INT PRIMARY KEY, balance FLOAT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE accounts failed: %v", err)
	}

	// 1. INSERT ... OUTPUT
	resIns, err := coord.ExecuteSQL(`INSERT INTO accounts (id, balance) OUTPUT INSERTED.id, INSERTED.balance VALUES (10, 500.0);`)
	if err != nil || !resIns.Success {
		t.Fatalf("INSERT OUTPUT failed: %v", err)
	}
	if len(resIns.Rows) != 1 {
		t.Fatalf("expected 1 row in INSERT OUTPUT, got %d: %+v", len(resIns.Rows), resIns.Rows)
	}
	if fmt.Sprintf("%v", resIns.Rows[0]["id"]) != "10" {
		t.Errorf("expected id=10, got %v", resIns.Rows[0]["id"])
	}

	// 2. UPDATE ... OUTPUT
	resUpd, err := coord.ExecuteSQL(`UPDATE accounts SET balance = 750.0 OUTPUT DELETED.balance, INSERTED.balance WHERE id = 10;`)
	if err != nil || !resUpd.Success {
		t.Fatalf("UPDATE OUTPUT failed: %v", err)
	}
	if len(resUpd.Rows) != 1 {
		t.Fatalf("expected 1 row in UPDATE OUTPUT, got %d: %+v", len(resUpd.Rows), resUpd.Rows)
	}

	// 3. DELETE ... OUTPUT
	resDel, err := coord.ExecuteSQL(`DELETE FROM accounts OUTPUT DELETED.id WHERE id = 10;`)
	if err != nil || !resDel.Success {
		t.Fatalf("DELETE OUTPUT failed: %v", err)
	}
	if len(resDel.Rows) != 1 {
		t.Fatalf("expected 1 row in DELETE OUTPUT, got %d: %+v", len(resDel.Rows), resDel.Rows)
	}
	if fmt.Sprintf("%v", resDel.Rows[0]["id"]) != "10" {
		t.Errorf("expected id=10 in DELETE OUTPUT, got %v", resDel.Rows[0]["id"])
	}
}

func TestCoordinator_FullAndRightOuterJoin(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	if _, err := coord.ExecuteSQL(`CREATE TABLE departments (id INT PRIMARY KEY, name VARCHAR(50));`); err != nil {
		t.Fatalf("CREATE TABLE departments failed: %v", err)
	}
	if _, err := coord.ExecuteSQL(`CREATE TABLE employees (id INT PRIMARY KEY, name VARCHAR(50), dept_id INT);`); err != nil {
		t.Fatalf("CREATE TABLE employees failed: %v", err)
	}

	if _, err := coord.ExecuteSQL(`INSERT INTO departments (id, name) VALUES (10, 'Engineering'), (20, 'HR'), (30, 'Finance');`); err != nil {
		t.Fatalf("INSERT departments failed: %v", err)
	}
	if _, err := coord.ExecuteSQL(`INSERT INTO employees (id, name, dept_id) VALUES (1, 'Alice', 10), (2, 'Bob', 20), (3, 'Charlie', 40);`); err != nil {
		t.Fatalf("INSERT employees failed: %v", err)
	}

	// 1. FULL OUTER JOIN
	resFull, err := coord.ExecuteSQL(`SELECT d.id AS did, d.name AS dname, e.id AS eid, e.name AS ename FROM departments d FULL OUTER JOIN employees e ON d.id = e.dept_id;`)
	if err != nil || !resFull.Success {
		t.Fatalf("FULL OUTER JOIN failed: %v", err)
	}
	if len(resFull.Rows) != 4 {
		t.Fatalf("expected 4 rows in FULL OUTER JOIN, got %d: %+v", len(resFull.Rows), resFull.Rows)
	}

	var hasMatchedEng, hasMatchedHR, hasUnmatchedFinance, hasUnmatchedCharlie bool
	for _, row := range resFull.Rows {
		dname := fmt.Sprintf("%v", row["dname"])
		ename := fmt.Sprintf("%v", row["ename"])
		if dname == "Engineering" && ename == "Alice" {
			hasMatchedEng = true
		} else if dname == "HR" && ename == "Bob" {
			hasMatchedHR = true
		} else if dname == "Finance" && (row["ename"] == nil || ename == "<nil>") {
			hasUnmatchedFinance = true
		} else if (row["dname"] == nil || dname == "<nil>") && ename == "Charlie" {
			hasUnmatchedCharlie = true
		}
	}
	if !hasMatchedEng || !hasMatchedHR || !hasUnmatchedFinance || !hasUnmatchedCharlie {
		t.Errorf("FULL OUTER JOIN row verification failed: Eng=%v, HR=%v, Finance=%v, Charlie=%v (rows: %+v)",
			hasMatchedEng, hasMatchedHR, hasUnmatchedFinance, hasUnmatchedCharlie, resFull.Rows)
	}

	// 2. RIGHT OUTER JOIN
	resRight, err := coord.ExecuteSQL(`SELECT d.name AS dname, e.id AS eid, e.name AS ename FROM departments d RIGHT OUTER JOIN employees e ON d.id = e.dept_id;`)
	if err != nil || !resRight.Success {
		t.Fatalf("RIGHT OUTER JOIN failed: %v", err)
	}
	if len(resRight.Rows) != 3 {
		t.Fatalf("expected 3 rows in RIGHT OUTER JOIN, got %d: %+v", len(resRight.Rows), resRight.Rows)
	}
	hasCharlie := false
	for _, row := range resRight.Rows {
		if fmt.Sprintf("%v", row["ename"]) == "Charlie" {
			hasCharlie = true
			if row["dname"] != nil && fmt.Sprintf("%v", row["dname"]) != "<nil>" {
				t.Errorf("expected NULL department for Charlie, got %v", row["dname"])
			}
		}
	}
	if !hasCharlie {
		t.Errorf("expected Charlie in RIGHT JOIN results, got: %+v", resRight.Rows)
	}
}

func TestCoordinator_CoalesceAndIsNull(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	if _, err := coord.ExecuteSQL(`CREATE TABLE items (id INT PRIMARY KEY, title VARCHAR(50), price FLOAT);`); err != nil {
		t.Fatalf("CREATE TABLE items failed: %v", err)
	}
	if _, err := coord.ExecuteSQL(`INSERT INTO items (id, title, price) VALUES (1, 'Book', 15.0), (2, 'Freebie', NULL);`); err != nil {
		t.Fatalf("INSERT items failed: %v", err)
	}

	// 1. ISNULL on column with NULL
	res, err := coord.ExecuteSQL(`SELECT id, ISNULL(price, 0.0) AS final_price FROM items ORDER BY id ASC;`)
	if err != nil || !res.Success {
		t.Fatalf("SELECT ISNULL failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}
	if fmt.Sprintf("%v", res.Rows[0]["final_price"]) != "15" {
		t.Errorf("expected 15 for row 0, got %v", res.Rows[0]["final_price"])
	}
	if fmt.Sprintf("%v", res.Rows[1]["final_price"]) != "0" {
		t.Errorf("expected 0 for row 1, got %v", res.Rows[1]["final_price"])
	}

	// 2. COALESCE multi-argument fallback
	resCoal, err := coord.ExecuteSQL(`SELECT id, COALESCE(price, 99.9) AS c_price FROM items WHERE id = 2;`)
	if err != nil || !resCoal.Success {
		t.Fatalf("SELECT COALESCE failed: %v", err)
	}
	if fmt.Sprintf("%v", resCoal.Rows[0]["c_price"]) != "99.9" {
		t.Errorf("expected 99.9 for row 0, got %v", resCoal.Rows[0]["c_price"])
	}

	// 3. Constant scalar COALESCE & ISNULL without FROM
	resConst, err := coord.ExecuteSQL(`SELECT COALESCE(NULL, NULL, 'ThirdVal', 'FourthVal') AS val;`)
	if err != nil || !resConst.Success {
		t.Fatalf("SELECT constant COALESCE failed: %v", err)
	}
	if fmt.Sprintf("%v", resConst.Rows[0]["val"]) != "ThirdVal" {
		t.Errorf("expected 'ThirdVal', got %v", resConst.Rows[0]["val"])
	}
}

func TestCoordinator_BeekeeperDbGateTableQueries(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	coord.ExecuteSQL("CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(100), email VARCHAR(100), balance FLOAT);")
	coord.ExecuteSQL("INSERT INTO users VALUES (1, 'Alice', 'alice@test.com', 100.0), (2, 'Bob', 'bob@test.com', 200.0);")

	queries := []string{
		"SELECT * FROM users;",
		"SELECT * FROM \"users\" LIMIT 50;",
		"SELECT * FROM \"public\".\"users\" LIMIT 50;",
		"SELECT \"basetbl\".* FROM \"public\".\"users\" \"basetbl\" LIMIT 50 OFFSET 0;",
		"SELECT \"basetbl\".* FROM \"public\".\"users\" AS \"basetbl\" LIMIT 50 OFFSET 0;",
		"SELECT \"basetbl\".* FROM \"public\".\"users\" \"basetbl\" ORDER BY \"basetbl\".\"id\" ASC LIMIT 50 OFFSET 0;",
		"SELECT \"basetbl\".* FROM \"public\".\"users\" \"basetbl\" ORDER BY \"id\" ASC LIMIT 50 OFFSET 0;",
		"SELECT \"t\".* FROM \"public\".\"users\" \"t\" LIMIT 100;",
		"SELECT TOP 50 * FROM [dbo].[users];",
		"SELECT TOP 50 [dbo].[users].* FROM [dbo].[users];",
		"SELECT TOP 50 [t].* FROM [dbo].[users] [t];",
		"SELECT TOP 50 [basetbl].* FROM [dbo].[users] AS [basetbl];",
		"SELECT TOP 50 [basetbl].* FROM [dbo].[users] AS [basetbl] ORDER BY [basetbl].[id] ASC;",
		"SELECT TOP 50 [t].* FROM [dbo].[users] [t] ORDER BY [t].[id] ASC;",
		"SELECT [users].* FROM [dbo].[users];",
		"SELECT users.* FROM users;",
		"SELECT \"users\".* FROM \"users\";",
		"SELECT \"public\".\"users\".* FROM \"public\".\"users\";",
		`SELECT
  [basetbl].[id] AS [id],
  [basetbl].[name] AS [name],
  [basetbl].[balance] AS [balance]
  FROM [dbo].[users] [basetbl]
ORDER BY [id] ASC
OFFSET 0 ROWS FETCH NEXT 100 ROWS ONLY`,
	}

	for _, q := range queries {
		res, err := coord.ExecuteSQL(q)
		if err != nil {
			t.Errorf("query failed %q: %v", q, err)
			continue
		}
		if len(res.Rows) != 2 {
			t.Errorf("query %q: expected 2 rows, got %d", q, len(res.Rows))
			continue
		}
		if res.Rows[0]["name"] != "Alice" {
			t.Errorf("query %q: expected Alice, got %v (columns: %v, row: %+v)", q, res.Rows[0]["name"], res.Columns, res.Rows[0])
		}
	}
}

func TestCoordinator_DBeaverCatalogQueries(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	coord.ExecuteSQL("CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(100));")
	coord.ExecuteSQL("INSERT INTO users VALUES (1, 'Alice');")

	// 1. DBeaver SQL Server sys.databases query with alias wildcard
	resDb, err := coord.ExecuteSQL("SELECT db.* FROM sys.databases db ORDER BY db.name")
	if err != nil {
		t.Fatalf("sys.databases query failed: %v", err)
	}
	if len(resDb.Rows) == 0 {
		t.Fatalf("expected databases, got 0 rows")
	}
	// Columns must NOT be just ["*"]
	foundName := false
	for _, c := range resDb.Columns {
		if c == "name" {
			foundName = true
			break
		}
	}
	if !foundName {
		t.Fatalf("expected 'name' in columns, got %v", resDb.Columns)
	}

	// 2. DBeaver PostgreSQL pg_shdescription join query
	resShDesc, err := coord.ExecuteSQL("select description from pg_shdescription join pg_database on objoid = pg_database.oid where datname = 'postgres'")
	if err != nil {
		t.Fatalf("pg_shdescription join query failed: %v", err)
	}
	if len(resShDesc.Columns) != 1 || resShDesc.Columns[0] != "description" {
		t.Fatalf("expected [description] column, got %v", resShDesc.Columns)
	}

	// 3. DBeaver PostgreSQL pg_roles with alias wildcard and left join pg_shdescription
	resRoles, err := coord.ExecuteSQL("SELECT a.oid,a.*,pd.description FROM pg_catalog.pg_roles a left join pg_catalog.pg_shdescription pd on a.oid = pd.objoid ORDER BY a.rolname")
	if err != nil {
		t.Fatalf("pg_roles join query failed: %v", err)
	}
	if len(resRoles.Rows) == 0 {
		t.Fatalf("expected roles, got 0 rows")
	}
	foundRolName := false
	for _, c := range resRoles.Columns {
		if c == "rolname" {
			foundRolName = true
			break
		}
	}
	if !foundRolName {
		t.Fatalf("expected 'rolname' in expanded columns, got %v", resRoles.Columns)
	}

	// 4. DBeaver PostgreSQL pg_conversion query
	qConv := `SELECT c.contoencoding as encid,pg_catalog.pg_encoding_to_char(c.contoencoding) as encname
FROM pg_catalog.pg_conversion c
GROUP BY c.contoencoding
ORDER BY 2`
	resConv, err := coord.ExecuteSQL(qConv)
	if err != nil {
		t.Fatalf("pg_conversion query failed: %v", err)
	}
	if len(resConv.Rows) == 0 {
		t.Fatalf("expected encodings, got 0 rows")
	}

	// 5. DBeaver SQL Server sys.all_objects query
	qObj := `SELECT o.*,ep.value as description,t.is_external FROM lionsql.sys.all_objects o
LEFT OUTER JOIN lionsql.sys.extended_properties ep ON ep.class=1 AND ep.major_id=o.object_id AND ep.minor_id=0 AND ep.name='MS_Description'
LEFT OUTER JOIN lionsql.sys.tables t ON t.object_id=o.object_id
WHERE o.type IN ('U','S','V','TT') AND o.schema_id = 1`
	resObj, err := coord.ExecuteSQL(qObj)
	if err != nil {
		t.Fatalf("sys.all_objects query failed: %v", err)
	}
	foundObjName := false
	for _, c := range resObj.Columns {
		if c == "name" {
			foundObjName = true
			break
		}
	}
	if !foundObjName {
		t.Fatalf("expected 'name' in sys.all_objects columns, got %v", resObj.Columns)
	}

	// 6. DBeaver SQL Server Table Size & Rows query
	qSize := `SELECT t.name, p.rows, SUM(a.total_pages) * 8 AS totalSize, SUM(a.used_pages) * 8 AS usedSize
FROM lionsql.sys.tables t
INNER JOIN lionsql.sys.indexes i ON t.OBJECT_ID = i.object_id
INNER JOIN lionsql.sys.partitions p ON i.object_id = p.OBJECT_ID AND i.index_id = p.index_id
INNER JOIN lionsql.sys.allocation_units a ON p.partition_id = a.container_id
LEFT OUTER JOIN lionsql.sys.schemas s ON t.schema_id = s.schema_id
WHERE t.schema_id =  1
AND t.object_id= 1000
GROUP BY t.name, p.rows`
	resSize, err := coord.ExecuteSQL(qSize)
	if err != nil {
		t.Fatalf("table size query failed: %v", err)
	}
	if len(resSize.Rows) != 1 {
		t.Fatalf("expected 1 row for table 1000, got %d", len(resSize.Rows))
	}
	if resSize.Rows[0]["name"] != "users" {
		t.Fatalf("expected table name users, got %v", resSize.Rows[0]["name"])
	}

	// 7. DBeaver PostgreSQL pg_class table query with relnamespace=2201 (dbo)
	qPgClass := `SELECT c.oid,c.*,d.description,pg_catalog.pg_get_expr(c.relpartbound, c.oid) as partition_expr,  pg_catalog.pg_get_partkeydef(c.oid) as partition_key
FROM pg_catalog.pg_class c
LEFT OUTER JOIN pg_catalog.pg_description d ON d.objoid=c.oid AND d.objsubid=0 AND d.classoid='pg_class'::regclass
WHERE c.relnamespace=2201 AND c.relkind not in ('i','I','c')`
	resPgClass, err := coord.ExecuteSQL(qPgClass)
	if err != nil {
		t.Fatalf("pg_class table query failed: %v", err)
	}
	if len(resPgClass.Rows) == 0 {
		t.Fatalf("expected tables for dbo, got 0 rows")
	}
	foundRelName := false
	for _, c := range resPgClass.Columns {
		if c == "relname" {
			foundRelName = true
			break
		}
	}
	if !foundRelName {
		t.Fatalf("expected relname in columns, got %v", resPgClass.Columns)
	}
}

func TestCoordinator_SSMS_VirtualCatalogQueries(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// Setup a sample table
	_, _ = coord.ExecuteSQL("CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(50))")
	_, _ = coord.ExecuteSQL("CREATE VIEW v_users AS SELECT id, name FROM users")

	// 1. sys.configurations
	resCfg, err := coord.ExecuteSQL("SELECT configuration_id, name, value, value_in_use FROM sys.configurations ORDER BY configuration_id")
	if err != nil {
		t.Fatalf("sys.configurations failed: %v", err)
	}
	if len(resCfg.Rows) == 0 {
		t.Fatalf("expected configurations rows, got 0")
	}

	// 2. sys.dm_exec_sessions
	resSess, err := coord.ExecuteSQL("SELECT session_id, login_name, status, is_user_process FROM sys.dm_exec_sessions")
	if err != nil {
		t.Fatalf("sys.dm_exec_sessions failed: %v", err)
	}
	if len(resSess.Rows) == 0 {
		t.Fatalf("expected sessions rows, got 0")
	}

	// 3. sys.dm_exec_connections
	resConn, err := coord.ExecuteSQL("SELECT session_id, net_transport, client_net_address FROM sys.dm_exec_connections")
	if err != nil {
		t.Fatalf("sys.dm_exec_connections failed: %v", err)
	}
	if len(resConn.Rows) == 0 {
		t.Fatalf("expected connections rows, got 0")
	}

	// 4. sys.database_principals
	resPrincipals, err := coord.ExecuteSQL("SELECT name, principal_id, type_desc FROM sys.database_principals")
	if err != nil {
		t.Fatalf("sys.database_principals failed: %v", err)
	}
	if len(resPrincipals.Rows) == 0 {
		t.Fatalf("expected database_principals rows, got 0")
	}

	// 5. sys.server_principals
	resServerPrincipals, err := coord.ExecuteSQL("SELECT name, principal_id, type_desc FROM sys.server_principals")
	if err != nil {
		t.Fatalf("sys.server_principals failed: %v", err)
	}
	if len(resServerPrincipals.Rows) == 0 {
		t.Fatalf("expected server_principals rows, got 0")
	}

	// 6. sys.dm_os_sys_info
	resSysInfo, err := coord.ExecuteSQL("SELECT cpu_count, physical_memory_kb FROM sys.dm_os_sys_info")
	if err != nil {
		t.Fatalf("sys.dm_os_sys_info failed: %v", err)
	}
	if len(resSysInfo.Rows) == 0 {
		t.Fatalf("expected dm_os_sys_info rows, got 0")
	}

	// 6-1. sys.dm_os_host_info (SSMS Object Explorer connection requirement)
	resHostInfo, err := coord.ExecuteSQL("SELECT host_platform, host_distribution, host_release, os_architecture FROM sys.dm_os_host_info")
	if err != nil {
		t.Fatalf("sys.dm_os_host_info failed: %v", err)
	}
	if len(resHostInfo.Rows) == 0 {
		t.Fatalf("expected dm_os_host_info rows, got 0")
	}
	rowMap := resHostInfo.Rows[0]
	// Invariant 11: host_platform must unconditionally be "Windows" for 100% SSMS parity regardless of host OS
	if rowMap["host_platform"] != "Windows" {
		t.Fatalf("Invariant 11 violation: expected host_platform to be 'Windows', got %v", rowMap["host_platform"])
	}
	if rowMap["host_distribution"] != "Windows" {
		t.Fatalf("Invariant 11 violation: expected host_distribution to be 'Windows', got %v", rowMap["host_distribution"])
	}
	if rowMap["host_release"] != "10.0" {
		t.Fatalf("Invariant 11 violation: expected host_release to be '10.0', got %v", rowMap["host_release"])
	}
	if rowMap["os_architecture"] == "" {
		t.Fatalf("expected non-empty os_architecture, got %v", rowMap)
	}

	// 7. sys.endpoints
	resEndpoints, err := coord.ExecuteSQL("SELECT endpoint_id, name, protocol_desc, state_desc FROM sys.endpoints")
	if err != nil {
		t.Fatalf("sys.endpoints failed: %v", err)
	}
	if len(resEndpoints.Rows) == 0 {
		t.Fatalf("expected endpoints rows, got 0")
	}

	// 8. sys.database_files and master.sys.master_files
	resDbFiles, err := coord.ExecuteSQL("SELECT file_id, name, physical_name, size FROM sys.database_files")
	if err != nil {
		t.Fatalf("sys.database_files failed: %v", err)
	}
	if len(resDbFiles.Rows) == 0 {
		t.Fatalf("expected database_files rows, got 0")
	}

	resMasterFiles, err := coord.ExecuteSQL("SELECT database_id, file_id, name FROM [master].[sys].[master_files]")
	if err != nil {
		t.Fatalf("master_files failed: %v", err)
	}
	if len(resMasterFiles.Rows) == 0 {
		t.Fatalf("expected master_files rows, got 0")
	}

	// 9. sys.all_objects
	resAllObj, err := coord.ExecuteSQL("SELECT name, type, type_desc FROM sys.all_objects")
	if err != nil {
		t.Fatalf("sys.all_objects failed: %v", err)
	}
	foundUsersTable := false
	for _, r := range resAllObj.Rows {
		if r["name"] == "users" && (r["type"] == "U " || r["type"] == "U") {
			foundUsersTable = true
			break
		}
	}
	if !foundUsersTable {
		t.Fatalf("expected 'users' table in sys.all_objects, got %v", resAllObj.Rows)
	}

	// 10. msdb.dbo.syspolicy_configuration & syspolicy_policies
	resPolicyCfg, err := coord.ExecuteSQL("SELECT current_value FROM msdb.dbo.syspolicy_configuration WHERE name = 'Enabled'")
	if err != nil {
		t.Fatalf("syspolicy_configuration failed: %v", err)
	}
	if len(resPolicyCfg.Rows) != 1 {
		t.Fatalf("expected 1 row for syspolicy_configuration Enabled, got %d", len(resPolicyCfg.Rows))
	}
	if enabledVal, ok := resPolicyCfg.Rows[0]["current_value"].(bool); !ok || enabledVal != false {
		t.Fatalf("expected Enabled = false (bool), got %v (%T)", resPolicyCfg.Rows[0]["current_value"], resPolicyCfg.Rows[0]["current_value"])
	}

	resPolicyList, err := coord.ExecuteSQL("SELECT policy_id, name, is_enabled FROM msdb.dbo.syspolicy_policies")
	if err != nil {
		t.Fatalf("syspolicy_policies failed: %v", err)
	}
	if len(resPolicyList.Columns) < 3 {
		t.Fatalf("expected at least 3 columns for syspolicy_policies, got %v", resPolicyList.Columns)
	}

	// 11. SSMS Object Explorer Database Discovery Query (from ssms7.txt)
	qSSMS := `SELECT
dtb.name AS [Database_Name],
'Server[@Name=' + quotename(CAST(
        serverproperty(N'Servername')
       AS sysname),'''') + ']' + '/Database[@Name=' + quotename(dtb.name,'''') + ']' AS [Database_Urn],
dtb.containment AS [Database_ContainmentType],
dtb.recovery_model AS [Database_RecoveryModel],
ISNULL(suser_sname(dtb.owner_sid),'') AS [Database_Owner],
dtb.compatibility_level AS [Database_CompatibilityLevel],

        case
        when dtb.collation_name is null then 0x200
        else 0
        end |
        case
        when 1 = dtb.is_in_standby then 0x40
        else 0
        end |
        case dtb.state
        when 1 then 0x2
        when 2 then 0x8
        when 3 then 0x4
        when 4 then 0x10
        when 5 then 0x100
        when 6 then 0x20
        else 1
        end
       AS [Database_Status],
CAST(
        case
        when SERVERPROPERTY('EngineEdition') = 6 then cast(1 as bit)
        else cast(0 as bit)
        end
       AS bit) AS [Database_IsSqlDw],
case when SERVERPROPERTY('EngineEdition') = 8 then SERVERPROPERTY('IsFullTextInstalled') else dtb.is_fulltext_enabled end AS [Database_IsFullTextEnabled],
dtb.recovery_model AS [RecoveryModel],
dtb.user_access AS [UserAccess],
dtb.is_read_only AS [ReadOnly],
dtb.name AS [Database_DatabaseName2]
FROM
master.sys.databases AS dtb

WHERE
(CAST(isnull(dtb.source_database_id, 0) AS bit)='1')
ORDER BY
[Database_Name] ASC`

	// 11. SSMS Object Explorer System Database Discovery Query (from ssms7.txt)
	qSSMSSystem := `SELECT
dtb.name AS [Database_Name],
'Server[@Name=' + quotename(CAST(
        serverproperty(N'Servername')
       AS sysname),'''') + ']' + '/Database[@Name=' + quotename(dtb.name,'''') + ']' AS [Database_Urn],
dtb.containment AS [Database_ContainmentType],
dtb.recovery_model AS [Database_RecoveryModel],
ISNULL(suser_sname(dtb.owner_sid),'') AS [Database_Owner],
dtb.compatibility_level AS [Database_CompatibilityLevel],

        case
        when dtb.collation_name is null then 0x200
        else 0
        end |
        case
        when 1 = dtb.is_in_standby then 0x40
        else 0
        end |
        case dtb.state
        when 1 then 0x2
        when 2 then 0x8
        when 3 then 0x4
        when 4 then 0x10
        when 5 then 0x100
        when 6 then 0x20
        else 1
        end
       AS [Database_Status],
CAST(
        case
        when SERVERPROPERTY('EngineEdition') = 6 then cast(1 as bit)
        else cast(0 as bit)
        end
       AS bit) AS [Database_IsSqlDw],
case when SERVERPROPERTY('EngineEdition') = 8 then SERVERPROPERTY('IsFullTextInstalled') else dtb.is_fulltext_enabled end AS [Database_IsFullTextEnabled],
dtb.recovery_model AS [RecoveryModel],
dtb.user_access AS [UserAccess],
dtb.is_read_only AS [ReadOnly],
dtb.name AS [Database_DatabaseName2]
FROM
master.sys.databases AS dtb

WHERE
(CAST(case when dtb.name in ('master','model','msdb','tempdb') then 1 else dtb.is_distributor end AS bit)='1')
ORDER BY
[Database_Name] ASC`

	resDbSys, err := coord.ExecuteSQL(qSSMSSystem)
	if err != nil {
		t.Fatalf("SSMS system database discovery query failed: %v", err)
	}
	if resDbSys == nil || len(resDbSys.Rows) < 4 {
		t.Fatalf("expected 4 system database rows (master, tempdb, model, msdb), got %v", resDbSys)
	}

	// 12. SSMS Snapshot Discovery Query (should return 0 rows without errors)
	resDbSnap, err := coord.ExecuteSQL(qSSMS)
	if err != nil {
		t.Fatalf("SSMS snapshot discovery query failed: %v", err)
	}
	if resDbSnap == nil {
		t.Fatalf("expected non-nil snapshot result")
	}

	// 13. SSMS Table Enumeration Query
	qSSMSTables := `SELECT
tbl.name AS [Name],
'Server[@Name=' + quotename(CAST(serverproperty(N'Servername') AS sysname),'''') + ']' + '/Database[@Name=' + quotename(db_name(),'''') + ']' + '/Table[@Name=' + quotename(tbl.name,'''') + ' and @Schema=' + quotename(SCHEMA_NAME(tbl.schema_id),'''') + ']' AS [Urn],
tbl.object_id AS [ID],
tbl.create_date AS [CreateDate],
tbl.modify_date AS [DateLastModified],
SCHEMA_NAME(tbl.schema_id) AS [Schema],
CAST(case when tbl.principal_id is null then 1 else 0 end AS bit) AS [IsSchemaOwned]
FROM
sys.tables AS tbl
ORDER BY
[Schema] ASC,[Name] ASC`

	resTbl, err := coord.ExecuteSQL(qSSMSTables)
	if err != nil {
		t.Fatalf("SSMS table enumeration query failed: %v", err)
	}
	if resTbl == nil || len(resTbl.Rows) == 0 {
		t.Fatalf("expected at least 1 table row (users), got %v", resTbl)
	}
	foundUsers := false
	var usersRow map[string]any
	for _, r := range resTbl.Rows {
		if r["Name"] == "users" {
			foundUsers = true
			usersRow = r
			break
		}
	}
	if !foundUsers {
		t.Fatalf("expected 'users' table in SSMS table list, got: %v", resTbl.Rows)
	}
	if usersRow["Schema"] != "dbo" {
		t.Fatalf("expected Schema='dbo', got %v", usersRow["Schema"])
	}
	if usersRow["Urn"] == nil || !strings.Contains(fmt.Sprintf("%v", usersRow["Urn"]), "Table[@Name='users'") {
		t.Fatalf("expected valid Table URN, got %v", usersRow["Urn"])
	}
	if isOwned, ok := usersRow["IsSchemaOwned"].(bool); !ok || !isOwned {
		t.Fatalf("expected IsSchemaOwned=true, got %v", usersRow["IsSchemaOwned"])
	}

	// 14. SSMS Resource Governor Query
	qSSMSRG := `SELECT
'Server[@Name=' + quotename(CAST(serverproperty(N'Servername') AS sysname),'''') + ']' + '/ResourceGovernor' AS [Urn]
FROM
sys.resource_governor_configuration AS c
ORDER BY
CAST(c.is_enabled AS bit) ASC`
	resRG, err := coord.ExecuteSQL(qSSMSRG)
	if err != nil {
		t.Fatalf("SSMS resource governor query failed: %v", err)
	}
	if resRG == nil || len(resRG.Rows) == 0 {
		t.Fatalf("expected 1 row for resource governor, got %v", resRG)
	}
	if resRG.Rows[0]["Urn"] == nil || !strings.Contains(fmt.Sprintf("%v", resRG.Rows[0]["Urn"]), "/ResourceGovernor") {
		t.Fatalf("expected ResourceGovernor URN, got %v", resRG.Rows[0]["Urn"])
	}

	// 15. SSMS Collation Property Query (COLLATIONPROPERTY)
	qSSMSCollation := `SELECT COLLATIONPROPERTY((select collation_name from sys.databases where name = ISNULL('master', db_name())), 'LCID') AS [LCID], COLLATIONPROPERTY((select collation_name from sys.databases where name = ISNULL('master', db_name())), 'ComparisonStyle') AS [ComparisonStyle]`
	resCollation, err := coord.ExecuteSQL(qSSMSCollation)
	if err != nil {
		t.Fatalf("SSMS collation property query failed: %v", err)
	}
	if len(resCollation.Rows) != 1 {
		t.Fatalf("expected 1 row for collation property, got %d", len(resCollation.Rows))
	}
	if fmt.Sprintf("%v", resCollation.Rows[0]["LCID"]) != "1033" {
		t.Fatalf("expected LCID=1033, got %v", resCollation.Rows[0]["LCID"])
	}
	if fmt.Sprintf("%v", resCollation.Rows[0]["ComparisonStyle"]) != "196609" {
		t.Fatalf("expected ComparisonStyle=196609, got %v", resCollation.Rows[0]["ComparisonStyle"])
	}

	// 16. SSMS Complex Table Enumeration Query with sys.indexes & sys.data_spaces & sys.extended_properties
	qSSMSComplexTables := `SELECT
tbl.name AS [Name],
SCHEMA_NAME(tbl.schema_id) AS [Schema],
'Server[@Name=' + quotename(CAST(serverproperty(N'Servername') AS sysname),'''') + ']' + '/Database[@Name=' + quotename(db_name(),'''') + ']' + '/Table[@Name=' + quotename(tbl.name,'''') + ' and @Schema=' + quotename(SCHEMA_NAME(tbl.schema_id),'''') + ']' AS [Urn],
tbl.create_date AS [CreateDate],
CAST(tbl.is_memory_optimized AS bit) AS [IsMemoryOptimized],
CAST(CASE idx.type WHEN 5 THEN 1 ELSE 0 END AS bit) AS [HasClusteredColumnStoreIndex],
CAST(CASE idx.index_id WHEN 1 THEN 1 ELSE 0 END AS bit) AS [HasClusteredIndex],
CAST(case idx.index_id when 1 then case when (idx.is_primary_key + 2*idx.is_unique_constraint = 1) then 1 else 0 end else 0 end AS bit) AS [HasPrimaryClusteredIndex]
FROM
sys.tables AS tbl
INNER JOIN sys.indexes AS idx ON
        idx.object_id = tbl.object_id and (idx.index_id < '2'  or (tbl.is_memory_optimized = 1 and idx.index_id = (select min(index_id) from sys.indexes where object_id = tbl.object_id)))
LEFT OUTER JOIN sys.data_spaces AS dsidx ON dsidx.data_space_id = idx.data_space_id
WHERE
(CAST(
 case
    when tbl.is_ms_shipped = 1 then 1
    when exists (
        select
            1
        from
            sys.extended_properties
        where
            major_id = tbl.object_id and
            minor_id = 0 and
            class = 1 and
            name = N'microsoft_database_tools_support')
        then 1
    else 0
end
             AS bit)='0' and tbl.is_filetable='0' and CAST(tbl.is_memory_optimized AS bit)='0' and tbl.temporal_type='0' and CAST(tbl.is_external AS bit)='0' and CAST(tbl.is_node AS bit)='0' and CAST(tbl.is_edge AS bit)='0')
ORDER BY
[Schema] ASC,[Name] ASC`

	resComplexTbl, err := coord.ExecuteSQL(qSSMSComplexTables)
	if err != nil {
		t.Fatalf("SSMS complex table query failed: %v", err)
	}
	if len(resComplexTbl.Rows) == 0 {
		t.Fatalf("expected non-zero rows for complex table query, got 0")
	}
	for k, v := range resComplexTbl.Rows[0] {
		t.Logf("col [%s] = %v (%T)", k, v, v)
	}
	if fmt.Sprintf("%v", resComplexTbl.Rows[0]["Name"]) != "users" {
		t.Fatalf("expected table 'users', got %v", resComplexTbl.Rows[0]["Name"])
	}

	// 17. SSMS User Database Query
	qSSMSUserDb := `SELECT
dtb.name AS [Database_Name]
FROM
master.sys.databases AS dtb
WHERE
(CAST(case when dtb.name in ('master','model','msdb','tempdb') then 1 else dtb.is_distributor end AS bit)=0)`
	resUserDb, err := coord.ExecuteSQL(qSSMSUserDb)
	if err != nil {
		t.Fatalf("SSMS user db query failed: %v", err)
	}
	if len(resUserDb.Rows) != 1 || resUserDb.Rows[0]["Database_Name"] != "lionsql" {
		t.Fatalf("expected 1 user database 'lionsql', got %+v", resUserDb.Rows)
	}

	// 18. SSMS Synonyms Query
	qSSMSSynonyms := `SELECT
s.name AS [Name],
SCHEMA_NAME(s.schema_id) AS [Schema],
'Server[@Name=' + quotename(CAST(serverproperty(N'Servername') AS sysname),'''') + ']' + '/Database[@Name=' + quotename(db_name(),'''') + ']' + '/Synonym[@Name=' + quotename(s.name,'''') + ' and @Schema=' + quotename(SCHEMA_NAME(s.schema_id),'''') + ']' AS [Urn],
s.create_date AS [CreateDate],
ISNULL(ss.name, N'') AS [Owner]
FROM
sys.synonyms AS s
LEFT OUTER JOIN sys.database_principals AS ss ON ss.principal_id = ISNULL(s.principal_id, (OBJECTPROPERTY(s.object_id, 'OwnerId')))
ORDER BY
[Schema] ASC,[Name] ASC`
	if _, err := coord.ExecuteSQL(qSSMSSynonyms); err != nil {
		t.Fatalf("SSMS synonyms query failed: %v", err)
	}

	// 19. SSMS Stored Procedures Query
	qSSMSProcs := `SELECT
sp.name AS [Name],
SCHEMA_NAME(sp.schema_id) AS [Schema],
'Server[@Name=' + quotename(CAST(serverproperty(N'Servername') AS sysname),'''') + ']' + '/Database[@Name=' + quotename(db_name(),'''') + ']' + '/StoredProcedure[@Name=' + quotename(sp.name,'''') + ' and @Schema=' + quotename(SCHEMA_NAME(sp.schema_id),'''') + ']' AS [Urn],
sp.create_date AS [CreateDate],
ISNULL(ssp.name, N'') AS [Owner],
CAST(CASE WHEN ISNULL(smsp.definition, ssmsp.definition) IS NULL THEN 1 ELSE 0 END AS bit) AS [IsEncrypted],
CASE WHEN sp.type = N'P' THEN 1 WHEN sp.type = N'PC' THEN 2 ELSE 1 END AS [ImplementationType],
ISNULL(sm.uses_native_compilation,0) AS [IsNativelyCompiled]
FROM
sys.all_objects AS sp
LEFT OUTER JOIN sys.database_principals AS ssp ON ssp.principal_id = ISNULL(sp.principal_id, (OBJECTPROPERTY(sp.object_id, 'OwnerId')))
LEFT OUTER JOIN sys.sql_modules AS smsp ON smsp.object_id = sp.object_id
LEFT OUTER JOIN sys.system_sql_modules AS ssmsp ON ssmsp.object_id = sp.object_id
LEFT OUTER JOIN sys.all_sql_modules AS sm ON sm.object_id = sp.object_id
WHERE
(sp.type = 'P' OR sp.type = 'RF' OR sp.type='PC')and(CAST(
 case
    when sp.is_ms_shipped = 1 then 1
    when exists (
        select
            1
        from
            sys.extended_properties
        where
            major_id = sp.object_id and
            minor_id = 0 and
            class = 1 and
            name = N'microsoft_database_tools_support')
        then 1
    else 0
end
             AS bit)='0' and ISNULL(sm.uses_native_compilation,0)='0')`
	if _, err := coord.ExecuteSQL(qSSMSProcs); err != nil {
		t.Fatalf("SSMS procs query failed: %v", err)
	}
}

func TestReplicaSessionZeroAllocationFastPath(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. Create table and sample data on primary mode
	if _, err := coord.ExecuteSQL("CREATE TABLE products (id INT PRIMARY KEY, name VARCHAR(50), price FLOAT)"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO products (id, name, price) VALUES (1, 'Widget', 19.99), (2, 'Gadget', 29.99)"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 2. Attach replication manager in RoleReplica mode
	repMgr, err := replication.NewManager(replication.Config{
		NodeID:     "replica-node-1",
		Role:       replication.RoleReplica,
		PrimaryURL: "http://127.0.0.1:8090",
		Engine:     eng,
	})
	if err != nil {
		t.Fatalf("failed to create replication manager: %v", err)
	}
	defer repMgr.Close()

	coord.SetReplicationManager(repMgr)

	// 3. Verify coordinator is recognized as read-only
	if !coord.IsReadOnly() {
		t.Fatalf("expected coordinator to be read-only")
	}

	// 4. Verify newly acquired session automatically inherits ReadOnly status
	sess, err := coord.GetOrCreateSession("replica-sess-1")
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	if !sess.IsReadOnly() {
		t.Fatalf("expected replica session to report IsReadOnly() == true")
	}
	if !sess.TxOptions().ReadOnly {
		t.Fatalf("expected replica session TxOptions.ReadOnly to be true")
	}

	// 5. Verify write query is rejected on replica session
	_, err = coord.ExecuteSessionSQL("replica-sess-1", "INSERT INTO products (id, name, price) VALUES (3, 'Doohickey', 9.99)")
	if err == nil {
		t.Fatalf("expected INSERT on read-only replica to fail, got nil")
	}

	// 6. Verify read query succeeds on replica session
	res, err := coord.ExecuteSessionSQL("replica-sess-1", "SELECT id, name, price FROM products WHERE id = 1")
	if err != nil {
		t.Fatalf("SELECT on read-only replica failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Widget" {
		t.Fatalf("unexpected query result on replica: %v", res.Rows)
	}

	// 7. Test Failover Promotion: Promote replica to Primary
	if err := repMgr.Promote(); err != nil {
		t.Fatalf("failed to promote replica: %v", err)
	}
	if coord.IsReadOnly() {
		t.Fatalf("promoted coordinator should no longer be read-only")
	}
	if sess.IsReadOnly() {
		t.Fatalf("session on promoted node should dynamically report IsReadOnly() == false")
	}
	if sess.TxOptions().ReadOnly {
		t.Fatalf("session TxOptions on promoted node should be read-write (ReadOnly == false)")
	}

	// 8. Verify writes are now accepted on the promoted node
	if _, err := coord.ExecuteSessionSQL("replica-sess-1", "INSERT INTO products (id, name, price) VALUES (3, 'Doohickey', 9.99)"); err != nil {
		t.Fatalf("INSERT on promoted primary failed: %v", err)
	}
}

// TestTolerantUseDatabase verifies tolerant virtual no-op acceptance of USE <database>
// and seamless synchronization with DB_NAME() and CURRENT_DATABASE().
func TestTolerantUseDatabase(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})

	// 1. Initial state defaults to postgres for generic sessions, master for TDS sessions
	res, err := coord.ExecuteSessionSQL("sess-db-1", "SELECT DB_NAME() AS db, CURRENT_DATABASE() AS cdb, DB_ID() AS id;")
	if err != nil {
		t.Fatalf("initial SELECT DB_NAME() failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["db"] != "postgres" || res.Rows[0]["cdb"] != "postgres" {
		t.Fatalf("expected default database postgres, got: %v", res.Rows)
	}

	resTds, err := coord.ExecuteSessionSQL("tds-db-1", "SELECT DB_NAME() AS db, CURRENT_DATABASE() AS cdb;")
	if err != nil || len(resTds.Rows) != 1 || resTds.Rows[0]["db"] != "master" || resTds.Rows[0]["cdb"] != "master" {
		t.Fatalf("expected default database master for TDS session, got: %v", resTds.Rows)
	}

	// 2. Bracketed identifier: USE [SalesDB]
	resUse, err := coord.ExecuteSessionSQL("sess-db-1", "USE [SalesDB];")
	if err != nil {
		t.Fatalf("USE [SalesDB] failed: %v", err)
	}
	if !resUse.Success || resUse.Command != "USE" {
		t.Fatalf("unexpected USE response: %+v", resUse)
	}

	res, err = coord.ExecuteSessionSQL("sess-db-1", "SELECT DB_NAME() AS db;")
	if err != nil || res.Rows[0]["db"] != "SalesDB" {
		t.Fatalf("expected DB_NAME() SalesDB after USE, got: %v", res.Rows)
	}

	// 3. Quoted identifier: USE "Analytics_2026"
	_, err = coord.ExecuteSessionSQL("sess-db-1", `USE "Analytics_2026";`)
	if err != nil {
		t.Fatalf(`USE "Analytics_2026" failed: %v`, err)
	}

	res, err = coord.ExecuteSessionSQL("sess-db-1", "SELECT DB_NAME() AS db, CURRENT_DATABASE() AS cdb;")
	if err != nil || res.Rows[0]["db"] != "Analytics_2026" || res.Rows[0]["cdb"] != "Analytics_2026" {
		t.Fatalf("expected Analytics_2026, got: %v", res.Rows)
	}

	// 4. Batch query with USE prefix: USE [master]; CREATE TABLE ...; INSERT ...; SELECT ...
	batchSQL := `
USE [master];
CREATE TABLE t_use_demo (id INT PRIMARY KEY, title VARCHAR(50));
INSERT INTO t_use_demo (id, title) VALUES (100, 'Test Record');
SELECT id, title FROM t_use_demo WHERE id = 100;
`
	resBatch, err := coord.ExecuteSessionSQL("sess-db-1", batchSQL)
	if err != nil {
		t.Fatalf("batch query with USE prefix failed: %v", err)
	}
	if len(resBatch.Rows) != 1 || resBatch.Rows[0]["title"] != "Test Record" {
		t.Fatalf("unexpected batch query result: %+v", resBatch.Rows)
	}
}

func TestCoordinator_SessionVarQuoteEscaping(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	_, err := coord.ExecuteSQL("CREATE TABLE test_quotes (id INT PRIMARY KEY, title VARCHAR(100));")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	sessID := "sess-quote-test"
	defer coord.CloseSession(sessID)

	// Set string variable with single quote: O'Reilly Media
	_, err = coord.ExecuteSessionSQL(sessID, "SET @author = 'O''Reilly Media';")
	if err != nil {
		t.Fatalf("SET @author failed: %v", err)
	}

	// Insert using @author substitution
	_, err = coord.ExecuteSessionSQL(sessID, "INSERT INTO test_quotes (id, title) VALUES (1, @author);")
	if err != nil {
		t.Fatalf("INSERT with session variable failed: %v", err)
	}

	// Query using @author substitution
	res, err := coord.ExecuteSessionSQL(sessID, "SELECT title FROM test_quotes WHERE title = @author;")
	if err != nil {
		t.Fatalf("SELECT with session variable failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["title"] != "O'Reilly Media" {
		t.Fatalf("unexpected query result: %+v", res.Rows)
	}
}

func TestCoordinator_TableVarSessionLifecycleAndCleanup(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessID := "sess-tblvar-test"

	// 1. Declare table variable in session
	declSQL := "DECLARE @my_items TABLE (id INT, name VARCHAR(50));"
	res, err := coord.ExecuteSessionSQL(sessID, declSQL)
	if err != nil || !res.Success {
		t.Fatalf("DECLARE TABLE variable failed: %v", err)
	}

	physTable := fmt.Sprintf("__tv_%s_my_items", sessID)
	// Verify physical table exists in storage engine
	if _, err := eng.GetTable(physTable); err != nil {
		t.Fatalf("expected physical table %s to exist, got error: %v", physTable, err)
	}

	// Insert and select into table variable
	_, err = coord.ExecuteSessionSQL(sessID, "INSERT INTO @my_items VALUES (10, 'Widget A');")
	if err != nil {
		t.Fatalf("INSERT into @my_items failed: %v", err)
	}

	res, err = coord.ExecuteSessionSQL(sessID, "SELECT * FROM @my_items;")
	if err != nil || len(res.Rows) != 1 || res.Rows[0]["name"] != "Widget A" {
		t.Fatalf("SELECT from @my_items failed: %+v", res)
	}

	// 2. Close session and verify physical table is cleanly dropped
	coord.CloseSession(sessID)

	if _, err := eng.GetTable(physTable); err == nil {
		t.Fatalf("expected physical table %s to be dropped on CloseSession, but it still exists", physTable)
	}

	// 3. Test DISCARD ALL cleanup
	sessID2 := "sess-discard-test"
	defer coord.CloseSession(sessID2)

	_, err = coord.ExecuteSessionSQL(sessID2, "DECLARE @temp_items TABLE (id INT);")
	if err != nil {
		t.Fatalf("DECLARE @temp_items failed: %v", err)
	}
	physTable2 := fmt.Sprintf("__tv_%s_temp_items", sessID2)
	if _, err := eng.GetTable(physTable2); err != nil {
		t.Fatalf("expected physical table %s to exist, got error: %v", physTable2, err)
	}

	// Issue DISCARD ALL
	res, err = coord.ExecuteSessionSQL(sessID2, "DISCARD ALL;")
	if err != nil || !res.Success {
		t.Fatalf("DISCARD ALL failed: %v", err)
	}

	if _, err := eng.GetTable(physTable2); err == nil {
		t.Fatalf("expected physical table %s to be dropped on DISCARD ALL, but it still exists", physTable2)
	}
}

func TestCoordinator_ContradictionPruningFastFail(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	coord.ExecuteSQL(`CREATE TABLE t_items (id INT PRIMARY KEY, name VARCHAR(50), price INT);`)
	coord.ExecuteSQL(`INSERT INTO t_items VALUES (1, 'Item 1', 100), (2, 'Item 2', 200);`)

	// 1. SELECT * with contradiction condition 1 = 0 should return 0 rows instantly
	res, err := coord.ExecuteSQL(`SELECT * FROM t_items WHERE 1 = 0;`)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows for WHERE 1=0, got %d", len(res.Rows))
	}

	// 2. Aggregate query with 1 = 0 should return 1 row with COUNT = 0
	resAgg, err := coord.ExecuteSQL(`SELECT COUNT(*) AS total_count FROM t_items WHERE 1 = 0;`)
	if err != nil {
		t.Fatalf("aggregate query failed: %v", err)
	}
	if len(resAgg.Rows) != 1 {
		t.Fatalf("expected 1 row for aggregate with WHERE 1=0, got %d", len(resAgg.Rows))
	}
	if fmt.Sprintf("%v", resAgg.Rows[0]["total_count"]) != "0" {
		t.Fatalf("expected count 0, got %v", resAgg.Rows[0]["total_count"])
	}
}

func TestCoordinator_Regression_P0_37_QueryPanicRecovery(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	// Register a scalar UDF that deliberately panics
	proc.RegisterScalarFunction("PANIC_UDF", func(args []storage.Value) storage.Value {
		panic("deliberate catastrophic UDF panic in engine")
	})

	sessID := "panic_session"
	// 1. Begin a transaction in the session
	_, err := coord.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION;")
	if err != nil {
		t.Fatalf("BEGIN TRANSACTION failed: %v", err)
	}

	// 2. Execute query invoking the panicking UDF.
	// Without P0-37 panic recovery, this would crash the entire process/goroutine.
	res, err := coord.ExecuteSessionSQL(sessID, "SELECT PANIC_UDF();")
	if err == nil && (res == nil || res.Success) {
		t.Fatalf("expected query with panicking UDF to fail, but got success")
	}

	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	} else if res != nil && !res.Success {
		errMsg = res.Error
	}
	if !strings.Contains(errMsg, "panic") && !strings.Contains(errMsg, "deliberate catastrophic UDF panic") {
		t.Fatalf("expected error message to mention panic, got: %v", errMsg)
	}

	// 3. Verify that the session has rolled back its aborted transaction and can execute subsequent queries normally
	cs, err := coord.GetOrCreateSession(sessID)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	cs.Mu.Lock()
	txActive := cs.Tx != nil
	tranCount := cs.TranCount
	cs.Mu.Unlock()

	if txActive || tranCount != 0 {
		t.Errorf("expected transaction to be rolled back and cleaned up after panic, but txActive=%v, tranCount=%d", txActive, tranCount)
	}

	// 4. Run a normal query on the same session to confirm engine viability
	resNormal, err := coord.ExecuteSessionSQL(sessID, "SELECT 42 AS answer;")
	if err != nil || !resNormal.Success {
		t.Fatalf("subsequent query after panic recovery failed: %v (res: %v)", err, resNormal)
	}
	if len(resNormal.Rows) != 1 || fmt.Sprintf("%v", resNormal.Rows[0]["answer"]) != "42" {
		t.Fatalf("expected answer 42, got: %v", resNormal.Rows)
	}
}



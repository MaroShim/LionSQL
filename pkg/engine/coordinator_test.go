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

	_, err := coord.ExecuteSQL(`CREATE TABLE items (id INT PRIMARY KEY, name VARCHAR(50));`)
	if err != nil {
		t.Fatalf("CREATE TABLE items failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO items VALUES (1, 'Pen'), (2, 'Notebook');`)
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




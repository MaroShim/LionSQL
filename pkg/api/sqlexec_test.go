package api_test

import (
	"fmt"
	"strings"
	"testing"

	"sqlion/pkg/api"
	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
)

func TestSQLExecutor_DDL_DML_DQL(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	// 1. CREATE TABLE
	createSQL := `CREATE TABLE customers (
		id INT PRIMARY KEY,
		name VARCHAR(100) NOT NULL,
		balance FLOAT,
		is_active BOOL
	);`
	res, err := server.ExecuteSQL(createSQL)
	if err != nil || !res.Success {
		t.Fatalf("CREATE TABLE failed: %v (res: %+v)", err, res)
	}

	// 2. INSERT single and multiple rows
	insertSQL := `INSERT INTO customers (id, name, balance, is_active) VALUES
		(1, 'Alice Smith', 1500.50, TRUE),
		(2, 'Bob Jones', 750.00, TRUE),
		(3, 'Charlie Brown', 250.25, FALSE);`
	res, err = server.ExecuteSQL(insertSQL)
	if err != nil || !res.Success {
		t.Fatalf("INSERT failed: %v (res: %+v)", err, res)
	}
	if res.RowsAffected != 3 {
		t.Fatalf("expected 3 rows inserted, got %d", res.RowsAffected)
	}

	// 3. SELECT * FROM customers
	selectSQL := `SELECT * FROM customers WHERE balance > 500 ORDER BY id ASC;`
	res, err = server.ExecuteSQL(selectSQL)
	if err != nil || !res.Success {
		t.Fatalf("SELECT failed: %v (res: %+v)", err, res)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 matching rows (>500), got %d", len(res.Rows))
	}
	if res.Rows[0]["name"] != "Alice Smith" || res.Rows[1]["name"] != "Bob Jones" {
		t.Fatalf("unexpected row contents: %+v", res.Rows)
	}

	// 4. UPDATE
	updateSQL := `UPDATE customers SET balance = 2000.0 WHERE id = 1;`
	res, err = server.ExecuteSQL(updateSQL)
	if err != nil || !res.Success {
		t.Fatalf("UPDATE failed: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("expected 1 row updated, got %d", res.RowsAffected)
	}

	// Verify update
	res, _ = server.ExecuteSQL(`SELECT balance FROM customers WHERE id = 1;`)
	if len(res.Rows) != 1 || res.Rows[0]["balance"] != 2000.0 {
		t.Fatalf("expected updated balance 2000.0, got: %+v", res.Rows)
	}

	// 5. DELETE
	deleteSQL := `DELETE FROM customers WHERE id = 3;`
	res, err = server.ExecuteSQL(deleteSQL)
	if err != nil || !res.Success {
		t.Fatalf("DELETE failed: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("expected 1 row deleted, got %d", res.RowsAffected)
	}

	// 6. DROP TABLE
	dropSQL := `DROP TABLE customers;`
	res, err = server.ExecuteSQL(dropSQL)
	if err != nil || !res.Success {
		t.Fatalf("DROP TABLE failed: %v", err)
	}
}

func TestSQLExecutor_CreateProcedureAndExec(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	// Create table first
	_, _ = server.ExecuteSQL(`CREATE TABLE wallets (id INT PRIMARY KEY, balance INT);`)
	_, _ = server.ExecuteSQL(`INSERT INTO wallets (id, balance) VALUES (1, 100), (2, 50);`)

	// CREATE PROCEDURE
	procSQL := `
CREATE PROCEDURE TransferCoins
    @from INT,
    @to INT,
    @amt INT
AS
BEGIN
    UPDATE wallets SET balance = balance - @amt WHERE id = @from;
    UPDATE wallets SET balance = balance + @amt WHERE id = @to;
    RETURN 0;
END;`
	res, err := server.ExecuteSQL(procSQL)
	if err != nil || !res.Success {
		t.Fatalf("CREATE PROCEDURE failed: %v (res: %+v)", err, res)
	}

	// EXEC
	execSQL := `EXEC TransferCoins 1, 2, 30;`
	res, err = server.ExecuteSQL(execSQL)
	if err != nil || !res.Success {
		t.Fatalf("EXEC failed: %v (res: %+v)", err, res)
	}

	// Verify wallet balances
	selRes, _ := server.ExecuteSQL(`SELECT * FROM wallets ORDER BY id ASC;`)
	if len(selRes.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(selRes.Rows))
	}
	if selRes.Rows[0]["balance"] != int64(70) || selRes.Rows[1]["balance"] != int64(80) {
		t.Fatalf("expected balances 70 and 80, got: %+v", selRes.Rows)
	}
}

func TestSQLExecutor_CreateIndexAndScan(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	_, _ = server.ExecuteSQL(`CREATE TABLE members (id INT PRIMARY KEY, username VARCHAR(50), score INT);`)
	_, _ = server.ExecuteSQL(`INSERT INTO members (id, username, score) VALUES (1, 'alice', 95), (2, 'bob', 80), (3, 'charlie', 95);`)

	// 1. CREATE UNIQUE INDEX
	res, err := server.ExecuteSQL(`CREATE UNIQUE INDEX idx_members_username ON members (username);`)
	if err != nil || !res.Success {
		t.Fatalf("CREATE UNIQUE INDEX failed: %v", err)
	}

	// 2. CREATE NON-UNIQUE INDEX
	res, err = server.ExecuteSQL(`CREATE INDEX idx_members_score ON members (score);`)
	if err != nil || !res.Success {
		t.Fatalf("CREATE INDEX failed: %v", err)
	}

	// 3. Query using index
	res, err = server.ExecuteSQL(`SELECT * FROM members WHERE username = 'alice';`)
	if err != nil || !res.Success {
		t.Fatalf("SELECT with indexed username failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["username"] != "alice" {
		t.Fatalf("expected 1 row for alice, got %+v", res.Rows)
	}

	// 4. DROP INDEX
	res, err = server.ExecuteSQL(`DROP INDEX idx_members_score ON members;`)
	if err != nil || !res.Success {
		t.Fatalf("DROP INDEX failed: %v", err)
	}
}

func TestSQLExecutor_TemporaryTables(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	// 1. CREATE TABLE #temp_summary
	res, err := server.ExecuteSQL(`CREATE TABLE #temp_summary (
		id INT PRIMARY KEY,
		metric VARCHAR(50),
		total INT
	);`)
	if err != nil || !res.Success {
		t.Fatalf("CREATE TABLE #temp_summary failed: %v", err)
	}

	// 2. INSERT INTO #temp_summary
	res, err = server.ExecuteSQL(`INSERT INTO #temp_summary (id, metric, total) VALUES (1, 'tps', 75000), (2, 'latency', 12);`)
	if err != nil || !res.Success || res.RowsAffected != 2 {
		t.Fatalf("INSERT into #temp_summary failed: %v", err)
	}

	// 3. SELECT * FROM #temp_summary
	res, err = server.ExecuteSQL(`SELECT * FROM #temp_summary WHERE total > 50;`)
	if err != nil || !res.Success || len(res.Rows) != 1 {
		t.Fatalf("SELECT from #temp_summary failed: %v (rows: %+v)", err, res.Rows)
	}
	if res.Rows[0]["metric"] != "tps" {
		t.Errorf("expected tps row, got: %+v", res.Rows[0])
	}

	// 4. DROP TABLE #temp_summary
	res, err = server.ExecuteSQL(`DROP TABLE #temp_summary;`)
	if err != nil || !res.Success {
		t.Fatalf("DROP TABLE #temp_summary failed: %v", err)
	}
}

func TestSQLExecutor_TableValuedFunction(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	_, _ = server.ExecuteSQL(`CREATE TABLE bank_accounts (id INT PRIMARY KEY, holder VARCHAR(50), balance INT);`)
	_, _ = server.ExecuteSQL(`INSERT INTO bank_accounts (id, holder, balance) VALUES (1, 'Alice', 5000), (2, 'Bob', 1200), (3, 'Charlie', 300);`)

	// 1. Register Inline TVF
	tvfSQL := `CREATE FUNCTION GetHighBalanceAccounts(@min_bal INT)
	RETURNS TABLE
	AS
	RETURN (
		SELECT id, holder, balance FROM bank_accounts WHERE balance >= @min_bal ORDER BY id ASC
	);`
	res, err := server.ExecuteSQL(tvfSQL)
	if err != nil || !res.Success {
		t.Fatalf("CREATE FUNCTION (TVF) failed: %v", err)
	}

	// 2. Query from TVF: SELECT * FROM GetHighBalanceAccounts(1000)
	querySQL := `SELECT * FROM GetHighBalanceAccounts(1000);`
	res, err = server.ExecuteSQL(querySQL)
	if err != nil || !res.Success {
		t.Fatalf("SELECT from TVF failed: %v (res: %+v)", err, res)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 accounts with balance >= 1000, got %d (rows: %+v)", len(res.Rows), res.Rows)
	}
	if res.Rows[0]["holder"] != "Alice" || res.Rows[1]["holder"] != "Bob" {
		t.Errorf("unexpected rows returned from TVF: %+v", res.Rows)
	}
}

func TestSQLExecutor_ExplainAndExplainAnalyze(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	_, _ = server.ExecuteSQL(`CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(50), score INT);`)
	_, _ = server.ExecuteSQL(`CREATE INDEX idx_users_score ON users(score);`)
	_, _ = server.ExecuteSQL(`INSERT INTO users (id, name, score) VALUES (1, 'Alice', 95), (2, 'Bob', 80), (3, 'Charlie', 60);`)

	// 1. EXPLAIN SELECT with Seq Scan
	res, err := server.ExecuteSQL(`EXPLAIN SELECT id, name FROM users WHERE name = 'Alice';`)
	if err != nil || !res.Success {
		t.Fatalf("EXPLAIN SELECT failed: %v", err)
	}
	if len(res.Rows) == 0 || res.Columns[0] != "QUERY PLAN" {
		t.Fatalf("unexpected EXPLAIN result structure: %+v", res)
	}
	planText := ""
	for _, r := range res.Rows {
		planText += r["QUERY PLAN"].(string) + "\n"
	}
	if !strings.Contains(planText, "Seq Scan on users") {
		t.Errorf("expected Seq Scan in EXPLAIN text, got:\n%s", planText)
	}

	// 2. EXPLAIN SELECT with Index Scan
	res, err = server.ExecuteSQL(`EXPLAIN SELECT id, name FROM users WHERE score = 95;`)
	if err != nil || !res.Success {
		t.Fatalf("EXPLAIN index scan failed: %v", err)
	}
	planText = ""
	for _, r := range res.Rows {
		planText += r["QUERY PLAN"].(string) + "\n"
	}
	if !strings.Contains(planText, "Index Scan using idx_users_score on users") {
		t.Errorf("expected Index Scan in EXPLAIN text, got:\n%s", planText)
	}

	// 3. EXPLAIN ANALYZE SELECT
	res, err = server.ExecuteSQL(`EXPLAIN ANALYZE SELECT id, name FROM users ORDER BY score DESC LIMIT 2;`)
	if err != nil || !res.Success {
		t.Fatalf("EXPLAIN ANALYZE failed: %v", err)
	}
	planText = ""
	for _, r := range res.Rows {
		planText += r["QUERY PLAN"].(string) + "\n"
	}
	if !strings.Contains(planText, "Limit") || !strings.Contains(planText, "Sort") || !strings.Contains(planText, "actual time=") {
		t.Errorf("expected Limit, Sort and actual metrics in EXPLAIN ANALYZE text, got:\n%s", planText)
	}
}

func TestSQLExecutor_HashJoin_InnerAndLeft(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	// Create tables
	_, _ = server.ExecuteSQL(`CREATE TABLE customers (id INT PRIMARY KEY, name VARCHAR(50), city VARCHAR(50));`)
	_, _ = server.ExecuteSQL(`CREATE TABLE orders (id INT PRIMARY KEY, cust_id INT, amount INT);`)

	_, _ = server.ExecuteSQL(`INSERT INTO customers (id, name, city) VALUES (1, 'Alice', 'Seoul'), (2, 'Bob', 'Busan'), (3, 'Charlie', 'Incheon');`)
	_, _ = server.ExecuteSQL(`INSERT INTO orders (id, cust_id, amount) VALUES (101, 1, 500), (102, 1, 300), (103, 2, 700);`)

	// 1. INNER JOIN
	innerSQL := `SELECT c.name, o.amount FROM customers c INNER JOIN orders o ON c.id = o.cust_id WHERE o.amount >= 500 ORDER BY o.amount DESC;`
	res, err := server.ExecuteSQL(innerSQL)
	if err != nil || !res.Success {
		t.Fatalf("INNER JOIN failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 joined rows, got %d: %+v", len(res.Rows), res.Rows)
	}
	if res.Rows[0]["name"] != "Bob" || res.Rows[0]["amount"] != int64(700) {
		t.Errorf("expected Bob with 700 first, got: %+v", res.Rows[0])
	}
	if res.Rows[1]["name"] != "Alice" || res.Rows[1]["amount"] != int64(500) {
		t.Errorf("expected Alice with 500 second, got: %+v", res.Rows[1])
	}

	// 2. LEFT JOIN
	leftSQL := `SELECT c.name, o.amount FROM customers c LEFT JOIN orders o ON c.id = o.cust_id ORDER BY c.id ASC;`
	res, err = server.ExecuteSQL(leftSQL)
	if err != nil || !res.Success {
		t.Fatalf("LEFT JOIN failed: %v", err)
	}
	// Alice has 2 orders, Bob has 1 order, Charlie has 0 orders -> total 4 rows
	if len(res.Rows) != 4 {
		t.Fatalf("expected 4 left joined rows, got %d: %+v", len(res.Rows), res.Rows)
	}
	charlieRow := res.Rows[3]
	if charlieRow["name"] != "Charlie" || charlieRow["amount"] != nil {
		t.Errorf("expected Charlie with NULL amount, got: %+v", charlieRow)
	}

	// 3. EXPLAIN Hash Join
	explainSQL := `EXPLAIN SELECT c.name, o.amount FROM customers c INNER JOIN orders o ON c.id = o.cust_id;`
	res, err = server.ExecuteSQL(explainSQL)
	if err != nil || !res.Success {
		t.Fatalf("EXPLAIN Hash Join failed: %v", err)
	}
	planText := ""
	for _, r := range res.Rows {
		planText += r["QUERY PLAN"].(string) + "\n"
	}
	if !strings.Contains(planText, "Hash Join") || !strings.Contains(planText, "Hash") {
		t.Errorf("expected Hash Join in plan, got:\n%s", planText)
	}
}

func TestSQLExecutor_IndexNestedLoopJoin(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	// Create tables and index on orders(cust_id)
	_, _ = server.ExecuteSQL(`CREATE TABLE customers (id INT PRIMARY KEY, name VARCHAR(50));`)
	_, _ = server.ExecuteSQL(`CREATE TABLE orders (id INT PRIMARY KEY, cust_id INT, item VARCHAR(50));`)
	_, _ = server.ExecuteSQL(`CREATE INDEX idx_orders_cust ON orders(cust_id);`)

	_, _ = server.ExecuteSQL(`INSERT INTO customers (id, name) VALUES (1, 'Alice'), (2, 'Bob'), (3, 'Charlie');`)
	_, _ = server.ExecuteSQL(`INSERT INTO orders (id, cust_id, item) VALUES (101, 1, 'Book'), (102, 1, 'Pen'), (103, 2, 'Laptop');`)

	// 1. EXPLAIN shows Nested Loop
	explainSQL := `EXPLAIN SELECT c.name, o.item FROM customers c JOIN orders o ON c.id = o.cust_id;`
	res, err := server.ExecuteSQL(explainSQL)
	if err != nil || !res.Success {
		t.Fatalf("EXPLAIN failed: %v", err)
	}
	planText := ""
	for _, r := range res.Rows {
		planText += r["QUERY PLAN"].(string) + "\n"
	}
	if !strings.Contains(planText, "Nested Loop") || !strings.Contains(planText, "Index Scan using idx_orders_cust on orders") {
		t.Errorf("expected Nested Loop with Index Scan in plan, got:\n%s", planText)
	}

	// 2. Execute Nested Loop Join
	querySQL := `SELECT c.name, o.item FROM customers c JOIN orders o ON c.id = o.cust_id ORDER BY o.id ASC;`
	res, err = server.ExecuteSQL(querySQL)
	if err != nil || !res.Success {
		t.Fatalf("Nested Loop Join query failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 joined rows, got %d: %+v", len(res.Rows), res.Rows)
	}
	if res.Rows[0]["item"] != "Book" || res.Rows[1]["item"] != "Pen" || res.Rows[2]["item"] != "Laptop" {
		t.Errorf("unexpected rows returned: %+v", res.Rows)
	}
}

func TestSQLExecutor_GroupByAndHaving(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	_, _ = server.ExecuteSQL(`CREATE TABLE employees (id INT PRIMARY KEY, department VARCHAR(50), salary INT);`)
	_, _ = server.ExecuteSQL(`INSERT INTO employees (id, department, salary) VALUES 
		(1, 'Engineering', 90000),
		(2, 'Engineering', 110000),
		(3, 'Engineering', 80000),
		(4, 'HR', 55000),
		(5, 'HR', 65000),
		(6, 'Sales', 70000);`)

	// 1. GROUP BY with COUNT, SUM, AVG, MIN, MAX
	groupSQL := `SELECT department, COUNT(*) AS emp_count, SUM(salary) AS total_salary, AVG(salary) AS avg_salary, MIN(salary) AS min_sal, MAX(salary) AS max_sal 
		FROM employees 
		GROUP BY department 
		ORDER BY total_salary DESC;`
	res, err := server.ExecuteSQL(groupSQL)
	if err != nil || !res.Success {
		t.Fatalf("GROUP BY failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 department groups, got %d: %+v", len(res.Rows), res.Rows)
	}
	// Engineering should be first with total 280000
	engRow := res.Rows[0]
	if engRow["department"] != "Engineering" || engRow["emp_count"] != int64(3) || engRow["total_salary"] != int64(280000) {
		t.Errorf("unexpected Engineering aggregate row: %+v", engRow)
	}

	// 2. GROUP BY with HAVING filter
	havingSQL := `SELECT department, COUNT(*) AS emp_count, SUM(salary) AS total_salary 
		FROM employees 
		GROUP BY department 
		HAVING COUNT(*) >= 2 
		ORDER BY emp_count DESC;`
	res, err = server.ExecuteSQL(havingSQL)
	if err != nil || !res.Success {
		t.Fatalf("GROUP BY HAVING failed: %v", err)
	}
	// Only Engineering (3) and HR (2) have >= 2 employees
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 groups passing HAVING, got %d: %+v", len(res.Rows), res.Rows)
	}
	if res.Rows[0]["department"] != "Engineering" || res.Rows[1]["department"] != "HR" {
		t.Errorf("unexpected HAVING filtered rows: %+v", res.Rows)
	}

	// 3. Global Aggregation without GROUP BY
	globalSQL := `SELECT COUNT(*) AS total_emps, SUM(salary) AS all_salaries, AVG(salary) AS mean_sal FROM employees;`
	res, err = server.ExecuteSQL(globalSQL)
	if err != nil || !res.Success {
		t.Fatalf("Global Aggregation failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 global aggregate row, got %d", len(res.Rows))
	}
	gRow := res.Rows[0]
	if gRow["total_emps"] != int64(6) || gRow["all_salaries"] != int64(470000) {
		t.Errorf("unexpected global aggregate row: %+v", gRow)
	}
}

func TestSQLExecutor_CTE_Basic(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	_, _ = server.ExecuteSQL(`CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(50), role VARCHAR(50), dept_id INT);`)
	_, _ = server.ExecuteSQL(`INSERT INTO users (id, name, role, dept_id) VALUES 
		(1, 'Alice', 'admin', 10),
		(2, 'Bob', 'dev', 10),
		(3, 'Charlie', 'admin', 20),
		(4, 'Dave', 'designer', 20);`)

	// 1. Single CTE
	cteSQL := `WITH admin_users AS (
		SELECT id, name FROM users WHERE role = 'admin'
	)
	SELECT name FROM admin_users ORDER BY id ASC;`

	res, err := server.ExecuteSQL(cteSQL)
	if err != nil || !res.Success {
		t.Fatalf("CTE execution failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 admin users, got %d: %+v", len(res.Rows), res.Rows)
	}
	if res.Rows[0]["name"] != "Alice" || res.Rows[1]["name"] != "Charlie" {
		t.Errorf("unexpected CTE rows: %+v", res.Rows)
	}

	// 2. Multiple CTEs joined
	multiCteSQL := `WITH admins AS (
		SELECT id, name, dept_id FROM users WHERE role = 'admin'
	), devs AS (
		SELECT id, name, dept_id FROM users WHERE role = 'dev'
	)
	SELECT a.name AS admin_name, d.name AS dev_name 
	FROM admins a 
	JOIN devs d ON a.dept_id = d.dept_id;`

	res, err = server.ExecuteSQL(multiCteSQL)
	if err != nil || !res.Success {
		t.Fatalf("Multiple CTE execution failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 joined CTE row, got %d: %+v", len(res.Rows), res.Rows)
	}
	if res.Rows[0]["admin_name"] != "Alice" || res.Rows[0]["dev_name"] != "Bob" {
		t.Errorf("unexpected multi-CTE row: %+v", res.Rows)
	}
}

func TestSQLExecutor_Subqueries_Scalar_In_Exists(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	_, _ = server.ExecuteSQL(`CREATE TABLE depts (id INT PRIMARY KEY, name VARCHAR(50));`)
	_, _ = server.ExecuteSQL(`CREATE TABLE emps (id INT PRIMARY KEY, name VARCHAR(50), dept_id INT, salary INT);`)

	_, _ = server.ExecuteSQL(`INSERT INTO depts (id, name) VALUES (1, 'Engineering'), (2, 'Sales'), (3, 'Marketing');`)
	_, _ = server.ExecuteSQL(`INSERT INTO emps (id, name, dept_id, salary) VALUES 
		(101, 'Alice', 1, 95000),
		(102, 'Bob', 1, 85000),
		(103, 'Charlie', 2, 70000);`)

	// 1. WHERE IN (SELECT ...)
	inSubquerySQL := `SELECT name FROM emps WHERE dept_id IN (SELECT id FROM depts WHERE name = 'Engineering') ORDER BY id ASC;`
	res, err := server.ExecuteSQL(inSubquerySQL)
	if err != nil || !res.Success {
		t.Fatalf("WHERE IN subquery failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 emps in Engineering, got %d: %+v", len(res.Rows), res.Rows)
	}
	if res.Rows[0]["name"] != "Alice" || res.Rows[1]["name"] != "Bob" {
		t.Errorf("unexpected rows for IN subquery: %+v", res.Rows)
	}

	// 2. WHERE EXISTS (SELECT ...)
	existsSQL := `SELECT name FROM depts WHERE EXISTS (SELECT 1 FROM emps WHERE dept_id = 1) ORDER BY id ASC;`
	res, err = server.ExecuteSQL(existsSQL)
	if err != nil || !res.Success {
		t.Fatalf("WHERE EXISTS subquery failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 depts because dept 1 has emps, got %d: %+v", len(res.Rows), res.Rows)
	}

	notExistsSQL := `SELECT name FROM depts WHERE NOT EXISTS (SELECT 1 FROM emps WHERE dept_id = 999);`
	res, err = server.ExecuteSQL(notExistsSQL)
	if err != nil || !res.Success {
		t.Fatalf("WHERE NOT EXISTS subquery failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 depts because dept 999 does not exist, got %d", len(res.Rows))
	}

	// 3. Scalar Subquery in WHERE: WHERE salary > (SELECT AVG(salary) FROM emps)
	scalarSQL := `SELECT name, salary FROM emps WHERE salary > (SELECT AVG(salary) FROM emps);`
	res, err = server.ExecuteSQL(scalarSQL)
	if err != nil || !res.Success {
		t.Fatalf("Scalar subquery failed: %v", err)
	}
	// Avg is (95000+85000+70000)/3 = 83333. Alice (95000) and Bob (85000) are > 83333.
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 emps above avg salary, got %d: %+v", len(res.Rows), res.Rows)
	}
}

func TestSQLExecutor_SessionTransactions_CommitAndRollback(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	_, err := server.ExecuteSQL(`CREATE TABLE bank_accounts (id INT PRIMARY KEY, owner VARCHAR(50), balance INT);`)
	if err != nil {
		t.Fatalf("create table failed: %v", err)
	}

	// 1. Transaction Commit
	sessA := "session-A"
	_, err = server.ExecuteSessionSQL(sessA, `BEGIN TRANSACTION;`)
	if err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	_, err = server.ExecuteSessionSQL(sessA, `INSERT INTO bank_accounts (id, owner, balance) VALUES (1, 'Alice', 1000);`)
	if err != nil {
		t.Fatalf("INSERT in tx failed: %v", err)
	}
	_, err = server.ExecuteSessionSQL(sessA, `UPDATE bank_accounts SET balance = 1200 WHERE id = 1;`)
	if err != nil {
		t.Fatalf("UPDATE in tx failed: %v", err)
	}
	_, err = server.ExecuteSessionSQL(sessA, `COMMIT;`)
	if err != nil {
		t.Fatalf("COMMIT failed: %v", err)
	}

	// Verify committed row
	res, err := server.ExecuteSQL(`SELECT balance FROM bank_accounts WHERE id = 1;`)
	if err != nil || len(res.Rows) != 1 || res.Rows[0]["balance"] != int64(1200) {
		t.Fatalf("expected balance 1200, got: %+v", res)
	}

	// 2. Transaction Rollback
	sessB := "session-B"
	_, err = server.ExecuteSessionSQL(sessB, `BEGIN;`)
	if err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	_, err = server.ExecuteSessionSQL(sessB, `INSERT INTO bank_accounts (id, owner, balance) VALUES (2, 'Bob', 500);`)
	if err != nil {
		t.Fatalf("INSERT in tx failed: %v", err)
	}
	_, err = server.ExecuteSessionSQL(sessB, `ROLLBACK;`)
	if err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// Verify row 2 does not exist
	res, err = server.ExecuteSQL(`SELECT id FROM bank_accounts WHERE id = 2;`)
	if err != nil || len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows after rollback, got: %+v", res)
	}
}

func TestSQLExecutor_SessionVariables(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	_, _ = server.ExecuteSQL(`CREATE TABLE members (id INT PRIMARY KEY, name VARCHAR(50), tier VARCHAR(20), points INT);`)
	_, _ = server.ExecuteSQL(`INSERT INTO members (id, name, tier, points) VALUES 
		(1, 'Alice', 'gold', 500),
		(2, 'Bob', 'silver', 200),
		(3, 'Charlie', 'gold', 800);`)

	sess1 := "sess-user-1"
	// 1. SET and SELECT variable
	_, err := server.ExecuteSessionSQL(sess1, `SET @min_pts = 300;`)
	if err != nil {
		t.Fatalf("SET variable failed: %v", err)
	}
	_, err = server.ExecuteSessionSQL(sess1, `SET @target_tier = 'gold';`)
	if err != nil {
		t.Fatalf("SET variable failed: %v", err)
	}

	varRes, err := server.ExecuteSessionSQL(sess1, `SELECT @target_tier AS active_tier;`)
	if err != nil || len(varRes.Rows) != 1 || varRes.Rows[0]["active_tier"] != "gold" {
		t.Fatalf("SELECT @var failed: %+v, err: %v", varRes, err)
	}

	// 2. Query with substituted session variables
	querySQL := `SELECT name, points FROM members WHERE tier = @target_tier AND points >= @min_pts ORDER BY id ASC;`
	res, err := server.ExecuteSessionSQL(sess1, querySQL)
	if err != nil || !res.Success {
		t.Fatalf("query with session variables failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 gold members with >= 300 points, got %d: %+v", len(res.Rows), res.Rows)
	}
	if res.Rows[0]["name"] != "Alice" || res.Rows[1]["name"] != "Charlie" {
		t.Errorf("unexpected rows: %+v", res.Rows)
	}

	// 3. Multi-statement script execution with semicolon
	scriptSQL := `
		SET @bonus = 50;
		UPDATE members SET points = 550 WHERE id = 1;
		SELECT points FROM members WHERE id = 1;
	`
	scriptRes, err := server.ExecuteSessionSQL(sess1, scriptSQL)
	if err != nil || !scriptRes.Success {
		t.Fatalf("script execution failed: %v", err)
	}
	if len(scriptRes.Rows) != 1 || scriptRes.Rows[0]["points"] != int64(550) {
		t.Fatalf("unexpected script result: %+v", scriptRes)
	}
}

func TestSQLExecutor_VirtualCatalogViews(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	// Create test tables and procedures
	_, _ = server.ExecuteSQL(`CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(50) NOT NULL, age INT);`)
	_, _ = server.ExecuteSQL(`CREATE TABLE orders (order_id INT PRIMARY KEY, user_id INT, amount FLOAT);`)
	_, _ = server.ExecuteSQL(`CREATE INDEX idx_user_name ON users (name);`)

	// 1. Query sys.tables
	res, err := server.ExecuteSQL(`SELECT table_name, column_count FROM sys.tables ORDER BY table_name ASC;`)
	if err != nil || !res.Success {
		t.Fatalf("query sys.tables failed: %v", err)
	}
	if len(res.Rows) < 2 {
		t.Fatalf("expected at least 2 tables in sys.tables, got %d", len(res.Rows))
	}
	foundUsers := false
	for _, r := range res.Rows {
		if r["table_name"] == "users" {
			foundUsers = true
		}
	}
	if !foundUsers {
		t.Errorf("users table not found in sys.tables")
	}

	// 2. Query information_schema.tables with WHERE
	res, err = server.ExecuteSQL(`SELECT table_name, table_type FROM information_schema.tables WHERE table_name = 'orders';`)
	if err != nil || !res.Success {
		t.Fatalf("query information_schema.tables failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["table_name"] != "orders" {
		t.Fatalf("unexpected information_schema.tables result: %+v", res.Rows)
	}

	// 3. Query sys.columns
	res, err = server.ExecuteSQL(`SELECT column_name, data_type, is_nullable FROM sys.columns WHERE table_name = 'users' ORDER BY ordinal_position ASC;`)
	if err != nil || !res.Success {
		t.Fatalf("query sys.columns failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 columns for users table, got %d: %+v", len(res.Rows), res.Rows)
	}
	if res.Rows[0]["column_name"] != "id" || res.Rows[1]["column_name"] != "name" {
		t.Errorf("unexpected columns: %+v", res.Rows)
	}

	// 4. Query sys.indexes
	res, err = server.ExecuteSQL(`SELECT index_name, table_name FROM sys.indexes WHERE table_name = 'users';`)
	if err != nil || !res.Success {
		t.Fatalf("query sys.indexes failed: %v", err)
	}
	if len(res.Rows) < 1 || res.Rows[0]["index_name"] != "idx_user_name" {
		t.Fatalf("expected idx_user_name in sys.indexes, got: %+v", res.Rows)
	}

	// 5. Query sys.databases
	res, err = server.ExecuteSQL(`SELECT name, database_id, state_desc FROM sys.databases;`)
	if err != nil || !res.Success {
		t.Fatalf("query sys.databases failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "sqlion" {
		t.Fatalf("unexpected sys.databases: %+v", res.Rows)
	}
}

func TestSQLExecutor_AnalyzeTableAndStats(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	// Create table and insert data
	_, err := server.ExecuteSQL(`CREATE TABLE items (id INT PRIMARY KEY, category VARCHAR(20), price FLOAT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	for i := 1; i <= 20; i++ {
		catStr := "books"
		if i > 10 {
			catStr = "electronics"
		}
		price := float64(i * 10)
		_, err = server.ExecuteSQL(fmt.Sprintf("INSERT INTO items (id, category, price) VALUES (%d, '%s', %f);", i, catStr, price))
		if err != nil {
			t.Fatalf("INSERT row %d failed: %v", i, err)
		}
	}

	// 1. Run ANALYZE TABLE items
	res, err := server.ExecuteSQL(`ANALYZE TABLE items;`)
	if err != nil || !res.Success {
		t.Fatalf("ANALYZE TABLE items failed: %v", err)
	}
	if len(res.Messages) == 0 {
		t.Fatalf("expected message in ANALYZE result")
	}

	// 2. Query sys.table_stats
	res, err = server.ExecuteSQL(`SELECT table_name, row_count FROM sys.table_stats WHERE table_name = 'items';`)
	if err != nil || !res.Success {
		t.Fatalf("query sys.table_stats failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row in sys.table_stats, got %d", len(res.Rows))
	}
	if res.Rows[0]["row_count"] != int64(20) {
		t.Errorf("expected row_count 20, got %v", res.Rows[0]["row_count"])
	}

	// 3. Query sys.column_stats
	res, err = server.ExecuteSQL(`SELECT table_name, column_name, distinct_count, histogram_buckets FROM sys.column_stats WHERE table_name = 'items' AND column_name = 'category';`)
	if err != nil || !res.Success {
		t.Fatalf("query sys.column_stats failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row in sys.column_stats, got %d", len(res.Rows))
	}
	if res.Rows[0]["distinct_count"] != int64(2) {
		t.Errorf("expected distinct_count 2 for category, got %v", res.Rows[0]["distinct_count"])
	}
	if buckets, ok := res.Rows[0]["histogram_buckets"].(int); !ok || buckets <= 0 {
		t.Errorf("expected histogram_buckets > 0, got %v", res.Rows[0]["histogram_buckets"])
	}

	// 4. Verify EXPLAIN leverages stats
	res, err = server.ExecuteSQL(`EXPLAIN SELECT * FROM items WHERE category = 'books';`)
	if err != nil || !res.Success {
		t.Fatalf("EXPLAIN failed: %v", err)
	}
	if len(res.Rows) == 0 {
		t.Fatalf("expected rows from EXPLAIN")
	}

	// 5. Verify Volcano query execution uses stats without error
	res, err = server.ExecuteSQL(`SELECT id, category, price FROM items WHERE price > 150.0 ORDER BY id ASC;`)
	if err != nil || !res.Success {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 5 {
		t.Errorf("expected 5 rows for price > 150, got %d", len(res.Rows))
	}
}

func TestSQLExecutor_WorkMemGovernorAndSpill(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	// 1. Check default work_mem
	res, err := server.ExecuteSessionSQL("sess1", `SHOW work_mem;`)
	if err != nil || !res.Success {
		t.Fatalf("SHOW work_mem failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["work_mem"] != "64MB" {
		t.Errorf("expected default 64MB, got: %+v", res.Rows)
	}

	// 2. Set work_mem on sess1
	res, err = server.ExecuteSessionSQL("sess1", `SET work_mem = '128MB';`)
	if err != nil || !res.Success {
		t.Fatalf("SET work_mem failed: %v", err)
	}

	// Verify on sess1
	res, err = server.ExecuteSessionSQL("sess1", `SHOW work_mem;`)
	if err != nil || !res.Success || res.Rows[0]["work_mem"] != "128MB" {
		t.Fatalf("expected 128MB on sess1, got: %+v", res.Rows)
	}

	// Verify session isolation: sess2 should still see default 64MB
	res, err = server.ExecuteSessionSQL("sess2", `SHOW work_mem;`)
	if err != nil || !res.Success || res.Rows[0]["work_mem"] != "64MB" {
		t.Fatalf("expected 64MB on sess2, got: %+v", res.Rows)
	}

	// 3. Test query with tight work_mem forcing spill to disk
	_, _ = server.ExecuteSQL(`CREATE TABLE logs (id INT PRIMARY KEY, score INT, msg VARCHAR(100));`)
	for i := 1; i <= 100; i++ {
		val := (i * 17) % 50
		_, _ = server.ExecuteSQL(fmt.Sprintf("INSERT INTO logs (id, score, msg) VALUES (%d, %d, 'log-message-%d');", i, val, i))
	}

	// Set tight work_mem on sess1 to trigger External Merge Sort
	_, err = server.ExecuteSessionSQL("sess1", `SET work_mem = '2kB';`)
	if err != nil {
		t.Fatalf("SET 2kB failed: %v", err)
	}

	res, err = server.ExecuteSessionSQL("sess1", `SELECT id, score, msg FROM logs ORDER BY score ASC, id ASC;`)
	if err != nil || !res.Success {
		t.Fatalf("ORDER BY query failed under tight work_mem: %v", err)
	}
	if len(res.Rows) != 100 {
		t.Fatalf("expected 100 rows, got %d", len(res.Rows))
	}

	// Verify ordering correctness
	for i := 1; i < len(res.Rows); i++ {
		sPrev := res.Rows[i-1]["score"]
		sCur := res.Rows[i]["score"]
		var vPrev, vCur int64
		fmt.Sscanf(fmt.Sprintf("%v", sPrev), "%d", &vPrev)
		fmt.Sscanf(fmt.Sprintf("%v", sCur), "%d", &vCur)
		if vCur < vPrev {
			t.Fatalf("ordering violated at row %d: %d < %d", i, vCur, vPrev)
		}
	}
}


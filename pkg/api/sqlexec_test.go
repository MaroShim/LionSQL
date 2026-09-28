package api_test

import (
	"fmt"
	"strings"
	"testing"

	"sqlion/pkg/api"
	"sqlion/pkg/catalog"
	"sqlion/pkg/storage"
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

func TestSQLExecutor_ScalarSelect_SessionSettings_VirtualCatalog(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	// 1. Table-less scalar SELECT
	res, err := server.ExecuteSQL("SELECT 1;")
	if err != nil || !res.Success || len(res.Rows) != 1 {
		t.Fatalf("SELECT 1 failed: %v", err)
	}

	res, err = server.ExecuteSQL("SELECT 1 AS num, current_database() AS db, current_schema() AS schema, version() AS ver;")
	if err != nil || !res.Success {
		t.Fatalf("scalar functions SELECT failed: %v", err)
	}
	if fmt.Sprintf("%v", res.Rows[0]["num"]) != "1" {
		t.Fatalf("expected num=1, got %v", res.Rows[0]["num"])
	}
	if res.Rows[0]["db"] != "sqlion" {
		t.Fatalf("expected db=sqlion, got %v", res.Rows[0]["db"])
	}
	if res.Rows[0]["schema"] != "dbo" {
		t.Fatalf("expected schema=dbo, got %v", res.Rows[0]["schema"])
	}

	// 2. MSSQL system variables in scalar SELECT
	res, err = server.ExecuteSQL("SELECT @@VERSION AS ver, @@SPID AS spid, @@TRANCOUNT AS tc, @@ROWCOUNT AS rc;")
	if err != nil || !res.Success {
		t.Fatalf("MSSQL scalar variables SELECT failed: %v", err)
	}
	if !strings.Contains(fmt.Sprintf("%v", res.Rows[0]["ver"]), "Microsoft SQL Server") {
		t.Fatalf("expected MSSQL @@VERSION, got %v", res.Rows[0]["ver"])
	}
	if fmt.Sprintf("%v", res.Rows[0]["spid"]) != "51" {
		t.Fatalf("expected @@SPID=51, got %v", res.Rows[0]["spid"])
	}

	// 3. SET options
	setQueries := []string{
		"SET NOCOUNT ON;",
		"SET ANSI_NULLS ON;",
		"SET QUOTED_IDENTIFIER ON;",
		"SET IMPLICIT_TRANSACTIONS OFF;",
		"SET TRANSACTION ISOLATION LEVEL READ COMMITTED;",
		"SET TIME ZONE 'UTC';",
		"SET NAMES 'utf8mb4';",
		"SET extra_float_digits = 3;",
		"DISCARD ALL;",
		"RESET ALL;",
	}
	for _, q := range setQueries {
		res, err = server.ExecuteSessionSQL("sess1", q)
		if err != nil || !res.Success {
			t.Fatalf("SET query %q failed: %v", q, err)
		}
	}

	// 4. SHOW parameters
	showQueries := []struct {
		query    string
		col      string
		expected string
	}{
		{"SHOW search_path;", "search_path", `"$user", public`},
		{"SHOW transaction_isolation;", "transaction_isolation", "read committed"},
		{"SHOW timezone;", "timezone", "UTC"},
		{"SHOW server_version;", "server_version", "15.0 (SQLion)"},
		{"SHOW max_connections;", "max_connections", "100"},
	}
	for _, sq := range showQueries {
		res, err = server.ExecuteSessionSQL("sess1", sq.query)
		if err != nil || !res.Success {
			t.Fatalf("SHOW query %q failed: %v", sq.query, err)
		}
		if fmt.Sprintf("%v", res.Rows[0][sq.col]) != sq.expected {
			t.Fatalf("query %q: expected %s, got %v", sq.query, sq.expected, res.Rows[0][sq.col])
		}
	}

	// 5. Create a table to verify virtual catalog views
	_, _ = server.ExecuteSQL("CREATE TABLE products (id INT PRIMARY KEY, title VARCHAR(50), price FLOAT);")

	// 6. pg_catalog views
	res, err = server.ExecuteSQL("SELECT datname, oid FROM pg_catalog.pg_database;")
	if err != nil || !res.Success || len(res.Rows) != 1 || res.Rows[0]["datname"] != "sqlion" {
		t.Fatalf("pg_database query failed: %v, rows: %+v", err, res.Rows)
	}

	res, err = server.ExecuteSQL("SELECT nspname FROM pg_catalog.pg_namespace;")
	if err != nil || !res.Success || len(res.Rows) < 4 {
		t.Fatalf("pg_namespace query failed: %v, rows: %+v", err, res.Rows)
	}

	res, err = server.ExecuteSQL("SELECT tablename FROM pg_catalog.pg_tables WHERE tablename = 'products';")
	if err != nil || !res.Success || len(res.Rows) != 1 {
		t.Fatalf("pg_tables query failed: %v, rows: %+v", err, res.Rows)
	}

	res, err = server.ExecuteSQL("SELECT relname FROM pg_catalog.pg_class WHERE relname = 'products';")
	if err != nil || !res.Success || len(res.Rows) != 1 {
		t.Fatalf("pg_class query failed: %v, rows: %+v", err, res.Rows)
	}

	// 7. sys views
	res, err = server.ExecuteSQL("SELECT name FROM sys.schemas WHERE name = 'dbo';")
	if err != nil || !res.Success || len(res.Rows) != 1 {
		t.Fatalf("sys.schemas query failed: %v, rows: %+v", err, res.Rows)
	}

	res, err = server.ExecuteSQL("SELECT name, type_desc FROM sys.objects WHERE name = 'products';")
	if err != nil || !res.Success || len(res.Rows) != 1 {
		t.Fatalf("sys.objects query failed: %v, rows: %+v", err, res.Rows)
	}

	// 8. Bracketed and quoted schema table queries
	res, err = server.ExecuteSQL("SELECT * FROM [sys].[tables];")
	if err != nil || !res.Success || len(res.Rows) < 1 {
		t.Fatalf("bracketed sys.tables query failed: %v", err)
	}

	res, err = server.ExecuteSQL("SELECT * FROM \"pg_catalog\".\"pg_tables\";")
	if err != nil || !res.Success || len(res.Rows) < 1 {
		t.Fatalf("quoted pg_tables query failed: %v", err)
	}
}

func TestSQLExecutor_BuiltinSystemStoredProcedures(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	// Create test table
	_, _ = server.ExecuteSQL("CREATE TABLE inventory (id INT PRIMARY KEY, sku VARCHAR(50), qty INT);")
	_, _ = server.ExecuteSQL("INSERT INTO inventory (id, sku, qty) VALUES (1, 'SKU-A', 100), (2, 'SKU-B', 200);")

	// 1. sp_help without arguments
	res, err := server.ExecuteSQL("EXEC sp_help;")
	if err != nil || !res.Success || len(res.Rows) < 1 {
		t.Fatalf("EXEC sp_help failed: %v", err)
	}

	// 2. sp_help with table name
	res, err = server.ExecuteSQL("EXEC sp_help inventory;")
	if err != nil || !res.Success || len(res.Rows) != 3 {
		t.Fatalf("EXEC sp_help inventory failed: %v, rows: %+v", err, res.Rows)
	}
	if res.Rows[0]["Column_name"] != "id" || res.Rows[0]["PrimaryKey"] != true {
		t.Fatalf("unexpected sp_help column detail: %+v", res.Rows[0])
	}

	// 3. sp_spaceused (database and table)
	res, err = server.ExecuteSQL("EXEC sp_spaceused;")
	if err != nil || !res.Success || len(res.Rows) != 1 || res.Rows[0]["database_name"] != "sqlion" {
		t.Fatalf("EXEC sp_spaceused failed: %v", err)
	}

	res, err = server.ExecuteSQL("EXEC sp_spaceused inventory;")
	if err != nil || !res.Success || len(res.Rows) != 1 || res.Rows[0]["rows"] != "2" {
		t.Fatalf("EXEC sp_spaceused inventory failed: %v, rows: %+v", err, res.Rows)
	}

	// 4. sp_who
	res, err = server.ExecuteSQL("EXEC sp_who;")
	if err != nil || !res.Success || len(res.Rows) < 1 {
		t.Fatalf("EXEC sp_who failed: %v", err)
	}

	// 5. sp_lock
	res, err = server.ExecuteSQL("EXEC sp_lock;")
	if err != nil || !res.Success || len(res.Rows) < 1 {
		t.Fatalf("EXEC sp_lock failed: %v", err)
	}

	// 6. sp_rename column: inventory.sku -> item_code
	res, err = server.ExecuteSQL("EXEC sp_rename 'inventory.sku', 'item_code', 'COLUMN';")
	if err != nil || !res.Success {
		t.Fatalf("EXEC sp_rename column failed: %v", err)
	}

	// Verify column rename succeeded
	res, err = server.ExecuteSQL("SELECT id, item_code, qty FROM inventory WHERE id = 1;")
	if err != nil || !res.Success || len(res.Rows) != 1 || res.Rows[0]["item_code"] != "SKU-A" {
		t.Fatalf("query after column rename failed: %v, rows: %+v", err, res.Rows)
	}

	// 7. sp_rename table: inventory -> stock_items
	res, err = server.ExecuteSQL("EXEC sp_rename 'inventory', 'stock_items';")
	if err != nil || !res.Success {
		t.Fatalf("EXEC sp_rename table failed: %v", err)
	}

	// Verify table rename succeeded
	res, err = server.ExecuteSQL("SELECT id, item_code, qty FROM stock_items WHERE id = 2;")
	if err != nil || !res.Success || len(res.Rows) != 1 || res.Rows[0]["item_code"] != "SKU-B" {
		t.Fatalf("query after table rename failed: %v, rows: %+v", err, res.Rows)
	}

	// 8. Direct invocation without EXEC: sp_help stock_items
	res, err = server.ExecuteSQL("sp_help stock_items;")
	if err != nil || !res.Success || len(res.Rows) != 3 {
		t.Fatalf("direct sp_help stock_items failed: %v", err)
	}
}

func TestSQLExecutor_ErrorAndIdentityInsert(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)
	sessID := "test_identity_sess"

	// 1. Initial @@ERROR should be 0
	res, err := server.ExecuteSessionSQL(sessID, "SELECT @@ERROR;")
	if err != nil || !res.Success {
		t.Fatalf("SELECT @@ERROR failed: %v", err)
	}
	if fmt.Sprintf("%v", res.Rows[0]["@@error"]) != "0" {
		t.Fatalf("expected @@ERROR=0, got: %+v", res.Rows[0])
	}

	// 2. Erroneous statement should set @@ERROR to 50000
	_, err = server.ExecuteSessionSQL(sessID, "SELECT * FROM non_existent_table_xyz;")
	if err == nil {
		t.Fatalf("expected error from non-existent table, got nil")
	}

	// Immediate next query should see @@ERROR = 50000
	res, err = server.ExecuteSessionSQL(sessID, "SELECT @@ERROR;")
	if err != nil || !res.Success {
		t.Fatalf("SELECT @@ERROR after error failed: %v", err)
	}
	if fmt.Sprintf("%v", res.Rows[0]["@@error"]) != "50000" {
		t.Fatalf("expected @@ERROR=50000, got: %+v", res.Rows[0])
	}

	// And subsequent query should see @@ERROR reset to 0
	res, err = server.ExecuteSessionSQL(sessID, "SELECT @@ERROR;")
	if err != nil || !res.Success {
		t.Fatalf("SELECT @@ERROR reset failed: %v", err)
	}
	if fmt.Sprintf("%v", res.Rows[0]["@@error"]) != "0" {
		t.Fatalf("expected @@ERROR reset to 0, got: %+v", res.Rows[0])
	}

	// 3. Create table with IDENTITY column
	_, err = server.ExecuteSessionSQL(sessID, `CREATE TABLE employees (
		id INT IDENTITY(1,1) PRIMARY KEY,
		name VARCHAR(50) NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE with IDENTITY failed: %v", err)
	}

	// 4. Auto-increment insert
	res, err = server.ExecuteSessionSQL(sessID, "INSERT INTO employees (name) VALUES ('Alice');")
	if err != nil || !res.Success {
		t.Fatalf("INSERT into identity table failed: %v", err)
	}

	res, err = server.ExecuteSessionSQL(sessID, "SELECT @@IDENTITY, SCOPE_IDENTITY();")
	if err != nil || !res.Success {
		t.Fatalf("SELECT @@IDENTITY failed: %v", err)
	}
	if fmt.Sprintf("%v", res.Rows[0]["@@identity"]) != "1" {
		t.Fatalf("expected @@IDENTITY=1, got: %+v", res.Rows[0])
	}

	// Insert multi-row
	res, err = server.ExecuteSessionSQL(sessID, "INSERT INTO employees (name) VALUES ('Bob'), ('Charlie');")
	if err != nil || !res.Success {
		t.Fatalf("multi-row insert failed: %v", err)
	}
	res, err = server.ExecuteSessionSQL(sessID, "SELECT @@IDENTITY;")
	if err != nil || !res.Success {
		t.Fatalf("SELECT @@IDENTITY failed: %v", err)
	}
	if fmt.Sprintf("%v", res.Rows[0]["@@identity"]) != "3" {
		t.Fatalf("expected @@IDENTITY=3, got: %+v", res.Rows[0])
	}

	// 5. Explicit insert with IDENTITY_INSERT OFF should fail
	_, err = server.ExecuteSessionSQL(sessID, "INSERT INTO employees (id, name) VALUES (99, 'Dave');")
	if err == nil {
		t.Fatalf("expected error inserting explicit value when IDENTITY_INSERT is OFF, got nil")
	}
	if !strings.Contains(err.Error(), "IDENTITY_INSERT is set to OFF") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// Verify @@ERROR is 50000
	res, _ = server.ExecuteSessionSQL(sessID, "SELECT @@ERROR;")
	if fmt.Sprintf("%v", res.Rows[0]["@@error"]) != "50000" {
		t.Fatalf("expected @@ERROR=50000 after failed insert, got: %+v", res.Rows[0])
	}

	// 6. Turn SET IDENTITY_INSERT ON
	res, err = server.ExecuteSessionSQL(sessID, "SET IDENTITY_INSERT employees ON;")
	if err != nil || !res.Success {
		t.Fatalf("SET IDENTITY_INSERT ON failed: %v", err)
	}

	// Now explicit insert succeeds
	res, err = server.ExecuteSessionSQL(sessID, "INSERT INTO employees (id, name) VALUES (100, 'Dave');")
	if err != nil || !res.Success {
		t.Fatalf("explicit insert with IDENTITY_INSERT ON failed: %v", err)
	}
	res, _ = server.ExecuteSessionSQL(sessID, "SELECT @@IDENTITY;")
	if fmt.Sprintf("%v", res.Rows[0]["@@identity"]) != "100" {
		t.Fatalf("expected @@IDENTITY=100, got: %+v", res.Rows[0])
	}

	// 7. Turn SET IDENTITY_INSERT OFF
	res, err = server.ExecuteSessionSQL(sessID, "SET IDENTITY_INSERT employees OFF;")
	if err != nil || !res.Success {
		t.Fatalf("SET IDENTITY_INSERT OFF failed: %v", err)
	}

	// Next auto-increment should start after watermark (101)
	res, err = server.ExecuteSessionSQL(sessID, "INSERT INTO employees (name) VALUES ('Eve');")
	if err != nil || !res.Success {
		t.Fatalf("insert after watermark failed: %v", err)
	}
	res, _ = server.ExecuteSessionSQL(sessID, "SELECT @@IDENTITY;")
	if fmt.Sprintf("%v", res.Rows[0]["@@identity"]) != "101" {
		t.Fatalf("expected @@IDENTITY=101, got: %+v", res.Rows[0])
	}

	// Verify all rows in employees
	res, err = server.ExecuteSessionSQL(sessID, "SELECT id, name FROM employees ORDER BY id ASC;")
	if err != nil || !res.Success || len(res.Rows) != 5 {
		t.Fatalf("SELECT all employees failed: %v, rows: %+v", err, res.Rows)
	}
	expectedIDs := []string{"1", "2", "3", "100", "101"}
	for i, exp := range expectedIDs {
		if fmt.Sprintf("%v", res.Rows[i]["id"]) != exp {
			t.Fatalf("row %d expected id %s, got: %+v", i, exp, res.Rows[i])
		}
	}

	// 8. Error validations: Cannot enable IDENTITY_INSERT on table without identity
	_, err = server.ExecuteSessionSQL(sessID, "CREATE TABLE plain (x INT PRIMARY KEY);")
	if err != nil {
		t.Fatalf("CREATE TABLE plain failed: %v", err)
	}
	_, err = server.ExecuteSessionSQL(sessID, "SET IDENTITY_INSERT plain ON;")
	if err == nil || !strings.Contains(err.Error(), "does not have the identity property") {
		t.Fatalf("expected identity property error, got: %v", err)
	}
}

func TestSQLExecutor_GlobalTemporaryTables(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	sess1 := "session_alpha"
	sess2 := "session_beta"

	// 1. Session 1 creates a global temporary table
	_, err := server.ExecuteSessionSQL(sess1, `CREATE TABLE ##shared_accounts (
		id INT PRIMARY KEY,
		owner VARCHAR(50) NOT NULL,
		balance FLOAT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE ##shared_accounts failed: %v", err)
	}

	// 2. Session 1 inserts a row
	res, err := server.ExecuteSessionSQL(sess1, "INSERT INTO ##shared_accounts (id, owner, balance) VALUES (1, 'Alice', 1000.0);")
	if err != nil || !res.Success {
		t.Fatalf("INSERT into ##shared_accounts from sess1 failed: %v", err)
	}

	// 3. Session 2 queries and sees Session 1's global temporary table
	res, err = server.ExecuteSessionSQL(sess2, "SELECT id, owner, balance FROM ##shared_accounts WHERE id = 1;")
	if err != nil || !res.Success || len(res.Rows) != 1 {
		t.Fatalf("SELECT from ##shared_accounts on sess2 failed: %v, rows: %+v", err, res.Rows)
	}
	if res.Rows[0]["owner"] != "Alice" || fmt.Sprintf("%v", res.Rows[0]["balance"]) != "1000" {
		t.Fatalf("unexpected row from sess2: %+v", res.Rows[0])
	}

	// 4. Session 2 inserts a row
	res, err = server.ExecuteSessionSQL(sess2, "INSERT INTO ##shared_accounts (id, owner, balance) VALUES (2, 'Bob', 2500.0);")
	if err != nil || !res.Success {
		t.Fatalf("INSERT into ##shared_accounts from sess2 failed: %v", err)
	}

	// 5. Session 1 sees Bob's row and verifies case-insensitivity (##SHARED_ACCOUNTS)
	res, err = server.ExecuteSessionSQL(sess1, "SELECT id, owner FROM ##SHARED_ACCOUNTS ORDER BY id ASC;")
	if err != nil || !res.Success || len(res.Rows) != 2 {
		t.Fatalf("SELECT case-insensitive ##SHARED_ACCOUNTS failed: %v, rows: %+v", err, res.Rows)
	}
	if res.Rows[1]["owner"] != "Bob" {
		t.Fatalf("expected Bob in second row, got: %+v", res.Rows[1])
	}

	// 6. Session 2 updates a row
	res, err = server.ExecuteSessionSQL(sess2, "UPDATE ##shared_accounts SET balance = 1200.0 WHERE id = 1;")
	if err != nil || !res.Success || res.RowsAffected != 1 {
		t.Fatalf("UPDATE ##shared_accounts from sess2 failed: %v", err)
	}

	// 7. Introspection via sp_help
	res, err = server.ExecuteSessionSQL(sess2, "EXEC sp_help '##shared_accounts';")
	if err != nil || !res.Success || len(res.Rows) != 3 {
		t.Fatalf("sp_help on ##shared_accounts failed: %v, rows: %+v", err, res.Rows)
	}

	// 8. Session 1 disconnects / closes -> should automatically drop ##shared_accounts
	server.CloseSession(sess1)

	// 9. Session 2 now querying ##shared_accounts should fail with table not found
	_, err = server.ExecuteSessionSQL(sess2, "SELECT * FROM ##shared_accounts;")
	if err == nil {
		t.Fatalf("expected error querying ##shared_accounts after creator session closed, got nil")
	}

	// 10. Explicit DROP TABLE on global temporary table
	_, err = server.ExecuteSessionSQL(sess2, "CREATE TABLE ##temp_to_drop (id INT PRIMARY KEY);")
	if err != nil {
		t.Fatalf("CREATE TABLE ##temp_to_drop failed: %v", err)
	}
	res, err = server.ExecuteSessionSQL(sess2, "DROP TABLE ##temp_to_drop;")
	if err != nil || !res.Success {
		t.Fatalf("DROP TABLE ##temp_to_drop failed: %v", err)
	}
	_, err = server.ExecuteSessionSQL(sess2, "SELECT * FROM ##temp_to_drop;")
	if err == nil {
		t.Fatalf("expected table not found after DROP TABLE, got nil")
	}
}

func TestSQLExecutor_DBCCCommands(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)
	sessID := "dbcc_sess"

	// Setup a sample table
	_, err := server.ExecuteSessionSQL(sessID, "CREATE TABLE inventory (id INT PRIMARY KEY, name VARCHAR(50), qty INT);")
	if err != nil {
		t.Fatalf("CREATE TABLE inventory failed: %v", err)
	}
	_, err = server.ExecuteSessionSQL(sessID, "INSERT INTO inventory (id, name, qty) VALUES (1, 'Widget', 10), (2, 'Gadget', 25);")
	if err != nil {
		t.Fatalf("INSERT into inventory failed: %v", err)
	}

	// 1. DBCC USEROPTIONS
	res, err := server.ExecuteSessionSQL(sessID, "DBCC USEROPTIONS;")
	if err != nil || !res.Success {
		t.Fatalf("DBCC USEROPTIONS failed: %v", err)
	}
	if len(res.Rows) < 5 {
		t.Fatalf("expected at least 5 useroptions, got %d", len(res.Rows))
	}
	foundIsolation := false
	for _, r := range res.Rows {
		if r["Set Option"] == "isolation level" {
			foundIsolation = true
			if r["Value"] != "read committed" {
				t.Errorf("expected default read committed, got %v", r["Value"])
			}
		}
	}
	if !foundIsolation {
		t.Errorf("isolation level not found in DBCC USEROPTIONS")
	}

	// Change isolation level and re-check DBCC USEROPTIONS
	_, _ = server.ExecuteSessionSQL(sessID, "SET TRANSACTION ISOLATION LEVEL SERIALIZABLE;")
	res, _ = server.ExecuteSessionSQL(sessID, "DBCC USEROPTIONS;")
	for _, r := range res.Rows {
		if r["Set Option"] == "isolation level" && r["Value"] != "serializable" {
			t.Errorf("expected updated serializable, got %v", r["Value"])
		}
	}

	// 2. DBCC CHECKTABLE ('inventory')
	res, err = server.ExecuteSessionSQL(sessID, "DBCC CHECKTABLE ('inventory');")
	if err != nil || !res.Success {
		t.Fatalf("DBCC CHECKTABLE failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["Table"] != "inventory" || fmt.Sprintf("%v", res.Rows[0]["Rows"]) != "2" {
		t.Fatalf("unexpected DBCC CHECKTABLE results: %+v", res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["ConsistencyErrors"]) != "0" {
		t.Fatalf("expected 0 consistency errors, got: %+v", res.Rows[0])
	}

	// Test non-existent table in CHECKTABLE
	_, err = server.ExecuteSessionSQL(sessID, "DBCC CHECKTABLE ('non_existent');")
	if err == nil {
		t.Fatalf("expected error for non_existent table in CHECKTABLE, got nil")
	}

	// 3. DBCC CHECKDB
	res, err = server.ExecuteSessionSQL(sessID, "DBCC CHECKDB;")
	if err != nil || !res.Success {
		t.Fatalf("DBCC CHECKDB failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["Database"] != "sqlion" {
		t.Fatalf("unexpected DBCC CHECKDB results: %+v", res.Rows)
	}
	msgJoined := strings.Join(res.Messages, " ")
	if !strings.Contains(msgJoined, "CHECKDB found 0 allocation errors and 0 consistency errors") {
		t.Fatalf("missing expected CHECKDB message in: %s", msgJoined)
	}

	// 4. DBCC SHRINKDATABASE
	res, err = server.ExecuteSessionSQL(sessID, "DBCC SHRINKDATABASE (sqlion);")
	if err != nil || !res.Success {
		t.Fatalf("DBCC SHRINKDATABASE failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["DbId"] != int64(1) {
		t.Fatalf("unexpected DBCC SHRINKDATABASE results: %+v", res.Rows)
	}
}

func TestSQLExecutor_TableVariables(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	sessA := "session_a"
	sessB := "session_b"

	// 1. Declare table variable in Session A
	declA := "DECLARE @users TABLE (id INT PRIMARY KEY, name VARCHAR(50), balance FLOAT);"
	res, err := server.ExecuteSessionSQL(sessA, declA)
	if err != nil || !res.Success {
		t.Fatalf("failed to declare table variable in Session A: %v", err)
	}

	// 2. Insert into @users in Session A
	insA := "INSERT INTO @users VALUES (1, 'Alice', 100.5), (2, 'Bob', 250.0);"
	res, err = server.ExecuteSessionSQL(sessA, insA)
	if err != nil || !res.Success {
		t.Fatalf("failed to insert into @users: %v", err)
	}

	// 3. Select from @users
	selA := "SELECT id, name, balance FROM @users WHERE id = 1;"
	res, err = server.ExecuteSessionSQL(sessA, selA)
	if err != nil || !res.Success {
		t.Fatalf("failed to select from @users: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Alice" {
		t.Fatalf("unexpected select result from @users: %+v", res.Rows)
	}

	// 4. Update @users
	updA := "UPDATE @users SET balance = 150.0 WHERE id = 1;"
	res, err = server.ExecuteSessionSQL(sessA, updA)
	if err != nil || !res.Success {
		t.Fatalf("failed to update @users: %v", err)
	}

	// 5. Delete from @users
	delA := "DELETE FROM @users WHERE id = 2;"
	res, err = server.ExecuteSessionSQL(sessA, delA)
	if err != nil || !res.Success {
		t.Fatalf("failed to delete from @users: %v", err)
	}

	// 6. Verify row count
	res, err = server.ExecuteSessionSQL(sessA, "SELECT COUNT(*) AS total FROM @users;")
	if err != nil || !res.Success {
		t.Fatalf("failed to count @users: %v", err)
	}
	if fmt.Sprintf("%v", res.Rows[0]["total"]) != "1" {
		t.Fatalf("expected count 1, got %+v", res.Rows)
	}

	// 7. Multi-session isolation: Session B declares @users with different schema
	declB := "DECLARE @users TABLE (id INT PRIMARY KEY, score INT);"
	res, err = server.ExecuteSessionSQL(sessB, declB)
	if err != nil || !res.Success {
		t.Fatalf("failed to declare table variable in Session B: %v", err)
	}
	_, err = server.ExecuteSessionSQL(sessB, "INSERT INTO @users VALUES (1, 999);")
	if err != nil {
		t.Fatalf("failed to insert in Session B: %v", err)
	}

	resB, err := server.ExecuteSessionSQL(sessB, "SELECT score FROM @users WHERE id = 1;")
	if err != nil || len(resB.Rows) != 1 || fmt.Sprintf("%v", resB.Rows[0]["score"]) != "999" {
		t.Fatalf("unexpected Session B result: %+v", resB.Rows)
	}

	// Session A remains unaffected
	resA, err := server.ExecuteSessionSQL(sessA, "SELECT name FROM @users WHERE id = 1;")
	if err != nil || len(resA.Rows) != 1 || resA.Rows[0]["name"] != "Alice" {
		t.Fatalf("Session A corrupted by Session B: %+v", resA.Rows)
	}

	// 8. Close session and verify cleanup
	server.CloseSession(sessA)
	_, err = server.ExecuteSessionSQL(sessA, "SELECT * FROM @users;")
	// Since session was closed and table dropped, @users is now undefined
	if err == nil {
		t.Fatalf("expected error querying dropped table variable, got success")
	}
}

func TestSQLExecutor_SelectInto(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	sessID := "sess_select_into"

	// 1. Create source table and insert seed data
	_, err := server.ExecuteSessionSQL(sessID, `CREATE TABLE employees (
		id INT PRIMARY KEY,
		name VARCHAR(50),
		dept VARCHAR(50),
		salary FLOAT
	);`)
	if err != nil {
		t.Fatalf("failed creating employees: %v", err)
	}

	_, err = server.ExecuteSessionSQL(sessID, `INSERT INTO employees VALUES
		(1, 'Alice', 'Engineering', 95000.0),
		(2, 'Bob', 'HR', 65000.0),
		(3, 'Charlie', 'Engineering', 105000.0),
		(4, 'Diana', 'Marketing', 75000.0);`)
	if err != nil {
		t.Fatalf("failed inserting employees: %v", err)
	}

	// 2. SELECT ... INTO #temp_eng FROM employees WHERE dept = 'Engineering'
	res, err := server.ExecuteSessionSQL(sessID, "SELECT id, name, salary INTO #temp_eng FROM employees WHERE dept = 'Engineering';")
	if err != nil || !res.Success {
		t.Fatalf("SELECT INTO temp table failed: %v", err)
	}
	if res.RowsAffected != 2 {
		t.Fatalf("expected 2 rows affected, got %d", res.RowsAffected)
	}

	// 3. Query from #temp_eng
	selRes, err := server.ExecuteSessionSQL(sessID, "SELECT * FROM #temp_eng ORDER BY id ASC;")
	if err != nil || !selRes.Success {
		t.Fatalf("SELECT from #temp_eng failed: %v", err)
	}
	if len(selRes.Rows) != 2 {
		t.Fatalf("expected 2 rows in #temp_eng, got %d", len(selRes.Rows))
	}
	if selRes.Rows[0]["name"] != "Alice" || selRes.Rows[1]["name"] != "Charlie" {
		t.Fatalf("unexpected data in #temp_eng: %+v", selRes.Rows)
	}

	// 4. Duplicate SELECT INTO should fail because target table already exists
	_, err = server.ExecuteSessionSQL(sessID, "SELECT id, name INTO #temp_eng FROM employees;")
	if err == nil {
		t.Fatalf("expected error creating existing table via SELECT INTO, got nil")
	}

	// 5. SELECT ... INTO permanent table
	res, err = server.ExecuteSessionSQL(sessID, "SELECT * INTO backup_employees FROM employees;")
	if err != nil || !res.Success {
		t.Fatalf("SELECT INTO permanent table failed: %v", err)
	}
	if res.RowsAffected != 4 {
		t.Fatalf("expected 4 rows affected, got %d", res.RowsAffected)
	}

	selBkp, err := server.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS total FROM backup_employees;")
	if err != nil || len(selBkp.Rows) != 1 || fmt.Sprintf("%v", selBkp.Rows[0]["total"]) != "4" {
		t.Fatalf("unexpected backup count: %+v", selBkp)
	}

	// 6. SELECT ... INTO ##global_temp
	res, err = server.ExecuteSessionSQL(sessID, "SELECT id, name INTO ##global_eng FROM employees WHERE id = 1;")
	if err != nil || !res.Success {
		t.Fatalf("SELECT INTO global temp failed: %v", err)
	}
	resGlobal, err := server.ExecuteSessionSQL("other_sess", "SELECT name FROM ##global_eng;")
	if err != nil || len(resGlobal.Rows) != 1 || resGlobal.Rows[0]["name"] != "Alice" {
		t.Fatalf("failed reading global temp table from other session: %+v", resGlobal)
	}

	// 7. Verify session cleanup of #temp_eng
	server.CloseSession(sessID)
	_, err = server.ExecuteSessionSQL(sessID, "SELECT * FROM #temp_eng;")
	if err == nil {
		t.Fatalf("expected error querying dropped #temp_eng after session close, got nil")
	}
}

func TestSQLExecutor_TruncateTable(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	sessID := "sess_truncate"

	// 1. Setup table with secondary index
	_, err := server.ExecuteSessionSQL(sessID, `CREATE TABLE products (
		id INT PRIMARY KEY,
		name VARCHAR(50),
		price FLOAT
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE products failed: %v", err)
	}

	_, err = server.ExecuteSessionSQL(sessID, `CREATE INDEX idx_prod_name ON products (name);`)
	if err != nil {
		t.Fatalf("CREATE INDEX failed: %v", err)
	}

	_, err = server.ExecuteSessionSQL(sessID, `INSERT INTO products VALUES
		(1, 'Laptop', 1200.0),
		(2, 'Phone', 800.0),
		(3, 'Tablet', 400.0);`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	res, err := server.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS total FROM products;")
	if err != nil || fmt.Sprintf("%v", res.Rows[0]["total"]) != "3" {
		t.Fatalf("expected 3 rows before truncate, got: %+v", res)
	}

	// 2. TRUNCATE TABLE products
	truncRes, err := server.ExecuteSessionSQL(sessID, "TRUNCATE TABLE products;")
	if err != nil || !truncRes.Success {
		t.Fatalf("TRUNCATE TABLE products failed: %v", err)
	}

	// 3. Verify table is empty
	res, err = server.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS total FROM products;")
	if err != nil || fmt.Sprintf("%v", res.Rows[0]["total"]) != "0" {
		t.Fatalf("expected 0 rows after truncate, got: %+v", res)
	}

	// 4. Verify index is empty
	idxRes, err := server.ExecuteSessionSQL(sessID, "SELECT * FROM products WHERE name = 'Laptop';")
	if err != nil || len(idxRes.Rows) != 0 {
		t.Fatalf("expected empty index lookup after truncate, got: %+v", idxRes.Rows)
	}

	// 5. Re-insert after truncate (verifies primary key and index reuse without collision)
	_, err = server.ExecuteSessionSQL(sessID, "INSERT INTO products VALUES (1, 'New Laptop', 1500.0);")
	if err != nil {
		t.Fatalf("failed inserting after truncate: %v", err)
	}
	res, err = server.ExecuteSessionSQL(sessID, "SELECT name, price FROM products WHERE id = 1;")
	if err != nil || len(res.Rows) != 1 || res.Rows[0]["name"] != "New Laptop" {
		t.Fatalf("unexpected row after re-insert: %+v", res.Rows)
	}

	// 6. Foreign key protection: cannot truncate table referenced by foreign key
	_, err = server.ExecuteSessionSQL(sessID, `CREATE TABLE categories (
		cat_id INT PRIMARY KEY,
		cat_name VARCHAR(50)
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE categories failed: %v", err)
	}

	_, err = server.ExecuteSessionSQL(sessID, `CREATE TABLE items (
		item_id INT PRIMARY KEY,
		cat_id INT,
		FOREIGN KEY (cat_id) REFERENCES categories (cat_id)
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE items with FK failed: %v", err)
	}

	// Attempt to truncate categories (referenced by items)
	_, err = server.ExecuteSessionSQL(sessID, "TRUNCATE TABLE categories;")
	if err == nil {
		t.Fatalf("expected error truncating table referenced by FK, got nil")
	}

	// 7. Truncate temporary table
	_, err = server.ExecuteSessionSQL(sessID, "SELECT * INTO #temp_prod FROM products;")
	if err != nil {
		t.Fatalf("SELECT INTO #temp_prod failed: %v", err)
	}
	res, err = server.ExecuteSessionSQL(sessID, "TRUNCATE TABLE #temp_prod;")
	if err != nil || !res.Success {
		t.Fatalf("TRUNCATE TABLE #temp_prod failed: %v", err)
	}
	res, err = server.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS total FROM #temp_prod;")
	if err != nil || fmt.Sprintf("%v", res.Rows[0]["total"]) != "0" {
		t.Fatalf("expected 0 rows in truncated temp table, got: %+v", res)
	}
}

func TestSQLExecutor_BuiltinFunctions(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	// 1. STUFF
	res, err := server.ExecuteSQL("SELECT STUFF('abcdef', 2, 3, 'xyz') AS result;")
	if err != nil || len(res.Rows) != 1 || res.Rows[0]["result"] != "axyzef" {
		t.Fatalf("STUFF failed: %+v, err: %v", res, err)
	}

	// 2. CHARINDEX
	res, err = server.ExecuteSQL("SELECT CHARINDEX('server', 'SQL server 2000') AS idx;")
	if err != nil || len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["idx"]) != "5" {
		t.Fatalf("CHARINDEX failed: %+v, err: %v", res, err)
	}

	// 3. REPLACE
	res, err = server.ExecuteSQL("SELECT REPLACE('abcdef', 'cd', 'XX') AS rep;")
	if err != nil || len(res.Rows) != 1 || res.Rows[0]["rep"] != "abXXef" {
		t.Fatalf("REPLACE failed: %+v, err: %v", res, err)
	}

	// 4. REVERSE
	res, err = server.ExecuteSQL("SELECT REVERSE('Turbo') AS rev;")
	if err != nil || len(res.Rows) != 1 || res.Rows[0]["rev"] != "obruT" {
		t.Fatalf("REVERSE failed: %+v, err: %v", res, err)
	}

	// 5. DATEADD with unquoted datepart
	res, err = server.ExecuteSQL("SELECT DATEADD(day, 7, '2026-03-01 12:00:00') AS dt;")
	if err != nil || len(res.Rows) != 1 || res.Rows[0]["dt"] != "2026-03-08 12:00:00" {
		t.Fatalf("DATEADD failed: %+v, err: %v", res, err)
	}

	// 6. DATEDIFF with unquoted datepart
	res, err = server.ExecuteSQL("SELECT DATEDIFF(day, '2026-01-01', '2026-01-20') AS diff;")
	if err != nil || len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["diff"]) != "19" {
		t.Fatalf("DATEDIFF failed: %+v, err: %v", res, err)
	}
}

func TestSQLExecutor_ExplainBytecode(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	query := "EXPLAIN BYTECODE SELECT id, name FROM users WHERE id > 10"
	res, err := server.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("ExecuteSQL EXPLAIN BYTECODE failed: %v", err)
	}

	if !res.Success {
		t.Fatalf("expected success, got false")
	}
	if len(res.Columns) != 1 || res.Columns[0] != "Bytecode" {
		t.Fatalf("expected Column 'Bytecode', got %v", res.Columns)
	}
	if len(res.Rows) == 0 {
		t.Fatalf("expected bytecode rows, got empty")
	}

	foundRelQuery := false
	for _, row := range res.Rows {
		line := fmt.Sprintf("%v", row["Bytecode"])
		if strings.Contains(line, "REL_QUERY") {
			foundRelQuery = true
			break
		}
	}
	if !foundRelQuery {
		t.Errorf("expected REL_QUERY opcode in bytecode disassembly, rows: %+v", res.Rows)
	}
}

func TestSQLExecutor_VBDEDML(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)
	sessID := "vdbe_dml_sess"

	// 1. Setup table
	_, err := server.ExecuteSessionSQL(sessID, "CREATE TABLE products (id INT PRIMARY KEY, name VARCHAR(50), price INT);")
	if err != nil {
		t.Fatalf("CREATE TABLE products failed: %v", err)
	}

	// 2. EXPLAIN BYTECODE for DML
	res, err := server.ExecuteSessionSQL(sessID, "EXPLAIN BYTECODE INSERT INTO products (id, name, price) VALUES (1, 'Widget', 100);")
	if err != nil || !res.Success {
		t.Fatalf("EXPLAIN BYTECODE INSERT failed: %v", err)
	}
	foundInsertOp := false
	for _, row := range res.Rows {
		if strings.Contains(fmt.Sprintf("%v", row["Bytecode"]), "INSERT") {
			foundInsertOp = true
			break
		}
	}
	if !foundInsertOp {
		t.Errorf("expected INSERT opcode in bytecode, got %+v", res.Rows)
	}

	res, err = server.ExecuteSessionSQL(sessID, "EXPLAIN BYTECODE UPDATE products SET price = 200 WHERE id = 1;")
	if err != nil || !res.Success {
		t.Fatalf("EXPLAIN BYTECODE UPDATE failed: %v", err)
	}
	foundUpdateOp := false
	for _, row := range res.Rows {
		if strings.Contains(fmt.Sprintf("%v", row["Bytecode"]), "UPDATE") {
			foundUpdateOp = true
			break
		}
	}
	if !foundUpdateOp {
		t.Errorf("expected UPDATE opcode in bytecode, got %+v", res.Rows)
	}

	res, err = server.ExecuteSessionSQL(sessID, "EXPLAIN BYTECODE DELETE FROM products WHERE id = 1;")
	if err != nil || !res.Success {
		t.Fatalf("EXPLAIN BYTECODE DELETE failed: %v", err)
	}
	foundDeleteOp := false
	for _, row := range res.Rows {
		if strings.Contains(fmt.Sprintf("%v", row["Bytecode"]), "DELETE") {
			foundDeleteOp = true
			break
		}
	}
	if !foundDeleteOp {
		t.Errorf("expected DELETE opcode in bytecode, got %+v", res.Rows)
	}

	// 3. Execute single INSERT via VDBE
	res, err = server.ExecuteSessionSQL(sessID, "INSERT INTO products (id, name, price) VALUES (10, 'Alpha', 500);")
	if err != nil || !res.Success {
		t.Fatalf("VDBE INSERT failed: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("expected 1 row affected, got %d", res.RowsAffected)
	}

	// Check @@ROWCOUNT
	res, err = server.ExecuteSessionSQL(sessID, "SELECT @@ROWCOUNT AS rc;")
	if err != nil || !res.Success || len(res.Rows) == 0 {
		t.Fatalf("SELECT @@ROWCOUNT failed: %v", err)
	}
	if fmt.Sprintf("%v", res.Rows[0]["rc"]) != "1" {
		t.Errorf("expected @@ROWCOUNT = 1, got %v", res.Rows[0]["rc"])
	}

	// 4. Multi-row INSERT via VDBE
	res, err = server.ExecuteSessionSQL(sessID, "INSERT INTO products (id, name, price) VALUES (11, 'Beta', 600), (12, 'Gamma', 700);")
	if err != nil || !res.Success {
		t.Fatalf("VDBE Multi-row INSERT failed: %v", err)
	}
	if res.RowsAffected != 2 {
		t.Errorf("expected 2 rows affected, got %d", res.RowsAffected)
	}

	// 5. UPDATE via VDBE
	res, err = server.ExecuteSessionSQL(sessID, "UPDATE products SET price = 550 WHERE id = 10;")
	if err != nil || !res.Success {
		t.Fatalf("VDBE UPDATE failed: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("expected 1 row affected on UPDATE, got %d", res.RowsAffected)
	}

	// 6. DELETE via VDBE
	res, err = server.ExecuteSessionSQL(sessID, "DELETE FROM products WHERE id = 11;")
	if err != nil || !res.Success {
		t.Fatalf("VDBE DELETE failed: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("expected 1 row affected on DELETE, got %d", res.RowsAffected)
	}

	// 7. Verify remaining rows
	res, err = server.ExecuteSessionSQL(sessID, "SELECT id, name, price FROM products ORDER BY id;")
	if err != nil || !res.Success {
		t.Fatalf("SELECT products failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 remaining products, got %d: %+v", len(res.Rows), res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["price"]) != "550" {
		t.Errorf("expected updated price 550 for id 10, got %v", res.Rows[0]["price"])
	}
	if fmt.Sprintf("%v", res.Rows[1]["id"]) != "12" {
		t.Errorf("expected remaining product id 12, got %v", res.Rows[1]["id"])
	}
}

func TestSQLExecutor_MultiTableChainedJoins(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	// 1. Setup tables
	_, err := server.ExecuteSQL("CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(50));")
	if err != nil {
		t.Fatalf("CREATE users failed: %v", err)
	}
	_, err = server.ExecuteSQL("CREATE TABLE orders (id INT PRIMARY KEY, user_id INT, amount FLOAT);")
	if err != nil {
		t.Fatalf("CREATE orders failed: %v", err)
	}
	_, err = server.ExecuteSQL("CREATE TABLE items (id INT PRIMARY KEY, order_id INT, product VARCHAR(50));")
	if err != nil {
		t.Fatalf("CREATE items failed: %v", err)
	}

	// 2. Insert records
	_, _ = server.ExecuteSQL("INSERT INTO users VALUES (1, 'Alice'), (2, 'Bob');")
	_, _ = server.ExecuteSQL("INSERT INTO orders VALUES (101, 1, 50.0), (102, 1, 150.0), (103, 2, 20.0);")
	_, _ = server.ExecuteSQL("INSERT INTO items VALUES (1001, 101, 'Book'), (1002, 101, 'Pen'), (1003, 102, 'Laptop');")

	// 3. EXPLAIN 3-table chained join
	explainSQL := "EXPLAIN SELECT u.name, o.amount, i.product FROM users u JOIN orders o ON u.id = o.user_id JOIN items i ON o.id = i.order_id;"
	expRes, err := server.ExecuteSQL(explainSQL)
	if err != nil || !expRes.Success {
		t.Fatalf("EXPLAIN multi-join failed: %v", err)
	}
	if len(expRes.Rows) == 0 {
		t.Fatalf("expected EXPLAIN output rows, got 0")
	}

	// 4. Execute INNER JOIN chain
	innerSQL := "SELECT u.name, o.amount, i.product FROM users u JOIN orders o ON u.id = o.user_id JOIN items i ON o.id = i.order_id ORDER BY i.product;"
	res, err := server.ExecuteSQL(innerSQL)
	if err != nil || !res.Success {
		t.Fatalf("ExecuteSQL multi-join failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows from inner join, got %d: %+v", len(res.Rows), res.Rows)
	}

	// 5. Execute mixed INNER + LEFT JOIN chain
	leftSQL := "SELECT u.name, o.amount, i.product FROM users u JOIN orders o ON u.id = o.user_id LEFT JOIN items i ON o.id = i.order_id;"
	resLeft, err := server.ExecuteSQL(leftSQL)
	if err != nil || !resLeft.Success {
		t.Fatalf("ExecuteSQL multi-left-join failed: %v", err)
	}
	if len(resLeft.Rows) != 4 {
		t.Fatalf("expected 4 rows from multi-join with LEFT, got %d: %+v", len(resLeft.Rows), resLeft.Rows)
	}

	foundBob := false
	for _, row := range resLeft.Rows {
		if fmt.Sprintf("%v", row["name"]) == "Bob" {
			foundBob = true
			if row["product"] != nil && row["product"] != "" {
				t.Errorf("expected Bob's product to be NULL, got %v", row["product"])
			}
		}
	}
	if !foundBob {
		t.Errorf("expected to find Bob in LEFT JOIN results")
	}
}

func TestSQLExecutor_DecimalAndFixedPoint(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	// 1. CREATE TABLE with DECIMAL(10, 2), NUMERIC(18, 4), and MONEY
	createSQL := `CREATE TABLE ledger (
		id INT PRIMARY KEY,
		amount DECIMAL(10, 2) NOT NULL,
		balance NUMERIC(18, 4) NOT NULL,
		fee MONEY
	);`
	res, err := server.ExecuteSQL(createSQL)
	if err != nil || !res.Success {
		t.Fatalf("CREATE TABLE with DECIMAL failed: %v", err)
	}

	tbl, err := eng.GetTable("ledger")
	if err != nil {
		t.Fatalf("GetTable ledger failed: %v", err)
	}
	if len(tbl.Columns) != 4 {
		t.Fatalf("expected 4 columns, got %d", len(tbl.Columns))
	}
	if tbl.Columns[1].Type != storage.TypeDecimal || tbl.Columns[1].Precision != 10 || tbl.Columns[1].Scale != 2 {
		t.Errorf("unexpected column 1: %+v", tbl.Columns[1])
	}
	if tbl.Columns[2].Type != storage.TypeDecimal || tbl.Columns[2].Precision != 18 || tbl.Columns[2].Scale != 4 {
		t.Errorf("unexpected column 2: %+v", tbl.Columns[2])
	}
	if tbl.Columns[3].Type != storage.TypeDecimal || tbl.Columns[3].Scale != 4 {
		t.Errorf("unexpected column 3: %+v", tbl.Columns[3])
	}

	// 2. Insert records testing financial precision (0.10, 0.20)
	_, err = server.ExecuteSQL("INSERT INTO ledger (id, amount, balance, fee) VALUES (1, 0.10, 1000.1234, 1.25);")
	if err != nil {
		t.Fatalf("INSERT row 1 failed: %v", err)
	}
	_, err = server.ExecuteSQL("INSERT INTO ledger (id, amount, balance, fee) VALUES (2, 0.20, 2000.5678, 2.50);")
	if err != nil {
		t.Fatalf("INSERT row 2 failed: %v", err)
	}
	_, err = server.ExecuteSQL("INSERT INTO ledger (id, amount, balance, fee) VALUES (3, 100.55, 3000.0000, 5.00);")
	if err != nil {
		t.Fatalf("INSERT row 3 failed: %v", err)
	}

	// 3. Exact Query & Filter: WHERE amount = 0.20
	qRes, err := server.ExecuteSQL("SELECT id, amount, balance FROM ledger WHERE amount = 0.20;")
	if err != nil || !qRes.Success {
		t.Fatalf("SELECT with decimal filter failed: %v", err)
	}
	if len(qRes.Rows) != 1 {
		t.Fatalf("expected 1 row matching amount = 0.20, got %d", len(qRes.Rows))
	}
	if fmt.Sprintf("%v", qRes.Rows[0]["amount"]) != "0.20" {
		t.Errorf("expected amount '0.20', got %v", qRes.Rows[0]["amount"])
	}

	// 4. Exact SUM aggregate: 0.10 + 0.20 + 100.55 = 100.85 (No IEEE-754 precision drift!)
	sumRes, err := server.ExecuteSQL("SELECT SUM(amount) AS total_amount FROM ledger;")
	if err != nil || !sumRes.Success {
		t.Fatalf("SUM query failed: %v", err)
	}
	if len(sumRes.Rows) != 1 {
		t.Fatalf("expected 1 row from SUM, got %d", len(sumRes.Rows))
	}
	if fmt.Sprintf("%v", sumRes.Rows[0]["total_amount"]) != "100.85" {
		t.Errorf("expected exact sum '100.85', got %v", sumRes.Rows[0]["total_amount"])
	}

	// 5. MIN and MAX aggregates
	minMaxRes, err := server.ExecuteSQL("SELECT MIN(amount) AS min_amt, MAX(amount) AS max_amt FROM ledger;")
	if err != nil || !minMaxRes.Success {
		t.Fatalf("MIN/MAX query failed: %v", err)
	}
	if fmt.Sprintf("%v", minMaxRes.Rows[0]["min_amt"]) != "0.10" {
		t.Errorf("expected min_amt '0.10', got %v", minMaxRes.Rows[0]["min_amt"])
	}
	if fmt.Sprintf("%v", minMaxRes.Rows[0]["max_amt"]) != "100.55" {
		t.Errorf("expected max_amt '100.55', got %v", minMaxRes.Rows[0]["max_amt"])
	}

	// 6. Range query: WHERE amount > 0.15
	rangeRes, err := server.ExecuteSQL("SELECT id, amount FROM ledger WHERE amount > 0.15;")
	if err != nil || !rangeRes.Success {
		t.Fatalf("range query failed: %v", err)
	}
	if len(rangeRes.Rows) != 2 {
		t.Fatalf("expected 2 rows > 0.15, got %d", len(rangeRes.Rows))
	}
}



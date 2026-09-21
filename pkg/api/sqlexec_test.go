package api_test

import (
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


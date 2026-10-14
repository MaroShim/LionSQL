package tds_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"testing"
	"time"

	_ "github.com/microsoft/go-mssqldb"

	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
	"sqlion/pkg/wire/tds"
)

func TestDriverIntegration_GoMSSQLDB_Ping(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start TDS server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port

	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)

	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	t.Logf("Connecting to SQLion TDS server at port %d using official go-mssqldb...", port)
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("db.PingContext failed: %v", err)
	}
	t.Logf("db.PingContext succeeded!")
}

func TestDriverIntegration_GoMSSQLDB_QueryBasic(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start TDS server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)

	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var sum int
	err = db.QueryRowContext(ctx, "SELECT 40 + 2 AS val").Scan(&sum)
	if err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if sum != 42 {
		t.Fatalf("expected 42, got %d", sum)
	}
}

func TestDriverIntegration_GoMSSQLDB_TableCRUD(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start TDS server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)

	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. CREATE TABLE
	_, err = db.ExecContext(ctx, "CREATE TABLE accounts (id INT PRIMARY KEY, name VARCHAR(50), balance INT)")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 2. INSERT
	res, err := db.ExecContext(ctx, "INSERT INTO accounts (id, name, balance) VALUES (1, 'Alice', 1000), (2, 'Bob', 2500)")
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}
	affected, _ := res.RowsAffected()
	if affected != 2 {
		t.Fatalf("expected 2 rows affected, got %d", affected)
	}

	// 3. SELECT
	rows, err := db.QueryContext(ctx, "SELECT id, name, balance FROM accounts ORDER BY id ASC")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	defer rows.Close()

	type account struct {
		id      int
		name    string
		balance int
	}
	var accs []account
	for rows.Next() {
		var a account
		if err := rows.Scan(&a.id, &a.name, &a.balance); err != nil {
			t.Fatalf("rows.Scan failed: %v", err)
		}
		accs = append(accs, a)
	}

	if len(accs) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(accs))
	}
	if accs[0].name != "Alice" || accs[0].balance != 1000 {
		t.Errorf("unexpected acc 0: %+v", accs[0])
	}
	if accs[1].name != "Bob" || accs[1].balance != 2500 {
		t.Errorf("unexpected acc 1: %+v", accs[1])
	}
}

func TestDriverIntegration_GoMSSQLDB_ParameterizedQuery(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start TDS server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)

	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := db.ExecContext(ctx, "CREATE TABLE products (id INT PRIMARY KEY, name VARCHAR(50), price INT)"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO products VALUES (1, 'Apple', 10), (2, 'Banana', 20), (3, 'Cherry', 30)"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 1. Parameterized Query via sql.Named / @p1
	var name string
	var price int
	err = db.QueryRowContext(ctx, "SELECT name, price FROM products WHERE id = @p1", sql.Named("p1", 2)).Scan(&name, &price)
	if err != nil {
		t.Fatalf("parameterized QueryRow failed: %v", err)
	}
	if name != "Banana" || price != 20 {
		t.Fatalf("expected Banana 20, got %s %d", name, price)
	}
}

func TestDriverIntegration_GoMSSQLDB_PreparedStatement(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start TDS server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)

	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := db.ExecContext(ctx, "CREATE TABLE items (id INT PRIMARY KEY, title VARCHAR(50))"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO items VALUES (1, 'Book'), (2, 'Pen')"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// Prepare statement (triggers sp_prepare over TDS RPC)
	stmt, err := db.PrepareContext(ctx, "SELECT title FROM items WHERE id = @p1")
	if err != nil {
		t.Fatalf("db.Prepare failed: %v", err)
	}
	defer stmt.Close()

	var title string
	if err := stmt.QueryRowContext(ctx, 1).Scan(&title); err != nil {
		t.Fatalf("stmt.QueryRow (1) failed: %v", err)
	}
	if title != "Book" {
		t.Fatalf("expected 'Book', got %q", title)
	}

	if err := stmt.QueryRowContext(ctx, 2).Scan(&title); err != nil {
		t.Fatalf("stmt.QueryRow (2) failed: %v", err)
	}
	if title != "Pen" {
		t.Fatalf("expected 'Pen', got %q", title)
	}
}

func TestDriverIntegration_GoMSSQLDB_SessionSetOptions(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start TDS server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)

	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Queries typical of SSMS / ADO.NET initialization
	initBatches := []string{
		"SET NOCOUNT ON",
		"SET QUOTED_IDENTIFIER ON",
		"SET ANSI_NULLS ON",
		"SET ANSI_PADDING ON",
		"SET ANSI_WARNINGS ON",
		"SET CONCAT_NULL_YIELDS_NULL ON",
	}

	for _, batch := range initBatches {
		if _, err := db.ExecContext(ctx, batch); err != nil {
			t.Fatalf("Init batch failed %q: %v", batch, err)
		}
	}
}

func TestDriverIntegration_GoMSSQLDB_Transactions(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start TDS server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)

	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := db.ExecContext(ctx, "CREATE TABLE wallets (id INT PRIMARY KEY, balance INT)"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO wallets VALUES (1, 500)"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 1. Rollback test
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE wallets SET balance = 1000 WHERE id = 1"); err != nil {
		t.Fatalf("tx.Exec failed: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("tx.Rollback failed: %v", err)
	}

	var bal int
	if err := db.QueryRowContext(ctx, "SELECT balance FROM wallets WHERE id = 1").Scan(&bal); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if bal != 500 {
		t.Fatalf("expected rolled back balance 500, got %d", bal)
	}

	// 2. Commit test
	tx2, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if _, err := tx2.ExecContext(ctx, "UPDATE wallets SET balance = 750 WHERE id = 1"); err != nil {
		t.Fatalf("tx2.Exec failed: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("tx2.Commit failed: %v", err)
	}

	if err := db.QueryRowContext(ctx, "SELECT balance FROM wallets WHERE id = 1").Scan(&bal); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if bal != 750 {
		t.Fatalf("expected committed balance 750, got %d", bal)
	}
}

func TestDriverIntegration_GoMSSQLDB_MetadataAndDMVs(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start TDS server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)

	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Query @@VERSION
	var version string
	if err := db.QueryRowContext(ctx, "SELECT @@VERSION").Scan(&version); err != nil {
		t.Fatalf("SELECT @@VERSION failed: %v", err)
	}
	t.Logf("@@VERSION: %s", version)

	// 1b. Query SELECT @@VERSION as version (Beekeeper Studio query)
	rowsVer, err := db.QueryContext(ctx, "SELECT @@VERSION as version")
	if err != nil {
		t.Fatalf("SELECT @@VERSION as version failed: %v", err)
	}
	cols, err := rowsVer.Columns()
	if err != nil {
		t.Fatalf("Columns() failed: %v", err)
	}
	if len(cols) != 1 || cols[0] != "version" {
		t.Fatalf("expected column 'version', got %v", cols)
	}
	if !rowsVer.Next() {
		t.Fatalf("expected at least 1 row for version")
	}
	var verVal string
	if err := rowsVer.Scan(&verVal); err != nil {
		t.Fatalf("Scan version failed: %v", err)
	}
	if verVal == "" {
		t.Fatalf("expected non-empty version")
	}
	rowsVer.Close()

	// 1c. RPC Query: sp_executesql N'SELECT @@VERSION as version' (Beekeeper RPC mode)
	rowsVerRPC, err := db.QueryContext(ctx, "exec sp_executesql N'SELECT @@VERSION as version'")
	if err != nil {
		t.Fatalf("RPC SELECT @@VERSION as version failed: %v", err)
	}
	colsRPC, err := rowsVerRPC.Columns()
	if err != nil {
		t.Fatalf("Columns() failed: %v", err)
	}
	if len(colsRPC) != 1 || colsRPC[0] != "version" {
		t.Fatalf("expected column 'version', got %v", colsRPC)
	}
	if !rowsVerRPC.Next() {
		t.Fatalf("expected at least 1 row for RPC version")
	}
	var verRPCVal string
	if err := rowsVerRPC.Scan(&verRPCVal); err != nil {
		t.Fatalf("Scan RPC version failed: %v", err)
	}
	if verRPCVal == "" {
		t.Fatalf("expected non-empty version from RPC")
	}
	rowsVerRPC.Close()

	// 1d. DbGate version & edition detection multi-column query
	dbgateQuery := `SELECT
  @@VERSION AS version,
  SERVERPROPERTY ('productversion') as productVersion,
  CONVERT(INT, SERVERPROPERTY ('EngineEdition')) as engineEdition,
  CASE
  WHEN CONVERT(VARCHAR(128), SERVERPROPERTY ('productversion')) like '8%' THEN 'SQL Server 2000'
  WHEN CONVERT(VARCHAR(128), SERVERPROPERTY ('productversion')) like '9%' THEN 'SQL Server 2005'
  WHEN CONVERT(VARCHAR(128), SERVERPROPERTY ('productversion')) like '10.0%' THEN 'SQL Server 2008'
  WHEN CONVERT(VARCHAR(128), SERVERPROPERTY ('productversion')) like '10.5%' THEN 'SQL Server 2008 R2'
  WHEN CONVERT(VARCHAR(128), SERVERPROPERTY ('productversion')) like '11%' THEN 'SQL Server 2012'
  WHEN CONVERT(VARCHAR(128), SERVERPROPERTY ('productversion')) like '12%' THEN 'SQL Server 2014'
  WHEN CONVERT(VARCHAR(128), SERVERPROPERTY ('productversion')) like '13%' THEN 'SQL Server 2016'
  WHEN CONVERT(VARCHAR(128), SERVERPROPERTY ('productversion')) like '14%' THEN 'SQL Server 2017'
  WHEN CONVERT(VARCHAR(128), SERVERPROPERTY ('productversion')) like '15%' THEN 'SQL Server 2019'
  ELSE 'Unknown'
  END AS versionText`
	rowsDbGate, err := db.QueryContext(ctx, dbgateQuery)
	if err != nil {
		t.Fatalf("DbGate version query failed: %v", err)
	}
	colsDbGate, err := rowsDbGate.Columns()
	if err != nil {
		t.Fatalf("DbGate Columns() failed: %v", err)
	}
	if len(colsDbGate) != 4 || colsDbGate[0] != "version" || colsDbGate[1] != "productVersion" || colsDbGate[2] != "engineEdition" || colsDbGate[3] != "versionText" {
		t.Fatalf("expected 4 DbGate columns [version, productVersion, engineEdition, versionText], got %v", colsDbGate)
	}
	if !rowsDbGate.Next() {
		t.Fatalf("expected 1 row for DbGate version query")
	}
	var ver, prodVer, verText string
	var engEdition int
	if err := rowsDbGate.Scan(&ver, &prodVer, &engEdition, &verText); err != nil {
		t.Fatalf("Scan DbGate version query failed: %v", err)
	}
	if prodVer != "15.0.2000.5" || engEdition != 3 || verText != "SQL Server 2019" {
		t.Fatalf("unexpected DbGate version values: %s, %d, %s", prodVer, engEdition, verText)
	}
	rowsDbGate.Close()

	// 2. Query @@SPID
	var spid int
	if err := db.QueryRowContext(ctx, "SELECT @@SPID").Scan(&spid); err != nil {
		t.Fatalf("SELECT @@SPID failed: %v", err)
	}
	t.Logf("@@SPID: %d", spid)

	// 3. Query sys.databases
	rows, err := db.QueryContext(ctx, "SELECT name FROM sys.databases")
	if err != nil {
		t.Fatalf("SELECT FROM sys.databases failed: %v", err)
	}
	defer rows.Close()

	var dbNames []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("rows.Scan failed: %v", err)
		}
		dbNames = append(dbNames, name)
	}
	if len(dbNames) == 0 {
		t.Fatalf("expected at least 1 database in sys.databases")
	}
	t.Logf("sys.databases returned: %v", dbNames)
}

func TestDriverIntegration_GoMSSQLDB_NBCRowNulls(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start TDS server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)

	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. SELECT query returning explicit NULL values
	var id int
	var name sql.NullString
	var score float64
	var age sql.NullInt64

	row := db.QueryRowContext(ctx, "SELECT 101 AS id, CAST(NULL AS VARCHAR(50)) AS name, 45.67 AS score, CAST(NULL AS INT) AS age")
	if err := row.Scan(&id, &name, &score, &age); err != nil {
		t.Fatalf("QueryRowContext with NULLs failed: %v", err)
	}
	if id != 101 {
		t.Fatalf("expected id 101, got %d", id)
	}
	if name.Valid {
		t.Fatalf("expected name to be NULL (Valid=false), got %v", name.String)
	}
	if score < 45.66 || score > 45.68 {
		t.Fatalf("expected score ~45.67, got %f", score)
	}
	if age.Valid {
		t.Fatalf("expected age to be NULL (Valid=false), got %v", age.Int64)
	}

	// 2. Table CRUD with NULL columns (testing NBCROW and standard ROW interleaving)
	_, err = db.ExecContext(ctx, "CREATE TABLE items_null_test (id INT PRIMARY KEY, name VARCHAR(50), description VARCHAR(100));")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = db.ExecContext(ctx, "INSERT INTO items_null_test (id, name, description) VALUES (1, 'Widget', NULL);")
	if err != nil {
		t.Fatalf("INSERT row 1 failed: %v", err)
	}
	_, err = db.ExecContext(ctx, "INSERT INTO items_null_test (id, name, description) VALUES (2, 'Gadget', 'A cool gadget');")
	if err != nil {
		t.Fatalf("INSERT row 2 failed: %v", err)
	}

	rows, err := db.QueryContext(ctx, "SELECT id, name, description FROM items_null_test ORDER BY id;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	defer rows.Close()

	// Row 1: description is NULL
	if !rows.Next() {
		t.Fatalf("expected row 1")
	}
	var r1ID int
	var r1Name string
	var r1Desc sql.NullString
	if err := rows.Scan(&r1ID, &r1Name, &r1Desc); err != nil {
		t.Fatalf("Scan row 1 failed: %v", err)
	}
	if r1ID != 1 || r1Name != "Widget" || r1Desc.Valid {
		t.Fatalf("unexpected row 1 values: id=%d, name=%s, desc.Valid=%v", r1ID, r1Name, r1Desc.Valid)
	}

	// Row 2: description is NOT NULL
	if !rows.Next() {
		t.Fatalf("expected row 2")
	}
	var r2ID int
	var r2Name string
	var r2Desc sql.NullString
	if err := rows.Scan(&r2ID, &r2Name, &r2Desc); err != nil {
		t.Fatalf("Scan row 2 failed: %v", err)
	}
	if r2ID != 2 || r2Name != "Gadget" || !r2Desc.Valid || r2Desc.String != "A cool gadget" {
		t.Fatalf("unexpected row 2 values: id=%d, name=%s, desc=%v", r2ID, r2Name, r2Desc.String)
	}
}

func TestDriverIntegration_BeekeeperDbGate_TableQuery(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start TDS server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)

	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = db.ExecContext(ctx, "CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(100), email VARCHAR(100));")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	_, err = db.ExecContext(ctx, "INSERT INTO users VALUES (1, 'Alice', 'alice@test.com'), (2, 'Bob', 'bob@test.com');")
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	testQueries := []string{
		"SELECT TOP 50 * FROM [dbo].[users];",
		"SELECT TOP 50 [dbo].[users].* FROM [dbo].[users];",
		"SELECT TOP 50 [t].* FROM [dbo].[users] [t];",
		"SELECT [basetbl].* FROM [dbo].[users] AS [basetbl];",
		`SELECT
  [basetbl].[id] AS [id],
  [basetbl].[name] AS [name],
  [basetbl].[email] AS [email]
  FROM [dbo].[users] [basetbl]
ORDER BY [id] ASC
OFFSET 0 ROWS FETCH NEXT 100 ROWS ONLY`,
	}

	for _, q := range testQueries {
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			t.Fatalf("query failed %q: %v", q, err)
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatalf("rows.Columns failed %q: %v", q, err)
		}
		if len(cols) != 3 {
			rows.Close()
			t.Fatalf("query %q: expected 3 columns, got %v", q, cols)
		}

		count := 0
		for rows.Next() {
			var id int
			var name, email string
			if err := rows.Scan(&id, &name, &email); err != nil {
				rows.Close()
				t.Fatalf("rows.Scan failed %q: %v", q, err)
			}
			if count == 0 && name != "Alice" {
				rows.Close()
				t.Fatalf("query %q: expected Alice, got %s", q, name)
			}
			count++
		}
		rows.Close()
		if count != 2 {
			t.Fatalf("query %q: expected 2 rows, got %d", q, count)
		}
	}
}




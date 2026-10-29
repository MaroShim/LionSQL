package tds_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/wire/tds"
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

	t.Logf("Connecting to LionSQL TDS server at port %d using official go-mssqldb...", port)
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

func TestDriverIntegration_DBeaver_Queries(t *testing.T) {
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

	// 1. DBeaver scalar SERVERPROPERTY('EngineEdition')
	var engineEdition int
	if err := db.QueryRowContext(ctx, "SELECT SERVERPROPERTY('EngineEdition')").Scan(&engineEdition); err != nil {
		t.Fatalf("SELECT SERVERPROPERTY('EngineEdition') failed: %v", err)
	}
	if engineEdition != 3 {
		t.Fatalf("expected engineEdition = 3, got %d", engineEdition)
	}

	// 2. DBeaver sys.databases query with db.*
	rows, err := db.QueryContext(ctx, "SELECT db.* FROM sys.databases db ORDER BY db.name")
	if err != nil {
		t.Fatalf("SELECT db.* FROM sys.databases db failed: %v", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("Columns() failed: %v", err)
	}
	foundName := false
	for _, c := range cols {
		if c == "name" {
			foundName = true
			break
		}
	}
	if !foundName {
		t.Fatalf("expected 'name' in columns, got %v", cols)
	}
}

func TestDriverIntegration_DateDateTime2Guid(t *testing.T) {
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

	_, err = db.ExecContext(ctx, "CREATE TABLE events_2008 (id INT PRIMARY KEY, user_guid VARCHAR(36), event_date DATE, created_at DATETIME);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	testGuid := "6F9619FF-8B86-D011-B42D-00C04FC964FF"
	_, err = db.ExecContext(ctx, fmt.Sprintf("INSERT INTO events_2008 (id, user_guid, event_date, created_at) VALUES (1, '%s', '2026-10-15', '2026-10-15 10:20:30');", testGuid))
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	var id int
	var guidVal mssql.UniqueIdentifier
	var dateVal time.Time
	var createdAt time.Time
	err = db.QueryRowContext(ctx, "SELECT id, user_guid, event_date, created_at FROM events_2008 WHERE id = 1;").Scan(&id, &guidVal, &dateVal, &createdAt)
	if err != nil {
		t.Fatalf("QueryRowContext failed: %v", err)
	}

	if id != 1 {
		t.Fatalf("expected id 1, got %d", id)
	}
	if dateVal.Year() != 2026 || dateVal.Month() != 10 || dateVal.Day() != 15 {
		t.Fatalf("expected event_date 2026-10-15, got %v", dateVal)
	}
	if !strings.EqualFold(guidVal.String(), testGuid) {
		t.Fatalf("expected GUID %s, got %s", testGuid, guidVal.String())
	}
}

func TestDriverIntegration_TimeAndDateTimeOffset(t *testing.T) {
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

	_, err = db.ExecContext(ctx, "CREATE TABLE shifts_2008 (id INT PRIMARY KEY, shift_time VARCHAR(20), order_offset VARCHAR(40));")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = db.ExecContext(ctx, "INSERT INTO shifts_2008 (id, shift_time, order_offset) VALUES (1, '14:30:15', '2026-10-15 12:34:56 +09:00');")
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	var id int
	var timeVal time.Time
	var offsetVal time.Time
	err = db.QueryRowContext(ctx, "SELECT id, shift_time, order_offset FROM shifts_2008 WHERE id = 1;").Scan(&id, &timeVal, &offsetVal)
	if err != nil {
		t.Fatalf("QueryRowContext failed: %v", err)
	}

	if id != 1 {
		t.Fatalf("expected id 1, got %d", id)
	}
	// Verify TIME: 14:30:15
	if timeVal.Hour() != 14 || timeVal.Minute() != 30 || timeVal.Second() != 15 {
		t.Fatalf("expected shift_time 14:30:15, got %v", timeVal)
	}
	// Verify DATETIMEOFFSET: 2026-10-15 12:34:56 with +09:00 offset
	if offsetVal.Year() != 2026 || offsetVal.Month() != 10 || offsetVal.Day() != 15 || offsetVal.Hour() != 12 || offsetVal.Minute() != 34 || offsetVal.Second() != 56 {
		t.Fatalf("expected order_offset 2026-10-15 12:34:56, got %v", offsetVal)
	}
	_, offSec := offsetVal.Zone()
	if offSec != 9*3600 {
		t.Fatalf("expected zone offset +09:00 (+32400 sec), got %d", offSec)
	}
}

func TestDriverIntegration_PLP_LOBStreaming(t *testing.T) {
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

	_, err = db.ExecContext(ctx, "CREATE TABLE plp_docs (id INT PRIMARY KEY, big_text VARCHAR(MAX), null_text VARCHAR(MAX));")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 12,000 character string (exceeds standard 8,000 byte packet limit)
	expectedText := strings.Repeat("A", 12000)
	_, err = db.ExecContext(ctx, "INSERT INTO plp_docs (id, big_text, null_text) VALUES (1, '"+expectedText+"', NULL);")
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	var id int
	var bigText string
	var nullText sql.NullString
	err = db.QueryRowContext(ctx, "SELECT id, big_text, null_text FROM plp_docs WHERE id = 1;").Scan(&id, &bigText, &nullText)
	if err != nil {
		t.Fatalf("QueryRowContext failed: %v", err)
	}

	if id != 1 {
		t.Fatalf("expected id 1, got %d", id)
	}
	if len(bigText) != 12000 {
		t.Fatalf("expected big_text length 12000, got %d", len(bigText))
	}
	if bigText != expectedText {
		t.Fatalf("big_text content mismatch")
	}
	if nullText.Valid {
		t.Fatalf("expected null_text to be NULL, got %v", nullText.String)
	}
}

func TestDriverIntegration_TSQLFunctions(t *testing.T) {
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

	// 1. ISNUMERIC
	var isNum1, isNum2 int
	err = db.QueryRowContext(ctx, "SELECT ISNUMERIC('42.5'), ISNUMERIC('invalid_text');").Scan(&isNum1, &isNum2)
	if err != nil {
		t.Fatalf("ISNUMERIC query failed: %v", err)
	}
	if isNum1 != 1 || isNum2 != 0 {
		t.Fatalf("unexpected ISNUMERIC results: %d, %d", isNum1, isNum2)
	}

	// 2. CHARINDEX
	var charIdx int
	err = db.QueryRowContext(ctx, "SELECT CHARINDEX('world', 'hello world');").Scan(&charIdx)
	if err != nil {
		t.Fatalf("CHARINDEX query failed: %v", err)
	}
	if charIdx != 7 {
		t.Fatalf("expected CHARINDEX 7, got %d", charIdx)
	}

	// 3. PATINDEX
	var patIdx int
	err = db.QueryRowContext(ctx, "SELECT PATINDEX('%[0-9]%', 'abc123def');").Scan(&patIdx)
	if err != nil {
		t.Fatalf("PATINDEX query failed: %v", err)
	}
	if patIdx != 4 {
		t.Fatalf("expected PATINDEX 4, got %d", patIdx)
	}

	// 4. STUFF
	var stuffVal string
	err = db.QueryRowContext(ctx, "SELECT STUFF('abcdef', 2, 3, 'xyz');").Scan(&stuffVal)
	if err != nil {
		t.Fatalf("STUFF query failed: %v", err)
	}
	if stuffVal != "axyzef" {
		t.Fatalf("expected STUFF 'axyzef', got %s", stuffVal)
	}

	// 5. SCOPE_IDENTITY and @@IDENTITY
	_, err = db.ExecContext(ctx, "CREATE TABLE id_test_tbl (id INT IDENTITY PRIMARY KEY, name VARCHAR(20));")
	if err != nil {
		t.Fatalf("CREATE TABLE id_test_tbl failed: %v", err)
	}
	_, err = db.ExecContext(ctx, "INSERT INTO id_test_tbl (name) VALUES ('first');")
	if err != nil {
		t.Fatalf("INSERT into id_test_tbl failed: %v", err)
	}

	var identCurrent int
	err = db.QueryRowContext(ctx, "SELECT IDENT_CURRENT('id_test_tbl');").Scan(&identCurrent)
	if err != nil {
		t.Fatalf("IDENT_CURRENT failed: %v", err)
	}
	if identCurrent != 1 {
		t.Fatalf("expected IDENT_CURRENT 1, got %d", identCurrent)
	}

	var isIdent int
	err = db.QueryRowContext(ctx, "SELECT COLUMNPROPERTY(OBJECT_ID('id_test_tbl'), 'id', 'IsIdentity');").Scan(&isIdent)
	if err != nil {
		t.Fatalf("COLUMNPROPERTY failed: %v", err)
	}
	if isIdent != 1 {
		t.Fatalf("expected COLUMNPROPERTY IsIdentity 1, got %d", isIdent)
	}
}

func TestDriverIntegration_GoMSSQLDB_ConcatNullYieldsNull(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start TDS server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;user id=sa;password=secret;encrypt=disable", port)

	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. String + NULL yields NULL (CONCAT_NULL_YIELDS_NULL)
	var nullStr sql.NullString
	err = db.QueryRowContext(ctx, "SELECT 'Hello ' + CAST(NULL AS VARCHAR(10));").Scan(&nullStr)
	if err != nil {
		t.Fatalf("SELECT 'Hello ' + NULL failed: %v", err)
	}
	if nullStr.Valid {
		t.Fatalf("expected NULL string for 'Hello ' + NULL, got valid string: %q", nullStr.String)
	}

	// 2. Int + NULL yields NULL
	var nullInt sql.NullInt64
	err = db.QueryRowContext(ctx, "SELECT 10 + CAST(NULL AS INT);").Scan(&nullInt)
	if err != nil {
		t.Fatalf("SELECT 10 + NULL failed: %v", err)
	}
	if nullInt.Valid {
		t.Fatalf("expected NULL int for 10 + NULL, got valid int: %d", nullInt.Int64)
	}

	// 3. Normal string concatenation
	var normStr string
	err = db.QueryRowContext(ctx, "SELECT 'Hello ' + 'World';").Scan(&normStr)
	if err != nil {
		t.Fatalf("SELECT 'Hello ' + 'World' failed: %v", err)
	}
	if normStr != "Hello World" {
		t.Fatalf("expected 'Hello World', got %q", normStr)
	}
}

func TestDriverIntegration_GoMSSQLDB_BitColumnNumericCoercion(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start TDS server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;user id=sa;password=secret;encrypt=disable", port)

	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Create table with BIT columns
	_, err = db.ExecContext(ctx, "CREATE TABLE test_bit (id INT PRIMARY KEY, is_active BIT, flag BIT);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 2. Insert with numeric literals 1 and 0
	_, err = db.ExecContext(ctx, "INSERT INTO test_bit VALUES (1, 1, 0);")
	if err != nil {
		t.Fatalf("INSERT numeric literals 1, 0 failed: %v", err)
	}

	_, err = db.ExecContext(ctx, "INSERT INTO test_bit VALUES (2, 0, 1);")
	if err != nil {
		t.Fatalf("INSERT numeric literals 0, 1 failed: %v", err)
	}

	// 3. Insert with string literals '1' and '0'
	_, err = db.ExecContext(ctx, "INSERT INTO test_bit VALUES (3, '1', '0');")
	if err != nil {
		t.Fatalf("INSERT string literals '1', '0' failed: %v", err)
	}

	// 4. Query with WHERE is_active = 1
	rows, err := db.QueryContext(ctx, "SELECT id, is_active, flag FROM test_bit WHERE is_active = 1 ORDER BY id;")
	if err != nil {
		t.Fatalf("SELECT WHERE is_active = 1 failed: %v", err)
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		var id int
		var isActive, flag bool
		if err := rows.Scan(&id, &isActive, &flag); err != nil {
			t.Fatalf("Scan failed: %v", err)
		}
		if !isActive {
			t.Errorf("expected is_active to be true, got false for id=%d", id)
		}
		count++
	}
	if count != 2 {
		t.Errorf("expected 2 active rows, got %d", count)
	}

	// 5. Query with WHERE is_active = 0
	var zeroId int
	var zeroActive, zeroFlag bool
	err = db.QueryRowContext(ctx, "SELECT id, is_active, flag FROM test_bit WHERE is_active = 0;").Scan(&zeroId, &zeroActive, &zeroFlag)
	if err != nil {
		t.Fatalf("SELECT WHERE is_active = 0 failed: %v", err)
	}
	if zeroId != 2 || zeroActive != false || zeroFlag != true {
		t.Errorf("expected id=2, is_active=false, flag=true, got id=%d, active=%v, flag=%v", zeroId, zeroActive, zeroFlag)
	}

	// 6. Update BIT column with numeric literal 0
	_, err = db.ExecContext(ctx, "UPDATE test_bit SET is_active = 0 WHERE id = 1;")
	if err != nil {
		t.Fatalf("UPDATE SET is_active = 0 failed: %v", err)
	}

	var updatedActive bool
	err = db.QueryRowContext(ctx, "SELECT is_active FROM test_bit WHERE id = 1;").Scan(&updatedActive)
	if err != nil {
		t.Fatalf("SELECT updated is_active failed: %v", err)
	}
	if updatedActive != false {
		t.Errorf("expected updated is_active to be false, got true")
	}

	// 7. Verify CAST(... AS BIT)
	var cast1, cast0, castStr1, castStr0 bool
	err = db.QueryRowContext(ctx, "SELECT CAST(1 AS BIT), CAST(0 AS BIT), CAST('1' AS BIT), CAST('0' AS BIT);").Scan(&cast1, &cast0, &castStr1, &castStr0)
	if err != nil {
		t.Fatalf("CAST AS BIT failed: %v", err)
	}
	if !cast1 || cast0 || !castStr1 || castStr0 {
		t.Errorf("unexpected CAST results: cast1=%v, cast0=%v, castStr1=%v, castStr0=%v", cast1, cast0, castStr1, castStr0)
	}
}

func TestDriverIntegration_GoMSSQLDB_InlineVariableDeclaration(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start TDS server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;user id=sa;password=secret;encrypt=disable", port)

	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Ad-hoc query with comma in parentheses and string literal
	var val float64
	var msg string
	err = db.QueryRowContext(ctx, "DECLARE @val DECIMAL(10, 2) = 12.34, @msg VARCHAR(50) = 'hello, world'; SELECT @val, @msg;").Scan(&val, &msg)
	if err != nil {
		t.Fatalf("Ad-hoc inline DECLARE failed: %v", err)
	}
	if val != 12.34 || msg != "hello, world" {
		t.Errorf("expected val=12.34, msg='hello, world', got val=%f, msg=%q", val, msg)
	}

	// 2. Create Procedure with multiple inline DECLARE statements and sequential dependency
	createProcSQL := `
CREATE PROCEDURE dbo.usp_TestInlineDecl
AS
BEGIN
    DECLARE @cnt INT = 10, @multiplier INT = 3;
    DECLARE @total INT = @cnt * @multiplier;
    DECLARE @flag BIT = 1;
    SELECT @total AS total, @flag AS flag;
END;
`
	_, err = db.ExecContext(ctx, createProcSQL)
	if err != nil {
		t.Fatalf("CREATE PROCEDURE with inline DECLARE failed: %v", err)
	}

	// 3. Execute the procedure
	var total int
	var flag bool
	err = db.QueryRowContext(ctx, "EXEC dbo.usp_TestInlineDecl;").Scan(&total, &flag)
	if err != nil {
		t.Fatalf("EXEC dbo.usp_TestInlineDecl failed: %v", err)
	}
	if total != 30 || flag != true {
		t.Errorf("expected total=30, flag=true, got total=%d, flag=%v", total, flag)
	}
}

func TestDriverIntegration_GoMSSQLDB_SystemProcs_TablesAndColumns(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start TDS server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=5", port)

	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = db.ExecContext(ctx, "CREATE TABLE diag_customers (cust_id INT PRIMARY KEY, name VARCHAR(50) NOT NULL, score DECIMAL(10,2), active BIT DEFAULT 1);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 1. EXEC sp_tables
	rows, err := db.QueryContext(ctx, "EXEC sp_tables @table_name = 'diag_customers';")
	if err != nil {
		t.Fatalf("EXEC sp_tables failed: %v", err)
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		var qualifier, owner, tableName, tableType string
		var remarks sql.NullString
		if err := rows.Scan(&qualifier, &owner, &tableName, &tableType, &remarks); err != nil {
			t.Fatalf("Scan sp_tables row failed: %v", err)
		}
		if tableName == "diag_customers" && tableType == "TABLE" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 row for diag_customers in sp_tables, got %d", count)
	}

	// 2. EXEC sp_columns
	colRows, err := db.QueryContext(ctx, "EXEC sp_columns @table_name = 'diag_customers';")
	if err != nil {
		t.Fatalf("EXEC sp_columns failed: %v", err)
	}
	defer colRows.Close()

	cols, err := colRows.Columns()
	if err != nil {
		t.Fatalf("colRows.Columns failed: %v", err)
	}
	if len(cols) != 19 {
		t.Fatalf("expected 19 columns from sp_columns, got %d: %v", len(cols), cols)
	}

	var colNames []string
	for colRows.Next() {
		values := make([]any, len(cols))
		scanArgs := make([]any, len(cols))
		for i := range values {
			scanArgs[i] = &values[i]
		}
		if err := colRows.Scan(scanArgs...); err != nil {
			t.Fatalf("Scan sp_columns row failed: %v", err)
		}
		colNameStr := fmt.Sprintf("%v", values[3]) // COLUMN_NAME is index 3
		colNames = append(colNames, colNameStr)
	}
	if len(colNames) != 4 || colNames[0] != "cust_id" || colNames[1] != "name" || colNames[2] != "score" || colNames[3] != "active" {
		t.Fatalf("unexpected colNames from sp_columns: %v", colNames)
	}
}

func TestDriverIntegration_GoMSSQLDB_RaiserrorAndThrow(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start TDS server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("sqlserver://sa:admin@127.0.0.1:%d?database=master&encrypt=disable", port)
	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("Failed to open sqlserver db: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. THROW statement via go-mssqldb
	_, err = db.ExecContext(ctx, "THROW 50005, 'TDS throw test', 3;")
	if err == nil {
		t.Fatalf("expected error from THROW, got nil")
	}
	var mssqlErr mssql.Error
	if !errors.As(err, &mssqlErr) {
		t.Fatalf("expected mssql.Error, got %T: %v", err, err)
	}
	if mssqlErr.Number != 50005 || mssqlErr.Class != 16 || mssqlErr.State != 3 {
		t.Errorf("unexpected mssql error details: Number=%d, Class=%d, State=%d", mssqlErr.Number, mssqlErr.Class, mssqlErr.State)
	}
	if !strings.Contains(mssqlErr.Message, "TDS throw test") {
		t.Errorf("expected 'TDS throw test' in message, got %s", mssqlErr.Message)
	}

	// 2. RAISERROR severity 16 via go-mssqldb
	_, err = db.ExecContext(ctx, "RAISERROR('TDS raiserror test', 16, 2);")
	if err == nil {
		t.Fatalf("expected error from RAISERROR, got nil")
	}
	if !errors.As(err, &mssqlErr) {
		t.Fatalf("expected mssql.Error, got %T: %v", err, err)
	}
	if mssqlErr.Number != 50000 || mssqlErr.Class != 16 || mssqlErr.State != 2 {
		t.Errorf("unexpected mssql error details: Number=%d, Class=%d, State=%d", mssqlErr.Number, mssqlErr.Class, mssqlErr.State)
	}
	if !strings.Contains(mssqlErr.Message, "TDS raiserror test") {
		t.Errorf("expected 'TDS raiserror test' in message, got %s", mssqlErr.Message)
	}
}

func TestDriverIntegration_GoMSSQLDB_NPrefixUnicodeLiterals(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start TDS server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("sqlserver://sa:admin@127.0.0.1:%d?database=master&encrypt=disable", port)
	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("Failed to open sqlserver db: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. SELECT N'한글유니코드' AS s (Scenario 27 regression prevention)
	var str string
	if err := db.QueryRowContext(ctx, "SELECT N'한글유니코드' AS s").Scan(&str); err != nil {
		t.Fatalf("QueryRow N'한글유니코드' failed: %v", err)
	}
	if str != "한글유니코드" {
		t.Fatalf("expected '한글유니코드', got: %q", str)
	}

	// 2. SELECT n'소문자n유니코드' AS s
	if err := db.QueryRowContext(ctx, "SELECT n'소문자n유니코드' AS s").Scan(&str); err != nil {
		t.Fatalf("QueryRow n'소문자n유니코드' failed: %v", err)
	}
	if str != "소문자n유니코드" {
		t.Fatalf("expected '소문자n유니코드', got: %q", str)
	}

	// 3. String concatenation with N'...'
	if err := db.QueryRowContext(ctx, "SELECT N'안녕 ' + N'세계' AS s").Scan(&str); err != nil {
		t.Fatalf("QueryRow concat failed: %v", err)
	}
	if str != "안녕 세계" {
		t.Fatalf("expected '안녕 세계', got: %q", str)
	}

	// 4. CRUD with table and WHERE filter using N'...'
	if _, err := db.ExecContext(ctx, "CREATE TABLE tbl_unicode (id INT PRIMARY KEY, name VARCHAR(100));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO tbl_unicode (id, name) VALUES (1, N'홍길동'), (2, N'세종대왕');"); err != nil {
		t.Fatalf("INSERT with N'...' failed: %v", err)
	}

	if err := db.QueryRowContext(ctx, "SELECT name FROM tbl_unicode WHERE name = N'홍길동'").Scan(&str); err != nil {
		t.Fatalf("QueryRow with WHERE = N'홍길동' failed: %v", err)
	}
	if str != "홍길동" {
		t.Fatalf("expected '홍길동', got: %q", str)
	}
}

func TestDriverIntegration_GoMSSQLDB_GetDateNativeScan(t *testing.T) {
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

	// 1. SELECT GETDATE() AS dt scanned into time.Time (Scenario 29)
	var dt time.Time
	if err := db.QueryRowContext(ctx, "SELECT GETDATE() AS dt").Scan(&dt); err != nil {
		t.Fatalf("QueryRow SELECT GETDATE() AS dt into time.Time failed: %v", err)
	}
	if dt.IsZero() {
		t.Fatalf("expected non-zero dt, got %v", dt)
	}
	if dt.Year() < 2026 {
		t.Fatalf("expected year >= 2026, got %d", dt.Year())
	}

	// 2. SELECT GETDATE() (without alias) scanned into time.Time
	var dtNoAlias time.Time
	if err := db.QueryRowContext(ctx, "SELECT GETDATE()").Scan(&dtNoAlias); err != nil {
		t.Fatalf("QueryRow SELECT GETDATE() into time.Time failed: %v", err)
	}
	if dtNoAlias.IsZero() {
		t.Fatalf("expected non-zero dtNoAlias, got %v", dtNoAlias)
	}

	// 3. SELECT CURRENT_TIMESTAMP scanned into time.Time
	var ts time.Time
	if err := db.QueryRowContext(ctx, "SELECT CURRENT_TIMESTAMP AS ts").Scan(&ts); err != nil {
		t.Fatalf("QueryRow SELECT CURRENT_TIMESTAMP into time.Time failed: %v", err)
	}
	if ts.IsZero() {
		t.Fatalf("expected non-zero ts, got %v", ts)
	}

	// 4. SELECT GETUTCDATE() scanned into time.Time
	var utc time.Time
	if err := db.QueryRowContext(ctx, "SELECT GETUTCDATE() AS utc_now").Scan(&utc); err != nil {
		t.Fatalf("QueryRow SELECT GETUTCDATE() into time.Time failed: %v", err)
	}
	if utc.IsZero() {
		t.Fatalf("expected non-zero utc, got %v", utc)
	}

	// 5. String scan compatibility on GETDATE()
	var s string
	if err := db.QueryRowContext(ctx, "SELECT GETDATE() AS dt").Scan(&s); err != nil {
		t.Fatalf("QueryRow SELECT GETDATE() into string failed: %v", err)
	}
	if len(s) == 0 {
		t.Fatalf("expected non-empty string for GETDATE, got %q", s)
	}

	// 6. Formatted scan compatibility (Scenario 30)
	var formatted string
	if err := db.QueryRowContext(ctx, "SELECT CONVERT(VARCHAR(30), GETDATE(), 120) AS s").Scan(&formatted); err != nil {
		t.Fatalf("QueryRow CONVERT(VARCHAR(30), GETDATE(), 120) failed: %v", err)
	}
	if len(formatted) < 19 {
		t.Fatalf("expected ISO formatted date string (len >= 19), got %q", formatted)
	}
}

func TestDriverIntegration_Scenario64_AdHocIfElse(t *testing.T) {
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

	// Scenario 64: DECLARE @flag INT = 1; IF @flag = 1 SELECT 'TRUE'; ELSE SELECT 'FALSE';
	var res1 string
	if err := db.QueryRowContext(ctx, "DECLARE @flag INT = 1; IF @flag = 1 SELECT 'TRUE' AS res; ELSE SELECT 'FALSE' AS res;").Scan(&res1); err != nil {
		t.Fatalf("Scenario 64 (@flag=1) query failed: %v", err)
	}
	if res1 != "TRUE" {
		t.Fatalf("expected 'TRUE', got %q", res1)
	}

	var res0 string
	if err := db.QueryRowContext(ctx, "DECLARE @flag INT = 0; IF @flag = 1 SELECT 'TRUE' AS res; ELSE SELECT 'FALSE' AS res;").Scan(&res0); err != nil {
		t.Fatalf("Scenario 64 (@flag=0) query failed: %v", err)
	}
	if res0 != "FALSE" {
		t.Fatalf("expected 'FALSE', got %q", res0)
	}
}

func TestDriverIntegration_Scenario65_AdHocWhile(t *testing.T) {
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

	// Scenario 65: DECLARE @cnt INT = 0; WHILE @cnt < 5 BEGIN SET @cnt = @cnt + 1; END; SELECT @cnt;
	var cnt int
	if err := db.QueryRowContext(ctx, "DECLARE @cnt INT = 0; WHILE @cnt < 5 BEGIN SET @cnt = @cnt + 1; END; SELECT @cnt AS count;").Scan(&cnt); err != nil {
		t.Fatalf("Scenario 65 query failed: %v", err)
	}
	if cnt != 5 {
		t.Fatalf("expected @cnt=5, got %d", cnt)
	}
}

func TestDriverIntegration_Scenario66_GiteaUserInsert(t *testing.T) {
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

	createTableSQL := `CREATE TABLE [user] (
		[id] INT IDENTITY(1,1) PRIMARY KEY,
		[lower_name] VARCHAR(255),
		[name] VARCHAR(255),
		[full_name] VARCHAR(255),
		[email] VARCHAR(255),
		[keep_email_private] BIT,
		[email_notifications_preference] VARCHAR(20),
		[passwd] VARCHAR(255),
		[passwd_hash_algo] VARCHAR(255),
		[must_change_password] BIT,
		[login_type] INT,
		[login_source] INT,
		[login_name] VARCHAR(255),
		[type] INT,
		[location] VARCHAR(255),
		[website] VARCHAR(255),
		[rands] VARCHAR(32),
		[salt] VARCHAR(32),
		[language] VARCHAR(5),
		[description] VARCHAR(255),
		[created_unix] INT,
		[updated_unix] INT,
		[last_login_unix] INT,
		[last_repo_visibility] BIT,
		[max_repo_creation] INT,
		[is_active] BIT,
		[is_admin] BIT,
		[is_restricted] BIT,
		[allow_git_hook] BIT,
		[allow_import_local] BIT,
		[allow_create_organization] BIT,
		[prohibit_login] BIT,
		[avatar] VARCHAR(2048),
		[avatar_email] VARCHAR(255),
		[use_custom_avatar] BIT,
		[num_followers] INT,
		[num_following] INT,
		[num_stars] INT,
		[num_repos] INT,
		[num_teams] INT,
		[num_members] INT,
		[visibility] INT,
		[repo_admin_change_team_access] BIT,
		[diff_view_style] VARCHAR(255),
		[theme] VARCHAR(255),
		[keep_activity_private] BIT
	);`

	if _, err := db.ExecContext(ctx, createTableSQL); err != nil {
		t.Fatalf("create table failed: %v", err)
	}

	insertSQL := `INSERT INTO [user] ([lower_name],[name],[full_name],[email],[keep_email_private],[email_notifications_preference],[passwd],[passwd_hash_algo],[must_change_password],[login_type],[login_source],[login_name],[type],[location],[website],[rands],[salt],[language],[description],[created_unix],[updated_unix],[last_login_unix],[last_repo_visibility],[max_repo_creation],[is_active],[is_admin],[is_restricted],[allow_git_hook],[allow_import_local],[allow_create_organization],[prohibit_login],[avatar],[avatar_email],[use_custom_avatar],[num_followers],[num_following],[num_stars],[num_repos],[num_teams],[num_members],[visibility],[repo_admin_change_team_access],[diff_view_style],[theme],[keep_activity_private]) OUTPUT Inserted.[id] VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

	var newID int64
	row := db.QueryRowContext(ctx, insertSQL,
		"alice", "alice", "", "alice@example.com", false, "enabled",
		"60c666b5ffbfb92da4940bb3e3ce871e6a0c7b9261e0be6e1d240f5ff71b6a2270d8c207042ba2bc53581f81a06450204bcf",
		"pbkdf2$50000$50", false, 0, 0, "", 0, "", "",
		"1e556af7e3d35aa19e1641a0908220df", "35618da2b15689ca7088247525da6536", "", "",
		1793268847, 1793268847, 0, false, -1, true, false, false, false, false, true, false,
		"avatar_val", "alice@example.com", false, 0, 0, 0, 0, 0, 0, 0, false, "", "gitea-auto", false,
	)
	if err := row.Scan(&newID); err != nil {
		t.Fatalf("insert failed: %v", err)
	}
	t.Logf("Inserted user ID: %d", newID)

	// Now check if email, passwd, is_active are populated
	var email, passwd string
	var isActive bool
	checkRow := db.QueryRowContext(ctx, `SELECT email, passwd, is_active FROM [user] WHERE id = ?`, newID)
	if err := checkRow.Scan(&email, &passwd, &isActive); err != nil {
		t.Fatalf("scan check row failed: %v", err)
	}
	t.Logf("Got: email=%q, passwd=%q, isActive=%v", email, passwd, isActive)
	if email != "alice@example.com" {
		t.Fatalf("expected email 'alice@example.com', got %q", email)
	}
	if !isActive {
		t.Fatalf("expected isActive=true, got false")
	}
}

func TestDriverIntegration_Scenario67_GiteaRepoInsertAndGet(t *testing.T) {
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

	createSQL := `CREATE TABLE [repository] (
		[id] INT IDENTITY(1,1) PRIMARY KEY,
		[owner_id] INT,
		[name] VARCHAR(255)
	);`
	if _, err := db.ExecContext(ctx, createSQL); err != nil {
		t.Fatalf("create table failed: %v", err)
	}

	insertSQL := `INSERT INTO [repository] ([owner_id],[name]) OUTPUT Inserted.[id] VALUES (?,?)`
	var newID int64
	row := db.QueryRowContext(ctx, insertSQL, 9, "lion-engine")
	if err := row.Scan(&newID); err != nil {
		t.Fatalf("insert failed: %v", err)
	}
	t.Logf("Inserted repo ID: %d", newID)

	var fetchedID, ownerID int64
	var name string
	selectSQL := `SELECT [id], [owner_id], [name] FROM [repository] WHERE [id] = ?`
	rows, err := db.QueryContext(ctx, selectSQL, newID)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns failed: %v", err)
	}
	expectedCols := []string{"id", "owner_id", "name"}
	if len(cols) != len(expectedCols) {
		t.Fatalf("expected %d columns, got %d: %v", len(expectedCols), len(cols), cols)
	}
	for i, c := range cols {
		if c != expectedCols[i] {
			t.Fatalf("expected col %d to be %q, got %q", i, expectedCols[i], c)
		}
	}

	if !rows.Next() {
		t.Fatalf("expected 1 row, got 0")
	}
	if err := rows.Scan(&fetchedID, &ownerID, &name); err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	t.Logf("Fetched repo: ID=%d, OwnerID=%d, Name=%q, Cols=%v", fetchedID, ownerID, name, cols)

	if fetchedID != newID {
		t.Fatalf("expected fetchedID=%d, got %d", newID, fetchedID)
	}
}

func TestDriverIntegration_Scenario68_MergeMultilineWhitespace(t *testing.T) {
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

	_, err = db.ExecContext(ctx, `CREATE TABLE merge_target (id INT PRIMARY KEY, max_index INT);`)
	if err != nil {
		t.Fatalf("create target table failed: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO merge_target (id, max_index) VALUES (1, 10);`)
	if err != nil {
		t.Fatalf("insert target failed: %v", err)
	}

	// Test Gitea exact MERGE syntax with WITH (HOLDLOCK) AS target and multiline WHEN MATCHED
	mergeSQL := `
MERGE INTO merge_target WITH (HOLDLOCK) AS target
USING (SELECT 1 AS group_id) AS source
(group_id)
ON target.id = source.group_id
WHEN MATCHED
	THEN UPDATE
			SET max_index = max_index + 1
WHEN NOT MATCHED
	THEN INSERT (id, max_index)
			VALUES (1, 1);
`

	res, err := db.ExecContext(ctx, mergeSQL)
	if err != nil {
		t.Fatalf("MERGE with multiline whitespace failed: %v", err)
	}
	aff, _ := res.RowsAffected()
	if aff != 1 {
		t.Fatalf("expected 1 row affected, got %d", aff)
	}

	var maxIndex int
	err = db.QueryRowContext(ctx, "SELECT max_index FROM merge_target WHERE id = 1;").Scan(&maxIndex)
	if err != nil {
		t.Fatalf("select failed: %v", err)
	}
	if maxIndex != 11 {
		t.Fatalf("expected max_index=11, got %d", maxIndex)
	}

	// Test NOT MATCHED branch with new key 2
	mergeSQL2 := `
MERGE INTO merge_target WITH (HOLDLOCK) AS target
USING (SELECT 2 AS group_id) AS source
(group_id)
ON target.id = source.group_id
WHEN MATCHED
	THEN UPDATE
			SET max_index = max_index + 1
WHEN NOT MATCHED
	THEN INSERT (id, max_index)
			VALUES (2, 1);
`
	_, err = db.ExecContext(ctx, mergeSQL2)
	if err != nil {
		t.Fatalf("MERGE NOT MATCHED failed: %v", err)
	}

	var maxIndex2 int
	err = db.QueryRowContext(ctx, "SELECT max_index FROM merge_target WHERE id = 2;").Scan(&maxIndex2)
	if err != nil {
		t.Fatalf("select 2 failed: %v", err)
	}
	if maxIndex2 != 1 {
		t.Fatalf("expected max_index2=1, got %d", maxIndex2)
	}
}

func TestDriverIntegration_Scenario69_GiteaBranchSelect(t *testing.T) {
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

	createSQL := `CREATE TABLE [branch] (
		[id] BIGINT IDENTITY(1,1) PRIMARY KEY,
		[repo_id] BIGINT,
		[name] VARCHAR(255),
		[commit_id] VARCHAR(40),
		[commit_message] TEXT,
		[pusher_id] BIGINT,
		[is_deleted] BIT,
		[deleted_by_id] BIGINT,
		[deleted_unix] BIGINT,
		[commit_time] BIGINT,
		[created_unix] BIGINT,
		[updated_unix] BIGINT
	);
	CREATE UNIQUE INDEX [UQE_branch_s] ON [branch] ([repo_id], [name]);
	CREATE INDEX [IDX_branch_is_deleted] ON [branch] ([is_deleted]);
	`
	if _, err := db.ExecContext(ctx, createSQL); err != nil {
		t.Fatalf("create table branch failed: %v", err)
	}

	insertSQL := `INSERT INTO [branch] ([repo_id], [name], [commit_id], [commit_message], [pusher_id], [is_deleted], [deleted_by_id], [deleted_unix], [commit_time], [created_unix], [updated_unix])
		VALUES (9, 'main', 'abcdef1234567890abcdef1234567890abcdef12', 'Initial commit', 1, 0, 0, 0, 1730000000, 1730000000, 1730000000);`
	if _, err := db.ExecContext(ctx, insertSQL); err != nil {
		t.Fatalf("insert branch failed: %v", err)
	}

	querySQL := `SELECT [id], [repo_id], [name], [commit_id], [commit_message], [pusher_id], [is_deleted], [deleted_by_id], [deleted_unix], [commit_time], [created_unix], [updated_unix] FROM [branch] WHERE [repo_id] = ? ORDER BY is_deleted ASC, commit_time DESC, name ASC`

	rows, err := db.QueryContext(ctx, querySQL, int64(9))
	if err != nil {
		t.Fatalf("query branch failed: %v", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns failed: %v", err)
	}
	t.Logf("Columns returned: %v", cols)

	var id, repoID, pusherID, deletedByID, deletedUnix, commitTime, createdUnix, updatedUnix int64
	var name, commitID, commitMessage string
	var isDeleted bool

	if !rows.Next() {
		t.Fatalf("expected 1 row from branch query, got 0")
	}

	if err := rows.Scan(&id, &repoID, &name, &commitID, &commitMessage, &pusherID, &isDeleted, &deletedByID, &deletedUnix, &commitTime, &createdUnix, &updatedUnix); err != nil {
		t.Fatalf("scan branch row failed: %v", err)
	}

	t.Logf("Scanned branch: id=%d, repo_id=%d, name=%q, isDeleted=%v, commit_time=%d", id, repoID, name, isDeleted, commitTime)
	if id != 1 || repoID != 9 || name != "main" {
		t.Fatalf("unexpected branch data: id=%d, repo_id=%d, name=%s", id, repoID, name)
	}

	querySQL2 := `SELECT [id], [repo_id], [name], [commit_id], [commit_message], [pusher_id], [is_deleted], [deleted_by_id], [deleted_unix], [commit_time], [created_unix], [updated_unix] FROM [branch] WHERE (repo_id=?) AND (name=?) ORDER BY [id] ASC OFFSET 0 ROWS FETCH NEXT 1 ROWS ONLY`
	rows2, err := db.QueryContext(ctx, querySQL2, int64(9), "main")
	if err != nil {
		t.Fatalf("query branch 2 failed: %v", err)
	}
	defer rows2.Close()
	if !rows2.Next() {
		t.Fatalf("expected 1 row from branch query 2, got 0")
	}
}

func TestDriverIntegration_RepoJoinUser(t *testing.T) {
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

	if _, err := db.ExecContext(ctx, "CREATE TABLE [user] ([id] BIGINT PRIMARY KEY, [lower_name] VARCHAR(255), [name] VARCHAR(255))"); err != nil {
		t.Fatalf("create user failed: %v", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE [repository] ([id] BIGINT PRIMARY KEY, [owner_id] BIGINT, [owner_name] VARCHAR(255), [lower_name] VARCHAR(255), [name] VARCHAR(255))"); err != nil {
		t.Fatalf("create repo failed: %v", err)
	}

	if _, err := db.ExecContext(ctx, "INSERT INTO [user] ([id], [lower_name], [name]) VALUES (9, 'lionsql-core', 'lionsql-core')"); err != nil {
		t.Fatalf("insert user failed: %v", err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO [repository] ([id], [owner_id], [owner_name], [lower_name], [name]) VALUES (9, 9, 'lionsql-core', 'lion-docs', 'lion-docs')"); err != nil {
		t.Fatalf("insert repo failed: %v", err)
	}

	q := `SELECT repository.* FROM [repository] INNER JOIN [user] ON [user].id = repository.owner_id WHERE (repository.lower_name = ?) AND ([user].lower_name = ?) ORDER BY [repository].[id] ASC OFFSET 0 ROWS FETCH NEXT 1 ROWS ONLY`
	rows, err := db.QueryContext(ctx, q, "lion-docs", "lionsql-core")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("cols failed: %v", err)
	}
	t.Logf("Cols returned: %v", cols)

	if !rows.Next() {
		t.Fatalf("expected 1 row, got 0")
	}

	dest := make([]any, len(cols))
	destPtrs := make([]any, len(cols))
	for i := range dest {
		destPtrs[i] = &dest[i]
	}
	if err := rows.Scan(destPtrs...); err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	for i, col := range cols {
		t.Logf("Col %d (%s) = %v (%T)", i, col, dest[i], dest[i])
	}
}





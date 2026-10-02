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



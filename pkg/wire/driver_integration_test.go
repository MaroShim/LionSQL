package wire_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/lib/pq"
	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
	"sqlion/pkg/wire"
)

func startTestPostgresServer(t *testing.T) (*wire.Server, int) {
	t.Helper()

	eng := memengine.New()
	_ = eng.Open("")
	cat := catalog.New(eng)

	srv := wire.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start PG wire server: %v", err)
	}

	tcpAddr, ok := srv.Addr().(*net.TCPAddr)
	if !ok {
		srv.Close()
		t.Fatalf("Failed to get TCP address from server")
	}

	return srv, tcpAddr.Port
}

func TestDriverIntegration_LibPQ_Ping(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("db.PingContext failed: %v", err)
	}
}

func TestDriverIntegration_LibPQ_QueryBasic(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var result int
	if err := db.QueryRowContext(ctx, "SELECT 40 + 2").Scan(&result); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if result != 42 {
		t.Fatalf("expected 42, got %d", result)
	}

	var msg string
	if err := db.QueryRowContext(ctx, "SELECT 'Hello SQLion'").Scan(&msg); err != nil {
		t.Fatalf("QueryRow string failed: %v", err)
	}
	if msg != "Hello SQLion" {
		t.Fatalf("expected 'Hello SQLion', got %q", msg)
	}
}

func TestDriverIntegration_LibPQ_TableCRUD(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. CREATE TABLE
	if _, err := db.ExecContext(ctx, "CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(50), age INT)"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 2. INSERT
	res, err := db.ExecContext(ctx, "INSERT INTO users VALUES (1, 'Alice', 30), (2, 'Bob', 25)")
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected != 2 {
		t.Fatalf("expected 2 rows affected, got %d", rowsAffected)
	}

	// 3. SELECT Single Row
	var name string
	var age int
	if err := db.QueryRowContext(ctx, "SELECT name, age FROM users WHERE id = 1").Scan(&name, &age); err != nil {
		t.Fatalf("SELECT single row failed: %v", err)
	}
	if name != "Alice" || age != 30 {
		t.Fatalf("expected Alice 30, got %s %d", name, age)
	}

	// 4. UPDATE
	if _, err := db.ExecContext(ctx, "UPDATE users SET age = 31 WHERE id = 1"); err != nil {
		t.Fatalf("UPDATE failed: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT age FROM users WHERE id = 1").Scan(&age); err != nil {
		t.Fatalf("SELECT after UPDATE failed: %v", err)
	}
	if age != 31 {
		t.Fatalf("expected age 31, got %d", age)
	}

	// 5. DELETE
	if _, err := db.ExecContext(ctx, "DELETE FROM users WHERE id = 2"); err != nil {
		t.Fatalf("DELETE failed: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		t.Fatalf("SELECT COUNT failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 user remaining, got %d", count)
	}
}

func TestDriverIntegration_LibPQ_ParameterizedQuery(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
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

	// Query with $1 parameter placeholder
	rows, err := db.QueryContext(ctx, "SELECT name, price FROM products WHERE price >= $1 ORDER BY id", 20)
	if err != nil {
		t.Fatalf("QueryContext with parameter failed: %v", err)
	}
	defer rows.Close()

	type prod struct {
		Name  string
		Price int
	}
	var list []prod
	for rows.Next() {
		var p prod
		if err := rows.Scan(&p.Name, &p.Price); err != nil {
			t.Fatalf("rows.Scan failed: %v", err)
		}
		list = append(list, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err failed: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("expected 2 products, got %d: %+v", len(list), list)
	}
	if list[0].Name != "Banana" || list[0].Price != 20 {
		t.Fatalf("expected Banana 20, got %+v", list[0])
	}
	if list[1].Name != "Cherry" || list[1].Price != 30 {
		t.Fatalf("expected Cherry 30, got %+v", list[1])
	}
}

func TestDriverIntegration_LibPQ_PreparedStatement(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
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

	// Prepare statement (triggers Parse/Describe/Bind/Execute over PG Extended Query Protocol)
	stmt, err := db.PrepareContext(ctx, "SELECT title FROM items WHERE id = $1")
	if err != nil {
		t.Fatalf("db.PrepareContext failed: %v", err)
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

func TestDriverIntegration_LibPQ_Transactions(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := db.ExecContext(ctx, "CREATE TABLE accounts (id INT PRIMARY KEY, balance INT)"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO accounts VALUES (1, 1000)"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 1. Rollback test
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE accounts SET balance = 2000 WHERE id = 1"); err != nil {
		t.Fatalf("tx.Exec failed: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("tx.Rollback failed: %v", err)
	}

	var bal int
	if err := db.QueryRowContext(ctx, "SELECT balance FROM accounts WHERE id = 1").Scan(&bal); err != nil {
		t.Fatalf("QueryRow after rollback failed: %v", err)
	}
	if bal != 1000 {
		t.Fatalf("expected rolled back balance 1000, got %d", bal)
	}

	// 2. Commit test
	tx2, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx (2) failed: %v", err)
	}
	if _, err := tx2.ExecContext(ctx, "UPDATE accounts SET balance = 1500 WHERE id = 1"); err != nil {
		t.Fatalf("tx2.Exec failed: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("tx2.Commit failed: %v", err)
	}

	if err := db.QueryRowContext(ctx, "SELECT balance FROM accounts WHERE id = 1").Scan(&bal); err != nil {
		t.Fatalf("QueryRow after commit failed: %v", err)
	}
	if bal != 1500 {
		t.Fatalf("expected committed balance 1500, got %d", bal)
	}
}

func TestDriverIntegration_LibPQ_SessionSetOptions(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Setting standard session variables
	if _, err := db.ExecContext(ctx, "SET client_encoding = 'UTF8'"); err != nil {
		t.Fatalf("SET client_encoding failed: %v", err)
	}
	if _, err := db.ExecContext(ctx, "SET TimeZone = 'UTC'"); err != nil {
		t.Fatalf("SET TimeZone failed: %v", err)
	}
}

func TestDriverIntegration_LibPQ_Metadata(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var ver string
	if err := db.QueryRowContext(ctx, "SELECT version()").Scan(&ver); err != nil {
		t.Fatalf("SELECT version() failed: %v", err)
	}
	if ver == "" {
		t.Fatalf("expected non-empty version string")
	}
	t.Logf("PostgreSQL version(): %s", ver)
}

func TestDriverIntegration_LibPQ_JDBC_ODBC_Compatibility(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. pgjdbc / psqlODBC initialization settings
	initSettings := []string{
		"SET extra_float_digits = 3",
		"SET application_name = 'PostgreSQL JDBC Driver'",
		"SET client_min_messages = 'warning'",
		"SET client_encoding = 'UTF8'",
	}
	for _, sqlStmt := range initSettings {
		if _, err := db.ExecContext(ctx, sqlStmt); err != nil {
			t.Fatalf("Driver init %q failed: %v", sqlStmt, err)
		}
	}

	// 2. SHOW parameter queries
	var txIso string
	if err := db.QueryRowContext(ctx, "SHOW transaction_isolation").Scan(&txIso); err != nil {
		t.Fatalf("SHOW transaction_isolation failed: %v", err)
	}
	if txIso == "" {
		t.Fatalf("expected non-empty transaction_isolation")
	}

	var scs string
	if err := db.QueryRowContext(ctx, "SHOW standard_conforming_strings").Scan(&scs); err != nil {
		t.Fatalf("SHOW standard_conforming_strings failed: %v", err)
	}
	if scs != "on" {
		t.Fatalf("expected 'on', got %q", scs)
	}

	// 3. current_schema() function
	var curSchema string
	if err := db.QueryRowContext(ctx, "SELECT current_schema()").Scan(&curSchema); err != nil {
		t.Fatalf("SELECT current_schema() failed: %v", err)
	}
	if curSchema == "" {
		t.Fatalf("expected non-empty current_schema")
	}

	// 4. Create sample table for metadata verification
	if _, err := db.ExecContext(ctx, "CREATE TABLE jdbc_test (id INT PRIMARY KEY, name VARCHAR(100));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 5. pg_type catalog query (used by JDBC for type introspection)
	rows, err := db.QueryContext(ctx, "SELECT typname, oid, typlen FROM pg_type WHERE typname IN ('int4', 'varchar', 'bool')")
	if err != nil {
		t.Fatalf("pg_type query failed: %v", err)
	}
	defer rows.Close()
	typeCount := 0
	for rows.Next() {
		var typname string
		var oid, typlen int
		if err := rows.Scan(&typname, &oid, &typlen); err != nil {
			t.Fatalf("Scan pg_type row failed: %v", err)
		}
		typeCount++
	}
	if typeCount == 0 {
		t.Fatalf("expected at least 1 pg_type row, got 0")
	}

	// 6. information_schema.tables query (used by DatabaseMetaData.getTables())
	var tblName, tblType string
	if err := db.QueryRowContext(ctx, "SELECT table_name, table_type FROM information_schema.tables WHERE table_name = 'jdbc_test'").Scan(&tblName, &tblType); err != nil {
		t.Fatalf("information_schema query failed: %v", err)
	}
	if tblName != "jdbc_test" {
		t.Fatalf("expected table_name 'jdbc_test', got %q", tblName)
	}
	if tblType != "BASE TABLE" {
		t.Fatalf("expected table_type 'BASE TABLE', got %q", tblType)
	}
}

func TestDriverIntegration_LibPQ_DuplicatePrimaryKey(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = db.ExecContext(ctx, "CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(50));")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = db.ExecContext(ctx, "INSERT INTO users (id, name) VALUES (1, 'Alice');")
	if err != nil {
		t.Fatalf("First INSERT failed: %v", err)
	}

	// Second insert with duplicate primary key
	_, err = db.ExecContext(ctx, "INSERT INTO users (id, name) VALUES (1, 'Bob');")
	if err == nil {
		t.Fatalf("expected error on duplicate primary key insert, got nil")
	}

	pqErr, ok := err.(*pq.Error)
	if !ok {
		t.Fatalf("expected *pq.Error, got %T: %v", err, err)
	}
	if pqErr.Code != "23505" {
		t.Fatalf("expected SQLSTATE 23505 (unique_violation), got %s (message: %s)", pqErr.Code, pqErr.Message)
	}
}

func TestDriverIntegration_LibPQ_UntypedParameter(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res int
	err = db.QueryRowContext(ctx, "SELECT $1", 100).Scan(&res)
	if err != nil {
		t.Fatalf("QueryRowContext SELECT $1 (int) failed: %v", err)
	}
	if res != 100 {
		t.Fatalf("expected 100, got %d", res)
	}

	var strRes string
	err = db.QueryRowContext(ctx, "SELECT $1", "hello").Scan(&strRes)
	if err != nil {
		t.Fatalf("QueryRowContext SELECT $1 (string) failed: %v", err)
	}
	if strRes != "hello" {
		t.Fatalf("expected 'hello', got %q", strRes)
	}

	var r1 int
	var r2 string
	err = db.QueryRowContext(ctx, "SELECT $1, $2", 42, "world").Scan(&r1, &r2)
	if err != nil {
		t.Fatalf("QueryRowContext SELECT $1, $2 failed: %v", err)
	}
	if r1 != 42 || r2 != "world" {
		t.Fatalf("expected 42 and 'world', got %d, %q", r1, r2)
	}
}

func TestDriverIntegration_LibPQ_RelationalJoin(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable&connect_timeout=3", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := db.ExecContext(ctx, "CREATE TABLE pg_suite_test (id INT PRIMARY KEY, name VARCHAR(50))"); err != nil {
		t.Fatalf("CREATE TABLE pg_suite_test failed: %v", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE pg_suite_orders (id INT PRIMARY KEY, user_id INT, amount FLOAT)"); err != nil {
		t.Fatalf("CREATE TABLE pg_suite_orders failed: %v", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE INDEX idx_orders_user_id ON pg_suite_orders (user_id)"); err != nil {
		t.Fatalf("CREATE INDEX failed: %v", err)
	}

	if _, err := db.ExecContext(ctx, "INSERT INTO pg_suite_test VALUES (1, 'Alice'), (2, 'Bob')"); err != nil {
		t.Fatalf("INSERT pg_suite_test failed: %v", err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO pg_suite_orders VALUES (101, 1, 99.5), (102, 2, 150.0)"); err != nil {
		t.Fatalf("INSERT pg_suite_orders failed: %v", err)
	}

	rows, err := db.QueryContext(ctx, "SELECT u.name, o.amount FROM pg_suite_test u INNER JOIN pg_suite_orders o ON u.id = o.user_id")
	if err != nil {
		t.Fatalf("join query failed: %v", err)
	}
	defer rows.Close()

	type result struct {
		name   string
		amount *float64
	}
	var res []result
	for rows.Next() {
		var r result
		if err := rows.Scan(&r.name, &r.amount); err != nil {
			t.Fatalf("scan error: %v", err)
		}
		res = append(res, r)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res))
	}
	t.Logf("res: %+v", res)
	if res[0].amount == nil {
		t.Fatalf("res[0].amount is NULL! expected 99.5")
	}
	if *res[0].amount != 99.5 {
		t.Fatalf("expected 99.5, got %v", *res[0].amount)
	}
}



package wire

import (
	"database/sql"
	"fmt"
	"net"
	"testing"

	_ "github.com/lib/pq"
	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestPGWire_PreparedStatement_StoredProcedureFastPath(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	server := NewServer("127.0.0.1:0", cat, eng)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen in sandbox: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go server.handleConnection(conn)
		}
	}()

	addr := ln.Addr().String()
	dsn := fmt.Sprintf("postgres://admin:admin@%s/master?sslmode=disable", addr)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("failed to open postgres connection: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("CREATE TABLE bank (id INT PRIMARY KEY, balance INT);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := db.Exec("INSERT INTO bank (id, balance) VALUES (1, 1000);"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}
	if _, err := db.Exec(`
CREATE PROCEDURE TransferFunds
    @acc INT,
    @amount INT
AS
BEGIN
    UPDATE bank SET balance = balance + @amount WHERE id = @acc;
END;
`); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	// 1. Prepare stored procedure with named parameters
	stmt, err := db.Prepare("EXEC TransferFunds @acc=$1, @amount=$2;")
	if err != nil {
		t.Fatalf("Prepare SP failed: %v", err)
	}
	defer stmt.Close()

	// 2. Execute prepared statement
	if _, err := stmt.Exec(1, 50); err != nil {
		t.Fatalf("stmt.Exec failed: %v", err)
	}

	// 3. Verify bank balance
	var balance int
	if err := db.QueryRow("SELECT balance FROM bank WHERE id = 1;").Scan(&balance); err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if balance != 1050 {
		t.Fatalf("expected balance 1050, got %d", balance)
	}

	// 4. Test within explicit transaction
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	txStmt := tx.Stmt(stmt)
	if _, err := txStmt.Exec(1, 100); err != nil {
		t.Fatalf("txStmt.Exec failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	if err := db.QueryRow("SELECT balance FROM bank WHERE id = 1;").Scan(&balance); err != nil {
		t.Fatalf("SELECT after commit failed: %v", err)
	}
	if balance != 1150 {
		t.Fatalf("expected balance 1150, got %d", balance)
	}
}

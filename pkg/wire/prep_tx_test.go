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

func TestPGWire_PreparedStatementInTransaction(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	server := NewServer("127.0.0.1:0", cat, eng)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
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

	if _, err := db.Exec("CREATE TABLE prep_accounts (id INT PRIMARY KEY, balance INT, name VARCHAR(50))"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 1. Autocommit prepared insert
	stmtInsert, err := db.Prepare("INSERT INTO prep_accounts (id, balance, name) VALUES ($1, $2, $3)")
	if err != nil {
		t.Fatalf("Prepare INSERT failed: %v", err)
	}
	defer stmtInsert.Close()

	if _, err := stmtInsert.Exec(1, 1000, "Alice"); err != nil {
		t.Fatalf("stmtInsert.Exec autocommit failed: %v", err)
	}

	// 2. Multi-step transaction using prepared statements (ExecuteInTx directly)
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("db.Begin failed: %v", err)
	}

	txStmtInsert := tx.Stmt(stmtInsert)
	if _, err := txStmtInsert.Exec(2, 2000, "Bob"); err != nil {
		_ = tx.Rollback()
		t.Fatalf("txStmtInsert.Exec failed: %v", err)
	}

	stmtUpdate, err := tx.Prepare("UPDATE prep_accounts SET balance = balance + $1 WHERE id = $2")
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("tx.Prepare UPDATE failed: %v", err)
	}
	defer stmtUpdate.Close()

	if _, err := stmtUpdate.Exec(500, 1); err != nil {
		_ = tx.Rollback()
		t.Fatalf("stmtUpdate.Exec in tx failed: %v", err)
	}

	stmtSelect, err := tx.Prepare("SELECT balance, name FROM prep_accounts WHERE id = $1")
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("tx.Prepare SELECT failed: %v", err)
	}
	defer stmtSelect.Close()

	var bal int
	var name string
	if err := stmtSelect.QueryRow(1).Scan(&bal, &name); err != nil {
		_ = tx.Rollback()
		t.Fatalf("stmtSelect.QueryRow in tx failed: %v", err)
	}
	if bal != 1500 || name != "Alice" {
		_ = tx.Rollback()
		t.Fatalf("expected bal=1500, name=Alice, got bal=%d, name=%s", bal, name)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("tx.Commit failed: %v", err)
	}

	// 3. Verify committed state
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM prep_accounts").Scan(&count); err != nil {
		t.Fatalf("SELECT COUNT failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 accounts, got %d", count)
	}

	// 4. Test Transaction Rollback
	tx2, err := db.Begin()
	if err != nil {
		t.Fatalf("db.Begin 2 failed: %v", err)
	}
	tx2Stmt := tx2.Stmt(stmtInsert)
	if _, err := tx2Stmt.Exec(3, 3000, "Charlie"); err != nil {
		_ = tx2.Rollback()
		t.Fatalf("tx2Stmt.Exec failed: %v", err)
	}
	if err := tx2.Rollback(); err != nil {
		t.Fatalf("tx2.Rollback failed: %v", err)
	}

	var countAfterRollback int
	if err := db.QueryRow("SELECT COUNT(*) FROM prep_accounts").Scan(&countAfterRollback); err != nil {
		t.Fatalf("SELECT COUNT after rollback failed: %v", err)
	}
	if countAfterRollback != 2 {
		t.Fatalf("expected 2 accounts after rollback, got %d", countAfterRollback)
	}
}

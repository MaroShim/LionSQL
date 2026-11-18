//go:build cgo && wiredtiger

package tds_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/wiredtiger"
	"lionsql/pkg/wire/tds"
	_ "github.com/microsoft/go-mssqldb"
)

func TestTDS_WT_WriteWriteConflict_Autocommit_FailsFast(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_tds_conflict_reg_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng := wiredtiger.New()
	if err := eng.Open(tmpDir); err != nil {
		t.Fatalf("failed to open WT engine: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	srv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start TDS server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=5", port)

	// Conn 1 (Session 1)
	db1, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open db1: %v", err)
	}
	defer db1.Close()
	db1.SetMaxOpenConns(1)

	// Conn 2 (Session 2)
	db2, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open db2: %v", err)
	}
	defer db2.Close()
	db2.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := db1.ExecContext(ctx, "CREATE TABLE accounts (id INT PRIMARY KEY, balance INT)"); err != nil {
		t.Fatalf("create table failed: %v", err)
	}
	if _, err := db1.ExecContext(ctx, "INSERT INTO accounts VALUES (1, 100)"); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// 1. Session 1 begins explicit transaction and updates row 1
	tx1, err := db1.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("db1 BeginTx failed: %v", err)
	}
	if _, err := tx1.ExecContext(ctx, "UPDATE accounts SET balance = 200 WHERE id = 1"); err != nil {
		t.Fatalf("tx1 update failed: %v", err)
	}

	// 2. Session 2 executes autocommit update on same row -> MUST FAIL FAST
	res2, err2 := db2.ExecContext(ctx, "UPDATE accounts SET balance = 300 WHERE id = 1")
	if err2 == nil {
		t.Fatalf("CRITICAL BUG: Session 2 autocommit update succeeded while Session 1 is uncommitted! Result: %v", res2)
	}
	t.Logf("Session 2 correctly rejected with conflict error: %v", err2)

	// 3. Session 1 commits
	if err := tx1.Commit(); err != nil {
		t.Fatalf("tx1 Commit failed: %v", err)
	}

	// 4. Session 2 retries after commit -> MUST SUCCEED
	res2Retry, err2Retry := db2.ExecContext(ctx, "UPDATE accounts SET balance = 300 WHERE id = 1")
	if err2Retry != nil {
		t.Fatalf("Session 2 retry failed after tx1 commit: %v", err2Retry)
	}
	ra, _ := res2Retry.RowsAffected()
	if ra != 1 {
		t.Fatalf("expected 1 row affected on retry, got %d", ra)
	}
}

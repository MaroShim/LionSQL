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

	_ "github.com/microsoft/go-mssqldb"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/wiredtiger"
	"lionsql/pkg/wire/tds"
)

func TestDriverIntegration_WiredTiger_TruncateTable(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_tds_trunc_*")
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
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)

	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = db.ExecContext(ctx, "CREATE TABLE wt_trunc (id INT PRIMARY KEY, name VARCHAR(50))")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = db.ExecContext(ctx, "INSERT INTO wt_trunc VALUES (1, 'Alice'), (2, 'Bob')")
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 1. Truncate table in autocommit mode
	_, err = db.ExecContext(ctx, "TRUNCATE TABLE wt_trunc")
	if err != nil {
		t.Fatalf("TRUNCATE TABLE failed: %v", err)
	}

	// 2. Query count after truncate
	var count int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM wt_trunc").Scan(&count)
	if err != nil {
		t.Fatalf("SELECT COUNT failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows after truncate, got %d", count)
	}

	// 3. Insert new row after truncate
	_, err = db.ExecContext(ctx, "INSERT INTO wt_trunc VALUES (10, 'Charlie')")
	if err != nil {
		t.Fatalf("INSERT after truncate failed: %v", err)
	}

	// 4. Test TRUNCATE inside explicit transaction with uncommitted modifications
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO wt_trunc VALUES (20, 'Dave')")
	if err != nil {
		t.Fatalf("INSERT inside tx failed: %v", err)
	}
	_, err = tx.ExecContext(ctx, "TRUNCATE TABLE wt_trunc")
	if err != nil {
		t.Fatalf("TRUNCATE TABLE inside tx failed: %v", err)
	}
	err = tx.Commit()
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM wt_trunc").Scan(&count)
	if err != nil {
		t.Fatalf("SELECT COUNT failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows after tx truncate, got %d", count)
	}

	// 5. Test TRUNCATE inside explicit transaction with ROLLBACK
	_, err = db.ExecContext(ctx, "INSERT INTO wt_trunc VALUES (30, 'Eve'), (31, 'Frank')")
	if err != nil {
		t.Fatalf("INSERT before tx rollback test failed: %v", err)
	}

	txRollback, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	_, err = txRollback.ExecContext(ctx, "TRUNCATE TABLE wt_trunc")
	if err != nil {
		t.Fatalf("TRUNCATE TABLE in tx failed: %v", err)
	}
	err = txRollback.Rollback()
	if err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	// After rollback, the 2 rows must still be intact!
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM wt_trunc").Scan(&count)
	if err != nil {
		t.Fatalf("SELECT COUNT after rollback failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 rows restored after rollback, got %d", count)
	}
}

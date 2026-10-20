//go:build cgo && wiredtiger

package wire_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/wiredtiger"
	"lionsql/pkg/wire"
)

func TestDriverIntegration_WiredTiger_RelationalJoin(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt_pg_join_*")
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
	srv := wire.NewServer("127.0.0.1:0", cat, eng)
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start PG server: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
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

	if _, err := db.ExecContext(ctx, "INSERT INTO pg_suite_test VALUES (1, 'Alice'), (2, 'Bob')"); err != nil {
		t.Fatalf("INSERT pg_suite_test failed: %v", err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO pg_suite_orders VALUES (101, 1, 99.5), (102, 2, 150.0)"); err != nil {
		t.Fatalf("INSERT pg_suite_orders failed: %v", err)
	}

	rows, err := db.QueryContext(ctx, "SELECT u.name, o.amount FROM pg_suite_test u INNER JOIN pg_suite_orders o ON u.id = o.user_id ORDER BY u.id ASC")
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
	t.Logf("WiredTiger Join result: %+v", res)
	if res[0].amount == nil {
		t.Fatalf("res[0].amount is NULL! (PG-COMPAT-3 reproduced)")
	}
	if *res[0].amount != 99.5 {
		t.Fatalf("expected 99.5, got %v", *res[0].amount)
	}
}

package wire_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// 1. DBeaver & Beekeeper PostgreSQL Wire Protocol Introspection Suite
func TestToolAudit_PGWire_DBeaver_Introspection(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable&connect_timeout=5", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Seed schema
	ddls := []string{
		"CREATE TABLE customers (customer_id INT PRIMARY KEY, name VARCHAR(100), email VARCHAR(100), balance NUMERIC(18,2), is_active BOOLEAN);",
		"CREATE TABLE orders (order_id INT PRIMARY KEY, customer_id INT, amount NUMERIC(18,2), order_date DATE);",
		"CREATE VIEW v_active_customers AS SELECT customer_id, name FROM customers WHERE is_active = true;",
		"CREATE PROCEDURE sp_pg_report AS BEGIN SELECT 1; END;",
	}
	for _, ddl := range ddls {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("DDL execution failed %q: %v", ddl, err)
		}
	}

	// Seed records
	_, err = db.ExecContext(ctx, "INSERT INTO customers VALUES (1, 'Alice Corp', 'alice@test.com', 1500.50, true), (2, 'Bob Inc', 'bob@test.com', 8900.00, true);")
	if err != nil {
		t.Fatalf("seed customers failed: %v", err)
	}

	// 1.1. Session properties
	var curDB, curSchema string
	if err := db.QueryRowContext(ctx, "SELECT current_database(), current_schema()").Scan(&curDB, &curSchema); err != nil {
		t.Fatalf("SELECT current_database(), current_schema() failed: %v", err)
	}
	if curDB == "" || curSchema == "" {
		t.Fatalf("expected non-empty database/schema, got db=%s, schema=%s", curDB, curSchema)
	}

	// 1.2. DBeaver pg_class JOIN pg_namespace table tree discovery
	qClass := "SELECT c.relname, n.nspname, c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relkind IN ('r', 'v')"
	rowsClass, err := db.QueryContext(ctx, qClass)
	if err != nil {
		t.Fatalf("pg_class query failed: %v", err)
	}
	defer rowsClass.Close()

	classCount := 0
	for rowsClass.Next() {
		classCount++
	}
	if classCount < 3 {
		t.Fatalf("expected at least 3 pg_class entries (2 tables, 1 view), got %d", classCount)
	}

	// 1.3. DBeaver pg_attribute column introspection
	qAttr := "SELECT a.attname, a.attnum FROM pg_attribute a JOIN pg_class c ON a.attrelid = c.oid WHERE c.relname = 'customers' AND a.attnum > 0 ORDER BY a.attnum"
	rowsAttr, err := db.QueryContext(ctx, qAttr)
	if err != nil {
		t.Fatalf("pg_attribute query failed: %v", err)
	}
	defer rowsAttr.Close()

	attrCount := 0
	for rowsAttr.Next() {
		attrCount++
	}
	if attrCount != 5 {
		t.Fatalf("expected 5 attributes for customers, got %d", attrCount)
	}

	// 1.4. DBeaver pg_constraint constraint introspection
	qCon := "SELECT con.conname, con.contype FROM pg_constraint con JOIN pg_class c ON con.conrelid = c.oid WHERE c.relname = 'customers'"
	rowsCon, err := db.QueryContext(ctx, qCon)
	if err != nil {
		t.Fatalf("pg_constraint query failed: %v", err)
	}
	defer rowsCon.Close()

	conCount := 0
	for rowsCon.Next() {
		conCount++
	}
	if conCount < 1 {
		t.Fatalf("expected at least 1 constraint for customers, got %d", conCount)
	}

	// 1.5. DBeaver Data Grid Paged Query
	qData := "SELECT * FROM customers ORDER BY customer_id LIMIT 1 OFFSET 1"
	rowsData, err := db.QueryContext(ctx, qData)
	if err != nil {
		t.Fatalf("LIMIT OFFSET query failed: %v", err)
	}
	defer rowsData.Close()

	dataRows := 0
	for rowsData.Next() {
		dataRows++
	}
	if dataRows != 1 {
		t.Fatalf("expected 1 paged customer record, got %d", dataRows)
	}
}

// 2. High-Concurrency GUI Client Stress Test over PGWire
func TestToolAudit_PGWire_ConcurrentGUIStress(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable&connect_timeout=5", port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = db.ExecContext(ctx, "CREATE TABLE accounts (account_id INT PRIMARY KEY, owner VARCHAR(100), balance NUMERIC(18,2));")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	_, err = db.ExecContext(ctx, "INSERT INTO accounts VALUES (1, 'User 1', 1000.00), (2, 'User 2', 2000.00);")
	if err != nil {
		t.Fatalf("seed accounts failed: %v", err)
	}

	const numWorkers = 8
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for w := 0; w < numWorkers; w++ {
		go func(workerID int) {
			defer wg.Done()
			wCtx, wCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer wCancel()

			for i := 0; i < iterations; i++ {
				switch (workerID + i) % 3 {
				case 0:
					// Metadata introspection
					rows, err := db.QueryContext(wCtx, "SELECT c.relname, n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relkind = 'r'")
					if err != nil {
						t.Errorf("worker %d iteration %d pg_class failed: %v", workerID, i, err)
						return
					}
					cnt := 0
					for rows.Next() {
						cnt++
					}
					rows.Close()
					if cnt == 0 {
						t.Errorf("worker %d iteration %d returned 0 tables", workerID, i)
						return
					}

				case 1:
					// Paged data query
					rows, err := db.QueryContext(wCtx, "SELECT * FROM accounts ORDER BY account_id LIMIT 10 OFFSET 0")
					if err != nil {
						t.Errorf("worker %d iteration %d LIMIT OFFSET failed: %v", workerID, i, err)
						return
					}
					for rows.Next() {
					}
					rows.Close()

				case 2:
					// Concurrent DML
					accID := 10000 + workerID*1000 + i
					_, err := db.ExecContext(wCtx, fmt.Sprintf("INSERT INTO accounts VALUES (%d, 'Concurrent User', 500.00)", accID))
					if err != nil {
						t.Errorf("worker %d iteration %d INSERT failed: %v", workerID, i, err)
						return
					}
				}
			}
		}(w)
	}

	wg.Wait()
}

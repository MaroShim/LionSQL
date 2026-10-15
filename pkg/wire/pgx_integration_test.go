package wire_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestDriverIntegration_PGX_Connect(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable", port)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("pgx.Connect failed: %v", err)
	}
	defer conn.Close(ctx)

	var result int
	if err := conn.QueryRow(ctx, "SELECT 40 + 2").Scan(&result); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if result != 42 {
		t.Fatalf("expected 42, got %d", result)
	}
}

func TestDriverIntegration_PGX_QueryParams(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable", port)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("pgx.Connect failed: %v", err)
	}
	defer conn.Close(ctx)

	var res int
	if err := conn.QueryRow(ctx, "SELECT $1 + $2", 15, 27).Scan(&res); err != nil {
		t.Fatalf("QueryRow with params failed: %v", err)
	}
	if res != 42 {
		t.Fatalf("expected 42, got %d", res)
	}
}

func TestDriverIntegration_PGX_TableCRUD(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable", port)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("pgx.Connect failed: %v", err)
	}
	defer conn.Close(ctx)

	// DDL
	_, err = conn.Exec(ctx, "CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(50), active INT)")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Insert
	tag, err := conn.Exec(ctx, "INSERT INTO users (id, name, active) VALUES (1, 'Alice', 1)")
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expected 1 row affected, got %d", tag.RowsAffected())
	}

	// Query
	var id, active int
	var name string
	err = conn.QueryRow(ctx, "SELECT id, name, active FROM users WHERE id = 1").Scan(&id, &name, &active)
	if err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if id != 1 || name != "Alice" || active != 1 {
		t.Fatalf("unexpected query result: %d, %s, %d", id, name, active)
	}

	// Update
	tag, err = conn.Exec(ctx, "UPDATE users SET active = 0 WHERE id = 1")
	if err != nil {
		t.Fatalf("UPDATE failed: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expected 1 row affected, got %d", tag.RowsAffected())
	}

	// Verify update
	err = conn.QueryRow(ctx, "SELECT active FROM users WHERE id = 1").Scan(&active)
	if err != nil {
		t.Fatalf("QueryRow active failed: %v", err)
	}
	if active != 0 {
		t.Fatalf("expected active 0, got %d", active)
	}
}

func TestDriverIntegration_PGX_Transaction(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable", port)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("pgx.Connect failed: %v", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, "CREATE TABLE items (id INT PRIMARY KEY, qty INT)"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Begin + Rollback
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("conn.Begin failed: %v", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO items (id, qty) VALUES (1, 100)"); err != nil {
		t.Fatalf("tx.Exec failed: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("tx.Rollback failed: %v", err)
	}

	var cnt int
	if err := conn.QueryRow(ctx, "SELECT COUNT(*) FROM items").Scan(&cnt); err != nil {
		t.Fatalf("QueryRow after rollback failed: %v", err)
	}
	if cnt != 0 {
		t.Fatalf("expected 0 items after rollback, got %d", cnt)
	}

	// Begin + Commit
	tx2, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("conn.Begin failed: %v", err)
	}
	if _, err := tx2.Exec(ctx, "INSERT INTO items (id, qty) VALUES (2, 200)"); err != nil {
		t.Fatalf("tx2.Exec failed: %v", err)
	}
	if err := tx2.Commit(ctx); err != nil {
		t.Fatalf("tx2.Commit failed: %v", err)
	}

	if err := conn.QueryRow(ctx, "SELECT COUNT(*) FROM items").Scan(&cnt); err != nil {
		t.Fatalf("QueryRow after commit failed: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expected 1 item after commit, got %d", cnt)
	}
}

func TestDriverIntegration_PGX_BatchPipeline(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable", port)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("pgx.Connect failed: %v", err)
	}
	defer conn.Close(ctx)

	batch := &pgx.Batch{}
	batch.Queue("SELECT 10 + 1")
	batch.Queue("SELECT 20 + 2")
	batch.Queue("SELECT 30 + 3")

	br := conn.SendBatch(ctx, batch)
	defer br.Close()

	var r1, r2, r3 int
	if err := br.QueryRow().Scan(&r1); err != nil {
		t.Fatalf("batch item 1 failed: %v", err)
	}
	if err := br.QueryRow().Scan(&r2); err != nil {
		t.Fatalf("batch item 2 failed: %v", err)
	}
	if err := br.QueryRow().Scan(&r3); err != nil {
		t.Fatalf("batch item 3 failed: %v", err)
	}

	if r1 != 11 || r2 != 22 || r3 != 33 {
		t.Fatalf("unexpected batch results: %d, %d, %d", r1, r2, r3)
	}
}

func TestDriverIntegration_PGX_StdlibSQL(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable", port)
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		t.Fatalf("sql.Open pgx failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("db.PingContext failed: %v", err)
	}

	var val int
	if err := db.QueryRowContext(ctx, "SELECT 100 * 2").Scan(&val); err != nil {
		t.Fatalf("QueryRowContext failed: %v", err)
	}
	if val != 200 {
		t.Fatalf("expected 200, got %d", val)
	}
}

func TestDriverIntegration_PGX_DataTypes(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable", port)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("pgx.Connect failed: %v", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, "CREATE TABLE types_test (id INT PRIMARY KEY, num FLOAT, is_valid INT, note VARCHAR(50))"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	if _, err := conn.Exec(ctx, "INSERT INTO types_test (id, num, is_valid, note) VALUES (1, 3.14, 1, 'pi')"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	var id, isValid int
	var num float64
	var note string
	if err := conn.QueryRow(ctx, "SELECT id, num, is_valid, note FROM types_test WHERE id = 1").Scan(&id, &num, &isValid, &note); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}

	if id != 1 || num != 3.14 || isValid != 1 || note != "pi" {
		t.Fatalf("unexpected values: id=%d, num=%f, isValid=%d, note=%s", id, num, isValid, note)
	}
}

func TestDriverIntegration_PGX_TSQLStoredProcedure(t *testing.T) {
	srv, port := startTestPostgresServer(t)
	defer srv.Close()

	connStr := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable", port)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("pgx.Connect failed: %v", err)
	}
	defer conn.Close(ctx)

	// 1. Prepare table
	if _, err := conn.Exec(ctx, "CREATE TABLE sp_emp (id INT PRIMARY KEY, name VARCHAR(50), salary INT)"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := conn.Exec(ctx, "INSERT INTO sp_emp (id, name, salary) VALUES (1, 'Alice', 100), (2, 'Bob', 200)"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 2. Define T-SQL stored procedure with parameter and control flow
	procSQL := `CREATE PROCEDURE sp_get_emp @target_id INT AS 
BEGIN 
    SELECT id, name, salary FROM sp_emp WHERE id = @target_id; 
END;`
	if _, err := conn.Exec(ctx, procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	// 3. Execute T-SQL stored procedure via PGWire using EXEC syntax
	var id, salary int
	var name string
	if err := conn.QueryRow(ctx, "EXEC sp_get_emp 1;").Scan(&id, &name, &salary); err != nil {
		t.Fatalf("EXEC sp_get_emp failed: %v", err)
	}
	if id != 1 || name != "Alice" || salary != 100 {
		t.Fatalf("expected (1, Alice, 100), got (%d, %s, %d)", id, name, salary)
	}

	// 4. Update via T-SQL procedure
	bonusProcSQL := `CREATE PROCEDURE sp_give_bonus @emp_id INT, @bonus INT AS 
BEGIN 
    UPDATE sp_emp SET salary = salary + @bonus WHERE id = @emp_id; 
END;`
	if _, err := conn.Exec(ctx, bonusProcSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE bonus failed: %v", err)
	}

	if _, err := conn.Exec(ctx, "EXEC sp_give_bonus 1, 50;"); err != nil {
		t.Fatalf("EXEC sp_give_bonus failed: %v", err)
	}

	// 5. Verify update succeeded
	if err := conn.QueryRow(ctx, "SELECT salary FROM sp_emp WHERE id = 1;").Scan(&salary); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if salary != 150 {
		t.Fatalf("expected salary 150, got %d", salary)
	}

	// 6. Test CALL syntax (PostgreSQL standard call syntax)
	if err := conn.QueryRow(ctx, "CALL sp_get_emp(2);").Scan(&id, &name, &salary); err != nil {
		t.Fatalf("CALL sp_get_emp(2) failed: %v", err)
	}
	if id != 2 || name != "Bob" || salary != 200 {
		t.Fatalf("expected (2, Bob, 200), got (%d, %s, %d)", id, name, salary)
	}
}


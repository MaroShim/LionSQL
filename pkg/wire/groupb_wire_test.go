package wire

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"testing"
	"time"

	_ "github.com/lib/pq"
	_ "github.com/microsoft/go-mssqldb"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/wire/tds"
)

// BUG-AUTH-ADMIN-PW-BYPASS-1: Admin account with password set must reject unauthenticated login
func TestGroupB_AdminPasswordBypass(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	authSvc := coord.AuthService()

	// Set password for admin account 'postgres' and 'sa'
	if err := authSvc.AlterLoginPassword("postgres", "SuperSecret123!"); err != nil {
		t.Fatalf("AlterLoginPassword postgres failed: %v", err)
	}
	if err := authSvc.AlterLoginPassword("sa", "SuperSecret123!"); err != nil {
		t.Fatalf("AlterLoginPassword sa failed: %v", err)
	}

	// 1. Verify PGWire
	pgSrv := NewServerWithCoordinator("127.0.0.1:0", cat, eng, nil, coord)
	if err := pgSrv.Start(); err != nil {
		t.Fatalf("Start PG server failed: %v", err)
	}
	defer pgSrv.Close()

	pgPort := pgSrv.Addr().(*net.TCPAddr).Port

	// Try unauthenticated or wrong password for postgres on PGWire: MUST FAIL
	connNoPw := fmt.Sprintf("host=127.0.0.1 port=%d user=postgres dbname=default sslmode=disable connect_timeout=2", pgPort)
	dbNoPw, err := sql.Open("postgres", connNoPw)
	if err == nil {
		defer dbNoPw.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := dbNoPw.PingContext(ctx); err == nil {
			t.Fatalf("Expected login failure without password for postgres account with password set, but ping succeeded")
		}
	}

	// Try with correct password: MUST SUCCEED
	connWithPw := fmt.Sprintf("host=127.0.0.1 port=%d user=postgres password=SuperSecret123! dbname=default sslmode=disable connect_timeout=2", pgPort)
	dbWithPw, err := sql.Open("postgres", connWithPw)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer dbWithPw.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := dbWithPw.PingContext(ctx); err != nil {
		t.Fatalf("Expected ping to succeed with correct password: %v", err)
	}

	// 2. Verify TDS
	tdsSrv := tds.NewServerWithCoordinator("127.0.0.1:0", cat, eng, nil, coord)
	if err := tdsSrv.Start(); err != nil {
		t.Fatalf("Start TDS server failed: %v", err)
	}
	defer tdsSrv.Close()

	tdsPort := tdsSrv.Addr().(*net.TCPAddr).Port

	// Try wrong password for sa on TDS: MUST FAIL
	connTdsWrong := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=WrongPassword;encrypt=disable;connection timeout=2", tdsPort)
	dbTdsWrong, err := sql.Open("sqlserver", connTdsWrong)
	if err == nil {
		defer dbTdsWrong.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := dbTdsWrong.PingContext(ctx); err == nil {
			t.Fatalf("Expected TDS ping to fail with wrong password for sa, but succeeded")
		}
	}

	// Try correct password for sa on TDS: MUST SUCCEED
	connTdsRight := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=SuperSecret123!;encrypt=disable;connection timeout=2", tdsPort)
	dbTdsRight, err := sql.Open("sqlserver", connTdsRight)
	if err != nil {
		t.Fatalf("sql.Open TDS failed: %v", err)
	}
	defer dbTdsRight.Close()
	ctxTds, cancelTds := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelTds()
	if err := dbTdsRight.PingContext(ctxTds); err != nil {
		t.Fatalf("Expected TDS ping to succeed with correct password for sa: %v", err)
	}
}

// BUG-COPY-DELIM-1: DELIMITER and NULL option parsing
func TestGroupB_CopyDelimiterAndNull(t *testing.T) {
	// Semicolon delimiter in CSV
	stmt1, err := ParseCopyStmt("COPY my_tbl FROM STDIN WITH (FORMAT CSV, DELIMITER ';', HEADER);")
	if err != nil {
		t.Fatalf("ParseCopyStmt failed: %v", err)
	}
	if stmt1.Delimiter != ';' {
		t.Errorf("Expected delimiter ';', got %q", stmt1.Delimiter)
	}
	if !stmt1.Header {
		t.Errorf("Expected header true")
	}

	// Tab delimiter with escape notation
	stmt2, err := ParseCopyStmt("COPY my_tbl FROM STDIN WITH (DELIMITER '\\t');")
	if err != nil {
		t.Fatalf("ParseCopyStmt failed: %v", err)
	}
	if stmt2.Delimiter != '\t' {
		t.Errorf("Expected delimiter '\\t', got %q", stmt2.Delimiter)
	}

	// Custom NULL string
	stmt3, err := ParseCopyStmt("COPY my_tbl FROM STDIN WITH (DELIMITER '|', NULL 'NIL');")
	if err != nil {
		t.Fatalf("ParseCopyStmt failed: %v", err)
	}
	if stmt3.Delimiter != '|' {
		t.Errorf("Expected delimiter '|', got %q", stmt3.Delimiter)
	}
	if stmt3.NullStr != "NIL" {
		t.Errorf("Expected NullStr 'NIL', got %q", stmt3.NullStr)
	}
}

// BUG-PGWIRE-PARAM-1: buildPreviewSQL parameter replacement without prefix collision ($1 vs $10)
func TestGroupB_PgWireParamPreview(t *testing.T) {
	query := "SELECT * FROM users WHERE id = $1 AND role_id = $10 AND org_id = $2;"
	// $1 is int (23), $2 is int (23), ..., $10 is int (23)
	paramTypes := make([]int32, 10)
	for i := range paramTypes {
		paramTypes[i] = 23
	}

	preview := buildPreviewSQL(query, paramTypes)
	expected := "SELECT * FROM users WHERE id = 1 AND role_id = 1 AND org_id = 1;"
	if preview != expected {
		t.Fatalf("Expected preview SQL %q, got %q", expected, preview)
	}
}

// BUG-PGWIRE-DESCRIBE-1: Qualified column names, brackets, and extended type inference
func TestGroupB_PgWireDescribeQualified(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	_ = eng.CreateTable(&storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "age", Type: storage.TypeInt},
		},
	})

	srv := NewServer("127.0.0.1:0", cat, eng)
	colNames := []string{"u.id", "u.name", "[users].[age]", "cnt"}
	sampleRow := map[string]any{
		"cnt": int64(100),
	}

	oids := srv.resolveColumnOIDs("SELECT u.id, u.name, [users].[age], COUNT(*) as cnt FROM users u", colNames, sampleRow)
	if len(oids) != 4 {
		t.Fatalf("Expected 4 OIDs, got %d", len(oids))
	}
	if oids[0] != 23 { // INT4
		t.Errorf("Expected oids[0] == 23 (INT4), got %d", oids[0])
	}
	if oids[1] != 1043 { // VARCHAR
		t.Errorf("Expected oids[1] == 1043 (VARCHAR), got %d", oids[1])
	}
	if oids[2] != 23 { // INT4
		t.Errorf("Expected oids[2] == 23 (INT4), got %d", oids[2])
	}
	if oids[3] != 23 && oids[3] != 20 { // INT4/INT8
		t.Errorf("Expected oids[3] == 20 or 23, got %d", oids[3])
	}

	// Test inferPgOID with storage.Value and storage.Decimal
	valInt := storage.IntValue(42)
	if oid := inferPgOID(valInt); oid != 23 {
		t.Errorf("inferPgOID(IntValue) expected 23, got %d", oid)
	}

	valDec := storage.DecimalValue("123.45")
	if oid := inferPgOID(valDec); oid != 1700 {
		t.Errorf("inferPgOID(DecimalValue) expected 1700, got %d", oid)
	}
}

// BUG-WIRE-COPY-TX-BYPASS-1: COPY IN inside explicit transaction must respect COMMIT / ROLLBACK
func TestGroupB_CopyTransactionIsolation(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	_ = eng.CreateTable(&storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	})

	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	srv := NewServerWithCoordinator("127.0.0.1:0", cat, eng, nil, coord)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start server failed: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("host=127.0.0.1 port=%d user=default dbname=default sslmode=disable connect_timeout=3", port)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	// 1. Begin transaction, COPY IN, then ROLLBACK: table must remain empty
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("db.Begin failed: %v", err)
	}

	stmt, err := tx.Prepare("COPY items (id, name) FROM STDIN;")
	if err != nil {
		t.Fatalf("tx.Prepare COPY failed: %v", err)
	}
	if _, err := stmt.Exec(1, "ItemA"); err != nil {
		t.Fatalf("stmt.Exec row 1 failed: %v", err)
	}
	if _, err := stmt.Exec(2, "ItemB"); err != nil {
		t.Fatalf("stmt.Exec row 2 failed: %v", err)
	}
	if _, err := stmt.Exec(); err != nil {
		t.Fatalf("stmt.Exec EOF failed: %v", err)
	}
	_ = stmt.Close()

	if err := tx.Rollback(); err != nil {
		t.Fatalf("tx.Rollback failed: %v", err)
	}

	// Verify table has 0 rows
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM items;").Scan(&count); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("Expected 0 rows after ROLLBACK, got %d", count)
	}

	// 2. Begin transaction, COPY IN, then COMMIT: table must have 2 rows
	tx2, err := db.Begin()
	if err != nil {
		t.Fatalf("db.Begin 2 failed: %v", err)
	}
	stmt2, err := tx2.Prepare("COPY items (id, name) FROM STDIN;")
	if err != nil {
		t.Fatalf("tx2.Prepare COPY failed: %v", err)
	}
	if _, err := stmt2.Exec(10, "Item10"); err != nil {
		t.Fatalf("stmt2.Exec row 1 failed: %v", err)
	}
	if _, err := stmt2.Exec(20, "Item20"); err != nil {
		t.Fatalf("stmt2.Exec row 2 failed: %v", err)
	}
	if _, err := stmt2.Exec(); err != nil {
		t.Fatalf("stmt2.Exec EOF failed: %v", err)
	}
	_ = stmt2.Close()

	if err := tx2.Commit(); err != nil {
		t.Fatalf("tx2.Commit failed: %v", err)
	}

	if err := db.QueryRow("SELECT COUNT(*) FROM items;").Scan(&count); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("Expected 2 rows after COMMIT, got %d", count)
	}
}

// BUG-WIRE-COPY-SILENT-CORRUPT-1: parseFieldVal must fail cleanly on invalid input syntax
func TestGroupB_CopyParseValidation(t *testing.T) {
	colInt := storage.Column{Name: "val_int", Type: storage.TypeInt}
	colDate := storage.Column{Name: "val_date", Type: storage.TypeDate}
	colFloat := storage.Column{Name: "val_float", Type: storage.TypeFloat}
	colDecimal := storage.Column{Name: "val_dec", Type: storage.TypeDecimal}

	// Valid inputs
	v, err := parseFieldVal("12345", colInt)
	if err != nil || v.IVal != 12345 {
		t.Fatalf("Expected 12345, got %v, err=%v", v, err)
	}

	// Invalid inputs: MUST return error
	if _, err := parseFieldVal("not_an_int", colInt); err == nil {
		t.Errorf("Expected error parsing invalid integer 'not_an_int', got nil")
	}
	if _, err := parseFieldVal("not_a_date", colDate); err == nil {
		t.Errorf("Expected error parsing invalid date 'not_a_date', got nil")
	}
	if _, err := parseFieldVal("not_a_float", colFloat); err == nil {
		t.Errorf("Expected error parsing invalid float 'not_a_float', got nil")
	}
	if _, err := parseFieldVal("invalid_numeric_$$", colDecimal); err == nil {
		t.Errorf("Expected error parsing invalid decimal 'invalid_numeric_$$', got nil")
	}
}

// BUG-WIRE-COPY-OUT-UNESCAPED-1: Text format escaping and unescaping
func TestGroupB_CopyTextEscaping(t *testing.T) {
	orig := "Line1\tLine2\r\nBackslash\\End"
	escaped := escapeCopyText(orig, '\t')
	expected := "Line1\\tLine2\\r\\nBackslash\\\\End"
	if escaped != expected {
		t.Fatalf("Expected escaped %q, got %q", expected, escaped)
	}

	unescaped := unescapeCopyText(escaped)
	if unescaped != orig {
		t.Fatalf("Expected unescaped %q, got %q", orig, unescaped)
	}

	// Split line containing escaped tab delimiter
	line := "col1\tcol\\tdelim\tcol3"
	fields := splitCopyTextLine(line, '\t')
	if len(fields) != 3 {
		t.Fatalf("Expected 3 fields, got %d: %v", len(fields), fields)
	}
	if unescapeCopyText(fields[1]) != "col\tdelim" {
		t.Fatalf("Expected unescaped field[1] to be %q, got %q", "col\tdelim", unescapeCopyText(fields[1]))
	}
}

// BUG-TDS-RPC-PREP-TX-BYPASS-1: TDS RPC prepared statement execution in active transaction
func TestGroupB_TdsRpcTransactionBinding(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	_ = eng.CreateTable(&storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "balance", Type: storage.TypeInt},
		},
	})

	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	srv := tds.NewServerWithCoordinator("127.0.0.1:0", cat, eng, nil, coord)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start TDS server failed: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)

	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open TDS failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Begin transaction
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	// Prepare and execute inside tx (triggers sp_prepare / sp_execute or sp_prepexec)
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO accounts (id, balance) VALUES (@p1, @p2)")
	if err != nil {
		t.Fatalf("tx.PrepareContext failed: %v", err)
	}

	if _, err := stmt.ExecContext(ctx, 100, 5000); err != nil {
		t.Fatalf("stmt.ExecContext failed: %v", err)
	}
	_ = stmt.Close()

	// Rollback transaction
	if err := tx.Rollback(); err != nil {
		t.Fatalf("tx.Rollback failed: %v", err)
	}

	// Check table: must have 0 rows!
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM accounts").Scan(&count); err != nil {
		t.Fatalf("QueryRowContext failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("Expected 0 rows in accounts after ROLLBACK, got %d", count)
	}
}

// BUG-TDS-RPC-PARAM-NAME-ORDER-1: TDS sp_executesql with inverted or named parameter ordering
func TestGroupB_TdsRpcExecuteSQLParamOrder(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	_ = eng.CreateTable(&storage.TableSchema{
		Name: "products",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	})

	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	srv := tds.NewServerWithCoordinator("127.0.0.1:0", cat, eng, nil, coord)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start TDS server failed: %v", err)
	}
	defer srv.Close()

	port := srv.Addr().(*net.TCPAddr).Port
	connStr := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=3", port)

	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		t.Fatalf("sql.Open TDS failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Direct query using parameter names in sp_executesql
	var name string
	row := db.QueryRowContext(ctx, "SELECT 'Laptop' AS name")
	if err := row.Scan(&name); err != nil {
		t.Fatalf("QueryRowContext failed: %v", err)
	}
	if name != "Laptop" {
		t.Fatalf("Expected 'Laptop', got %q", name)
	}

	// Execute with parameters using driver (driver uses sp_executesql under the hood)
	_, err = db.ExecContext(ctx, "INSERT INTO products (id, name) VALUES (@id, @name)", sql.Named("id", 1), sql.Named("name", "Phone"))
	if err != nil {
		t.Fatalf("ExecContext with named parameters failed: %v", err)
	}

	var prodName string
	err = db.QueryRowContext(ctx, "SELECT name FROM products WHERE id = @p_id", sql.Named("p_id", 1)).Scan(&prodName)
	if err != nil {
		t.Fatalf("QueryRowContext with named parameter failed: %v", err)
	}
	if prodName != "Phone" {
		t.Fatalf("Expected product name 'Phone', got %q", prodName)
	}
}

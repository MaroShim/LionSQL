package engine

import (
	"fmt"
	"testing"

	"sqlion/pkg/catalog"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
)

func setupTestCoordinatorWithTable(t *testing.T) (*Coordinator, storage.Engine) {
	t.Helper()
	eng := memengine.New()
	cat := catalog.New(eng)

	coord := NewCoordinator(cat, eng, DefaultConfig())

	// Create users table
	_, err := coord.ExecuteSQL(`CREATE TABLE users (
		id INT PRIMARY KEY,
		name VARCHAR,
		email VARCHAR,
		age INT
	)`)
	if err != nil {
		t.Fatalf("failed to create users table: %v", err)
	}

	// Insert test data
	_, err = coord.ExecuteSQL("INSERT INTO users VALUES (1, 'Alice', 'alice@sqlion.local', 30)")
	if err != nil {
		t.Fatalf("failed to insert Alice: %v", err)
	}
	_, err = coord.ExecuteSQL("INSERT INTO users VALUES (2, 'Bob', 'bob@sqlion.local', 25)")
	if err != nil {
		t.Fatalf("failed to insert Bob: %v", err)
	}
	_, err = coord.ExecuteSQL("INSERT INTO users VALUES (3, 'Charlie', 'charlie@sqlion.local', 40)")
	if err != nil {
		t.Fatalf("failed to insert Charlie: %v", err)
	}

	return coord, eng
}

func TestPKLookupBypass_IntPK(t *testing.T) {
	coord, _ := setupTestCoordinatorWithTable(t)

	initialBypassHits := coord.Metrics().PKBypassHits()

	// 1. Basic SELECT * point lookup
	res, err := coord.ExecuteSQL("SELECT * FROM users WHERE id = 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Success || res.RowsAffected != 1 || len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %+v", res)
	}
	if res.Rows[0]["name"] != "Alice" || res.Rows[0]["id"] != int64(1) {
		t.Errorf("unexpected row content: %+v", res.Rows[0])
	}
	if coord.Metrics().PKBypassHits() <= initialBypassHits {
		t.Errorf("expected bypass hit increment, got %d", coord.Metrics().PKBypassHits())
	}

	// 2. Projection list (name, email)
	hitsBefore := coord.Metrics().PKBypassHits()
	res, err = coord.ExecuteSQL("SELECT name, email FROM users WHERE id = 2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Success || len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %+v", res)
	}
	if res.Rows[0]["name"] != "Bob" || res.Rows[0]["email"] != "bob@sqlion.local" {
		t.Errorf("unexpected projected row: %+v", res.Rows[0])
	}
	if _, hasAge := res.Rows[0]["age"]; hasAge {
		t.Errorf("age column should not be projected")
	}
	if len(res.Columns) != 2 || res.Columns[0] != "name" || res.Columns[1] != "email" {
		t.Errorf("unexpected columns metadata: %+v", res.Columns)
	}
	if coord.Metrics().PKBypassHits() <= hitsBefore {
		t.Errorf("expected bypass hit increment for projected query")
	}

	// 3. Reverse operand: WHERE 3 = id
	res, err = coord.ExecuteSQL("SELECT * FROM users WHERE 3 = id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Charlie" {
		t.Errorf("unexpected row for reverse operand: %+v", res.Rows)
	}

	// 4. Bracketed identifiers: SELECT * FROM [users] WHERE [id] = 1
	res, err = coord.ExecuteSQL("SELECT * FROM [users] WHERE [id] = 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Alice" {
		t.Errorf("unexpected row for bracketed query: %+v", res.Rows)
	}

	// 5. Query non-existent PK: WHERE id = 999 (must return 0 rows, Success: true)
	res, err = coord.ExecuteSQL("SELECT * FROM users WHERE id = 999")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Success || res.RowsAffected != 0 || len(res.Rows) != 0 {
		t.Errorf("expected 0 rows for non-existent key, got %+v", res)
	}

	// 6. With LIMIT 1
	res, err = coord.ExecuteSQL("SELECT * FROM users WHERE id = 1 LIMIT 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Alice" {
		t.Errorf("unexpected row with LIMIT 1: %+v", res.Rows)
	}

	// 7. With TOP 1
	res, err = coord.ExecuteSQL("SELECT TOP 1 * FROM users WHERE id = 2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Bob" {
		t.Errorf("unexpected row with TOP 1: %+v", res.Rows)
	}

	// 8. With trailing semicolon and mixed whitespace
	res, err = coord.ExecuteSQL("   SELECT   name   FROM   users   WHERE   id = 3 ;  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Charlie" {
		t.Errorf("unexpected row with whitespace and semicolon: %+v", res.Rows)
	}
}

func TestPKLookupBypass_StringPK(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())

	_, err := coord.ExecuteSQL(`CREATE TABLE accounts (
		acc_no VARCHAR PRIMARY KEY,
		balance FLOAT,
		active BOOL
	)`)
	if err != nil {
		t.Fatalf("failed to create accounts table: %v", err)
	}

	_, err = coord.ExecuteSQL("INSERT INTO accounts VALUES ('ACC-001', 1500.5, true)")
	if err != nil {
		t.Fatalf("failed to insert account: %v", err)
	}

	hitsBefore := coord.Metrics().PKBypassHits()

	res, err := coord.ExecuteSQL("SELECT * FROM accounts WHERE acc_no = 'ACC-001'")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Success || len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %+v", res)
	}
	if res.Rows[0]["acc_no"] != "ACC-001" || res.Rows[0]["balance"] != 1500.5 || res.Rows[0]["active"] != true {
		t.Errorf("unexpected row content: %+v", res.Rows[0])
	}
	if coord.Metrics().PKBypassHits() <= hitsBefore {
		t.Errorf("expected bypass hit for string PK lookup")
	}

	// Non-existent string key
	res, err = coord.ExecuteSQL("SELECT * FROM accounts WHERE acc_no = 'ACC-NONE'")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RowsAffected != 0 || len(res.Rows) != 0 {
		t.Errorf("expected 0 rows for non-existent string key, got %+v", res)
	}
}

func TestPKLookupBypass_InTransaction(t *testing.T) {
	coord, _ := setupTestCoordinatorWithTable(t)

	sessID := "tx-sess-1"

	// 1. Begin transaction
	_, err := coord.ExecuteSessionSQL(sessID, "BEGIN TRANSACTION")
	if err != nil {
		t.Fatalf("failed to start transaction: %v", err)
	}

	// 2. Point lookup within transaction
	res, err := coord.ExecuteSessionSQL(sessID, "SELECT * FROM users WHERE id = 1")
	if err != nil {
		t.Fatalf("failed to query within transaction: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Alice" {
		t.Errorf("unexpected result inside transaction: %+v", res.Rows)
	}

	// 3. Commit
	_, err = coord.ExecuteSessionSQL(sessID, "COMMIT")
	if err != nil {
		t.Fatalf("failed to commit transaction: %v", err)
	}
}

func TestPKLookupBypass_NonPKFallback(t *testing.T) {
	coord, _ := setupTestCoordinatorWithTable(t)

	hitsBefore := coord.Metrics().PKBypassHits()

	// Condition on non-PK column 'name' -> must NOT bypass, but must succeed via normal engine
	res, err := coord.ExecuteSQL("SELECT * FROM users WHERE name = 'Alice'")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["id"] != int64(1) {
		t.Errorf("expected Alice via standard execution, got %+v", res.Rows)
	}
	if coord.Metrics().PKBypassHits() != hitsBefore {
		t.Errorf("non-PK query should not increment bypass hits, was %d, now %d", hitsBefore, coord.Metrics().PKBypassHits())
	}
}

func TestPKLookupBypass_ComplexFallback(t *testing.T) {
	coord, _ := setupTestCoordinatorWithTable(t)

	hitsBefore := coord.Metrics().PKBypassHits()

	// 1. Multiple conditions with AND
	res, err := coord.ExecuteSQL("SELECT * FROM users WHERE id = 1 AND age > 20")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Alice" {
		t.Errorf("expected Alice with AND condition, got %+v", res.Rows)
	}

	// 2. Aggregate function
	res, err = coord.ExecuteSQL("SELECT COUNT(*) AS cnt FROM users WHERE id = 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["cnt"]) != "1" {
		t.Errorf("expected count 1, got %+v", res.Rows)
	}

	// Bypass hit count should remain unchanged
	if coord.Metrics().PKBypassHits() != hitsBefore {
		t.Errorf("complex queries should not hit bypass cache")
	}
}

func TestPKLookupBypass_InvalidationOnDDL(t *testing.T) {
	coord, _ := setupTestCoordinatorWithTable(t)

	// Populate bypass plan
	res, err := coord.ExecuteSQL("SELECT * FROM users WHERE id = 1")
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("failed initial query: %v", err)
	}

	// 1. TRUNCATE TABLE
	_, err = coord.ExecuteSQL("TRUNCATE TABLE users")
	if err != nil {
		t.Fatalf("failed to truncate: %v", err)
	}

	// Query again: must return 0 rows
	res, err = coord.ExecuteSQL("SELECT * FROM users WHERE id = 1")
	if err != nil {
		t.Fatalf("unexpected error after truncate: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Errorf("expected 0 rows after truncate, got %d", len(res.Rows))
	}

	// 2. DROP TABLE
	_, err = coord.ExecuteSQL("DROP TABLE users")
	if err != nil {
		t.Fatalf("failed to drop table: %v", err)
	}

	_, err = coord.ExecuteSQL("SELECT * FROM users WHERE id = 1")
	if err == nil {
		t.Errorf("expected table not found error after drop, got nil")
	}
}

func BenchmarkPKLookupBypass(b *testing.B) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())

	_, _ = coord.ExecuteSQL(`CREATE TABLE benchmark_tbl (
		id INT PRIMARY KEY,
		val VARCHAR
	)`)
	for i := 1; i <= 100; i++ {
		_, _ = coord.ExecuteSQL(fmt.Sprintf("INSERT INTO benchmark_tbl VALUES (%d, 'val_%d')", i, i))
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		id := (i % 100) + 1
		q := fmt.Sprintf("SELECT * FROM benchmark_tbl WHERE id = %d", id)
		res, err := coord.ExecuteSQL(q)
		if err != nil || len(res.Rows) != 1 {
			b.Fatalf("benchmark query failed: %v", err)
		}
	}
}

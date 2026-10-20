package engine_test

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestVPerf_OnConflictIndexScan(t *testing.T) {
	storageEngine := memengine.New()
	defer storageEngine.Close()

	cat := catalog.New(storageEngine)
	coord := engine.NewCoordinator(cat, storageEngine, engine.DefaultConfig())
	defer coord.Close()

	sessID := "vperf-test-session"

	ddl := `
CREATE TABLE t_conflict (
    id INT PRIMARY KEY,
    email VARCHAR(100) UNIQUE,
    score INT
);
`
	if _, err := coord.ExecuteSessionSQL(sessID, ddl); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	// 1. Initial insert
	q1 := `INSERT INTO t_conflict (id, email, score) VALUES (1, 'alice@example.com', 10);`
	if _, err := coord.ExecuteSessionSQL(sessID, q1); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// 2. Conflict on unique secondary column with DO UPDATE
	q2 := `INSERT INTO t_conflict (id, email, score) VALUES (2, 'alice@example.com', 50)
           ON CONFLICT (email) DO UPDATE SET score = EXCLUDED.score;`
	if _, err := coord.ExecuteSessionSQL(sessID, q2); err != nil {
		t.Fatalf("Upsert DO UPDATE failed: %v", err)
	}

	// Verify updated score
	res, err := coord.ExecuteSessionSQL(sessID, `SELECT score FROM t_conflict WHERE id = 1;`)
	if err != nil {
		t.Fatalf("Select failed: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["score"]) != "50" {
		t.Fatalf("expected score 50, got %v", res.Rows)
	}

	// 3. Conflict on unique column with DO NOTHING
	q3 := `INSERT INTO t_conflict (id, email, score) VALUES (3, 'alice@example.com', 99)
           ON CONFLICT (email) DO NOTHING;`
	if _, err := coord.ExecuteSessionSQL(sessID, q3); err != nil {
		t.Fatalf("Upsert DO NOTHING failed: %v", err)
	}

	// Verify score remains 50
	res2, err := coord.ExecuteSessionSQL(sessID, `SELECT score FROM t_conflict WHERE id = 1;`)
	if err != nil {
		t.Fatalf("Select failed: %v", err)
	}
	if len(res2.Rows) != 1 || fmt.Sprintf("%v", res2.Rows[0]["score"]) != "50" {
		t.Fatalf("expected score 50, got %v", res2.Rows)
	}
}

func TestVPerf_AutocommitSessionReuse(t *testing.T) {
	storageEngine := memengine.New()
	defer storageEngine.Close()

	cat := catalog.New(storageEngine)
	coord := engine.NewCoordinator(cat, storageEngine, engine.DefaultConfig())
	defer coord.Close()

	sessID := "vperf-sess-reuse"

	if _, err := coord.ExecuteSessionSQL(sessID, `CREATE TABLE t_reuse (id INT PRIMARY KEY, val INT);`); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Execute consecutive autocommit queries
	for i := 1; i <= 20; i++ {
		insertSQL := fmt.Sprintf("INSERT INTO t_reuse VALUES (%d, %d);", i, i*10)
		if _, err := coord.ExecuteSessionSQL(sessID, insertSQL); err != nil {
			t.Fatalf("Insert %d failed: %v", i, err)
		}
	}

	res, err := coord.ExecuteSessionSQL(sessID, `SELECT COUNT(*) AS cnt FROM t_reuse;`)
	if err != nil {
		t.Fatalf("Select count failed: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["cnt"]) != "20" {
		t.Fatalf("expected 20 rows, got %v", res.Rows)
	}
}

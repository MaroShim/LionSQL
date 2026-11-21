package grammar

import (
	"math/rand"
	"strings"
	"testing"
)

func TestGrammar_TableSchemaGeneration(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	ts := GenerateTableSchema(rng, "test_fuzz", 5)

	if ts.Name != "test_fuzz" {
		t.Fatalf("expected name test_fuzz, got %s", ts.Name)
	}
	if len(ts.Columns) != 5 {
		t.Fatalf("expected 5 columns, got %d", len(ts.Columns))
	}
	if !ts.Columns[0].IsPK {
		t.Fatalf("expected first column to be PK")
	}

	ddl := ts.GenerateCreateTableDDL()
	if !strings.HasPrefix(ddl, "CREATE TABLE test_fuzz") {
		t.Fatalf("unexpected DDL: %s", ddl)
	}
	if !strings.Contains(ddl, "PRIMARY KEY") {
		t.Fatalf("expected PRIMARY KEY in DDL: %s", ddl)
	}

	dmls := ts.GenerateInsertDML(rng, 10)
	if len(dmls) != 10 {
		t.Fatalf("expected 10 insert statements, got %d", len(dmls))
	}
	for _, dml := range dmls {
		if !strings.HasPrefix(dml, "INSERT INTO test_fuzz") {
			t.Fatalf("unexpected DML: %s", dml)
		}
	}
}

func TestGrammar_NoRECPairGeneration(t *testing.T) {
	rng := rand.New(rand.NewSource(123))
	ts := GenerateTableSchema(rng, "items", 4)

	for i := 0; i < 20; i++ {
		pair := GenerateNoRECPair(rng, ts, 3)
		if !strings.HasPrefix(pair.OptimizedSQL, "SELECT COUNT(*) AS total FROM items WHERE") {
			t.Fatalf("invalid optimized SQL: %s", pair.OptimizedSQL)
		}
		if !strings.HasPrefix(pair.UnoptimizedSQL, "SELECT COALESCE(SUM(CASE WHEN") {
			t.Fatalf("invalid unoptimized SQL: %s", pair.UnoptimizedSQL)
		}
		if len(pair.Predicate) == 0 {
			t.Fatalf("empty predicate")
		}
	}
}

func TestGrammar_SelectQueryGeneration(t *testing.T) {
	rng := rand.New(rand.NewSource(999))
	ts := GenerateTableSchema(rng, "orders", 6)

	for i := 0; i < 10; i++ {
		q := GenerateSelectQuery(rng, ts, 2)
		if !strings.HasPrefix(q.SQL, "SELECT") {
			t.Fatalf("invalid SQL: %s", q.SQL)
		}
		if !strings.Contains(q.SQL, "FROM orders WHERE") {
			t.Fatalf("missing FROM orders WHERE in: %s", q.SQL)
		}
		if !strings.Contains(q.SQL, "ORDER BY id;") {
			t.Fatalf("missing ORDER BY in: %s", q.SQL)
		}
	}
}

package engine

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestSubquery_WildcardProjectionLeakage(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE _t_n1 (id INT PRIMARY KEY, val INT);"); err != nil {
		t.Fatalf("create _t_n1 failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("CREATE TABLE _t_n2 (id INT PRIMARY KEY, val INT);"); err != nil {
		t.Fatalf("create _t_n2 failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO _t_n1 VALUES (1, 10), (2, 20);"); err != nil {
		t.Fatalf("insert _t_n1 failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO _t_n2 VALUES (1, 10);"); err != nil {
		t.Fatalf("insert _t_n2 failed: %v", err)
	}

	// 1. IN subquery (decorrelated to SEMI join)
	resIn, err := coord.ExecuteSQL("SELECT * FROM _t_n1 WHERE val IN (SELECT val FROM _t_n2);")
	if err != nil {
		t.Fatalf("SELECT * with IN subquery failed: %v", err)
	}
	if len(resIn.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(resIn.Rows))
	}
	if len(resIn.Columns) != 2 {
		t.Fatalf("expected exactly 2 columns (id, val), got %d: %v", len(resIn.Columns), resIn.Columns)
	}

	// 2. NOT IN subquery (decorrelated to ANTI join)
	resNotIn, err := coord.ExecuteSQL("SELECT * FROM _t_n1 WHERE val NOT IN (SELECT val FROM _t_n2);")
	if err != nil {
		t.Fatalf("SELECT * with NOT IN subquery failed: %v", err)
	}
	if len(resNotIn.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(resNotIn.Rows))
	}
	if len(resNotIn.Columns) != 2 {
		t.Fatalf("expected exactly 2 columns (id, val), got %d: %v", len(resNotIn.Columns), resNotIn.Columns)
	}
}

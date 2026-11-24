package engine_test

import (
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestSubquery_NoSuchColumn_ErrorPropagation(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE t_sub1 (id INT, val INT);"); err != nil {
		t.Fatalf("create t_sub1 failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("CREATE TABLE t_sub2 (id INT, val INT);"); err != nil {
		t.Fatalf("create t_sub2 failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO t_sub2 VALUES (1, 10);"); err != nil {
		t.Fatalf("insert t_sub2 failed: %v", err)
	}

	// 1. Projection Correlated Subquery with non-existent column must fail-closed with error
	queryFail := "SELECT (SELECT 1 FROM t_sub1 WHERE no_such_col = t_sub2.val) FROM t_sub2;"
	res, err := coord.ExecuteSQL(queryFail)
	if err == nil {
		t.Fatalf("expected error for non-existent column in subquery, got nil err and res: %+v", res)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "no_such_col") && !strings.Contains(strings.ToLower(err.Error()), "column") {
		t.Fatalf("expected error message to mention missing column, got: %v", err)
	}

	// 2. Projection Correlated Subquery with valid column must succeed
	if _, err := coord.ExecuteSQL("INSERT INTO t_sub1 VALUES (1, 10);"); err != nil {
		t.Fatalf("insert t_sub1 failed: %v", err)
	}
	queryPass := "SELECT (SELECT 1 FROM t_sub1 WHERE val = t_sub2.val) FROM t_sub2;"
	resPass, err := coord.ExecuteSQL(queryPass)
	if err != nil {
		t.Fatalf("expected valid correlated subquery to succeed, got: %v", err)
	}
	if len(resPass.Rows) != 1 {
		t.Fatalf("expected 1 row returned, got %d", len(resPass.Rows))
	}
}

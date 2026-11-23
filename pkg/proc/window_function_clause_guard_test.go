package proc_test

import (
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestWindowFunction_DisallowedInWhereAndHaving(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	_, err := coord.ExecuteSQL("CREATE TABLE t_win_err (val INT);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	_, err = coord.ExecuteSQL("INSERT INTO t_win_err VALUES (10), (20);")
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 1. Window function in WHERE clause must fail with error!
	_, errWhere := coord.ExecuteSQL("SELECT val FROM t_win_err WHERE ROW_NUMBER() OVER () > 1;")
	if errWhere == nil {
		t.Fatalf("expected error for window function in WHERE clause, but query succeeded")
	}
	if !strings.Contains(strings.ToLower(errWhere.Error()), "window") ||
		!strings.Contains(strings.ToLower(errWhere.Error()), "where") {
		t.Fatalf("expected window function error in WHERE, got: %v", errWhere)
	}

	// 2. Window function in HAVING clause must fail with error!
	_, errHaving := coord.ExecuteSQL("SELECT val FROM t_win_err GROUP BY val HAVING COUNT(*) OVER () > 1;")
	if errHaving == nil {
		t.Fatalf("expected error for window function in HAVING clause, but query succeeded")
	}
	if !strings.Contains(strings.ToLower(errHaving.Error()), "window") ||
		!strings.Contains(strings.ToLower(errHaving.Error()), "having") {
		t.Fatalf("expected window function error in HAVING, got: %v", errHaving)
	}
}

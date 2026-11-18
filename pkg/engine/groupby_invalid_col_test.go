package engine

import (
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestGroupBy_InvalidColumnValidation(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE _t_grp (id INT PRIMARY KEY, dept_id INT);"); err != nil {
		t.Fatalf("create table failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO _t_grp VALUES (1, 10), (2, 10), (3, 20);"); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// 1. Valid alias GROUP BY should return 2 rows (dept 10: 2, dept 20: 1)
	resValid, err := coord.ExecuteSQL("SELECT dept_id AS d, COUNT(*) AS cnt FROM _t_grp GROUP BY d;")
	if err != nil {
		t.Fatalf("valid alias GROUP BY failed: %v", err)
	}
	if len(resValid.Rows) != 2 {
		t.Fatalf("expected 2 groups for valid alias GROUP BY, got %d (rows: %v)", len(resValid.Rows), resValid.Rows)
	}

	// 2. Invalid column in GROUP BY must fail closed with explicit diagnostic error
	_, errInvalid := coord.ExecuteSQL("SELECT dept_id, COUNT(*) AS cnt FROM _t_grp GROUP BY non_existent_col;")
	if errInvalid == nil {
		t.Fatalf("expected error for non_existent_col in GROUP BY, got success")
	}
	if !strings.Contains(errInvalid.Error(), "non_existent_col") || !strings.Contains(errInvalid.Error(), "does not exist") {
		t.Errorf("expected 'column \"non_existent_col\" does not exist', got: %v", errInvalid)
	}
}

func TestWindow_InvalidColumnValidation(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE _t_win (id INT PRIMARY KEY, val INT);"); err != nil {
		t.Fatalf("create table failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO _t_win VALUES (1, 10), (2, 20);"); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// 1. Invalid column in PARTITION BY must fail closed
	_, errPart := coord.ExecuteSQL("SELECT id, ROW_NUMBER() OVER (PARTITION BY bad_col ORDER BY id) FROM _t_win;")
	if errPart == nil {
		t.Fatalf("expected error for bad_col in PARTITION BY, got success")
	}
	if !strings.Contains(errPart.Error(), "bad_col") || !strings.Contains(errPart.Error(), "does not exist") {
		t.Errorf("expected error mentioning bad_col, got: %v", errPart)
	}

	// 2. Invalid column in ORDER BY must fail closed
	_, errOrd := coord.ExecuteSQL("SELECT id, ROW_NUMBER() OVER (ORDER BY invalid_ord_col) FROM _t_win;")
	if errOrd == nil {
		t.Fatalf("expected error for invalid_ord_col in ORDER BY, got success")
	}
	if !strings.Contains(errOrd.Error(), "invalid_ord_col") || !strings.Contains(errOrd.Error(), "does not exist") {
		t.Errorf("expected error mentioning invalid_ord_col, got: %v", errOrd)
	}
}

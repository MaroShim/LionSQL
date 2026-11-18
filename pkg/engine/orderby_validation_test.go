package engine

import (
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestOrderBy_ValidationAndOrdinals(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE _t_ord (id INT PRIMARY KEY, val INT);"); err != nil {
		t.Fatalf("create table failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO _t_ord VALUES (1, 10), (2, 20);"); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// 1. Invalid column in ORDER BY must fail closed
	_, errInvalidCol := coord.ExecuteSQL("SELECT id, val FROM _t_ord ORDER BY non_existent_col;")
	if errInvalidCol == nil {
		t.Fatalf("expected error for non_existent_col in ORDER BY, got success")
	}
	if !strings.Contains(errInvalidCol.Error(), "non_existent_col") || !strings.Contains(errInvalidCol.Error(), "does not exist") {
		t.Errorf("expected error mentioning non_existent_col does not exist, got: %v", errInvalidCol)
	}

	// 2. Out-of-bounds ordinal 0 must fail closed
	_, errOrdZero := coord.ExecuteSQL("SELECT id, val FROM _t_ord ORDER BY 0;")
	if errOrdZero == nil {
		t.Fatalf("expected error for ORDER BY 0, got success")
	}
	if !strings.Contains(errOrdZero.Error(), "ORDER BY position 0 is not in select list") {
		t.Errorf("expected error mentioning ORDER BY position 0, got: %v", errOrdZero)
	}

	// 3. Out-of-bounds ordinal 999 must fail closed
	_, errOrd999 := coord.ExecuteSQL("SELECT id, val FROM _t_ord ORDER BY 999;")
	if errOrd999 == nil {
		t.Fatalf("expected error for ORDER BY 999, got success")
	}
	if !strings.Contains(errOrd999.Error(), "ORDER BY position 999 is not in select list") {
		t.Errorf("expected error mentioning ORDER BY position 999, got: %v", errOrd999)
	}

	// 4. Valid ordinal 1 should succeed
	resOrd1, err := coord.ExecuteSQL("SELECT id, val FROM _t_ord ORDER BY 1 DESC;")
	if err != nil {
		t.Fatalf("valid ORDER BY 1 failed: %v", err)
	}
	if len(resOrd1.Rows) != 2 || resOrd1.Rows[0]["id"] != int64(2) {
		t.Errorf("expected id 2 first, got %v", resOrd1.Rows)
	}

	// 5. Valid projection alias should succeed
	resAlias, err := coord.ExecuteSQL("SELECT id AS my_id, val FROM _t_ord ORDER BY my_id DESC;")
	if err != nil {
		t.Fatalf("valid alias ORDER BY failed: %v", err)
	}
	if len(resAlias.Rows) != 2 || resAlias.Rows[0]["my_id"] != int64(2) {
		t.Errorf("expected my_id 2 first, got %v", resAlias.Rows)
	}
}

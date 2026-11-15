//go:build cgo && wiredtiger

package engine_test

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/wiredtiger"
)

func TestUpdate_WiredTiger_ColumnSwapAtomicSnapshot(t *testing.T) {
	wtDir := t.TempDir()
	eng := wiredtiger.New()
	if err := eng.Open(wtDir); err != nil {
		t.Fatalf("wiredtiger.Open failed: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	_, err := coord.ExecuteSQL(`CREATE TABLE wt_swap_tbl (id INT PRIMARY KEY, a INT, b INT);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`INSERT INTO wt_swap_tbl (id, a, b) VALUES (1, 10, 20);`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 1. Basic Column Swap: SET a = b, b = a
	_, err = coord.ExecuteSQL(`UPDATE wt_swap_tbl SET a = b, b = a WHERE id = 1;`)
	if err != nil {
		t.Fatalf("UPDATE SET a = b, b = a failed: %v", err)
	}

	res, err := coord.ExecuteSQL(`SELECT a, b FROM wt_swap_tbl WHERE id = 1;`)
	if err != nil || len(res.Rows) == 0 {
		t.Fatalf("SELECT failed: %v", err)
	}
	if fmt.Sprintf("%v", res.Rows[0]["a"]) != "20" || fmt.Sprintf("%v", res.Rows[0]["b"]) != "10" {
		t.Errorf("expected a=20, b=10 (swapped), got a=%v, b=%v", res.Rows[0]["a"], res.Rows[0]["b"])
	}

	// 2. Self-referential arithmetic: SET a = a + b, b = a
	_, err = coord.ExecuteSQL(`UPDATE wt_swap_tbl SET a = a + b, b = a WHERE id = 1;`)
	if err != nil {
		t.Fatalf("UPDATE SET a = a + b, b = a failed: %v", err)
	}
	res2, err := coord.ExecuteSQL(`SELECT a, b FROM wt_swap_tbl WHERE id = 1;`)
	if err != nil || len(res2.Rows) == 0 {
		t.Fatalf("SELECT failed: %v", err)
	}
	if fmt.Sprintf("%v", res2.Rows[0]["a"]) != "30" || fmt.Sprintf("%v", res2.Rows[0]["b"]) != "20" {
		t.Errorf("expected a=30, b=20, got a=%v, b=%v", res2.Rows[0]["a"], res2.Rows[0]["b"])
	}

	// 3. Reversed order: SET b = a, a = a + b
	_, err = coord.ExecuteSQL(`UPDATE wt_swap_tbl SET a = 10, b = 20 WHERE id = 1;`)
	if err != nil {
		t.Fatalf("reset failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`UPDATE wt_swap_tbl SET b = a, a = a + b WHERE id = 1;`)
	if err != nil {
		t.Fatalf("UPDATE SET b = a, a = a + b failed: %v", err)
	}
	resRev, err := coord.ExecuteSQL(`SELECT a, b FROM wt_swap_tbl WHERE id = 1;`)
	if err != nil || len(resRev.Rows) == 0 {
		t.Fatalf("SELECT failed: %v", err)
	}
	if fmt.Sprintf("%v", resRev.Rows[0]["a"]) != "30" || fmt.Sprintf("%v", resRev.Rows[0]["b"]) != "10" {
		t.Errorf("expected a=30, b=10, got a=%v, b=%v", resRev.Rows[0]["a"], resRev.Rows[0]["b"])
	}
}

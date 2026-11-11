//go:build cgo && wiredtiger

package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/wiredtiger"
)

func TestCoordinator_WiredTiger_TempTableAndTableVarBypass(t *testing.T) {
	wtDir := filepath.Join(t.TempDir(), "wtdata")
	eng := wiredtiger.New()
	if err := eng.Open(wtDir); err != nil {
		t.Fatalf("wiredtiger.Open failed: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sid := "wthybridsess1"
	if res, err := coord.ExecuteSessionSQL(sid, `
		CREATE TABLE wt_orders (id INT PRIMARY KEY, cust VARCHAR(50), amount INT);
		INSERT INTO wt_orders VALUES (1, 'Alice', 120), (2, 'Bob', 80), (3, 'Carol', 350);
	`); err != nil || !res.Success {
		t.Fatalf("setup wt_orders failed: res=%+v err=%v", res, err)
	}

	// Snapshot .wt files after permanent table setup
	beforeWT := make(map[string]struct{})
	entries, err := os.ReadDir(wtDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".wt") {
			beforeWT[e.Name()] = struct{}{}
		}
	}

	// Execute #temp table + DECLARE @tvar TABLE joined against WiredTiger permanent table
	batch := `
	CREATE TABLE #high_orders (id INT PRIMARY KEY, amount INT);
	INSERT INTO #high_orders SELECT id, amount FROM wt_orders WHERE amount >= 100;

	DECLARE @discounts TABLE (id INT PRIMARY KEY, bonus INT);
	INSERT INTO @discounts VALUES (1, 10), (3, 50);

	SELECT o.id, o.cust, h.amount, d.bonus
	FROM wt_orders o
	JOIN #high_orders h ON o.id = h.id
	JOIN @discounts d ON o.id = d.id
	ORDER BY o.id ASC;
	`
	res, err := coord.ExecuteSessionSQL(sid, batch)
	if err != nil || !res.Success {
		t.Fatalf("hybrid query failed: res=%+v err=%v", res, err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 joined rows, got %d (%v)", len(res.Rows), res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["cust"]) != "Alice" || fmt.Sprintf("%v", res.Rows[1]["cust"]) != "Carol" {
		t.Fatalf("unexpected joined rows: %v", res.Rows)
	}

	// Explicit mixed transaction rollback test (#high_orders + wt_orders)
	if res, err := coord.ExecuteSessionSQL(sid, `
		BEGIN TRANSACTION;
		INSERT INTO wt_orders VALUES (4, 'Dave', 500);
		INSERT INTO #high_orders VALUES (4, 500);
		ROLLBACK TRANSACTION;
	`); err != nil || !res.Success {
		t.Fatalf("mixed rollback tx failed: res=%+v err=%v", res, err)
	}

	chkPerm, err := coord.ExecuteSessionSQL(sid, "SELECT COUNT(*) AS cnt FROM wt_orders;")
	if err != nil || !chkPerm.Success || fmt.Sprintf("%v", chkPerm.Rows[0]["cnt"]) != "3" {
		t.Fatalf("expected wt_orders count=3 after rollback, got %+v err=%v", chkPerm, err)
	}
	chkTemp, err := coord.ExecuteSessionSQL(sid, "SELECT COUNT(*) AS cnt FROM #high_orders;")
	if err != nil || !chkTemp.Success || fmt.Sprintf("%v", chkTemp.Rows[0]["cnt"]) != "2" {
		t.Fatalf("expected #high_orders count=2 after rollback, got %+v err=%v", chkTemp, err)
	}

	// Verify zero .wt files created for #high_orders or @discounts
	afterEntries, err := os.ReadDir(wtDir)
	if err != nil {
		t.Fatalf("ReadDir after failed: %v", err)
	}
	for _, e := range afterEntries {
		if strings.HasSuffix(e.Name(), ".wt") {
			if _, ok := beforeWT[e.Name()]; !ok {
				t.Fatalf("unexpected .wt file created for temp table / table variable: %s", e.Name())
			}
		}
	}
}

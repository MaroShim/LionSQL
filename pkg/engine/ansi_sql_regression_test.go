package engine

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestAnsiSQL_NewBugsRegression(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	// 1. TRIM(BOTH/LEADING/TRAILING FROM)
	resTrimBoth, err := coord.ExecuteSQL("SELECT TRIM(BOTH 'x' FROM 'xxxhelloxxx') AS t;")
	if err != nil {
		t.Fatalf("TRIM BOTH failed: %v", err)
	}
	if fmt.Sprintf("%v", resTrimBoth.Rows[0]["t"]) != "hello" {
		t.Errorf("expected 'hello', got %v", resTrimBoth.Rows[0]["t"])
	}

	resTrimLead, err := coord.ExecuteSQL("SELECT TRIM(LEADING 'x' FROM 'xxxhelloxxx') AS t;")
	if err != nil {
		t.Fatalf("TRIM LEADING failed: %v", err)
	}
	if fmt.Sprintf("%v", resTrimLead.Rows[0]["t"]) != "helloxxx" {
		t.Errorf("expected 'helloxxx', got %v", resTrimLead.Rows[0]["t"])
	}

	resTrimTrail, err := coord.ExecuteSQL("SELECT TRIM(TRAILING 'x' FROM 'xxxhelloxxx') AS t;")
	if err != nil {
		t.Fatalf("TRIM TRAILING failed: %v", err)
	}
	if fmt.Sprintf("%v", resTrimTrail.Rows[0]["t"]) != "xxxhello" {
		t.Errorf("expected 'xxxhello', got %v", resTrimTrail.Rows[0]["t"])
	}

	// 2. SUBSTRING 2 arguments
	resSub2, err := coord.ExecuteSQL("SELECT SUBSTRING('hello', 2) AS s;")
	if err != nil {
		t.Fatalf("SUBSTRING 2-arg failed: %v", err)
	}
	if fmt.Sprintf("%v", resSub2.Rows[0]["s"]) != "ello" {
		t.Errorf("expected 'ello', got %v", resSub2.Rows[0]["s"])
	}

	// 3. SETOP INTERSECT precedence over UNION
	resSetop, err := coord.ExecuteSQL("SELECT 1 AS val UNION ALL SELECT 2 INTERSECT SELECT 2;")
	if err != nil {
		t.Fatalf("SETOP INTERSECT failed: %v", err)
	}
	if len(resSetop.Rows) != 2 {
		t.Fatalf("expected 2 rows for UNION ALL + INTERSECT, got %d (rows: %v)", len(resSetop.Rows), resSetop.Rows)
	}

	// 4. Window FIRST_VALUE and LAST_VALUE
	if _, err := coord.ExecuteSQL(`
		CREATE TABLE _t_win_fl (id INT PRIMARY KEY, dept VARCHAR(10), val INT);
		INSERT INTO _t_win_fl VALUES (1, 'HR', 100), (2, 'HR', 200), (3, 'IT', 300), (4, 'IT', 400);
	`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	resWin, err := coord.ExecuteSQL(`
		SELECT id,
		       FIRST_VALUE(val) OVER (PARTITION BY dept ORDER BY id) AS fv,
		       LAST_VALUE(val) OVER (PARTITION BY dept ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) AS lv
		FROM _t_win_fl
		ORDER BY id;
	`)
	if err != nil {
		t.Fatalf("FIRST_VALUE/LAST_VALUE query failed: %v", err)
	}
	if len(resWin.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(resWin.Rows))
	}

	// HR first is 100, last is 200
	if fmt.Sprintf("%v", resWin.Rows[0]["fv"]) != "100" || fmt.Sprintf("%v", resWin.Rows[0]["lv"]) != "200" {
		t.Errorf("row 1 expected fv=100 lv=200, got fv=%v lv=%v", resWin.Rows[0]["fv"], resWin.Rows[0]["lv"])
	}
	if fmt.Sprintf("%v", resWin.Rows[1]["fv"]) != "100" || fmt.Sprintf("%v", resWin.Rows[1]["lv"]) != "200" {
		t.Errorf("row 2 expected fv=100 lv=200, got fv=%v lv=%v", resWin.Rows[1]["fv"], resWin.Rows[1]["lv"])
	}
	// IT first is 300, last is 400
	if fmt.Sprintf("%v", resWin.Rows[2]["fv"]) != "300" || fmt.Sprintf("%v", resWin.Rows[2]["lv"]) != "400" {
		t.Errorf("row 3 expected fv=300 lv=400, got fv=%v lv=%v", resWin.Rows[2]["fv"], resWin.Rows[2]["lv"])
	}
	if fmt.Sprintf("%v", resWin.Rows[3]["fv"]) != "300" || fmt.Sprintf("%v", resWin.Rows[3]["lv"]) != "400" {
		t.Errorf("row 4 expected fv=300 lv=400, got fv=%v lv=%v", resWin.Rows[3]["fv"], resWin.Rows[3]["lv"])
	}
}

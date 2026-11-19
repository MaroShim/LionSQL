package execution_test

import (
	"context"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestWindow_NTILE_PercentRank_CumeDist(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	ctx := context.Background()
	sessID := "window-test-sess"

	if _, err := coord.ExecuteSessionSQLContext(ctx, sessID, `
CREATE TABLE t_win_test (id INT PRIMARY KEY, val INT);
INSERT INTO t_win_test VALUES (1, 10), (2, 20), (3, 20), (4, 30);
`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	q := `SELECT id, val,
		NTILE(2) OVER (ORDER BY val) AS nt,
		PERCENT_RANK() OVER (ORDER BY val) AS pr,
		CUME_DIST() OVER (ORDER BY val) AS cd
	FROM t_win_test ORDER BY id;`

	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, q)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if len(res.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d: %+v", len(res.Rows), res.Rows)
	}

	for i, r := range res.Rows {
		t.Logf("Row %d: %+v", i, r)
		if r["nt"] == nil {
			t.Errorf("row %d: nt is nil", i)
		}
		if r["pr"] == nil {
			t.Errorf("row %d: pr is nil", i)
		}
		if r["cd"] == nil {
			t.Errorf("row %d: cd is nil", i)
		}
	}

	// Verify exact values
	// Row 0 (id=1, val=10): nt=1, pr=0.0, cd=0.25
	if res.Rows[0]["nt"] != int64(1) {
		t.Errorf("row 0 nt: expected 1, got %v", res.Rows[0]["nt"])
	}
	if res.Rows[0]["pr"] != float64(0) {
		t.Errorf("row 0 pr: expected 0, got %v", res.Rows[0]["pr"])
	}
	if res.Rows[0]["cd"] != 0.25 {
		t.Errorf("row 0 cd: expected 0.25, got %v", res.Rows[0]["cd"])
	}

	// Row 1 (id=2, val=20): nt=1, pr=1/3, cd=0.75
	if res.Rows[1]["nt"] != int64(1) {
		t.Errorf("row 1 nt: expected 1, got %v", res.Rows[1]["nt"])
	}
	if diff := res.Rows[1]["pr"].(float64) - (1.0 / 3.0); diff > 1e-9 || diff < -1e-9 {
		t.Errorf("row 1 pr: expected ~0.3333, got %v", res.Rows[1]["pr"])
	}
	if res.Rows[1]["cd"] != 0.75 {
		t.Errorf("row 1 cd: expected 0.75, got %v", res.Rows[1]["cd"])
	}

	// Row 2 (id=3, val=20): nt=2, pr=1/3, cd=0.75
	if res.Rows[2]["nt"] != int64(2) {
		t.Errorf("row 2 nt: expected 2, got %v", res.Rows[2]["nt"])
	}
	if diff := res.Rows[2]["pr"].(float64) - (1.0 / 3.0); diff > 1e-9 || diff < -1e-9 {
		t.Errorf("row 2 pr: expected ~0.3333, got %v", res.Rows[2]["pr"])
	}
	if res.Rows[2]["cd"] != 0.75 {
		t.Errorf("row 2 cd: expected 0.75, got %v", res.Rows[2]["cd"])
	}

	// Row 3 (id=4, val=30): nt=2, pr=1.0, cd=1.0
	if res.Rows[3]["nt"] != int64(2) {
		t.Errorf("row 3 nt: expected 2, got %v", res.Rows[3]["nt"])
	}
	if res.Rows[3]["pr"] != float64(1) {
		t.Errorf("row 3 pr: expected 1, got %v", res.Rows[3]["pr"])
	}
	if res.Rows[3]["cd"] != float64(1) {
		t.Errorf("row 3 cd: expected 1, got %v", res.Rows[3]["cd"])
	}
}

package engine

import (
	"fmt"
	"strconv"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestWindow_SlidingAvg_RunningAndFrame(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE test_sales (id INT PRIMARY KEY, amount INT);"); err != nil {
		t.Fatalf("create test_sales failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO test_sales VALUES (1, 10), (2, 20), (3, 30), (4, 40);"); err != nil {
		t.Fatalf("insert test_sales failed: %v", err)
	}

	// 1. Shorthand running average: AVG(amount) OVER (ORDER BY id ASC)
	// Expected cumulative averages:
	// Row 1: 10 / 1 = 10.0
	// Row 2: (10 + 20) / 2 = 15.0
	// Row 3: (10 + 20 + 30) / 3 = 20.0
	// Row 4: (10 + 20 + 30 + 40) / 4 = 25.0
	q1 := "SELECT id, amount, AVG(amount) OVER (ORDER BY id ASC) AS running_avg FROM test_sales ORDER BY id ASC;"
	res1, err := coord.ExecuteSQL(q1)
	if err != nil || !res1.Success {
		t.Fatalf("running avg query failed: %v", err)
	}
	if len(res1.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(res1.Rows))
	}
	expectedRunningAvgs := []float64{10.0, 15.0, 20.0, 25.0}
	for i, exp := range expectedRunningAvgs {
		got, err := strconv.ParseFloat(fmt.Sprintf("%v", res1.Rows[i]["running_avg"]), 64)
		if err != nil {
			t.Fatalf("row %d: failed to parse running_avg %v: %v", i, res1.Rows[i]["running_avg"], err)
		}
		if got != exp {
			t.Errorf("row %d: expected running_avg %f, got %f (raw row: %+v)", i, exp, got, res1.Rows[i])
		}
	}

	// 2. Sliding window frame: ROWS BETWEEN 1 PRECEDING AND CURRENT ROW
	// Expected moving averages:
	// Row 1: 10 / 1 = 10.0
	// Row 2: (10 + 20) / 2 = 15.0
	// Row 3: (20 + 30) / 2 = 25.0
	// Row 4: (30 + 40) / 2 = 35.0
	q2 := "SELECT id, amount, AVG(amount) OVER (ORDER BY id ASC ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) AS moving_avg FROM test_sales ORDER BY id ASC;"
	res2, err := coord.ExecuteSQL(q2)
	if err != nil || !res2.Success {
		t.Fatalf("moving avg query failed: %v", err)
	}
	if len(res2.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(res2.Rows))
	}
	expectedMovingAvgs := []float64{10.0, 15.0, 25.0, 35.0}
	for i, exp := range expectedMovingAvgs {
		got, err := strconv.ParseFloat(fmt.Sprintf("%v", res2.Rows[i]["moving_avg"]), 64)
		if err != nil {
			t.Fatalf("row %d: failed to parse moving_avg %v: %v", i, res2.Rows[i]["moving_avg"], err)
		}
		if got != exp {
			t.Errorf("row %d: expected moving_avg %f, got %f (raw row: %+v)", i, exp, got, res2.Rows[i])
		}
	}
}

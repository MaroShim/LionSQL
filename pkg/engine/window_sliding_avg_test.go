package engine

import (
	"fmt"
	"strconv"
	"strings"
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

func TestWindow_CastInAggregate(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE test_cast_win (id INT PRIMARY KEY, amount INT);"); err != nil {
		t.Fatalf("create test_cast_win failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO test_cast_win VALUES (1, 10), (2, 20), (3, 30), (4, 40);"); err != nil {
		t.Fatalf("insert test_cast_win failed: %v", err)
	}

	// 1. SUM(CAST(id AS INT)) OVER ()
	q1 := "SELECT id, SUM(CAST(id AS INT)) OVER () AS total FROM test_cast_win ORDER BY id ASC;"
	res1, err := coord.ExecuteSQL(q1)
	if err != nil || !res1.Success {
		t.Fatalf("SUM(CAST(id AS INT)) OVER () failed: %v", err)
	}
	if len(res1.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(res1.Rows))
	}
	for i, r := range res1.Rows {
		got, err := strconv.ParseFloat(fmt.Sprintf("%v", r["total"]), 64)
		if err != nil || got != 10.0 {
			t.Errorf("row %d: expected total 10.0, got %v (err: %v)", i, r["total"], err)
		}
	}

	// 2. AVG(CAST(amount AS FLOAT)) OVER (ORDER BY id ASC)
	q2 := "SELECT id, AVG(CAST(amount AS FLOAT)) OVER (ORDER BY id ASC) AS running_avg FROM test_cast_win ORDER BY id ASC;"
	res2, err := coord.ExecuteSQL(q2)
	if err != nil || !res2.Success {
		t.Fatalf("AVG(CAST(amount AS FLOAT)) OVER (...) failed: %v", err)
	}
	expectedAvgs := []float64{10.0, 15.0, 20.0, 25.0}
	for i, exp := range expectedAvgs {
		got, err := strconv.ParseFloat(fmt.Sprintf("%v", res2.Rows[i]["running_avg"]), 64)
		if err != nil || got != exp {
			t.Errorf("row %d: expected running_avg %f, got %v", i, exp, res2.Rows[i]["running_avg"])
		}
	}

	// 3. Verify genuine invalid column still fails with clear diagnostic error
	q3 := "SELECT id, SUM(CAST(nonexistent_col AS INT)) OVER () FROM test_cast_win;"
	_, err3 := coord.ExecuteSQL(q3)
	if err3 == nil {
		t.Fatalf("expected error for nonexistent column in window aggregate, got nil")
	}
	if !strings.Contains(err3.Error(), "nonexistent_col") {
		t.Errorf("expected error message to mention nonexistent_col, got: %v", err3)
	}
}

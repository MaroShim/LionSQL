package engine_test

import (
	"fmt"
	"testing"
)

func TestPERF_MinMaxIndexSeek_Integration(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_min_max"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	// Create table and secondary index
	if _, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE perf_minmax (id INT PRIMARY KEY, score INT, age INT);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSessionSQL(sess, "CREATE INDEX ix_score ON perf_minmax (score);"); err != nil {
		t.Fatalf("CREATE INDEX failed: %v", err)
	}

	// 1. Empty table test: should return 1 row with NULLs
	resEmpty, err := coord.ExecuteSessionSQL(sess, "SELECT MIN(score) AS min_s, MAX(score) AS max_s FROM perf_minmax;")
	if err != nil {
		t.Fatalf("SELECT on empty table failed: %v", err)
	}
	if len(resEmpty.Rows) != 1 {
		t.Fatalf("expected 1 row for empty table aggregate, got %d", len(resEmpty.Rows))
	}
	if resEmpty.Rows[0]["min_s"] != nil || resEmpty.Rows[0]["max_s"] != nil {
		t.Fatalf("expected NULLs on empty table, got %+v", resEmpty.Rows[0])
	}

	// 2. Insert 5,000 records
	for i := 1; i <= 5000; i++ {
		score := (i*31 + 17) % 100000
		q := fmt.Sprintf("INSERT INTO perf_minmax (id, score, age) VALUES (%d, %d, %d);", i, score, i%80)
		if _, err := coord.ExecuteSessionSQL(sess, q); err != nil {
			t.Fatalf("INSERT row %d failed: %v", i, err)
		}
	}

	// 3. Test MIN(score) and MAX(score)
	res, err := coord.ExecuteSessionSQL(sess, "SELECT MIN(score) AS min_s, MAX(score) AS max_s FROM perf_minmax;")
	if err != nil {
		t.Fatalf("SELECT MIN/MAX failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}

	minVal := res.Rows[0]["min_s"].(int64)
	maxVal := res.Rows[0]["max_s"].(int64)

	// Calculate expected by full scan verification
	resFull, err := coord.ExecuteSessionSQL(sess, "SELECT score FROM perf_minmax;")
	if err != nil {
		t.Fatalf("Full scan failed: %v", err)
	}
	var expMin int64 = 99999999
	var expMax int64 = -1
	for _, r := range resFull.Rows {
		s := r["score"].(int64)
		if s < expMin {
			expMin = s
		}
		if s > expMax {
			expMax = s
		}
	}

	if minVal != expMin {
		t.Fatalf("MIN mismatch: got %d, expected %d", minVal, expMin)
	}
	if maxVal != expMax {
		t.Fatalf("MAX mismatch: got %d, expected %d", maxVal, expMax)
	}

	// 4. Test column without index (age) falls back cleanly to normal aggregate
	resNoIdx, err := coord.ExecuteSessionSQL(sess, "SELECT MIN(age) AS min_a, MAX(age) AS max_a FROM perf_minmax;")
	if err != nil {
		t.Fatalf("SELECT MIN/MAX on non-indexed column failed: %v", err)
	}
	if len(resNoIdx.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(resNoIdx.Rows))
	}
	if resNoIdx.Rows[0]["min_a"].(int64) != 0 {
		t.Fatalf("expected min_a = 0, got %v", resNoIdx.Rows[0]["min_a"])
	}

	// 5. Test Primary Key MIN seek
	resPK, err := coord.ExecuteSessionSQL(sess, "SELECT MIN(id) AS min_id FROM perf_minmax;")
	if err != nil {
		t.Fatalf("SELECT MIN(id) on PK failed: %v", err)
	}
	if len(resPK.Rows) != 1 || resPK.Rows[0]["min_id"].(int64) != 1 {
		t.Fatalf("expected min_id = 1, got %v", resPK.Rows[0]["min_id"])
	}
}

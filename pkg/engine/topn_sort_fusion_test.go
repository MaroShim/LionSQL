package engine_test

import (
	"fmt"
	"testing"
)

func TestPERF_TopNSortFusion_Integration(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_topn"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	// Create table and insert 500 records
	if _, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE perf_topn (id INT PRIMARY KEY, score INT, name VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	for i := 1; i <= 500; i++ {
		score := (i * 37) % 1000
		q := fmt.Sprintf("INSERT INTO perf_topn (id, score, name) VALUES (%d, %d, 'user_%d');", i, score, i)
		if _, err := coord.ExecuteSessionSQL(sess, q); err != nil {
			t.Fatalf("INSERT row %d failed: %v", i, err)
		}
	}

	// 1. Test standard ORDER BY ... LIMIT 5
	res, err := coord.ExecuteSessionSQL(sess, "SELECT id, score FROM perf_topn ORDER BY score ASC, id ASC LIMIT 5;")
	if err != nil {
		t.Fatalf("SELECT Top-5 failed: %v", err)
	}
	if len(res.Rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(res.Rows))
	}

	// Verify ascending order
	var prevScore int64 = -1
	for idx, row := range res.Rows {
		score := row["score"].(int64)
		if score < prevScore {
			t.Fatalf("row %d: score %d is less than prevScore %d", idx, score, prevScore)
		}
		prevScore = score
	}

	// 2. Test ORDER BY ... OFFSET 10 LIMIT 5
	resOffset, err := coord.ExecuteSessionSQL(sess, "SELECT id, score FROM perf_topn ORDER BY score DESC, id ASC OFFSET 10 LIMIT 5;")
	if err != nil {
		t.Fatalf("SELECT Top-N with OFFSET failed: %v", err)
	}
	if len(resOffset.Rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(resOffset.Rows))
	}

	var prevDescScore int64 = 999999
	for idx, row := range resOffset.Rows {
		score := row["score"].(int64)
		if score > prevDescScore {
			t.Fatalf("row %d: score %d is greater than prevDescScore %d", idx, score, prevDescScore)
		}
		prevDescScore = score
	}
}

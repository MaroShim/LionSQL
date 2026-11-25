package engine_test

import (
	"fmt"
	"testing"
)

func TestPERF_ScanFilterFusion_Integration(t *testing.T) {
	coord, _, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sess := "test_sess_scan_filter"
	_, _ = coord.GetOrCreateSession(sess)
	defer coord.CloseSession(sess)

	if _, err := coord.ExecuteSessionSQL(sess, "CREATE TABLE sensor_data (id INT PRIMARY KEY, temp INT, city VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	for i := 1; i <= 300; i++ {
		temp := (i * 7) % 100
		q := fmt.Sprintf("INSERT INTO sensor_data (id, temp, city) VALUES (%d, %d, 'city_%d');", i, temp, i%5)
		if _, err := coord.ExecuteSessionSQL(sess, q); err != nil {
			t.Fatalf("INSERT row %d failed: %v", i, err)
		}
	}

	// Filter query where scan-filter fusion discards non-matching rows
	res, err := coord.ExecuteSessionSQL(sess, "SELECT id, temp FROM sensor_data WHERE temp > 90;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}

	for i, row := range res.Rows {
		temp := row["temp"].(int64)
		if temp <= 90 {
			t.Fatalf("row %d has temp %d, expected > 90", i, temp)
		}
	}
}

package engine

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestTypeGuessingRemediation_Regression(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	// 1. Setup table with string codes
	_, err := coord.ExecuteSQL(`
CREATE TABLE code_test (
    code VARCHAR PRIMARY KEY,
    val INT
);
INSERT INTO code_test VALUES ('012', 1);
INSERT INTO code_test VALUES ('2', 2);
INSERT INTO code_test VALUES ('10', 3);
`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// 2. Test MIN() and MAX() lexicographical accuracy (NOT numeric coercion!)
	// Lexicographically: '012' < '10' < '2'
	// MIN must be '012', MAX must be '2'
	minMaxRes, err := coord.ExecuteSQL(`SELECT MIN(code) AS min_c, MAX(code) AS max_c FROM code_test;`)
	if err != nil {
		t.Fatalf("MIN/MAX query failed: %v", err)
	}
	if len(minMaxRes.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(minMaxRes.Rows))
	}
	minCode := fmt.Sprintf("%v", minMaxRes.Rows[0]["min_c"])
	maxCode := fmt.Sprintf("%v", minMaxRes.Rows[0]["max_c"])
	if minCode != "012" {
		t.Errorf("expected MIN(code) = '012', got %q (possible float coercion bug)", minCode)
	}
	if maxCode != "2" {
		t.Errorf("expected MAX(code) = '2', got %q (possible float coercion bug)", maxCode)
	}

	// 3. Test ORDER BY ASC on string codes
	orderAscRes, err := coord.ExecuteSQL(`SELECT code FROM code_test ORDER BY code ASC;`)
	if err != nil {
		t.Fatalf("ORDER BY ASC query failed: %v", err)
	}
	if len(orderAscRes.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(orderAscRes.Rows))
	}
	expectedAsc := []string{"012", "10", "2"}
	for i, exp := range expectedAsc {
		got := fmt.Sprintf("%v", orderAscRes.Rows[i]["code"])
		if got != exp {
			t.Errorf("row %d: expected code %q, got %q", i, exp, got)
		}
	}

	// 4. Test ORDER BY DESC on string codes
	orderDescRes, err := coord.ExecuteSQL(`SELECT code FROM code_test ORDER BY code DESC;`)
	if err != nil {
		t.Fatalf("ORDER BY DESC query failed: %v", err)
	}
	expectedDesc := []string{"2", "10", "012"}
	for i, exp := range expectedDesc {
		got := fmt.Sprintf("%v", orderDescRes.Rows[i]["code"])
		if got != exp {
			t.Errorf("row %d: expected code %q, got %q", i, exp, got)
		}
	}

	// 5. Test WHERE clause string comparison (NOT float coercion!)
	whereRes, err := coord.ExecuteSQL(`SELECT code FROM code_test WHERE code > '012' ORDER BY code ASC;`)
	if err != nil {
		t.Fatalf("WHERE query failed: %v", err)
	}
	if len(whereRes.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(whereRes.Rows), whereRes.Rows)
	}

	// 6. Test sp_executesql parameter preservation with string복합키 ('1_1_1001' and '00123')
	_, err = coord.ExecuteSQL(`CREATE TABLE sp_param_test (p_key VARCHAR PRIMARY KEY, note VARCHAR);`)
	if err != nil {
		t.Fatalf("create sp_param_test failed: %v", err)
	}

	spSQL := `EXEC sp_executesql N'INSERT INTO sp_param_test VALUES (@k, @n);', N'@k VARCHAR, @n VARCHAR', @k = '1_1_1001', @n = '00123';`
	if _, err := coord.ExecuteSessionSQL("sess-prep", spSQL); err != nil {
		t.Fatalf("sp_executesql failed: %v", err)
	}

	checkRes, err := coord.ExecuteSessionSQL("sess-prep", `SELECT p_key, note FROM sp_param_test WHERE p_key = '1_1_1001';`)
	if err != nil || len(checkRes.Rows) != 1 {
		t.Fatalf("SELECT from sp_param_test failed: %v (rows: %d)", err, len(checkRes.Rows))
	}
	gotKey := fmt.Sprintf("%v", checkRes.Rows[0]["p_key"])
	gotNote := fmt.Sprintf("%v", checkRes.Rows[0]["note"])
	if gotKey != "1_1_1001" {
		t.Errorf("expected p_key = '1_1_1001', got %q", gotKey)
	}
	if gotNote != "00123" {
		t.Errorf("expected note = '00123', got %q", gotNote)
	}
}

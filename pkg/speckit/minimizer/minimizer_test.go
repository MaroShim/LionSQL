package minimizer

import (
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/speckit/grammar"
	"lionsql/pkg/speckit/oracle"
	"lionsql/pkg/storage/memengine"
)

func TestMinimizer_GenerateTDDTestFile(t *testing.T) {
	d := &oracle.Discrepancy{
		Type:            "NoREC",
		Table:           grammar.TableSchema{Name: "test_tbl"},
		TableDDL:        "CREATE TABLE test_tbl (id INT, val TEXT);",
		InsertDMLs:      []string{"INSERT INTO test_tbl VALUES (1, 'a');", "INSERT INTO test_tbl VALUES (2, 'b');"},
		Query:           "SELECT COUNT(*) FROM test_tbl WHERE id > 0;",
		EquivalentQuery: "SELECT COALESCE(SUM(CASE WHEN id > 0 THEN 1 ELSE 0 END), 0) FROM test_tbl;",
		Details:         "WHERE count != SUM(CASE WHEN ...)",
	}

	content := GenerateTDDTestFile("spec_defects", "TestDefect_1", "DEFECT-123", d)
	if !strings.Contains(content, "package spec_defects") {
		t.Errorf("expected package name in generated code")
	}
	if !strings.Contains(content, "TestDefect_1") {
		t.Errorf("expected test name in generated code")
	}

	// Test TLP Generator
	tlpD := &oracle.Discrepancy{
		Type:       oracle.DiscrepancyTLP,
		Table:      grammar.TableSchema{Name: "tlp_tbl"},
		TableDDL:   "CREATE TABLE tlp_tbl (id INT);",
		InsertDMLs: []string{"INSERT INTO tlp_tbl VALUES (1);"},
		Query:      "SELECT COUNT(*) FROM tlp_tbl;",
		Partitions: []string{
			"SELECT COUNT(*) FROM tlp_tbl WHERE id > 0;",
			"SELECT COUNT(*) FROM tlp_tbl WHERE NOT (id > 0);",
			"SELECT COUNT(*) FROM tlp_tbl WHERE (id > 0) IS NULL;",
		},
		Details: "TLP 3VL Violation",
	}
	tlpContent := GenerateTDDTestFile("spec_defects", "TestDefect_TLP_1", "DEFECT-TLP-1", tlpD)
	if !strings.Contains(tlpContent, "sumPartitions") {
		t.Errorf("expected sumPartitions in TLP generated code")
	}

	// Test PQS Generator
	pqsD := &oracle.Discrepancy{
		Type:       oracle.DiscrepancyPQS,
		Table:      grammar.TableSchema{Name: "pqs_tbl"},
		TableDDL:   "CREATE TABLE pqs_tbl (id INT);",
		InsertDMLs: []string{"INSERT INTO pqs_tbl VALUES (42);"},
		Query:      "SELECT id FROM pqs_tbl WHERE id = 42;",
		PivotID:    42,
		Details:    "PQS Missing Pivot",
	}
	pqsContent := GenerateTDDTestFile("spec_defects", "TestDefect_PQS_1", "DEFECT-PQS-1", pqsD)
	if !strings.Contains(pqsContent, "targetPivot := int64(42)") {
		t.Errorf("expected targetPivot in PQS generated code")
	}
	if !strings.Contains(content, "DEFECT-123") {
		t.Errorf("expected defect ID in generated code")
	}
	if !strings.Contains(content, "CREATE TABLE test_tbl") {
		t.Errorf("expected DDL in generated code")
	}
}

func TestMinimizer_Minimize(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})

	sessID := "test_min_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	d := &oracle.Discrepancy{
		Type:     "NoREC",
		Table:    grammar.TableSchema{Name: "min_tbl"},
		TableDDL: "CREATE TABLE min_tbl (id INT);",
		InsertDMLs: []string{
			"INSERT INTO min_tbl VALUES (1);",
			"INSERT INTO min_tbl VALUES (2);",
		},
		Query:           "SELECT COUNT(*) FROM min_tbl WHERE id = 999;",
		EquivalentQuery: "SELECT COUNT(*) FROM min_tbl WHERE id = 999;",
	}

	// Since queries match, minimize should return original or safe copy without panic
	res := Minimize(coord, sessID, d)
	if res == nil {
		t.Fatalf("expected non-nil result from Minimize")
	}
}

package minimizer

import (
	"fmt"
	"lionsql/pkg/engine"
	"lionsql/pkg/speckit/oracle"
)

// Minimize attempts to minimize a Discrepancy by pruning unnecessary rows and clauses.
func Minimize(coord *engine.Coordinator, sessID string, d *oracle.Discrepancy) *oracle.Discrepancy {
	if d == nil {
		return nil
	}

	minD := *d
	tableName := d.Table.Name

	// 1. Delta Debugging: Row Pruning (Try removing insert rows one by one)
	if len(minD.InsertDMLs) > 1 {
		prunedDMLs := make([]string, len(minD.InsertDMLs))
		copy(prunedDMLs, minD.InsertDMLs)

		for i := len(prunedDMLs) - 1; i >= 0; i-- {
			candidateDMLs := make([]string, 0, len(prunedDMLs)-1)
			candidateDMLs = append(candidateDMLs, prunedDMLs[:i]...)
			candidateDMLs = append(candidateDMLs, prunedDMLs[i+1:]...)

			if testReproduces(coord, sessID, tableName, minD.TableDDL, candidateDMLs, minD.Query, minD.EquivalentQuery) {
				prunedDMLs = candidateDMLs
			}
		}
		minD.InsertDMLs = prunedDMLs
	}

	return &minD
}

// testReproduces checks if dropping to candidateDMLs still reproduces the NoREC discrepancy.
func testReproduces(coord *engine.Coordinator, sessID, tableName, tableDDL string, dmls []string, qOpt, qUnopt string) bool {
	// Recreate table
	_, _ = coord.ExecuteSessionSQL(sessID, fmt.Sprintf("DROP TABLE IF EXISTS %s;", tableName))
	if _, err := coord.ExecuteSessionSQL(sessID, tableDDL); err != nil {
		return false
	}
	for _, dml := range dmls {
		if _, err := coord.ExecuteSessionSQL(sessID, dml); err != nil {
			return false
		}
	}

	resOpt, errOpt := coord.ExecuteSessionSQL(sessID, qOpt)
	if errOpt != nil {
		return false
	}
	resUnopt, errUnopt := coord.ExecuteSessionSQL(sessID, qUnopt)
	if errUnopt != nil {
		return false
	}

	countOpt, err1 := oracle.ExtractCount(resOpt)
	countUnopt, err2 := oracle.ExtractCount(resUnopt)
	if err1 != nil || err2 != nil {
		return false
	}

	return countOpt != countUnopt
}

// GenerateTDDTestFile generates a Go TDD Red phase test file content from a Discrepancy.
func GenerateTDDTestFile(pkgName string, testName string, defectID string, d *oracle.Discrepancy) string {
	return fmt.Sprintf(`package %s

import (
	"fmt"
	"testing"
	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

// %s verifies the spec discrepancy:
// Discrepancy Type: %s
// Details: %s
func %s(t *testing.T) {
	if testing.Short() {
		t.Skip("TDD RED: %s - pending resolution sprint")
	}

	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})

	sessID := "%s_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	// Setup Schema & Data
	_, err := coord.ExecuteSessionSQL(sessID, %q)
	if err != nil {
		t.Fatalf("setup DDL failed: %%v", err)
	}

	inserts := []string{
%s
	}
	for _, dml := range inserts {
		if _, err := coord.ExecuteSessionSQL(sessID, dml); err != nil {
			t.Fatalf("insert failed: %%v", err)
		}
	}

	// 1. Run Optimized Query
	resOpt, err := coord.ExecuteSessionSQL(sessID, %q)
	if err != nil {
		t.Fatalf("optimized query failed: %%v", err)
	}

	// 2. Run Unoptimized Query
	resUnopt, err := coord.ExecuteSessionSQL(sessID, %q)
	if err != nil {
		t.Fatalf("unoptimized query failed: %%v", err)
	}

	// 3. Assert Equivalence
	if len(resOpt.Rows) == 0 || len(resUnopt.Rows) == 0 {
		t.Fatalf("empty rows returned")
	}
	rowOpt := resOpt.Rows[0]
	rowUnopt := resUnopt.Rows[0]
	if fmt.Sprintf("%%v", rowOpt) != fmt.Sprintf("%%v", rowUnopt) {
		t.Fatalf("TDD RED Discrepancy: Optimized (%%v) != Unoptimized (%%v)", rowOpt, rowUnopt)
	}
}
`, pkgName, testName, d.Type, d.Details, testName, defectID, testName, d.TableDDL, formatInserts(d.InsertDMLs), d.Query, d.EquivalentQuery)
}

func formatInserts(dmls []string) string {
	var s string
	for _, d := range dmls {
		s += fmt.Sprintf("\t\t%q,\n", d)
	}
	return s
}

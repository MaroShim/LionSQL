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
	if d.Type == oracle.DiscrepancyNoREC && len(minD.InsertDMLs) > 1 {
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
	if d.Type == oracle.DiscrepancyTLP && len(d.Partitions) >= 3 {
		return fmt.Sprintf(`package %s

import (
	"testing"
	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/speckit/oracle"
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

	// 1. Run Total Count Query
	resTotal, err := coord.ExecuteSessionSQL(sessID, %q)
	if err != nil {
		t.Fatalf("total count query failed: %%v", err)
	}
	countTotal, err := oracle.ExtractCount(resTotal)
	if err != nil {
		t.Fatalf("failed to extract total count: %%v", err)
	}

	// 2. Run Partition Queries (True, False, Null)
	partQueries := []string{
		%q,
		%q,
		%q,
	}
	var sumPartitions int64
	for i, q := range partQueries {
		resPart, err := coord.ExecuteSessionSQL(sessID, q)
		if err != nil {
			t.Fatalf("partition query %%d failed: %%v", i, err)
		}
		c, err := oracle.ExtractCount(resPart)
		if err != nil {
			t.Fatalf("failed to extract partition %%d count: %%v", i, err)
		}
		sumPartitions += c
	}

	// 3. Assert TLP 3VL Invariant: Total == True + False + Null
	if countTotal != sumPartitions {
		t.Fatalf("TDD RED Discrepancy (TLP): Total (%%d) != Sum of Partitions (%%d)", countTotal, sumPartitions)
	}
}
`, pkgName, testName, d.Type, d.Details, testName, defectID, testName, d.TableDDL, formatInserts(d.InsertDMLs), d.Query, d.Partitions[0], d.Partitions[1], d.Partitions[2])
	}

	if d.Type == oracle.DiscrepancyPQS {
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

	// 1. Run PQS Query
	res, err := coord.ExecuteSessionSQL(sessID, %q)
	if err != nil {
		t.Fatalf("PQS query failed: %%v", err)
	}

	// 2. Assert Pivot ID Presence
	targetPivot := int64(%d)
	found := false
	for _, row := range res.Rows {
		for _, v := range row {
			var idVal int64
			switch val := v.(type) {
			case int64:
				idVal = val
			case int:
				idVal = int64(val)
			case int32:
				idVal = int64(val)
			default:
				_, _ = fmt.Sscanf(fmt.Sprintf("%%v", v), "%%d", &idVal)
			}
			if idVal == targetPivot {
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		t.Fatalf("TDD RED Discrepancy (PQS): Pivot ID %%d not found in query results (%%d rows returned)", targetPivot, len(res.Rows))
	}
}
`, pkgName, testName, d.Type, d.Details, testName, defectID, testName, d.TableDDL, formatInserts(d.InsertDMLs), d.Query, d.PivotID)
	}

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

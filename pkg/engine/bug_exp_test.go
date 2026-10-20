package engine

import (
	"strings"
	"testing"

	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
)

func TestRegression_BUG_EXP_1_ExplainSubqueryCaseInsensitiveCols(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	setupSQL := []string{
		"CREATE TABLE departments (DeptID INT PRIMARY KEY, DepartmentName VARCHAR(50), ExtraCode VARCHAR(20));",
		"INSERT INTO departments (DeptID, DepartmentName, ExtraCode) VALUES (10, 'Engineering', 'ENG-01');",
		"INSERT INTO departments (DeptID, DepartmentName, ExtraCode) VALUES (20, 'Sales', 'SLS-02');",
	}

	for _, sql := range setupSQL {
		if _, err := coord.ExecuteSQL(sql); err != nil {
			t.Fatalf("setup failed on %s: %v", sql, err)
		}
	}

	// Subquery with lowercase column alias/name matching uppercase schema DeptID
	// and multiple columns in departments table to catch random map iteration.
	whereClause := "DeptID IN (SELECT deptid FROM departments WHERE DepartmentName = 'Engineering')"

	for i := 0; i < 20; i++ {
		resolved, err := coord.resolveSubqueries(whereClause)
		if err != nil {
			t.Fatalf("run %d resolveSubqueries failed: %v", i, err)
		}

		// Must deterministically resolve to DeptID IN (10) and NEVER to 'Engineering' or 'ENG-01'
		if !strings.Contains(resolved, "DeptID IN (10)") {
			t.Fatalf("run %d: expected 'DeptID IN (10)', got: %q", i, resolved)
		}
	}

	// Also test NOT IN
	whereNotClause := "DeptID NOT IN (SELECT deptid FROM departments WHERE DepartmentName = 'Engineering')"
	for i := 0; i < 20; i++ {
		resolved, err := coord.resolveSubqueries(whereNotClause)
		if err != nil {
			t.Fatalf("run %d resolveSubqueries NOT IN failed: %v", i, err)
		}
		if !strings.Contains(resolved, "DeptID NOT IN ('10')") {
			t.Fatalf("run %d: expected 'DeptID NOT IN ('10')', got: %q", i, resolved)
		}
	}

	// Also test scalar subquery comparison: > (SELECT deptid FROM ...)
	whereScalarClause := "DeptID = (SELECT deptid FROM departments WHERE DepartmentName = 'Engineering')"
	for i := 0; i < 20; i++ {
		resolved, err := coord.resolveSubqueries(whereScalarClause)
		if err != nil {
			t.Fatalf("run %d resolveSubqueries scalar failed: %v", i, err)
		}
		if !strings.Contains(resolved, "DeptID = 10") {
			t.Fatalf("run %d: expected 'DeptID = 10', got: %q", i, resolved)
		}
	}
}

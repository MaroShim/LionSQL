package slt

import (
	"fmt"
	"sort"
	"strings"

	"lionsql/pkg/sqlutil"
)

// DualityMismatchError represents a differential divergence between two execution tracks.
type DualityMismatchError struct {
	SQL        string
	LabelA     string
	LabelB     string
	Divergence string
}

func (e *DualityMismatchError) Error() string {
	return fmt.Sprintf("[DUALITY-MISMATCH] %s vs %s:\n  Divergence: %s\n  SQL:\n%s", e.LabelA, e.LabelB, e.Divergence, e.SQL)
}

// DualityExecutor wraps two Executors (Track A and Track B) and executes every query
// simultaneously across both, strictly asserting 1:1 parity on errors, columns, and rows.
type DualityExecutor struct {
	ExecA  Executor
	ExecB  Executor
	LabelA string
	LabelB string
}

// NewDualityExecutor creates a differential cross-verification executor wrapping two distinct tracks.
func NewDualityExecutor(execA Executor, labelA string, execB Executor, labelB string) *DualityExecutor {
	return &DualityExecutor{
		ExecA:  execA,
		LabelA: labelA,
		ExecB:  execB,
		LabelB: labelB,
	}
}

// Execute runs the given SQL across both tracks and validates exact 1:1 differential parity.
func (d *DualityExecutor) Execute(sqlStr string) ([]string, []map[string]any, error) {
	colsA, rowsA, errA := d.ExecA.Execute(sqlStr)
	colsB, rowsB, errB := d.ExecB.Execute(sqlStr)

	// 1. Error parity
	if (errA == nil) != (errB == nil) {
		return nil, nil, &DualityMismatchError{
			SQL:        sqlStr,
			LabelA:     d.LabelA,
			LabelB:     d.LabelB,
			Divergence: fmt.Sprintf("Error presence mismatch:\n    %s: err=%v\n    %s: err=%v", d.LabelA, errA, d.LabelB, errB),
		}
	}
	if errA != nil {
		// Both failed as expected
		return nil, nil, errA
	}

	// 2. Column count parity
	if len(colsA) != len(colsB) {
		return nil, nil, &DualityMismatchError{
			SQL:        sqlStr,
			LabelA:     d.LabelA,
			LabelB:     d.LabelB,
			Divergence: fmt.Sprintf("Column count mismatch: %s has %d cols (%v), %s has %d cols (%v)", d.LabelA, len(colsA), colsA, d.LabelB, len(colsB), colsB),
		}
	}

	// 3. Row count parity
	if len(rowsA) != len(rowsB) {
		return nil, nil, &DualityMismatchError{
			SQL:        sqlStr,
			LabelA:     d.LabelA,
			LabelB:     d.LabelB,
			Divergence: fmt.Sprintf("Row count mismatch: %s returned %d rows, %s returned %d rows", d.LabelA, len(rowsA), d.LabelB, len(rowsB)),
		}
	}

	// 4. Value parity across all rows and cells
	// In relational algebra, queries without explicit ORDER BY represent unordered sets/bags.
	// Different query engines (e.g. Volcano CBO join reordering vs VDBE nested loops) may emit
	// rows in different physical arrival orders. If ORDER BY is absent, canonicalize row ordering
	// before cell-by-cell equivalence verification.
	cmpA := rowsA
	cmpB := rowsB
	if !sqlutil.ContainsKeyword(sqlStr, "ORDER BY", true) {
		cmpA = canonicalSortRows(rowsA, colsA)
		cmpB = canonicalSortRows(rowsB, colsA)
	}

	for rIdx := range cmpA {
		rA := cmpA[rIdx]
		rB := cmpB[rIdx]
		for _, col := range colsA {
			vA := normalizeDualityVal(rA[col])
			vB := normalizeDualityVal(rB[col])
			if vA != vB {
				return nil, nil, &DualityMismatchError{
					SQL:        sqlStr,
					LabelA:     d.LabelA,
					LabelB:     d.LabelB,
					Divergence: fmt.Sprintf("Row %d Col %q Value mismatch:\n    %s: %q (%T)\n    %s: %q (%T)", rIdx, col, d.LabelA, vA, rA[col], d.LabelB, vB, rB[col]),
				}
			}
		}
	}

	return colsA, rowsA, nil
}

func canonicalSortRows(rows []map[string]any, cols []string) []map[string]any {
	sorted := make([]map[string]any, len(rows))
	copy(sorted, rows)
	sort.SliceStable(sorted, func(i, j int) bool {
		rI, rJ := sorted[i], sorted[j]
		for _, col := range cols {
			vI := normalizeDualityVal(rI[col])
			vJ := normalizeDualityVal(rJ[col])
			if vI != vJ {
				return vI < vJ
			}
		}
		return false
	})
	return sorted
}

func normalizeDualityVal(v any) string {
	if v == nil {
		return "NULL"
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

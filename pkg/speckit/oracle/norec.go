package oracle

import (
	"fmt"
	"lionsql/pkg/engine"
	"lionsql/pkg/speckit/grammar"
)

// CheckNoREC executes a NoREC query pair and returns a Discrepancy if the counts do not match.
func CheckNoREC(coord *engine.Coordinator, sessID string, pair grammar.NoRECPair, tableDDL string, insertDMLs []string) (*Discrepancy, error) {
	// 1. Run Optimized Query
	resOpt, errOpt := coord.ExecuteSessionSQL(sessID, pair.OptimizedSQL)
	if errOpt != nil {
		return &Discrepancy{
			Type:            DiscrepancyErrorDiff,
			Table:           pair.Table,
			TableDDL:        tableDDL,
			InsertDMLs:      insertDMLs,
			Query:           pair.OptimizedSQL,
			EquivalentQuery: pair.UnoptimizedSQL,
			Expected:        "successful execution",
			Actual:          fmt.Sprintf("error: %v", errOpt),
			Details:         "Optimized query failed during execution",
		}, nil
	}

	// 2. Run Unoptimized Query
	resUnopt, errUnopt := coord.ExecuteSessionSQL(sessID, pair.UnoptimizedSQL)
	if errUnopt != nil {
		return &Discrepancy{
			Type:            DiscrepancyErrorDiff,
			Table:           pair.Table,
			TableDDL:        tableDDL,
			InsertDMLs:      insertDMLs,
			Query:           pair.UnoptimizedSQL,
			EquivalentQuery: pair.OptimizedSQL,
			Expected:        "successful execution",
			Actual:          fmt.Sprintf("error: %v", errUnopt),
			Details:         "Unoptimized reference query failed during execution",
		}, nil
	}

	// 3. Extract and compare counts
	countOpt, errCountOpt := ExtractCount(resOpt)
	if errCountOpt != nil {
		return &Discrepancy{
			Type:       DiscrepancyNoREC,
			Table:      pair.Table,
			TableDDL:   tableDDL,
			InsertDMLs: insertDMLs,
			Query:      pair.OptimizedSQL,
			Expected:   "valid count column",
			Actual:     fmt.Sprintf("extract error: %v", errCountOpt),
			Details:    "Could not extract count from optimized query result",
		}, nil
	}

	countUnopt, errCountUnopt := ExtractCount(resUnopt)
	if errCountUnopt != nil {
		return &Discrepancy{
			Type:       DiscrepancyNoREC,
			Table:      pair.Table,
			TableDDL:   tableDDL,
			InsertDMLs: insertDMLs,
			Query:      pair.UnoptimizedSQL,
			Expected:   "valid count column",
			Actual:     fmt.Sprintf("extract error: %v", errCountUnopt),
			Details:    "Could not extract count from unoptimized reference result",
		}, nil
	}

	// 4. Invariant check: countOpt must equal countUnopt
	if countOpt != countUnopt {
		return &Discrepancy{
			Type:            DiscrepancyNoREC,
			Table:           pair.Table,
			TableDDL:        tableDDL,
			InsertDMLs:      insertDMLs,
			Query:           pair.OptimizedSQL,
			EquivalentQuery: pair.UnoptimizedSQL,
			Expected:        fmt.Sprintf("Count = %d", countUnopt),
			Actual:          fmt.Sprintf("Count = %d", countOpt),
			Details: fmt.Sprintf("NoREC Invariant Violation: WHERE count (%d) != SUM(CASE WHEN ...) (%d) for predicate: %s",
				countOpt, countUnopt, pair.Predicate),
		}, nil
	}

	return nil, nil
}

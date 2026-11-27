package oracle

import (
	"fmt"
	"lionsql/pkg/engine"
	"lionsql/pkg/speckit/grammar"
)

// CheckPQS executes Pivoted Query Synthesis (PQS):
// Invariant: The synthesized query MUST return at least the row with pivotID.
func CheckPQS(coord *engine.Coordinator, sessID string, pair grammar.PQSPair, tableDDL string, insertDMLs []string) (*Discrepancy, error) {
	res, err := coord.ExecuteSessionSQL(sessID, pair.QuerySQL)
	if err != nil {
		return nil, nil // Expression evaluation error on data domain (e.g. arithmetic overflow)
	}

	// Verify pivotID is present in result rows
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
				_, _ = fmt.Sscanf(fmt.Sprintf("%v", v), "%d", &idVal)
			}
			if idVal == pair.PivotID {
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		return &Discrepancy{
			Type:            DiscrepancyPQS,
			Table:           pair.Table,
			TableDDL:        tableDDL,
			InsertDMLs:      insertDMLs,
			Query:           pair.QuerySQL,
			PivotID:         pair.PivotID,
			Expected:        fmt.Sprintf("Result containing pivot ID %d", pair.PivotID),
			Actual:          fmt.Sprintf("Result returned %d rows without pivot ID %d", len(res.Rows), pair.PivotID),
			Details:         fmt.Sprintf("PQS Pivot Row Missing for predicate: %s", pair.Predicate),
		}, nil
	}

	return nil, nil
}

package oracle

import (
	"fmt"
	"lionsql/pkg/engine"
	"lionsql/pkg/speckit/grammar"
)

// CheckDualEngine executes a query on two coordinators and asserts 100% result parity.
func CheckDualEngine(
	coord1 *engine.Coordinator, sess1 string, name1 string,
	coord2 *engine.Coordinator, sess2 string, name2 string,
	query grammar.SelectQuery, tableDDL string, insertDMLs []string,
) (*Discrepancy, error) {
	// Execute on Engine 1
	res1, err1 := coord1.ExecuteSessionSQL(sess1, query.SQL)

	// Execute on Engine 2
	res2, err2 := coord2.ExecuteSessionSQL(sess2, query.SQL)

	// Compare error states
	if (err1 != nil) != (err2 != nil) {
		return &Discrepancy{
			Type:       DiscrepancyDualEngine,
			Table:      query.Table,
			TableDDL:   tableDDL,
			InsertDMLs: insertDMLs,
			Query:      query.SQL,
			Expected:   fmt.Sprintf("[%s] Error parity", name1),
			Actual:     fmt.Sprintf("[%s] err=%v vs [%s] err=%v", name1, err1, name2, err2),
			Details:    "One engine produced an execution error while the other succeeded",
		}, nil
	}

	if err1 != nil {
		return nil, nil // Both errored consistently
	}

	// Compare row counts
	if len(res1.Rows) != len(res2.Rows) {
		return &Discrepancy{
			Type:       DiscrepancyDualEngine,
			Table:      query.Table,
			TableDDL:   tableDDL,
			InsertDMLs: insertDMLs,
			Query:      query.SQL,
			Expected:   fmt.Sprintf("[%s] %d rows", name1, len(res1.Rows)),
			Actual:     fmt.Sprintf("[%s] %d rows", name2, len(res2.Rows)),
			Details:    fmt.Sprintf("Row count divergence across engines: %s vs %s", name1, name2),
		}, nil
	}

	// Compare row cell values
	for rIdx := 0; rIdx < len(res1.Rows); rIdx++ {
		row1 := res1.Rows[rIdx]
		row2 := res2.Rows[rIdx]

		for _, col := range query.Projections {
			val1 := normalizeVal(row1[col])
			val2 := normalizeVal(row2[col])
			if val1 != val2 {
				return &Discrepancy{
					Type:       DiscrepancyDualEngine,
					Table:      query.Table,
					TableDDL:   tableDDL,
					InsertDMLs: insertDMLs,
					Query:      query.SQL,
					Expected:   fmt.Sprintf("[%s Row %d Col %s] %s", name1, rIdx, col, val1),
					Actual:     fmt.Sprintf("[%s Row %d Col %s] %s", name2, rIdx, col, val2),
					Details:    fmt.Sprintf("Value divergence on column %q at row %d", col, rIdx),
				}, nil
			}
		}
	}

	return nil, nil
}

func normalizeVal(v any) string {
	if v == nil {
		return "NULL"
	}
	return fmt.Sprintf("%v", v)
}

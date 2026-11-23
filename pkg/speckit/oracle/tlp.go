package oracle

import (
	"fmt"
	"lionsql/pkg/engine"
	"lionsql/pkg/speckit/grammar"
)

// CheckTLP executes Ternary Logic Partitioning (TLP) query partitions:
// Invariant: COUNT(Original) == COUNT(WHERE phi) + COUNT(WHERE NOT (phi)) + COUNT(WHERE (phi) IS NULL)
func CheckTLP(coord *engine.Coordinator, sessID string, pair grammar.TLPPair, tableDDL string, insertDMLs []string) (*Discrepancy, error) {
	// 1. Run Original Query
	resOrig, errOrig := coord.ExecuteSessionSQL(sessID, pair.OriginalSQL)
	if errOrig != nil {
		return &Discrepancy{
			Type:            DiscrepancyErrorDiff,
			Table:           pair.Table,
			TableDDL:        tableDDL,
			InsertDMLs:      insertDMLs,
			Query:           pair.OriginalSQL,
			Expected:        "successful execution",
			Actual:          fmt.Sprintf("error: %v", errOrig),
			Details:         "TLP original query failed",
		}, nil
	}
	countOrig, errCountOrig := ExtractCount(resOrig)
	if errCountOrig != nil {
		return nil, fmt.Errorf("failed to extract count from original query: %w", errCountOrig)
	}

	// 2. Run True Query
	resTrue, errTrue := coord.ExecuteSessionSQL(sessID, pair.TrueSQL)
	if errTrue != nil {
		return &Discrepancy{
			Type:            DiscrepancyErrorDiff,
			Table:           pair.Table,
			TableDDL:        tableDDL,
			InsertDMLs:      insertDMLs,
			Query:           pair.TrueSQL,
			Expected:        "successful execution",
			Actual:          fmt.Sprintf("error: %v", errTrue),
			Details:         "TLP True query failed",
		}, nil
	}
	countTrue, errCountTrue := ExtractCount(resTrue)
	if errCountTrue != nil {
		return nil, fmt.Errorf("failed to extract count from True query: %w", errCountTrue)
	}

	// 3. Run False Query
	resFalse, errFalse := coord.ExecuteSessionSQL(sessID, pair.FalseSQL)
	if errFalse != nil {
		return &Discrepancy{
			Type:            DiscrepancyErrorDiff,
			Table:           pair.Table,
			TableDDL:        tableDDL,
			InsertDMLs:      insertDMLs,
			Query:           pair.FalseSQL,
			Expected:        "successful execution",
			Actual:          fmt.Sprintf("error: %v", errFalse),
			Details:         "TLP False query failed",
		}, nil
	}
	countFalse, errCountFalse := ExtractCount(resFalse)
	if errCountFalse != nil {
		return nil, fmt.Errorf("failed to extract count from False query: %w", errCountFalse)
	}

	// 4. Run Null Query
	resNull, errNull := coord.ExecuteSessionSQL(sessID, pair.NullSQL)
	if errNull != nil {
		return &Discrepancy{
			Type:            DiscrepancyErrorDiff,
			Table:           pair.Table,
			TableDDL:        tableDDL,
			InsertDMLs:      insertDMLs,
			Query:           pair.NullSQL,
			Expected:        "successful execution",
			Actual:          fmt.Sprintf("error: %v", errNull),
			Details:         "TLP Null query failed",
		}, nil
	}
	countNull, errCountNull := ExtractCount(resNull)
	if errCountNull != nil {
		return nil, fmt.Errorf("failed to extract count from Null query: %w", errCountNull)
	}

	// 5. Invariant check: countOrig == countTrue + countFalse + countNull
	sumPartition := countTrue + countFalse + countNull
	if countOrig != sumPartition {
		return &Discrepancy{
			Type:            DiscrepancyTLP,
			Table:           pair.Table,
			TableDDL:        tableDDL,
			InsertDMLs:      insertDMLs,
			Query:           pair.OriginalSQL,
			EquivalentQuery: fmt.Sprintf("%s + %s + %s", pair.TrueSQL, pair.FalseSQL, pair.NullSQL),
			Partitions:      []string{pair.TrueSQL, pair.FalseSQL, pair.NullSQL},
			Expected:        fmt.Sprintf("Total %d rows", countOrig),
			Actual:          fmt.Sprintf("Partitions sum %d (True: %d, False: %d, Null: %d)", sumPartition, countTrue, countFalse, countNull),
			Details:         fmt.Sprintf("TLP 3VL Partitioning violation for predicate %s", pair.Predicate),
		}, nil
	}

	return nil, nil
}

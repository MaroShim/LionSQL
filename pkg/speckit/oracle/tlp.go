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
		return nil, nil
	}
	countOrig, errCountOrig := ExtractCount(resOrig)
	if errCountOrig != nil {
		return nil, nil
	}

	// 2. Run True Query
	resTrue, errTrue := coord.ExecuteSessionSQL(sessID, pair.TrueSQL)
	if errTrue != nil {
		return nil, nil
	}
	countTrue, errCountTrue := ExtractCount(resTrue)
	if errCountTrue != nil {
		return nil, nil
	}

	// 3. Run False Query
	resFalse, errFalse := coord.ExecuteSessionSQL(sessID, pair.FalseSQL)
	if errFalse != nil {
		return nil, nil
	}
	countFalse, errCountFalse := ExtractCount(resFalse)
	if errCountFalse != nil {
		return nil, nil
	}

	// 4. Run Null Query
	resNull, errNull := coord.ExecuteSessionSQL(sessID, pair.NullSQL)
	if errNull != nil {
		return nil, nil
	}
	countNull, errCountNull := ExtractCount(resNull)
	if errCountNull != nil {
		return nil, nil
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

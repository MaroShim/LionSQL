package grammar

import (
	"fmt"
	"math/rand"
	"strings"
)

// NoRECPair contains two mathematically equivalent queries:
// 1. Optimized query: uses regular WHERE predicate which CBO optimizer optimizes.
// 2. Unoptimized reference query: evaluates predicate inside projection CASE expression on unoptimized table scan.
type NoRECPair struct {
	Table            TableSchema
	OptimizedSQL     string
	UnoptimizedSQL   string
	Predicate        string
}

// GenerateNoRECPair generates a NoREC query pair for differential testing.
func GenerateNoRECPair(rng *rand.Rand, table TableSchema, maxDepth int) NoRECPair {
	predicate := GeneratePredicate(rng, table, 0, maxDepth)

	// Optimized query: COUNT(*) with WHERE predicate
	optSQL := fmt.Sprintf("SELECT COUNT(*) AS total FROM %s WHERE %s;", table.Name, predicate)

	// Unoptimized reference query: SUM(CASE WHEN predicate THEN 1 ELSE 0 END) without WHERE
	unoptSQL := fmt.Sprintf("SELECT COALESCE(SUM(CASE WHEN %s THEN 1 ELSE 0 END), 0) AS total FROM %s;", predicate, table.Name)

	return NoRECPair{
		Table:          table,
		OptimizedSQL:   optSQL,
		UnoptimizedSQL: unoptSQL,
		Predicate:      predicate,
	}
}

// TLPPair contains queries partitioned under SQL-92 Ternary Logic (3VL):
// Invariant: COUNT(Total) = COUNT(WHERE phi) + COUNT(WHERE NOT (phi)) + COUNT(WHERE (phi) IS NULL)
type TLPPair struct {
	Table        TableSchema
	OriginalSQL  string
	TrueSQL      string
	FalseSQL     string
	NullSQL      string
	Predicate    string
}

// GenerateTLPPair generates a TLP query set according to Ternary Logic Partitioning.
func GenerateTLPPair(rng *rand.Rand, table TableSchema, maxDepth int) TLPPair {
	predicate := GeneratePredicate(rng, table, 0, maxDepth)

	origSQL := fmt.Sprintf("SELECT COUNT(*) AS total FROM %s;", table.Name)
	trueSQL := fmt.Sprintf("SELECT COUNT(*) AS total FROM %s WHERE (%s);", table.Name, predicate)
	falseSQL := fmt.Sprintf("SELECT COUNT(*) AS total FROM %s WHERE NOT (%s);", table.Name, predicate)
	nullSQL := fmt.Sprintf("SELECT COUNT(*) AS total FROM %s WHERE (%s) IS NULL;", table.Name, predicate)

	return TLPPair{
		Table:       table,
		OriginalSQL: origSQL,
		TrueSQL:     trueSQL,
		FalseSQL:    falseSQL,
		NullSQL:     nullSQL,
		Predicate:   predicate,
	}
}

// PQSPair contains a synthesized query guaranteed to match a known pivot row ID.
// Invariant: SELECT * FROM T WHERE phi MUST return at least the pivot row.
type PQSPair struct {
	Table        TableSchema
	PivotID      int64
	Predicate    string
	QuerySQL     string
}

// GeneratePQSPair generates a Pivoted Query Synthesis pair where the predicate is guaranteed
// to evaluate to TRUE for pivotID.
func GeneratePQSPair(rng *rand.Rand, table TableSchema, pivotID int64) PQSPair {
	// Synthesize a conjunction: (id = pivotID OR (id = pivotID AND (true_expr)))
	pk := table.PKColumn()
	cols := table.NonPKColumns()

	var pred string
	if len(cols) > 0 && rng.Float64() < 0.7 {
		col := cols[rng.Intn(len(cols))]
		// Construct an equality or NULL check that guarantees inclusion of pivotID:
		// (pk = pivotID AND (col IS NULL OR col IS NOT NULL))
		pred = fmt.Sprintf("(%s = %d AND (%s IS NULL OR %s IS NOT NULL))", pk.Name, pivotID, col.Name, col.Name)
	} else {
		pred = fmt.Sprintf("(%s = %d)", pk.Name, pivotID)
	}

	querySQL := fmt.Sprintf("SELECT %s FROM %s WHERE %s;", pk.Name, table.Name, pred)

	return PQSPair{
		Table:     table,
		PivotID:   pivotID,
		Predicate: pred,
		QuerySQL:  querySQL,
	}
}

// SelectQuery contains a generated SELECT query with projected columns and optional ORDER BY.
type SelectQuery struct {
	Table       TableSchema
	SQL         string
	Projections []string
	Predicate   string
}

// GenerateSelectQuery generates a SELECT query for cross-engine differential testing.
func GenerateSelectQuery(rng *rand.Rand, table TableSchema, maxDepth int) SelectQuery {
	numProj := rng.Intn(len(table.Columns)) + 1
	var projs []string
	for i := 0; i < numProj; i++ {
		projs = append(projs, GenerateScalarExpr(rng, table, 0, 1))
	}

	predicate := GeneratePredicate(rng, table, 0, maxDepth)
	pk := table.PKColumn()

	sql := fmt.Sprintf("SELECT %s FROM %s WHERE %s ORDER BY %s;",
		strings.Join(projs, ", "), table.Name, predicate, pk.Name)

	return SelectQuery{
		Table:       table,
		SQL:         sql,
		Projections: projs,
		Predicate:   predicate,
	}
}

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

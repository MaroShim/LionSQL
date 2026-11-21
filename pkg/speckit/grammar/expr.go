package grammar

import (
	"fmt"
	"math/rand"
)

// GeneratePredicate generates a boolean WHERE predicate expression from table columns.
func GeneratePredicate(rng *rand.Rand, table TableSchema, depth, maxDepth int) string {
	if depth >= maxDepth {
		return generateLeafPredicate(rng, table)
	}

	choice := rng.Intn(13)
	switch choice {
	case 0, 1: // AND
		left := GeneratePredicate(rng, table, depth+1, maxDepth)
		right := GeneratePredicate(rng, table, depth+1, maxDepth)
		return fmt.Sprintf("(%s AND %s)", left, right)
	case 2, 3: // OR
		left := GeneratePredicate(rng, table, depth+1, maxDepth)
		right := GeneratePredicate(rng, table, depth+1, maxDepth)
		return fmt.Sprintf("(%s OR %s)", left, right)
	case 4: // NOT
		inner := GeneratePredicate(rng, table, depth+1, maxDepth)
		return fmt.Sprintf("NOT (%s)", inner)
	case 5: // BETWEEN
		col := randomColumn(rng, table)
		v1 := SampleValues(col.Type, rng, false)
		v2 := SampleValues(col.Type, rng, false)
		return fmt.Sprintf("(%s BETWEEN %s AND %s)", col.Name, v1, v2)
	case 6: // IN list with NULL to test SQL-92 3VL
		col := randomColumn(rng, table)
		v1 := SampleValues(col.Type, rng, false)
		v2 := SampleValues(col.Type, rng, true)
		v3 := SampleValues(col.Type, rng, true)
		return fmt.Sprintf("(%s IN (%s, %s, %s))", col.Name, v1, v2, v3)
	case 7: // CASE WHEN
		cond := generateLeafPredicate(rng, table)
		return fmt.Sprintf("(CASE WHEN %s THEN 1 ELSE 0 END = 1)", cond)
	case 8: // COALESCE
		col := randomColumn(rng, table)
		v := SampleValues(col.Type, rng, false)
		return fmt.Sprintf("(COALESCE(%s, %s) = %s)", col.Name, v, v)
	case 9: // Arithmetic in predicate (e.g. col + 1 > 5)
		col := randomColumn(rng, table)
		if isNumericType(col.Type) {
			op := []string{"+", "-", "*"}[rng.Intn(3)]
			n := rng.Intn(5) + 1
			v := SampleValues(col.Type, rng, false)
			return fmt.Sprintf("((%s %s %d) > %s)", col.Name, op, n, v)
		}
		return generateLeafPredicate(rng, table)
	case 10: // LIKE / NOT LIKE for VARCHAR
		for _, col := range table.Columns {
			if col.Type == TypeVarChar {
				patterns := []string{"'%a%'", "'%NULL%'", "'foo%'", "'%123'", "'_'", "'a%'"}
				pat := patterns[rng.Intn(len(patterns))]
				if rng.Intn(2) == 0 {
					return fmt.Sprintf("(%s LIKE %s)", col.Name, pat)
				}
				return fmt.Sprintf("(%s NOT LIKE %s)", col.Name, pat)
			}
		}
		return generateLeafPredicate(rng, table)
	case 11: // Date functions for DATETIME
		for _, col := range table.Columns {
			if col.Type == TypeDateTime {
				return fmt.Sprintf("(YEAR(%s) = 2026)", col.Name)
			}
		}
		return generateLeafPredicate(rng, table)
	default:
		return generateLeafPredicate(rng, table)
	}
}

// generateLeafPredicate creates a leaf comparison predicate.
func generateLeafPredicate(rng *rand.Rand, table TableSchema) string {
	col := randomColumn(rng, table)

	opChoice := rng.Intn(7)
	switch opChoice {
	case 0:
		return fmt.Sprintf("(%s IS NULL)", col.Name)
	case 1:
		return fmt.Sprintf("(%s IS NOT NULL)", col.Name)
	case 2:
		val := SampleValues(col.Type, rng, col.Nullable)
		return fmt.Sprintf("(%s = %s)", col.Name, val)
	case 3:
		val := SampleValues(col.Type, rng, col.Nullable)
		return fmt.Sprintf("(%s <> %s)", col.Name, val)
	case 4:
		val := SampleValues(col.Type, rng, col.Nullable)
		return fmt.Sprintf("(%s < %s)", col.Name, val)
	case 5:
		val := SampleValues(col.Type, rng, col.Nullable)
		return fmt.Sprintf("(%s >= %s)", col.Name, val)
	default:
		// Column-to-Column comparison if there's another column of same type
		otherCol := findColumnWithSameType(table, col)
		if otherCol != nil && otherCol.Name != col.Name {
			return fmt.Sprintf("(%s = %s)", col.Name, otherCol.Name)
		}
		val := SampleValues(col.Type, rng, col.Nullable)
		return fmt.Sprintf("(%s = %s)", col.Name, val)
	}
}

// GenerateScalarExpr generates a scalar expression for projections.
func GenerateScalarExpr(rng *rand.Rand, table TableSchema, depth, maxDepth int) string {
	col := randomColumn(rng, table)
	if depth >= maxDepth {
		return col.Name
	}

	switch col.Type {
	case TypeInt, TypeBigInt, TypeSmallInt, TypeTinyInt:
		ops := []string{"+", "-", "*", "%"}
		op := ops[rng.Intn(len(ops))]
		v := rng.Intn(10) + 1
		return fmt.Sprintf("(%s %s %d)", col.Name, op, v)
	case TypeFloat, TypeDecimal:
		return fmt.Sprintf("ABS(%s)", col.Name)
	case TypeVarChar:
		return fmt.Sprintf("UPPER(%s)", col.Name)
	default:
		return col.Name
	}
}

func randomColumn(rng *rand.Rand, table TableSchema) ColumnDef {
	return table.Columns[rng.Intn(len(table.Columns))]
}

func findColumnWithSameType(table TableSchema, match ColumnDef) *ColumnDef {
	for i := range table.Columns {
		if table.Columns[i].Name != match.Name && table.Columns[i].Type == match.Type {
			return &table.Columns[i]
		}
	}
	return nil
}

func isNumericType(t SQLType) bool {
	switch t {
	case TypeInt, TypeBigInt, TypeSmallInt, TypeTinyInt, TypeFloat, TypeDecimal:
		return true
	default:
		return false
	}
}

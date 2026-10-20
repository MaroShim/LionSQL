package planner

import (
	"strings"

	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

// ColumnRef represents an extracted column reference from a predicate.
type ColumnRef struct {
	TableOrAlias string
	Column       string
}

// PushdownContext holds the table schemas and aliases available for predicate pushdown analysis.
type PushdownContext struct {
	PrimaryTable  string
	PrimaryAlias  string
	PrimarySchema *storage.TableSchema
	Joins         []*JoinInfo
	RightSchemas  map[string]*storage.TableSchema
}

// PushDownResult holds the partitioned predicates after pushdown analysis.
type PushDownResult struct {
	PrimaryConds   []string         // Predicates pushed down to primary table
	JoinConds      map[int][]string // Predicates pushed down to Joins[i]
	RemainingConds []string         // Cross-table or non-pushable predicates
}

// ExtractColumnRefs extracts all referenced column and table names from a SQL predicate expression.
func ExtractColumnRefs(cond string) []ColumnRef {
	var refs []ColumnRef
	l := proc.NewLexer(cond)
	var tokens []proc.Token
	for {
		tok := l.NextToken()
		if tok.Type == proc.TokenEOF {
			break
		}
		tokens = append(tokens, tok)
	}

	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok.Type == proc.TokenIdent {
			// If followed by '(', it's a function call (e.g. UPPER, COUNT), not a column reference
			if i+1 < len(tokens) && tokens[i+1].Type == proc.TokenLParen {
				continue
			}

			lit := cleanIdent(tok.Literal)
			if lit == "" {
				continue
			}

			// Check for dot qualification: e.g. u.name or orders.id
			if dotIdx := strings.Index(lit, "."); dotIdx != -1 {
				tbl := cleanIdent(lit[:dotIdx])
				col := cleanIdent(lit[dotIdx+1:])
				refs = append(refs, ColumnRef{TableOrAlias: tbl, Column: col})
			} else {
				refs = append(refs, ColumnRef{TableOrAlias: "", Column: lit})
			}
		}
	}
	return refs
}

func schemaHasColumn(s *storage.TableSchema, col string) bool {
	if s == nil {
		return false
	}
	col = strings.ToLower(cleanIdent(col))
	for _, c := range s.Columns {
		if strings.ToLower(cleanIdent(c.Name)) == col {
			return true
		}
	}
	return false
}

// ResolveTable maps a ColumnRef to a table index:
// 0: Primary Table
// 1 + i: Joins[i]
// -1: Ambiguous or Unknown
func (ctx *PushdownContext) ResolveTable(ref ColumnRef) (int, bool) {
	col := strings.ToLower(cleanIdent(ref.Column))
	if ref.TableOrAlias != "" {
		tbl := strings.ToLower(cleanIdent(ref.TableOrAlias))
		pName := strings.ToLower(cleanIdent(ctx.PrimaryTable))
		pAlias := strings.ToLower(cleanIdent(ctx.PrimaryAlias))
		if (pName != "" && tbl == pName) || (pAlias != "" && tbl == pAlias) {
			return 0, true
		}
		for i, j := range ctx.Joins {
			jName := strings.ToLower(cleanIdent(j.Table))
			jAlias := strings.ToLower(cleanIdent(j.Alias))
			if (jName != "" && tbl == jName) || (jAlias != "" && tbl == jAlias) {
				return 1 + i, true
			}
		}
		return -1, false
	}

	// Unqualified column: search available schemas
	primaryHas := schemaHasColumn(ctx.PrimarySchema, col)
	matchIdx := -1
	matchCount := 0
	if primaryHas {
		matchIdx = 0
		matchCount++
	}

	for i, j := range ctx.Joins {
		var s *storage.TableSchema
		if ctx.RightSchemas != nil {
			if sc, ok := ctx.RightSchemas[j.Table]; ok && sc != nil {
				s = sc
			} else if sc, ok := ctx.RightSchemas[cleanIdent(j.Table)]; ok && sc != nil {
				s = sc
			} else if sc, ok := ctx.RightSchemas[j.Alias]; ok && sc != nil {
				s = sc
			} else if sc, ok := ctx.RightSchemas[cleanIdent(j.Alias)]; ok && sc != nil {
				s = sc
			}
		}
		if schemaHasColumn(s, col) {
			matchIdx = 1 + i
			matchCount++
		}
	}

	if matchCount == 1 {
		return matchIdx, true
	}
	if matchCount == 0 && len(ctx.Joins) == 0 {
		// Single-table query: attribute to primary table by default
		return 0, true
	}
	return -1, false
}

// PushDownPredicates analyzes a query's WHERE clause and partitions its conjuncts into:
// - PrimaryConds: predicates referencing only the primary table (pushable to primary scan)
// - JoinConds: predicates referencing only a specific joined table (pushable to join scan)
// - RemainingConds: cross-table conditions or conditions requiring post-join evaluation
func PushDownPredicates(q *QueryInfo, schema *storage.TableSchema, rightSchemas map[string]*storage.TableSchema) *PushDownResult {
	res := &PushDownResult{
		JoinConds: make(map[int][]string),
	}
	if q == nil || q.WhereClause == "" {
		return res
	}

	var joinList []*JoinInfo
	if len(q.Joins) > 0 {
		joinList = q.Joins
	} else if q.Join != nil {
		joinList = []*JoinInfo{q.Join}
	}

	ctx := &PushdownContext{
		PrimaryTable:  q.TableName,
		PrimaryAlias:  q.TableAlias,
		PrimarySchema: schema,
		Joins:         joinList,
		RightSchemas:  rightSchemas,
	}

	conds := splitAndConditions(q.WhereClause)
	for _, cond := range conds {
		cond = strings.TrimSpace(cond)
		if cond == "" {
			continue
		}

		refs := ExtractColumnRefs(cond)
		if len(refs) == 0 {
			// Constant or literal predicate like "1=1" or "3 > 2"
			res.PrimaryConds = append(res.PrimaryConds, cond)
			continue
		}

		firstTbl := -2
		allSame := true
		for _, ref := range refs {
			tblIdx, ok := ctx.ResolveTable(ref)
			if !ok {
				allSame = false
				break
			}
			if firstTbl == -2 {
				firstTbl = tblIdx
			} else if firstTbl != tblIdx {
				allSame = false
				break
			}
		}

		if !allSame || firstTbl == -1 {
			// Cross-table or unresolved: remains above joins
			res.RemainingConds = append(res.RemainingConds, cond)
			continue
		}

		if firstTbl == 0 {
			// Primary table predicate
			res.PrimaryConds = append(res.PrimaryConds, cond)
		} else {
			joinIdx := firstTbl - 1
			j := joinList[joinIdx]
			// Push down to inner, semi, or anti join
			if strings.EqualFold(j.Type, "INNER") || strings.EqualFold(j.Type, "SEMI") || strings.EqualFold(j.Type, "ANTI") || j.Type == "" {
				res.JoinConds[joinIdx] = append(res.JoinConds[joinIdx], cond)
			} else {
				// LEFT/RIGHT/FULL OUTER JOIN: WHERE predicates on nullable side can null-reject or alter outer join semantics
				res.RemainingConds = append(res.RemainingConds, cond)
			}
		}
	}

	return res
}

package planner

import (
	"fmt"
	"sort"
	"strings"

	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
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
	PrimaryConds    []string         // Predicates pushed down to primary table
	JoinConds       map[int][]string // Predicates pushed down to Joins[i]
	RemainingConds  []string         // Cross-table or non-pushable predicates
	IsContradiction bool             // True if a static contradiction (e.g. 1=0, false) was detected
}

// IsStaticContradiction checks if a predicate condition is a constant contradiction (evaluates to false or null).
func IsStaticContradiction(cond string) bool {
	cond = strings.TrimSpace(cond)
	if cond == "" {
		return false
	}
	refs := ExtractColumnRefs(cond)
	if len(refs) > 0 {
		return false
	}
	p := proc.NewParser(proc.NewLexer(cond))
	expr := p.ParseExpr()
	if expr == nil {
		u := strings.ToUpper(cond)
		if u == "1=0" || u == "1 = 0" || u == "0=1" || u == "0 = 1" || u == "FALSE" {
			return true
		}
		return false
	}
	return !proc.EvalBoolExpr(expr, nil, nil)
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

	convertDepth := -1
	parenDepth := 0

	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]

		if tok.Type == proc.TokenLParen {
			parenDepth++
			continue
		} else if tok.Type == proc.TokenRParen {
			if convertDepth == parenDepth {
				convertDepth = -1
			}
			parenDepth--
			continue
		}

		if tok.Type == proc.TokenConvert || tok.Type == proc.TokenTryConvert {
			// In CONVERT(data_type, expr [, style]), the first argument before comma is a type
			convertDepth = parenDepth + 1
			continue
		}

		if convertDepth == parenDepth && tok.Type == proc.TokenComma {
			// Reached the end of the data_type argument in CONVERT(...)
			convertDepth = -1
			continue
		}

		if tok.Type == proc.TokenIdent {
			// If followed by '(', it's a function call (e.g. UPPER, COUNT) or type with params (e.g. VARCHAR(50))
			if i+1 < len(tokens) && tokens[i+1].Type == proc.TokenLParen {
				continue
			}

			// If inside the first argument of CONVERT/TRY_CONVERT, it's a data type
			if convertDepth == parenDepth {
				continue
			}

			// If preceded by AS, ::, or COLLATE, it's an alias, cast type, or collation name
			if i > 0 {
				prev := tokens[i-1].Type
				if prev == proc.TokenAs || prev == proc.TokenDoubleColon || prev == proc.TokenCollate {
					continue
				}
				// Also check multi-word type continuation, e.g. DOUBLE PRECISION or CHARACTER VARYING
				if i > 1 && (tokens[i-2].Type == proc.TokenAs || tokens[i-2].Type == proc.TokenDoubleColon) {
					if sqlutil.IsSQLTypeName(tokens[i-1].Literal) && sqlutil.IsSQLTypeName(tok.Literal) {
						continue
					}
				}
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
		if pAlias != "" {
			if tbl == pAlias {
				return 0, true
			}
		} else if pName != "" && tbl == pName {
			return 0, true
		}
		for i, j := range ctx.Joins {
			jName := strings.ToLower(cleanIdent(j.Table))
			jAlias := strings.ToLower(cleanIdent(j.Alias))
			if jAlias != "" {
				if tbl == jAlias {
					return 1 + i, true
				}
			} else if jName != "" && tbl == jName {
				return 1 + i, true
			}
		}
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

		if IsStaticContradiction(cond) {
			res.IsContradiction = true
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
			// Push down to inner, semi, anti, or cross join
			if strings.EqualFold(j.Type, "INNER") || strings.EqualFold(j.Type, "SEMI") || strings.EqualFold(j.Type, "ANTI") || strings.EqualFold(j.Type, "CROSS") || j.Type == "" {
				res.JoinConds[joinIdx] = append(res.JoinConds[joinIdx], cond)
			} else {
				// LEFT/RIGHT/FULL OUTER JOIN: WHERE predicates on nullable side can null-reject or alter outer join semantics
				res.RemainingConds = append(res.RemainingConds, cond)
			}
		}
	}

	// Infer equijoins from RemainingConds for unconstrained joins (e.g. ANSI-89 comma joins)
	numTables := 1 + len(joinList)
	type equiEdge struct {
		cond        string
		u, v        int
		left, right string
	}

	var equiEdges []equiEdge
	var nonEquiConds []string

	for _, cond := range res.RemainingConds {
		l, r := sqlutil.SplitJoinCond(cond)
		if l == "" || r == "" {
			nonEquiConds = append(nonEquiConds, cond)
			continue
		}
		refsL := ExtractColumnRefs(l)
		refsR := ExtractColumnRefs(r)
		if len(refsL) != 1 || len(refsR) != 1 {
			nonEquiConds = append(nonEquiConds, cond)
			continue
		}
		idxL, okL := ctx.ResolveTable(refsL[0])
		idxR, okR := ctx.ResolveTable(refsR[0])
		if !okL || !okR || idxL < 0 || idxR < 0 || idxL == idxR {
			nonEquiConds = append(nonEquiConds, cond)
			continue
		}

		equiEdges = append(equiEdges, equiEdge{
			cond:  cond,
			u:     idxL,
			v:     idxR,
			left:  l,
			right: r,
		})
	}

	type adjEdge struct {
		edgeIdx int
		target  int
	}
	adj := make([][]adjEdge, numTables)
	for i, e := range equiEdges {
		adj[e.u] = append(adj[e.u], adjEdge{edgeIdx: i, target: e.v})
		adj[e.v] = append(adj[e.v], adjEdge{edgeIdx: i, target: e.u})
	}

	assignedJoin := make([]bool, len(joinList))
	for jIdx, j := range joinList {
		if j.LeftKey != "" && j.RightKey != "" {
			assignedJoin[jIdx] = true
		}
	}

	visited := make([]bool, numTables)
	usedEdge := make([]bool, len(equiEdges))

	bfs := func(start int) {
		visited[start] = true
		queue := []int{start}

		for len(queue) > 0 {
			curr := queue[0]
			queue = queue[1:]

			for _, a := range adj[curr] {
				neighbor := a.target
				if !visited[neighbor] {
					visited[neighbor] = true
					queue = append(queue, neighbor)

					if neighbor > 0 {
						jIdx := neighbor - 1
						if jIdx < len(joinList) && !assignedJoin[jIdx] {
							j := joinList[jIdx]
							if j.Type == "CROSS" || strings.EqualFold(j.Type, "INNER") || j.Type == "" {
								j.Type = "INNER"
								e := equiEdges[a.edgeIdx]
								if e.u == curr {
									j.LeftKey = e.left
									j.RightKey = e.right
								} else {
									j.LeftKey = e.right
									j.RightKey = e.left
								}
								assignedJoin[jIdx] = true
								usedEdge[a.edgeIdx] = true
							}
						}
					}
				}
			}
		}
	}

	bfs(0)
	for t := 1; t < numTables; t++ {
		if !visited[t] {
			bfs(t)
		}
	}

	var finalRemaining []string
	finalRemaining = append(finalRemaining, nonEquiConds...)
	for i, e := range equiEdges {
		if !usedEdge[i] {
			finalRemaining = append(finalRemaining, e.cond)
		}
	}
	res.RemainingConds = finalRemaining

	// Transitive Predicate Inference (Equivalence Class Propagation) across equi-joined relations
	InferTransitivePredicates(res, ctx)

	return res
}

type equivColKey struct {
	tableIdx int
	colName  string
}

// InferTransitivePredicates performs transitive predicate inference (equivalence class propagation)
// across equi-joined tables for INNER/CROSS joins.
// E.g.: o.cust_id = c.id AND o.cust_id = 100 => infers c.id = 100.
func InferTransitivePredicates(res *PushDownResult, ctx *PushdownContext) {
	if res == nil || ctx == nil || len(ctx.Joins) == 0 {
		return
	}

	// 1. Build adjacency list of column equivalences from INNER join conditions and WHERE equijoins
	adj := make(map[equivColKey][]equivColKey)
	addEdge := func(k1, k2 equivColKey) {
		adj[k1] = append(adj[k1], k2)
		adj[k2] = append(adj[k2], k1)
	}

	// From Joins:
	for _, j := range ctx.Joins {
		jType := strings.ToUpper(strings.TrimSpace(j.Type))
		if jType != "INNER" && jType != "CROSS" && jType != "" {
			continue
		}
		if j.LeftKey != "" && j.RightKey != "" {
			refsL := ExtractColumnRefs(j.LeftKey)
			refsR := ExtractColumnRefs(j.RightKey)
			if len(refsL) == 1 && len(refsR) == 1 {
				idxL, okL := ctx.ResolveTable(refsL[0])
				idxR, okR := ctx.ResolveTable(refsR[0])
				if okL && okR && idxL != idxR {
					kL := equivColKey{tableIdx: idxL, colName: strings.ToLower(cleanIdent(refsL[0].Column))}
					kR := equivColKey{tableIdx: idxR, colName: strings.ToLower(cleanIdent(refsR[0].Column))}
					addEdge(kL, kR)
				}
			}
		}
	}

	// From WHERE equi-conditions in res.RemainingConds:
	for _, cond := range res.RemainingConds {
		l, r := sqlutil.SplitJoinCond(cond)
		if l == "" || r == "" {
			continue
		}
		refsL := ExtractColumnRefs(l)
		refsR := ExtractColumnRefs(r)
		if len(refsL) == 1 && len(refsR) == 1 {
			idxL, okL := ctx.ResolveTable(refsL[0])
			idxR, okR := ctx.ResolveTable(refsR[0])
			if okL && okR && idxL != idxR {
				kL := equivColKey{tableIdx: idxL, colName: strings.ToLower(cleanIdent(refsL[0].Column))}
				kR := equivColKey{tableIdx: idxR, colName: strings.ToLower(cleanIdent(refsR[0].Column))}
				addEdge(kL, kR)
			}
		}
	}

	if len(adj) == 0 {
		return
	}

	// 2. Find connected components (equivalence classes) using BFS
	visited := make(map[equivColKey]bool)
	var equivClasses [][]equivColKey

	for node := range adj {
		if visited[node] {
			continue
		}
		var comp []equivColKey
		queue := []equivColKey{node}
		visited[node] = true

		for len(queue) > 0 {
			curr := queue[0]
			queue = queue[1:]
			comp = append(comp, curr)

			for _, neighbor := range adj[curr] {
				if !visited[neighbor] {
					visited[neighbor] = true
					queue = append(queue, neighbor)
				}
			}
		}

		if len(comp) > 1 {
			equivClasses = append(equivClasses, comp)
		}
	}

	// Helper to get all current conditions for a table
	getCondsForTable := func(tblIdx int) []string {
		if tblIdx == 0 {
			return res.PrimaryConds
		}
		return res.JoinConds[tblIdx-1]
	}

	// Helper to check if a condition on (col, op, val) already exists
	hasMatchingCond := func(conds []string, col, op, val string) bool {
		for _, c := range conds {
			cRawCol, cOp, cVal, ok := parseASTComparison(c)
			if ok && cOp == op && cVal == val {
				cCol := strings.ToLower(cleanIdent(cRawCol))
				if dot := strings.Index(cCol, "."); dot != -1 {
					cCol = cCol[dot+1:]
				}
				if cCol == col {
					return true
				}
			}
		}
		return false
	}

	// Helper to format table prefix for column
	getTablePrefix := func(tblIdx int) string {
		if tblIdx == 0 {
			if ctx.PrimaryAlias != "" {
				return ctx.PrimaryAlias + "."
			}
			if ctx.PrimaryTable != "" {
				return ctx.PrimaryTable + "."
			}
			return ""
		}
		j := ctx.Joins[tblIdx-1]
		if j.Alias != "" {
			return j.Alias + "."
		}
		if j.Table != "" {
			return j.Table + "."
		}
		return ""
	}

	// 3. For each equivalence class, collect single-table predicates and propagate across the class
	for _, eqClass := range equivClasses {
		type predInfo struct {
			srcTbl int
			op     string
			val    string
		}
		var preds []predInfo

		for _, member := range eqClass {
			conds := getCondsForTable(member.tableIdx)
			for _, cond := range conds {
				rawCol, op, val, ok := parseASTComparison(cond)
				if !ok {
					continue
				}
				cCol := strings.ToLower(cleanIdent(rawCol))
				if dot := strings.Index(cCol, "."); dot != -1 {
					cCol = cCol[dot+1:]
				}
				if cCol == member.colName {
					preds = append(preds, predInfo{
						srcTbl: member.tableIdx,
						op:     op,
						val:    val,
					})
				}
			}
		}

		// Propagate collected predicates to other members of the class
		for _, p := range preds {
			for _, member := range eqClass {
				if member.tableIdx == p.srcTbl {
					continue
				}
				targetConds := getCondsForTable(member.tableIdx)
				if hasMatchingCond(targetConds, member.colName, p.op, p.val) {
					continue
				}

				newCond := fmt.Sprintf("%s%s %s %s", getTablePrefix(member.tableIdx), member.colName, p.op, p.val)
				if member.tableIdx == 0 {
					res.PrimaryConds = append(res.PrimaryConds, newCond)
				} else {
					joinIdx := member.tableIdx - 1
					res.JoinConds[joinIdx] = append(res.JoinConds[joinIdx], newCond)
				}
			}
		}
	}
}

// CollectReferencedColumns returns the sorted slice of 0-based column indices required from the specified table.
// If all columns are needed (e.g. wildcard * or full scan), it returns nil.
func CollectReferencedColumns(schema *storage.TableSchema, tblName string, alias string, q *QueryInfo) []int {
	if schema == nil || len(schema.Columns) == 0 || q == nil {
		return nil
	}

	cleanTbl := strings.ToLower(cleanIdent(tblName))
	cleanAl := strings.ToLower(cleanIdent(alias))

	// 1. Check for wildcards in projections:
	// If "*" is present, or "<alias>.*" / "<tblName>.*", then all columns are required.
	for _, p := range q.Projections {
		trimmed := strings.TrimSpace(p)
		if trimmed == "*" {
			return nil
		}
		upper := strings.ToUpper(trimmed)
		if strings.HasSuffix(upper, ".*") {
			prefix := strings.ToLower(cleanIdent(trimmed[:len(trimmed)-2]))
			if prefix == cleanTbl || (cleanAl != "" && prefix == cleanAl) {
				return nil
			}
		}
	}

	// 2. Collect all candidate column references across all query clauses
	var allRefs []ColumnRef

	for _, p := range q.Projections {
		allRefs = append(allRefs, ExtractColumnRefs(p)...)
	}
	if q.WhereClause != "" {
		allRefs = append(allRefs, ExtractColumnRefs(q.WhereClause)...)
	}
	if q.HavingClause != "" {
		allRefs = append(allRefs, ExtractColumnRefs(q.HavingClause)...)
	}
	for _, g := range q.GroupByCols {
		allRefs = append(allRefs, ExtractColumnRefs(g)...)
	}
	if q.OrderByClause != "" {
		allRefs = append(allRefs, ExtractColumnRefs(q.OrderByClause)...)
	}
	if q.Join != nil {
		allRefs = append(allRefs, ExtractColumnRefs(q.Join.LeftKey)...)
		allRefs = append(allRefs, ExtractColumnRefs(q.Join.RightKey)...)
		if q.Join.Filter != "" {
			allRefs = append(allRefs, ExtractColumnRefs(q.Join.Filter)...)
		}
	}
	for _, j := range q.Joins {
		allRefs = append(allRefs, ExtractColumnRefs(j.LeftKey)...)
		allRefs = append(allRefs, ExtractColumnRefs(j.RightKey)...)
		if j.Filter != "" {
			allRefs = append(allRefs, ExtractColumnRefs(j.Filter)...)
		}
	}
	for _, w := range q.WindowDefs {
		for _, arg := range w.Args {
			allRefs = append(allRefs, ExtractColumnRefs(arg)...)
		}
		for _, pk := range w.PartitionKeys {
			allRefs = append(allRefs, ExtractColumnRefs(pk)...)
		}
		for _, ok := range w.OrderByKeys {
			allRefs = append(allRefs, ExtractColumnRefs(ok.Column)...)
		}
	}

	// 3. Match column references to the table schema
	matched := make(map[int]bool)
	for _, ref := range allRefs {
		refCol := strings.ToLower(cleanIdent(ref.Column))
		if refCol == "" {
			continue
		}

		if ref.TableOrAlias != "" {
			refTbl := strings.ToLower(cleanIdent(ref.TableOrAlias))
			if refTbl != cleanTbl && (cleanAl == "" || refTbl != cleanAl) {
				continue
			}
		}

		for idx, col := range schema.Columns {
			if strings.ToLower(cleanIdent(col.Name)) == refCol {
				matched[idx] = true
				break
			}
		}
	}

	// Expand dependencies for any matched computed columns
	for idx, col := range schema.Columns {
		if matched[idx] && col.HasComputedExpr() {
			for _, ref := range ExtractColumnRefs(col.ComputedExpr) {
				refCol := strings.ToLower(cleanIdent(ref.Column))
				for depIdx, depCol := range schema.Columns {
					if strings.ToLower(cleanIdent(depCol.Name)) == refCol {
						matched[depIdx] = true
						break
					}
				}
			}
		}
	}

	// If no columns were matched (e.g. SELECT 1, COUNT(*)), retain at least column 0
	if len(matched) == 0 {
		return []int{0}
	}

	// If all columns are needed, return nil (meaning full decode without pruning overhead)
	if len(matched) >= len(schema.Columns) {
		return nil
	}

	result := make([]int, 0, len(matched))
	for idx := range matched {
		result = append(result, idx)
	}
	sort.Ints(result)
	return result
}

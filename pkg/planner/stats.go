package planner

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"sqlion/pkg/proc"
	"sqlion/pkg/storage"
)

// Standard PostgreSQL-aligned query cost parameters.
const (
	SeqPageCost        = 1.0    // Cost of a sequential disk page fetch
	RandomPageCost     = 4.0    // Cost of a non-sequential disk page fetch
	CPUTupleCost       = 0.01   // Cost of processing a tuple
	CPUIndexTupleCost  = 0.005  // Cost of processing an index entry
	CPUOperatorCost    = 0.0025 // Cost of evaluating an operator or function
	RowsPerPageDefault = 50     // Default average number of rows per disk page
)

// ColumnStats stores statistical distribution metadata for a single column.
type ColumnStats struct {
	Name          string          `json:"name"`
	Type          storage.DataType `json:"type"`
	NullCount     int64           `json:"null_count"`
	DistinctCount int64           `json:"distinct_count"` // NDV (Number of Distinct Values)
	MinVal        storage.Value   `json:"min_val"`
	MaxVal        storage.Value   `json:"max_val"`
	Histogram     []storage.Value `json:"histogram,omitempty"` // Equi-depth quantile bucket bounds
}

// MultiColumnStats stores joint distribution, distinct combination count (NDV),
// and functional dependency metadata for a group of correlated columns (C-5).
type MultiColumnStats struct {
	Name          string   `json:"name,omitempty"`
	Columns       []string `json:"columns"`        // Cleaned lowercase column names, e.g. ["country", "city"]
	DistinctCount int64    `json:"distinct_count"` // Multi-column NDV
	Dependency    float64  `json:"dependency"`     // Degree of functional dependency [0.0, 1.0]
}

// TableStats stores table-level cardinality and column statistics.
type TableStats struct {
	TableName    string                  `json:"table_name"`
	RowCount     int64                   `json:"row_count"`
	PageCount    int64                   `json:"page_count"`
	Columns      map[string]*ColumnStats `json:"columns"`
	MultiColumns []*MultiColumnStats     `json:"multi_columns,omitempty"`
	Partitions   []string                `json:"partitions,omitempty"`
	AnalyzedAt   time.Time               `json:"analyzed_at"`
}

// NewTableStats creates a new TableStats instance.
func NewTableStats(tableName string, rowCount int64) *TableStats {
	if rowCount < 0 {
		rowCount = 0
	}
	pages := rowCount / RowsPerPageDefault
	if pages == 0 && rowCount > 0 {
		pages = 1
	}
	return &TableStats{
		TableName:  tableName,
		RowCount:   rowCount,
		PageCount:  pages,
		Columns:    make(map[string]*ColumnStats),
		AnalyzedAt: time.Now(),
	}
}

// BuildTableStatsParallel computes ColumnStats for all columns in parallel using worker goroutines.
func BuildTableStatsParallel(tbl string, schema *storage.TableSchema, colVals map[string][]storage.Value, nullCounts map[string]int64, rowCount int64) *TableStats {
	ts := NewTableStats(tbl, rowCount)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, c := range schema.Columns {
		wg.Add(1)
		go func(col storage.Column) {
			defer wg.Done()
			cs := BuildColumnStats(col.Name, col.Type, colVals[col.Name], nullCounts[col.Name], rowCount)
			mu.Lock()
			ts.Columns[col.Name] = cs
			mu.Unlock()
		}(c)
	}
	wg.Wait()
	return ts
}

// BuildColumnStats constructs ColumnStats from sampled/scanned column values.
func BuildColumnStats(name string, colType storage.DataType, vals []storage.Value, nullCount int64, totalRows int64) *ColumnStats {
	cs := &ColumnStats{
		Name:      name,
		Type:      colType,
		NullCount: nullCount,
	}

	if len(vals) == 0 {
		return cs
	}

	// Sort values to calculate NDV, min, max, and equi-depth histogram
	sort.Slice(vals, func(i, j int) bool {
		return vals[i].Compare(vals[j]) < 0
	})

	cs.MinVal = vals[0]
	cs.MaxVal = vals[len(vals)-1]

	// Compute NDV (Number of Distinct Values)
	distinct := int64(1)
	for i := 1; i < len(vals); i++ {
		if !vals[i].Equal(vals[i-1]) {
			distinct++
		}
	}
	cs.DistinctCount = distinct

	// Construct Equi-Depth Histogram (up to 20 quantile buckets)
	numBuckets := 20
	if len(vals) < numBuckets {
		numBuckets = len(vals)
	}
	if numBuckets > 1 {
		cs.Histogram = make([]storage.Value, numBuckets)
		for b := 0; b < numBuckets; b++ {
			idx := int(float64(b) * float64(len(vals)-1) / float64(numBuckets-1))
			cs.Histogram[b] = vals[idx]
		}
	} else if len(vals) > 0 {
		cs.Histogram = []storage.Value{vals[0]}
	}

	return cs
}

// EstimateSelectivity estimates the fraction of rows [0.0, 1.0] matching a predicate on a column.
func (ts *TableStats) EstimateSelectivity(colName string, op string, valStr string) float64 {
	if ts == nil || ts.RowCount == 0 {
		return 0.33 // Default heuristic fallback when no stats exist
	}

	cs, exists := ts.Columns[colName]
	if !exists {
		if idx := strings.Index(colName, "."); idx != -1 {
			cs, exists = ts.Columns[colName[idx+1:]]
		}
	}
	if !exists || cs == nil {
		return 0.33
	}

	op = strings.ToUpper(strings.TrimSpace(op))
	nonNullFraction := 1.0 - (float64(cs.NullCount) / float64(ts.RowCount))
	if nonNullFraction < 0.01 {
		nonNullFraction = 0.01
	}

	switch op {
	case "IS NULL":
		return float64(cs.NullCount) / float64(ts.RowCount)
	case "IS NOT NULL":
		return nonNullFraction
	case "=", "==":
		if cs.DistinctCount > 0 {
			sel := 1.0 / float64(cs.DistinctCount)
			return math.Min(1.0, math.Max(0.0001, sel*nonNullFraction))
		}
		return 0.1
	case "!=", "<>":
		if cs.DistinctCount > 0 {
			sel := 1.0 - (1.0 / float64(cs.DistinctCount))
			return math.Min(1.0, math.Max(0.0, sel*nonNullFraction))
		}
		return 0.9
	case "<", "<=":
		return cs.interpolateRange(valStr, true) * nonNullFraction
	case ">", ">=":
		return cs.interpolateRange(valStr, false) * nonNullFraction
	case "LIKE":
		return 0.05 // Typical pattern selectivity
	default:
		return 0.33
	}
}

func (ts *TableStats) findColumnStats(colName string) (*ColumnStats, bool) {
	if ts == nil || ts.Columns == nil {
		return nil, false
	}
	col := cleanIdent(colName)
	if cs, ok := ts.Columns[col]; ok && cs != nil {
		return cs, true
	}
	if idx := strings.Index(col, "."); idx != -1 {
		if cs, ok := ts.Columns[col[idx+1:]]; ok && cs != nil {
			return cs, true
		}
	}
	for k, cs := range ts.Columns {
		if strings.EqualFold(k, col) && cs != nil {
			return cs, true
		}
	}
	return nil, false
}

// AddMultiColumnStats adds or updates extended multi-column statistics for a group of columns (C-5).
func (ts *TableStats) AddMultiColumnStats(name string, cols []string, distinctCount int64, dependency float64) *MultiColumnStats {
	if ts == nil || len(cols) < 2 {
		return nil
	}
	cleanCols := make([]string, len(cols))
	for i, c := range cols {
		cleanCols[i] = strings.ToLower(cleanIdent(c))
	}
	sort.Strings(cleanCols)

	if dependency < 0.0 {
		dependency = 0.0
	} else if dependency > 1.0 {
		dependency = 1.0
	}

	for _, existing := range ts.MultiColumns {
		if equalStringSlices(existing.Columns, cleanCols) {
			existing.Name = name
			existing.DistinctCount = distinctCount
			existing.Dependency = dependency
			return existing
		}
	}

	ms := &MultiColumnStats{
		Name:          name,
		Columns:       cleanCols,
		DistinctCount: distinctCount,
		Dependency:    dependency,
	}
	ts.MultiColumns = append(ts.MultiColumns, ms)
	return ms
}

// FindMultiColumnStats finds an exact matching MultiColumnStats for the given set of columns (order-independent).
func (ts *TableStats) FindMultiColumnStats(cols []string) *MultiColumnStats {
	if ts == nil || len(ts.MultiColumns) == 0 || len(cols) < 2 {
		return nil
	}
	cleanCols := make([]string, len(cols))
	for i, c := range cols {
		cleanCols[i] = strings.ToLower(cleanIdent(c))
	}
	sort.Strings(cleanCols)

	for _, ms := range ts.MultiColumns {
		if equalStringSlices(ms.Columns, cleanCols) {
			return ms
		}
	}
	return nil
}

// FindBestMultiColumnStats finds the most specific MultiColumnStats whose columns are all present in queryCols.
func (ts *TableStats) FindBestMultiColumnStats(queryCols []string) (*MultiColumnStats, []string) {
	if ts == nil || len(ts.MultiColumns) == 0 || len(queryCols) < 2 {
		return nil, nil
	}
	var bestStat *MultiColumnStats
	for _, ms := range ts.MultiColumns {
		if matchSubsetColumns(ms.Columns, queryCols) {
			if bestStat == nil || len(ms.Columns) > len(bestStat.Columns) {
				bestStat = ms
			}
		}
	}
	if bestStat == nil {
		return nil, nil
	}
	return bestStat, bestStat.Columns
}

func matchSubsetColumns(statCols []string, queryCols []string) bool {
	if len(statCols) > len(queryCols) || len(statCols) < 2 {
		return false
	}
	colSet := make(map[string]bool, len(queryCols))
	for _, c := range queryCols {
		colSet[strings.ToLower(cleanIdent(c))] = true
	}
	for _, sc := range statCols {
		if !colSet[strings.ToLower(cleanIdent(sc))] {
			return false
		}
	}
	return true
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// EstimateMultiColumnSelectivity calculates combined joint selectivity for correlated columns (C-5).
func (ts *TableStats) EstimateMultiColumnSelectivity(stat *MultiColumnStats, condCols []string, colSels map[string]float64) float64 {
	if stat == nil || len(condCols) < 2 {
		prod := 1.0
		for _, c := range condCols {
			prod *= colSels[c]
		}
		return prod
	}

	minSel := 1.0
	prodSel := 1.0
	for _, c := range condCols {
		s, ok := colSels[c]
		if !ok || s <= 0 {
			s = 0.33
		}
		if s < minSel {
			minSel = s
		}
		prodSel *= s
	}

	dep := stat.Dependency
	var jointSel float64
	if dep > 0.0 {
		jointSel = dep*minSel + (1.0-dep)*prodSel
	} else if stat.DistinctCount > 0 {
		ndvSel := 1.0 / float64(stat.DistinctCount)
		jointSel = math.Max(ndvSel, prodSel)
	} else {
		jointSel = prodSel
	}

	floor := 0.0001
	if ts.RowCount > 0 {
		floor = 1.0 / float64(ts.RowCount)
	}
	return math.Min(1.0, math.Max(floor, jointSel))
}

// ComputeFunctionalDependency computes degree of functional dependency [0.0, 1.0]
// based on multi-column NDV and individual column NDVs (C-5).
func ComputeFunctionalDependency(cols []string, multiNDV int64, colStats map[string]*ColumnStats, rowCount int64) float64 {
	if len(cols) < 2 || multiNDV <= 0 || rowCount <= 0 {
		return 0.0
	}
	var maxColNDV int64 = 1
	var prodColNDV int64 = 1
	hasStats := false
	for _, c := range cols {
		clean := strings.ToLower(cleanIdent(c))
		if cs, ok := colStats[clean]; ok && cs != nil && cs.DistinctCount > 0 {
			hasStats = true
			if cs.DistinctCount > maxColNDV {
				maxColNDV = cs.DistinctCount
			}
			if prodColNDV < rowCount {
				prodColNDV *= cs.DistinctCount
				if prodColNDV > rowCount {
					prodColNDV = rowCount
				}
			}
		}
	}
	if !hasStats {
		return 0.0
	}
	indepNDV := prodColNDV
	corrNDV := maxColNDV
	if indepNDV <= corrNDV {
		return 1.0
	}
	if multiNDV <= corrNDV {
		return 1.0
	}
	if multiNDV >= indepNDV {
		return 0.0
	}
	dep := float64(indepNDV-multiNDV) / float64(indepNDV-corrNDV)
	if dep < 0.0 {
		return 0.0
	}
	if dep > 1.0 {
		return 1.0
	}
	return dep
}

func extractColumnFromExpr(expr proc.Expr) (string, bool) {
	if expr == nil {
		return "", false
	}
	switch e := expr.(type) {
	case *proc.ColumnRefExpr:
		return cleanIdent(e.Name), true
	case *proc.LiteralExpr:
		return "", false
	default:
		s := e.String()
		if !strings.ContainsAny(s, " ()+-*/=><!&|;") {
			return cleanIdent(s), true
		}
		return "", false
	}
}

func extractColumnAndLiteral(left, right proc.Expr) (col string, val string, isLeftCol bool, ok bool) {
	if colName, isCol := extractColumnFromExpr(left); isCol {
		if lit, isLit := right.(*proc.LiteralExpr); isLit {
			return colName, lit.Val.String(), true, true
		}
		return colName, strings.Trim(right.String(), "'\""), true, true
	}
	if colName, isCol := extractColumnFromExpr(right); isCol {
		if lit, isLit := left.(*proc.LiteralExpr); isLit {
			return colName, lit.Val.String(), false, true
		}
		return colName, strings.Trim(left.String(), "'\""), false, true
	}
	return "", "", false, false
}

func isSameColumnEquality(left, right proc.Expr) bool {
	bLeft, ok1 := left.(*proc.BinaryExpr)
	bRight, ok2 := right.(*proc.BinaryExpr)
	if !ok1 || !ok2 {
		return false
	}
	if bLeft.Op != proc.TokenEq || bRight.Op != proc.TokenEq {
		return false
	}
	colLeft, _, _, okColLeft := extractColumnAndLiteral(bLeft.Left, bLeft.Right)
	colRight, _, _, okColRight := extractColumnAndLiteral(bRight.Left, bRight.Right)
	return okColLeft && okColRight && strings.EqualFold(colLeft, colRight)
}

func tokenToOpString(tok proc.TokenType) string {
	switch tok {
	case proc.TokenEq:
		return "="
	case proc.TokenNeq:
		return "!="
	case proc.TokenLt:
		return "<"
	case proc.TokenLte:
		return "<="
	case proc.TokenGt:
		return ">"
	case proc.TokenGte:
		return ">="
	case proc.TokenLike:
		return "LIKE"
	default:
		return "="
	}
}

func flipComparisonOp(op string) string {
	switch op {
	case "<":
		return ">"
	case "<=":
		return ">="
	case ">":
		return "<"
	case ">=":
		return "<="
	default:
		return op
	}
}

// EstimateExprSelectivity calculates selectivity recursively across a boolean expression tree (AND, OR, NOT, IN, BETWEEN).
func (ts *TableStats) EstimateExprSelectivity(expr proc.Expr) float64 {
	if ts == nil || ts.RowCount == 0 || expr == nil {
		return 0.33
	}

	minSel := 0.0001
	if ts.RowCount > 0 {
		minSel = 1.0 / float64(ts.RowCount)
	}

	switch e := expr.(type) {
	case *proc.BinaryExpr:
		switch e.Op {
		case proc.TokenAnd:
			if len(ts.MultiColumns) > 0 {
				conjuncts := flattenAndExpr(e)
				return ts.estimateAndConjunctsSelectivity(conjuncts, minSel)
			}
			selLeft := ts.EstimateExprSelectivity(e.Left)
			selRight := ts.EstimateExprSelectivity(e.Right)
			return math.Max(minSel, selLeft*selRight)

		case proc.TokenOr:
			selLeft := ts.EstimateExprSelectivity(e.Left)
			selRight := ts.EstimateExprSelectivity(e.Right)
			if isSameColumnEquality(e.Left, e.Right) {
				// Disjoint / mutually exclusive outcomes on the same column: P(A ∪ B) = P(A) + P(B)
				return math.Min(1.0, math.Max(minSel, selLeft+selRight))
			}
			// General probability union for independent predicates: P(A ∪ B) = P(A) + P(B) - P(A)*P(B)
			combined := selLeft + selRight - (selLeft * selRight)
			return math.Min(1.0, math.Max(minSel, combined))

		case proc.TokenEq, proc.TokenNeq, proc.TokenLt, proc.TokenLte, proc.TokenGt, proc.TokenGte:
			col, val, isLeftCol, ok := extractColumnAndLiteral(e.Left, e.Right)
			if !ok {
				return 0.1
			}
			op := tokenToOpString(e.Op)
			if !isLeftCol {
				op = flipComparisonOp(op)
			}
			return ts.EstimateSelectivity(col, op, val)

		case proc.TokenLike:
			return 0.05
		}

	case *proc.UnaryExpr:
		if e.Op == proc.TokenNot {
			inner := ts.EstimateExprSelectivity(e.Expr)
			return math.Max(minSel, math.Min(1.0, 1.0-inner))
		}

	case *proc.IsNullExpr:
		if col, ok := extractColumnFromExpr(e.Expr); ok {
			op := "IS NULL"
			if e.Not {
				op = "IS NOT NULL"
			}
			return ts.EstimateSelectivity(col, op, "")
		}
		return 0.1

	case *proc.BetweenExpr:
		if col, ok := extractColumnFromExpr(e.Expr); ok {
			if cs, found := ts.findColumnStats(col); found {
				lowVal := e.Low.String()
				highVal := e.High.String()
				highSel := cs.interpolateRange(highVal, true)
				lowSel := cs.interpolateRange(lowVal, true)
				rangeSel := math.Max(minSel, highSel-lowSel)
				if e.Not {
					rangeSel = math.Max(minSel, 1.0-rangeSel)
				}
				return math.Min(1.0, rangeSel)
			}
		}
		return 0.25

	case *proc.InExpr:
		if col, ok := extractColumnFromExpr(e.Expr); ok {
			if len(e.List) > 0 {
				var inSel float64
				if cs, found := ts.findColumnStats(col); found && cs.DistinctCount > 0 {
					inSel = math.Min(1.0, float64(len(e.List))/float64(cs.DistinctCount))
				} else {
					inSel = math.Min(0.5, float64(len(e.List))*0.05)
				}
				if e.Not {
					inSel = math.Max(minSel, 1.0-inSel)
				}
				return math.Max(minSel, inSel)
			}
			if e.Query != nil {
				return 0.20
			}
		}
		return 0.25

	case *proc.LikeExpr:
		sel := 0.05
		if e.Not {
			sel = 0.95
		}
		return sel

	case *proc.LiteralExpr:
		if e.Val.Type == storage.TypeBool {
			if e.Val.BVal {
				return 1.0
			}
			return 0.0
		}
	}

	return 0.33
}

func flattenAndExpr(expr proc.Expr) []proc.Expr {
	var list []proc.Expr
	var walk func(ex proc.Expr)
	walk = func(ex proc.Expr) {
		if b, ok := ex.(*proc.BinaryExpr); ok && b.Op == proc.TokenAnd {
			walk(b.Left)
			walk(b.Right)
			return
		}
		list = append(list, ex)
	}
	walk(expr)
	return list
}

func (ts *TableStats) estimateAndConjunctsSelectivity(conjuncts []proc.Expr, minSel float64) float64 {
	type eqItem struct {
		col string
		val string
		sel float64
		idx int
	}
	var eqItems []eqItem
	otherSels := make([]float64, 0, len(conjuncts))

	for i, c := range conjuncts {
		if b, ok := c.(*proc.BinaryExpr); ok && b.Op == proc.TokenEq {
			col, val, isLeftCol, matchOk := extractColumnAndLiteral(b.Left, b.Right)
			if matchOk {
				op := tokenToOpString(b.Op)
				if !isLeftCol {
					op = flipComparisonOp(op)
				}
				s := ts.EstimateSelectivity(col, op, val)
				cleanCol := strings.ToLower(cleanIdent(col))
				if idx := strings.Index(cleanCol, "."); idx != -1 {
					cleanCol = cleanCol[idx+1:]
				}
				eqItems = append(eqItems, eqItem{col: cleanCol, val: val, sel: s, idx: i})
				continue
			}
		}
		s := ts.EstimateExprSelectivity(c)
		otherSels = append(otherSels, s)
	}

	usedIndices := make(map[int]bool)
	totalSel := 1.0

	if len(eqItems) >= 2 {
		queryCols := make([]string, len(eqItems))
		for i, eq := range eqItems {
			queryCols[i] = eq.col
		}

		if bestStat, matchedCols := ts.FindBestMultiColumnStats(queryCols); bestStat != nil && len(matchedCols) >= 2 {
			colSels := make(map[string]float64)
			for _, eq := range eqItems {
				for _, mc := range matchedCols {
					if strings.EqualFold(eq.col, mc) && !usedIndices[eq.idx] {
						colSels[mc] = eq.sel
						usedIndices[eq.idx] = true
						break
					}
				}
			}
			jointSel := ts.EstimateMultiColumnSelectivity(bestStat, matchedCols, colSels)
			totalSel *= jointSel
		}
	}

	for _, eq := range eqItems {
		if !usedIndices[eq.idx] {
			totalSel *= eq.sel
		}
	}
	for _, s := range otherSels {
		totalSel *= s
	}

	return math.Min(1.0, math.Max(minSel, totalSel))
}

var (
	cboEqRe     = regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_\.]+)\s*(<=|>=|!=|<>|<|>|==|=|LIKE)\s*(.+)$`)
	cboIsNullRe = regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_\.]+)\s+IS\s+(NOT\s+)?NULL\s*$`)
)

// EstimateCompoundSelectivity calculates the combined selectivity across multiple predicates.
// It parses each predicate into an AST expression when possible, using boolean tree rules (AND, OR, NOT, IN, BETWEEN),
// and falls back to regex matching or heuristic defaults. It leverages MultiColumnStats for correlated equality conditions (C-5).
func (ts *TableStats) EstimateCompoundSelectivity(conds []string) float64 {
	if ts == nil || len(conds) == 0 {
		return 0.33
	}

	type parsedCond struct {
		col  string
		isEq bool
		sel  float64
	}

	parsed := make([]parsedCond, 0, len(conds))
	matchedAny := false

	for _, cond := range conds {
		cond = strings.TrimSpace(cond)
		if cond == "" || cond == "1=1" || cond == "1 = 1" {
			continue
		}

		// 1. Try AST parsing first for full boolean tree (OR, NOT, AND, BETWEEN, IN)
		parser := proc.NewParser(proc.NewLexer(cond))
		if expr := parser.ParseExpr(); expr != nil && len(parser.Errors()) == 0 {
			if b, ok := expr.(*proc.BinaryExpr); ok && b.Op == proc.TokenEq {
				col, val, isLeftCol, matchOk := extractColumnAndLiteral(b.Left, b.Right)
				if matchOk {
					op := tokenToOpString(b.Op)
					if !isLeftCol {
						op = flipComparisonOp(op)
					}
					cleanCol := strings.ToLower(cleanIdent(col))
					if idx := strings.Index(cleanCol, "."); idx != -1 {
						cleanCol = cleanCol[idx+1:]
					}
					sel := ts.EstimateSelectivity(col, op, val)
					parsed = append(parsed, parsedCond{col: cleanCol, isEq: true, sel: sel})
					matchedAny = true
					continue
				}
			}
			sel := ts.EstimateExprSelectivity(expr)
			parsed = append(parsed, parsedCond{sel: sel})
			matchedAny = true
			continue
		}

		// 2. Fallback to regex pattern matching for simple comparisons
		if m := cboEqRe.FindStringSubmatch(cond); len(m) == 4 {
			rawCol := m[1]
			if idx := strings.Index(rawCol, "."); idx != -1 {
				rawCol = rawCol[idx+1:]
			}
			colName := strings.ToLower(cleanIdent(rawCol))
			op := m[2]
			val := strings.TrimSpace(m[3])
			sel := ts.EstimateSelectivity(colName, op, val)
			isEq := (op == "=" || op == "==")
			parsed = append(parsed, parsedCond{col: colName, isEq: isEq, sel: sel})
			matchedAny = true
		} else if m := cboIsNullRe.FindStringSubmatch(cond); len(m) >= 2 {
			rawCol := m[1]
			if idx := strings.Index(rawCol, "."); idx != -1 {
				rawCol = rawCol[idx+1:]
			}
			colName := strings.ToLower(cleanIdent(rawCol))
			op := "IS NULL"
			if strings.TrimSpace(m[2]) != "" {
				op = "IS NOT NULL"
			}
			sel := ts.EstimateSelectivity(colName, op, "")
			parsed = append(parsed, parsedCond{col: colName, sel: sel})
			matchedAny = true
		} else {
			parsed = append(parsed, parsedCond{sel: 0.33})
			matchedAny = true
		}
	}

	if !matchedAny {
		return 0.33
	}

	totalSel := 1.0
	usedIndices := make(map[int]bool)

	// Evaluate multi-column statistics for correlated equality conditions (C-5)
	if len(ts.MultiColumns) > 0 {
		var eqIndices []int
		var queryCols []string
		for i, p := range parsed {
			if p.isEq && p.col != "" {
				eqIndices = append(eqIndices, i)
				queryCols = append(queryCols, p.col)
			}
		}

		if len(eqIndices) >= 2 {
			if bestStat, matchedCols := ts.FindBestMultiColumnStats(queryCols); bestStat != nil && len(matchedCols) >= 2 {
				colSels := make(map[string]float64)
				for _, idx := range eqIndices {
					p := parsed[idx]
					for _, mc := range matchedCols {
						if strings.EqualFold(p.col, mc) && !usedIndices[idx] {
							colSels[mc] = p.sel
							usedIndices[idx] = true
							break
						}
					}
				}
				jointSel := ts.EstimateMultiColumnSelectivity(bestStat, matchedCols, colSels)
				totalSel *= jointSel
			}
		}
	}

	for i, p := range parsed {
		if !usedIndices[i] {
			totalSel *= p.sel
		}
	}

	minSel := 0.0001
	if ts.RowCount > 0 {
		minSel = 1.0 / float64(ts.RowCount)
	}
	if totalSel < minSel {
		totalSel = minSel
	}
	if totalSel > 1.0 {
		totalSel = 1.0
	}
	return totalSel
}

// interpolateRange estimates selectivity for <, <=, >, >= using histogram buckets.
func (cs *ColumnStats) interpolateRange(valStr string, isLess bool) float64 {
	if len(cs.Histogram) < 2 {
		return 0.5 // Default midpoint
	}

	target := parseStorageVal(valStr, cs.Type)

	// If target is smaller than minimum histogram bound
	if target.Compare(cs.Histogram[0]) <= 0 {
		if isLess {
			return 0.01
		}
		return 0.99
	}

	// If target is greater than maximum histogram bound
	if target.Compare(cs.Histogram[len(cs.Histogram)-1]) >= 0 {
		if isLess {
			return 0.99
		}
		return 0.01
	}

	// Locate histogram bucket
	numBuckets := len(cs.Histogram) - 1
	bucketIdx := 0
	for i := 0; i < numBuckets; i++ {
		if target.Compare(cs.Histogram[i+1]) <= 0 {
			bucketIdx = i
			break
		}
	}

	// Linear interpolation within bucket
	bucketFraction := 1.0 / float64(numBuckets)
	bucketStart := float64(bucketIdx) * bucketFraction

	// Approximate interpolation offset within bucket
	innerFraction := 0.5
	if cs.Type == storage.TypeInt {
		low := cs.Histogram[bucketIdx].IVal
		high := cs.Histogram[bucketIdx+1].IVal
		if high > low {
			innerFraction = float64(target.IVal-low) / float64(high-low)
		}
	} else if cs.Type == storage.TypeFloat {
		low := cs.Histogram[bucketIdx].FVal
		high := cs.Histogram[bucketIdx+1].FVal
		if high > low {
			innerFraction = (target.FVal - low) / (high - low)
		}
	}

	if innerFraction < 0 {
		innerFraction = 0
	} else if innerFraction > 1 {
		innerFraction = 1
	}

	cumSelectivity := bucketStart + innerFraction*bucketFraction
	if isLess {
		return math.Max(0.01, math.Min(0.99, cumSelectivity))
	}
	return math.Max(0.01, math.Min(0.99, 1.0-cumSelectivity))
}

func parseStorageVal(s string, t storage.DataType) storage.Value {
	s = strings.Trim(strings.TrimSpace(s), "'\"")
	switch t {
	case storage.TypeInt:
		var n int64
		_, _ = fmt.Sscanf(s, "%d", &n)
		return storage.IntValue(n)
	case storage.TypeFloat:
		var f float64
		_, _ = fmt.Sscanf(s, "%f", &f)
		return storage.FloatValue(f)
	case storage.TypeBool:
		return storage.BoolValue(strings.EqualFold(s, "true") || s == "1")
	case storage.TypeVarChar:
		return storage.StringValue(s)
	default:
		return storage.StringValue(s)
	}
}

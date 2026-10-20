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
	SeqPageCost          = 1.0    // Cost of a sequential disk page fetch
	RandomPageCost       = 4.0    // Cost of a non-sequential disk page fetch
	CPUTupleCost         = 0.01   // Cost of processing a tuple
	CPUIndexTupleCost    = 0.005  // Cost of processing an index entry
	CPUOperatorCost      = 0.0025 // Cost of evaluating an operator or function
	RowsPerPageDefault   = 50     // Default average number of rows per disk page
	MaxMCVCount          = 20     // Maximum number of Most Common Values retained per column (C-7)
	MinMCVCountThreshold = 2      // Minimum occurrences required to qualify as an MCV unless NDV == 1
)

// MCVItem represents a Most Common Value and its observed frequency in the dataset (C-7).
type MCVItem struct {
	Value     storage.Value `json:"value"`
	Frequency float64       `json:"frequency"` // Fraction of non-null rows [0.0, 1.0]
	Count     int64         `json:"count"`     // Absolute occurrence count
}

// ColumnStats stores statistical distribution metadata for a single column.
type ColumnStats struct {
	Name          string           `json:"name"`
	Type          storage.DataType `json:"type"`
	NullCount     int64            `json:"null_count"`
	DistinctCount int64            `json:"distinct_count"` // NDV (Number of Distinct Values)
	MinVal        storage.Value    `json:"min_val"`
	MaxVal        storage.Value    `json:"max_val"`
	Histogram     []storage.Value  `json:"histogram,omitempty"` // Equi-depth quantile bucket bounds (non-MCV tail)
	MCVList       []MCVItem        `json:"mcv_list,omitempty"`  // Most Common Values and frequencies (C-7)
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

// SumMCVFrequency computes the cumulative frequency of all items in MCVList.
func (cs *ColumnStats) SumMCVFrequency() float64 {
	if cs == nil || len(cs.MCVList) == 0 {
		return 0.0
	}
	var total float64
	for _, m := range cs.MCVList {
		total += m.Frequency
	}
	if total > 1.0 {
		return 1.0
	}
	return total
}

// FindMCV finds an MCVItem matching the target storage value.
func (cs *ColumnStats) FindMCV(target storage.Value) (*MCVItem, bool) {
	if cs == nil || len(cs.MCVList) == 0 {
		return nil, false
	}
	for i := range cs.MCVList {
		if cs.MCVList[i].Value.Compare(target) == 0 {
			return &cs.MCVList[i], true
		}
	}
	return nil, false
}

// isMCVValue reports whether val is present in MCVList.
func (cs *ColumnStats) isMCVValue(val storage.Value) bool {
	_, found := cs.FindMCV(val)
	return found
}

// EstimateRangeSelectivity computes range selectivity combining MCV matches and non-MCV histogram interpolation (C-7).
func (cs *ColumnStats) EstimateRangeSelectivity(valStr string, isLess bool, orEqual bool) float64 {
	target := parseStorageVal(valStr, cs.Type)

	var mcvFreqSum float64
	for _, m := range cs.MCVList {
		cmp := m.Value.Compare(target)
		if isLess {
			if cmp < 0 || (orEqual && cmp == 0) {
				mcvFreqSum += m.Frequency
			}
		} else {
			if cmp > 0 || (orEqual && cmp == 0) {
				mcvFreqSum += m.Frequency
			}
		}
	}

	remWeight := 1.0 - cs.SumMCVFrequency()
	if remWeight <= 0.0001 || len(cs.Histogram) < 2 {
		if len(cs.MCVList) > 0 {
			return math.Min(1.0, math.Max(0.0001, mcvFreqSum))
		}
		return cs.interpolateRange(valStr, isLess)
	}

	histSel := cs.interpolateRange(valStr, isLess)
	total := mcvFreqSum + (remWeight * histSel)
	return math.Min(1.0, math.Max(0.0001, total))
}

// EstimateBetweenSelectivity computes BETWEEN selectivity combining MCVs and non-MCV histogram (C-7).
func (cs *ColumnStats) EstimateBetweenSelectivity(lowStr, highStr string) float64 {
	low := parseStorageVal(lowStr, cs.Type)
	high := parseStorageVal(highStr, cs.Type)
	if low.Compare(high) > 0 {
		low, high = high, low
	}

	var mcvFreqSum float64
	for _, m := range cs.MCVList {
		if m.Value.Compare(low) >= 0 && m.Value.Compare(high) <= 0 {
			mcvFreqSum += m.Frequency
		}
	}

	remWeight := 1.0 - cs.SumMCVFrequency()
	if remWeight <= 0.0001 || len(cs.Histogram) < 2 {
		if len(cs.MCVList) > 0 {
			return math.Min(1.0, math.Max(0.0001, mcvFreqSum))
		}
		highSel := cs.interpolateRange(highStr, true)
		lowSel := cs.interpolateRange(lowStr, true)
		return math.Max(0.0001, highSel-lowSel)
	}

	highSel := cs.interpolateRange(highStr, true)
	lowSel := cs.interpolateRange(lowStr, true)
	histSel := math.Max(0.0, highSel-lowSel)

	total := mcvFreqSum + (remWeight * histSel)
	return math.Min(1.0, math.Max(0.0001, total))
}

// EstimateInSelectivity calculates selectivity for an IN clause using MCV matching and remaining tail estimation (C-7).
func (cs *ColumnStats) EstimateInSelectivity(list []proc.Expr) float64 {
	if len(list) == 0 {
		return 0.0
	}
	var matchedMCVFreq float64
	unmatchedCount := 0

	for _, item := range list {
		valStr := item.String()
		target := parseStorageVal(valStr, cs.Type)
		if m, ok := cs.FindMCV(target); ok {
			matchedMCVFreq += m.Frequency
		} else {
			unmatchedCount++
		}
	}

	remFreq := 1.0 - cs.SumMCVFrequency()
	remDistinct := cs.DistinctCount - int64(len(cs.MCVList))
	var nonMCVSel float64
	if remDistinct > 0 && remFreq > 0 {
		nonMCVSel = float64(unmatchedCount) * (remFreq / float64(remDistinct))
	} else if cs.DistinctCount > 0 {
		nonMCVSel = float64(unmatchedCount) * (1.0 / float64(cs.DistinctCount))
	} else {
		nonMCVSel = float64(unmatchedCount) * 0.05
	}

	total := matchedMCVFreq + nonMCVSel
	return math.Min(1.0, math.Max(0.0001, total))
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

	// Compute NDV (Number of Distinct Values) and value counts
	type valCount struct {
		val   storage.Value
		count int64
	}
	groups := make([]valCount, 0, 64)
	var curVal = vals[0]
	var curCount int64 = 1
	for i := 1; i < len(vals); i++ {
		if vals[i].Equal(curVal) {
			curCount++
		} else {
			groups = append(groups, valCount{val: curVal, count: curCount})
			curVal = vals[i]
			curCount = 1
		}
	}
	groups = append(groups, valCount{val: curVal, count: curCount})
	cs.DistinctCount = int64(len(groups))

	// Extract Most Common Values (MCVs) (C-7)
	totalVals := float64(len(vals))
	if len(groups) == 1 {
		cs.MCVList = []MCVItem{
			{Value: groups[0].val, Frequency: 1.0, Count: groups[0].count},
		}
	} else {
		// Sort groups by count descending
		sortedGroups := make([]valCount, len(groups))
		copy(sortedGroups, groups)
		sort.Slice(sortedGroups, func(i, j int) bool {
			if sortedGroups[i].count != sortedGroups[j].count {
				return sortedGroups[i].count > sortedGroups[j].count
			}
			return sortedGroups[i].val.Compare(sortedGroups[j].val) < 0
		})

		avgFreq := 1.0 / float64(cs.DistinctCount)
		mcvCandidates := make([]MCVItem, 0, MaxMCVCount)

		for _, g := range sortedGroups {
			if len(mcvCandidates) >= MaxMCVCount {
				break
			}
			freq := float64(g.count) / totalVals
			// Qualify as MCV if count >= MinMCVCountThreshold AND:
			// - Either small cardinality (<= MaxMCVCount distinct values)
			// - Or frequency is higher than average uniform frequency
			// - Or count is significantly elevated (>= 1% of total)
			if g.count >= MinMCVCountThreshold {
				if len(groups) <= MaxMCVCount || freq >= avgFreq*1.2 || freq >= 0.01 {
					mcvCandidates = append(mcvCandidates, MCVItem{
						Value:     g.val,
						Frequency: freq,
						Count:     g.count,
					})
				}
			}
		}
		if len(mcvCandidates) > 0 {
			cs.MCVList = mcvCandidates
		}
	}

	// Filter out MCVs to build Equi-Depth Histogram on the non-MCV tail (C-7)
	var nonMCVVals []storage.Value
	if len(cs.MCVList) == 0 {
		nonMCVVals = vals
	} else if len(cs.MCVList) == len(groups) {
		nonMCVVals = vals
	} else {
		nonMCVVals = make([]storage.Value, 0, len(vals))
		for _, v := range vals {
			if !cs.isMCVValue(v) {
				nonMCVVals = append(nonMCVVals, v)
			}
		}
		if len(nonMCVVals) == 0 {
			nonMCVVals = vals
		}
	}

	// Construct Equi-Depth Histogram (up to 20 quantile buckets)
	numBuckets := 20
	if len(nonMCVVals) < numBuckets {
		numBuckets = len(nonMCVVals)
	}
	if numBuckets > 1 {
		cs.Histogram = make([]storage.Value, numBuckets)
		for b := 0; b < numBuckets; b++ {
			idx := int(float64(b) * float64(len(nonMCVVals)-1) / float64(numBuckets-1))
			cs.Histogram[b] = nonMCVVals[idx]
		}
	} else if len(nonMCVVals) > 0 {
		cs.Histogram = []storage.Value{nonMCVVals[0]}
	}

	return cs
}

// EstimateSelectivity estimates the fraction of rows [0.0, 1.0] matching a predicate on a column.
func (ts *TableStats) EstimateSelectivity(colName string, op string, valStr string) float64 {
	if ts == nil || ts.RowCount == 0 {
		return 0.33 // Default heuristic fallback when no stats exist
	}

	cs, exists := ts.findColumnStats(colName)
	if !exists || cs == nil {
		return 0.33
	}

	op = strings.ToUpper(strings.TrimSpace(op))
	nonNullFraction := 1.0 - (float64(cs.NullCount) / float64(ts.RowCount))
	if nonNullFraction < 0.01 {
		nonNullFraction = 0.01
	}

	minSel := 0.0001
	if ts.RowCount > 0 {
		minSel = 1.0 / float64(ts.RowCount)
	}

	switch op {
	case "IS NULL":
		return float64(cs.NullCount) / float64(ts.RowCount)
	case "IS NOT NULL":
		return nonNullFraction
	case "=", "==":
		target := parseStorageVal(valStr, cs.Type)
		if mcv, ok := cs.FindMCV(target); ok {
			return math.Min(1.0, math.Max(minSel, mcv.Frequency*nonNullFraction))
		}
		// Not in MCV: belongs to non-MCV tail
		remFreq := 1.0 - cs.SumMCVFrequency()
		remDistinct := cs.DistinctCount - int64(len(cs.MCVList))
		if remDistinct <= 0 {
			return minSel
		}
		sel := (remFreq / float64(remDistinct)) * nonNullFraction
		return math.Min(1.0, math.Max(minSel, sel))

	case "!=", "<>":
		target := parseStorageVal(valStr, cs.Type)
		if mcv, ok := cs.FindMCV(target); ok {
			sel := (1.0 - mcv.Frequency) * nonNullFraction
			return math.Min(1.0, math.Max(minSel, sel))
		}
		remFreq := 1.0 - cs.SumMCVFrequency()
		remDistinct := cs.DistinctCount - int64(len(cs.MCVList))
		if remDistinct <= 0 {
			return math.Min(1.0, math.Max(minSel, nonNullFraction))
		}
		eqSel := remFreq / float64(remDistinct)
		sel := (1.0 - eqSel) * nonNullFraction
		return math.Min(1.0, math.Max(minSel, sel))

	case "<", "<=":
		orEqual := (op == "<=")
		return cs.EstimateRangeSelectivity(valStr, true, orEqual) * nonNullFraction
	case ">", ">=":
		orEqual := (op == ">=")
		return cs.EstimateRangeSelectivity(valStr, false, orEqual) * nonNullFraction
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
				nonNullFraction := 1.0 - (float64(cs.NullCount) / float64(ts.RowCount))
				if nonNullFraction < 0.01 {
					nonNullFraction = 0.01
				}
				rangeSel := cs.EstimateBetweenSelectivity(lowVal, highVal) * nonNullFraction
				if e.Not {
					rangeSel = math.Max(minSel, nonNullFraction-rangeSel)
				}
				return math.Min(1.0, math.Max(minSel, rangeSel))
			}
		}
		return 0.25

	case *proc.InExpr:
		if col, ok := extractColumnFromExpr(e.Expr); ok {
			if len(e.List) > 0 {
				var inSel float64
				if cs, found := ts.findColumnStats(col); found {
					nonNullFraction := 1.0 - (float64(cs.NullCount) / float64(ts.RowCount))
					if nonNullFraction < 0.01 {
						nonNullFraction = 0.01
					}
					inSel = cs.EstimateInSelectivity(e.List) * nonNullFraction
				} else {
					inSel = math.Min(0.5, float64(len(e.List))*0.05)
				}
				if e.Not {
					inSel = math.Max(minSel, 1.0-inSel)
				}
				return math.Min(1.0, math.Max(minSel, inSel))
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
	case storage.TypeDecimal:
		return storage.DecimalValue(s)
	case storage.TypeVarChar:
		return storage.StringValue(s)
	default:
		return storage.StringValue(s)
	}
}

package planner

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

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

// TableStats stores table-level cardinality and column statistics.
type TableStats struct {
	TableName   string                  `json:"table_name"`
	RowCount    int64                   `json:"row_count"`
	PageCount   int64                   `json:"page_count"`
	Columns     map[string]*ColumnStats `json:"columns"`
	Partitions  []string                `json:"partitions,omitempty"`
	AnalyzedAt  time.Time               `json:"analyzed_at"`
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

var (
	cboEqRe     = regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_\.]+)\s*(<=|>=|!=|<>|<|>|==|=|LIKE)\s*(.+)$`)
	cboIsNullRe = regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_\.]+)\s+IS\s+(NOT\s+)?NULL\s*$`)
)

// EstimateCompoundSelectivity calculates the combined selectivity across multiple AND-predicates using statistical independence.
func (ts *TableStats) EstimateCompoundSelectivity(conds []string) float64 {
	if ts == nil || len(conds) == 0 {
		return 0.33
	}

	totalSel := 1.0
	matchedAny := false

	for _, cond := range conds {
		cond = strings.TrimSpace(cond)
		if cond == "" || cond == "1=1" || cond == "1 = 1" {
			continue
		}

		if m := cboEqRe.FindStringSubmatch(cond); len(m) == 4 {
			rawCol := m[1]
			if idx := strings.Index(rawCol, "."); idx != -1 {
				rawCol = rawCol[idx+1:]
			}
			colName := cleanIdent(rawCol)
			op := m[2]
			val := strings.TrimSpace(m[3])
			sel := ts.EstimateSelectivity(colName, op, val)
			totalSel *= sel
			matchedAny = true
		} else if m := cboIsNullRe.FindStringSubmatch(cond); len(m) >= 2 {
			rawCol := m[1]
			if idx := strings.Index(rawCol, "."); idx != -1 {
				rawCol = rawCol[idx+1:]
			}
			colName := cleanIdent(rawCol)
			op := "IS NULL"
			if strings.TrimSpace(m[2]) != "" {
				op = "IS NOT NULL"
			}
			sel := ts.EstimateSelectivity(colName, op, "")
			totalSel *= sel
			matchedAny = true
		} else {
			totalSel *= 0.33
			matchedAny = true
		}
	}

	if !matchedAny {
		return 0.33
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

package execution

import (
	"context"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"lionsql/pkg/planner"
	"lionsql/pkg/proc"
)

// WindowIterator evaluates SQL window functions over partitions of materialized rows.
type WindowIterator struct {
	child       Iterator
	windowDefs  []planner.WindowDef
	rows        []Tuple
	currIdx     int
	schema      []string
	colIndexMap *ColumnIndexMap
}

// NewWindowIterator creates a new Volcano iterator for window functions.
func NewWindowIterator(child Iterator, defs []planner.WindowDef) *WindowIterator {
	return &WindowIterator{
		child:      child,
		windowDefs: defs,
		currIdx:    0,
	}
}

func (w *WindowIterator) Open(ctx context.Context) error {
	if w.child == nil {
		return fmt.Errorf("window: child iterator is nil")
	}
	if err := w.child.Open(ctx); err != nil {
		return err
	}

	w.rows = nil
	for {
		t, err := w.child.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		w.rows = append(w.rows, t)
	}

	if len(w.rows) == 0 {
		w.schema = w.child.Schema()
		w.colIndexMap = w.child.ColIndexMap()
		return nil
	}

	currCols := append([]string(nil), w.child.Schema()...)
	currIndexMap := w.child.ColIndexMap()
	if currIndexMap == nil {
		currIndexMap = NewColumnIndexMapFromCols(currCols)
	}

	// Process each window definition and augment rows with the computed values
	for _, winDef := range w.windowDefs {
		winValues := make([]any, len(w.rows))

		// 1. Group rows into partitions: map of partitionKey -> []int (row indices)
		partitions := make(map[string][]int)
		var partitionOrder []string

		for rowIdx, t := range w.rows {
			pKey := ""
			if len(winDef.PartitionKeys) > 0 {
				var b strings.Builder
				for _, pCol := range winDef.PartitionKeys {
					val := t.Get(pCol)
					fmt.Fprintf(&b, "%v\x1f", val)
				}
				pKey = b.String()
			}
			if _, exists := partitions[pKey]; !exists {
				partitionOrder = append(partitionOrder, pKey)
			}
			partitions[pKey] = append(partitions[pKey], rowIdx)
		}

		// 2. Sort within each partition and compute the window function
		for _, pKey := range partitionOrder {
			indices := partitions[pKey]

			if len(winDef.OrderByKeys) > 0 {
				sort.SliceStable(indices, func(i, j int) bool {
					ti := w.rows[indices[i]]
					tj := w.rows[indices[j]]
					for _, ord := range winDef.OrderByKeys {
						vi := ti.Get(ord.Column)
						vj := tj.Get(ord.Column)
						cmp := CompareValues(vi, vj)
						if cmp == 0 {
							continue
						}
						if strings.EqualFold(ord.Direction, "DESC") {
							return cmp > 0
						}
						return cmp < 0
					}
					return false
				})
			}

			w.computeWindowFunction(winDef, indices, winValues)
		}

		// 3. Augment schema and each row with the computed window column
		newColName := winDef.Alias
		if newColName == "" {
			newColName = winDef.RawExpr
		}
		currCols = append(currCols, newColName)

		newIndexMap := &ColumnIndexMap{
			indices: make(map[string]int, len(currIndexMap.indices)+4),
			cols:    currCols,
		}
		for k, v := range currIndexMap.indices {
			newIndexMap.indices[k] = v
		}
		newColIdx := len(currCols) - 1
		newIndexMap.indices[newColName] = newColIdx
		newIndexMap.indices[strings.ToLower(newColName)] = newColIdx
		if winDef.Alias != "" {
			newIndexMap.indices[winDef.Alias] = newColIdx
			newIndexMap.indices[strings.ToLower(winDef.Alias)] = newColIdx
		}
		if winDef.RawExpr != "" {
			newIndexMap.indices[winDef.RawExpr] = newColIdx
			newIndexMap.indices[strings.ToLower(winDef.RawExpr)] = newColIdx
		}

		for i := range w.rows {
			w.rows[i].Values = append(w.rows[i].Values, winValues[i])
			w.rows[i].Index = newIndexMap
		}
		currIndexMap = newIndexMap
	}

	w.schema = currCols
	w.colIndexMap = currIndexMap
	w.currIdx = 0
	return nil
}

func (w *WindowIterator) computeWindowFunction(winDef planner.WindowDef, indices []int, winValues []any) {
	fn := strings.ToUpper(winDef.Function)
	switch fn {
	case "ROW_NUMBER":
		for rank, rowIdx := range indices {
			winValues[rowIdx] = int64(rank + 1)
		}

	case "RANK":
		currentRank := int64(1)
		for i, rowIdx := range indices {
			if i > 0 {
				prevIdx := indices[i-1]
				if !sameSortKeys(w.rows[rowIdx], w.rows[prevIdx], winDef.OrderByKeys) {
					currentRank = int64(i + 1)
				}
			}
			winValues[rowIdx] = currentRank
		}

	case "DENSE_RANK":
		currentRank := int64(1)
		for i, rowIdx := range indices {
			if i > 0 {
				prevIdx := indices[i-1]
				if !sameSortKeys(w.rows[rowIdx], w.rows[prevIdx], winDef.OrderByKeys) {
					currentRank++
				}
			}
			winValues[rowIdx] = currentRank
		}

	case "LEAD":
		targetCol := ""
		offset := 1
		var defVal any = nil
		if len(winDef.Args) > 0 {
			targetCol = winDef.Args[0]
		}
		if len(winDef.Args) > 1 {
			if n, err := strconv.Atoi(winDef.Args[1]); err == nil {
				offset = n
			}
		}
		if len(winDef.Args) > 2 {
			defVal = parseLiteralValue(winDef.Args[2])
		}

		for i, rowIdx := range indices {
			targetI := i + offset
			if targetI >= 0 && targetI < len(indices) {
				targetRowIdx := indices[targetI]
				winValues[rowIdx] = w.rows[targetRowIdx].Get(targetCol)
			} else {
				winValues[rowIdx] = defVal
			}
		}

	case "LAG":
		targetCol := ""
		offset := 1
		var defVal any = nil
		if len(winDef.Args) > 0 {
			targetCol = winDef.Args[0]
		}
		if len(winDef.Args) > 1 {
			if n, err := strconv.Atoi(winDef.Args[1]); err == nil {
				offset = n
			}
		}
		if len(winDef.Args) > 2 {
			defVal = parseLiteralValue(winDef.Args[2])
		}

		for i, rowIdx := range indices {
			targetI := i - offset
			if targetI >= 0 && targetI < len(indices) {
				targetRowIdx := indices[targetI]
				winValues[rowIdx] = w.rows[targetRowIdx].Get(targetCol)
			} else {
				winValues[rowIdx] = defVal
			}
		}

	case "SUM", "COUNT", "AVG", "MIN", "MAX":
		w.computeWindowAggregate(winDef, indices, winValues)

	case "PERCENTILE_CONT", "PERCENTILE_DISC", "APPROX_PERCENTILE", "MEDIAN":
		w.computeWindowPercentile(winDef, indices, winValues)
	}
}

func parseWindowExpr(col string) proc.Expr {
	if col == "" || col == "*" || !strings.ContainsAny(col, " +-*/%()->") {
		return nil
	}
	p := proc.NewParser(proc.NewLexer(col))
	if expr := p.ParseExpr(); expr != nil && len(p.Errors()) == 0 {
		return expr
	}
	return nil
}

func getValFromRow(t Tuple, col string, parsed proc.Expr) any {
	if val, ok := t.GetOK(col); ok {
		return val
	}
	if parsed != nil {
		if val, ok := EvalTupleExpr(t, parsed); ok {
			return val
		}
	}
	val, _ := lookupVal(t, col)
	return val
}

func (w *WindowIterator) computeWindowPercentile(winDef planner.WindowDef, indices []int, winValues []any) {
	fn := strings.ToUpper(winDef.Function)
	targetCol := ""
	percentile := 0.5

	if len(winDef.Args) > 0 {
		targetCol = winDef.Args[0]
	}
	parsedExpr := parseWindowExpr(targetCol)
	if len(winDef.Args) > 1 {
		if p, err := strconv.ParseFloat(winDef.Args[1], 64); err == nil {
			percentile = p
		}
	}

	var vals []float64
	for _, rowIdx := range indices {
		if v := getValFromRow(w.rows[rowIdx], targetCol, parsedExpr); v != nil {
			if f, ok := toFloat64(v); ok {
				vals = append(vals, f)
			}
		}
	}

	var computedVal any
	if len(vals) == 0 {
		computedVal = nil
	} else {
		sort.Float64s(vals)
		var res float64
		switch fn {
		case "PERCENTILE_DISC":
			res = ComputePercentileDisc(vals, percentile)
		default: // PERCENTILE_CONT, APPROX_PERCENTILE, MEDIAN
			res = ComputePercentileCont(vals, percentile)
		}
		if res == math.Trunc(res) {
			computedVal = int64(res)
		} else {
			computedVal = res
		}
	}

	for _, rowIdx := range indices {
		winValues[rowIdx] = computedVal
	}
}

func (w *WindowIterator) computeWindowAggregate(winDef planner.WindowDef, indices []int, winValues []any) {
	fn := strings.ToUpper(winDef.Function)
	targetCol := ""
	if len(winDef.Args) > 0 {
		targetCol = winDef.Args[0]
	}
	parsedExpr := parseWindowExpr(targetCol)

	frame := winDef.Frame
	if frame == nil && len(winDef.OrderByKeys) > 0 {
		// ANSI SQL default with ORDER BY: RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
		frame = &planner.WindowFrame{
			Unit:  planner.FrameRange,
			Start: planner.FrameBound{Type: planner.FrameUnboundedPreceding},
			End:   planner.FrameBound{Type: planner.FrameCurrentRow},
		}
	}

	if frame == nil {
		// Fast-path: Entire partition computation (no frame / no ORDER BY)
		switch fn {
		case "COUNT":
			cnt := int64(0)
			for _, rowIdx := range indices {
				if targetCol == "" || targetCol == "*" || getValFromRow(w.rows[rowIdx], targetCol, parsedExpr) != nil {
					cnt++
				}
			}
			for _, rowIdx := range indices {
				winValues[rowIdx] = cnt
			}
		case "SUM":
			sum := float64(0)
			hasVal := false
			for _, rowIdx := range indices {
				if v := getValFromRow(w.rows[rowIdx], targetCol, parsedExpr); v != nil {
					if f, ok := toFloat64(v); ok {
						sum += f
						hasVal = true
					}
				}
			}
			for _, rowIdx := range indices {
				if hasVal {
					winValues[rowIdx] = sum
				} else {
					winValues[rowIdx] = nil
				}
			}
		case "AVG":
			sum := float64(0)
			count := 0
			for _, rowIdx := range indices {
				if v := getValFromRow(w.rows[rowIdx], targetCol, parsedExpr); v != nil {
					if f, ok := toFloat64(v); ok {
						sum += f
						count++
					}
				}
			}
			var avg any = nil
			if count > 0 {
				avg = sum / float64(count)
			}
			for _, rowIdx := range indices {
				winValues[rowIdx] = avg
			}
		case "MIN":
			var minVal any = nil
			for _, rowIdx := range indices {
				v := getValFromRow(w.rows[rowIdx], targetCol, parsedExpr)
				if v != nil {
					if minVal == nil || CompareValues(v, minVal) < 0 {
						minVal = v
					}
				}
			}
			for _, rowIdx := range indices {
				winValues[rowIdx] = minVal
			}
		case "MAX":
			var maxVal any = nil
			for _, rowIdx := range indices {
				v := getValFromRow(w.rows[rowIdx], targetCol, parsedExpr)
				if v != nil {
					if maxVal == nil || CompareValues(v, maxVal) > 0 {
						maxVal = v
					}
				}
			}
			for _, rowIdx := range indices {
				winValues[rowIdx] = maxVal
			}
		}
		return
	}

	// Frame-based sliding window computation
	total := len(indices)
	for i := 0; i < total; i++ {
		rowIdx := indices[i]
		startK, endK := getFrameBounds(frame, i, total, w.rows, indices, winDef.OrderByKeys)
		if endK < startK {
			if fn == "COUNT" {
				winValues[rowIdx] = int64(0)
			} else {
				winValues[rowIdx] = nil
			}
			continue
		}

		switch fn {
		case "COUNT":
			cnt := int64(0)
			for k := startK; k <= endK; k++ {
				kRowIdx := indices[k]
				if targetCol == "" || targetCol == "*" || getValFromRow(w.rows[kRowIdx], targetCol, parsedExpr) != nil {
					cnt++
				}
			}
			winValues[rowIdx] = cnt

		case "SUM":
			sum := float64(0)
			hasVal := false
			for k := startK; k <= endK; k++ {
				kRowIdx := indices[k]
				if v := getValFromRow(w.rows[kRowIdx], targetCol, parsedExpr); v != nil {
					if f, ok := toFloat64(v); ok {
						sum += f
						hasVal = true
					}
				}
			}
			if hasVal {
				winValues[rowIdx] = sum
			} else {
				winValues[rowIdx] = nil
			}

		case "AVG":
			sum := float64(0)
			count := 0
			for k := startK; k <= endK; k++ {
				kRowIdx := indices[k]
				if v := getValFromRow(w.rows[kRowIdx], targetCol, parsedExpr); v != nil {
					if f, ok := toFloat64(v); ok {
						sum += f
						count++
					}
				}
			}
			if count > 0 {
				winValues[rowIdx] = sum / float64(count)
			} else {
				winValues[rowIdx] = nil
			}

		case "MIN":
			var minVal any = nil
			for k := startK; k <= endK; k++ {
				kRowIdx := indices[k]
				v := getValFromRow(w.rows[kRowIdx], targetCol, parsedExpr)
				if v != nil {
					if minVal == nil || CompareValues(v, minVal) < 0 {
						minVal = v
					}
				}
			}
			winValues[rowIdx] = minVal

		case "MAX":
			var maxVal any = nil
			for k := startK; k <= endK; k++ {
				kRowIdx := indices[k]
				v := getValFromRow(w.rows[kRowIdx], targetCol, parsedExpr)
				if v != nil {
					if maxVal == nil || CompareValues(v, maxVal) > 0 {
						maxVal = v
					}
				}
			}
			winValues[rowIdx] = maxVal
		}
	}
}

func getFrameBounds(frame *planner.WindowFrame, i, total int, rows []Tuple, indices []int, orderKeys []planner.SortKeyDef) (int, int) {
	if frame == nil {
		return 0, total - 1
	}

	startIdx := 0
	switch frame.Start.Type {
	case planner.FrameUnboundedPreceding:
		startIdx = 0
	case planner.FramePreceding:
		startIdx = i - frame.Start.Offset
		if startIdx < 0 {
			startIdx = 0
		}
	case planner.FrameCurrentRow:
		startIdx = i
	case planner.FrameFollowing:
		startIdx = i + frame.Start.Offset
		if startIdx >= total {
			startIdx = total
		}
	case planner.FrameUnboundedFollowing:
		startIdx = total - 1
	}

	endIdx := total - 1
	switch frame.End.Type {
	case planner.FrameCurrentRow:
		endIdx = i
	case planner.FrameUnboundedFollowing:
		endIdx = total - 1
	case planner.FrameFollowing:
		endIdx = i + frame.End.Offset
		if endIdx >= total {
			endIdx = total - 1
		}
	case planner.FramePreceding:
		endIdx = i - frame.End.Offset
		if endIdx < 0 {
			endIdx = -1
		}
	case planner.FrameUnboundedPreceding:
		endIdx = 0
	}

	// SQL standard RANGE mode: expand peer rows having identical ORDER BY values
	if frame.Unit == planner.FrameRange && len(orderKeys) > 0 && len(rows) > 0 && len(indices) == total && i >= 0 && i < total {
		if frame.Start.Type == planner.FrameCurrentRow {
			for startIdx > 0 && sameSortKeys(rows[indices[startIdx-1]], rows[indices[i]], orderKeys) {
				startIdx--
			}
		}
		if frame.End.Type == planner.FrameCurrentRow {
			for endIdx+1 < total && sameSortKeys(rows[indices[endIdx+1]], rows[indices[i]], orderKeys) {
				endIdx++
			}
		}
	}

	return startIdx, endIdx
}

func sameSortKeys(t1, t2 Tuple, orderKeys []planner.SortKeyDef) bool {
	for _, ord := range orderKeys {
		v1 := t1.Get(ord.Column)
		v2 := t2.Get(ord.Column)
		if CompareValues(v1, v2) != 0 {
			return false
		}
	}
	return true
}

func (w *WindowIterator) Next(ctx context.Context) (Tuple, error) {
	if w.currIdx >= len(w.rows) {
		return Tuple{}, io.EOF
	}
	t := w.rows[w.currIdx]
	w.currIdx++
	return t, nil
}

func (w *WindowIterator) Close() error {
	if w.child != nil {
		return w.child.Close()
	}
	return nil
}

func (w *WindowIterator) Schema() []string {
	return w.schema
}

func (w *WindowIterator) ColIndexMap() *ColumnIndexMap {
	return w.colIndexMap
}

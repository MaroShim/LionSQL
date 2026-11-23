package execution

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"lionsql/pkg/planner"
	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
)

// ErrWindowWorkMemExceeded is returned when the window function materialization exceeds work_mem.
var ErrWindowWorkMemExceeded = errors.New("window function memory budget exceeded")

// WindowIterator evaluates SQL window functions over partitions of materialized rows.
type WindowIterator struct {
	child       Iterator
	windowDefs  []planner.WindowDef
	rows        []Tuple
	currIdx     int
	schema      []string
	colIndexMap *ColumnIndexMap
	workMem     int64
	accumMemory int64
}

// NewWindowIterator creates a new Volcano iterator for window functions.
func NewWindowIterator(child Iterator, defs []planner.WindowDef) *WindowIterator {
	return &WindowIterator{
		child:      child,
		windowDefs: defs,
		currIdx:    0,
		workMem:    DefaultWorkMem,
	}
}

// SetWorkMem sets the maximum memory budget for materializing window rows ([RULE:BoundedBuffers]).
func (w *WindowIterator) SetWorkMem(bytes int64) {
	if bytes > 0 {
		w.workMem = bytes
	}
}

func (w *WindowIterator) Open(ctx context.Context) (retErr error) {
	if w.child == nil {
		return fmt.Errorf("window: child iterator is nil")
	}
	if err := w.child.Open(ctx); err != nil {
		return err
	}
	defer func() {
		if retErr != nil && w.child != nil {
			_ = w.child.Close()
		}
	}()

	w.rows = nil
	w.accumMemory = 0
	for {
		t, err := w.child.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		tSize := EstimateTupleSize(t)
		if w.workMem > 0 && w.accumMemory+tSize > w.workMem {
			_ = w.child.Close()
			return fmt.Errorf("%w: %s > work_mem (%s)",
				ErrWindowWorkMemExceeded, FormatMemSize(w.accumMemory+tSize), FormatMemSize(w.workMem))
		}
		w.accumMemory += tSize
		w.rows = append(w.rows, t)
	}

	currCols := append([]string(nil), w.child.Schema()...)
	currIndexMap := w.child.ColIndexMap()
	if currIndexMap == nil {
		currIndexMap = NewColumnIndexMapFromCols(currCols)
	}

	if len(w.rows) == 0 {
		for _, winDef := range w.windowDefs {
			if err := w.validateWindowDef(winDef, currIndexMap); err != nil {
				_ = w.child.Close()
				return err
			}
		}
		w.schema = w.child.Schema()
		w.colIndexMap = w.child.ColIndexMap()
		return nil
	}

	// Process each window definition and augment rows with the computed values
	for _, winDef := range w.windowDefs {
		// Validate partition, order by, and argument columns exist in the child schema
		if err := w.validateWindowDef(winDef, currIndexMap); err != nil {
			_ = w.child.Close()
			return err
		}

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
						if vi == nil && vj == nil {
							continue
						}
						if (vi == nil || vj == nil) && ord.NullsFirst != nil {
							if *ord.NullsFirst {
								return vi == nil
							}
							return vi != nil
						}
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

func (w *WindowIterator) validateWindowDef(winDef planner.WindowDef, indexMap *ColumnIndexMap) error {
	fn := strings.ToUpper(winDef.Function)
	// Check if window function is recognized
	switch fn {
	case "ROW_NUMBER", "RANK", "DENSE_RANK", "NTILE", "LAG", "LEAD", "PERCENT_RANK", "CUME_DIST",
		"SUM", "COUNT", "AVG", "MIN", "MAX", "FIRST_VALUE", "LAST_VALUE",
		"PERCENTILE_CONT", "PERCENTILE_DISC", "APPROX_PERCENTILE", "MEDIAN":
		// supported
	default:
		return fmt.Errorf("function %s does not exist or is unsupported as a window function", strings.ToLower(fn))
	}

	// Validate function arity
	if err := proc.ValidateBuiltinArity(fn, len(winDef.Args)); err != nil {
		return err
	}
	// T-SQL requires ORDER BY in OVER clause for ranking, offset and distribution window functions
	switch fn {
	case "ROW_NUMBER", "RANK", "DENSE_RANK", "NTILE", "LAG", "LEAD", "PERCENT_RANK", "CUME_DIST":
		if len(winDef.OrderByKeys) == 0 {
			return fmt.Errorf("The function %q must have an OVER clause with ORDER BY.", fn)
		}
	}
	if fn == "NTILE" && len(winDef.Args) > 0 {
		n, err := strconv.ParseInt(strings.TrimSpace(winDef.Args[0]), 10, 64)
		if err != nil || n <= 0 {
			return fmt.Errorf("ERROR 22003: Msg 7812, Level 16, State 1: The argument of NTILE must be greater than zero.")
		}
	}
	if (fn == "PERCENTILE_CONT" || fn == "PERCENTILE_DISC") && len(winDef.Args) > 0 {
		// Args[0] is percentile fraction if 1 arg or within group, or Args[1]
		targetFractionStr := winDef.Args[0]
		if len(winDef.Args) > 1 {
			targetFractionStr = winDef.Args[1]
		}
		if p, err := strconv.ParseFloat(strings.TrimSpace(targetFractionStr), 64); err == nil {
			if p < 0.0 || p > 1.0 {
				return fmt.Errorf("ERROR 22003: Msg 8704, Level 16, State 1: Percentile value %v must be between 0.0 and 1.0.", p)
			}
		}
	}
	if (fn == "LAG" || fn == "LEAD") && len(winDef.Args) > 1 {
		argStr := strings.TrimSpace(winDef.Args[1])
		argStr = strings.ReplaceAll(argStr, " ", "")
		if offset, err := strconv.ParseInt(argStr, 10, 64); err == nil && offset < 0 {
			return fmt.Errorf("Msg 8730, Level 16, State 1: Offset argument of LAG or LEAD function must be a non-negative integer.")
		}
	}
	// Validate window frame bounds if frame is present
	if f := winDef.Frame; f != nil {
		if f.Start.Type == planner.FrameUnboundedFollowing {
			return fmt.Errorf("Window frame start cannot be UNBOUNDED FOLLOWING.")
		}
		if f.End.Type == planner.FrameUnboundedPreceding {
			return fmt.Errorf("Window frame end cannot be UNBOUNDED PRECEDING.")
		}
		if f.Start.Type == planner.FrameFollowing && f.End.Type == planner.FramePreceding {
			return fmt.Errorf("Window frame start cannot be FOLLOWING if frame end is PRECEDING.")
		}
		if f.Start.Type == planner.FrameFollowing && f.End.Type == planner.FrameFollowing {
			if f.Start.Offset > f.End.Offset {
				return fmt.Errorf("Window frame start offset cannot exceed frame end offset.")
			}
		}
		if f.Start.Type == planner.FramePreceding && f.End.Type == planner.FramePreceding {
			if f.Start.Offset < f.End.Offset {
				return fmt.Errorf("Window frame start offset cannot be smaller than frame end offset.")
			}
		}
	}
	// Validate partition columns
	for _, pCol := range winDef.PartitionKeys {
		cleanPCol := strings.TrimSpace(cleanIdentifierParts(pCol))
		if cleanPCol == "" || strings.ContainsAny(cleanPCol, " +-*/%|()") {
			continue
		}
		if _, ok := indexMap.Index(cleanPCol); !ok {
			bare := cleanPCol
			if dot := strings.Index(bare, "."); dot != -1 {
				bare = bare[dot+1:]
			}
			if _, ok2 := indexMap.Index(bare); !ok2 {
				return fmt.Errorf("column %q does not exist", pCol)
			}
		}
	}
	// Validate order by columns
	for _, ord := range winDef.OrderByKeys {
		cleanOrd := strings.TrimSpace(cleanIdentifierParts(ord.Column))
		if cleanOrd == "" || strings.ContainsAny(cleanOrd, " +-*/%|()") {
			continue
		}
		if _, err := strconv.ParseInt(cleanOrd, 10, 64); err == nil {
			continue
		}
		if _, ok := indexMap.Index(cleanOrd); !ok {
			bare := cleanOrd
			if dot := strings.Index(bare, "."); dot != -1 {
				bare = bare[dot+1:]
			}
			if _, ok2 := indexMap.Index(bare); !ok2 {
				return fmt.Errorf("column %q does not exist", ord.Column)
			}
		}
	}
	// Validate function arguments
	for _, arg := range winDef.Args {
		cleanArg := strings.TrimSpace(cleanIdentifierParts(arg))
		if cleanArg == "" || cleanArg == "*" || strings.HasPrefix(cleanArg, "'") {
			continue
		}
		if _, err := strconv.ParseFloat(cleanArg, 64); err == nil {
			continue
		}
		refs := planner.ExtractColumnRefs(arg)
		if len(refs) == 0 {
			if !strings.ContainsAny(cleanArg, " +-*/%|()") {
				if _, ok := indexMap.Index(cleanArg); !ok {
					bare := cleanArg
					if dot := strings.Index(bare, "."); dot != -1 {
						bare = bare[dot+1:]
					}
					if _, ok2 := indexMap.Index(bare); !ok2 {
						return fmt.Errorf("column %q does not exist", arg)
					}
				}
			}
		} else {
			for _, ref := range refs {
				col := ref.Column
				if _, ok := indexMap.Index(col); !ok {
					qualified := col
					if ref.TableOrAlias != "" {
						qualified = ref.TableOrAlias + "." + col
					}
					if _, ok2 := indexMap.Index(qualified); !ok2 {
						if sqlutil.IsSQLTypeName(col) {
							continue
						}
						return fmt.Errorf("column %q does not exist", col)
					}
				}
			}
		}
	}
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

	case "NTILE":
		buckets := int64(1)
		if len(winDef.Args) > 0 {
			if n, err := strconv.ParseInt(strings.TrimSpace(winDef.Args[0]), 10, 64); err == nil && n > 0 {
				buckets = n
			}
		}
		totalRows := int64(len(indices))
		base := totalRows / buckets
		rem := totalRows % buckets
		for i, rowIdx := range indices {
			idx := int64(i)
			var b int64
			if idx < rem*(base+1) {
				b = 1 + idx/(base+1)
			} else {
				idxPrime := idx - rem*(base+1)
				if base > 0 {
					b = 1 + rem + idxPrime/base
				} else {
					b = buckets
				}
			}
			winValues[rowIdx] = b
		}

	case "PERCENT_RANK":
		n := len(indices)
		if n <= 1 {
			for _, rowIdx := range indices {
				winValues[rowIdx] = float64(0)
			}
		} else {
			currentRank := int64(1)
			for i, rowIdx := range indices {
				if i > 0 {
					prevIdx := indices[i-1]
					if !sameSortKeys(w.rows[rowIdx], w.rows[prevIdx], winDef.OrderByKeys) {
						currentRank = int64(i + 1)
					}
				}
				pct := float64(currentRank-1) / float64(n-1)
				winValues[rowIdx] = pct
			}
		}

	case "CUME_DIST":
		n := len(indices)
		for i := 0; i < n; {
			j := i
			for j+1 < n && sameSortKeys(w.rows[indices[j+1]], w.rows[indices[i]], winDef.OrderByKeys) {
				j++
			}
			cume := float64(j+1) / float64(n)
			for k := i; k <= j; k++ {
				winValues[indices[k]] = cume
			}
			i = j + 1
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

	case "SUM", "COUNT", "AVG", "MIN", "MAX", "FIRST_VALUE", "LAST_VALUE":
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
		case "FIRST_VALUE":
			firstVal := getValFromRow(w.rows[indices[0]], targetCol, parsedExpr)
			for _, rowIdx := range indices {
				winValues[rowIdx] = firstVal
			}
		case "LAST_VALUE":
			lastVal := getValFromRow(w.rows[indices[len(indices)-1]], targetCol, parsedExpr)
			for _, rowIdx := range indices {
				winValues[rowIdx] = lastVal
			}
		}
		return
	}

	// Frame-based sliding window computation
	total := len(indices)
	for i := 0; i < total; i++ {
		rowIdx := indices[i]
		startK, endK := getFrameBounds(frame, i, total, w.rows, indices, winDef.OrderByKeys)
		if startK >= total || endK < 0 || endK < startK {
			if fn == "COUNT" {
				winValues[rowIdx] = int64(0)
			} else {
				winValues[rowIdx] = nil
			}
			continue
		}
		if startK < 0 {
			startK = 0
		}
		if endK >= total {
			endK = total - 1
		}

		isExcluded := func(k int) bool {
			return frame != nil && frame.ExcludeCurrentRow && k == i
		}

		switch fn {
		case "COUNT":
			cnt := int64(0)
			for k := startK; k <= endK; k++ {
				if isExcluded(k) {
					continue
				}
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
				if isExcluded(k) {
					continue
				}
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
				if isExcluded(k) {
					continue
				}
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
				if isExcluded(k) {
					continue
				}
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
				if isExcluded(k) {
					continue
				}
				kRowIdx := indices[k]
				v := getValFromRow(w.rows[kRowIdx], targetCol, parsedExpr)
				if v != nil {
					if maxVal == nil || CompareValues(v, maxVal) > 0 {
						maxVal = v
					}
				}
			}
			winValues[rowIdx] = maxVal

		case "FIRST_VALUE":
			firstK := startK
			for firstK <= endK && isExcluded(firstK) {
				firstK++
			}
			if firstK <= endK {
				winValues[rowIdx] = getValFromRow(w.rows[indices[firstK]], targetCol, parsedExpr)
			} else {
				winValues[rowIdx] = nil
			}

		case "LAST_VALUE":
			lastK := endK
			for lastK >= startK && isExcluded(lastK) {
				lastK--
			}
			if lastK >= startK {
				winValues[rowIdx] = getValFromRow(w.rows[indices[lastK]], targetCol, parsedExpr)
			} else {
				winValues[rowIdx] = nil
			}
		}
	}
}

func getFrameBounds(frame *planner.WindowFrame, i, total int, rows []Tuple, indices []int, orderKeys []planner.SortKeyDef) (int, int) {
	if total <= 0 {
		return 0, -1
	}
	if frame == nil {
		return 0, total - 1
	}

	startIdx := 0
	switch frame.Start.Type {
	case planner.FrameUnboundedPreceding:
		startIdx = 0
	case planner.FramePreceding:
		startIdx = i - frame.Start.Offset
	case planner.FrameCurrentRow:
		startIdx = i
	case planner.FrameFollowing:
		startIdx = i + frame.Start.Offset
	case planner.FrameUnboundedFollowing:
		startIdx = total - 1
	}
	if startIdx < 0 {
		startIdx = 0
	}
	if startIdx >= total {
		startIdx = total
	}

	endIdx := total - 1
	switch frame.End.Type {
	case planner.FrameCurrentRow:
		endIdx = i
	case planner.FrameUnboundedFollowing:
		endIdx = total - 1
	case planner.FrameFollowing:
		endIdx = i + frame.End.Offset
	case planner.FramePreceding:
		endIdx = i - frame.End.Offset
	case planner.FrameUnboundedPreceding:
		endIdx = 0
	}
	if endIdx < 0 {
		endIdx = -1
	}
	if endIdx >= total {
		endIdx = total - 1
	}

	// SQL standard RANGE mode: expand peer rows having identical ORDER BY values
	if frame.Unit == planner.FrameRange && len(orderKeys) > 0 && len(rows) > 0 && len(indices) == total && i >= 0 && i < total {
		if frame.Start.Type == planner.FrameCurrentRow && startIdx >= 0 && startIdx <= total {
			for startIdx > 0 && sameSortKeys(rows[indices[startIdx-1]], rows[indices[i]], orderKeys) {
				startIdx--
			}
		}
		if frame.End.Type == planner.FrameCurrentRow && endIdx >= -1 && endIdx < total {
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
	w.rows = nil
	w.accumMemory = 0
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

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

	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

// MaxCubeColumns defines the maximum allowed dimension columns for GROUP BY CUBE.
// Bounded to 12 columns to guarantee maximum 4,096 grouping sets, matching Microsoft SQL Server limit ([RULE:BoundedBuffers]).
const MaxCubeColumns = 12

// ErrTooManyCubeColumns is returned when a CUBE clause exceeds the maximum allowed dimensions.
var ErrTooManyCubeColumns = errors.New("CUBE clause exceeds maximum allowed dimensions")

type aggFuncType int

const (
	aggCount aggFuncType = iota
	aggSum
	aggTotal
	aggAvg
	aggMin
	aggMax
	aggGroupConcat
	aggGrouping
	aggPercentileCont
	aggPercentileDisc
	aggApproxPercentile
	aggMedian
)

type aggField struct {
	fnType     aggFuncType
	argCol     string
	separator  string
	alias      string
	rawExpr    string
	isStar     bool
	isLiteral  bool
	isDistinct bool
	percentile float64
	parsed     proc.Expr
}

type groupAccumulator struct {
	groupTuple Tuple
	groupMask  uint32
	counts     map[int]int64
	sums       map[int]float64
	decSums    map[int]storage.Decimal
	isDecimal  map[int]bool
	mins       map[int]any
	maxs       map[int]any
	values     map[int][]float64
	strLists   map[int][]string
	tDigests   map[int]*TDigest
	distincts  map[int]map[string]struct{}
}

// AggregateIterator evaluates GROUP BY, aggregate functions (COUNT, SUM, AVG, MIN, MAX), and HAVING filters.
type AggregateIterator struct {
	child       Iterator
	groupKeys   []string
	groupByMod  proc.GroupByModifier
	projections  []string
	havingCond   string
	parsedHaving proc.Expr
	fields       []aggField
	tuples      []Tuple
	idx         int
	schema      []string
	colIndexMap *ColumnIndexMap
	arena       *storage.Arena
}

// SetArena configures an optional memory arena for aggregate result tuples.
func (a *AggregateIterator) SetArena(ar *storage.Arena) {
	a.arena = ar
}

// NewAggregateIterator creates a new Aggregate operator.
func NewAggregateIterator(child Iterator, groupKeys, projections []string, havingCond string) *AggregateIterator {
	return NewAggregateIteratorWithModifier(child, groupKeys, proc.GroupByNormal, projections, havingCond)
}

// NewAggregateIteratorWithModifier creates a new Aggregate operator with ROLLUP / CUBE modifier.
func NewAggregateIteratorWithModifier(child Iterator, groupKeys []string, mod proc.GroupByModifier, projections []string, havingCond string) *AggregateIterator {
	a := &AggregateIterator{
		child:       child,
		groupKeys:   groupKeys,
		groupByMod:  mod,
		projections: projections,
		havingCond:  strings.TrimSpace(havingCond),
	}
	a.parseFields()
	return a
}

func matchColName(a, b string) bool {
	a = strings.Trim(strings.TrimSpace(a), "\"`[]")
	b = strings.Trim(strings.TrimSpace(b), "\"`[]")
	if strings.EqualFold(a, b) {
		return true
	}
	if strings.ContainsAny(a, " +-*/%|()") || strings.ContainsAny(b, " +-*/%|()") {
		return false
	}
	if dot := strings.LastIndex(a, "."); dot != -1 {
		a = a[dot+1:]
	}
	if dot := strings.LastIndex(b, "."); dot != -1 {
		b = b[dot+1:]
	}
	return strings.EqualFold(a, b)
}

func isUnaryOp(s string) bool {
	return s == "+" || s == "-" || s == "~" || s == "!" || strings.EqualFold(s, "NOT")
}

func parseExprOrLiteral(exprStr string) proc.Expr {
	exprStr = strings.TrimSpace(exprStr)
	if exprStr == "" {
		return nil
	}
	if strings.ContainsAny(exprStr, " +-*/%|()") || strings.Contains(exprStr, "->") {
		parser := proc.NewParser(proc.NewLexer(exprStr))
		if ast := parser.ParseExpr(); ast != nil && len(parser.Errors()) == 0 {
			return ast
		}
	}
	if intVal, err := strconv.ParseInt(exprStr, 10, 64); err == nil {
		return &proc.LiteralExpr{Val: storage.IntValue(intVal)}
	}
	if floatVal, err := strconv.ParseFloat(exprStr, 64); err == nil {
		return &proc.LiteralExpr{Val: storage.FloatValue(floatVal)}
	}
	if (strings.HasPrefix(exprStr, "'") && strings.HasSuffix(exprStr, "'")) || (strings.HasPrefix(exprStr, "\"") && strings.HasSuffix(exprStr, "\"")) {
		strVal := exprStr[1 : len(exprStr)-1]
		return &proc.LiteralExpr{Val: storage.StringValue(strVal)}
	}
	return nil
}

func (a *AggregateIterator) parseFields() {
	var auxAggFields []aggField
	var extractAggFromAST func(expr proc.Expr)
	extractAggFromAST = func(expr proc.Expr) {
		if expr == nil {
			return
		}
		switch e := expr.(type) {
		case *proc.FunctionCallExpr:
			fnName := strings.ToUpper(e.Name)
			if fnName == "COUNT" || fnName == "SUM" || fnName == "TOTAL" || fnName == "AVG" || fnName == "MIN" || fnName == "MAX" || fnName == "GROUP_CONCAT" || fnName == "STRING_AGG" {
				var fnType aggFuncType
				switch fnName {
				case "COUNT":
					fnType = aggCount
				case "SUM":
					fnType = aggSum
				case "TOTAL":
					fnType = aggTotal
				case "AVG":
					fnType = aggAvg
				case "MIN":
					fnType = aggMin
				case "MAX":
					fnType = aggMax
				case "GROUP_CONCAT", "STRING_AGG":
					fnType = aggGroupConcat
				}
				isDist := e.Distinct
				isStar := false
				var argCol string
				var parsedArg proc.Expr
				if len(e.Args) == 0 {
					isStar = true
					argCol = "*"
				} else if colRef, ok := e.Args[0].(*proc.ColumnRefExpr); ok && colRef.Name == "*" {
					isStar = true
					argCol = "*"
				} else {
					parsedArg = e.Args[0]
					argCol = e.Args[0].String()
				}

				rawCall := e.String()
				already := false
				for _, ef := range a.fields {
					if !ef.isLiteral && strings.EqualFold(ef.rawExpr, rawCall) {
						already = true
						break
					}
				}
				if !already {
					for _, ef := range auxAggFields {
						if !ef.isLiteral && strings.EqualFold(ef.rawExpr, rawCall) {
							already = true
							break
						}
					}
				}
				sep := ","
				if fnType == aggGroupConcat && len(e.Args) > 1 {
					sep = strings.Trim(e.Args[1].String(), "'\"")
				}
				if !already {
					auxAggFields = append(auxAggFields, aggField{
						fnType:     fnType,
						argCol:     argCol,
						separator:  sep,
						alias:      rawCall,
						isDistinct: isDist,
						isStar:     isStar,
						rawExpr:    rawCall,
						parsed:     parsedArg,
					})
				}
			}
			for _, child := range e.Args {
				extractAggFromAST(child)
			}
		case *proc.UnaryExpr:
			extractAggFromAST(e.Expr)
		case *proc.BinaryExpr:
			extractAggFromAST(e.Left)
			extractAggFromAST(e.Right)
		case *proc.CastExpr:
			extractAggFromAST(e.Expr)
		case *proc.CaseExpr:
			extractAggFromAST(e.Operand)
			for _, w := range e.Whens {
				extractAggFromAST(w.Condition)
				extractAggFromAST(w.Result)
			}
			extractAggFromAST(e.Else)
		case *proc.BetweenExpr:
			extractAggFromAST(e.Expr)
			extractAggFromAST(e.Low)
			extractAggFromAST(e.High)
		case *proc.InExpr:
			extractAggFromAST(e.Expr)
			for _, it := range e.List {
				extractAggFromAST(it)
			}
		case *proc.IsNullExpr:
			extractAggFromAST(e.Expr)
		case *proc.LikeExpr:
			extractAggFromAST(e.Expr)
			extractAggFromAST(e.Pattern)
		case *proc.WithinGroupExpr:
			if e.Function != nil {
				extractAggFromAST(e.Function)
			}
		}
	}

	for _, p := range a.projections {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.HasPrefix(strings.ToUpper(p), "ALL ") {
			p = strings.TrimSpace(p[4:])
		}
		if p == "*" {
			if a.child != nil {
				for _, col := range a.child.Schema() {
					if storage.IsSyntheticPKName(col) {
						continue
					}
					a.fields = append(a.fields, aggField{isLiteral: true, argCol: col, alias: col, rawExpr: col})
					a.schema = append(a.schema, col)
				}
			}
			continue
		}

		var extractedAlias string
		exprPart := p
		if expr, alias, hasAs := sqlutil.SplitAsAlias(p); hasAs {
			exprPart = strings.TrimSpace(expr)
			extractedAlias = sqlutil.CleanIdentifier(alias)
		} else {
			parts := sqlutil.SplitTopLevelTokens(p)
			if len(parts) == 2 && !strings.Contains(parts[1], "(") && !strings.Contains(parts[1], "-") && !isUnaryOp(parts[0]) {
				exprPart = strings.TrimSpace(parts[0])
				extractedAlias = sqlutil.CleanIdentifier(parts[1])
			}
		}

		pLex := proc.NewParser(proc.NewLexer(exprPart))
		ast := pLex.ParseExpr()
		hasAST := ast != nil && len(pLex.Errors()) == 0

		if hasAST {
			if fn, ok := ast.(*proc.FunctionCallExpr); ok {
				fnName := strings.ToUpper(fn.Name)
				if fnName == "COUNT" {
					isStar := len(fn.Args) == 0
					if len(fn.Args) == 1 {
						if colRef, isCol := fn.Args[0].(*proc.ColumnRefExpr); isCol && colRef.Name == "*" {
							isStar = true
						}
					}
					if isStar {
						alias := extractedAlias
						if alias == "" {
							alias = "count"
						}
						a.fields = append(a.fields, aggField{fnType: aggCount, isStar: true, alias: alias, rawExpr: "COUNT(*)"})
						a.schema = append(a.schema, alias)
						continue
					}
				}

				if fnName == "COUNT" || fnName == "SUM" || fnName == "TOTAL" || fnName == "AVG" || fnName == "MIN" || fnName == "MAX" || fnName == "GROUP_CONCAT" || fnName == "STRING_AGG" {
					var fnType aggFuncType
					switch fnName {
					case "COUNT":
						fnType = aggCount
					case "SUM":
						fnType = aggSum
					case "TOTAL":
						fnType = aggTotal
					case "AVG":
						fnType = aggAvg
					case "MIN":
						fnType = aggMin
					case "MAX":
						fnType = aggMax
					case "GROUP_CONCAT", "STRING_AGG":
						fnType = aggGroupConcat
					}

					alias := extractedAlias
					if alias == "" {
						alias = strings.ToLower(fnName)
					}
					isDistinct := fn.Distinct
					arg := ""
					sep := ","
					var parsedArg proc.Expr
					if len(fn.Args) > 0 {
						parsedArg = fn.Args[0]
						arg = strings.Trim(strings.TrimSpace(fn.Args[0].String()), "\"`[]")
						if fnType == aggGroupConcat && len(fn.Args) > 1 {
							sep = strings.Trim(fn.Args[1].String(), "'\"")
						}
					}
					rawCall := fmt.Sprintf("%s(%s)", fnName, arg)

					a.fields = append(a.fields, aggField{
						fnType:     fnType,
						argCol:     arg,
						separator:  sep,
						alias:      alias,
						isDistinct: isDistinct,
						rawExpr:    rawCall,
						parsed:     parsedArg,
					})
					a.schema = append(a.schema, alias)
					continue
				}

				if fnName == "GROUPING" && len(fn.Args) == 1 {
					arg := strings.Trim(strings.TrimSpace(fn.Args[0].String()), "\"`[]")
					alias := extractedAlias
					if alias == "" {
						alias = fmt.Sprintf("grouping_%s", arg)
					}
					rawCall := fmt.Sprintf("GROUPING(%s)", arg)
					a.fields = append(a.fields, aggField{fnType: aggGrouping, argCol: arg, alias: alias, rawExpr: rawCall})
					a.schema = append(a.schema, alias)
					continue
				}

				if fnName == "MEDIAN" && len(fn.Args) == 1 {
					arg := strings.Trim(strings.TrimSpace(fn.Args[0].String()), "\"`[]")
					alias := extractedAlias
					if alias == "" {
						alias = "median"
					}
					a.fields = append(a.fields, aggField{fnType: aggMedian, argCol: arg, alias: alias, isDistinct: fn.Distinct, percentile: 0.5, rawExpr: exprPart})
					a.schema = append(a.schema, alias)
					continue
				}

				if fnName == "APPROX_PERCENTILE" && len(fn.Args) >= 2 {
					arg := strings.Trim(strings.TrimSpace(fn.Args[0].String()), "\"`[]")
					pVal, _ := strconv.ParseFloat(fn.Args[1].String(), 64)
					alias := extractedAlias
					if alias == "" {
						alias = "approx_percentile"
					}
					a.fields = append(a.fields, aggField{fnType: aggApproxPercentile, argCol: arg, alias: alias, percentile: pVal, rawExpr: exprPart})
					a.schema = append(a.schema, alias)
					continue
				}

				if (fnName == "PERCENTILE_CONT" || fnName == "PERCENTILE_DISC") && len(fn.Args) == 2 {
					arg := strings.Trim(strings.TrimSpace(fn.Args[0].String()), "\"`[]")
					pVal, _ := strconv.ParseFloat(fn.Args[1].String(), 64)
					alias := extractedAlias
					tType := aggPercentileCont
					if fnName == "PERCENTILE_DISC" {
						tType = aggPercentileDisc
					}
					if alias == "" {
						alias = strings.ToLower(fnName)
					}
					a.fields = append(a.fields, aggField{fnType: tType, argCol: arg, alias: alias, percentile: pVal, rawExpr: exprPart})
					a.schema = append(a.schema, alias)
					continue
				}
			}

			if wg, ok := ast.(*proc.WithinGroupExpr); ok && wg.Function != nil {
				fnName := strings.ToUpper(wg.Function.Name)
				if (fnName == "PERCENTILE_CONT" || fnName == "PERCENTILE_DISC") && len(wg.Function.Args) == 1 {
					pVal, _ := strconv.ParseFloat(wg.Function.Args[0].String(), 64)
					arg := ""
					if wg.OrderBy != nil {
						arg = strings.Trim(strings.TrimSpace(wg.OrderBy.String()), "\"`[]")
					}
					alias := extractedAlias
					tType := aggPercentileCont
					if fnName == "PERCENTILE_DISC" {
						tType = aggPercentileDisc
					}
					if alias == "" {
						alias = strings.ToLower(fnName)
					}
					a.fields = append(a.fields, aggField{fnType: tType, argCol: arg, alias: alias, percentile: pVal, rawExpr: exprPart})
					a.schema = append(a.schema, alias)
					continue
				}
			}
		}

		// Group-by column or passthrough (or nested aggregate expression like COALESCE(SUM(col), 0))
		alias := extractedAlias
		if alias == "" {
			alias = exprPart
			if !strings.ContainsAny(alias, " +-*/%|()") {
				if dotIdx := strings.Index(alias, "."); dotIdx != -1 {
					alias = alias[dotIdx+1:]
				}
			}
			alias = sqlutil.CleanIdentifier(alias)
		}
		parsedExpr := ast
		if parsedExpr == nil {
			parsedExpr = parseExprOrLiteral(exprPart)
		}
		a.fields = append(a.fields, aggField{isLiteral: true, argCol: exprPart, alias: alias, rawExpr: exprPart, parsed: parsedExpr})
		a.schema = append(a.schema, alias)

		// Extract any nested aggregate function calls inside exprPart (e.g. COALESCE(SUM(o.amount), 0))
		// so they are accumulated across the group and available during post-group expression evaluation.
		if parsedExpr != nil {
			extractAggFromAST(parsedExpr)
		}
	}

	if a.havingCond != "" {
		p := proc.NewParser(proc.NewLexer(a.havingCond))
		if expr := p.ParseExpr(); expr != nil && len(p.Errors()) == 0 {
			a.parsedHaving = expr
			extractAggFromAST(expr)
		}
	}

	a.fields = append(a.fields, auxAggFields...)

	colMap := &ColumnIndexMap{
		indices: make(map[string]int, len(a.fields)*4),
		cols:    a.schema,
	}
	for fIdx, f := range a.fields {
		colMap.indices[f.alias] = fIdx
		colMap.indices[strings.ToLower(f.alias)] = fIdx
		colMap.indices[strings.ToUpper(f.alias)] = fIdx
		if f.rawExpr != "" {
			colMap.indices[f.rawExpr] = fIdx
			colMap.indices[strings.ToLower(f.rawExpr)] = fIdx
			colMap.indices[strings.ToUpper(f.rawExpr)] = fIdx
		}
		if f.argCol != "" {
			if _, exists := colMap.indices[f.argCol]; !exists {
				colMap.indices[f.argCol] = fIdx
				colMap.indices[strings.ToLower(f.argCol)] = fIdx
			}
		}
	}
	a.colIndexMap = colMap
}

func (a *AggregateIterator) Open(ctx context.Context) error {
	if a.child == nil {
		return fmt.Errorf("aggregate: child iterator is nil")
	}
	if err := a.child.Open(ctx); err != nil {
		return err
	}

	if a.colIndexMap == nil {
		a.colIndexMap = NewColumnIndexMapFromCols(a.Schema())
	}
	a.tuples = nil
	a.idx = 0

	groups := make(map[string]*groupAccumulator)
	var groupOrder []string

	nKeys := len(a.groupKeys)
	if nKeys > 31 {
		nKeys = 31
	}
	var masks []uint32
	if nKeys == 0 {
		masks = []uint32{0}
	} else {
		switch a.groupByMod {
		case proc.GroupByRollup:
			masks = make([]uint32, nKeys+1)
			for lvl := nKeys; lvl >= 0; lvl-- {
				if lvl == 0 {
					masks[nKeys-lvl] = 0
				} else {
					masks[nKeys-lvl] = (uint32(1) << lvl) - 1
				}
			}
		case proc.GroupByCube:
			if nKeys > MaxCubeColumns {
				_ = a.child.Close()
				return fmt.Errorf("%w: %d columns specified, maximum allowed is %d (limit: 4,096 grouping sets)",
					ErrTooManyCubeColumns, nKeys, MaxCubeColumns)
			}
			totalComb := 1 << nKeys
			masks = make([]uint32, totalComb)
			for m := 0; m < totalComb; m++ {
				masks[m] = uint32(m)
			}
		default:
			masks = []uint32{(uint32(1) << nKeys) - 1}
		}
	}

	hasRows := false
	inChunk := GetChunk()
	defer PutChunk(inChunk)

	if len(a.groupKeys) == 0 {
		// Fast-path: Global aggregation (no GROUP BY). Avoids string formatting and map hashing per tuple.
		globalAcc := &groupAccumulator{
			counts:    make(map[int]int64),
			sums:      make(map[int]float64),
			decSums:   make(map[int]storage.Decimal),
			isDecimal: make(map[int]bool),
			mins:      make(map[int]any),
			maxs:      make(map[int]any),
			values:    make(map[int][]float64),
			strLists:  make(map[int][]string),
			tDigests:  make(map[int]*TDigest),
		}
		groups[""] = globalAcc
		groupOrder = append(groupOrder, "")

		for {
			select {
			case <-ctx.Done():
				_ = a.child.Close()
				a.tuples = nil
				return ctx.Err()
			default:
			}

			err := PullChunk(ctx, a.child, inChunk, DefaultChunkSize)
			if err != nil && err != io.EOF {
				_ = a.child.Close()
				a.tuples = nil
				return err
			}
			if inChunk.Len() == 0 && err == io.EOF {
				break
			}
			hasRows = true

			for _, tuple := range inChunk.Tuples {
				if globalAcc.groupTuple.IsNil() {
					globalAcc.groupTuple = tuple
				}
				a.accumulateTuple(globalAcc, tuple)
			}

			if err == io.EOF {
				break
			}
		}
	} else {
		for {
			select {
			case <-ctx.Done():
				_ = a.child.Close()
				a.tuples = nil
				return ctx.Err()
			default:
			}

			err := PullChunk(ctx, a.child, inChunk, DefaultChunkSize)
			if err != nil && err != io.EOF {
				_ = a.child.Close()
				a.tuples = nil
				return err
			}
			if inChunk.Len() == 0 && err == io.EOF {
				break
			}
			hasRows = true

			for _, tuple := range inChunk.Tuples {
				for _, mask := range masks {
					var gk strings.Builder
					gk.WriteString(strconv.FormatUint(uint64(mask), 10))
					gk.WriteByte('\x01')
					for i, k := range a.groupKeys {
						if (mask & (1 << i)) != 0 {
							val, _ := lookupVal(tuple, k)
							gk.WriteString(fmt.Sprintf("%v", val))
						}
						gk.WriteByte('\x00')
					}
					groupKeyStr := gk.String()

					acc, exists := groups[groupKeyStr]
					if !exists {
						acc = &groupAccumulator{
							groupTuple: tuple,
							groupMask:  mask,
							counts:     make(map[int]int64),
							sums:       make(map[int]float64),
							decSums:    make(map[int]storage.Decimal),
							isDecimal:  make(map[int]bool),
							mins:       make(map[int]any),
							maxs:       make(map[int]any),
							values:     make(map[int][]float64),
							strLists:   make(map[int][]string),
							tDigests:   make(map[int]*TDigest),
						}
						groups[groupKeyStr] = acc
						groupOrder = append(groupOrder, groupKeyStr)
					}

					a.accumulateTuple(acc, tuple)
				}
			}

			if err == io.EOF {
				break
			}
		}
	}

	// If no rows returned and no GROUP BY (global aggregation: e.g. SELECT COUNT(*) FROM empty)
	if !hasRows && len(a.groupKeys) == 0 {
		var vals []any
		if a.arena != nil {
			vals = a.arena.AllocAnySlice(len(a.fields))
		} else {
			vals = make([]any, len(a.fields))
		}
		evalMap := make(map[string]any, len(a.fields)*2)
		for fIdx, f := range a.fields {
			if f.isLiteral {
				continue
			}
			if f.isStar || f.fnType == aggCount {
				vals[fIdx] = int64(0)
			} else {
				vals[fIdx] = nil
			}
			if f.rawExpr != "" {
				evalMap[f.rawExpr] = vals[fIdx]
				evalMap[strings.ToUpper(f.rawExpr)] = vals[fIdx]
				evalMap[strings.ToLower(f.rawExpr)] = vals[fIdx]
			}
			if f.alias != "" {
				evalMap[f.alias] = vals[fIdx]
			}
		}
		for fIdx, f := range a.fields {
			if !f.isLiteral {
				continue
			}
			if f.parsed != nil {
				if res, err := proc.EvalRowExpr(f.parsed, evalMap, nil); err == nil {
					vals[fIdx] = res
				}
			}
		}
		emptyTuple := Tuple{Values: vals, Index: a.colIndexMap}
		if a.havingCond == "" || EvalConditionTuple(emptyTuple, a.havingCond) {
			if len(vals) > len(a.schema) {
				emptyTuple.Values = vals[:len(a.schema)]
			}
			a.tuples = append(a.tuples, emptyTuple)
		}
		return nil
	}

	if a.groupByMod != proc.GroupByNormal && nKeys > 0 {
		sort.SliceStable(groupOrder, func(i, j int) bool {
			accA := groups[groupOrder[i]]
			accB := groups[groupOrder[j]]
			for k := 0; k < nKeys; k++ {
				actA := (accA.groupMask & (1 << k)) != 0
				actB := (accB.groupMask & (1 << k)) != 0
				if actA && actB {
					valA, _ := lookupVal(accA.groupTuple, a.groupKeys[k])
					valB, _ := lookupVal(accB.groupTuple, a.groupKeys[k])
					c := CompareValues(valA, valB)
					if c != 0 {
						return c < 0
					}
				} else if actA != actB {
					return actA
				}
			}
			return accA.groupMask > accB.groupMask
		})
	}

	// Materialize groups into result tuples
	for _, gk := range groupOrder {
		acc := groups[gk]
		var vals []any
		if a.arena != nil {
			vals = a.arena.AllocAnySlice(len(a.fields))
		} else {
			vals = make([]any, len(a.fields))
		}

		// Pass 1: Compute all aggregate fields (!f.isLiteral) first so composite expressions
		// (like COALESCE(SUM(x), 0) or SUM(a) + SUM(b)) can reference their accumulated results.
		for fIdx, f := range a.fields {
			if f.isLiteral {
				continue
			}

			if f.fnType == aggGrouping {
				isAggregated := true
				for kIdx, k := range a.groupKeys {
					if matchColName(k, f.argCol) {
						if (acc.groupMask & (1 << kIdx)) != 0 {
							isAggregated = false
						}
						break
					}
				}
				if isAggregated {
					vals[fIdx] = int64(1)
				} else {
					vals[fIdx] = int64(0)
				}
				continue
			}

			var val any
			switch f.fnType {
			case aggCount:
				val = acc.counts[fIdx]
			case aggSum:
				if acc.counts[fIdx] == 0 {
					val = nil
				} else if acc.isDecimal[fIdx] {
					val = acc.decSums[fIdx].String()
				} else {
					s := acc.sums[fIdx]
					if s == math.Trunc(s) {
						val = int64(s)
					} else {
						val = s
					}
				}
			case aggTotal:
				s := acc.sums[fIdx]
				if s == math.Trunc(s) {
					val = int64(s)
				} else {
					val = s
				}
			case aggGroupConcat:
				list := acc.strLists[fIdx]
				if len(list) == 0 {
					val = nil
				} else {
					sep := f.separator
					if sep == "" {
						sep = ","
					}
					val = strings.Join(list, sep)
				}
			case aggAvg:
				if acc.counts[fIdx] == 0 {
					val = nil
				} else if acc.isDecimal[fIdx] {
					countDec := storage.NewDecimalFromInt(acc.counts[fIdx], 0)
					if quo, err := acc.decSums[fIdx].Div(countDec, 4); err == nil {
						val = quo.String()
					} else {
						val = acc.decSums[fIdx].String()
					}
				} else {
					avg := acc.sums[fIdx] / float64(acc.counts[fIdx])
					if avg == math.Trunc(avg) {
						val = int64(avg)
					} else {
						val = avg
					}
				}
			case aggMin:
				val = acc.mins[fIdx]
			case aggMax:
				val = acc.maxs[fIdx]
			case aggPercentileCont, aggMedian:
				if acc.counts[fIdx] == 0 || len(acc.values[fIdx]) == 0 {
					val = nil
				} else {
					sort.Float64s(acc.values[fIdx])
					res := ComputePercentileCont(acc.values[fIdx], f.percentile)
					if res == math.Trunc(res) {
						val = int64(res)
					} else {
						val = res
					}
				}
			case aggPercentileDisc:
				if acc.counts[fIdx] == 0 || len(acc.values[fIdx]) == 0 {
					val = nil
				} else {
					sort.Float64s(acc.values[fIdx])
					res := ComputePercentileDisc(acc.values[fIdx], f.percentile)
					if res == math.Trunc(res) {
						val = int64(res)
					} else {
						val = res
					}
				}
			case aggApproxPercentile:
				if acc.counts[fIdx] == 0 {
					val = nil
				} else if td, ok := acc.tDigests[fIdx]; ok {
					var res float64
					if len(acc.values[fIdx]) <= 10000 {
						sort.Float64s(acc.values[fIdx])
						res = ComputePercentileCont(acc.values[fIdx], f.percentile)
					} else {
						res = td.Quantile(f.percentile)
					}
					if res == math.Trunc(res) {
						val = int64(res)
					} else {
						val = res
					}
				} else {
					val = nil
				}
			}

			vals[fIdx] = val
		}

		// Pass 2: Compute group-by columns and literal/composite expressions (f.isLiteral).
		var evalMap map[string]any
		for fIdx, f := range a.fields {
			if !f.isLiteral {
				continue
			}
			isGroupCol := false
			var colVal any
			for kIdx, k := range a.groupKeys {
				if matchColName(k, f.argCol) {
					isGroupCol = true
					if (acc.groupMask & (1 << kIdx)) != 0 {
						colVal, _ = lookupVal(acc.groupTuple, f.argCol)
					} else {
						colVal = nil
					}
					break
				}
			}
			if isGroupCol {
				vals[fIdx] = colVal
			} else if f.parsed != nil {
				if evalMap == nil {
					evalMap = acc.groupTuple.ToEvalMap()
					if evalMap == nil {
						evalMap = make(map[string]any, len(a.fields)*2)
					}
					for afIdx, af := range a.fields {
						if !af.isLiteral {
							if af.rawExpr != "" {
								evalMap[af.rawExpr] = vals[afIdx]
								evalMap[strings.ToUpper(af.rawExpr)] = vals[afIdx]
								evalMap[strings.ToLower(af.rawExpr)] = vals[afIdx]
							}
							if af.alias != "" {
								evalMap[af.alias] = vals[afIdx]
							}
						}
					}
				}
				res, err := proc.EvalRowExpr(f.parsed, evalMap, nil)
				if err == nil {
					vals[fIdx] = res
				} else {
					vals[fIdx] = nil
				}
			} else {
				v, _ := lookupVal(acc.groupTuple, f.argCol)
				vals[fIdx] = v
			}
		}

		havingPass := true
		if a.havingCond != "" {
			if evalMap == nil {
				evalMap = acc.groupTuple.ToEvalMap()
				if evalMap == nil {
					evalMap = make(map[string]any, len(a.fields)*2)
				}
			}
			for afIdx, af := range a.fields {
				if af.rawExpr != "" {
					evalMap[af.rawExpr] = vals[afIdx]
					evalMap[strings.ToUpper(af.rawExpr)] = vals[afIdx]
					evalMap[strings.ToLower(af.rawExpr)] = vals[afIdx]
				}
				if af.alias != "" {
					evalMap[af.alias] = vals[afIdx]
					evalMap[strings.ToUpper(af.alias)] = vals[afIdx]
					evalMap[strings.ToLower(af.alias)] = vals[afIdx]
				}
				if af.argCol != "" {
					evalMap[af.argCol] = vals[afIdx]
					evalMap[strings.ToUpper(af.argCol)] = vals[afIdx]
					evalMap[strings.ToLower(af.argCol)] = vals[afIdx]
				}
			}

			if a.parsedHaving != nil {
				havingPass = proc.EvalBoolExpr(a.parsedHaving, evalMap, nil)
			} else {
				row := Tuple{Values: vals, Index: a.colIndexMap}
				havingPass = EvalConditionTuple(row, a.havingCond)
			}
		}

		if havingPass {
			row := Tuple{Values: vals, Index: a.colIndexMap}
			if len(vals) > len(a.schema) {
				row.Values = vals[:len(a.schema)]
			}
			a.tuples = append(a.tuples, row)
		}
	}

	return nil
}

func (a *AggregateIterator) accumulateTuple(acc *groupAccumulator, tuple Tuple) {
	for fIdx, f := range a.fields {
		if f.isLiteral || f.fnType == aggGrouping {
			continue
		}
		if f.isStar {
			acc.counts[fIdx]++
			continue
		}

		var val any
		var ok bool
		if f.parsed != nil {
			val, ok = EvalTupleExpr(tuple, f.parsed)
		} else {
			val, ok = lookupVal(tuple, f.argCol)
		}
		if !ok || val == nil {
			continue
		}

		if f.isDistinct {
			if acc.distincts == nil {
				acc.distincts = make(map[int]map[string]struct{})
			}
			set, exists := acc.distincts[fIdx]
			if !exists {
				set = make(map[string]struct{})
				acc.distincts[fIdx] = set
			}
			dKey := fmt.Sprintf("%v", val)
			if _, seen := set[dKey]; seen {
				continue
			}
			set[dKey] = struct{}{}
		}

		switch f.fnType {
		case aggCount:
			acc.counts[fIdx]++
		case aggSum, aggTotal:
			if isDecValue(val) {
				dec, _ := tryDecimal(val)
				if !acc.isDecimal[fIdx] {
					acc.isDecimal[fIdx] = true
					if acc.sums[fIdx] != 0 {
						existingDec := storage.NewDecimalFromFloat(acc.sums[fIdx], 4)
						acc.decSums[fIdx] = acc.decSums[fIdx].Add(existingDec)
						acc.sums[fIdx] = 0
					}
				}
				cur := acc.decSums[fIdx]
				acc.decSums[fIdx] = cur.Add(dec)
			} else {
				fVal, _ := toFloat64(val)
				if acc.isDecimal[fIdx] {
					dec := storage.NewDecimalFromFloat(fVal, 4)
					cur := acc.decSums[fIdx]
					acc.decSums[fIdx] = cur.Add(dec)
				} else {
					acc.sums[fIdx] += fVal
				}
			}
			acc.counts[fIdx]++
		case aggAvg:
			if isDecValue(val) {
				dec, _ := tryDecimal(val)
				if !acc.isDecimal[fIdx] {
					acc.isDecimal[fIdx] = true
					if acc.sums[fIdx] != 0 {
						existingDec := storage.NewDecimalFromFloat(acc.sums[fIdx], 4)
						acc.decSums[fIdx] = acc.decSums[fIdx].Add(existingDec)
						acc.sums[fIdx] = 0
					}
				}
				cur := acc.decSums[fIdx]
				acc.decSums[fIdx] = cur.Add(dec)
			} else {
				fVal, _ := toFloat64(val)
				if acc.isDecimal[fIdx] {
					dec := storage.NewDecimalFromFloat(fVal, 4)
					cur := acc.decSums[fIdx]
					acc.decSums[fIdx] = cur.Add(dec)
				} else {
					acc.sums[fIdx] += fVal
				}
			}
			acc.counts[fIdx]++
		case aggMin:
			if cur, ok := acc.mins[fIdx]; !ok || CompareValues(val, cur) < 0 {
				acc.mins[fIdx] = val
			}
		case aggMax:
			if cur, ok := acc.maxs[fIdx]; !ok || CompareValues(val, cur) > 0 {
				acc.maxs[fIdx] = val
			}
		case aggGroupConcat:
			if val != nil {
				acc.strLists[fIdx] = append(acc.strLists[fIdx], fmt.Sprintf("%v", val))
				acc.counts[fIdx]++
			}
		case aggPercentileCont, aggPercentileDisc, aggMedian:
			if fVal, ok := toFloat64(val); ok {
				acc.values[fIdx] = append(acc.values[fIdx], fVal)
				acc.counts[fIdx]++
			}
		case aggApproxPercentile:
			if fVal, ok := toFloat64(val); ok {
				td, exists := acc.tDigests[fIdx]
				if !exists {
					td = NewTDigest(100)
					acc.tDigests[fIdx] = td
				}
				td.Add(fVal)
				acc.values[fIdx] = append(acc.values[fIdx], fVal)
				acc.counts[fIdx]++
			}
		}
	}
}

func (a *AggregateIterator) NextChunk(ctx context.Context, dest *Chunk) error {
	dest.Reset()
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if a.idx >= len(a.tuples) {
		return io.EOF
	}

	for dest.Len() < DefaultChunkSize && a.idx < len(a.tuples) {
		dest.Tuples = append(dest.Tuples, a.tuples[a.idx])
		a.idx++
	}
	return nil
}

func (a *AggregateIterator) Next(ctx context.Context) (Tuple, error) {
	select {
	case <-ctx.Done():
		return Tuple{}, ctx.Err()
	default:
	}

	if a.idx >= len(a.tuples) {
		return Tuple{}, io.EOF
	}

	t := a.tuples[a.idx]
	a.idx++
	return t, nil
}

func (a *AggregateIterator) Close() error {
	a.tuples = nil
	a.idx = 0
	if a.child != nil {
		return a.child.Close()
	}
	return nil
}

func (a *AggregateIterator) Schema() []string {
	return a.schema
}

func (a *AggregateIterator) ColIndexMap() *ColumnIndexMap {
	return a.colIndexMap
}

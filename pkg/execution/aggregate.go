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

// MaxGroupByColumns defines the maximum allowed grouping columns for GROUP BY.
// Bounded to 64 columns matching Microsoft SQL Server limit and 64-bit mask capacity ([RULE:BoundedBuffers]).
const MaxGroupByColumns = 64

// ErrTooManyCubeColumns is returned when a CUBE clause exceeds the maximum allowed dimensions.
var ErrTooManyCubeColumns = errors.New("CUBE clause exceeds maximum allowed dimensions")

// ErrTooManyGroupByColumns is returned when a GROUP BY clause exceeds the maximum allowed columns (64).
var ErrTooManyGroupByColumns = errors.New("GROUP BY clause exceeds maximum allowed columns (64 columns)")

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
	fnType      aggFuncType
	argCol      string
	argColIdx   int
	separator   string
	alias       string
	rawExpr     string
	origExpr    string
	isStar      bool
	isLiteral   bool
	isDistinct  bool
	percentile  float64
	parsed      proc.Expr
	fnExpr      proc.Expr
	orderByExpr proc.Expr
	orderByDesc bool
}

type strOrderItem struct {
	str string
	key any
}

type groupAccumulator struct {
	groupTuple Tuple
	groupMask  uint64
	counts     []int64
	sums       []float64
	intSums    []int64
	hasIntSum  []bool
	decSums    []storage.Decimal
	isDecimal  []bool
	mins       []any
	maxs       []any
	values     [][]float64
	strLists   [][]string
	strOrders  [][]strOrderItem
	tDigests   []*TDigest
	distincts  []map[string]struct{}
}

// AggregateIterator evaluates GROUP BY, aggregate functions (COUNT, SUM, AVG, MIN, MAX), and HAVING filters.
type AggregateIterator struct {
	child         Iterator
	groupKeys     []string
	groupKeyIsCS  []bool
	groupByMod    proc.GroupByModifier
	projections   []string
	havingCond    string
	parsedHaving  proc.Expr
	initErr       error
	fields        []aggField
	tuples        []Tuple
	idx           int
	schema        []string
	colIndexMap   *ColumnIndexMap
	arena         *storage.Arena
	groupKeyIdx   []int
	needCounts    bool
	needSums      bool
	needMins      bool
	needMaxs      bool
	needValues    bool
	needStrLists  bool
	needStrOrders bool
	needTDigests  bool
	needDistincts bool
}

func (a *AggregateIterator) initAccumulatorNeeds() {
	a.needCounts = false
	a.needSums = false
	a.needMins = false
	a.needMaxs = false
	a.needValues = false
	a.needStrLists = false
	a.needStrOrders = false
	a.needTDigests = false
	a.needDistincts = false

	for _, f := range a.fields {
		if f.isDistinct {
			a.needDistincts = true
		}
		switch f.fnType {
		case aggCount:
			a.needCounts = true
		case aggSum, aggTotal, aggAvg:
			a.needCounts = true
			a.needSums = true
		case aggMin:
			a.needMins = true
		case aggMax:
			a.needMaxs = true
		case aggGroupConcat:
			a.needCounts = true
			a.needStrLists = true
			if f.orderByExpr != nil {
				a.needStrOrders = true
			}
		case aggPercentileCont, aggPercentileDisc, aggMedian:
			a.needCounts = true
			a.needValues = true
		case aggApproxPercentile:
			a.needCounts = true
			a.needValues = true
			a.needTDigests = true
		}
	}
}

func (a *AggregateIterator) newGroupAccumulator(tuple Tuple, mask uint64) *groupAccumulator {
	n := len(a.fields)
	acc := &groupAccumulator{
		groupTuple: tuple,
		groupMask:  mask,
	}
	if a.needCounts {
		acc.counts = make([]int64, n)
	}
	if a.needSums {
		acc.sums = make([]float64, n)
		acc.intSums = make([]int64, n)
		acc.hasIntSum = make([]bool, n)
		acc.decSums = make([]storage.Decimal, n)
		acc.isDecimal = make([]bool, n)
	}
	if a.needMins {
		acc.mins = make([]any, n)
	}
	if a.needMaxs {
		acc.maxs = make([]any, n)
	}
	if a.needValues {
		acc.values = make([][]float64, n)
	}
	if a.needStrLists {
		acc.strLists = make([][]string, n)
	}
	if a.needStrOrders {
		acc.strOrders = make([][]strOrderItem, n)
	}
	if a.needTDigests {
		acc.tDigests = make([]*TDigest, n)
	}
	if a.needDistincts {
		acc.distincts = make([]map[string]struct{}, n)
	}
	return acc
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
			if err := proc.ValidateBuiltinArity(fnName, len(e.Args)); err != nil && a.initErr == nil {
				a.initErr = err
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
						origExpr:   rawCall,
						parsed:     parsedArg,
						fnExpr:     e,
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
				fnName := strings.ToUpper(e.Function.Name)
				if fnName != "PERCENTILE_CONT" && fnName != "PERCENTILE_DISC" {
					if err := proc.ValidateBuiltinArity(fnName, len(e.Function.Args)); err != nil && a.initErr == nil {
						a.initErr = err
					}
				}
				if fnName == "STRING_AGG" || fnName == "GROUP_CONCAT" {
					rawCall := e.String()
					already := false
					for _, ef := range a.fields {
						if !ef.isLiteral && (strings.EqualFold(ef.rawExpr, rawCall) || strings.EqualFold(ef.origExpr, rawCall)) {
							already = true
							break
						}
					}
					if !already {
						for _, ef := range auxAggFields {
							if !ef.isLiteral && (strings.EqualFold(ef.rawExpr, rawCall) || strings.EqualFold(ef.origExpr, rawCall)) {
								already = true
								break
							}
						}
					}
					if !already && len(e.Function.Args) > 0 {
						parsedArg := e.Function.Args[0]
						argCol := strings.Trim(strings.TrimSpace(parsedArg.String()), "\"`[]")
						sep := ","
						if len(e.Function.Args) > 1 {
							sep = strings.Trim(e.Function.Args[1].String(), "'\"")
						}
						auxAggFields = append(auxAggFields, aggField{
							fnType:      aggGroupConcat,
							argCol:      argCol,
							separator:   sep,
							alias:       rawCall,
							isDistinct:  e.Function.Distinct,
							rawExpr:     rawCall,
							origExpr:    rawCall,
							parsed:      parsedArg,
							orderByExpr: e.OrderBy,
							orderByDesc: e.Desc,
						})
					}
					return
				}
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
				if err := proc.ValidateBuiltinArity(fnName, len(fn.Args)); err != nil && a.initErr == nil {
					a.initErr = err
				}
				if fnName == "COUNT" {
					isStar := false
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
						a.fields = append(a.fields, aggField{fnType: aggCount, isStar: true, alias: alias, rawExpr: "COUNT(*)", fnExpr: fn})
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
							if lit, ok := fn.Args[1].(*proc.LiteralExpr); (ok && lit.Val.IsNull) || strings.EqualFold(strings.Trim(fn.Args[1].String(), "'\""), "NULL") {
								if a.initErr == nil {
									a.initErr = fmt.Errorf("Msg 8711, Level 16, State 1: The separator expression for STRING_AGG cannot be NULL")
								}
							}
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
						origExpr:   exprPart,
						parsed:     parsedArg,
						fnExpr:     fn,
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
				if fnName != "PERCENTILE_CONT" && fnName != "PERCENTILE_DISC" {
					if err := proc.ValidateBuiltinArity(fnName, len(wg.Function.Args)); err != nil && a.initErr == nil {
						a.initErr = err
					}
				}
				if (fnName == "STRING_AGG" || fnName == "GROUP_CONCAT") && len(wg.Function.Args) >= 1 {
					parsedArg := wg.Function.Args[0]
					arg := strings.Trim(strings.TrimSpace(parsedArg.String()), "\"`[]")
					sep := ","
					if len(wg.Function.Args) > 1 {
						if lit, ok := wg.Function.Args[1].(*proc.LiteralExpr); (ok && lit.Val.IsNull) || strings.EqualFold(strings.Trim(wg.Function.Args[1].String(), "'\""), "NULL") {
							if a.initErr == nil {
								a.initErr = fmt.Errorf("Msg 8711, Level 16, State 1: The separator expression for STRING_AGG cannot be NULL")
							}
						}
						sep = strings.Trim(wg.Function.Args[1].String(), "'\"")
					}
					alias := extractedAlias
					if alias == "" {
						alias = strings.ToLower(fnName)
					}
					a.fields = append(a.fields, aggField{
						fnType:      aggGroupConcat,
						argCol:      arg,
						separator:   sep,
						alias:       alias,
						isDistinct:  wg.Function.Distinct,
						rawExpr:     exprPart,
						origExpr:    wg.String(),
						parsed:      parsedArg,
						fnExpr:      wg,
						orderByExpr: wg.OrderBy,
						orderByDesc: wg.Desc,
					})
					a.schema = append(a.schema, alias)
					continue
				}
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
					a.fields = append(a.fields, aggField{fnType: tType, argCol: arg, alias: alias, percentile: pVal, rawExpr: exprPart, origExpr: wg.String()})
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
		if f.origExpr != "" {
			colMap.indices[f.origExpr] = fIdx
			colMap.indices[strings.ToLower(f.origExpr)] = fIdx
			colMap.indices[strings.ToUpper(f.origExpr)] = fIdx
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

func findHashJoinIterator(it Iterator) *HashJoinIterator {
	curr := it
	for curr != nil {
		if h, ok := curr.(*HashJoinIterator); ok {
			return h
		}
		if inst, ok := curr.(*InstrumentedIterator); ok {
			curr = inst.child
			continue
		}
		break
	}
	return nil
}

type fusedFieldBinding struct {
	fnType    aggFuncType
	isStar    bool
	isLiteral bool
	fromProbe bool
	probeIdx  int
	fromBuild bool
	buildIdx  int
}

func (a *AggregateIterator) accumulateFused(acc *groupAccumulator, probeTuple, buildTuple Tuple, bindings []fusedFieldBinding) {
	for fIdx, b := range bindings {
		if b.isStar {
			acc.counts[fIdx]++
			continue
		}
		if b.isLiteral {
			continue
		}
		var val any
		if b.fromProbe && b.probeIdx >= 0 {
			val, _ = probeTuple.GetIdx(b.probeIdx)
		} else if b.fromBuild && b.buildIdx >= 0 {
			val, _ = buildTuple.GetIdx(b.buildIdx)
		}
		if val == nil {
			continue
		}

		switch b.fnType {
		case aggCount:
			acc.counts[fIdx]++
		case aggSum, aggTotal:
			if iv, isInt := toInt64Fast(val); isInt && !acc.isDecimal[fIdx] {
				acc.intSums[fIdx] += iv
				acc.hasIntSum[fIdx] = true
				acc.counts[fIdx]++
			} else if fv, isFloat := toFloat64(val); isFloat {
				acc.sums[fIdx] += fv
				acc.counts[fIdx]++
			}
		case aggAvg:
			if iv, isInt := toInt64Fast(val); isInt && !acc.isDecimal[fIdx] {
				acc.intSums[fIdx] += iv
				acc.hasIntSum[fIdx] = true
				acc.counts[fIdx]++
			} else if fv, isFloat := toFloat64(val); isFloat {
				acc.sums[fIdx] += fv
				acc.counts[fIdx]++
			}
		case aggMin:
			if acc.counts[fIdx] == 0 || CompareValues(val, acc.mins[fIdx]) < 0 {
				acc.mins[fIdx] = val
			}
			acc.counts[fIdx]++
		case aggMax:
			if acc.counts[fIdx] == 0 || CompareValues(val, acc.maxs[fIdx]) > 0 {
				acc.maxs[fIdx] = val
			}
			acc.counts[fIdx]++
		}
	}
}

func (a *AggregateIterator) tryJoinAggregateFusion(ctx context.Context, orderedAccs *[]*groupAccumulator, hasRows *bool) (bool, error) {
	h := findHashJoinIterator(a.child)
	if h == nil || !h.CanFuseWithAggregate(a.groupKeys, a.groupByMod) {
		return false, nil
	}

	for _, f := range a.fields {
		if f.isDistinct {
			return false, nil
		}
	}

	probeMap := h.left.ColIndexMap()
	buildMap := h.right.ColIndexMap()
	if h.buildSide == "LEFT" {
		probeMap = h.right.ColIndexMap()
		buildMap = h.left.ColIndexMap()
	}
	if probeMap == nil || buildMap == nil {
		return false, nil
	}

	bindings := make([]fusedFieldBinding, len(a.fields))
	for fIdx, f := range a.fields {
		b := fusedFieldBinding{
			fnType:    f.fnType,
			isStar:    f.isStar,
			isLiteral: f.isLiteral,
			probeIdx:  -1,
			buildIdx:  -1,
		}
		if !f.isStar && !f.isLiteral {
			cleanArg := strings.TrimSpace(cleanIdentifierParts(f.argCol))
			if dot := strings.Index(cleanArg, "."); dot != -1 {
				cleanArg = cleanArg[dot+1:]
			}
			if pIdx, ok := probeMap.Index(cleanArg); ok && pIdx >= 0 {
				b.fromProbe = true
				b.probeIdx = pIdx
			} else if bIdx, ok := buildMap.Index(cleanArg); ok && bIdx >= 0 {
				b.fromBuild = true
				b.buildIdx = bIdx
			} else if f.parsed != nil {
				return false, nil
			}
		}
		bindings[fIdx] = b
	}

	type fusedAccItem struct {
		acc       *groupAccumulator
		seenCount int64
		build     Tuple
	}
	keyAccMap := make(map[int64]*fusedAccItem)
	var orderList []*fusedAccItem

	if h.isFlatIntTable {
		for i := range h.flatIntTable {
			slot := &h.flatIntTable[i]
			if slot.used {
				acc := a.newGroupAccumulator(slot.tuple, 1)
				item := &fusedAccItem{acc: acc, build: slot.tuple}
				keyAccMap[slot.key] = item
				orderList = append(orderList, item)
			}
		}
	} else if len(h.intHashTable) > 0 {
		for key, matches := range h.intHashTable {
			if len(matches) > 0 {
				acc := a.newGroupAccumulator(matches[0].tuple, 1)
				item := &fusedAccItem{acc: acc, build: matches[0].tuple}
				keyAccMap[key] = item
				orderList = append(orderList, item)
			}
		}
	}

	probeIter := h.getProbeIter()
	probeKeyIdx := h.leftKeyIdx
	if h.buildSide == "LEFT" {
		probeKeyIdx = h.rightKeyIdx
	}
	if probeKeyIdx < 0 {
		return false, nil
	}

	inChunk := GetChunk()
	defer PutChunk(inChunk)

	for {
		select {
		case <-ctx.Done():
			_ = a.child.Close()
			return true, ctx.Err()
		default:
		}

		err := PullChunk(ctx, probeIter, inChunk, DefaultChunkSize)
		if err != nil && err != io.EOF {
			_ = a.child.Close()
			return true, err
		}
		if inChunk.Len() == 0 && err == io.EOF {
			break
		}

		for _, probeTuple := range inChunk.Tuples {
			val, ok := probeTuple.GetIdx(probeKeyIdx)
			if !ok || val == nil {
				continue
			}
			iKey, isInt := toInt64Fast(val)
			if !isInt {
				continue
			}
			item, exists := keyAccMap[iKey]
			if !exists {
				continue
			}
			if item.seenCount == 0 {
				item.acc.groupTuple = mergeTuplesDirect(probeTuple, item.build, h.buildSide != "LEFT", h.colIndexMap, a.arena)
			}
			item.seenCount++
			a.accumulateFused(item.acc, probeTuple, item.build, bindings)
		}

		if err == io.EOF {
			break
		}
	}

	for _, item := range orderList {
		if item.seenCount > 0 {
			*orderedAccs = append(*orderedAccs, item.acc)
		}
	}

	*hasRows = len(*orderedAccs) > 0
	return true, nil
}

func (a *AggregateIterator) canFuseWithHashJoin(h *HashJoinIterator) bool {
	if h == nil || h.joinType != "INNER" || h.parsedJoinFilter != nil {
		return false
	}
	if a.groupByMod != proc.GroupByNormal || len(a.groupKeys) != 1 {
		return false
	}
	for _, f := range a.fields {
		if f.isDistinct || f.parsed != nil {
			return false
		}
	}
	return true
}

func (a *AggregateIterator) Open(ctx context.Context) (retErr error) {
	if a.initErr != nil {
		return a.initErr
	}
	if a.child == nil {
		return fmt.Errorf("aggregate: child iterator is nil")
	}
	if h := findHashJoinIterator(a.child); h != nil && a.canFuseWithHashJoin(h) {
		h.fusedWithParentAgg = true
	}
	if err := a.child.Open(ctx); err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			_ = a.child.Close()
		}
	}()

	childMap := a.child.ColIndexMap()
	if childMap == nil {
		childMap = NewColumnIndexMapFromCols(a.child.Schema())
	}
	for _, k := range a.groupKeys {
		trimmed := strings.TrimSpace(k)
		if colIdx := sqlutil.FindTopLevelKeyword(trimmed, "COLLATE"); colIdx != -1 {
			trimmed = strings.TrimSpace(trimmed[:colIdx])
		}
		if trimmed == "" || strings.EqualFold(trimmed, "NULL") || strings.ContainsAny(trimmed, " +-*/%|()") || strings.HasPrefix(trimmed, "'") || strings.HasPrefix(trimmed, "\"") {
			continue
		}
		if _, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			continue
		}
		cleanK := cleanIdentifierParts(trimmed)
		bareK := cleanK
		if dot := strings.Index(bareK, "."); dot != -1 {
			bareK = bareK[dot+1:]
		}

		found := false
		if childMap != nil {
			if _, ok := childMap.Index(cleanK); ok {
				found = true
			} else if _, ok := childMap.Index(bareK); ok {
				found = true
			}
		}
		if !found {
			for _, f := range a.fields {
				if strings.EqualFold(cleanIdentifierParts(f.alias), cleanK) || strings.EqualFold(cleanIdentifierParts(f.alias), bareK) {
					found = true
					break
				}
			}
		}
		if !found {
			return fmt.Errorf("column %q does not exist", k)
		}
	}

	if a.parsedHaving != nil {
		if err := validateHavingClause(a.parsedHaving, a.groupKeys, a.fields); err != nil {
			return err
		}
	}

	// ANSI SQL: If query has GROUP BY or aggregate functions, any non-aggregate projection field must be in groupKeys (or constant)
	hasAggFuncs := false
	for _, f := range a.fields {
		if !f.isLiteral && f.fnType != aggGrouping {
			hasAggFuncs = true
			break
		}
	}
	if len(a.groupKeys) > 0 || hasAggFuncs {
		for _, f := range a.fields {
			exprToCheck := f.fnExpr
			if exprToCheck == nil {
				exprToCheck = f.parsed
			}
			if exprToCheck == nil && f.rawExpr != "" {
				exprToCheck = parseExprOrLiteral(f.rawExpr)
			}
			if exprToCheck != nil && !exprHasAgg(exprToCheck) {
				if err := validateHavingClause(exprToCheck, a.groupKeys, nil); err != nil {
					return err
				}
			}
		}
	}

	if a.colIndexMap == nil {
		a.colIndexMap = NewColumnIndexMapFromCols(a.Schema())
	}
	a.tuples = nil
	a.idx = 0

	a.initAccumulatorNeeds()
	if a.child != nil {
		childMap := a.child.ColIndexMap()
		if childMap == nil {
			childMap = NewColumnIndexMapFromCols(a.child.Schema())
		}
		for i := range a.fields {
			f := &a.fields[i]
			f.argColIdx = -1
			if f.argCol != "" && childMap != nil {
				f.argColIdx = resolveColIndex(childMap, f.argCol)
			}
		}
		if len(a.groupKeys) > 0 {
			schema := a.child.Schema()
			a.groupKeyIdx = make([]int, len(a.groupKeys))
			a.groupKeyIsCS = make([]bool, len(a.groupKeys))
			for i, gk := range a.groupKeys {
				cleanGk := strings.ToLower(sqlutil.CleanIdentifier(gk))
				if colIdx := sqlutil.FindTopLevelKeyword(cleanGk, "collate"); colIdx != -1 {
					cleanGk = strings.TrimSpace(cleanGk[:colIdx])
				}
				idx := -1
				for ci, col := range schema {
					if strings.ToLower(sqlutil.CleanIdentifier(col)) == cleanGk {
						idx = ci
						break
					}
				}
				a.groupKeyIdx[i] = idx
				if colIdx := sqlutil.FindTopLevelKeyword(gk, "COLLATE"); colIdx != -1 {
					collName := strings.TrimSpace(gk[colIdx+7:])
					a.groupKeyIsCS[i] = sqlutil.IsCaseSensitiveCollation(collName)
				}
			}
		}
	}

	var orderedAccs []*groupAccumulator

	nKeys := len(a.groupKeys)
	if nKeys > MaxGroupByColumns {
		_ = a.child.Close()
		return fmt.Errorf("%w: %d columns specified, maximum allowed is %d",
			ErrTooManyGroupByColumns, nKeys, MaxGroupByColumns)
	}
	var masks []uint64
	if nKeys == 0 {
		masks = []uint64{0}
	} else {
		switch a.groupByMod {
		case proc.GroupByRollup:
			masks = make([]uint64, nKeys+1)
			for lvl := nKeys; lvl >= 0; lvl-- {
				if lvl == 0 {
					masks[nKeys-lvl] = 0
				} else if lvl == 64 {
					masks[nKeys-lvl] = ^uint64(0)
				} else {
					masks[nKeys-lvl] = (uint64(1) << lvl) - 1
				}
			}
		case proc.GroupByCube:
			if nKeys > MaxCubeColumns {
				_ = a.child.Close()
				return fmt.Errorf("%w: %d columns specified, maximum allowed is %d (limit: 4,096 grouping sets)",
					ErrTooManyCubeColumns, nKeys, MaxCubeColumns)
			}
			totalComb := 1 << nKeys
			masks = make([]uint64, totalComb)
			for m := 0; m < totalComb; m++ {
				masks[m] = uint64(m)
			}
		default:
			if nKeys == 64 {
				masks = []uint64{^uint64(0)}
			} else {
				masks = []uint64{(uint64(1) << nKeys) - 1}
			}
		}
	}

	hasRows := false
	inChunk := GetChunk()
	defer PutChunk(inChunk)

	// [PERF-JOIN-AGG-OPERATOR-FUSION-1] Operator Fusion Fast-Path:
	// If child is HashJoin with a materialized small build table and matching group key,
	// fuse join probe and aggregation into a single direct-slot loop, bypassing 100% of intermediate merged tuples.
	if fused, fErr := a.tryJoinAggregateFusion(ctx, &orderedAccs, &hasRows); fused {
		if fErr != nil {
			return fErr
		}
	} else if len(a.groupKeys) == 0 {
		// Fast-path: Global aggregation (no GROUP BY). Avoids string formatting and map hashing per tuple.
		globalAcc := a.newGroupAccumulator(Tuple{}, 0)
		orderedAccs = append(orderedAccs, globalAcc)

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
	} else if len(a.groupKeys) == 1 && a.groupByMod == proc.GroupByNormal && len(masks) == 1 && masks[0] == 1 {
		// Fast-path: Single integer key GROUP BY without cube/rollup.
		// Bypasses string formatting and string map lookups per row.
		intGroups := make(map[int64]*groupAccumulator)
		var nullAcc *groupAccumulator
		var slotMap map[uint64]*groupSlot
		fallbackToSlotHash := false

		keyIdx := -1
		if a.groupKeyIdx != nil && len(a.groupKeyIdx) > 0 {
			keyIdx = a.groupKeyIdx[0]
		}
		singleKeyName := a.groupKeys[0]
		if keyIdx < 0 && a.child != nil {
			keyIdx = resolveColIndex(a.child.ColIndexMap(), singleKeyName)
		}

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
				if fallbackToSlotHash {
					a.accumulateTupleSlotHash(tuple, 1, slotMap, &orderedAccs)
					continue
				}

				var val any
				if tuple.Lazy == nil && keyIdx >= 0 && keyIdx < len(tuple.Values) {
					val = tuple.Values[keyIdx]
				} else {
					val, _ = lookupVal(tuple, singleKeyName)
				}

				if val == nil {
					if nullAcc == nil {
						nullAcc = a.newGroupAccumulator(tuple, 1)
						orderedAccs = append(orderedAccs, nullAcc)
					}
					a.accumulateTuple(nullAcc, tuple)
					continue
				}

				if sv, ok := val.(storage.Value); ok {
					if sv.IsNull {
						if nullAcc == nil {
							nullAcc = a.newGroupAccumulator(tuple, 1)
							orderedAccs = append(orderedAccs, nullAcc)
						}
						a.accumulateTuple(nullAcc, tuple)
						continue
					}
				}

				if iKey, isInt := extractIntKey(val); isInt {
					acc, exists := intGroups[iKey]
					if !exists {
						acc = a.newGroupAccumulator(tuple, 1)
						intGroups[iKey] = acc
						orderedAccs = append(orderedAccs, acc)
					}
					a.accumulateTuple(acc, tuple)
				} else {
					fallbackToSlotHash = true
					slotMap = make(map[uint64]*groupSlot, len(orderedAccs)+16)
					for _, existingAcc := range orderedAccs {
						h := a.computeTupleGroupHash(existingAcc.groupTuple, 1)
						slotMap[h] = &groupSlot{acc: existingAcc, hash: h}
					}
					a.accumulateTupleSlotHash(tuple, 1, slotMap, &orderedAccs)
				}
			}

			if err == io.EOF {
				break
			}
		}
	} else {
		slotMap := make(map[uint64]*groupSlot)
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
					a.accumulateTupleSlotHash(tuple, mask, slotMap, &orderedAccs)
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
				res, err := proc.EvalRowExpr(f.parsed, evalMap, nil)
				if err != nil {
					if strings.Contains(err.Error(), "division by zero") || strings.Contains(err.Error(), "divide by zero") {
						vals[fIdx] = nil
					} else {
						if _, isCol := f.parsed.(*proc.ColumnRefExpr); !isCol {
							return err
						}
					}
				} else {
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
		sort.SliceStable(orderedAccs, func(i, j int) bool {
			accA := orderedAccs[i]
			accB := orderedAccs[j]
			for k := 0; k < nKeys; k++ {
				actA := (accA.groupMask & (uint64(1) << k)) != 0
				actB := (accB.groupMask & (uint64(1) << k)) != 0
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
	for _, acc := range orderedAccs {
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
						if (acc.groupMask & (uint64(1) << kIdx)) != 0 {
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
					val = storage.DecimalValueFromDecimal(acc.decSums[fIdx])
				} else if acc.hasIntSum[fIdx] {
					val = acc.intSums[fIdx]
				} else {
					s := acc.sums[fIdx]
					if s == math.Trunc(s) {
						val = int64(s)
					} else {
						val = s
					}
				}
			case aggTotal:
				if acc.hasIntSum[fIdx] {
					val = acc.intSums[fIdx]
				} else {
					s := acc.sums[fIdx]
					if s == math.Trunc(s) {
						val = int64(s)
					} else {
						val = s
					}
				}
			case aggGroupConcat:
				list := acc.strLists[fIdx]
				if f.orderByExpr != nil && len(acc.strOrders[fIdx]) > 0 {
					items := acc.strOrders[fIdx]
					desc := f.orderByDesc
					sort.SliceStable(items, func(i, j int) bool {
						cmp := CompareValues(items[i].key, items[j].key)
						if desc {
							return cmp > 0
						}
						return cmp < 0
					})
					list = make([]string, len(items))
					for i, it := range items {
						list[i] = it.str
					}
				}
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
						val = storage.DecimalValueFromDecimal(quo)
					} else {
						val = storage.DecimalValueFromDecimal(acc.decSums[fIdx])
					}
				} else if acc.hasIntSum[fIdx] && acc.intSums[fIdx]%acc.counts[fIdx] == 0 {
					val = acc.intSums[fIdx] / acc.counts[fIdx]
				} else {
					total := acc.sums[fIdx]
					if acc.hasIntSum[fIdx] {
						total += float64(acc.intSums[fIdx])
					}
					avg := total / float64(acc.counts[fIdx])
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
				} else if len(acc.tDigests) > fIdx && acc.tDigests[fIdx] != nil {
					td := acc.tDigests[fIdx]
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

		// Pre-check HAVING filter using group keys and computed aggregate values.
		// If HAVING fails, avoid executing projection expressions (Pass 2) for this discarded group.
		if a.havingCond != "" {
			hEvalMap := acc.groupTuple.ToEvalMap()
			if hEvalMap == nil {
				hEvalMap = make(map[string]any, len(a.fields)*2)
			}
			for afIdx, af := range a.fields {
				if !af.isLiteral {
					if af.rawExpr != "" {
						hEvalMap[af.rawExpr] = vals[afIdx]
						hEvalMap[strings.ToUpper(af.rawExpr)] = vals[afIdx]
						hEvalMap[strings.ToLower(af.rawExpr)] = vals[afIdx]
					}
					if af.alias != "" {
						hEvalMap[af.alias] = vals[afIdx]
						hEvalMap[strings.ToUpper(af.alias)] = vals[afIdx]
						hEvalMap[strings.ToLower(af.alias)] = vals[afIdx]
					}
					if af.argCol != "" {
						hEvalMap[af.argCol] = vals[afIdx]
						hEvalMap[strings.ToUpper(af.argCol)] = vals[afIdx]
						hEvalMap[strings.ToLower(af.argCol)] = vals[afIdx]
					}
				}
			}
			havingPass := true
			if a.parsedHaving != nil {
				havingPass = proc.EvalBoolExpr(a.parsedHaving, hEvalMap, nil)
			} else {
				row := Tuple{Values: vals, Index: a.colIndexMap}
				havingPass = EvalConditionTuple(row, a.havingCond)
			}
			if !havingPass {
				continue
			}
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
					if (acc.groupMask & (uint64(1) << kIdx)) != 0 {
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
							if af.origExpr != "" {
								evalMap[af.origExpr] = vals[afIdx]
								evalMap[strings.ToUpper(af.origExpr)] = vals[afIdx]
								evalMap[strings.ToLower(af.origExpr)] = vals[afIdx]
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
					if strings.Contains(err.Error(), "division by zero") || strings.Contains(err.Error(), "divide by zero") {
						vals[fIdx] = nil
					} else {
						if _, isCol := f.parsed.(*proc.ColumnRefExpr); !isCol {
							return err
						}
						vals[fIdx] = nil
					}
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

type groupSlot struct {
	acc  *groupAccumulator
	hash uint64
	next *groupSlot
}

const (
	fnvOffset64 uint64 = 14695981039346656037
	fnvPrime64  uint64 = 1099511628211
)

func hashUint64(h, val uint64) uint64 {
	h ^= val
	h *= fnvPrime64
	return h
}

func hashStringVal(h uint64, s string, isCS bool) uint64 {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !isCS && c >= 'A' && c <= 'Z' {
			c += 32
		}
		h ^= uint64(c)
		h *= fnvPrime64
	}
	return h
}

func (a *AggregateIterator) computeTupleGroupHash(tuple Tuple, mask uint64) uint64 {
	h := hashUint64(fnvOffset64, mask)
	for i, k := range a.groupKeys {
		h = hashUint64(h, uint64(i))
		if (mask & (uint64(1) << i)) == 0 {
			h = hashUint64(h, 0xFAFAFAFAFAFAFAFA)
			continue
		}
		var val any
		if tuple.Lazy == nil && a.groupKeyIdx != nil && i < len(a.groupKeyIdx) && a.groupKeyIdx[i] >= 0 && a.groupKeyIdx[i] < len(tuple.Values) {
			val = tuple.Values[a.groupKeyIdx[i]]
		} else {
			val, _ = lookupVal(tuple, k)
		}
		isCS := false
		if a.groupKeyIsCS != nil && i < len(a.groupKeyIsCS) {
			isCS = a.groupKeyIsCS[i]
		}

		if val == nil {
			h = hashUint64(h, 0xFEFEFEFEFEFEFEFE)
			continue
		}
		switch v := val.(type) {
		case storage.Value:
			if v.IsNull {
				h = hashUint64(h, 0xFEFEFEFEFEFEFEFE)
			} else if v.Type == storage.TypeInt {
				h = hashUint64(hashUint64(h, 1), uint64(v.IVal))
			} else if v.Type == storage.TypeVarChar {
				h = hashUint64(h, 2)
				h = hashStringVal(h, v.SVal, isCS)
			} else if v.Type == storage.TypeFloat {
				h = hashUint64(hashUint64(h, 3), math.Float64bits(v.FVal))
			} else if v.Type == storage.TypeBool {
				var bVal uint64
				if v.BVal {
					bVal = 1
				}
				h = hashUint64(hashUint64(h, 4), bVal)
			} else {
				h = hashUint64(h, 5)
				h = hashStringVal(h, v.String(), isCS)
			}
		case int64:
			h = hashUint64(hashUint64(h, 1), uint64(v))
		case int:
			h = hashUint64(hashUint64(h, 1), uint64(v))
		case int32:
			h = hashUint64(hashUint64(h, 1), uint64(v))
		case int16:
			h = hashUint64(hashUint64(h, 1), uint64(v))
		case int8:
			h = hashUint64(hashUint64(h, 1), uint64(v))
		case uint64:
			h = hashUint64(hashUint64(h, 1), v)
		case uint32:
			h = hashUint64(hashUint64(h, 1), uint64(v))
		case uint16:
			h = hashUint64(hashUint64(h, 1), uint64(v))
		case uint8:
			h = hashUint64(hashUint64(h, 1), uint64(v))
		case uint:
			h = hashUint64(hashUint64(h, 1), uint64(v))
		case string:
			h = hashUint64(h, 2)
			h = hashStringVal(h, v, isCS)
		case float64:
			h = hashUint64(hashUint64(h, 3), math.Float64bits(v))
		case float32:
			h = hashUint64(hashUint64(h, 3), math.Float64bits(float64(v)))
		case bool:
			var bVal uint64
			if v {
				bVal = 1
			}
			h = hashUint64(hashUint64(h, 4), bVal)
		default:
			h = hashUint64(h, 5)
			h = hashStringVal(h, fmt.Sprintf("%v", val), isCS)
		}
	}
	return h
}

func (a *AggregateIterator) matchTupleKeys(t1, t2 Tuple, mask uint64) bool {
	for i, k := range a.groupKeys {
		if (mask & (uint64(1) << i)) == 0 {
			continue
		}
		var v1, v2 any
		if t1.Lazy == nil && a.groupKeyIdx != nil && i < len(a.groupKeyIdx) && a.groupKeyIdx[i] >= 0 && a.groupKeyIdx[i] < len(t1.Values) {
			v1 = t1.Values[a.groupKeyIdx[i]]
		} else {
			v1, _ = lookupVal(t1, k)
		}
		if t2.Lazy == nil && a.groupKeyIdx != nil && i < len(a.groupKeyIdx) && a.groupKeyIdx[i] >= 0 && a.groupKeyIdx[i] < len(t2.Values) {
			v2 = t2.Values[a.groupKeyIdx[i]]
		} else {
			v2, _ = lookupVal(t2, k)
		}
		isCS := false
		if a.groupKeyIsCS != nil && i < len(a.groupKeyIsCS) {
			isCS = a.groupKeyIsCS[i]
		}
		if !equalGroupVal(v1, v2, isCS) {
			return false
		}
	}
	return true
}

func equalGroupVal(v1, v2 any, isCS bool) bool {
	if v1 == nil {
		if v2 == nil {
			return true
		}
		if sv2, ok := v2.(storage.Value); ok {
			return sv2.IsNull
		}
		return false
	}
	if v2 == nil {
		if sv1, ok := v1.(storage.Value); ok {
			return sv1.IsNull
		}
		return false
	}

	if sv1, ok := v1.(storage.Value); ok {
		if sv1.IsNull {
			if sv2, ok2 := v2.(storage.Value); ok2 {
				return sv2.IsNull
			}
			return false
		}
		if sv2, ok2 := v2.(storage.Value); ok2 {
			if sv2.IsNull {
				return false
			}
			if sv1.Type == storage.TypeVarChar && sv2.Type == storage.TypeVarChar && !isCS {
				return strings.EqualFold(sv1.SVal, sv2.SVal)
			}
			return sv1.Compare(sv2) == 0
		}
		v1 = sv1.Interface()
	}
	if sv2, ok := v2.(storage.Value); ok {
		if sv2.IsNull {
			return false
		}
		v2 = sv2.Interface()
	}

	i1, isInt1 := extractIntKey(v1)
	i2, isInt2 := extractIntKey(v2)
	if isInt1 && isInt2 {
		return i1 == i2
	}

	switch a := v1.(type) {
	case string:
		if b, ok := v2.(string); ok {
			if isCS {
				return a == b
			}
			return strings.EqualFold(a, b)
		}
	case float64:
		if b, ok := v2.(float64); ok {
			return a == b
		}
	case bool:
		if b, ok := v2.(bool); ok {
			return a == b
		}
	}
	return CompareValues(v1, v2) == 0
}

func (a *AggregateIterator) accumulateTupleSlotHash(tuple Tuple, mask uint64, slotMap map[uint64]*groupSlot, orderedAccs *[]*groupAccumulator) *groupAccumulator {
	h := a.computeTupleGroupHash(tuple, mask)
	head, exists := slotMap[h]
	if exists {
		slot := head
		for slot != nil {
			if slot.acc.groupMask == mask && a.matchTupleKeys(slot.acc.groupTuple, tuple, mask) {
				a.accumulateTuple(slot.acc, tuple)
				return slot.acc
			}
			if slot.next == nil {
				break
			}
			slot = slot.next
		}
		acc := a.newGroupAccumulator(tuple, mask)
		slot.next = &groupSlot{acc: acc, hash: h}
		*orderedAccs = append(*orderedAccs, acc)
		a.accumulateTuple(acc, tuple)
		return acc
	}

	acc := a.newGroupAccumulator(tuple, mask)
	slotMap[h] = &groupSlot{acc: acc, hash: h}
	*orderedAccs = append(*orderedAccs, acc)
	a.accumulateTuple(acc, tuple)
	return acc
}

func (a *AggregateIterator) accumulateTuple(acc *groupAccumulator, tuple Tuple) {
	for fIdx, f := range a.fields {
		if f.isLiteral || f.fnType == aggGrouping {
			continue
		}
		var filterExpr proc.Expr
		if fnCall, ok := f.fnExpr.(*proc.FunctionCallExpr); ok {
			filterExpr = fnCall.Filter
		} else if wg, ok := f.fnExpr.(*proc.WithinGroupExpr); ok && wg.Function != nil {
			filterExpr = wg.Function.Filter
		}
		if filterExpr != nil {
			if !EvalTupleBoolExpr(tuple, filterExpr) {
				continue
			}
		}
		if f.isStar {
			acc.counts[fIdx]++
			continue
		}

		var val any
		var ok bool
		if f.parsed != nil {
			val, ok = EvalTupleExpr(tuple, f.parsed)
		} else if tuple.Lazy == nil && f.argColIdx >= 0 && f.argColIdx < len(tuple.Values) {
			val = tuple.Values[f.argColIdx]
			ok = (val != nil)
		} else {
			val, ok = lookupVal(tuple, f.argCol)
		}
		if !ok || val == nil {
			continue
		}

		if f.isDistinct {
			if acc.distincts == nil {
				acc.distincts = make([]map[string]struct{}, len(a.fields))
			}
			set := acc.distincts[fIdx]
			if set == nil {
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
			switch v := val.(type) {
			case int64:
				if !acc.isDecimal[fIdx] {
					acc.intSums[fIdx] += v
					acc.hasIntSum[fIdx] = true
					acc.counts[fIdx]++
					continue
				}
			case int:
				if !acc.isDecimal[fIdx] {
					acc.intSums[fIdx] += int64(v)
					acc.hasIntSum[fIdx] = true
					acc.counts[fIdx]++
					continue
				}
			case float64:
				if !acc.isDecimal[fIdx] {
					if acc.hasIntSum[fIdx] {
						acc.sums[fIdx] += float64(acc.intSums[fIdx])
						acc.intSums[fIdx] = 0
						acc.hasIntSum[fIdx] = false
					}
					acc.sums[fIdx] += v
					acc.counts[fIdx]++
					continue
				}
			case storage.Value:
				if v.IsNull {
					continue
				}
				if v.Type == storage.TypeInt && !acc.isDecimal[fIdx] {
					acc.intSums[fIdx] += v.IVal
					acc.hasIntSum[fIdx] = true
					acc.counts[fIdx]++
					continue
				}
				if v.Type == storage.TypeFloat && !acc.isDecimal[fIdx] {
					if acc.hasIntSum[fIdx] {
						acc.sums[fIdx] += float64(acc.intSums[fIdx])
						acc.intSums[fIdx] = 0
						acc.hasIntSum[fIdx] = false
					}
					acc.sums[fIdx] += v.FVal
					acc.counts[fIdx]++
					continue
				}
				if v.Type == storage.TypeDecimal {
					acc.isDecimal[fIdx] = true
					if acc.hasIntSum[fIdx] {
						acc.decSums[fIdx] = acc.decSums[fIdx].Add(storage.NewDecimalFromInt(acc.intSums[fIdx], 0))
						acc.intSums[fIdx] = 0
						acc.hasIntSum[fIdx] = false
					} else if acc.sums[fIdx] != 0 {
						acc.decSums[fIdx] = acc.decSums[fIdx].Add(storage.NewDecimalFromFloat(acc.sums[fIdx], 4))
						acc.sums[fIdx] = 0
					}
					acc.decSums[fIdx] = acc.decSums[fIdx].Add(v.DVal)
					acc.counts[fIdx]++
					continue
				}
			}

			if isDecValue(val) {
				dec, _ := tryDecimal(val)
				if !acc.isDecimal[fIdx] {
					acc.isDecimal[fIdx] = true
					if acc.hasIntSum[fIdx] {
						acc.decSums[fIdx] = acc.decSums[fIdx].Add(storage.NewDecimalFromInt(acc.intSums[fIdx], 0))
						acc.intSums[fIdx] = 0
						acc.hasIntSum[fIdx] = false
					} else if acc.sums[fIdx] != 0 {
						acc.decSums[fIdx] = acc.decSums[fIdx].Add(storage.NewDecimalFromFloat(acc.sums[fIdx], 4))
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
					if acc.hasIntSum[fIdx] {
						acc.sums[fIdx] += float64(acc.intSums[fIdx])
						acc.intSums[fIdx] = 0
						acc.hasIntSum[fIdx] = false
					}
					acc.sums[fIdx] += fVal
				}
			}
			acc.counts[fIdx]++
		case aggAvg:
			switch v := val.(type) {
			case int64:
				if !acc.isDecimal[fIdx] {
					acc.intSums[fIdx] += v
					acc.hasIntSum[fIdx] = true
					acc.counts[fIdx]++
					continue
				}
			case int:
				if !acc.isDecimal[fIdx] {
					acc.intSums[fIdx] += int64(v)
					acc.hasIntSum[fIdx] = true
					acc.counts[fIdx]++
					continue
				}
			case float64:
				if !acc.isDecimal[fIdx] {
					if acc.hasIntSum[fIdx] {
						acc.sums[fIdx] += float64(acc.intSums[fIdx])
						acc.intSums[fIdx] = 0
						acc.hasIntSum[fIdx] = false
					}
					acc.sums[fIdx] += v
					acc.counts[fIdx]++
					continue
				}
			case storage.Value:
				if v.IsNull {
					continue
				}
				if v.Type == storage.TypeInt && !acc.isDecimal[fIdx] {
					acc.intSums[fIdx] += v.IVal
					acc.hasIntSum[fIdx] = true
					acc.counts[fIdx]++
					continue
				}
				if v.Type == storage.TypeFloat && !acc.isDecimal[fIdx] {
					if acc.hasIntSum[fIdx] {
						acc.sums[fIdx] += float64(acc.intSums[fIdx])
						acc.intSums[fIdx] = 0
						acc.hasIntSum[fIdx] = false
					}
					acc.sums[fIdx] += v.FVal
					acc.counts[fIdx]++
					continue
				}
				if v.Type == storage.TypeDecimal {
					acc.isDecimal[fIdx] = true
					if acc.hasIntSum[fIdx] {
						acc.decSums[fIdx] = acc.decSums[fIdx].Add(storage.NewDecimalFromInt(acc.intSums[fIdx], 0))
						acc.intSums[fIdx] = 0
						acc.hasIntSum[fIdx] = false
					} else if acc.sums[fIdx] != 0 {
						acc.decSums[fIdx] = acc.decSums[fIdx].Add(storage.NewDecimalFromFloat(acc.sums[fIdx], 4))
						acc.sums[fIdx] = 0
					}
					acc.decSums[fIdx] = acc.decSums[fIdx].Add(v.DVal)
					acc.counts[fIdx]++
					continue
				}
			}

			if isDecValue(val) {
				dec, _ := tryDecimal(val)
				if !acc.isDecimal[fIdx] {
					acc.isDecimal[fIdx] = true
					if acc.hasIntSum[fIdx] {
						acc.decSums[fIdx] = acc.decSums[fIdx].Add(storage.NewDecimalFromInt(acc.intSums[fIdx], 0))
						acc.intSums[fIdx] = 0
						acc.hasIntSum[fIdx] = false
					} else if acc.sums[fIdx] != 0 {
						acc.decSums[fIdx] = acc.decSums[fIdx].Add(storage.NewDecimalFromFloat(acc.sums[fIdx], 4))
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
					if acc.hasIntSum[fIdx] {
						acc.sums[fIdx] += float64(acc.intSums[fIdx])
						acc.intSums[fIdx] = 0
						acc.hasIntSum[fIdx] = false
					}
					acc.sums[fIdx] += fVal
				}
			}
			acc.counts[fIdx]++
		case aggMin:
			if acc.mins[fIdx] == nil {
				acc.mins[fIdx] = val
			} else {
				switch cur := acc.mins[fIdx].(type) {
				case int64:
					if v, ok := val.(int64); ok {
						if v < cur {
							acc.mins[fIdx] = v
						}
						continue
					}
				case float64:
					if v, ok := val.(float64); ok {
						if v < cur {
							acc.mins[fIdx] = v
						}
						continue
					}
				}
				if CompareValues(val, acc.mins[fIdx]) < 0 {
					acc.mins[fIdx] = val
				}
			}
		case aggMax:
			if acc.maxs[fIdx] == nil {
				acc.maxs[fIdx] = val
			} else {
				switch cur := acc.maxs[fIdx].(type) {
				case int64:
					if v, ok := val.(int64); ok {
						if v > cur {
							acc.maxs[fIdx] = v
						}
						continue
					}
				case float64:
					if v, ok := val.(float64); ok {
						if v > cur {
							acc.maxs[fIdx] = v
						}
						continue
					}
				}
				if CompareValues(val, acc.maxs[fIdx]) > 0 {
					acc.maxs[fIdx] = val
				}
			}
		case aggGroupConcat:
			if val != nil {
				strVal := fmt.Sprintf("%v", val)
				acc.strLists[fIdx] = append(acc.strLists[fIdx], strVal)
				if f.orderByExpr != nil {
					if acc.strOrders == nil {
						acc.strOrders = make([][]strOrderItem, len(a.fields))
					}
					ordVal, ok := EvalTupleExpr(tuple, f.orderByExpr)
					if !ok {
						ordVal, _ = lookupVal(tuple, f.orderByExpr.String())
					}
					acc.strOrders[fIdx] = append(acc.strOrders[fIdx], strOrderItem{str: strVal, key: ordVal})
				}
				acc.counts[fIdx]++
			}
		case aggPercentileCont, aggPercentileDisc, aggMedian:
			if fVal, ok := toFloat64(val); ok {
				acc.values[fIdx] = append(acc.values[fIdx], fVal)
				acc.counts[fIdx]++
			}
		case aggApproxPercentile:
			if fVal, ok := toFloat64(val); ok {
				if acc.tDigests == nil {
					acc.tDigests = make([]*TDigest, len(a.fields))
				}
				td := acc.tDigests[fIdx]
				if td == nil {
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

func exprHasAgg(expr proc.Expr) bool {
	if expr == nil {
		return false
	}
	hasAgg := false
	var walk func(e proc.Expr)
	walk = func(e proc.Expr) {
		if e == nil || hasAgg {
			return
		}
		switch node := e.(type) {
		case *proc.FunctionCallExpr:
			fnName := strings.ToUpper(node.Name)
			if fnName == "COUNT" || fnName == "SUM" || fnName == "TOTAL" || fnName == "AVG" ||
				fnName == "MIN" || fnName == "MAX" || fnName == "GROUP_CONCAT" || fnName == "STRING_AGG" ||
				fnName == "STDEV" || fnName == "STDEVP" || fnName == "VAR" || fnName == "VARP" ||
				fnName == "MEDIAN" || fnName == "PERCENTILE_CONT" || fnName == "PERCENTILE_DISC" || fnName == "APPROX_PERCENTILE" {
				hasAgg = true
				return
			}
			for _, arg := range node.Args {
				walk(arg)
			}
		case *proc.WithinGroupExpr:
			hasAgg = true
			return
		case *proc.BinaryExpr:
			walk(node.Left)
			walk(node.Right)
		case *proc.UnaryExpr:
			walk(node.Expr)
		case *proc.CaseExpr:
			walk(node.Operand)
			for _, w := range node.Whens {
				walk(w.Condition)
				walk(w.Result)
			}
			walk(node.Else)
		case *proc.BetweenExpr:
			walk(node.Expr)
			walk(node.Low)
			walk(node.High)
		case *proc.InExpr:
			walk(node.Expr)
			for _, item := range node.List {
				walk(item)
			}
		case *proc.LikeExpr:
			walk(node.Expr)
			walk(node.Pattern)
		}
	}
	walk(expr)
	return hasAgg
}

func isConstant(e proc.Expr) bool {
	if _, ok := e.(*proc.LiteralExpr); ok {
		return true
	}
	if u, ok := e.(*proc.UnaryExpr); ok && (u.Op == proc.TokenPlus || u.Op == proc.TokenMinus) {
		return isConstant(u.Expr)
	}
	return false
}

func isConstantNonNull(e proc.Expr) bool {
	if e == nil {
		return false
	}
	folded := proc.FoldConstants(e)
	if lit, ok := folded.(*proc.LiteralExpr); ok && !lit.Val.IsNull {
		return true
	}
	if cast, ok := e.(*proc.CastExpr); ok {
		return isConstantNonNull(cast.Expr)
	}
	if lit, ok := e.(*proc.LiteralExpr); ok && !lit.Val.IsNull {
		return true
	}
	if u, ok := e.(*proc.UnaryExpr); ok && (u.Op == proc.TokenPlus || u.Op == proc.TokenMinus) {
		return isConstantNonNull(u.Expr)
	}
	if b, ok := e.(*proc.BinaryExpr); ok {
		if b.Op == proc.TokenPlus || b.Op == proc.TokenMinus || b.Op == proc.TokenAsterisk {
			return isConstantNonNull(b.Left) && isConstantNonNull(b.Right)
		}
	}
	if fn, ok := e.(*proc.FunctionCallExpr); ok {
		uName := strings.ToUpper(fn.Name)
		if uName == "COALESCE" || uName == "ISNULL" || uName == "NVL" || uName == "IFNULL" {
			if len(fn.Args) > 0 && isConstantNonNull(fn.Args[0]) {
				return true
			}
		}
	}
	return false
}

func isConstantNull(e proc.Expr) bool {
	if e == nil {
		return false
	}
	folded := proc.FoldConstants(e)
	if lit, ok := folded.(*proc.LiteralExpr); ok && lit.Val.IsNull {
		return true
	}
	if u, ok := e.(*proc.UnaryExpr); ok && (u.Op == proc.TokenPlus || u.Op == proc.TokenMinus) {
		return isConstantNull(u.Expr)
	}
	if cast, ok := e.(*proc.CastExpr); ok {
		return isConstantNull(cast.Expr)
	}
	return false
}

func validateHavingClause(expr proc.Expr, groupKeys []string, fields []aggField) error {
	if expr == nil {
		return nil
	}

	norm := func(s string) string {
		return strings.ToLower(strings.ReplaceAll(cleanIdentifierParts(s), " ", ""))
	}
	groupKeySet := make(map[string]struct{}, (len(groupKeys)+len(fields))*4)
	for _, k := range groupKeys {
		trimmed := strings.TrimSpace(k)
		if colIdx := sqlutil.FindTopLevelKeyword(trimmed, "COLLATE"); colIdx != -1 {
			trimmed = strings.TrimSpace(trimmed[:colIdx])
		}
		clean := strings.ToLower(cleanIdentifierParts(trimmed))
		groupKeySet[clean] = struct{}{}
		groupKeySet[norm(trimmed)] = struct{}{}
		if idx := strings.Index(clean, "."); idx != -1 {
			groupKeySet[clean[idx+1:]] = struct{}{}
			groupKeySet[norm(clean[idx+1:])] = struct{}{}
		}
	}
	for _, f := range fields {
		if f.alias != "" {
			clean := strings.ToLower(cleanIdentifierParts(f.alias))
			groupKeySet[clean] = struct{}{}
			groupKeySet[norm(f.alias)] = struct{}{}
			if idx := strings.Index(clean, "."); idx != -1 {
				groupKeySet[clean[idx+1:]] = struct{}{}
				groupKeySet[norm(clean[idx+1:])] = struct{}{}
			}
		}
	}

	var check func(e proc.Expr, inAgg bool) error
	check = func(e proc.Expr, inAgg bool) error {
		if e == nil {
			return nil
		}
		if _, ok := groupKeySet[norm(e.String())]; ok {
			return nil
		}
		switch node := e.(type) {
		case *proc.ColumnRefExpr:
			if inAgg {
				return nil
			}
			colName := strings.ToLower(cleanIdentifierParts(node.Name))
			bareCol := colName
			if idx := strings.Index(bareCol, "."); idx != -1 {
				bareCol = bareCol[idx+1:]
			}
			if _, ok := groupKeySet[colName]; ok {
				return nil
			}
			if _, ok := groupKeySet[bareCol]; ok {
				return nil
			}
			return fmt.Errorf("column %q must appear in the GROUP BY clause or be used in an aggregate function", node.Name)

		case *proc.FunctionCallExpr:
			fnName := strings.ToUpper(node.Name)
			if fnName == "COALESCE" || fnName == "ISNULL" || fnName == "NVL" || fnName == "IFNULL" {
				for _, arg := range node.Args {
					if isConstantNonNull(arg) {
						break
					}
					if err := check(arg, inAgg); err != nil {
						return err
					}
				}
				return nil
			}
			if fnName == "COUNT" || fnName == "SUM" || fnName == "TOTAL" || fnName == "AVG" ||
				fnName == "MIN" || fnName == "MAX" || fnName == "GROUP_CONCAT" || fnName == "STRING_AGG" ||
				fnName == "STDEV" || fnName == "STDEVP" || fnName == "VAR" || fnName == "VARP" {
				for _, arg := range node.Args {
					if err := check(arg, true); err != nil {
						return err
					}
				}
				return nil
			}
			for _, arg := range node.Args {
				if err := check(arg, inAgg); err != nil {
					return err
				}
			}
			return nil

		case *proc.BinaryExpr:
			if err := check(node.Left, inAgg); err != nil {
				return err
			}
			return check(node.Right, inAgg)

		case *proc.UnaryExpr:
			return check(node.Expr, inAgg)

		case *proc.CaseExpr:
			allWhensNull := len(node.Whens) > 0
			for _, w := range node.Whens {
				if !isConstantNull(w.Condition) {
					allWhensNull = false
					break
				}
			}
			if allWhensNull {
				if node.Else != nil {
					return check(node.Else, inAgg)
				}
				return nil
			}

			if node.Operand != nil {
				if err := check(node.Operand, inAgg); err != nil {
					return err
				}
			}
			for _, w := range node.Whens {
				if isConstantNull(w.Condition) {
					continue
				}
				if node.Operand != nil && isConstant(node.Operand) && isConstant(w.Condition) {
					vOp, _ := proc.EvalRowExpr(node.Operand, nil, nil)
					vCond, _ := proc.EvalRowExpr(w.Condition, nil, nil)
					if vOp != vCond {
						continue
					}
				}
				if err := check(w.Condition, inAgg); err != nil {
					return err
				}
				if err := check(w.Result, inAgg); err != nil {
					return err
				}
			}
			if node.Else != nil {
				if err := check(node.Else, inAgg); err != nil {
					return err
				}
			}
			return nil

		case *proc.BetweenExpr:
			if err := check(node.Expr, inAgg); err != nil {
				return err
			}
			if err := check(node.Low, inAgg); err != nil {
				return err
			}
			return check(node.High, inAgg)

		case *proc.InExpr:
			if err := check(node.Expr, inAgg); err != nil {
				return err
			}
			for _, it := range node.List {
				if err := check(it, inAgg); err != nil {
					return err
				}
			}
			return nil

		case *proc.CastExpr:
			return check(node.Expr, inAgg)

		case *proc.IsNullExpr:
			return check(node.Expr, inAgg)

		case *proc.LikeExpr:
			if err := check(node.Expr, inAgg); err != nil {
				return err
			}
			return check(node.Pattern, inAgg)
		}
		return nil
	}

	return check(expr, false)
}

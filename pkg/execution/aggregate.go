package execution

import (
	"context"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"sqlion/pkg/proc"
	"sqlion/pkg/storage"
)

type aggFuncType int

const (
	aggCount aggFuncType = iota
	aggSum
	aggAvg
	aggMin
	aggMax
	aggGrouping
	aggPercentileCont
	aggPercentileDisc
	aggApproxPercentile
	aggMedian
)

type aggField struct {
	fnType     aggFuncType
	argCol     string
	alias      string
	rawExpr    string
	isStar     bool
	isLiteral  bool
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
	tDigests   map[int]*TDigest
}

// AggregateIterator evaluates GROUP BY, aggregate functions (COUNT, SUM, AVG, MIN, MAX), and HAVING filters.
type AggregateIterator struct {
	child       Iterator
	groupKeys   []string
	groupByMod  proc.GroupByModifier
	projections []string
	havingCond  string
	fields      []aggField
	tuples      []Tuple
	idx         int
	schema      []string
	colIndexMap *ColumnIndexMap
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
	if dot := strings.LastIndex(a, "."); dot != -1 {
		a = a[dot+1:]
	}
	if dot := strings.LastIndex(b, "."); dot != -1 {
		b = b[dot+1:]
	}
	return strings.EqualFold(a, b)
}

func (a *AggregateIterator) parseFields() {
	countStarRe := regexp.MustCompile(`(?i)^\s*COUNT\s*\(\s*\*\s*\)(?:\s+(?:AS\s+)?([a-zA-Z0-9_]+))?\s*$`)
	aggCallRe := regexp.MustCompile(`(?i)^\s*(COUNT|SUM|AVG|MIN|MAX)\s*\(\s*(?:DISTINCT\s+)?([^\)]+)\s*\)(?:\s+(?:AS\s+)?([a-zA-Z0-9_]+))?\s*$`)
	groupingRe := regexp.MustCompile(`(?i)^\s*GROUPING\s*\(\s*([^\)]+)\s*\)(?:\s+(?:AS\s+)?([a-zA-Z0-9_]+))?\s*$`)

	for _, p := range a.projections {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		if m := countStarRe.FindStringSubmatch(p); len(m) == 2 {
			alias := m[1]
			if alias == "" {
				alias = "count"
			}
			a.fields = append(a.fields, aggField{fnType: aggCount, isStar: true, alias: alias, rawExpr: "COUNT(*)"})
			a.schema = append(a.schema, alias)
			continue
		}

		if m := aggCallRe.FindStringSubmatch(p); len(m) == 4 {
			fnName := strings.ToUpper(m[1])
			arg := strings.Trim(strings.TrimSpace(m[2]), "\"`[]")
			alias := m[3]
			if alias == "" {
				alias = strings.ToLower(fnName)
			}
			rawCall := fmt.Sprintf("%s(%s)", fnName, arg)

			var fnType aggFuncType
			switch fnName {
			case "COUNT":
				fnType = aggCount
			case "SUM":
				fnType = aggSum
			case "AVG":
				fnType = aggAvg
			case "MIN":
				fnType = aggMin
			case "MAX":
				fnType = aggMax
			}

			a.fields = append(a.fields, aggField{fnType: fnType, argCol: arg, alias: alias, rawExpr: rawCall})
			a.schema = append(a.schema, alias)
			continue
		}

		if m := groupingRe.FindStringSubmatch(p); len(m) == 3 {
			arg := strings.Trim(strings.TrimSpace(m[1]), "\"`[]")
			alias := m[2]
			if alias == "" {
				alias = fmt.Sprintf("grouping_%s", arg)
			}
			rawCall := fmt.Sprintf("GROUPING(%s)", arg)
			a.fields = append(a.fields, aggField{fnType: aggGrouping, argCol: arg, alias: alias, rawExpr: rawCall})
			a.schema = append(a.schema, alias)
			continue
		}

		medianRe := regexp.MustCompile(`(?i)^\s*MEDIAN\s*\(\s*(?:DISTINCT\s+)?([^\)]+)\s*\)(?:\s+(?:AS\s+)?([a-zA-Z0-9_]+))?\s*$`)
		if m := medianRe.FindStringSubmatch(p); len(m) == 3 {
			arg := strings.Trim(strings.TrimSpace(m[1]), "\"`[]")
			alias := m[2]
			if alias == "" {
				alias = "median"
			}
			a.fields = append(a.fields, aggField{fnType: aggMedian, argCol: arg, alias: alias, percentile: 0.5, rawExpr: p})
			a.schema = append(a.schema, alias)
			continue
		}

		approxPercRe := regexp.MustCompile(`(?i)^\s*APPROX_PERCENTILE\s*\(\s*([^,\)]+)\s*,\s*([0-9\.]+)(?:\s*,\s*([0-9\.]+))?\s*\)(?:\s+(?:AS\s+)?([a-zA-Z0-9_]+))?\s*$`)
		if m := approxPercRe.FindStringSubmatch(p); len(m) == 5 {
			arg := strings.Trim(strings.TrimSpace(m[1]), "\"`[]")
			pVal, _ := strconv.ParseFloat(m[2], 64)
			alias := m[4]
			if alias == "" {
				alias = "approx_percentile"
			}
			a.fields = append(a.fields, aggField{fnType: aggApproxPercentile, argCol: arg, alias: alias, percentile: pVal, rawExpr: p})
			a.schema = append(a.schema, alias)
			continue
		}

		percCont2Re := regexp.MustCompile(`(?i)^\s*PERCENTILE_CONT\s*\(\s*([^,\)]+)\s*,\s*([0-9\.]+)\s*\)(?:\s+(?:AS\s+)?([a-zA-Z0-9_]+))?\s*$`)
		if m := percCont2Re.FindStringSubmatch(p); len(m) == 4 {
			arg := strings.Trim(strings.TrimSpace(m[1]), "\"`[]")
			pVal, _ := strconv.ParseFloat(m[2], 64)
			alias := m[3]
			if alias == "" {
				alias = "percentile_cont"
			}
			a.fields = append(a.fields, aggField{fnType: aggPercentileCont, argCol: arg, alias: alias, percentile: pVal, rawExpr: p})
			a.schema = append(a.schema, alias)
			continue
		}

		percDisc2Re := regexp.MustCompile(`(?i)^\s*PERCENTILE_DISC\s*\(\s*([^,\)]+)\s*,\s*([0-9\.]+)\s*\)(?:\s+(?:AS\s+)?([a-zA-Z0-9_]+))?\s*$`)
		if m := percDisc2Re.FindStringSubmatch(p); len(m) == 4 {
			arg := strings.Trim(strings.TrimSpace(m[1]), "\"`[]")
			pVal, _ := strconv.ParseFloat(m[2], 64)
			alias := m[3]
			if alias == "" {
				alias = "percentile_disc"
			}
			a.fields = append(a.fields, aggField{fnType: aggPercentileDisc, argCol: arg, alias: alias, percentile: pVal, rawExpr: p})
			a.schema = append(a.schema, alias)
			continue
		}

		percContWithinRe := regexp.MustCompile(`(?i)^\s*PERCENTILE_CONT\s*\(\s*([0-9\.]+)\s*\)\s*WITHIN\s+GROUP\s*\(\s*ORDER\s+BY\s+([^\)\s]+)(?:\s+(?:ASC|DESC))?\s*\)(?:\s+(?:AS\s+)?([a-zA-Z0-9_]+))?\s*$`)
		if m := percContWithinRe.FindStringSubmatch(p); len(m) == 4 {
			pVal, _ := strconv.ParseFloat(m[1], 64)
			arg := strings.Trim(strings.TrimSpace(m[2]), "\"`[]")
			alias := m[3]
			if alias == "" {
				alias = "percentile_cont"
			}
			a.fields = append(a.fields, aggField{fnType: aggPercentileCont, argCol: arg, alias: alias, percentile: pVal, rawExpr: p})
			a.schema = append(a.schema, alias)
			continue
		}

		percDiscWithinRe := regexp.MustCompile(`(?i)^\s*PERCENTILE_DISC\s*\(\s*([0-9\.]+)\s*\)\s*WITHIN\s+GROUP\s*\(\s*ORDER\s+BY\s+([^\)\s]+)(?:\s+(?:ASC|DESC))?\s*\)(?:\s+(?:AS\s+)?([a-zA-Z0-9_]+))?\s*$`)
		if m := percDiscWithinRe.FindStringSubmatch(p); len(m) == 4 {
			pVal, _ := strconv.ParseFloat(m[1], 64)
			arg := strings.Trim(strings.TrimSpace(m[2]), "\"`[]")
			alias := m[3]
			if alias == "" {
				alias = "percentile_disc"
			}
			a.fields = append(a.fields, aggField{fnType: aggPercentileDisc, argCol: arg, alias: alias, percentile: pVal, rawExpr: p})
			a.schema = append(a.schema, alias)
			continue
		}

		// Group-by column or passthrough
		alias := p
		if asIdx := strings.Index(strings.ToUpper(p), " AS "); asIdx != -1 {
			alias = strings.Trim(strings.TrimSpace(p[asIdx+4:]), "\"`[]")
			p = strings.TrimSpace(p[:asIdx])
		} else {
			if dotIdx := strings.Index(alias, "."); dotIdx != -1 {
				alias = alias[dotIdx+1:]
			}
			alias = strings.Trim(alias, "\"`[]")
		}
		var parsedExpr proc.Expr
		if strings.Contains(p, "->") || strings.Contains(p, "(") {
			parser := proc.NewParser(proc.NewLexer(p))
			if ast := parser.ParseExpr(); ast != nil && len(parser.Errors()) == 0 {
				parsedExpr = ast
			}
		}
		a.fields = append(a.fields, aggField{isLiteral: true, argCol: p, alias: alias, rawExpr: p, parsed: parsedExpr})
		a.schema = append(a.schema, alias)
	}

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
	inChunk := NewChunk(DefaultChunkSize)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := PullChunk(ctx, a.child, inChunk, DefaultChunkSize)
		if err != nil && err != io.EOF {
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
						tDigests:   make(map[int]*TDigest),
					}
					groups[groupKeyStr] = acc
					groupOrder = append(groupOrder, groupKeyStr)
				}

				// Accumulate into acc
				for fIdx, f := range a.fields {
					if f.isLiteral || f.fnType == aggGrouping {
						continue
					}
					if f.isStar {
						acc.counts[fIdx]++
						continue
					}

					val, ok := lookupVal(tuple, f.argCol)
					if !ok || val == nil {
						continue
					}

					switch f.fnType {
					case aggCount:
						acc.counts[fIdx]++
					case aggSum:
						if isDecValue(val) {
							dec, _ := tryDecimal(val)
							acc.isDecimal[fIdx] = true
							cur := acc.decSums[fIdx]
							acc.decSums[fIdx] = cur.Add(dec)
						} else {
							fVal, _ := toFloat64(val)
							acc.sums[fIdx] += fVal
						}
						acc.counts[fIdx]++
					case aggAvg:
						if isDecValue(val) {
							dec, _ := tryDecimal(val)
							acc.isDecimal[fIdx] = true
							cur := acc.decSums[fIdx]
							acc.decSums[fIdx] = cur.Add(dec)
						} else {
							fVal, _ := toFloat64(val)
							acc.sums[fIdx] += fVal
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
		}

		if err == io.EOF {
			break
		}
	}

	// If no rows returned and no GROUP BY (global aggregation: e.g. SELECT COUNT(*) FROM empty)
	if !hasRows && len(a.groupKeys) == 0 {
		vals := make([]any, len(a.fields))
		for fIdx, f := range a.fields {
			if f.isStar || f.fnType == aggCount {
				vals[fIdx] = int64(0)
			} else {
				vals[fIdx] = nil
			}
		}
		emptyTuple := Tuple{Values: vals, Index: a.colIndexMap}
		if a.havingCond == "" || EvalConditionTuple(emptyTuple, a.havingCond) {
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
		vals := make([]any, len(a.fields))

		for fIdx, f := range a.fields {
			if f.isLiteral {
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
					res, err := proc.EvalRowExpr(f.parsed, acc.groupTuple.ToEvalMap(), nil)
					if err == nil {
						vals[fIdx] = res
					} else {
						vals[fIdx] = nil
					}
				} else {
					v, _ := lookupVal(acc.groupTuple, f.argCol)
					vals[fIdx] = v
				}
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

		row := Tuple{Values: vals, Index: a.colIndexMap}
		if a.havingCond == "" || EvalConditionTuple(row, a.havingCond) {
			a.tuples = append(a.tuples, row)
		}
	}

	return nil
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

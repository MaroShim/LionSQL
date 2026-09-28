package execution

import (
	"context"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"

	"sqlion/pkg/storage"
)

type aggFuncType int

const (
	aggCount aggFuncType = iota
	aggSum
	aggAvg
	aggMin
	aggMax
)

type aggField struct {
	fnType    aggFuncType
	argCol    string
	alias     string
	rawExpr   string
	isStar    bool
	isLiteral bool
}

type groupAccumulator struct {
	groupTuple Tuple
	counts     map[int]int64
	sums       map[int]float64
	decSums    map[int]storage.Decimal
	isDecimal  map[int]bool
	mins       map[int]any
	maxs       map[int]any
}

// AggregateIterator evaluates GROUP BY, aggregate functions (COUNT, SUM, AVG, MIN, MAX), and HAVING filters.
type AggregateIterator struct {
	child       Iterator
	groupKeys   []string
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
	a := &AggregateIterator{
		child:       child,
		groupKeys:   groupKeys,
		projections: projections,
		havingCond:  strings.TrimSpace(havingCond),
	}
	a.parseFields()
	return a
}

func (a *AggregateIterator) parseFields() {
	countStarRe := regexp.MustCompile(`(?i)^\s*COUNT\s*\(\s*\*\s*\)(?:\s+(?:AS\s+)?([a-zA-Z0-9_]+))?\s*$`)
	aggCallRe := regexp.MustCompile(`(?i)^\s*(COUNT|SUM|AVG|MIN|MAX)\s*\(\s*(?:DISTINCT\s+)?([^\)]+)\s*\)(?:\s+(?:AS\s+)?([a-zA-Z0-9_]+))?\s*$`)

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
		a.fields = append(a.fields, aggField{isLiteral: true, argCol: p, alias: alias, rawExpr: p})
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

	hasRows := false
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		tuple, err := a.child.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		hasRows = true

		// Build group key
		var gk strings.Builder
		for i, k := range a.groupKeys {
			if i > 0 {
				gk.WriteByte('\x00')
			}
			val, _ := lookupVal(tuple, k)
			gk.WriteString(fmt.Sprintf("%v", val))
		}
		groupKeyStr := gk.String()

		acc, exists := groups[groupKeyStr]
		if !exists {
			acc = &groupAccumulator{
				groupTuple: tuple,
				counts:     make(map[int]int64),
				sums:       make(map[int]float64),
				decSums:    make(map[int]storage.Decimal),
				isDecimal:  make(map[int]bool),
				mins:       make(map[int]any),
				maxs:       make(map[int]any),
			}
			groups[groupKeyStr] = acc
			groupOrder = append(groupOrder, groupKeyStr)
		}

		// Accumulate
		for fIdx, f := range a.fields {
			if f.isLiteral {
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
			}
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

	// Materialize groups into result tuples
	for _, gk := range groupOrder {
		acc := groups[gk]
		vals := make([]any, len(a.fields))

		for fIdx, f := range a.fields {
			if f.isLiteral {
				v, _ := lookupVal(acc.groupTuple, f.argCol)
				vals[fIdx] = v
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

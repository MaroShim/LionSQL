package execution

import (
	"context"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
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
}

func (a *AggregateIterator) Open(ctx context.Context) error {
	if a.child == nil {
		return fmt.Errorf("aggregate: child iterator is nil")
	}
	if err := a.child.Open(ctx); err != nil {
		return err
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
				fVal, _ := toFloat64(val)
				acc.sums[fIdx] += fVal
				acc.counts[fIdx]++
			case aggAvg:
				fVal, _ := toFloat64(val)
				acc.sums[fIdx] += fVal
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
		emptyTuple := make(Tuple, len(a.fields))
		for _, f := range a.fields {
			if f.isStar || f.fnType == aggCount {
				emptyTuple[f.alias] = int64(0)
			} else {
				emptyTuple[f.alias] = nil
			}
		}
		if a.havingCond == "" || EvalConditionTuple(emptyTuple, a.havingCond) {
			a.tuples = append(a.tuples, emptyTuple)
		}
		return nil
	}

	// Materialize groups into result tuples
	for _, gk := range groupOrder {
		acc := groups[gk]
		row := make(Tuple, len(a.fields))

		for fIdx, f := range a.fields {
			if f.isLiteral {
				v, _ := lookupVal(acc.groupTuple, f.argCol)
				row[f.alias] = v
				row[strings.ToLower(f.alias)] = v
				if f.argCol != "" {
					row[f.argCol] = v
					row[strings.ToLower(f.argCol)] = v
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

			row[f.alias] = val
			row[strings.ToLower(f.alias)] = val
			if f.rawExpr != "" {
				row[f.rawExpr] = val
				row[strings.ToLower(f.rawExpr)] = val
			}
		}

		if a.havingCond == "" || EvalConditionTuple(row, a.havingCond) {
			a.tuples = append(a.tuples, row)
		}
	}

	return nil
}

func (a *AggregateIterator) Next(ctx context.Context) (Tuple, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if a.idx >= len(a.tuples) {
		return nil, io.EOF
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

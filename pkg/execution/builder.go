package execution

import (
	"fmt"
	"strconv"
	"strings"

	"lionsql/pkg/planner"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

// TableGetter fetches TableSchema by table name.
type TableGetter func(name string) (*storage.TableSchema, error)

// BuildExecutor recursively compiles a planner.PlanNode physical plan tree into an executable Volcano Iterator using default options.
func BuildExecutor(plan *planner.PlanNode, tx storage.Transaction, getTable TableGetter) (Iterator, error) {
	return BuildExecutorWithOptions(plan, tx, getTable, DefaultExecutorOptions())
}

// BuildInstrumentedExecutor compiles a planner.PlanNode physical plan tree into an executable
// Volcano Iterator tree where every operator node is wrapped with an InstrumentedIterator for runtime profiling (OBS-2).
func BuildInstrumentedExecutor(plan *planner.PlanNode, tx storage.Transaction, getTable TableGetter, opts ExecutorOptions) (Iterator, error) {
	return buildExecutorRecursive(plan, tx, getTable, opts, true)
}

// BuildExecutorWithOptions compiles a planner.PlanNode physical plan tree into an executable Volcano Iterator with custom memory limits.
func BuildExecutorWithOptions(plan *planner.PlanNode, tx storage.Transaction, getTable TableGetter, opts ExecutorOptions) (Iterator, error) {
	return buildExecutorRecursive(plan, tx, getTable, opts, false)
}

func buildExecutorRecursive(plan *planner.PlanNode, tx storage.Transaction, getTable TableGetter, opts ExecutorOptions, instrument bool) (Iterator, error) {
	if plan == nil {
		return nil, fmt.Errorf("cannot build executor: plan is nil")
	}

	var op Iterator
	var err error

	switch plan.Type {
	case planner.NodeTableScan:
		schema, sErr := getTable(plan.Relation)
		if sErr != nil {
			return nil, fmt.Errorf("table scan schema lookup failed for %q: %w", plan.Relation, sErr)
		}
		var sIter *SeqScanIterator
		if len(plan.Partitions) > 0 {
			sIter = NewSeqScanIteratorWithPartitions(tx, plan.Relation, plan.Alias, schema, plan.Partitions)
		} else {
			sIter = NewSeqScanIterator(tx, plan.Relation, plan.Alias, schema)
		}
		if opts.Arena != nil {
			sIter.SetArena(opts.Arena)
		}
		op = sIter

	case planner.NodeIndexScan, planner.NodeIndexOnlyScan:
		schema, sErr := getTable(plan.Relation)
		if sErr != nil {
			return nil, fmt.Errorf("index scan schema lookup failed for %q: %w", plan.Relation, sErr)
		}

		low, high := planner.ParseIndexCond(plan.IndexCond, schema, plan.IndexName)
		it := NewIndexScanIterator(tx, plan.Relation, plan.Alias, plan.IndexName, low, high, schema)
		it.IndexOnly = (plan.Type == planner.NodeIndexOnlyScan)
		if opts.Arena != nil {
			it.SetArena(opts.Arena)
		}
		if plan.FilterCond != "" {
			op = NewFilterIterator(it, plan.FilterCond)
		} else {
			op = it
		}

	case planner.NodeFilter:
		if len(plan.Children) == 0 {
			return nil, fmt.Errorf("filter node has no children")
		}
		child, cErr := buildExecutorRecursive(plan.Children[0], tx, getTable, opts, instrument)
		if cErr != nil {
			return nil, cErr
		}
		op = NewFilterIterator(child, plan.FilterCond)

	case planner.NodeProjection:
		if len(plan.Children) == 0 {
			return nil, fmt.Errorf("projection node has no children")
		}
		child, cErr := buildExecutorRecursive(plan.Children[0], tx, getTable, opts, instrument)
		if cErr != nil {
			return nil, cErr
		}
		op = NewProjectionIterator(child, plan.Projections)

	case planner.NodeLimit:
		if len(plan.Children) == 0 {
			return nil, fmt.Errorf("limit node has no children")
		}
		child, cErr := buildExecutorRecursive(plan.Children[0], tx, getTable, opts, instrument)
		if cErr != nil {
			return nil, cErr
		}
		op = NewLimitIterator(child, plan.LimitCount, plan.OffsetCount)

	case planner.NodeSort:
		if len(plan.Children) == 0 {
			return nil, fmt.Errorf("sort node has no children")
		}
		child, cErr := buildExecutorRecursive(plan.Children[0], tx, getTable, opts, instrument)
		if cErr != nil {
			return nil, cErr
		}
		orderClause := plan.SortKey
		if plan.SortOrder != "" {
			orderClause += " " + plan.SortOrder
		}
		sIter := NewSortIterator(child, orderClause)
		if opts.WorkMem > 0 {
			sIter.SetWorkMem(opts.WorkMem)
		}
		if opts.TempDir != "" {
			sIter.SetTempDir(opts.TempDir)
		}
		if opts.Arena != nil {
			sIter.SetArena(opts.Arena)
		}
		op = sIter

	case planner.NodeAggregate:
		if len(plan.Children) == 0 {
			return nil, fmt.Errorf("aggregate node has no children")
		}
		child, cErr := buildExecutorRecursive(plan.Children[0], tx, getTable, opts, instrument)
		if cErr != nil {
			return nil, cErr
		}
		aIter := NewAggregateIteratorWithModifier(child, plan.GroupKeys, plan.GroupByModifier, plan.Projections, plan.HavingCond)
		if opts.Arena != nil {
			aIter.SetArena(opts.Arena)
		}
		op = aIter

	case planner.NodeWindow:
		if len(plan.Children) == 0 {
			return nil, fmt.Errorf("window node has no children")
		}
		child, cErr := buildExecutorRecursive(plan.Children[0], tx, getTable, opts, instrument)
		if cErr != nil {
			return nil, cErr
		}
		op = NewWindowIterator(child, plan.WindowDefs)

	case planner.NodeHashJoin:
		if len(plan.Children) < 2 {
			return nil, fmt.Errorf("hash join requires at least 2 children")
		}
		leftIter, lErr := buildExecutorRecursive(plan.Children[0], tx, getTable, opts, instrument)
		if lErr != nil {
			return nil, lErr
		}

		// Second child may be NodeHash, whose child is the inner scan
		var rightIter Iterator
		if plan.Children[1].Type == planner.NodeHash && len(plan.Children[1].Children) > 0 {
			hashChild, hErr := buildExecutorRecursive(plan.Children[1].Children[0], tx, getTable, opts, instrument)
			if hErr != nil {
				return nil, hErr
			}
			if instrument {
				rightIter = NewInstrumentedIterator(hashChild, plan.Children[1])
			} else {
				rightIter = hashChild
			}
		} else {
			var rErr error
			rightIter, rErr = buildExecutorRecursive(plan.Children[1], tx, getTable, opts, instrument)
			if rErr != nil {
				return nil, rErr
			}
		}

		leftKey, rightKey := ParseJoinCond(plan.HashCond)
		if leftKey == "" && rightKey == "" {
			leftKey, rightKey = ParseJoinCond(plan.JoinCond)
		}

		hIter := NewHashJoinIterator(leftIter, rightIter, plan.JoinType, leftKey, rightKey)
		if opts.WorkMem > 0 {
			hIter.SetWorkMem(opts.WorkMem)
		}
		if opts.Arena != nil {
			hIter.SetArena(opts.Arena)
		}
		op = hIter

	case planner.NodeMergeJoin:
		if len(plan.Children) < 2 {
			return nil, fmt.Errorf("merge join requires at least 2 children")
		}
		leftIter, lErr := buildExecutorRecursive(plan.Children[0], tx, getTable, opts, instrument)
		if lErr != nil {
			return nil, lErr
		}
		rightIter, rErr := buildExecutorRecursive(plan.Children[1], tx, getTable, opts, instrument)
		if rErr != nil {
			return nil, rErr
		}

		leftKey, rightKey := ParseJoinCond(plan.MergeCond)
		if leftKey == "" && rightKey == "" {
			leftKey, rightKey = ParseJoinCond(plan.JoinCond)
		}

		if !isPlanSortedOnKey(plan.Children[0], leftKey) {
			sIter := NewSortIterator(leftIter, leftKey+" ASC")
			if opts.WorkMem > 0 {
				sIter.SetWorkMem(opts.WorkMem)
			}
			if opts.TempDir != "" {
				sIter.SetTempDir(opts.TempDir)
			}
			leftIter = sIter
		}
		if !isPlanSortedOnKey(plan.Children[1], rightKey) {
			sIter := NewSortIterator(rightIter, rightKey+" ASC")
			if opts.WorkMem > 0 {
				sIter.SetWorkMem(opts.WorkMem)
			}
			if opts.TempDir != "" {
				sIter.SetTempDir(opts.TempDir)
			}
			rightIter = sIter
		}

		op = NewMergeJoinIterator(leftIter, rightIter, plan.JoinType, leftKey, rightKey)

	case planner.NodeNestedLoop:
		if len(plan.Children) < 2 {
			return nil, fmt.Errorf("nested loop join requires at least 2 children")
		}
		outerIter, oErr := buildExecutorRecursive(plan.Children[0], tx, getTable, opts, instrument)
		if oErr != nil {
			return nil, oErr
		}

		innerPlan := plan.Children[1]
		innerSchema, sErr := getTable(innerPlan.Relation)
		if sErr != nil {
			return nil, fmt.Errorf("nested loop inner schema error: %w", sErr)
		}

		leftKey, rightKey := ParseJoinCond(plan.JoinCond)

		var innerIdx *storage.IndexSchema
		if innerPlan.IndexName != "" {
			if idx, _, found := innerSchema.FindIndex(innerPlan.IndexName); found {
				innerIdx = idx
			}
		} else if rightKey != "" {
			for i := range innerSchema.Indexes {
				idx := &innerSchema.Indexes[i]
				if len(idx.Columns) > 0 && strings.EqualFold(idx.Columns[0], rightKey) {
					innerIdx = idx
					break
				}
			}
		}

		if innerIdx != nil {
			nIter := NewIndexNestedLoopJoinIterator(outerIter, tx, innerPlan.Relation, innerPlan.Alias, innerIdx, innerSchema, leftKey, plan.JoinType)
			if innerPlan.FilterCond != "" {
				nIter.SetFilter(innerPlan.FilterCond)
			}
			if instrument {
				nIter.SetInnerPlan(innerPlan)
			}
			op = nIter
		} else {
			// Fallback to Hash Join if inner index is absent
			innerScan := NewSeqScanIterator(tx, innerPlan.Relation, innerPlan.Alias, innerSchema)
			var rIter Iterator = innerScan
			if instrument {
				rIter = NewInstrumentedIterator(innerScan, innerPlan)
			}
			op = NewHashJoinIterator(outerIter, rIter, plan.JoinType, leftKey, rightKey)
		}

	case planner.NodeSetOp:
		if len(plan.Children) < 2 {
			return nil, fmt.Errorf("set operator requires at least 2 children")
		}
		leftIter, lErr := buildExecutorRecursive(plan.Children[0], tx, getTable, opts, instrument)
		if lErr != nil {
			return nil, lErr
		}
		rightIter, rErr := buildExecutorRecursive(plan.Children[1], tx, getTable, opts, instrument)
		if rErr != nil {
			_ = leftIter.Close()
			return nil, rErr
		}
		setOpType, sErr := ParseSetOpType(plan.SetOp)
		if sErr != nil {
			_ = leftIter.Close()
			_ = rightIter.Close()
			return nil, sErr
		}
		op = NewSetOpIterator(leftIter, rightIter, setOpType)

	default:
		return nil, fmt.Errorf("unsupported plan node type: %s", plan.Type)
	}

	if err != nil {
		return nil, err
	}
	if instrument && op != nil {
		return NewInstrumentedIterator(op, plan), nil
	}
	return op, nil
}

func isColumnIdent(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// String literal
	if (strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'")) ||
		(strings.HasPrefix(s, "N'") && strings.HasSuffix(s, "'")) {
		return false
	}
	// Numeric literal
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return false
	}
	// Boolean/NULL literals
	lower := strings.ToLower(s)
	if lower == "null" || lower == "true" || lower == "false" {
		return false
	}
	// Operator characters that indicate an expression rather than an identifier
	if strings.ContainsAny(s, "+*/%^") {
		return false
	}
	return true
}

// ParseJoinCond extracts left and right join keys from an equijoin condition expression.
func ParseJoinCond(cond string) (string, string) {
	conds := sqlutil.SplitAndConditions(cond)
	var leftKeys, rightKeys []string
	for _, c := range conds {
		l, r := sqlutil.SplitJoinCond(c)
		if l != "" && r != "" && isColumnIdent(l) && isColumnIdent(r) {
			leftKeys = append(leftKeys, l)
			rightKeys = append(rightKeys, r)
		}
	}
	if len(leftKeys) > 0 {
		return strings.Join(leftKeys, ", "), strings.Join(rightKeys, ", ")
	}
	l, r := sqlutil.SplitJoinCond(cond)
	if l != "" && r != "" && isColumnIdent(l) && isColumnIdent(r) {
		return l, r
	}
	return "", ""
}

func isPlanSortedOnKey(plan *planner.PlanNode, key string) bool {
	if plan == nil || key == "" {
		return false
	}
	cleanKey := key
	if idx := strings.Index(cleanKey, "."); idx != -1 {
		cleanKey = cleanKey[idx+1:]
	}
	cleanKey = strings.ToLower(strings.Trim(cleanKey, "\"`[]"))

	if plan.Type == planner.NodeSort {
		sKey := plan.SortKey
		if idx := strings.Index(sKey, "."); idx != -1 {
			sKey = sKey[idx+1:]
		}
		sKey = strings.ToLower(strings.Trim(sKey, "\"`[]"))
		return sKey == cleanKey
	}

	if plan.Type == planner.NodeIndexScan || plan.Type == planner.NodeIndexOnlyScan {
		iCond := strings.ToLower(plan.IndexCond)
		if strings.Contains(iCond, cleanKey) {
			return true
		}
	}
	return false
}

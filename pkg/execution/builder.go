package execution

import (
	"fmt"
	"regexp"
	"strings"

	"sqlion/pkg/planner"
	"sqlion/pkg/storage"
)

// TableGetter fetches TableSchema by table name.
type TableGetter func(name string) (*storage.TableSchema, error)

// BuildExecutor recursively compiles a planner.PlanNode physical plan tree into an executable Volcano Iterator.
func BuildExecutor(plan *planner.PlanNode, tx storage.Transaction, getTable TableGetter) (Iterator, error) {
	if plan == nil {
		return nil, fmt.Errorf("cannot build executor: plan is nil")
	}

	switch plan.Type {
	case planner.NodeTableScan:
		schema, err := getTable(plan.Relation)
		if err != nil {
			return nil, fmt.Errorf("table scan schema lookup failed for %q: %w", plan.Relation, err)
		}
		return NewSeqScanIterator(tx, plan.Relation, plan.Alias, schema), nil

	case planner.NodeIndexScan:
		schema, err := getTable(plan.Relation)
		if err != nil {
			return nil, fmt.Errorf("index scan schema lookup failed for %q: %w", plan.Relation, err)
		}

		low, high := parseIndexCond(plan.IndexCond, schema)
		return NewIndexScanIterator(tx, plan.Relation, plan.Alias, plan.IndexName, low, high, schema), nil

	case planner.NodeFilter:
		if len(plan.Children) == 0 {
			return nil, fmt.Errorf("filter node has no children")
		}
		child, err := BuildExecutor(plan.Children[0], tx, getTable)
		if err != nil {
			return nil, err
		}
		return NewFilterIterator(child, plan.FilterCond), nil

	case planner.NodeProjection:
		if len(plan.Children) == 0 {
			return nil, fmt.Errorf("projection node has no children")
		}
		child, err := BuildExecutor(plan.Children[0], tx, getTable)
		if err != nil {
			return nil, err
		}
		return NewProjectionIterator(child, plan.Projections), nil

	case planner.NodeLimit:
		if len(plan.Children) == 0 {
			return nil, fmt.Errorf("limit node has no children")
		}
		child, err := BuildExecutor(plan.Children[0], tx, getTable)
		if err != nil {
			return nil, err
		}
		return NewLimitIterator(child, plan.LimitCount, 0), nil

	case planner.NodeSort:
		if len(plan.Children) == 0 {
			return nil, fmt.Errorf("sort node has no children")
		}
		child, err := BuildExecutor(plan.Children[0], tx, getTable)
		if err != nil {
			return nil, err
		}
		orderClause := plan.SortKey
		if plan.SortOrder != "" {
			orderClause += " " + plan.SortOrder
		}
		return NewSortIterator(child, orderClause), nil

	case planner.NodeAggregate:
		if len(plan.Children) == 0 {
			return nil, fmt.Errorf("aggregate node has no children")
		}
		child, err := BuildExecutor(plan.Children[0], tx, getTable)
		if err != nil {
			return nil, err
		}
		return NewAggregateIterator(child, plan.GroupKeys, plan.Projections, plan.HavingCond), nil

	case planner.NodeHashJoin:
		if len(plan.Children) < 2 {
			return nil, fmt.Errorf("hash join requires at least 2 children")
		}
		leftIter, err := BuildExecutor(plan.Children[0], tx, getTable)
		if err != nil {
			return nil, err
		}

		// Second child may be NodeHash, whose child is the inner scan
		var rightIter Iterator
		if plan.Children[1].Type == planner.NodeHash && len(plan.Children[1].Children) > 0 {
			rightIter, err = BuildExecutor(plan.Children[1].Children[0], tx, getTable)
		} else {
			rightIter, err = BuildExecutor(plan.Children[1], tx, getTable)
		}
		if err != nil {
			return nil, err
		}

		leftKey, rightKey := parseJoinCond(plan.HashCond)
		if leftKey == "" && rightKey == "" {
			leftKey, rightKey = parseJoinCond(plan.JoinCond)
		}

		return NewHashJoinIterator(leftIter, rightIter, plan.JoinType, leftKey, rightKey), nil

	case planner.NodeNestedLoop:
		if len(plan.Children) < 2 {
			return nil, fmt.Errorf("nested loop join requires at least 2 children")
		}
		outerIter, err := BuildExecutor(plan.Children[0], tx, getTable)
		if err != nil {
			return nil, err
		}

		innerPlan := plan.Children[1]
		innerSchema, err := getTable(innerPlan.Relation)
		if err != nil {
			return nil, fmt.Errorf("nested loop inner schema error: %w", err)
		}

		leftKey, _ := parseJoinCond(plan.JoinCond)

		var innerIdx *storage.IndexSchema
		if idx, _, found := innerSchema.FindIndex(innerPlan.IndexName); found {
			innerIdx = idx
		} else if len(innerSchema.Indexes) > 0 {
			innerIdx = &innerSchema.Indexes[0]
		}

		if innerIdx != nil {
			return NewIndexNestedLoopJoinIterator(outerIter, tx, innerPlan.Relation, innerPlan.Alias, innerIdx, innerSchema, leftKey, plan.JoinType), nil
		}

		// Fallback to Hash Join if inner index is absent
		innerScan := NewSeqScanIterator(tx, innerPlan.Relation, innerPlan.Alias, innerSchema)
		_, rightKey := parseJoinCond(plan.JoinCond)
		return NewHashJoinIterator(outerIter, innerScan, plan.JoinType, leftKey, rightKey), nil

	default:
		return nil, fmt.Errorf("unsupported plan node type: %s", plan.Type)
	}
}

func parseIndexCond(cond string, schema *storage.TableSchema) (*storage.Value, *storage.Value) {
	cond = strings.Trim(cond, "()")
	eqRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_\.]+)\s*=\s*(.+)$`)
	if m := eqRe.FindStringSubmatch(cond); len(m) == 3 {
		colName := m[1]
		if idx := strings.Index(colName, "."); idx != -1 {
			colName = colName[idx+1:]
		}
		valStr := strings.Trim(strings.TrimSpace(m[2]), "'\"")

		if schema != nil {
			if colMeta, _, found := schema.FindColumn(colName); found && colMeta != nil {
				parsed := parseValueByType(valStr, colMeta.Type)
				return &parsed, &parsed
			}
		}
		v := storage.StringValue(valStr)
		return &v, &v
	}
	return nil, nil
}

func parseValueByType(s string, t storage.DataType) storage.Value {
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

func parseJoinCond(cond string) (string, string) {
	cond = strings.Trim(strings.TrimSpace(cond), "()")
	parts := strings.Split(cond, "=")
	if len(parts) == 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	return "", ""
}

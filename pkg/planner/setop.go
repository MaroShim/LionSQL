package planner

import (
	"fmt"
	"strconv"
	"strings"

	"lionsql/pkg/sqlutil"
)

// SetOpSegment represents a single query branch within a set operation chain.
type SetOpSegment struct {
	Op    string // "", "UNION", "UNION ALL", "INTERSECT", "EXCEPT"
	Query string
}

// SetOpQuery represents a parsed chain of set operations with optional trailing ORDER BY, LIMIT, and OFFSET.
type SetOpQuery struct {
	Segments []SetOpSegment
	OrderBy  string
	Limit    int64 // default: -1 (unlimited)
	Offset   int64 // default: 0
}

// HasTopLevelSetOp quickly checks if the query contains top-level set operators without full parsing.
func HasTopLevelSetOp(query string) bool {
	q, ok := FindTopLevelSetOps(query)
	return ok && q != nil && len(q.Segments) >= 2
}

// FindTopLevelSetOps parses top-level set operators (UNION, UNION ALL, INTERSECT, EXCEPT, MINUS)
// ignoring operators enclosed within parentheses (subqueries, CTEs), string literals, or comments.
func FindTopLevelSetOps(query string) (*SetOpQuery, bool) {
	trimmed := strings.TrimRight(strings.TrimSpace(query), ";")
	if len(trimmed) < 7 { // Shortest: "SELECT"
		return nil, false
	}

	cleanSQL, trailingOrderBy, trailingLimit, trailingOffset := extractTrailingOrderByAndLimit(trimmed)
	cleanSQL = sqlutil.UnwrapOuterParens(cleanSQL)

	// List of set operator keywords in preference order (multi-word first)
	setOpKeywords := []string{
		"INTERSECT DISTINCT",
		"INTERSECT ALL",
		"EXCEPT DISTINCT",
		"EXCEPT ALL",
		"UNION DISTINCT",
		"UNION ALL",
		"INTERSECT",
		"EXCEPT",
		"MINUS",
		"UNION",
	}

	matches := sqlutil.FindAllTopLevelAnyKeywords(cleanSQL, setOpKeywords...)
	if len(matches) == 0 {
		return nil, false
	}

	var segments []SetOpSegment
	segStart := 0
	lastOp := ""

	for _, m := range matches {
		chunk := strings.TrimSpace(cleanSQL[segStart:m.Pos])
		segments = append(segments, SetOpSegment{
			Op:    lastOp,
			Query: sqlutil.UnwrapOuterParens(chunk),
		})
		lastOp = normalizeSetOp(m.Keyword)
		segStart = m.End
	}

	// Last segment contains remaining SQL
	lastChunk := strings.TrimSpace(cleanSQL[segStart:])
	lastQuery, extraOrder, extraLimit, extraOffset := extractTrailingOrderByAndLimit(lastChunk)
	if trailingOrderBy == "" {
		trailingOrderBy = extraOrder
	}
	if trailingLimit < 0 {
		trailingLimit = extraLimit
	}
	if trailingOffset <= 0 {
		trailingOffset = extraOffset
	}

	segments = append(segments, SetOpSegment{
		Op:    lastOp,
		Query: sqlutil.UnwrapOuterParens(lastQuery),
	})

	if len(segments) < 2 {
		return nil, false
	}

	return &SetOpQuery{
		Segments: segments,
		OrderBy:  trailingOrderBy,
		Limit:    trailingLimit,
		Offset:   trailingOffset,
	}, true
}

func normalizeSetOp(kw string) string {
	upper := strings.ToUpper(kw)
	switch upper {
	case "INTERSECT DISTINCT", "INTERSECT ALL", "INTERSECT":
		return "INTERSECT"
	case "EXCEPT DISTINCT", "EXCEPT ALL", "EXCEPT", "MINUS":
		return "EXCEPT"
	case "UNION DISTINCT", "UNION":
		return "UNION"
	case "UNION ALL":
		return "UNION ALL"
	default:
		return upper
	}
}

func extractTrailingOrderByAndLimit(sql string) (query string, orderBy string, limit int64, offset int64) {
	trimmed := strings.TrimSpace(sql)
	limit = -1
	offset = 0

	orderByIdx := sqlutil.FindTopLevelKeyword(trimmed, "ORDER BY")
	limitIdx := sqlutil.FindTopLevelKeyword(trimmed, "LIMIT")
	offsetIdx := sqlutil.FindTopLevelKeyword(trimmed, "OFFSET")

	// Determine where the main query body ends
	cutIdx := -1
	if orderByIdx != -1 {
		cutIdx = orderByIdx
	} else if limitIdx != -1 && offsetIdx != -1 {
		if limitIdx < offsetIdx {
			cutIdx = limitIdx
		} else {
			cutIdx = offsetIdx
		}
	} else if limitIdx != -1 {
		cutIdx = limitIdx
	} else if offsetIdx != -1 {
		cutIdx = offsetIdx
	}

	if cutIdx == -1 {
		query = trimmed
		return
	}

	query = strings.TrimSpace(trimmed[:cutIdx])
	trailing := trimmed[cutIdx:]

	// 1. ORDER BY
	if orderByIdx != -1 {
		orderStr := trailing[8:] // after "ORDER BY"
		nextLimit := sqlutil.FindTopLevelKeyword(orderStr, "LIMIT")
		nextOffset := sqlutil.FindTopLevelKeyword(orderStr, "OFFSET")

		endOrder := len(orderStr)
		if nextLimit != -1 && nextOffset != -1 {
			if nextLimit < nextOffset {
				endOrder = nextLimit
			} else {
				endOrder = nextOffset
			}
		} else if nextLimit != -1 {
			endOrder = nextLimit
		} else if nextOffset != -1 {
			endOrder = nextOffset
		}
		orderBy = strings.TrimSpace(orderStr[:endOrder])
	}

	// 2. LIMIT
	if limitIdx != -1 {
		relLimitIdx := sqlutil.FindTopLevelKeyword(trailing, "LIMIT")
		if relLimitIdx != -1 {
			limitStr := trailing[relLimitIdx+5:]
			tokens := sqlutil.SplitTopLevelTokens(limitStr)
			if len(tokens) > 0 {
				firstTok := tokens[0]
				// Check for MySQL "LIMIT offset, count"
				if commaIdx := strings.Index(firstTok, ","); commaIdx != -1 {
					offPart := strings.TrimSpace(firstTok[:commaIdx])
					limPart := strings.TrimSpace(firstTok[commaIdx+1:])
					if o, err := strconv.ParseInt(offPart, 10, 64); err == nil {
						offset = o
					}
					if l, err := strconv.ParseInt(limPart, 10, 64); err == nil {
						limit = l
					}
				} else {
					if l, err := strconv.ParseInt(firstTok, 10, 64); err == nil {
						limit = l
					}
					// check for "LIMIT n OFFSET m"
					if len(tokens) >= 3 && strings.EqualFold(tokens[1], "OFFSET") {
						if o, err := strconv.ParseInt(tokens[2], 10, 64); err == nil {
							offset = o
						}
					}
				}
			}
		}
	}

	// 3. OFFSET (if not already parsed)
	if offsetIdx != -1 && offset == 0 {
		relOffsetIdx := sqlutil.FindTopLevelKeyword(trailing, "OFFSET")
		if relOffsetIdx != -1 {
			offsetStr := trailing[relOffsetIdx+6:]
			tokens := sqlutil.SplitTopLevelTokens(offsetStr)
			if len(tokens) > 0 {
				if o, err := strconv.ParseInt(tokens[0], 10, 64); err == nil {
					offset = o
				}
				// check T-SQL "OFFSET o ROWS FETCH NEXT n ROWS ONLY"
				if len(tokens) >= 5 && strings.EqualFold(tokens[1], "ROWS") && strings.EqualFold(tokens[2], "FETCH") &&
					(strings.EqualFold(tokens[3], "NEXT") || strings.EqualFold(tokens[3], "FIRST")) {
					if l, err := strconv.ParseInt(tokens[4], 10, 64); err == nil {
						limit = l
					}
				}
			}
		}
	}

	return
}

func makeSetOpPlanNode(left, right *PlanNode, op string) *PlanNode {
	opCost := (left.TotalCost + right.TotalCost) * 1.05
	opRows := left.EstimatedRows + right.EstimatedRows
	if op == "INTERSECT" {
		if right.EstimatedRows < left.EstimatedRows {
			opRows = right.EstimatedRows
		} else {
			opRows = left.EstimatedRows
		}
	}
	return &PlanNode{
		Type:          NodeSetOp,
		SetOp:         op,
		StartupCost:   left.StartupCost + right.StartupCost,
		TotalCost:     opCost,
		EstimatedRows: opRows,
		Children:      []*PlanNode{left, right},
	}
}

// BuildSetOpPlan constructs a precedence-aware PlanNode tree combining subplans with SetOp, Sort, and Limit nodes.
// INTERSECT binds with higher precedence than UNION and EXCEPT according to ANSI SQL standards.
func (p *Planner) BuildSetOpPlan(setQuery *SetOpQuery, buildSubPlan func(subSQL string) (*PlanNode, error)) (*PlanNode, error) {
	if setQuery == nil || len(setQuery.Segments) < 2 {
		return nil, fmt.Errorf("set operation requires at least 2 query branches")
	}

	// Pass 1: Plan all sub-branches and reduce INTERSECT with higher precedence
	var reducedPlans []*PlanNode
	var reducedOps []string

	for i := 0; i < len(setQuery.Segments); i++ {
		seg := setQuery.Segments[i]
		plan, err := buildSubPlan(seg.Query)
		if err != nil {
			return nil, fmt.Errorf("failed to plan set query branch %d (%s): %w", i, seg.Op, err)
		}

		if seg.Op == "INTERSECT" && len(reducedPlans) > 0 {
			prevPlan := reducedPlans[len(reducedPlans)-1]
			reducedPlans[len(reducedPlans)-1] = makeSetOpPlanNode(prevPlan, plan, "INTERSECT")
		} else {
			reducedPlans = append(reducedPlans, plan)
			reducedOps = append(reducedOps, seg.Op)
		}
	}

	if len(reducedPlans) == 0 {
		return nil, fmt.Errorf("empty set operation branches")
	}

	// Pass 2: Left-associative reduction for remaining operators (UNION, UNION ALL, EXCEPT, MINUS)
	currPlan := reducedPlans[0]
	for i := 1; i < len(reducedPlans); i++ {
		currPlan = makeSetOpPlanNode(currPlan, reducedPlans[i], reducedOps[i])
	}

	// Apply trailing ORDER BY across the unified result set
	if setQuery.OrderBy != "" {
		sortKey := setQuery.OrderBy
		sortOrder := "ASC"
		orderItems := sqlutil.ParseOrderBy(setQuery.OrderBy)
		if len(orderItems) > 0 {
			sortKey = orderItems[0].Column
			if orderItems[0].Desc {
				sortOrder = "DESC"
			}
			if orderItems[0].NullsFirst != nil {
				if *orderItems[0].NullsFirst {
					sortOrder += " NULLS FIRST"
				} else {
					sortOrder += " NULLS LAST"
				}
			}
		}
		currPlan = &PlanNode{
			Type:          NodeSort,
			SortKey:       sortKey,
			SortOrder:     sortOrder,
			StartupCost:   currPlan.TotalCost + 2.0,
			TotalCost:     currPlan.TotalCost + 5.0,
			EstimatedRows: currPlan.EstimatedRows,
			Children:      []*PlanNode{currPlan},
		}
	}

	// Apply trailing LIMIT and OFFSET across the unified result set
	if setQuery.Limit >= 0 || setQuery.Offset > 0 {
		estRows := currPlan.EstimatedRows
		if setQuery.Limit >= 0 && setQuery.Limit < estRows {
			estRows = setQuery.Limit
		}
		currPlan = &PlanNode{
			Type:          NodeLimit,
			LimitCount:    setQuery.Limit,
			OffsetCount:   setQuery.Offset,
			StartupCost:   currPlan.StartupCost,
			TotalCost:     currPlan.TotalCost,
			EstimatedRows: estRows,
			Children:      []*PlanNode{currPlan},
		}
	}

	return currPlan, nil
}

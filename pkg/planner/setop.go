package planner

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// SetOpSegment represents a single query branch within a set operation chain.
type SetOpSegment struct {
	Op    string // "", "UNION", "UNION ALL", "INTERSECT", "EXCEPT"
	Query string
}

// SetOpQuery represents a parsed chain of set operations with optional trailing ORDER BY and LIMIT.
type SetOpQuery struct {
	Segments []SetOpSegment
	OrderBy  string
	Limit    int64
}

// HasTopLevelSetOp quickly checks if the query contains top-level set operators without full parsing.
func HasTopLevelSetOp(query string) bool {
	q, ok := FindTopLevelSetOps(query)
	return ok && q != nil && len(q.Segments) >= 2
}

// FindTopLevelSetOps parses top-level set operators (UNION, UNION ALL, INTERSECT, EXCEPT, MINUS)
// ignoring operators enclosed within parentheses (subqueries, CTEs) or string literals.
func FindTopLevelSetOps(query string) (*SetOpQuery, bool) {
	trimmed := strings.TrimRight(strings.TrimSpace(query), ";")
	n := len(trimmed)
	if n < 7 { // Shortest: "SELECT"
		return nil, false
	}

	type kwMatch struct {
		name       string
		normalized string
		length     int
	}

	// Keywords in descending order of length to prefer multi-word variants ("UNION ALL" over "UNION")
	keywords := []kwMatch{
		{"INTERSECT DISTINCT", "INTERSECT", 18},
		{"INTERSECT ALL", "INTERSECT", 13},
		{"EXCEPT DISTINCT", "EXCEPT", 15},
		{"EXCEPT ALL", "EXCEPT", 10},
		{"UNION DISTINCT", "UNION", 14},
		{"UNION ALL", "UNION ALL", 9},
		{"INTERSECT", "INTERSECT", 9},
		{"EXCEPT", "EXCEPT", 6},
		{"MINUS", "EXCEPT", 5},
		{"UNION", "UNION", 5},
	}

	var segments []SetOpSegment
	segStart := 0
	lastOp := ""

	parenDepth := 0
	bracketDepth := 0
	inSingleQuote := false
	inDoubleQuote := false

	isBoundary := func(b byte) bool {
		return unicode.IsSpace(rune(b)) || b == ')' || b == '(' || b == ';' || b == ','
	}

	for i := 0; i < n; i++ {
		ch := trimmed[i]

		// Handle string literals
		if ch == '\'' && !inDoubleQuote {
			if inSingleQuote && i+1 < n && trimmed[i+1] == '\'' {
				i++ // Skip escaped single quote ''
				continue
			}
			inSingleQuote = !inSingleQuote
			continue
		}
		if ch == '"' && !inSingleQuote {
			if inDoubleQuote && i+1 < n && trimmed[i+1] == '"' {
				i++ // Skip escaped double quote ""
				continue
			}
			inDoubleQuote = !inDoubleQuote
			continue
		}
		if inSingleQuote || inDoubleQuote {
			continue
		}

		// Handle brackets and parentheses
		if ch == '[' {
			bracketDepth++
			continue
		}
		if ch == ']' && bracketDepth > 0 {
			bracketDepth--
			continue
		}
		if bracketDepth > 0 {
			continue
		}

		if ch == '(' {
			parenDepth++
			continue
		}
		if ch == ')' {
			if parenDepth > 0 {
				parenDepth--
			}
			continue
		}

		// Only inspect top-level tokens outside of parentheses
		if parenDepth == 0 {
			// Check preceding character boundary
			prevOk := (i == 0) || isBoundary(trimmed[i-1])
			if !prevOk {
				continue
			}

			// Test keywords
			for _, kw := range keywords {
				if i+kw.length <= n {
					sub := trimmed[i : i+kw.length]
					if strings.EqualFold(sub, kw.name) {
						// Check trailing character boundary
						nextOk := (i+kw.length == n) || isBoundary(trimmed[i+kw.length])
						if nextOk {
							chunk := strings.TrimSpace(trimmed[segStart:i])
							segments = append(segments, SetOpSegment{
								Op:    lastOp,
								Query: cleanSubqueryParens(chunk),
							})
							lastOp = kw.normalized
							i += kw.length
							segStart = i
							break
						}
					}
				}
			}
		}
	}

	if len(segments) == 0 {
		return nil, false
	}

	// Last segment contains remaining SQL, plus potential trailing ORDER BY and LIMIT
	lastChunk := strings.TrimSpace(trimmed[segStart:])
	lastQuery, orderBy, limit := extractTrailingOrderByAndLimit(lastChunk)

	segments = append(segments, SetOpSegment{
		Op:    lastOp,
		Query: cleanSubqueryParens(lastQuery),
	})

	return &SetOpQuery{
		Segments: segments,
		OrderBy:  orderBy,
		Limit:    limit,
	}, true
}

func cleanSubqueryParens(sql string) string {
	trimmed := strings.TrimSpace(sql)
	if strings.HasPrefix(trimmed, "(") && strings.HasSuffix(trimmed, ")") {
		// Verify if the outer parens span the entire string
		depth := 0
		allWrapped := true
		for i := 0; i < len(trimmed)-1; i++ {
			if trimmed[i] == '(' {
				depth++
			} else if trimmed[i] == ')' {
				depth--
				if depth == 0 {
					allWrapped = false
					break
				}
			}
		}
		if allWrapped && depth == 1 {
			return strings.TrimSpace(trimmed[1 : len(trimmed)-1])
		}
	}
	return trimmed
}

func extractTrailingOrderByAndLimit(sql string) (query string, orderBy string, limit int64) {
	trimmed := strings.TrimSpace(sql)
	n := len(trimmed)
	limit = -1

	// If enclosed entirely in parentheses, don't peel inner ORDER BY / LIMIT
	if strings.HasPrefix(trimmed, "(") && strings.HasSuffix(trimmed, ")") {
		depth := 0
		allWrapped := true
		for i := 0; i < n-1; i++ {
			if trimmed[i] == '(' {
				depth++
			} else if trimmed[i] == ')' {
				depth--
				if depth == 0 {
					allWrapped = false
					break
				}
			}
		}
		if allWrapped && depth == 1 {
			return trimmed, "", -1
		}
	}

	// Find top-level ORDER BY and LIMIT from the right
	parenDepth := 0
	inQuote := false
	orderByIdx := -1
	limitIdx := -1

	for i := 0; i < n; i++ {
		ch := trimmed[i]
		if ch == '\'' {
			inQuote = !inQuote
			continue
		}
		if inQuote {
			continue
		}
		if ch == '(' {
			parenDepth++
			continue
		}
		if ch == ')' {
			if parenDepth > 0 {
				parenDepth--
			}
			continue
		}

		if parenDepth == 0 {
			prevOk := (i == 0) || unicode.IsSpace(rune(trimmed[i-1]))
			if prevOk {
				if i+8 <= n && strings.EqualFold(trimmed[i:i+8], "ORDER BY") && (i+8 == n || unicode.IsSpace(rune(trimmed[i+8]))) {
					orderByIdx = i
				} else if i+5 <= n && strings.EqualFold(trimmed[i:i+5], "LIMIT") && (i+5 == n || unicode.IsSpace(rune(trimmed[i+5]))) {
					limitIdx = i
				}
			}
		}
	}

	if orderByIdx != -1 && limitIdx != -1 && limitIdx > orderByIdx {
		query = strings.TrimSpace(trimmed[:orderByIdx])
		orderBy = strings.TrimSpace(trimmed[orderByIdx+8 : limitIdx])
		limitStr := strings.TrimSpace(trimmed[limitIdx+5:])
		if lim, err := strconv.ParseInt(limitStr, 10, 64); err == nil {
			limit = lim
		}
		return
	}

	if orderByIdx != -1 && limitIdx == -1 {
		query = strings.TrimSpace(trimmed[:orderByIdx])
		orderBy = strings.TrimSpace(trimmed[orderByIdx+8:])
		return
	}

	if orderByIdx == -1 && limitIdx != -1 {
		query = strings.TrimSpace(trimmed[:limitIdx])
		limitStr := strings.TrimSpace(trimmed[limitIdx+5:])
		if lim, err := strconv.ParseInt(limitStr, 10, 64); err == nil {
			limit = lim
		}
		return
	}

	return trimmed, "", -1
}

// BuildSetOpPlan constructs a left-deep PlanNode tree combining subplans with SetOp, Sort, and Limit nodes.
func (p *Planner) BuildSetOpPlan(setQuery *SetOpQuery, buildSubPlan func(subSQL string) (*PlanNode, error)) (*PlanNode, error) {
	if setQuery == nil || len(setQuery.Segments) < 2 {
		return nil, fmt.Errorf("set operation requires at least 2 query branches")
	}

	currPlan, err := buildSubPlan(setQuery.Segments[0].Query)
	if err != nil {
		return nil, fmt.Errorf("failed to plan initial set query branch: %w", err)
	}

	for i := 1; i < len(setQuery.Segments); i++ {
		seg := setQuery.Segments[i]
		rightPlan, err := buildSubPlan(seg.Query)
		if err != nil {
			return nil, fmt.Errorf("failed to plan set query branch %d (%s): %w", i, seg.Op, err)
		}

		opCost := (currPlan.TotalCost + rightPlan.TotalCost) * 1.05
		opRows := currPlan.EstimatedRows + rightPlan.EstimatedRows
		if seg.Op == "INTERSECT" {
			if rightPlan.EstimatedRows < currPlan.EstimatedRows {
				opRows = rightPlan.EstimatedRows
			} else {
				opRows = currPlan.EstimatedRows
			}
		}

		currPlan = &PlanNode{
			Type:          NodeSetOp,
			SetOp:         seg.Op,
			StartupCost:   currPlan.StartupCost + rightPlan.StartupCost,
			TotalCost:     opCost,
			EstimatedRows: opRows,
			Children:      []*PlanNode{currPlan, rightPlan},
		}
	}

	// Apply trailing ORDER BY across the unified result set
	if setQuery.OrderBy != "" {
		parts := strings.Fields(setQuery.OrderBy)
		sortKey := parts[0]
		sortOrder := "ASC"
		if len(parts) > 1 && strings.EqualFold(parts[1], "DESC") {
			sortOrder = "DESC"
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

	// Apply trailing LIMIT across the unified result set
	if setQuery.Limit >= 0 {
		currPlan = &PlanNode{
			Type:          NodeLimit,
			LimitCount:    setQuery.Limit,
			StartupCost:   currPlan.StartupCost,
			TotalCost:     currPlan.TotalCost,
			EstimatedRows: setQuery.Limit,
			Children:      []*PlanNode{currPlan},
		}
	}

	return currPlan, nil
}

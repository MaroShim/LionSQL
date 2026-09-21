package planner

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"sqlion/pkg/storage"
)

// NodeType represents the operational type of an execution plan node.
type NodeType string

const (
	NodeTableScan  NodeType = "Seq Scan"
	NodeIndexScan  NodeType = "Index Scan"
	NodeFilter     NodeType = "Filter"
	NodeSort       NodeType = "Sort"
	NodeProjection NodeType = "Projection"
	NodeLimit      NodeType = "Limit"
)

// PlanNode represents a single step in a query execution plan tree.
type PlanNode struct {
	Type             NodeType    `json:"type"`
	Relation         string      `json:"relation,omitempty"`
	IndexName        string      `json:"index_name,omitempty"`
	IndexCond        string      `json:"index_cond,omitempty"`
	FilterCond       string      `json:"filter_cond,omitempty"`
	SortKey          string      `json:"sort_key,omitempty"`
	SortOrder        string      `json:"sort_order,omitempty"`
	LimitCount       int64       `json:"limit_count,omitempty"`
	Projections      []string    `json:"projections,omitempty"`
	StartupCost      float64     `json:"startup_cost"`
	TotalCost        float64     `json:"total_cost"`
	EstimatedRows    int64       `json:"estimated_rows"`
	ActualRows       int64       `json:"actual_rows,omitempty"`
	ActualDurationUS int64       `json:"actual_duration_us,omitempty"`
	Children         []*PlanNode `json:"children,omitempty"`
}

// QueryInfo represents the structured parts of a parsed SELECT query.
type QueryInfo struct {
	RawQuery      string
	TableName     string
	Projections   []string
	WhereClause   string
	OrderByClause string
	LimitCount    int64
	IsAnalyze     bool
}

// Planner constructs and optimizes execution plans for SQL queries.
type Planner struct{}

// NewPlanner creates a new query planner instance.
func NewPlanner() *Planner {
	return &Planner{}
}

// ParseSelect parses a SELECT statement into QueryInfo.
func (p *Planner) ParseSelect(query string) (*QueryInfo, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	isAnalyze := false
	upper := strings.ToUpper(cleaned)
	if strings.HasPrefix(upper, "EXPLAIN ANALYZE ") {
		isAnalyze = true
		cleaned = strings.TrimSpace(cleaned[16:])
	} else if strings.HasPrefix(upper, "EXPLAIN ") {
		cleaned = strings.TrimSpace(cleaned[8:])
	}

	selectRe := regexp.MustCompile(`(?is)^SELECT\s+(.*?)\s+FROM\s+([^\s,;]+)(?:\s+WHERE\s+(.*?))?(?:\s+ORDER\s+BY\s+(.*?))?(?:\s+LIMIT\s+(\d+))?$`)
	matches := selectRe.FindStringSubmatch(cleaned)
	if len(matches) < 3 {
		return nil, fmt.Errorf("invalid SELECT statement for planning: %q", cleaned)
	}

	projStr := strings.TrimSpace(matches[1])
	rawTable := strings.TrimSpace(matches[2])
	whereClause := ""
	if len(matches) > 3 {
		whereClause = strings.TrimSpace(matches[3])
	}
	orderByClause := ""
	if len(matches) > 4 {
		orderByClause = strings.TrimSpace(matches[4])
	}
	limitCount := int64(-1)
	if len(matches) > 5 && matches[5] != "" {
		if n, err := strconv.ParseInt(matches[5], 10, 64); err == nil {
			limitCount = n
		}
	}

	var projections []string
	if projStr == "*" || projStr == "" {
		projections = []string{"*"}
	} else {
		for _, col := range strings.Split(projStr, ",") {
			c := strings.TrimSpace(col)
			if c != "" {
				projections = append(projections, c)
			}
		}
	}

	return &QueryInfo{
		RawQuery:      query,
		TableName:     rawTable,
		Projections:   projections,
		WhereClause:   whereClause,
		OrderByClause: orderByClause,
		LimitCount:    limitCount,
		IsAnalyze:     isAnalyze,
	}, nil
}

// BuildPlan produces an optimized PlanNode tree for the given query and schema.
func (p *Planner) BuildPlan(q *QueryInfo, schema *storage.TableSchema, totalRows int64) *PlanNode {
	if totalRows < 0 {
		totalRows = 0
	}
	estimatedTotal := totalRows
	if estimatedTotal == 0 {
		estimatedTotal = 1 // baseline default for cost estimation
	}

	var scanNode *PlanNode
	usedIndex := false

	// Check if WHERE condition matches an existing index
	if q.WhereClause != "" && schema != nil {
		eqRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_]+)\s*=\s*(.+)$`)
		if m := eqRe.FindStringSubmatch(q.WhereClause); len(m) == 3 {
			colName := m[1]
			if idx, found := schema.FindIndexByColumn(colName); found {
				usedIndex = true
				estRows := int64(1)
				if !idx.Unique {
					estRows = int64(math.Ceil(float64(estimatedTotal) * 0.1))
					if estRows < 1 {
						estRows = 1
					}
				}

				startupCost := 0.25
				totalCost := startupCost + float64(estRows)*0.05 + 0.1

				scanNode = &PlanNode{
					Type:          NodeIndexScan,
					Relation:      q.TableName,
					IndexName:     idx.Name,
					IndexCond:     fmt.Sprintf("(%s = %s)", colName, strings.TrimSpace(m[2])),
					StartupCost:   startupCost,
					TotalCost:     totalCost,
					EstimatedRows: estRows,
				}
			}
		}
	}

	if !usedIndex {
		// Table Scan (Sequential Scan)
		startupCost := 0.0
		totalCost := float64(estimatedTotal)*0.01 + 1.0

		scanNode = &PlanNode{
			Type:          NodeTableScan,
			Relation:      q.TableName,
			StartupCost:   startupCost,
			TotalCost:     totalCost,
			EstimatedRows: estimatedTotal,
		}
	}

	currentNode := scanNode

	// If table scan with where clause (or index scan with additional filter), wrap in Filter
	if q.WhereClause != "" && !usedIndex {
		filterEstRows := int64(math.Ceil(float64(currentNode.EstimatedRows) * 0.33))
		if filterEstRows < 1 {
			filterEstRows = 1
		}
		filterCost := currentNode.TotalCost + float64(currentNode.EstimatedRows)*0.0025

		filterNode := &PlanNode{
			Type:          NodeFilter,
			FilterCond:    q.WhereClause,
			StartupCost:   currentNode.StartupCost,
			TotalCost:     filterCost,
			EstimatedRows: filterEstRows,
			Children:      []*PlanNode{currentNode},
		}
		currentNode = filterNode
	}

	// Order By -> Sort Node
	if q.OrderByClause != "" {
		sortParts := strings.Fields(q.OrderByClause)
		sortKey := sortParts[0]
		sortOrder := "ASC"
		if len(sortParts) > 1 && strings.EqualFold(sortParts[1], "DESC") {
			sortOrder = "DESC"
		}

		n := float64(currentNode.EstimatedRows)
		sortStartup := currentNode.TotalCost + (n * math.Log2(math.Max(2, n)) * 0.01)
		sortTotal := sortStartup + n*0.01

		sortNode := &PlanNode{
			Type:          NodeSort,
			SortKey:       sortKey,
			SortOrder:     sortOrder,
			StartupCost:   sortStartup,
			TotalCost:     sortTotal,
			EstimatedRows: currentNode.EstimatedRows,
			Children:      []*PlanNode{currentNode},
		}
		currentNode = sortNode
	}

	// Projection Node
	projNode := &PlanNode{
		Type:          NodeProjection,
		Projections:   q.Projections,
		StartupCost:   currentNode.StartupCost,
		TotalCost:     currentNode.TotalCost + float64(currentNode.EstimatedRows)*0.001,
		EstimatedRows: currentNode.EstimatedRows,
		Children:      []*PlanNode{currentNode},
	}
	currentNode = projNode

	// Limit Node
	if q.LimitCount >= 0 {
		limitEstRows := currentNode.EstimatedRows
		if q.LimitCount < limitEstRows {
			limitEstRows = q.LimitCount
		}

		ratio := 1.0
		if currentNode.EstimatedRows > 0 {
			ratio = float64(limitEstRows) / float64(currentNode.EstimatedRows)
		}
		limitCost := currentNode.StartupCost + (currentNode.TotalCost-currentNode.StartupCost)*ratio

		limitNode := &PlanNode{
			Type:          NodeLimit,
			LimitCount:    q.LimitCount,
			StartupCost:   currentNode.StartupCost,
			TotalCost:     limitCost,
			EstimatedRows: limitEstRows,
			Children:      []*PlanNode{currentNode},
		}
		currentNode = limitNode
	}

	return currentNode
}

// FormatPlanText produces standard indented EXPLAIN execution tree text.
func FormatPlanText(node *PlanNode, analyze bool) []string {
	var lines []string
	formatNodeText(node, "", true, analyze, &lines)
	return lines
}

func formatNodeText(node *PlanNode, prefix string, isRoot bool, analyze bool, lines *[]string) {
	if node == nil {
		return
	}

	var sb strings.Builder
	if !isRoot {
		sb.WriteString(prefix)
		sb.WriteString("-> ")
	}

	sb.WriteString(string(node.Type))

	switch node.Type {
	case NodeTableScan:
		sb.WriteString(fmt.Sprintf(" on %s", node.Relation))
	case NodeIndexScan:
		sb.WriteString(fmt.Sprintf(" using %s on %s", node.IndexName, node.Relation))
	case NodeSort:
		sb.WriteString(fmt.Sprintf(" (sort_key=%s %s)", node.SortKey, node.SortOrder))
	case NodeLimit:
		sb.WriteString(fmt.Sprintf(" (count=%d)", node.LimitCount))
	case NodeProjection:
		sb.WriteString(fmt.Sprintf(" (columns=[%s])", strings.Join(node.Projections, ", ")))
	case NodeFilter:
		sb.WriteString(fmt.Sprintf(" (%s)", node.FilterCond))
	}

	sb.WriteString(fmt.Sprintf(" (cost=%.2f..%.2f rows=%d)", node.StartupCost, node.TotalCost, node.EstimatedRows))

	if analyze {
		durationMs := float64(node.ActualDurationUS) / 1000.0
		sb.WriteString(fmt.Sprintf(" (actual time=%.3fms rows=%d)", durationMs, node.ActualRows))
	}

	if node.IndexCond != "" {
		sb.WriteString(fmt.Sprintf(" Index Cond: %s", node.IndexCond))
	}

	*lines = append(*lines, sb.String())

	childPrefix := prefix
	if !isRoot {
		childPrefix += "   "
	} else {
		childPrefix = "  "
	}

	for _, child := range node.Children {
		formatNodeText(child, childPrefix, false, analyze, lines)
	}
}

// FormatPlanJSON serializes the plan tree to JSON string.
func FormatPlanJSON(node *PlanNode) (string, error) {
	bytes, err := json.MarshalIndent(node, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

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
	NodeTableScan   NodeType = "Seq Scan"
	NodeIndexScan   NodeType = "Index Scan"
	NodeFilter      NodeType = "Filter"
	NodeSort        NodeType = "Sort"
	NodeProjection  NodeType = "Projection"
	NodeLimit       NodeType = "Limit"
	NodeHashJoin    NodeType = "Hash Join"
	NodeHash        NodeType = "Hash"
	NodeNestedLoop  NodeType = "Nested Loop"
)

// JoinInfo contains metadata for a multi-table JOIN operation.
type JoinInfo struct {
	Type     string // "INNER" or "LEFT"
	Table    string
	Alias    string
	LeftKey  string
	RightKey string
}

// PlanNode represents a single step in a query execution plan tree.
type PlanNode struct {
	Type             NodeType    `json:"type"`
	Relation         string      `json:"relation,omitempty"`
	Alias            string      `json:"alias,omitempty"`
	IndexName        string      `json:"index_name,omitempty"`
	IndexCond        string      `json:"index_cond,omitempty"`
	FilterCond       string      `json:"filter_cond,omitempty"`
	SortKey          string      `json:"sort_key,omitempty"`
	SortOrder        string      `json:"sort_order,omitempty"`
	LimitCount       int64       `json:"limit_count,omitempty"`
	Projections      []string    `json:"projections,omitempty"`
	JoinType         string      `json:"join_type,omitempty"`
	HashCond         string      `json:"hash_cond,omitempty"`
	JoinCond         string      `json:"join_cond,omitempty"`
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
	TableAlias    string
	Join          *JoinInfo
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

	selectRe := regexp.MustCompile(`(?is)^SELECT\s+(.*?)\s+FROM\s+(.*?)(?:\s+WHERE\s+(.*?))?(?:\s+ORDER\s+BY\s+(.*?))?(?:\s+LIMIT\s+(\d+))?$`)
	matches := selectRe.FindStringSubmatch(cleaned)
	if len(matches) < 3 {
		return nil, fmt.Errorf("invalid SELECT statement for planning: %q", cleaned)
	}

	projStr := strings.TrimSpace(matches[1])
	fromClause := strings.TrimSpace(matches[2])
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

	baseTable, baseAlias, joinInfo, err := parseFromAndJoin(fromClause)
	if err != nil {
		return nil, err
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
		TableName:     baseTable,
		TableAlias:    baseAlias,
		Join:          joinInfo,
		Projections:   projections,
		WhereClause:   whereClause,
		OrderByClause: orderByClause,
		LimitCount:    limitCount,
		IsAnalyze:     isAnalyze,
	}, nil
}

func parseFromAndJoin(fromClause string) (string, string, *JoinInfo, error) {
	// Check for JOIN: [INNER | LEFT [OUTER]] JOIN
	joinRe := regexp.MustCompile(`(?is)^(.*?)\s+(?:(INNER|LEFT(?:\s+OUTER)?)\s+)?JOIN\s+([^\s]+)(?:\s+([a-zA-Z0-9_]+))?\s+ON\s+([^\s=]+)\s*=\s*([^\s=;]+)$`)
	if m := joinRe.FindStringSubmatch(fromClause); len(m) == 7 {
		leftPart := strings.TrimSpace(m[1])
		joinTypeUpper := strings.ToUpper(strings.TrimSpace(m[2]))
		rightTable := strings.TrimSpace(m[3])
		rightAlias := strings.TrimSpace(m[4])
		leftKey := strings.TrimSpace(m[5])
		rightKey := strings.TrimSpace(m[6])

		jType := "INNER"
		if strings.HasPrefix(joinTypeUpper, "LEFT") {
			jType = "LEFT"
		}

		baseTable, baseAlias := parseTableAndAlias(leftPart)
		if rightAlias == "" {
			rightAlias = rightTable
		}

		return baseTable, baseAlias, &JoinInfo{
			Type:     jType,
			Table:    rightTable,
			Alias:    rightAlias,
			LeftKey:  leftKey,
			RightKey: rightKey,
		}, nil
	}

	baseTable, baseAlias := parseTableAndAlias(fromClause)
	return baseTable, baseAlias, nil, nil
}

func parseTableAndAlias(str string) (string, string) {
	tokens := strings.Fields(strings.TrimSpace(str))
	if len(tokens) == 0 {
		return "", ""
	}
	if len(tokens) == 1 {
		return tokens[0], tokens[0]
	}
	if len(tokens) == 2 {
		return tokens[0], tokens[1]
	}
	if len(tokens) == 3 && strings.EqualFold(tokens[1], "AS") {
		return tokens[0], tokens[2]
	}
	return tokens[0], tokens[1]
}

// BuildPlan produces an optimized PlanNode tree for the given query and schema.
func (p *Planner) BuildPlan(q *QueryInfo, schema *storage.TableSchema, totalRows int64) *PlanNode {
	return p.BuildJoinPlan(q, schema, totalRows, nil, 0)
}

// BuildJoinPlan produces an optimized PlanNode tree considering joins and multiple tables.
func (p *Planner) BuildJoinPlan(q *QueryInfo, schema *storage.TableSchema, totalRows int64, rightSchema *storage.TableSchema, rightRows int64) *PlanNode {
	if totalRows < 0 {
		totalRows = 0
	}
	estimatedTotal := totalRows
	if estimatedTotal == 0 {
		estimatedTotal = 1 // baseline default for cost estimation
	}

	var scanNode *PlanNode
	usedIndex := false

	// Check if WHERE condition matches an existing index on primary table
	if q.WhereClause != "" && schema != nil && q.Join == nil {
		eqRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_\.]+)\s*=\s*(.+)$`)
		if m := eqRe.FindStringSubmatch(q.WhereClause); len(m) == 3 {
			rawCol := m[1]
			if idxDot := strings.Index(rawCol, "."); idxDot != -1 {
				rawCol = rawCol[idxDot+1:]
			}
			colName := rawCol
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
					Alias:         q.TableAlias,
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
			Alias:         q.TableAlias,
			StartupCost:   startupCost,
			TotalCost:     totalCost,
			EstimatedRows: estimatedTotal,
		}
	}

	var currentNode *PlanNode = scanNode

	// If JOIN is present, choose between Index Nested Loop Join and Hash Join
	if q.Join != nil {
		rRows := rightRows
		if rRows <= 0 {
			rRows = 1
		}

		rightCol := q.Join.RightKey
		if idx := strings.Index(rightCol, "."); idx != -1 {
			rightCol = rightCol[idx+1:]
		}

		// Check if inner table has an index on the join key
		var innerIndex *storage.IndexSchema
		if rightSchema != nil {
			if idx, found := rightSchema.FindIndexByColumn(rightCol); found {
				innerIndex = idx
			}
		}

		joinEstRows := estimatedTotal
		if q.Join.Type == "INNER" {
			joinEstRows = int64(math.Min(float64(estimatedTotal), float64(rRows)))
			if joinEstRows < 1 {
				joinEstRows = 1
			}
		}

		if innerIndex != nil {
			// Strategy: Index Nested Loop Join
			innerScanNode := &PlanNode{
				Type:          NodeIndexScan,
				Relation:      q.Join.Table,
				Alias:         q.Join.Alias,
				IndexName:     innerIndex.Name,
				IndexCond:     fmt.Sprintf("(%s = %s)", innerIndex.Columns[0], q.Join.LeftKey),
				StartupCost:   0.25,
				TotalCost:     0.35,
				EstimatedRows: 1,
			}

			nlStartupCost := scanNode.StartupCost + 0.25
			nlTotalCost := scanNode.TotalCost + float64(estimatedTotal)*0.06

			nlNode := &PlanNode{
				Type:          NodeNestedLoop,
				JoinType:      q.Join.Type,
				JoinCond:      fmt.Sprintf("(%s = %s)", q.Join.LeftKey, q.Join.RightKey),
				StartupCost:   nlStartupCost,
				TotalCost:     nlTotalCost,
				EstimatedRows: joinEstRows,
				Children:      []*PlanNode{scanNode, innerScanNode},
			}
			currentNode = nlNode
		} else {
			// Strategy: Hash Join
			rScanCost := float64(rRows)*0.01 + 1.0
			rScanNode := &PlanNode{
				Type:          NodeTableScan,
				Relation:      q.Join.Table,
				Alias:         q.Join.Alias,
				StartupCost:   0.0,
				TotalCost:     rScanCost,
				EstimatedRows: rRows,
			}

			hashStartupCost := rScanCost
			hashTotalCost := rScanCost + float64(rRows)*0.01
			hashNode := &PlanNode{
				Type:          NodeHash,
				StartupCost:   hashStartupCost,
				TotalCost:     hashTotalCost,
				EstimatedRows: rRows,
				Children:      []*PlanNode{rScanNode},
			}

			joinStartupCost := hashTotalCost
			joinTotalCost := joinStartupCost + scanNode.TotalCost + float64(estimatedTotal)*0.005

			hashJoinNode := &PlanNode{
				Type:          NodeHashJoin,
				JoinType:      q.Join.Type,
				HashCond:      fmt.Sprintf("(%s = %s)", q.Join.LeftKey, q.Join.RightKey),
				StartupCost:   joinStartupCost,
				TotalCost:     joinTotalCost,
				EstimatedRows: joinEstRows,
				Children:      []*PlanNode{scanNode, hashNode},
			}
			currentNode = hashJoinNode
		}
	}

	// Filter Node
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
	case NodeNestedLoop:
		if node.JoinType == "LEFT" {
			sb.WriteString(" (Left)")
		}
		if node.JoinCond != "" {
			sb.WriteString(fmt.Sprintf(" (join_cond=%s)", node.JoinCond))
		}
	case NodeHashJoin:
		if node.JoinType == "LEFT" {
			sb.WriteString(" (Left)")
		}
		if node.HashCond != "" {
			sb.WriteString(fmt.Sprintf(" (hash_cond=%s)", node.HashCond))
		}
	case NodeHash:
		// Hash intermediate node
	case NodeTableScan:
		sb.WriteString(fmt.Sprintf(" on %s", node.Relation))
		if node.Alias != "" && node.Alias != node.Relation {
			sb.WriteString(fmt.Sprintf(" %s", node.Alias))
		}
	case NodeIndexScan:
		sb.WriteString(fmt.Sprintf(" using %s on %s", node.IndexName, node.Relation))
		if node.Alias != "" && node.Alias != node.Relation {
			sb.WriteString(fmt.Sprintf(" %s", node.Alias))
		}
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

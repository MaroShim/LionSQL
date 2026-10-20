package planner

import (
	"encoding/json"
	"fmt"
	"math"
	"math/bits"
	"regexp"
	"strconv"
	"strings"

	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

// NodeType represents the operational type of an execution plan node.
type NodeType string

const (
	NodeTableScan     NodeType = "Seq Scan"
	NodeIndexScan     NodeType = "Index Scan"
	NodeIndexOnlyScan NodeType = "Index Only Scan"
	NodeFilter        NodeType = "Filter"
	NodeSort        NodeType = "Sort"
	NodeProjection  NodeType = "Projection"
	NodeLimit       NodeType = "Limit"
	NodeHashJoin    NodeType = "Hash Join"
	NodeHash        NodeType = "Hash"
	NodeNestedLoop  NodeType = "Nested Loop"
	NodeMergeJoin   NodeType = "Merge Join"
	NodeAggregate   NodeType = "Aggregate"
	NodeCTE         NodeType = "CTE Scan"
	NodeMaterialize NodeType = "Materialize"
	NodeWindow      NodeType = "Window"
	NodeSetOp       NodeType = "SetOp"
	NodeModifyTable NodeType = "ModifyTable"
)

// SortKeyDef defines a column and sort direction for ordering.
type SortKeyDef struct {
	Column    string `json:"column"`
	Direction string `json:"direction"` // "ASC" or "DESC"
}

// FrameBoundType defines the boundary of a window frame.
type FrameBoundType int

const (
	FrameUnboundedPreceding FrameBoundType = iota
	FramePreceding
	FrameCurrentRow
	FrameFollowing
	FrameUnboundedFollowing
)

// FrameBound defines a single frame boundary.
type FrameBound struct {
	Type   FrameBoundType `json:"type"`
	Offset int            `json:"offset,omitempty"`
}

// WindowFrame defines a window frame specification (e.g. ROWS BETWEEN 1 PRECEDING AND 1 FOLLOWING).
type WindowFrame struct {
	Start FrameBound `json:"start"`
	End   FrameBound `json:"end"`
}

// WindowDef defines a window function specification (function, args, partition by, order by, alias, frame).
type WindowDef struct {
	Function      string       `json:"function"`
	Args          []string     `json:"args,omitempty"`
	PartitionKeys []string     `json:"partition_keys,omitempty"`
	OrderByKeys   []SortKeyDef `json:"order_by_keys,omitempty"`
	Frame         *WindowFrame `json:"frame,omitempty"`
	Alias         string       `json:"alias,omitempty"`
	RawExpr       string       `json:"raw_expr,omitempty"`
}

// CTEInfo contains metadata for a Common Table Expression.
type CTEInfo struct {
	Name     string
	InnerSQL string
}

// JoinInfo contains metadata for a multi-table JOIN operation.
type JoinInfo struct {
	Type     string // "INNER", "LEFT", "SEMI", "ANTI", etc.
	Table    string
	Alias    string
	LeftKey  string
	RightKey string
	Filter   string // Optional filter condition on the joined table
	Hints    []string
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
	OffsetCount      int64       `json:"offset_count,omitempty"`
	Projections      []string    `json:"projections,omitempty"`
	GroupKeys        []string             `json:"group_keys,omitempty"`
	GroupByModifier  proc.GroupByModifier `json:"group_by_modifier,omitempty"`
	HavingCond       string               `json:"having_cond,omitempty"`
	WindowDefs       []WindowDef          `json:"window_defs,omitempty"`
	JoinType         string               `json:"join_type,omitempty"`
	HashCond         string               `json:"hash_cond,omitempty"`
	JoinCond         string               `json:"join_cond,omitempty"`
	MergeCond        string               `json:"merge_cond,omitempty"`
	CTEName          string               `json:"cte_name,omitempty"`
	SetOp            string               `json:"set_op,omitempty"`
	StartupCost      float64              `json:"startup_cost"`
	TotalCost        float64              `json:"total_cost"`
	EstimatedRows    int64                `json:"estimated_rows"`
	ActualRows       int64                `json:"actual_rows,omitempty"`
	ActualStartupUS  int64                `json:"actual_startup_us,omitempty"`
	ActualDurationUS int64                `json:"actual_duration_us,omitempty"`
	ActualLoops      int64                `json:"actual_loops,omitempty"`
	Partitions       []string             `json:"partitions,omitempty"`
	Children         []*PlanNode          `json:"children,omitempty"`
}

// Clone creates a deep copy of the PlanNode execution plan tree.
func (n *PlanNode) Clone() *PlanNode {
	if n == nil {
		return nil
	}
	cp := *n
	if len(n.Projections) > 0 {
		cp.Projections = make([]string, len(n.Projections))
		copy(cp.Projections, n.Projections)
	}
	if len(n.GroupKeys) > 0 {
		cp.GroupKeys = make([]string, len(n.GroupKeys))
		copy(cp.GroupKeys, n.GroupKeys)
	}
	if len(n.WindowDefs) > 0 {
		cp.WindowDefs = make([]WindowDef, len(n.WindowDefs))
		copy(cp.WindowDefs, n.WindowDefs)
	}
	if len(n.Partitions) > 0 {
		cp.Partitions = make([]string, len(n.Partitions))
		copy(cp.Partitions, n.Partitions)
	}
	if len(n.Children) > 0 {
		cp.Children = make([]*PlanNode, len(n.Children))
		for i, child := range n.Children {
			cp.Children[i] = child.Clone()
		}
	}
	return &cp
}

// QueryInfo represents the structured parts of a parsed SELECT query.
type QueryInfo struct {
	RawQuery      string
	CTEs          []CTEInfo
	TableName     string
	TableAlias    string
	TableHints    []string
	Join          *JoinInfo
	Joins         []*JoinInfo
	Projections   []string
	WindowDefs    []WindowDef
	WhereClause   string
	GroupByCols   []string
	GroupByMod    proc.GroupByModifier
	HavingClause  string
	OrderByClause string
	LimitCount    int64
	OffsetCount   int64
	HasAggregates bool
	Distinct      bool
	IsAnalyze     bool
	QueryHints    []string
	AST           *proc.GeneralSelectStmt
}

// Clone creates a shallow copy of QueryInfo so variable substitution can safely modify WhereClause.
func (q *QueryInfo) Clone() *QueryInfo {
	if q == nil {
		return nil
	}
	cp := *q
	return &cp
}

// Planner constructs and optimizes execution plans for SQL queries.
type Planner struct {
	PartitionGetter func(table string) []string
}

// NewPlanner creates a new query planner instance.
func NewPlanner() *Planner {
	return &Planner{}
}

func cleanIdent(s string) string {
	return proc.CleanSchemaPrefix(s)
}

func splitASTAndExpr(expr proc.Expr) []proc.Expr {
	if bin, ok := expr.(*proc.BinaryExpr); ok && bin.Op == proc.TokenAnd {
		return append(splitASTAndExpr(bin.Left), splitASTAndExpr(bin.Right)...)
	}
	return []proc.Expr{expr}
}

// FromAST converts a GeneralSelectStmt AST into a QueryInfo for cost-based planning.
func FromAST(stmt *proc.GeneralSelectStmt) *QueryInfo {
	if stmt == nil {
		return nil
	}

	var ctes []CTEInfo
	for _, c := range stmt.CTEs {
		ctes = append(ctes, CTEInfo{
			Name:     c.Name,
			InnerSQL: c.InnerSQL,
		})
	}

	var projections []string
	hasAgg := false
	for _, p := range stmt.Projections {
		pStr := p.String()
		projections = append(projections, pStr)
		pUpper := strings.ToUpper(pStr)
		if strings.Contains(pUpper, "OVER") {
			continue
		}
		if strings.Contains(pUpper, "COUNT(") || strings.Contains(pUpper, "SUM(") ||
			strings.Contains(pUpper, "AVG(") || strings.Contains(pUpper, "MIN(") ||
			strings.Contains(pUpper, "MAX(") || strings.Contains(pUpper, "GROUPING(") ||
			strings.Contains(pUpper, "PERCENTILE_CONT(") || strings.Contains(pUpper, "PERCENTILE_DISC(") ||
			strings.Contains(pUpper, "APPROX_PERCENTILE(") || strings.Contains(pUpper, "MEDIAN(") {
			hasAgg = true
		}
	}

	var winDefs []WindowDef
	for _, proj := range projections {
		if w, ok := ParseWindowDef(proj); ok {
			winDefs = append(winDefs, *w)
		}
	}

	var joins []*JoinInfo
	for _, j := range stmt.Joins {
		leftKey := ""
		rightKey := ""
		var onFilters []string
		if j.On != nil {
			onParts := splitASTAndExpr(j.On)
			for _, part := range onParts {
				if bin, ok := part.(*proc.BinaryExpr); ok && bin.Op == proc.TokenEq && leftKey == "" {
					leftKey = bin.Left.String()
					rightKey = bin.Right.String()
				} else {
					onFilters = append(onFilters, part.String())
				}
			}
		}
		rTable := cleanIdent(j.Table.Name)
		rAlias := cleanIdent(j.Table.Alias)
		if rAlias == "" {
			rAlias = rTable
		}
		filterCond := ""
		if len(onFilters) > 0 {
			filterCond = strings.Join(onFilters, " AND ")
		}
		joins = append(joins, &JoinInfo{
			Type:     j.Type,
			Table:    rTable,
			Alias:    rAlias,
			LeftKey:  cleanIdent(leftKey),
			RightKey: cleanIdent(rightKey),
			Filter:   filterCond,
			Hints:    j.Table.Hints,
		})
	}

	var joinInfo *JoinInfo
	if len(joins) > 0 {
		joinInfo = joins[0]
	}

	whereClause := ""
	if stmt.Where != nil {
		stmt.Where = proc.FoldConstants(stmt.Where)
		whereClause = stmt.Where.String()
	}

	var groupByCols []string
	for _, g := range stmt.GroupBy {
		groupByCols = append(groupByCols, g.String())
	}
	if len(groupByCols) > 0 {
		hasAgg = true
	}

	havingClause := ""
	if stmt.Having != nil {
		havingClause = stmt.Having.String()
		hasAgg = true
	}

	var orderByClause string
	if len(stmt.OrderBy) > 0 {
		var obStrs []string
		for _, o := range stmt.OrderBy {
			obStrs = append(obStrs, o.String())
		}
		orderByClause = strings.Join(obStrs, ", ")
	}

	return &QueryInfo{
		RawQuery:      stmt.RawSQL,
		CTEs:          ctes,
		TableName:     stmt.From.Name,
		TableAlias:    stmt.From.Alias,
		TableHints:    stmt.From.Hints,
		Join:          joinInfo,
		Joins:         joins,
		Projections:   projections,
		WindowDefs:    winDefs,
		WhereClause:   whereClause,
		GroupByCols:   groupByCols,
		GroupByMod:    stmt.GroupByMod,
		HavingClause:  havingClause,
		OrderByClause: orderByClause,
		LimitCount:    stmt.Limit,
		OffsetCount:   stmt.Offset,
		HasAggregates: hasAgg,
		Distinct:      stmt.Distinct,
		IsAnalyze:     stmt.IsAnalyze,
		QueryHints:    stmt.QueryHints,
		AST:           stmt,
	}
}

// ParseSelect parses a SELECT statement (including CTEs) into QueryInfo.
func (p *Planner) ParseSelect(query string) (*QueryInfo, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(query), ";")

	// Try AST-based parsing first (only if not containing window function OVER)
	if !strings.Contains(strings.ToUpper(cleaned), " OVER ") && !strings.Contains(strings.ToUpper(cleaned), " OVER(") {
		if parsedStmt, err := proc.ParseSQL(cleaned); err == nil {
			if sel, ok := parsedStmt.(*proc.GeneralSelectStmt); ok {
				qInfo := FromAST(sel)
				qInfo.RawQuery = query
				return qInfo, nil
			}
		}
	}

	isAnalyze := false
	upper := strings.ToUpper(cleaned)
	if strings.HasPrefix(upper, "EXPLAIN ANALYZE ") {
		isAnalyze = true
		cleaned = strings.TrimSpace(cleaned[16:])
	} else if strings.HasPrefix(upper, "EXPLAIN ") {
		cleaned = strings.TrimSpace(cleaned[8:])
	}

	ctes, mainSQL := parseCTEs(cleaned)

	var projStr, fromClause, whereClause, groupByStr, havingClause, orderByClause string
	var limitCount int64 = -1
	var offsetCount int64 = 0

	if pStr, fClause, wClause, gStr, hClause, oClause, lCount, offCount, ok := splitSelectClauses(mainSQL); ok {
		projStr = pStr
		fromClause = fClause
		whereClause = wClause
		groupByStr = gStr
		havingClause = hClause
		orderByClause = oClause
		limitCount = lCount
		offsetCount = offCount
	} else {
		selectRe := regexp.MustCompile(`(?is)^SELECT\s+(.*?)\s+FROM\s+(.*?)(?:\s+WHERE\s+(.*?))?(?:\s+GROUP\s+BY\s+(.*?))?(?:\s+HAVING\s+(.*?))?(?:\s+ORDER\s+BY\s+(.*?))?(?:\s+LIMIT\s+(\d+))?(?:\s+OFFSET\s+(\d+))?$`)
		matches := selectRe.FindStringSubmatch(mainSQL)
		if len(matches) < 3 {
			return nil, fmt.Errorf("invalid SELECT statement for planning: %q", mainSQL)
		}
		projStr = strings.TrimSpace(matches[1])
		fromClause = strings.TrimSpace(matches[2])
		if len(matches) > 3 {
			whereClause = strings.TrimSpace(matches[3])
		}
		if len(matches) > 4 {
			groupByStr = strings.TrimSpace(matches[4])
		}
		if len(matches) > 5 {
			havingClause = strings.TrimSpace(matches[5])
		}
		if len(matches) > 6 {
			orderByClause = strings.TrimSpace(matches[6])
		}
		if len(matches) > 7 && matches[7] != "" {
			if n, err := strconv.ParseInt(matches[7], 10, 64); err == nil {
				limitCount = n
			}
		}
		if len(matches) > 8 && matches[8] != "" {
			if n, err := strconv.ParseInt(matches[8], 10, 64); err == nil {
				offsetCount = n
			}
		}
	}

	if whereClause != "" {
		pExpr := proc.NewParser(proc.NewLexer(whereClause)).ParseExpr()
		if pExpr != nil {
			folded := proc.FoldConstants(pExpr)
			whereClause = folded.String()
		}
	}

	baseTable, baseAlias, joins, err := parseFromAndJoins(fromClause)
	if err != nil {
		return nil, err
	}
	var joinInfo *JoinInfo
	if len(joins) > 0 {
		joinInfo = joins[0]
	}

	isDistinct := false
	if strings.HasPrefix(strings.ToUpper(projStr), "DISTINCT ") {
		isDistinct = true
		projStr = strings.TrimSpace(projStr[9:])
	}

	var projections []string
	if projStr == "*" || projStr == "" {
		projections = []string{"*"}
	} else {
		for _, col := range SplitProjections(projStr) {
			c := strings.TrimSpace(col)
			if c != "" {
				projections = append(projections, c)
			}
		}
	}

	var groupByCols []string
	groupByMod := proc.GroupByNormal
	if groupByStr != "" {
		gTrim := strings.TrimSpace(groupByStr)
		gUpper := strings.ToUpper(gTrim)
		if strings.HasPrefix(gUpper, "ROLLUP") {
			groupByMod = proc.GroupByRollup
			inner := strings.TrimSpace(gTrim[6:])
			inner = strings.TrimPrefix(inner, "(")
			inner = strings.TrimSuffix(inner, ")")
			for _, col := range strings.Split(inner, ",") {
				c := strings.TrimSpace(col)
				if c != "" {
					groupByCols = append(groupByCols, c)
				}
			}
		} else if strings.HasPrefix(gUpper, "CUBE") {
			groupByMod = proc.GroupByCube
			inner := strings.TrimSpace(gTrim[4:])
			inner = strings.TrimPrefix(inner, "(")
			inner = strings.TrimSuffix(inner, ")")
			for _, col := range strings.Split(inner, ",") {
				c := strings.TrimSpace(col)
				if c != "" {
					groupByCols = append(groupByCols, c)
				}
			}
		} else if strings.HasSuffix(gUpper, "WITH ROLLUP") {
			groupByMod = proc.GroupByRollup
			inner := strings.TrimSpace(gTrim[:len(gTrim)-11])
			for _, col := range strings.Split(inner, ",") {
				c := strings.TrimSpace(col)
				if c != "" {
					groupByCols = append(groupByCols, c)
				}
			}
		} else if strings.HasSuffix(gUpper, "WITH CUBE") {
			groupByMod = proc.GroupByCube
			inner := strings.TrimSpace(gTrim[:len(gTrim)-9])
			for _, col := range strings.Split(inner, ",") {
				c := strings.TrimSpace(col)
				if c != "" {
					groupByCols = append(groupByCols, c)
				}
			}
		} else {
			for _, col := range strings.Split(groupByStr, ",") {
				c := strings.TrimSpace(col)
				if c != "" {
					groupByCols = append(groupByCols, c)
				}
			}
		}
	}

	hasAgg := len(groupByCols) > 0 || havingClause != ""
	for _, proj := range projections {
		pUpper := strings.ToUpper(proj)
		if strings.Contains(pUpper, "OVER") {
			continue
		}
		if strings.Contains(pUpper, "COUNT(") || strings.Contains(pUpper, "SUM(") ||
			strings.Contains(pUpper, "AVG(") || strings.Contains(pUpper, "MIN(") ||
			strings.Contains(pUpper, "MAX(") || strings.Contains(pUpper, "GROUPING(") ||
			strings.Contains(pUpper, "PERCENTILE_CONT(") || strings.Contains(pUpper, "PERCENTILE_DISC(") ||
			strings.Contains(pUpper, "APPROX_PERCENTILE(") || strings.Contains(pUpper, "MEDIAN(") {
			hasAgg = true
			break
		}
	}

	var winDefs []WindowDef
	for _, proj := range projections {
		if w, ok := ParseWindowDef(proj); ok {
			winDefs = append(winDefs, *w)
		}
	}

	return &QueryInfo{
		RawQuery:      query,
		CTEs:          ctes,
		TableName:     baseTable,
		TableAlias:    baseAlias,
		Join:          joinInfo,
		Joins:         joins,
		Projections:   projections,
		WindowDefs:    winDefs,
		WhereClause:   whereClause,
		GroupByCols:   groupByCols,
		GroupByMod:    groupByMod,
		HavingClause:  havingClause,
		OrderByClause: orderByClause,
		LimitCount:    limitCount,
		OffsetCount:   offsetCount,
		HasAggregates: hasAgg,
		Distinct:      isDistinct,
		IsAnalyze:     isAnalyze,
	}, nil
}

func parseCTEs(query string) ([]CTEInfo, string) {
	trimmed := strings.TrimSpace(query)
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, "WITH ") {
		return nil, trimmed
	}

	cur := trimmed[5:]
	var ctes []CTEInfo

	for {
		cur = strings.TrimSpace(cur)
		asIdx := strings.Index(strings.ToUpper(cur), " AS")
		if asIdx == -1 {
			break
		}
		cteName := strings.Trim(strings.TrimSpace(cur[:asIdx]), "\"`[]")
		afterAs := strings.TrimSpace(cur[asIdx+3:])
		if !strings.HasPrefix(afterAs, "(") {
			break
		}

		depth := 0
		closeIdx := -1
		for i := 0; i < len(afterAs); i++ {
			if afterAs[i] == '(' {
				depth++
			} else if afterAs[i] == ')' {
				depth--
				if depth == 0 {
					closeIdx = i
					break
				}
			}
		}

		if closeIdx == -1 {
			break
		}

		innerSQL := strings.TrimSpace(afterAs[1:closeIdx])
		ctes = append(ctes, CTEInfo{
			Name:     cteName,
			InnerSQL: innerSQL,
		})

		remaining := strings.TrimSpace(afterAs[closeIdx+1:])
		if strings.HasPrefix(remaining, ",") {
			cur = strings.TrimSpace(remaining[1:])
			continue
		} else {
			return ctes, remaining
		}
	}

	return nil, trimmed
}

// SplitProjections splits projection list while respecting nested parentheses and string literals.
func SplitProjections(s string) []string {
	var result []string
	var cur strings.Builder
	depth := 0
	inString := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '\'' {
			inString = !inString
			cur.WriteByte(ch)
			continue
		}

		if !inString {
			if ch == '(' {
				depth++
			} else if ch == ')' && depth > 0 {
				depth--
			}
		}

		if ch == ',' && depth == 0 && !inString {
			result = append(result, strings.TrimSpace(cur.String()))
			cur.Reset()
		} else {
			cur.WriteByte(ch)
		}
	}
	if cur.Len() > 0 {
		result = append(result, strings.TrimSpace(cur.String()))
	}
	return result
}

// parseFromAndJoins parses the FROM clause and extracts the base table and any number of chained JOINs.
func parseFromAndJoins(fromClause string) (string, string, []*JoinInfo, error) {
	joinPattern := regexp.MustCompile(`(?is)(?:(INNER|LEFT(?:\s+OUTER)?|RIGHT(?:\s+OUTER)?|FULL(?:\s+OUTER)?)\s+)?JOIN\s+([^\s]+)(?:\s+(?:AS\s+)?([a-zA-Z0-9_]+))?\s+ON\s+([^\s=]+)\s*=\s*([^\s=;]+)`)
	matches := joinPattern.FindAllStringSubmatchIndex(fromClause, -1)
	if len(matches) == 0 {
		baseTable, baseAlias := parseTableAndAlias(fromClause)
		return baseTable, baseAlias, nil, nil
	}

	// Base table is before the first JOIN
	firstJoinStart := matches[0][0]
	basePart := strings.TrimSpace(fromClause[:firstJoinStart])
	baseTable, baseAlias := parseTableAndAlias(basePart)

	var joins []*JoinInfo
	for _, m := range matches {
		var joinType, rightTable, rightAlias, leftKey, rightKey string
		if m[2] != -1 && m[3] != -1 {
			joinType = fromClause[m[2]:m[3]]
		}
		if m[4] != -1 && m[5] != -1 {
			rightTable = fromClause[m[4]:m[5]]
		}
		if m[6] != -1 && m[7] != -1 {
			rightAlias = fromClause[m[6]:m[7]]
		}
		if m[8] != -1 && m[9] != -1 {
			leftKey = fromClause[m[8]:m[9]]
		}
		if m[10] != -1 && m[11] != -1 {
			rightKey = fromClause[m[10]:m[11]]
		}

		jType := "INNER"
		uType := strings.ToUpper(strings.TrimSpace(joinType))
		if strings.HasPrefix(uType, "LEFT") {
			jType = "LEFT"
		} else if strings.HasPrefix(uType, "RIGHT") {
			jType = "RIGHT"
		} else if strings.HasPrefix(uType, "FULL") {
			jType = "FULL"
		}
		rightTable = cleanIdent(rightTable)
		rightAlias = cleanIdent(rightAlias)
		if rightAlias == "" || strings.EqualFold(rightAlias, "ON") {
			rightAlias = rightTable
		}

		joins = append(joins, &JoinInfo{
			Type:     jType,
			Table:    rightTable,
			Alias:    rightAlias,
			LeftKey:  cleanIdent(leftKey),
			RightKey: cleanIdent(rightKey),
		})
	}

	return baseTable, baseAlias, joins, nil
}

func parseFromAndJoin(fromClause string) (string, string, *JoinInfo, error) {
	baseTable, baseAlias, joins, err := parseFromAndJoins(fromClause)
	if err != nil || len(joins) == 0 {
		return baseTable, baseAlias, nil, err
	}
	return baseTable, baseAlias, joins[0], nil
}

func parseTableAndAlias(str string) (string, string) {
	tokens := strings.Fields(strings.TrimSpace(str))
	if len(tokens) == 0 {
		return "", ""
	}
	tbl := cleanIdent(tokens[0])
	if len(tokens) == 1 {
		return tbl, tbl
	}
	if len(tokens) == 2 {
		return tbl, cleanIdent(tokens[1])
	}
	if len(tokens) == 3 && strings.EqualFold(tokens[1], "AS") {
		return tbl, cleanIdent(tokens[2])
	}
	return tbl, cleanIdent(tokens[1])
}

// BuildPlan produces an optimized PlanNode tree for the given query and schema.
func (p *Planner) BuildPlan(q *QueryInfo, schema *storage.TableSchema, totalRows int64) *PlanNode {
	return p.BuildJoinPlan(q, schema, totalRows, nil, 0)
}

// BuildJoinPlan produces an optimized PlanNode tree considering joins, aggregation, grouping, and CTEs.
func (p *Planner) BuildJoinPlan(q *QueryInfo, schema *storage.TableSchema, totalRows int64, rightSchema *storage.TableSchema, rightRows int64) *PlanNode {
	var leftStats *TableStats
	if totalRows > 0 {
		leftStats = NewTableStats(q.TableName, totalRows)
	}
	var rightStats *TableStats
	if rightRows > 0 && q.Join != nil {
		rightStats = NewTableStats(q.Join.Table, rightRows)
	}
	return p.BuildJoinPlanWithStats(q, schema, leftStats, rightSchema, rightStats)
}

func (p *Planner) BuildJoinPlanWithStats(q *QueryInfo, schema *storage.TableSchema, leftStats *TableStats, rightSchema *storage.TableSchema, rightStats *TableStats) *PlanNode {
	rSchemas := make(map[string]*storage.TableSchema)
	rStats := make(map[string]*TableStats)
	if q.Join != nil {
		tbl := cleanIdent(q.Join.Table)
		if rightSchema != nil {
			rSchemas[q.Join.Table] = rightSchema
			rSchemas[tbl] = rightSchema
			if q.Join.Alias != "" {
				rSchemas[q.Join.Alias] = rightSchema
				rSchemas[cleanIdent(q.Join.Alias)] = rightSchema
			}
		}
		if rightStats != nil {
			rStats[q.Join.Table] = rightStats
			rStats[tbl] = rightStats
			if q.Join.Alias != "" {
				rStats[q.Join.Alias] = rightStats
				rStats[cleanIdent(q.Join.Alias)] = rightStats
			}
		}
	}
	return p.BuildMultiJoinPlanWithStats(q, schema, leftStats, rSchemas, rStats)
}

// parseIndexHint inspects table hints for INDEX(...) or INDEX = ... directives.
// Returns forceIndex name (or empty), and whether full table scan is forced (INDEX(0)).
func parseIndexHint(hints []string) (forceIndex string, forceTableScan bool) {
	for _, h := range hints {
		trimmed := strings.TrimSpace(h)
		u := strings.ToUpper(trimmed)
		if strings.HasPrefix(u, "INDEX") {
			rest := strings.TrimSpace(trimmed[5:])
			rest = strings.TrimPrefix(rest, "=")
			rest = strings.TrimSpace(rest)
			rest = strings.TrimPrefix(rest, "(")
			rest = strings.TrimSuffix(rest, ")")
			rest = strings.TrimSpace(rest)
			rest = strings.Trim(rest, "[]\"'`")
			if rest == "0" || rest == "" {
				return "", true
			}
			return rest, false
		}
	}
	return "", false
}

// hasQueryHint checks if a specific query hint (e.g. "HASH JOIN", "FORCE ORDER") is present.
func hasQueryHint(hints []string, target string) bool {
	targetUpper := strings.ToUpper(strings.TrimSpace(target))
	for _, h := range hints {
		u := strings.ToUpper(strings.TrimSpace(h))
		if u == targetUpper || strings.HasPrefix(u, targetUpper) {
			return true
		}
	}
	return false
}

// BuildMultiJoinPlanWithStats produces an optimized PlanNode tree supporting single or chained multi-table joins.
func (p *Planner) BuildMultiJoinPlanWithStats(q *QueryInfo, schema *storage.TableSchema, leftStats *TableStats, rightSchemas map[string]*storage.TableSchema, rightStats map[string]*TableStats) *PlanNode {
	estimatedTotal := int64(100)
	if leftStats != nil && leftStats.RowCount > 0 {
		estimatedTotal = leftStats.RowCount
	}

	seqPages := float64(estimatedTotal) / float64(RowsPerPageDefault)
	if seqPages < 1 {
		seqPages = 1
	}
	tableScanTotalCost := seqPages*SeqPageCost + float64(estimatedTotal)*CPUTupleCost

	var scanNode *PlanNode

	// Analyze predicate pushdown across primary and joined relations
	pushdownRes := PushDownPredicates(q, schema, rightSchemas)

	eqRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_\.]+)\s*(<=|>=|!=|<>|<|>|==|=|LIKE)\s*(.+)$`)

	var chosenIndex *storage.IndexSchema
	var chosenCond string
	var chosenCol, chosenOp, chosenVal string
	var chosenEstRows int64
	var chosenCost float64
	var chosenCovering bool

	forceIndex, forceTableScan := parseIndexHint(q.TableHints)

	if !forceTableScan && schema != nil {
		if forceIndex != "" {
			for i := range schema.Indexes {
				if strings.EqualFold(schema.Indexes[i].Name, forceIndex) {
					chosenIndex = &schema.Indexes[i]
					break
				}
			}
			if chosenIndex != nil {
				matchedCond := false
				for _, cond := range pushdownRes.PrimaryConds {
					m := eqRe.FindStringSubmatch(cond)
					if len(m) == 4 {
						colName := strings.TrimSpace(m[1])
						if dotIdx := strings.Index(colName, "."); dotIdx != -1 {
							colName = colName[dotIdx+1:]
						}
						if strings.EqualFold(colName, chosenIndex.Columns[0]) {
							chosenCond = cond
							chosenCol = colName
							chosenOp = strings.TrimSpace(m[2])
							chosenVal = strings.TrimSpace(m[3])
							matchedCond = true
							break
						}
					}
				}
				if !matchedCond {
					chosenCol = chosenIndex.Columns[0]
					chosenOp = "="
					chosenVal = "''"
					chosenCond = ""
				}
				chosenEstRows = int64(math.Max(1, float64(estimatedTotal)*0.1))
				chosenCost = 0.1
			}
		} else if len(pushdownRes.PrimaryConds) > 0 {
			// --- Composite index prefix match ---
			// Collect all equality conditions from pushed-down predicates.
			eqColSet := make(map[string]bool)
			eqColVals := make(map[string]string)
			onlyEqRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_\.]+)\s*=\s*(.+)$`)
		for _, cond := range pushdownRes.PrimaryConds {
			if m := onlyEqRe.FindStringSubmatch(cond); len(m) == 3 {
				rawCol := m[1]
				if dot := strings.Index(rawCol, "."); dot != -1 {
					rawCol = rawCol[dot+1:]
				}
				col := strings.ToLower(cleanIdent(rawCol))
				val := strings.TrimSpace(m[2])
				eqColSet[col] = true
				eqColVals[col] = val
			}
		}
		if len(eqColSet) >= 2 {
			if compIdx, prefixLen := schema.FindBestCompositeIndex(eqColSet); compIdx != nil && prefixLen >= 2 {
				sel := math.Pow(0.1, float64(prefixLen))
				estRows := int64(math.Ceil(float64(estimatedTotal) * sel))
				if estRows < 1 {
					estRows = 1
				}
				indexPagesCost := (float64(estRows) / float64(RowsPerPageDefault)) * RandomPageCost
				if indexPagesCost < 0.05 {
					indexPagesCost = 0.05
				}
				compCost := 0.25 + float64(estRows)*(CPUIndexTupleCost+CPUTupleCost) + indexPagesCost
				if compCost < tableScanTotalCost {
					// Compose a representative condition label for the plan node.
					var condParts []string
					for _, col := range compIdx.Columns[:prefixLen] {
						if v, ok := eqColVals[strings.ToLower(col)]; ok {
							condParts = append(condParts, fmt.Sprintf("%s = %s", col, v))
						}
					}
					chosenIndex = compIdx
					chosenCond = strings.Join(condParts, " AND ")
					chosenCol = strings.Join(compIdx.Columns[:prefixLen], ",")
					chosenOp = "="
					chosenVal = ""
					chosenEstRows = estRows
					chosenCost = compCost
					chosenCovering = false
				}
			}
		}

		// --- Single-column index match (fallback) ---
		for _, cond := range pushdownRes.PrimaryConds {
			if m := eqRe.FindStringSubmatch(cond); len(m) == 4 {
				rawCol := m[1]
				if idxDot := strings.Index(rawCol, "."); idxDot != -1 {
					rawCol = rawCol[idxDot+1:]
				}
				colName := cleanIdent(rawCol)
				op := m[2]
				valStr := strings.TrimSpace(m[3])

				if idx, found := schema.FindIndexByColumn(colName); found {
					// Skip if a composite index covering more columns is already chosen.
					if chosenIndex != nil && len(chosenIndex.Columns) > 1 {
						continue
					}
					sel := 0.1
					if idx.Unique && (op == "=" || op == "==") {
						sel = 1.0 / float64(estimatedTotal)
					} else if leftStats != nil {
						sel = leftStats.EstimateSelectivity(colName, op, valStr)
					}

					estRows := int64(math.Ceil(float64(estimatedTotal) * sel))
					if estRows < 1 {
						estRows = 1
					}

					startupCost := 0.25
					indexPagesCost := (float64(estRows) / float64(RowsPerPageDefault)) * RandomPageCost
					if indexPagesCost < 0.05 {
						indexPagesCost = 0.05
					}

					// Check covering index eligibility (Index-Only Scan)
					isCovering := false
					if len(q.Projections) > 0 {
						hasStar := false
						for _, proj := range q.Projections {
							trimmed := strings.TrimSpace(proj)
							if trimmed == "*" || strings.HasSuffix(trimmed, ".*") {
								hasStar = true
								break
							}
						}

						if !hasStar {
							coveredCols := make(map[string]bool)
							for _, c := range idx.Columns {
								coveredCols[strings.ToLower(c)] = true
							}
							for _, c := range idx.IncludeCols {
								coveredCols[strings.ToLower(c)] = true
							}
							if pkCol, err := schema.PrimaryKeyColumn(); err == nil && pkCol != nil {
								coveredCols[strings.ToLower(pkCol.Name)] = true
							}

							allCovered := true
							for _, proj := range q.Projections {
								clean := strings.ToLower(strings.TrimSpace(proj))
								if clean == "count(*)" || clean == "count(1)" {
									continue
								}
								if dotIdx := strings.Index(clean, "."); dotIdx != -1 {
									clean = clean[dotIdx+1:]
								}
								if asIdx := strings.Index(clean, " as "); asIdx != -1 {
									clean = strings.TrimSpace(clean[:asIdx])
								}
								if !coveredCols[clean] {
									allCovered = false
									break
								}
							}
							isCovering = allCovered
						}
					}

					var indexScanCost float64
					if isCovering {
						indexScanCost = startupCost + float64(estRows)*CPUIndexTupleCost + indexPagesCost*0.6
					} else {
						indexScanCost = startupCost + float64(estRows)*(CPUIndexTupleCost+CPUTupleCost) + indexPagesCost
					}

					if indexScanCost < tableScanTotalCost && (chosenIndex == nil || indexScanCost < chosenCost) {
						chosenIndex = idx
						chosenCond = cond
						chosenCol = colName
						chosenOp = op
						chosenVal = valStr
						chosenEstRows = estRows
						chosenCost = indexScanCost
						chosenCovering = isCovering
					}
				}
			} else {
				// --- Functional expression index match (e.g. doc->>'city' = 'Seoul' or JSON_VALUE(doc, '$.city') = 'Seoul') ---
				pExpr := proc.NewParser(proc.NewLexer(cond))
				if parsedExpr := pExpr.ParseExpression(proc.PrecLowest); parsedExpr != nil {
					if bin, ok := parsedExpr.(*proc.BinaryExpr); ok && (bin.Op == proc.TokenAssign || bin.Op == proc.TokenEq) {
						colName, jsonPath, isJSON := proc.ExtractJSONExprInfo(bin.Left)
						if isJSON {
							if idxDot := strings.Index(colName, "."); idxDot != -1 {
								colName = colName[idxDot+1:]
							}
							colName = cleanIdent(colName)
							if idx, found := schema.FindExpressionIndex(colName, jsonPath); found {
								if chosenIndex != nil && len(chosenIndex.Columns) > 1 {
									continue
								}
								sel := 0.1
								if idx.Unique {
									sel = 1.0 / float64(estimatedTotal)
								}
								estRows := int64(math.Ceil(float64(estimatedTotal) * sel))
								if estRows < 1 {
									estRows = 1
								}
								startupCost := 0.25
								indexPagesCost := (float64(estRows) / float64(RowsPerPageDefault)) * RandomPageCost
								if indexPagesCost < 0.05 {
									indexPagesCost = 0.05
								}
								indexScanCost := startupCost + float64(estRows)*(CPUIndexTupleCost+CPUTupleCost) + indexPagesCost
								if indexScanCost < tableScanTotalCost && (chosenIndex == nil || indexScanCost < chosenCost) {
									chosenIndex = idx
									chosenCond = cond
									chosenCol = bin.Left.String()
									chosenOp = "="
									chosenVal = bin.Right.String()
									chosenEstRows = estRows
									chosenCost = indexScanCost
									chosenCovering = false
								}
							}
						}
					}
				}
			}
		}
	}
}

	if chosenIndex != nil {
		nodeType := NodeIndexScan
		if chosenCovering {
			nodeType = NodeIndexOnlyScan
		}
		scanNode = &PlanNode{
			Type:          nodeType,
			Relation:      q.TableName,
			Alias:         q.TableAlias,
			IndexName:     chosenIndex.Name,
			IndexCond:     fmt.Sprintf("(%s %s %s)", chosenCol, chosenOp, chosenVal),
			StartupCost:   0.25,
			TotalCost:     chosenCost,
			EstimatedRows: chosenEstRows,
		}

		var remainingPrimary []string
		for _, c := range pushdownRes.PrimaryConds {
			if c != chosenCond {
				remainingPrimary = append(remainingPrimary, c)
			}
		}
		if len(remainingPrimary) > 0 {
			filterCond := strings.Join(remainingPrimary, " AND ")
			filterCost := chosenCost + float64(chosenEstRows)*CPUOperatorCost
			remSel := 0.5
			if leftStats != nil {
				remSel = leftStats.EstimateCompoundSelectivity(remainingPrimary)
			}
			remEstRows := int64(math.Ceil(float64(chosenEstRows) * remSel))
			if remEstRows < 1 {
				remEstRows = 1
			}
			scanNode = &PlanNode{
				Type:          NodeFilter,
				FilterCond:    filterCond,
				StartupCost:   0.25,
				TotalCost:     filterCost,
				EstimatedRows: remEstRows,
				Children:      []*PlanNode{scanNode},
			}
		}
	} else {
		// Table Scan (Sequential Scan)
		var prunedParts []string
		if schema != nil && schema.IsPartitioned() {
			var avail []string
			if p.PartitionGetter != nil {
				avail = p.PartitionGetter(q.TableName)
			} else if leftStats != nil && len(leftStats.Partitions) > 0 {
				avail = leftStats.Partitions
			}
			prunedParts = PrunePartitions(schema, pushdownRes.PrimaryConds, avail)
		}

		scanNode = &PlanNode{
			Type:          NodeTableScan,
			Relation:      q.TableName,
			Alias:         q.TableAlias,
			StartupCost:   0.0,
			TotalCost:     tableScanTotalCost,
			EstimatedRows: estimatedTotal,
			Partitions:    prunedParts,
		}

		if len(pushdownRes.PrimaryConds) > 0 {
			sel := 0.33
			if leftStats != nil {
				sel = leftStats.EstimateCompoundSelectivity(pushdownRes.PrimaryConds)
			}
			filterEstRows := int64(math.Ceil(float64(estimatedTotal) * sel))
			if filterEstRows < 1 {
				filterEstRows = 1
			}
			filterCost := tableScanTotalCost + float64(estimatedTotal)*CPUOperatorCost
			scanNode = &PlanNode{
				Type:          NodeFilter,
				FilterCond:    strings.Join(pushdownRes.PrimaryConds, " AND "),
				StartupCost:   0.0,
				TotalCost:     filterCost,
				EstimatedRows: filterEstRows,
				Children:      []*PlanNode{scanNode},
			}
		}
	}

	var currentNode *PlanNode = scanNode
	currentEstimatedRows := scanNode.EstimatedRows

	// Process all chained joins with CBO Join Order Optimization
	var joinList []*JoinInfo
	if len(q.Joins) > 0 {
		joinList = q.Joins
	} else if q.Join != nil {
		joinList = []*JoinInfo{q.Join}
	}

	// Step 1: Pre-attach pushed down join filters
	for idx, join := range joinList {
		if pushed, ok := pushdownRes.JoinConds[idx]; ok && len(pushed) > 0 {
			pStr := strings.Join(pushed, " AND ")
			if join.Filter != "" {
				join.Filter = "(" + join.Filter + ") AND (" + pStr + ")"
			} else {
				join.Filter = pStr
			}
		}
	}

	availableTables := make(map[string]bool)
	availableTables[strings.ToLower(cleanIdent(q.TableName))] = true
	if q.TableAlias != "" {
		availableTables[strings.ToLower(cleanIdent(q.TableAlias))] = true
	}

	// Step 2: Traverse and optimize join sequence (grouping contiguous INNER joins for reordering)
	i := 0
	for i < len(joinList) {
		if joinList[i].Type != "INNER" {
			// Non-inner join (e.g., LEFT OUTER JOIN): maintain sequence to preserve NULL-extension semantics
			join := joinList[i]
			currentNode, currentEstimatedRows = buildSingleJoinNode(currentNode, currentEstimatedRows, join, rightSchemas, rightStats, q.QueryHints)
			availableTables[strings.ToLower(cleanIdent(join.Table))] = true
			if join.Alias != "" {
				availableTables[strings.ToLower(cleanIdent(join.Alias))] = true
			}
			i++
			continue
		}

		// Collect contiguous slice of INNER joins
		j := i
		for j < len(joinList) && joinList[j].Type == "INNER" {
			j++
		}
		innerBlock := joinList[i:j]

		forceOrder := hasQueryHint(q.QueryHints, "FORCE ORDER")
		if forceOrder || len(innerBlock) <= 1 {
			for _, join := range innerBlock {
				currentNode, currentEstimatedRows = buildSingleJoinNode(currentNode, currentEstimatedRows, join, rightSchemas, rightStats, q.QueryHints)
				availableTables[strings.ToLower(cleanIdent(join.Table))] = true
				if join.Alias != "" {
					availableTables[strings.ToLower(cleanIdent(join.Alias))] = true
				}
			}
		} else {
			// CBO Cost-Based Join Reordering via Selinger Dynamic Programming (B-4)
			bestNode, bestEstRows, bestTables, ok := optimizeJoinOrderDP(
				currentNode, currentEstimatedRows, availableTables,
				innerBlock, rightSchemas, rightStats, q.QueryHints,
			)
			if ok && bestNode != nil {
				currentNode = bestNode
				currentEstimatedRows = bestEstRows
				availableTables = bestTables
			} else {
				// Fallback to original order
				for _, join := range innerBlock {
					currentNode, currentEstimatedRows = buildSingleJoinNode(currentNode, currentEstimatedRows, join, rightSchemas, rightStats, q.QueryHints)
					availableTables[strings.ToLower(cleanIdent(join.Table))] = true
					if join.Alias != "" {
						availableTables[strings.ToLower(cleanIdent(join.Alias))] = true
					}
				}
			}
		}

		i = j
	}


	// Filter Node (WHERE) for remaining cross-table or outer-join conditions
	if len(pushdownRes.RemainingConds) > 0 {
		remWhere := strings.Join(pushdownRes.RemainingConds, " AND ")
		sel := 0.33
		if leftStats != nil && len(pushdownRes.RemainingConds) == 1 {
			eqRe := regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_\.]+)\s*(=|<|<=|>|>=|!=|<>)\s*(.+)$`)
			if m := eqRe.FindStringSubmatch(remWhere); len(m) == 4 {
				col := m[1]
				if idx := strings.Index(col, "."); idx != -1 {
					col = col[idx+1:]
				}
				sel = leftStats.EstimateSelectivity(col, m[2], strings.TrimSpace(m[3]))
			}
		}

		filterEstRows := int64(math.Ceil(float64(currentNode.EstimatedRows) * sel))
		if filterEstRows < 1 {
			filterEstRows = 1
		}
		filterCost := currentNode.TotalCost + float64(currentNode.EstimatedRows)*CPUOperatorCost

		filterNode := &PlanNode{
			Type:          NodeFilter,
			FilterCond:    remWhere,
			StartupCost:   currentNode.StartupCost,
			TotalCost:     filterCost,
			EstimatedRows: filterEstRows,
			Children:      []*PlanNode{currentNode},
		}
		currentNode = filterNode
	}

	// Aggregate / GROUP BY Node
	if q.HasAggregates {
		aggEstRows := int64(1)
		if len(q.GroupByCols) > 0 {
			aggEstRows = int64(math.Ceil(float64(currentNode.EstimatedRows) * 0.2))
			if aggEstRows < 1 {
				aggEstRows = 1
			}
		}

		aggCost := currentNode.TotalCost + float64(currentNode.EstimatedRows)*0.005
		aggNode := &PlanNode{
			Type:            NodeAggregate,
			GroupKeys:       q.GroupByCols,
			GroupByModifier: q.GroupByMod,
			Projections:     q.Projections,
			HavingCond:      q.HavingClause,
			StartupCost:     currentNode.TotalCost,
			TotalCost:       aggCost,
			EstimatedRows:   aggEstRows,
			Children:        []*PlanNode{currentNode},
		}
		currentNode = aggNode
	}

	// Window Functions Node
	var winDefs []WindowDef
	if len(q.WindowDefs) > 0 {
		winDefs = q.WindowDefs
	} else {
		for _, proj := range q.Projections {
			if w, ok := ParseWindowDef(proj); ok {
				winDefs = append(winDefs, *w)
			}
		}
	}
	if len(winDefs) > 0 {
		winNode := &PlanNode{
			Type:          NodeWindow,
			WindowDefs:    winDefs,
			StartupCost:   currentNode.TotalCost,
			TotalCost:     currentNode.TotalCost + float64(currentNode.EstimatedRows)*0.01,
			EstimatedRows: currentNode.EstimatedRows,
			Children:      []*PlanNode{currentNode},
		}
		currentNode = winNode
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
	if q.LimitCount >= 0 || q.OffsetCount > 0 {
		limitEstRows := currentNode.EstimatedRows
		if q.LimitCount >= 0 && q.LimitCount < limitEstRows {
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
			OffsetCount:   q.OffsetCount,
			StartupCost:   currentNode.StartupCost,
			TotalCost:     limitCost,
			EstimatedRows: limitEstRows,
			Children:      []*PlanNode{currentNode},
		}
		currentNode = limitNode
	}

	// Wrap in CTE nodes if CTEs are present
	if len(q.CTEs) > 0 {
		var cteChildren []*PlanNode
		for _, cte := range q.CTEs {
			matNode := &PlanNode{
				Type:          NodeMaterialize,
				CTEName:       cte.Name,
				StartupCost:   0.5,
				TotalCost:     2.0,
				EstimatedRows: 10,
			}
			cteChildren = append(cteChildren, matNode)
		}
		cteChildren = append(cteChildren, currentNode)

		cteRoot := &PlanNode{
			Type:          NodeCTE,
			StartupCost:   currentNode.StartupCost + float64(len(q.CTEs))*0.5,
			TotalCost:     currentNode.TotalCost + float64(len(q.CTEs))*2.0,
			EstimatedRows: currentNode.EstimatedRows,
			Children:      cteChildren,
		}
		currentNode = cteRoot
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
	case NodeMaterialize:
		if node.CTEName != "" {
			sb.WriteString(fmt.Sprintf(" (CTE: %s)", node.CTEName))
		}
	case NodeAggregate:
		if len(node.GroupKeys) > 0 {
			switch node.GroupByModifier {
			case proc.GroupByRollup:
				sb.WriteString(fmt.Sprintf(" (Group By: ROLLUP(%s))", strings.Join(node.GroupKeys, ", ")))
			case proc.GroupByCube:
				sb.WriteString(fmt.Sprintf(" (Group By: CUBE(%s))", strings.Join(node.GroupKeys, ", ")))
			default:
				sb.WriteString(fmt.Sprintf(" (Group By: %s)", strings.Join(node.GroupKeys, ", ")))
			}
		}
		if node.HavingCond != "" {
			sb.WriteString(fmt.Sprintf(" (Having: %s)", node.HavingCond))
		}
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
		} else if node.JoinType == "RIGHT" {
			sb.WriteString(" (Right)")
		} else if node.JoinType == "FULL" {
			sb.WriteString(" (Full)")
		}
		if node.HashCond != "" {
			sb.WriteString(fmt.Sprintf(" (hash_cond=%s)", node.HashCond))
		}
	case NodeMergeJoin:
		if node.JoinType == "LEFT" {
			sb.WriteString(" (Left)")
		} else if node.JoinType == "RIGHT" {
			sb.WriteString(" (Right)")
		} else if node.JoinType == "FULL" {
			sb.WriteString(" (Full)")
		}
		if node.MergeCond != "" {
			sb.WriteString(fmt.Sprintf(" (merge_cond=%s)", node.MergeCond))
		} else if node.JoinCond != "" {
			sb.WriteString(fmt.Sprintf(" (merge_cond=%s)", node.JoinCond))
		}
	case NodeHash:
		// Hash intermediate node
	case NodeTableScan:
		sb.WriteString(fmt.Sprintf(" on %s", node.Relation))
		if node.Alias != "" && node.Alias != node.Relation {
			sb.WriteString(fmt.Sprintf(" %s", node.Alias))
		}
		if len(node.Partitions) > 0 {
			sb.WriteString(fmt.Sprintf(" (Partitions: [%s])", strings.Join(node.Partitions, ", ")))
		}
	case NodeIndexScan, NodeIndexOnlyScan:
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
	case NodeWindow:
		sb.WriteString(" (Window Functions)")
	case NodeSetOp:
		sb.WriteString(fmt.Sprintf(" (%s)", node.SetOp))
	case NodeModifyTable:
		if node.Relation != "" {
			sb.WriteString(fmt.Sprintf(" on %s", node.Relation))
		}
	}

	sb.WriteString(fmt.Sprintf(" (cost=%.2f..%.2f rows=%d)", node.StartupCost, node.TotalCost, node.EstimatedRows))

	if analyze {
		startupMs := float64(node.ActualStartupUS) / 1000.0
		durationMs := float64(node.ActualDurationUS) / 1000.0
		loops := node.ActualLoops
		if loops == 0 {
			loops = 1
		}
		sb.WriteString(fmt.Sprintf(" (actual time=%.3f..%.3f rows=%d loops=%d)", startupMs, durationMs, node.ActualRows, loops))
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

// ParseWindowDef extracts a WindowDef from a projection item like "ROW_NUMBER() OVER (...) AS rn".
func ParseWindowDef(item string) (*WindowDef, bool) {
	trimmed := strings.TrimSpace(item)
	upper := strings.ToUpper(trimmed)
	overIdx := strings.Index(upper, "OVER")
	if overIdx == -1 {
		return nil, false
	}

	afterOver := strings.TrimSpace(trimmed[overIdx+4:])
	if !strings.HasPrefix(afterOver, "(") {
		return nil, false
	}

	depth := 0
	closeParenIdx := -1
	for i := 0; i < len(afterOver); i++ {
		if afterOver[i] == '(' {
			depth++
		} else if afterOver[i] == ')' {
			depth--
			if depth == 0 {
				closeParenIdx = i
				break
			}
		}
	}
	if closeParenIdx == -1 {
		return nil, false
	}

	overContent := strings.TrimSpace(afterOver[1:closeParenIdx])
	trailing := strings.TrimSpace(afterOver[closeParenIdx+1:])

	funcPart := strings.TrimSpace(trimmed[:overIdx])
	fnOpenParen := strings.Index(funcPart, "(")
	fnCloseParen := strings.LastIndex(funcPart, ")")
	if fnOpenParen == -1 || fnCloseParen == -1 || fnCloseParen < fnOpenParen {
		return nil, false
	}

	fnName := strings.ToUpper(strings.TrimSpace(funcPart[:fnOpenParen]))
	rawArgs := strings.TrimSpace(funcPart[fnOpenParen+1 : fnCloseParen])

	var args []string
	if strings.Contains(strings.ToUpper(funcPart), "WITHIN GROUP") {
		withinRe := regexp.MustCompile(`(?i)^\s*(PERCENTILE_CONT|PERCENTILE_DISC)\s*\(\s*([0-9\.]+)\s*\)\s*WITHIN\s+GROUP\s*\(\s*ORDER\s+BY\s+([a-zA-Z0-9_\.]+)(?:\s+(?:ASC|DESC))?\s*\)\s*$`)
		if m := withinRe.FindStringSubmatch(funcPart); len(m) == 4 {
			fnName = strings.ToUpper(m[1])
			args = []string{strings.Trim(m[3], "\"`[]'"), m[2]} // [targetCol, percentile]
		}
	} else if rawArgs != "" {
		for _, arg := range strings.Split(rawArgs, ",") {
			a := strings.Trim(strings.TrimSpace(arg), "\"`[]'")
			if a != "" {
				args = append(args, a)
			}
		}
	}

	alias := ""
	if trailing != "" {
		aliasUpper := strings.ToUpper(trailing)
		if strings.HasPrefix(aliasUpper, "AS ") {
			alias = strings.Trim(strings.TrimSpace(trailing[3:]), "\"`[]")
		} else {
			alias = strings.Trim(strings.TrimSpace(trailing), "\"`[]")
		}
	}

	rawExpr := trimmed
	if alias != "" && trailing != "" {
		rawExpr = strings.TrimSpace(trimmed[:overIdx+4]) + " (" + overContent + ")"
	}

	w := &WindowDef{
		Function: fnName,
		Args:     args,
		Alias:    alias,
		RawExpr:  rawExpr,
	}

	overContentUpper := strings.ToUpper(overContent)
	frameIdx := -1
	if idx := strings.Index(overContentUpper, "ROWS "); idx != -1 {
		frameIdx = idx
	} else if idx := strings.Index(overContentUpper, "RANGE "); idx != -1 {
		frameIdx = idx
	}

	frameStr := ""
	cleanOverContent := overContent
	if frameIdx != -1 {
		frameStr = strings.TrimSpace(overContent[frameIdx:])
		cleanOverContent = strings.TrimSpace(overContent[:frameIdx])
		overContentUpper = strings.ToUpper(cleanOverContent)
	}

	if frameStr != "" {
		w.Frame = parseWindowFrame(frameStr)
	}

	partIdx := strings.Index(overContentUpper, "PARTITION BY")
	orderIdx := strings.Index(overContentUpper, "ORDER BY")

	partStr := ""
	orderStr := ""

	if partIdx != -1 && orderIdx != -1 {
		if partIdx < orderIdx {
			partStr = strings.TrimSpace(cleanOverContent[partIdx+12 : orderIdx])
			orderStr = strings.TrimSpace(cleanOverContent[orderIdx+8:])
		} else {
			orderStr = strings.TrimSpace(cleanOverContent[orderIdx+8 : partIdx])
			partStr = strings.TrimSpace(cleanOverContent[partIdx+12:])
		}
	} else if partIdx != -1 {
		partStr = strings.TrimSpace(cleanOverContent[partIdx+12:])
	} else if orderIdx != -1 {
		orderStr = strings.TrimSpace(cleanOverContent[orderIdx+8:])
	}

	if partStr != "" {
		for _, p := range strings.Split(partStr, ",") {
			col := strings.Trim(strings.TrimSpace(p), "\"`[]")
			if col != "" {
				w.PartitionKeys = append(w.PartitionKeys, col)
			}
		}
	}

	if orderStr != "" {
		for _, o := range strings.Split(orderStr, ",") {
			fields := strings.Fields(strings.TrimSpace(o))
			if len(fields) > 0 {
				col := strings.Trim(fields[0], "\"`[]")
				dir := "ASC"
				if len(fields) > 1 && strings.EqualFold(fields[1], "DESC") {
					dir = "DESC"
				}
				w.OrderByKeys = append(w.OrderByKeys, SortKeyDef{
					Column:    col,
					Direction: dir,
				})
			}
		}
	}

	return w, true
}

func parseWindowFrame(frameStr string) *WindowFrame {
	s := strings.TrimSpace(frameStr)
	sUpper := strings.ToUpper(s)
	if strings.HasPrefix(sUpper, "ROWS ") {
		s = strings.TrimSpace(s[5:])
		sUpper = strings.TrimSpace(sUpper[5:])
	} else if strings.HasPrefix(sUpper, "RANGE ") {
		s = strings.TrimSpace(s[6:])
		sUpper = strings.TrimSpace(sUpper[6:])
	} else {
		return nil
	}

	if strings.HasPrefix(sUpper, "BETWEEN ") {
		rest := strings.TrimSpace(s[8:])
		andIdx := strings.Index(strings.ToUpper(rest), " AND ")
		if andIdx == -1 {
			return nil
		}
		startStr := strings.TrimSpace(rest[:andIdx])
		endStr := strings.TrimSpace(rest[andIdx+5:])
		startBound, ok1 := parseFrameBound(startStr)
		endBound, ok2 := parseFrameBound(endStr)
		if !ok1 || !ok2 {
			return nil
		}
		return &WindowFrame{Start: startBound, End: endBound}
	}

	// Shorthand: e.g. ROWS UNBOUNDED PRECEDING, ROWS 5 PRECEDING, ROWS CURRENT ROW
	startBound, ok := parseFrameBound(s)
	if !ok {
		return nil
	}
	return &WindowFrame{
		Start: startBound,
		End:   FrameBound{Type: FrameCurrentRow},
	}
}

func parseFrameBound(bStr string) (FrameBound, bool) {
	upper := strings.ToUpper(strings.TrimSpace(bStr))
	if upper == "UNBOUNDED PRECEDING" {
		return FrameBound{Type: FrameUnboundedPreceding}, true
	}
	if upper == "UNBOUNDED FOLLOWING" {
		return FrameBound{Type: FrameUnboundedFollowing}, true
	}
	if upper == "CURRENT ROW" {
		return FrameBound{Type: FrameCurrentRow}, true
	}
	if strings.HasSuffix(upper, " PRECEDING") {
		numStr := strings.TrimSpace(upper[:len(upper)-len(" PRECEDING")])
		if n, err := strconv.Atoi(numStr); err == nil && n >= 0 {
			return FrameBound{Type: FramePreceding, Offset: n}, true
		}
	}
	if strings.HasSuffix(upper, " FOLLOWING") {
		numStr := strings.TrimSpace(upper[:len(upper)-len(" FOLLOWING")])
		if n, err := strconv.Atoi(numStr); err == nil && n >= 0 {
			return FrameBound{Type: FrameFollowing, Offset: n}, true
		}
	}
	return FrameBound{}, false
}

func extractTableQualifier(key string) string {
	if dot := strings.Index(key, "."); dot != -1 {
		return strings.ToLower(cleanIdent(key[:dot]))
	}
	return ""
}

func extractColumnName(key string) string {
	if dot := strings.Index(key, "."); dot != -1 {
		return cleanIdent(key[dot+1:])
	}
	return cleanIdent(key)
}

func checkAndOrientJoinKeys(j *JoinInfo, availableTables map[string]bool, rSchema *storage.TableSchema) (*JoinInfo, bool) {
	candTbl := strings.ToLower(cleanIdent(j.Table))
	candAlias := strings.ToLower(cleanIdent(j.Alias))

	tblL := extractTableQualifier(j.LeftKey)
	tblR := extractTableQualifier(j.RightKey)

	// Case 1: LeftKey belongs to an available (outer) table, RightKey belongs to the candidate (inner) table
	if tblL != "" && tblR != "" {
		if availableTables[tblL] && (tblR == candTbl || tblR == candAlias) {
			copyJ := *j
			return &copyJ, true
		}
		// Case 2: RightKey belongs to an available (outer) table, LeftKey belongs to candidate (inner) table -> Swap
		if availableTables[tblR] && (tblL == candTbl || tblL == candAlias) {
			copyJ := *j
			copyJ.LeftKey = j.RightKey
			copyJ.RightKey = j.LeftKey
			return &copyJ, true
		}
		// Neither is connected directly
		return nil, false
	}

	// Fallback for unqualified keys: check column presence in rSchema
	colL := extractColumnName(j.LeftKey)
	colR := extractColumnName(j.RightKey)

	if rSchema != nil {
		_, _, foundR := rSchema.FindColumn(colR)
		_, _, foundL := rSchema.FindColumn(colL)
		if foundR && !foundL {
			copyJ := *j
			return &copyJ, true
		}
		if foundL && !foundR {
			copyJ := *j
			copyJ.LeftKey = j.RightKey
			copyJ.RightKey = j.LeftKey
			return &copyJ, true
		}
	}

	// Default fallback: preserve as is
	copyJ := *j
	return &copyJ, true
}

type joinDPEntry struct {
	node      *PlanNode
	estRows   int64
	tables    map[string]bool
	totalCost float64
}

type candInfo struct {
	orig   *JoinInfo
	table  string
	alias  string
	schema *storage.TableSchema
}

// optimizeJoinOrderDP finds the optimal join ordering for a set of contiguous INNER joins
// using Selinger dynamic programming (B-4) with subset bitmask memoization.
func optimizeJoinOrderDP(
	currentNode *PlanNode,
	currentEstimatedRows int64,
	availableTables map[string]bool,
	innerBlock []*JoinInfo,
	rightSchemas map[string]*storage.TableSchema,
	rightStats map[string]*TableStats,
	queryHints []string,
) (*PlanNode, int64, map[string]bool, bool) {
	n := len(innerBlock)
	if n == 0 {
		return currentNode, currentEstimatedRows, availableTables, true
	}
	// Bound DP state space to <= 12 tables to ensure constant-time sub-millisecond planning (Golden Rule 21, 23)
	if n > 12 {
		return nil, 0, nil, false
	}

	cands := make([]candInfo, n)
	for k, j := range innerBlock {
		t := strings.ToLower(cleanIdent(j.Table))
		a := ""
		if j.Alias != "" {
			a = strings.ToLower(cleanIdent(j.Alias))
		}
		var s *storage.TableSchema
		if rightSchemas != nil {
			if sc, ok := rightSchemas[j.Table]; ok && sc != nil {
				s = sc
			} else if sc, ok := rightSchemas[cleanIdent(j.Table)]; ok && sc != nil {
				s = sc
			} else if sc, ok := rightSchemas[j.Alias]; ok && sc != nil {
				s = sc
			} else if a != "" {
				if sc, ok := rightSchemas[a]; ok && sc != nil {
					s = sc
				}
			}
		}
		cands[k] = candInfo{orig: j, table: t, alias: a, schema: s}
	}

	dpSize := 1 << n
	dp := make([]*joinDPEntry, dpSize)

	baseTables := make(map[string]bool, len(availableTables)+n*2)
	for k, v := range availableTables {
		baseTables[k] = v
	}

	dp[0] = &joinDPEntry{
		node:      currentNode,
		estRows:   currentEstimatedRows,
		tables:    baseTables,
		totalCost: currentNode.TotalCost,
	}

	// 1. Primary Selinger Pass: connect only through matching join keys (cross products pruned)
	for size := 1; size <= n; size++ {
		for mask := uint32(1); mask < uint32(dpSize); mask++ {
			if bits.OnesCount32(mask) != size {
				continue
			}

			for k := 0; k < n; k++ {
				bit := uint32(1 << k)
				if (mask & bit) == 0 {
					continue
				}
				prevMask := mask &^ bit
				prevEntry := dp[prevMask]
				if prevEntry == nil {
					continue
				}

				cand := cands[k]
				oriented, ok := checkAndOrientJoinKeys(cand.orig, prevEntry.tables, cand.schema)
				if !ok {
					continue
				}

				joinedNode, joinedEstRows := buildSingleJoinNode(prevEntry.node, prevEntry.estRows, oriented, rightSchemas, rightStats, queryHints)
				if dp[mask] == nil || joinedNode.TotalCost < dp[mask].totalCost {
					newTables := make(map[string]bool, len(prevEntry.tables)+2)
					for t, v := range prevEntry.tables {
						newTables[t] = v
					}
					newTables[cand.table] = true
					if cand.alias != "" {
						newTables[cand.alias] = true
					}
					dp[mask] = &joinDPEntry{
						node:      joinedNode,
						estRows:   joinedEstRows,
						tables:    newTables,
						totalCost: joinedNode.TotalCost,
					}
				}
			}
		}
	}

	fullMask := uint32(dpSize - 1)
	if dp[fullMask] != nil {
		best := dp[fullMask]
		return best.node, best.estRows, best.tables, true
	}

	// 2. Secondary Fallback Pass: allow Cartesian (unconnected) joins if no fully-connected path exists
	for size := 1; size <= n; size++ {
		for mask := uint32(1); mask < uint32(dpSize); mask++ {
			if bits.OnesCount32(mask) != size || dp[mask] != nil {
				continue
			}

			for k := 0; k < n; k++ {
				bit := uint32(1 << k)
				if (mask & bit) == 0 {
					continue
				}
				prevMask := mask &^ bit
				prevEntry := dp[prevMask]
				if prevEntry == nil {
					continue
				}

				cand := cands[k]
				oriented, ok := checkAndOrientJoinKeys(cand.orig, prevEntry.tables, cand.schema)
				if !ok {
					oriented = cand.orig
				}

				joinedNode, joinedEstRows := buildSingleJoinNode(prevEntry.node, prevEntry.estRows, oriented, rightSchemas, rightStats, queryHints)
				if dp[mask] == nil || joinedNode.TotalCost < dp[mask].totalCost {
					newTables := make(map[string]bool, len(prevEntry.tables)+2)
					for t, v := range prevEntry.tables {
						newTables[t] = v
					}
					newTables[cand.table] = true
					if cand.alias != "" {
						newTables[cand.alias] = true
					}
					dp[mask] = &joinDPEntry{
						node:      joinedNode,
						estRows:   joinedEstRows,
						tables:    newTables,
						totalCost: joinedNode.TotalCost,
					}
				}
			}
		}
	}

	if dp[fullMask] != nil {
		best := dp[fullMask]
		return best.node, best.estRows, best.tables, true
	}

	return nil, 0, nil, false
}

func buildSingleJoinNode(currentNode *PlanNode, currentEstimatedRows int64, join *JoinInfo, rightSchemas map[string]*storage.TableSchema, rightStats map[string]*TableStats, queryHints []string) (*PlanNode, int64) {
	rTable := cleanIdent(join.Table)
	rAlias := cleanIdent(join.Alias)
	if rAlias == "" {
		rAlias = rTable
	}

	var rSchema *storage.TableSchema
	if rightSchemas != nil {
		if s, ok := rightSchemas[join.Table]; ok && s != nil {
			rSchema = s
		} else if s, ok := rightSchemas[rTable]; ok && s != nil {
			rSchema = s
		} else if s, ok := rightSchemas[join.Alias]; ok && s != nil {
			rSchema = s
		} else if s, ok := rightSchemas[rAlias]; ok && s != nil {
			rSchema = s
		}
	}

	var rStat *TableStats
	if rightStats != nil {
		if st, ok := rightStats[join.Table]; ok && st != nil {
			rStat = st
		} else if st, ok := rightStats[rTable]; ok && st != nil {
			rStat = st
		} else if st, ok := rightStats[join.Alias]; ok && st != nil {
			rStat = st
		} else if st, ok := rightStats[rAlias]; ok && st != nil {
			rStat = st
		}
	}

	rRows := int64(100)
	if rStat != nil && rStat.RowCount > 0 {
		rRows = rStat.RowCount
	}

	rPages := float64(rRows) / float64(RowsPerPageDefault)
	if rPages < 1 {
		rPages = 1
	}
	rScanCost := rPages*SeqPageCost + float64(rRows)*CPUTupleCost

	leftKey := join.LeftKey
	rightKey := join.RightKey
	rightCol := rightKey
	if idx := strings.Index(rightCol, "."); idx != -1 {
		rightCol = rightCol[idx+1:]
	}

	var innerIndex *storage.IndexSchema
	if rSchema != nil {
		if idx, found := rSchema.FindIndexByColumn(rightCol); found {
			innerIndex = idx
		} else {
			leftCol := leftKey
			if idx := strings.Index(leftCol, "."); idx != -1 {
				leftCol = leftCol[idx+1:]
			}
			if idx, found := rSchema.FindIndexByColumn(leftCol); found {
				innerIndex = idx
				leftKey, rightKey = rightKey, leftKey
			}
		}
	}

	joinEstRows := currentEstimatedRows
	if join.Type == "INNER" || join.Type == "SEMI" {
		joinEstRows = int64(math.Min(float64(currentEstimatedRows), float64(rRows)))
		if joinEstRows < 1 {
			joinEstRows = 1
		}
	} else if join.Type == "ANTI" {
		joinEstRows = int64(math.Max(1, float64(currentEstimatedRows)*0.5))
	}

	// Calculate Hash Join Cost
	hashBuildCost := rScanCost + float64(rRows)*CPUTupleCost
	hashProbeCost := currentNode.TotalCost + float64(currentEstimatedRows)*CPUOperatorCost
	hashJoinTotalCost := hashBuildCost + hashProbeCost

	// Calculate Index Nested Loop Join Cost
	nlTotalCost := math.MaxFloat64
	if innerIndex != nil {
		innerProbeCost := float64(currentEstimatedRows) * (CPUIndexTupleCost + CPUTupleCost + 0.045)
		nlTotalCost = currentNode.TotalCost + 0.25 + innerProbeCost
	}

	var nextNode *PlanNode
	// CBO Decision: Compare Hash Join vs Index Nested Loop Join vs Sort-Merge Join
	isOuterUnmatched := strings.HasPrefix(strings.ToUpper(join.Type), "FULL") || strings.HasPrefix(strings.ToUpper(join.Type), "RIGHT")

	forceHashJoin := hasQueryHint(queryHints, "HASH JOIN") || strings.Contains(strings.ToUpper(join.Type), "HASH")
	forceLoopJoin := hasQueryHint(queryHints, "LOOP JOIN") || strings.Contains(strings.ToUpper(join.Type), "LOOP")
	forceMergeJoin := hasQueryHint(queryHints, "MERGE JOIN") || strings.Contains(strings.ToUpper(join.Type), "MERGE")

	// Check table hint on the joined table
	joinForceIndex, joinForceTableScan := parseIndexHint(join.Hints)
	if joinForceTableScan {
		innerIndex = nil
	} else if joinForceIndex != "" && rSchema != nil {
		for i := range rSchema.Indexes {
			if strings.EqualFold(rSchema.Indexes[i].Name, joinForceIndex) {
				innerIndex = &rSchema.Indexes[i]
				break
			}
		}
	}

	// Calculate Sort-Merge Join Cost
	cleanLeftKey := leftKey
	if idx := strings.Index(cleanLeftKey, "."); idx != -1 {
		cleanLeftKey = cleanLeftKey[idx+1:]
	}
	cleanLeftKey = strings.ToLower(cleanIdent(cleanLeftKey))

	cleanRightKey := rightKey
	if idx := strings.Index(cleanRightKey, "."); idx != -1 {
		cleanRightKey = cleanRightKey[idx+1:]
	}
	cleanRightKey = strings.ToLower(cleanIdent(cleanRightKey))

	outerSortCost := 0.0
	isOuterSorted := false
	if (currentNode.Type == NodeIndexScan || currentNode.Type == NodeIndexOnlyScan) && strings.Contains(strings.ToLower(currentNode.IndexCond), cleanLeftKey) {
		isOuterSorted = true
	} else if currentNode.Type == NodeSort && strings.EqualFold(cleanIdent(currentNode.SortKey), cleanLeftKey) {
		isOuterSorted = true
	}
	if !isOuterSorted {
		outerSortCost = float64(currentEstimatedRows)*(CPUTupleCost+math.Log2(math.Max(2, float64(currentEstimatedRows)))*2.0*CPUOperatorCost) + 0.1
	}

	innerSortCost := 0.0
	isInnerSorted := false
	if innerIndex != nil && len(innerIndex.Columns) > 0 && strings.EqualFold(innerIndex.Columns[0], cleanRightKey) {
		isInnerSorted = true
	}
	if !isInnerSorted {
		innerSortCost = float64(rRows)*(CPUTupleCost+math.Log2(math.Max(2, float64(rRows)))*2.0*CPUOperatorCost) + 0.1
	}

	mergeStreamingCost := (float64(currentEstimatedRows) + float64(rRows)) * CPUOperatorCost
	mergeJoinTotalCost := currentNode.TotalCost + rScanCost + outerSortCost + innerSortCost + mergeStreamingCost

	// Strategy selection
	joinStrategy := "HASH"
	if forceMergeJoin {
		joinStrategy = "MERGE"
	} else if forceLoopJoin && !isOuterUnmatched && innerIndex != nil {
		joinStrategy = "LOOP"
	} else if forceHashJoin {
		joinStrategy = "HASH"
	} else {
		// Cost-based selection between NL, Hash, and Merge
		if !isOuterUnmatched && innerIndex != nil && nlTotalCost < hashJoinTotalCost && nlTotalCost < mergeJoinTotalCost {
			joinStrategy = "LOOP"
		} else if mergeJoinTotalCost < hashJoinTotalCost {
			joinStrategy = "MERGE"
		} else {
			joinStrategy = "HASH"
		}
	}

	cleanType := strings.ToUpper(strings.TrimSpace(join.Type))
	cleanType = strings.ReplaceAll(cleanType, "MERGE", "")
	cleanType = strings.ReplaceAll(cleanType, "HASH", "")
	cleanType = strings.ReplaceAll(cleanType, "LOOP", "")
	cleanType = strings.TrimSpace(cleanType)
	if cleanType == "" {
		cleanType = "INNER"
	}

	if joinStrategy == "LOOP" {
		innerScanNode := &PlanNode{
			Type:          NodeIndexScan,
			Relation:      join.Table,
			Alias:         rAlias,
			IndexName:     innerIndex.Name,
			IndexCond:     fmt.Sprintf("(%s = %s)", innerIndex.Columns[0], leftKey),
			FilterCond:    join.Filter,
			StartupCost:   0.25,
			TotalCost:     0.35,
			EstimatedRows: 1,
		}

		nextNode = &PlanNode{
			Type:          NodeNestedLoop,
			JoinType:      cleanType,
			JoinCond:      fmt.Sprintf("(%s = %s)", leftKey, rightKey),
			StartupCost:   currentNode.StartupCost + 0.25,
			TotalCost:     nlTotalCost,
			EstimatedRows: joinEstRows,
			Children:      []*PlanNode{currentNode, innerScanNode},
		}
	} else if joinStrategy == "MERGE" {
		var rScanNode *PlanNode
		baseScan := &PlanNode{
			Type:          NodeTableScan,
			Relation:      join.Table,
			Alias:         rAlias,
			StartupCost:   0.0,
			TotalCost:     rScanCost,
			EstimatedRows: rRows,
		}
		if join.Filter != "" {
			rScanNode = &PlanNode{
				Type:          NodeFilter,
				FilterCond:    join.Filter,
				StartupCost:   0.0,
				TotalCost:     rScanCost + float64(rRows)*CPUTupleCost,
				EstimatedRows: int64(math.Max(1, float64(rRows)*0.5)),
				Children:      []*PlanNode{baseScan},
			}
		} else {
			rScanNode = baseScan
		}

		nextNode = &PlanNode{
			Type:          NodeMergeJoin,
			JoinType:      cleanType,
			MergeCond:     fmt.Sprintf("(%s = %s)", leftKey, rightKey),
			JoinCond:      fmt.Sprintf("(%s = %s)", leftKey, rightKey),
			StartupCost:   currentNode.StartupCost + outerSortCost + innerSortCost,
			TotalCost:     mergeJoinTotalCost,
			EstimatedRows: joinEstRows,
			Children:      []*PlanNode{currentNode, rScanNode},
		}
	} else {
		// Hash Join Strategy
		var rScanNode *PlanNode
		baseScan := &PlanNode{
			Type:          NodeTableScan,
			Relation:      join.Table,
			Alias:         rAlias,
			StartupCost:   0.0,
			TotalCost:     rScanCost,
			EstimatedRows: rRows,
		}
		if join.Filter != "" {
			rScanNode = &PlanNode{
				Type:          NodeFilter,
				FilterCond:    join.Filter,
				StartupCost:   0.0,
				TotalCost:     rScanCost + float64(rRows)*CPUTupleCost,
				EstimatedRows: int64(math.Max(1, float64(rRows)*0.5)),
				Children:      []*PlanNode{baseScan},
			}
		} else {
			rScanNode = baseScan
		}

		hashNode := &PlanNode{
			Type:          NodeHash,
			StartupCost:   rScanCost,
			TotalCost:     hashBuildCost,
			EstimatedRows: rRows,
			Children:      []*PlanNode{rScanNode},
		}

		nextNode = &PlanNode{
			Type:          NodeHashJoin,
			JoinType:      cleanType,
			HashCond:      fmt.Sprintf("(%s = %s)", leftKey, rightKey),
			StartupCost:   hashBuildCost,
			TotalCost:     hashJoinTotalCost,
			EstimatedRows: joinEstRows,
			Children:      []*PlanNode{currentNode, hashNode},
		}
	}

	return nextNode, joinEstRows
}

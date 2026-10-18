package planner

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"sqlion/pkg/storage"
)

// ExplainFormat defines the output format for EXPLAIN queries.
type ExplainFormat string

const (
	FormatText ExplainFormat = "TEXT"
	FormatJSON ExplainFormat = "JSON"
	FormatYAML ExplainFormat = "YAML"
	FormatXML  ExplainFormat = "XML"
)

// ExplainOptions holds configuration flags extracted from an EXPLAIN query.
type ExplainOptions struct {
	Analyze bool
	Verbose bool
	Costs   bool
	Buffers bool
	Format  ExplainFormat
}

// ParseExplainOptions extracts standard PostgreSQL options and the inner SQL statement.
// Supports both modern syntax: EXPLAIN (ANALYZE, FORMAT JSON, VERBOSE) SELECT ...
// and legacy syntax: EXPLAIN ANALYZE SELECT ...
func ParseExplainOptions(query string) (opts ExplainOptions, innerSQL string, err error) {
	trimmed := strings.TrimSpace(query)
	upper := strings.ToUpper(trimmed)

	opts.Costs = true
	opts.Format = FormatText

	if !strings.HasPrefix(upper, "EXPLAIN") {
		return opts, trimmed, fmt.Errorf("not an EXPLAIN statement")
	}

	rest := strings.TrimSpace(trimmed[7:])

	// 1. Check for parenthesized options: EXPLAIN (opt1, opt2, ...) ...
	if strings.HasPrefix(rest, "(") {
		closeIdx := strings.Index(rest, ")")
		if closeIdx == -1 {
			return opts, "", fmt.Errorf("unmatched parenthesis in EXPLAIN options")
		}

		optBlock := rest[1:closeIdx]
		innerSQL = strings.TrimSpace(rest[closeIdx+1:])

		optParts := strings.Split(optBlock, ",")
		for _, part := range optParts {
			partTrimmed := strings.TrimSpace(part)
			partUpper := strings.ToUpper(partTrimmed)

			if partUpper == "ANALYZE" || strings.HasPrefix(partUpper, "ANALYZE ") {
				opts.Analyze = !strings.HasSuffix(partUpper, "FALSE") && !strings.HasSuffix(partUpper, "0")
			} else if partUpper == "VERBOSE" || strings.HasPrefix(partUpper, "VERBOSE ") {
				opts.Verbose = !strings.HasSuffix(partUpper, "FALSE") && !strings.HasSuffix(partUpper, "0")
			} else if partUpper == "COSTS" || strings.HasPrefix(partUpper, "COSTS ") {
				opts.Costs = !strings.HasSuffix(partUpper, "FALSE") && !strings.HasSuffix(partUpper, "0")
			} else if partUpper == "BUFFERS" || strings.HasPrefix(partUpper, "BUFFERS ") {
				opts.Buffers = !strings.HasSuffix(partUpper, "FALSE") && !strings.HasSuffix(partUpper, "0")
			} else if strings.HasPrefix(partUpper, "FORMAT ") {
				fmtVal := strings.TrimSpace(partUpper[7:])
				switch fmtVal {
				case "JSON":
					opts.Format = FormatJSON
				case "YAML":
					opts.Format = FormatYAML
				case "XML":
					opts.Format = FormatXML
				default:
					opts.Format = FormatText
				}
			}
		}
		return opts, innerSQL, nil
	}

	// 2. Legacy prefix syntax: EXPLAIN ANALYZE SELECT ... or EXPLAIN SELECT ...
	upperRest := strings.ToUpper(rest)
	if strings.HasPrefix(upperRest, "ANALYZE ") {
		opts.Analyze = true
		innerSQL = strings.TrimSpace(rest[8:])
	} else {
		innerSQL = rest
	}

	return opts, innerSQL, nil
}

// PostgresPlanRepresentation mirrors the official PostgreSQL EXPLAIN (FORMAT JSON) structure
// expected by DBeaver, DataGrip, and visual explain tree tools.
type PostgresPlanRepresentation struct {
	Plan PostgresPlanNode `json:"Plan"`
}

// PostgresPlanNode is a single node in PostgreSQL visual plan JSON.
type PostgresPlanNode struct {
	NodeType          string              `json:"Node Type"`
	RelationName      string              `json:"Relation Name,omitempty"`
	Alias             string              `json:"Alias,omitempty"`
	IndexName         string              `json:"Index Name,omitempty"`
	IndexCond         string              `json:"Index Cond,omitempty"`
	Filter            string              `json:"Filter,omitempty"`
	SortKey           []string            `json:"Sort Key,omitempty"`
	StartupCost       float64             `json:"Startup Cost"`
	TotalCost         float64             `json:"Total Cost"`
	PlanRows          int64               `json:"Plan Rows"`
	ActualStartupTime *float64            `json:"Actual Startup Time,omitempty"`
	ActualTotalTime   *float64            `json:"Actual Total Time,omitempty"`
	ActualRows        *int64              `json:"Actual Rows,omitempty"`
	ActualLoops       *int64              `json:"Actual Loops,omitempty"`
	Partitions        []string            `json:"Partitions,omitempty"`
	Plans             []PostgresPlanNode  `json:"Plans,omitempty"`
}

// FormatPostgresPlanJSON serializes the PlanNode tree to canonical PostgreSQL EXPLAIN (FORMAT JSON).
func FormatPostgresPlanJSON(node *PlanNode, analyze bool) (string, error) {
	if node == nil {
		return "[]", nil
	}

	pgNode := toPostgresPlanNode(node, analyze)
	wrapper := []PostgresPlanRepresentation{
		{Plan: pgNode},
	}

	bytes, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func toPostgresPlanNode(node *PlanNode, analyze bool) PostgresPlanNode {
	pg := PostgresPlanNode{
		NodeType:     string(node.Type),
		RelationName: node.Relation,
		Alias:        node.Alias,
		IndexName:    node.IndexName,
		IndexCond:    node.IndexCond,
		Filter:       node.FilterCond,
		StartupCost:  node.StartupCost,
		TotalCost:    node.TotalCost,
		PlanRows:     node.EstimatedRows,
		Partitions:   node.Partitions,
	}

	if node.SortKey != "" {
		pg.SortKey = []string{fmt.Sprintf("%s %s", node.SortKey, node.SortOrder)}
	}

	if analyze {
		startupMs := float64(node.ActualStartupUS) / 1000.0
		durationMs := float64(node.ActualDurationUS) / 1000.0
		rows := node.ActualRows
		loops := node.ActualLoops
		if loops == 0 {
			loops = 1
		}
		pg.ActualStartupTime = &startupMs
		pg.ActualTotalTime = &durationMs
		pg.ActualRows = &rows
		pg.ActualLoops = &loops
	}

	for _, child := range node.Children {
		pg.Plans = append(pg.Plans, toPostgresPlanNode(child, analyze))
	}

	return pg
}

// BuildDMLPlanNode constructs an execution plan node for INSERT, UPDATE, or DELETE operations.
func (p *Planner) BuildDMLPlanNode(dmlType string, tableName string, filterClause string, schema *storage.TableSchema, stats *TableStats, estRows int64) *PlanNode {
	cleanTable := strings.ToLower(strings.TrimSpace(tableName))
	if schema != nil {
		cleanTable = schema.Name
	}

	var totalRows int64 = 100
	if stats != nil && stats.RowCount > 0 {
		totalRows = stats.RowCount
	}
	if estRows > 0 {
		totalRows = estRows
	}

	switch strings.ToUpper(dmlType) {
	case "INSERT":
		return &PlanNode{
			Type:          NodeModifyTable,
			Relation:      cleanTable,
			StartupCost:   0.00,
			TotalCost:     float64(totalRows) * 1.5,
			EstimatedRows: totalRows,
		}

	case "UPDATE", "DELETE":
		nodeType := NodeModifyTable
		var scanChild *PlanNode

		// Check if filter can use primary key index
		useIndex := false
		var pkCol string
		if schema != nil {
			if pk, err := schema.PrimaryKeyColumn(); err == nil && pk != nil {
				pkCol = pk.Name
				if filterClause != "" {
					eqPattern := fmt.Sprintf(`(?i)\b%s\s*=\s*([^\s;]+)`, regexp.QuoteMeta(pkCol))
					if regexp.MustCompile(eqPattern).MatchString(filterClause) {
						useIndex = true
					}
				}
			}
		}

		if useIndex {
			scanChild = &PlanNode{
				Type:          NodeIndexScan,
				Relation:      cleanTable,
				IndexName:     fmt.Sprintf("%s_pkey", cleanTable),
				IndexCond:     filterClause,
				StartupCost:   0.28,
				TotalCost:     8.30,
				EstimatedRows: 1,
			}
		} else {
			scanChild = &PlanNode{
				Type:          NodeTableScan,
				Relation:      cleanTable,
				FilterCond:    filterClause,
				StartupCost:   0.00,
				TotalCost:     float64(totalRows) * 1.1,
				EstimatedRows: totalRows,
			}
		}

		return &PlanNode{
			Type:          nodeType,
			Relation:      cleanTable,
			StartupCost:   scanChild.StartupCost,
			TotalCost:     scanChild.TotalCost + 1.25,
			EstimatedRows: scanChild.EstimatedRows,
			Children:      []*PlanNode{scanChild},
		}

	default:
		return &PlanNode{
			Type:          NodeModifyTable,
			Relation:      cleanTable,
			StartupCost:   0.00,
			TotalCost:     10.00,
			EstimatedRows: 1,
		}
	}
}

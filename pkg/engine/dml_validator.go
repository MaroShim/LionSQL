package engine

import (
	"fmt"
	"strings"

	"lionsql/pkg/planner"
	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

// ValidateDMLTableTarget verifies that tableName is valid and not a read-only virtual system catalog view.
func (s *Coordinator) ValidateDMLTableTarget(tableName string) error {
	tableName = cleanIdentifier(tableName)
	if tableName == "" {
		return fmt.Errorf("table name cannot be empty")
	}
	if s.isVirtualCatalogTable(tableName) {
		return fmt.Errorf("cannot modify system catalog view %q (read-only)", tableName)
	}
	return nil
}

// ValidateDMLStatementAST validates a DML query against the canonical planner.ValidateDMLStmt contracts.
func (s *Coordinator) ValidateDMLStatementAST(query string, cs *ClientSession) error {
	// Multi-table JOIN DML (UPDATE ... FROM ... or DELETE ... JOIN ...)
	// has aliases in target and is validated by dedicated join executors.
	if sqlutil.FindTopLevelKeyword(query, "FROM") != -1 {
		upper := strings.ToUpper(strings.TrimSpace(query))
		if strings.HasPrefix(upper, "UPDATE") {
			return nil
		}
		if strings.HasPrefix(upper, "DELETE") && sqlutil.FindTopLevelKeyword(query, "JOIN") != -1 {
			return nil
		}
	}

	parsed, err := proc.ParseSQL(query)
	if err != nil {
		// Non-standard vendor syntax or expressions fallback gracefully to string-based checks
		return nil
	}
	schemas := make(map[string]*storage.TableSchema)
	if s.engine != nil {
		if tables, err := s.engine.ListTables(); err == nil {
			for _, t := range tables {
				if t != nil {
					schemas[strings.ToLower(t.Name)] = t
					schemas[t.Name] = t
				}
			}
		}
	}
	// Overlay any session-specific / shadow / ephemeral table schemas
	if cs != nil {
		for name := range schemas {
			if tbl, err := s.getTable(cs, name); err == nil && tbl != nil {
				schemas[name] = tbl
				schemas[strings.ToLower(name)] = tbl
			}
		}
	}
	return planner.ValidateDMLStmt(parsed, schemas)
}

// ValidateOutputClause verifies that column references in RETURNING / OUTPUT exist on schema.
func ValidateOutputClause(rawOutput string, schema *storage.TableSchema) error {
	if rawOutput == "" || schema == nil {
		return nil
	}
	info := parseOutputClauseInfo(rawOutput)
	for _, p := range info.Projs {
		if p.column == "*" {
			continue
		}
		if _, _, ok := schema.FindColumn(p.column); !ok {
			return fmt.Errorf("column %q does not exist", p.column)
		}
	}
	return nil
}

// ValidateInsertValueExpr validates that a value expression in an INSERT VALUES tuple is a valid scalar expression
// and does not reference non-existent columns (ghost columns).
func ValidateInsertValueExpr(valStr string) error {
	valStr = strings.TrimSpace(valStr)
	if valStr == "" || strings.EqualFold(valStr, "NULL") || strings.EqualFold(valStr, "DEFAULT") {
		return nil
	}
	if strings.HasPrefix(valStr, "@") {
		return nil
	}
	if (strings.HasPrefix(valStr, "'") && strings.HasSuffix(valStr, "'")) ||
		(strings.HasPrefix(valStr, "N'") && strings.HasSuffix(valStr, "'")) {
		return nil
	}
	p := proc.NewParser(proc.NewLexer(valStr))
	expr := p.ParseExpr()
	if expr == nil {
		return nil
	}
	return planner.WalkAST(expr, func(node proc.Expr) error {
		col, ok := node.(*proc.ColumnRefExpr)
		if !ok {
			return nil
		}
		name := col.Name
		if strings.HasPrefix(name, "@") || proc.IsBuiltinFunction(name) ||
			strings.EqualFold(name, "NULL") || strings.EqualFold(name, "DEFAULT") ||
			strings.EqualFold(name, "TRUE") || strings.EqualFold(name, "FALSE") {
			return nil
		}
		return fmt.Errorf("column %q does not exist", name)
	})
}

// ValidateInsertArity validates that the number of values in each tuple of an INSERT statement
// matches the target column count.
func ValidateInsertArity(schema *storage.TableSchema, rawCols string, rawValues string, identityInsertAllowed bool) error {
	if schema == nil || rawValues == "" {
		return nil
	}
	trimmedVals := strings.TrimSpace(rawValues)
	if strings.EqualFold(trimmedVals, "DEFAULT VALUES") || strings.HasPrefix(strings.ToUpper(trimmedVals), "EXEC") {
		return nil
	}
	if conflictIdx := sqlutil.FindTopLevelKeyword(trimmedVals, "ON CONFLICT"); conflictIdx != -1 {
		trimmedVals = strings.TrimSpace(trimmedVals[:conflictIdx])
	}
	if dupIdx := sqlutil.FindTopLevelKeyword(trimmedVals, "ON DUPLICATE"); dupIdx != -1 {
		trimmedVals = strings.TrimSpace(trimmedVals[:dupIdx])
	}
	tupleStrings := sqlutil.ExtractTopLevelTuples(trimmedVals)
	if len(tupleStrings) == 0 {
		return fmt.Errorf("no values supplied in INSERT statement")
	}

	var identityCol *storage.Column
	for i := range schema.Columns {
		if schema.Columns[i].Identity {
			identityCol = &schema.Columns[i]
			break
		}
	}

	var targetCols []string
	if rawCols != "" {
		for _, c := range sqlutil.SplitTopLevel(rawCols, ',') {
			targetCols = append(targetCols, cleanIdentifier(c))
		}
	} else {
		if identityCol != nil && !identityInsertAllowed {
			for _, c := range schema.Columns {
				if !c.Identity {
					targetCols = append(targetCols, c.Name)
				}
			}
		} else {
			for _, c := range schema.Columns {
				targetCols = append(targetCols, c.Name)
			}
		}
	}

	for _, tupleStr := range tupleStrings {
		vals := splitValues(tupleStr)
		if len(vals) != len(targetCols) {
			if len(targetCols) > 0 && len(vals) > len(targetCols) && rawCols == "" {
				return fmt.Errorf("INSERT has more expressions than target columns")
			}
			return fmt.Errorf("column count mismatch: expected %d values, got %d in tuple (%s)", len(targetCols), len(vals), tupleStr)
		}
	}
	return nil
}

package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"lionsql/pkg/planner"
	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

// detectLinearWritableCTE determines whether a set of CTEs and the main query form
// a linear mutation pipeline (e.g. multi-step INSERT/UPDATE/DELETE with RETURNING/OUTPUT
// followed by a simple projection SELECT) that can be lowered into a single VDBE bytecode program.
func detectLinearWritableCTE(ctes []planner.CTEInfo, mainSQL string) bool {
	if len(ctes) == 0 {
		return false
	}

	// 1. Each CTE must be a single linear DML statement without analytical clauses.
	cteNames := make(map[string]bool, len(ctes))
	for _, cte := range ctes {
		nameLower := strings.ToLower(strings.Trim(cte.Name, "`\"[]"))
		cteNames[nameLower] = true

		inner := strings.TrimSpace(cte.InnerSQL)
		upper := strings.ToUpper(inner)

		// Must be INSERT, UPDATE, or DELETE
		isInsert := strings.HasPrefix(upper, "INSERT")
		isUpdate := strings.HasPrefix(upper, "UPDATE")
		isDelete := strings.HasPrefix(upper, "DELETE")
		if !isInsert && !isUpdate && !isDelete {
			return false
		}

		// Must NOT contain analytical or set operations
		if sqlutil.ContainsKeyword(inner, "UNION", false) ||
			sqlutil.ContainsKeyword(inner, "INTERSECT", false) ||
			sqlutil.ContainsKeyword(inner, "EXCEPT", false) ||
			sqlutil.ContainsKeyword(inner, "GROUP BY", false) ||
			sqlutil.ContainsKeyword(inner, "HAVING", false) ||
			sqlutil.ContainsKeyword(inner, "OVER", false) ||
			sqlutil.ContainsKeyword(inner, "PARTITION BY", false) ||
			sqlutil.ContainsKeyword(inner, "RECURSIVE", false) ||
			sqlutil.ContainsKeyword(inner, "ANY", false) ||
			sqlutil.ContainsKeyword(inner, "ALL", false) ||
			sqlutil.ContainsKeyword(inner, "SOME", false) {
			return false
		}
	}

	// 2. Main SQL must be a simple SELECT without complex analytical/join structures
	mainTrimmed := strings.TrimSpace(mainSQL)
	mainUpper := strings.ToUpper(mainTrimmed)
	if !strings.HasPrefix(mainUpper, "SELECT") {
		return false
	}
	if sqlutil.ContainsKeyword(mainTrimmed, "UNION", false) ||
		sqlutil.ContainsKeyword(mainTrimmed, "INTERSECT", false) ||
		sqlutil.ContainsKeyword(mainTrimmed, "EXCEPT", false) ||
		sqlutil.ContainsKeyword(mainTrimmed, "GROUP BY", false) ||
		sqlutil.ContainsKeyword(mainTrimmed, "HAVING", false) ||
		sqlutil.ContainsKeyword(mainTrimmed, "OVER", false) ||
		sqlutil.ContainsKeyword(mainTrimmed, "PARTITION BY", false) {
		return false
	}

	return true
}

// compileLinearWritableCTE transforms linear mutation CTEs and projection SELECT
// into an anonymous VDBE bytecode program.
func compileLinearWritableCTE(ctes []planner.CTEInfo, mainSQL string) (*proc.Program, []string, error) {
	colToVar := make(map[string]string)
	cteNames := make(map[string]bool, len(ctes))
	for _, c := range ctes {
		cteNames[strings.ToLower(strings.Trim(c.Name, "`\"[]"))] = true
	}

	var loweredStmts []proc.Statement
	hasConditionalDML := false

	for _, cte := range ctes {
		cteName := strings.ToLower(strings.Trim(cte.Name, "`\"[]"))
		p := proc.NewParser(proc.NewLexer(cte.InnerSQL))
		stmt, err := p.ParseSQLStatement()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse CTE %q: %w", cte.Name, err)
		}

		var outputCols []string
		var intoVars []string

		switch s := stmt.(type) {
		case *proc.InsertStmt:
			// Rewrite values if they reference previous CTE variables
			for i, v := range s.Values {
				s.Values[i] = rewriteExprWithVars(v, colToVar)
			}
			outputCols = s.OutputCols
			for _, col := range outputCols {
				cleanCol := strings.ToLower(proc.CleanSchemaPrefix(col))
				vName := fmt.Sprintf("__cte_%s_%s", cteName, cleanCol)
				intoVars = append(intoVars, vName)
				colToVar[cteName+"."+cleanCol] = vName
				if len(ctes) == 1 {
					colToVar[cleanCol] = vName
				}
			}
			s.IntoVars = intoVars

			if hasConditionalDML {
				loweredStmts = append(loweredStmts, wrapInRowCountCheck(s))
			} else {
				loweredStmts = append(loweredStmts, s)
			}

		case *proc.GeneralInsertStmt:
			outputCols = s.OutputCols
			for _, col := range outputCols {
				cleanCol := strings.ToLower(proc.CleanSchemaPrefix(col))
				vName := fmt.Sprintf("__cte_%s_%s", cteName, cleanCol)
				intoVars = append(intoVars, vName)
				colToVar[cteName+"."+cleanCol] = vName
				if len(ctes) == 1 {
					colToVar[cleanCol] = vName
				}
			}

			// If this is INSERT ... SELECT ... FROM <previous_cte>, lower to simple InsertStmt with rewritten values
			if s.Select != nil {
				fromTbl := strings.ToLower(strings.Trim(s.Select.From.Name, "`\"[]"))
				if cteNames[fromTbl] {
					var valExprs []proc.Expr
					for _, proj := range s.Select.Projections {
						valExprs = append(valExprs, rewriteExprWithVars(proj.Expr, colToVar))
					}
					insStmt := &proc.InsertStmt{
						Table:      s.Table,
						Columns:    s.Columns,
						Values:     valExprs,
						OutputCols: outputCols,
						IntoVars:   intoVars,
					}
					if hasConditionalDML {
						loweredStmts = append(loweredStmts, wrapInRowCountCheck(insStmt))
					} else {
						loweredStmts = append(loweredStmts, insStmt)
					}
					continue
				}
			}

			s.IntoVars = intoVars
			if hasConditionalDML {
				loweredStmts = append(loweredStmts, wrapInRowCountCheck(s))
			} else {
				loweredStmts = append(loweredStmts, s)
			}

		case *proc.UpdateStmt:
			hasConditionalDML = true
			for i, a := range s.Assignments {
				s.Assignments[i].Value = rewriteExprWithVars(a.Value, colToVar)
			}
			if s.WhereVal != nil {
				s.WhereVal = rewriteExprWithVars(s.WhereVal, colToVar)
			}
			outputCols = s.OutputCols
			for _, col := range outputCols {
				cleanCol := strings.ToLower(proc.CleanSchemaPrefix(col))
				vName := fmt.Sprintf("__cte_%s_%s", cteName, cleanCol)
				intoVars = append(intoVars, vName)
				colToVar[cteName+"."+cleanCol] = vName
				if len(ctes) == 1 {
					colToVar[cleanCol] = vName
				}
			}
			s.IntoVars = intoVars
			loweredStmts = append(loweredStmts, s)

		case *proc.UpdateWhereStmt:
			hasConditionalDML = true
			for i, a := range s.Assignments {
				s.Assignments[i].Value = rewriteExprWithVars(a.Value, colToVar)
			}
			if s.Where != nil {
				s.Where = rewriteExprWithVars(s.Where, colToVar)
			}
			outputCols = s.OutputCols
			for _, col := range outputCols {
				cleanCol := strings.ToLower(proc.CleanSchemaPrefix(col))
				vName := fmt.Sprintf("__cte_%s_%s", cteName, cleanCol)
				intoVars = append(intoVars, vName)
				colToVar[cteName+"."+cleanCol] = vName
				if len(ctes) == 1 {
					colToVar[cleanCol] = vName
				}
			}
			s.IntoVars = intoVars
			loweredStmts = append(loweredStmts, s)

		case *proc.DeleteStmt:
			hasConditionalDML = true
			if s.WhereVal != nil {
				s.WhereVal = rewriteExprWithVars(s.WhereVal, colToVar)
			}
			outputCols = s.OutputCols
			for _, col := range outputCols {
				cleanCol := strings.ToLower(proc.CleanSchemaPrefix(col))
				vName := fmt.Sprintf("__cte_%s_%s", cteName, cleanCol)
				intoVars = append(intoVars, vName)
				colToVar[cteName+"."+cleanCol] = vName
				if len(ctes) == 1 {
					colToVar[cleanCol] = vName
				}
			}
			s.IntoVars = intoVars
			loweredStmts = append(loweredStmts, s)

		case *proc.DeleteWhereStmt:
			hasConditionalDML = true
			if s.Where != nil {
				s.Where = rewriteExprWithVars(s.Where, colToVar)
			}
			outputCols = s.OutputCols
			for _, col := range outputCols {
				cleanCol := strings.ToLower(proc.CleanSchemaPrefix(col))
				vName := fmt.Sprintf("__cte_%s_%s", cteName, cleanCol)
				intoVars = append(intoVars, vName)
				colToVar[cteName+"."+cleanCol] = vName
				if len(ctes) == 1 {
					colToVar[cleanCol] = vName
				}
			}
			s.IntoVars = intoVars
			loweredStmts = append(loweredStmts, s)

		default:
			return nil, nil, fmt.Errorf("unsupported CTE statement type: %T", stmt)
		}
	}

	// 2. Parse and lower main query
	mainP := proc.NewParser(proc.NewLexer(mainSQL))
	mainStmt, err := mainP.ParseSQLStatement()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse main CTE query: %w", err)
	}

	sel, ok := mainStmt.(*proc.GeneralSelectStmt)
	if !ok {
		return nil, nil, fmt.Errorf("expected SELECT statement for main CTE query, got %T", mainStmt)
	}

	// Collect table aliases from main FROM clause
	tableAliases := make(map[string]string)
	if sel.From.Name != "" {
		fromTbl := strings.ToLower(strings.Trim(sel.From.Name, "`\"[]"))
		fromAlias := strings.ToLower(strings.Trim(sel.From.Alias, "`\"[]"))
		if fromAlias != "" {
			tableAliases[fromAlias] = fromTbl
		}
		tableAliases[fromTbl] = fromTbl
	}
	for _, j := range sel.Joins {
		jTbl := strings.ToLower(strings.Trim(j.Table.Name, "`\"[]"))
		jAlias := strings.ToLower(strings.Trim(j.Table.Alias, "`\"[]"))
		if jAlias != "" {
			tableAliases[jAlias] = jTbl
		}
		tableAliases[jTbl] = jTbl
	}

	var expectedColNames []string
	var newProjections []proc.Projection

	for _, p := range sel.Projections {
		origName := p.Alias
		if origName == "" {
			if colRef, isCol := p.Expr.(*proc.ColumnRefExpr); isCol {
				origName = proc.CleanSchemaPrefix(colRef.Name)
			} else {
				origName = p.Expr.String()
			}
		}
		origName = proc.CleanSchemaPrefix(origName)
		expectedColNames = append(expectedColNames, origName)

		rewrittenExpr := rewriteProjectionExpr(p.Expr, colToVar, tableAliases)
		newProjections = append(newProjections, proc.Projection{
			Expr:  rewrittenExpr,
			Alias: origName,
		})
	}

	// Eliminate FROM and Joins to make this a direct scalar SELECT emitting OpResultRow
	sel.Projections = newProjections
	sel.From = proc.TableRef{}
	sel.Joins = nil
	sel.Where = nil

	if hasConditionalDML {
		loweredStmts = append(loweredStmts, wrapInRowCountCheck(sel))
	} else {
		loweredStmts = append(loweredStmts, sel)
	}

	// 3. Compile all lowered statements into a single batch VDBE Program
	prog, err := proc.CompileStatements("LINEAR_WRITABLE_CTE", loweredStmts)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to compile lowered linear CTE program: %w", err)
	}

	return prog, expectedColNames, nil
}

func wrapInRowCountCheck(stmt proc.Statement) proc.Statement {
	return &proc.IfStmt{
		Condition: &proc.BinaryExpr{
			Left:  &proc.VariableExpr{Name: "@@ROWCOUNT"},
			Op:    proc.TokenGt,
			Right: &proc.LiteralExpr{Val: storage.IntValue(0)},
		},
		Then: stmt,
	}
}

func rewriteProjectionExpr(expr proc.Expr, colToVar map[string]string, tableAliases map[string]string) proc.Expr {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *proc.ColumnRefExpr:
		name := strings.Trim(e.Name, "`\"[]")
		parts := strings.Split(name, ".")
		if len(parts) == 2 {
			tblOrAlias := strings.ToLower(parts[0])
			col := strings.ToLower(parts[1])
			if canonicalTbl, ok := tableAliases[tblOrAlias]; ok {
				tblOrAlias = canonicalTbl
			}
			if vName, ok := colToVar[tblOrAlias+"."+col]; ok {
				return &proc.VariableExpr{Name: "@" + vName}
			}
		} else if len(parts) == 1 {
			col := strings.ToLower(parts[0])
			if vName, ok := colToVar[col]; ok {
				return &proc.VariableExpr{Name: "@" + vName}
			}
			// Search any CTE column matching this name
			for k, vName := range colToVar {
				if strings.HasSuffix(k, "."+col) {
					return &proc.VariableExpr{Name: "@" + vName}
				}
			}
		}
		return e

	case *proc.BinaryExpr:
		return &proc.BinaryExpr{
			Left:  rewriteProjectionExpr(e.Left, colToVar, tableAliases),
			Op:    e.Op,
			Right: rewriteProjectionExpr(e.Right, colToVar, tableAliases),
		}

	case *proc.UnaryExpr:
		return &proc.UnaryExpr{
			Op:   e.Op,
			Expr: rewriteProjectionExpr(e.Expr, colToVar, tableAliases),
		}

	case *proc.FunctionCallExpr:
		var newArgs []proc.Expr
		for _, a := range e.Args {
			newArgs = append(newArgs, rewriteProjectionExpr(a, colToVar, tableAliases))
		}
		return &proc.FunctionCallExpr{
			Name: e.Name,
			Args: newArgs,
		}

	default:
		return e
	}
}

func rewriteExprWithVars(expr proc.Expr, colToVar map[string]string) proc.Expr {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *proc.ColumnRefExpr:
		name := strings.ToLower(strings.Trim(e.Name, "`\"[]"))
		parts := strings.Split(name, ".")
		if len(parts) == 2 {
			tbl := parts[0]
			col := parts[1]
			if vName, ok := colToVar[tbl+"."+col]; ok {
				return &proc.VariableExpr{Name: "@" + vName}
			}
		} else if len(parts) == 1 {
			col := parts[0]
			if vName, ok := colToVar[col]; ok {
				return &proc.VariableExpr{Name: "@" + vName}
			}
			for k, vName := range colToVar {
				if strings.HasSuffix(k, "."+col) {
					return &proc.VariableExpr{Name: "@" + vName}
				}
			}
		}
		return e

	case *proc.BinaryExpr:
		return &proc.BinaryExpr{
			Left:  rewriteExprWithVars(e.Left, colToVar),
			Op:    e.Op,
			Right: rewriteExprWithVars(e.Right, colToVar),
		}

	case *proc.UnaryExpr:
		return &proc.UnaryExpr{
			Op:   e.Op,
			Expr: rewriteExprWithVars(e.Expr, colToVar),
		}

	case *proc.FunctionCallExpr:
		var newArgs []proc.Expr
		for _, a := range e.Args {
			newArgs = append(newArgs, rewriteExprWithVars(a, colToVar))
		}
		return &proc.FunctionCallExpr{
			Name: e.Name,
			Args: newArgs,
		}

	default:
		return e
	}
}

// tryExecuteLoweredLinearWritableCTE attempts to lower and execute the CTE using the VDBE VM fast path.
// If lowering compilation fails due to incompatible grammar, it returns (nil, nil) so the caller can fall back to Volcano.
func (s *Coordinator) tryExecuteLoweredLinearWritableCTE(ctx context.Context, ctes []planner.CTEInfo, mainSQL string, cs *ClientSession, start time.Time) (*QueryResult, error) {
	if s.executor == nil {
		return nil, nil
	}

	prog, colNames, err := compileLinearWritableCTE(ctes, mainSQL)
	if err != nil {
		return nil, nil
	}

	if cs == nil {
		cs, _ = s.GetOrCreateSession("default")
	}

	var session storage.Session
	var activeTx storage.Transaction

	if cs != nil {
		cs.Mu.Lock()
		activeTx = cs.Tx
		session = cs.Session
		cs.Mu.Unlock()
	}

	if activeTx == nil {
		// Run in implicit transaction managed by executor.RunContext with automatic conflict retry!
		sess, closeFn, err := s.acquireSessionForQuery(cs)
		if err != nil {
			return nil, err
		}
		defer closeFn()
		execRes, err := s.executor.RunContext(ctx, prog, "LINEAR_WRITABLE_CTE", nil, sess)
		if err != nil {
			return nil, err
		}
		return buildQueryResultFromExecResult(execRes, colNames, start), nil
	}

	// Active transaction exists: execute within activeTx
	execRes, err := s.executor.ExecuteInTxWithSessionContext(ctx, prog, nil, activeTx, session, nil)
	if err != nil {
		return nil, err
	}
	return buildQueryResultFromExecResult(execRes, colNames, start), nil
}

func buildQueryResultFromExecResult(execRes *runtime.ExecutionResult, colNames []string, start time.Time) *QueryResult {
	res := &QueryResult{
		Success:      true,
		Command:      "SELECT",
		DurationUS:   time.Since(start).Microseconds(),
		RowsAffected: execRes.RowsAffected,
		Messages:     execRes.Logs,
	}

	if len(execRes.Columns) > 0 {
		res.Columns = execRes.Columns
	} else if len(colNames) > 0 {
		res.Columns = colNames
	}

	if len(execRes.Rows) > 0 {
		res.Rows = execRes.Rows
		res.Values = execRes.Values
	} else {
		res.Rows = []map[string]any{}
		res.Values = [][]any{}
	}

	return res
}

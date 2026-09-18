package proc

import (
	"fmt"
	"strings"

	"sqlion/pkg/storage"
)

// Transpile converts a parsed T-SQL procedure AST into standard JavaScript code.
func Transpile(proc *CreateProcStmt) (string, error) {
	var b strings.Builder

	// Function header
	b.WriteString(fmt.Sprintf("function %s(params, db) {\n", proc.Name))

	// System state variables
	b.WriteString("  let _fetch_status = -1;\n")
	b.WriteString("  let _row_count = 0;\n")
	b.WriteString("  let _tran_count = 0;\n\n")

	// Bind parameters from params object
	for _, p := range proc.Params {
		rawName := cleanIdent(p.Name)
		b.WriteString(fmt.Sprintf("  let %s = params['%s'] !== undefined ? params['%s'] : params['@%s'];\n",
			rawName, rawName, rawName, rawName))
	}
	if len(proc.Params) > 0 {
		b.WriteString("\n")
	}

	// Transpile body
	bodyJS, err := transpileStmt(proc.Body, 1)
	if err != nil {
		return "", err
	}
	b.WriteString(bodyJS)

	if !strings.HasSuffix(strings.TrimSpace(bodyJS), "return 0;") &&
		!strings.HasSuffix(strings.TrimSpace(bodyJS), "return;") {
		b.WriteString("\n  return 0;\n")
	}
	b.WriteString("}\n")

	return b.String(), nil
}

func transpileStmt(stmt Statement, indentLevel int) (string, error) {
	indent := strings.Repeat("  ", indentLevel)

	switch s := stmt.(type) {
	case *BlockStmt:
		var b strings.Builder
		for _, sub := range s.Statements {
			code, err := transpileStmt(sub, indentLevel)
			if err != nil {
				return "", err
			}
			b.WriteString(code)
		}
		return b.String(), nil

	case *DeclareStmt:
		name := cleanIdent(s.Name)
		return fmt.Sprintf("%slet %s = null;\n", indent, name), nil

	case *SetStmt:
		name := cleanIdent(s.Name)
		exprJS, err := transpileExpr(s.Value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s%s = %s;\n", indent, name, exprJS), nil

	case *IfStmt:
		condJS, err := transpileExpr(s.Condition)
		if err != nil {
			return "", err
		}
		thenJS, err := transpileStmt(s.Then, indentLevel+1)
		if err != nil {
			return "", err
		}

		var b strings.Builder
		b.WriteString(fmt.Sprintf("%sif (%s) {\n%s%s}", indent, condJS, thenJS, indent))

		if s.Else != nil {
			elseJS, err := transpileStmt(s.Else, indentLevel+1)
			if err != nil {
				return "", err
			}
			b.WriteString(fmt.Sprintf(" else {\n%s%s}\n", elseJS, indent))
		} else {
			b.WriteString("\n")
		}
		return b.String(), nil

	case *WhileStmt:
		condJS, err := transpileExpr(s.Condition)
		if err != nil {
			return "", err
		}
		bodyJS, err := transpileStmt(s.Body, indentLevel+1)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%swhile (%s) {\n%s%s}\n", indent, condJS, bodyJS, indent), nil

	case *ReturnStmt:
		if s.Value != nil {
			valJS, err := transpileExpr(s.Value)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%sreturn %s;\n", indent, valJS), nil
		}
		return fmt.Sprintf("%sreturn 0;\n", indent), nil

	case *PrintStmt:
		exprJS, err := transpileExpr(s.Expr)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%sconsole.log(%s);\n", indent, exprJS), nil

	case *RaiseErrorStmt:
		return fmt.Sprintf("%sthrow new Error(%q);\n", indent, s.Message), nil

	case *ThrowStmt:
		return fmt.Sprintf("%sthrow new Error(%q);\n", indent, s.Message), nil

	case *TryCatchStmt:
		tryJS, err := transpileStmt(s.TryBody, indentLevel+1)
		if err != nil {
			return "", err
		}
		catchJS, err := transpileStmt(s.CatchBody, indentLevel+1)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%stry {\n%s%s} catch (_err) {\n%s%s}\n",
			indent, tryJS, indent, catchJS, indent), nil

	case *BreakStmt:
		return fmt.Sprintf("%sbreak;\n", indent), nil

	case *ContinueStmt:
		return fmt.Sprintf("%scontinue;\n", indent), nil

	case *TxStmt:
		switch s.Action {
		case TxBegin:
			return fmt.Sprintf("%s{ db.beginTransaction && db.beginTransaction(); _tran_count++; }\n", indent), nil
		case TxCommit:
			return fmt.Sprintf("%s{ db.commitTransaction && db.commitTransaction(); if (_tran_count > 0) _tran_count--; }\n", indent), nil
		case TxRollback:
			return fmt.Sprintf("%s{ db.rollbackTransaction && db.rollbackTransaction(); _tran_count = 0; }\n", indent), nil
		}
		return "", nil

	case *SelectIntoStmt:
		intoVar := cleanIdent(s.IntoVar)
		if s.WhereVal != nil {
			pkExpr, err := transpileExpr(s.WhereVal)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%s%s = db.get(%q, %s, %q);\n", indent, intoVar, s.Table, pkExpr, s.Column), nil
		}
		return fmt.Sprintf("%s{ let _rows = db.query(%q); if (_rows && _rows.length > 0) { %s = _rows[0][%q]; } }\n",
			indent, s.Table, intoVar, s.Column), nil

	case *InsertStmt:
		pairs := make([]string, len(s.Columns))
		for i, col := range s.Columns {
			valJS, err := transpileExpr(s.Values[i])
			if err != nil {
				return "", err
			}
			pairs[i] = fmt.Sprintf("%q: %s", col, valJS)
		}
		return fmt.Sprintf("%s{ db.insert(%q, { %s }); _row_count = 1; }\n", indent, s.Table, strings.Join(pairs, ", ")), nil

	case *UpdateStmt:
		pkExpr, err := transpileExpr(s.WhereVal)
		if err != nil {
			return "", err
		}

		hasColRef := false
		for _, a := range s.Assignments {
			if containsColRef(a.Value) {
				hasColRef = true
				break
			}
		}

		rowScope := ""
		if hasColRef {
			rowScope = "_curr"
		}

		pairs := make([]string, len(s.Assignments))
		for i, a := range s.Assignments {
			valJS, err := transpileExprWithScope(a.Value, rowScope)
			if err != nil {
				return "", err
			}
			pairs[i] = fmt.Sprintf("%q: %s", a.Column, valJS)
		}

		if hasColRef {
			var b strings.Builder
			b.WriteString(fmt.Sprintf("%s{\n", indent))
			b.WriteString(fmt.Sprintf("%s  let _curr = db.get(%q, %s);\n", indent, s.Table, pkExpr))
			b.WriteString(fmt.Sprintf("%s  db.update(%q, %s, { %s });\n", indent, s.Table, pkExpr, strings.Join(pairs, ", ")))
			b.WriteString(fmt.Sprintf("%s  _row_count = 1;\n", indent))
			b.WriteString(fmt.Sprintf("%s}\n", indent))
			return b.String(), nil
		}
		return fmt.Sprintf("%s{ db.update(%q, %s, { %s }); _row_count = 1; }\n", indent, s.Table, pkExpr, strings.Join(pairs, ", ")), nil

	case *UpdateWhereStmt:
		whereJS, err := transpileExprWithScope(s.Where, "row")
		if err != nil {
			return "", err
		}

		pairs := make([]string, len(s.Assignments))
		for i, a := range s.Assignments {
			valJS, err := transpileExprWithScope(a.Value, "row")
			if err != nil {
				return "", err
			}
			pairs[i] = fmt.Sprintf("%q: %s", a.Column, valJS)
		}

		return fmt.Sprintf("%s_row_count = db.updateWhere(%q, function(row) { return %s; }, function(row) { return { %s }; });\n",
			indent, s.Table, whereJS, strings.Join(pairs, ", ")), nil

	case *DeleteStmt:
		pkExpr, err := transpileExpr(s.WhereVal)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s{ db.delete(%q, %s); _row_count = 1; }\n", indent, s.Table, pkExpr), nil

	case *DeleteWhereStmt:
		whereJS := "true"
		if s.Where != nil {
			var err error
			whereJS, err = transpileExprWithScope(s.Where, "row")
			if err != nil {
				return "", err
			}
		}
		return fmt.Sprintf("%s_row_count = db.deleteWhere(%q, function(row) { return %s; });\n",
			indent, s.Table, whereJS), nil

	case *DeclareCursorStmt:
		curVar := "_cur_" + cleanIdent(s.Name)
		whereJS := "true"
		if s.Where != nil {
			var err error
			whereJS, err = transpileExprWithScope(s.Where, "row")
			if err != nil {
				return "", err
			}
		}
		colsJSON := make([]string, len(s.Columns))
		for i, c := range s.Columns {
			colsJSON[i] = fmt.Sprintf("%q", c)
		}
		return fmt.Sprintf("%slet %s = { index: 0, columns: [%s], rows: db.query(%q, function(row) { return %s; }) };\n",
			indent, curVar, strings.Join(colsJSON, ", "), s.Table, whereJS), nil

	case *OpenCursorStmt:
		curVar := "_cur_" + cleanIdent(s.Name)
		return fmt.Sprintf("%sif (%s) { %s.index = 0; }\n", indent, curVar, curVar), nil

	case *FetchNextStmt:
		curVar := "_cur_" + cleanIdent(s.CursorName)
		var b strings.Builder
		b.WriteString(fmt.Sprintf("%sif (%s && %s.index < %s.rows.length) {\n", indent, curVar, curVar, curVar))
		b.WriteString(fmt.Sprintf("%s  let _r = %s.rows[%s.index++];\n", indent, curVar, curVar))
		for i, v := range s.IntoVars {
			vName := cleanIdent(v)
			b.WriteString(fmt.Sprintf("%s  if (%s.columns && %s.columns[%d]) { %s = _r[%s.columns[%d]]; } else { %s = Object.values(_r)[%d]; }\n",
				indent, curVar, curVar, i, vName, curVar, i, vName, i))
		}
		b.WriteString(fmt.Sprintf("%s  _fetch_status = 0;\n", indent))
		b.WriteString(fmt.Sprintf("%s} else {\n", indent))
		b.WriteString(fmt.Sprintf("%s  _fetch_status = -1;\n", indent))
		b.WriteString(fmt.Sprintf("%s}\n", indent))
		return b.String(), nil

	case *CloseCursorStmt, *DeallocateCursorStmt:
		return "", nil

	case *ExecStmt:
		argStrs := make([]string, len(s.Args))
		for i, a := range s.Args {
			js, err := transpileExpr(a)
			if err != nil {
				return "", err
			}
			argStrs[i] = js
		}
		return fmt.Sprintf("%sif (typeof %s === 'function') { %s({ %s }, db); }\n",
			indent, s.ProcName, s.ProcName, strings.Join(argStrs, ", ")), nil

	default:
		return "", fmt.Errorf("unsupported statement for JS transpilation: %T", stmt)
	}
}

func containsColRef(expr Expr) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *ColumnRefExpr:
		return true
	case *UnaryExpr:
		return containsColRef(e.Expr)
	case *BinaryExpr:
		return containsColRef(e.Left) || containsColRef(e.Right)
	case *InExpr:
		if containsColRef(e.Expr) {
			return true
		}
		for _, item := range e.List {
			if containsColRef(item) {
				return true
			}
		}
		return false
	case *BetweenExpr:
		return containsColRef(e.Expr) || containsColRef(e.Low) || containsColRef(e.High)
	case *LikeExpr:
		return containsColRef(e.Expr) || containsColRef(e.Pattern)
	case *IsNullExpr:
		return containsColRef(e.Expr)
	case *CaseExpr:
		for _, w := range e.Whens {
			if containsColRef(w.Condition) || containsColRef(w.Result) {
				return true
			}
		}
		return containsColRef(e.Else)
	case *FunctionCallExpr:
		for _, a := range e.Args {
			if containsColRef(a) {
				return true
			}
		}
	}
	return false
}

func transpileExpr(expr Expr) (string, error) {
	return transpileExprWithScope(expr, "row")
}

func transpileExprWithScope(expr Expr, rowScope string) (string, error) {
	if expr == nil {
		return "null", nil
	}

	switch e := expr.(type) {
	case *LiteralExpr:
		if e.Val.IsNull {
			return "null", nil
		}
		switch e.Val.Type {
		case storage.TypeVarChar:
			return fmt.Sprintf("%q", e.Val.SVal), nil
		case storage.TypeBool:
			if e.Val.BVal {
				return "true", nil
			}
			return "false", nil
		default:
			return e.Val.String(), nil
		}

	case *VariableExpr:
		upper := strings.ToUpper(e.Name)
		switch upper {
		case "@@FETCH_STATUS":
			return "_fetch_status", nil
		case "@@ROWCOUNT":
			return "_row_count", nil
		case "@@TRANCOUNT":
			return "_tran_count", nil
		default:
			return cleanIdent(e.Name), nil
		}

	case *ColumnRefExpr:
		if rowScope != "" {
			return fmt.Sprintf("%s[%q]", rowScope, e.Name), nil
		}
		return fmt.Sprintf("row[%q]", e.Name), nil

	case *InExpr:
		targetJS, err := transpileExprWithScope(e.Expr, rowScope)
		if err != nil {
			return "", err
		}
		listJS := make([]string, len(e.List))
		for i, item := range e.List {
			s, err := transpileExprWithScope(item, rowScope)
			if err != nil {
				return "", err
			}
			listJS[i] = s
		}
		if e.Not {
			return fmt.Sprintf("(!SQLion.in(%s, [%s]))", targetJS, strings.Join(listJS, ", ")), nil
		}
		return fmt.Sprintf("SQLion.in(%s, [%s])", targetJS, strings.Join(listJS, ", ")), nil

	case *BetweenExpr:
		targetJS, err := transpileExprWithScope(e.Expr, rowScope)
		if err != nil {
			return "", err
		}
		lowJS, err := transpileExprWithScope(e.Low, rowScope)
		if err != nil {
			return "", err
		}
		highJS, err := transpileExprWithScope(e.High, rowScope)
		if err != nil {
			return "", err
		}
		if e.Not {
			return fmt.Sprintf("(!SQLion.between(%s, %s, %s))", targetJS, lowJS, highJS), nil
		}
		return fmt.Sprintf("SQLion.between(%s, %s, %s)", targetJS, lowJS, highJS), nil

	case *LikeExpr:
		targetJS, err := transpileExprWithScope(e.Expr, rowScope)
		if err != nil {
			return "", err
		}
		patternJS, err := transpileExprWithScope(e.Pattern, rowScope)
		if err != nil {
			return "", err
		}
		if e.Not {
			return fmt.Sprintf("(!SQLion.like(%s, %s))", targetJS, patternJS), nil
		}
		return fmt.Sprintf("SQLion.like(%s, %s)", targetJS, patternJS), nil

	case *IsNullExpr:
		subJS, err := transpileExprWithScope(e.Expr, rowScope)
		if err != nil {
			return "", err
		}
		if e.Not {
			return fmt.Sprintf("((%s) !== null && (%s) !== undefined)", subJS, subJS), nil
		}
		return fmt.Sprintf("((%s) === null || (%s) === undefined)", subJS, subJS), nil

	case *ScalarSubqueryExpr:
		whereJS := "true"
		if e.Where != nil {
			var err error
			whereJS, err = transpileExprWithScope(e.Where, "row")
			if err != nil {
				return "", err
			}
		}
		return fmt.Sprintf("(function() { let _rows = db.query(%q, function(row) { return %s; }); return _rows.length > 0 ? _rows[0][%q] : null; })()",
			e.Table, whereJS, e.Column), nil

	case *CaseExpr:
		elseJS := "null"
		if e.Else != nil {
			var err error
			elseJS, err = transpileExprWithScope(e.Else, rowScope)
			if err != nil {
				return "", err
			}
		}
		res := elseJS
		for i := len(e.Whens) - 1; i >= 0; i-- {
			w := e.Whens[i]
			condJS, err := transpileExprWithScope(w.Condition, rowScope)
			if err != nil {
				return "", err
			}
			valJS, err := transpileExprWithScope(w.Result, rowScope)
			if err != nil {
				return "", err
			}
			res = fmt.Sprintf("((%s) ? (%s) : (%s))", condJS, valJS, res)
		}
		return res, nil

	case *FunctionCallExpr:
		switch strings.ToUpper(e.Name) {
		case "ISNULL":
			if len(e.Args) >= 2 {
				aJS, _ := transpileExprWithScope(e.Args[0], rowScope)
				bJS, _ := transpileExprWithScope(e.Args[1], rowScope)
				return fmt.Sprintf("((%s !== null && %s !== undefined) ? (%s) : (%s))", aJS, aJS, aJS, bJS), nil
			}
		case "COALESCE":
			if len(e.Args) == 0 {
				return "null", nil
			}
			res := "null"
			for i := len(e.Args) - 1; i >= 0; i-- {
				argJS, _ := transpileExprWithScope(e.Args[i], rowScope)
				if i == len(e.Args)-1 {
					res = argJS
				} else {
					res = fmt.Sprintf("((%s !== null && %s !== undefined) ? (%s) : (%s))", argJS, argJS, argJS, res)
				}
			}
			return res, nil
		case "ABS":
			if len(e.Args) >= 1 {
				aJS, _ := transpileExprWithScope(e.Args[0], rowScope)
				return fmt.Sprintf("Math.abs(%s)", aJS), nil
			}
		case "ROUND":
			if len(e.Args) >= 1 {
				aJS, _ := transpileExprWithScope(e.Args[0], rowScope)
				return fmt.Sprintf("Math.round(%s)", aJS), nil
			}
		case "CEILING", "CEIL":
			if len(e.Args) >= 1 {
				aJS, _ := transpileExprWithScope(e.Args[0], rowScope)
				return fmt.Sprintf("Math.ceil(%s)", aJS), nil
			}
		case "FLOOR":
			if len(e.Args) >= 1 {
				aJS, _ := transpileExprWithScope(e.Args[0], rowScope)
				return fmt.Sprintf("Math.floor(%s)", aJS), nil
			}
		case "UPPER":
			if len(e.Args) >= 1 {
				aJS, _ := transpileExprWithScope(e.Args[0], rowScope)
				return fmt.Sprintf("String(%s).toUpperCase()", aJS), nil
			}
		case "LOWER":
			if len(e.Args) >= 1 {
				aJS, _ := transpileExprWithScope(e.Args[0], rowScope)
				return fmt.Sprintf("String(%s).toLowerCase()", aJS), nil
			}
		case "LTRIM":
			if len(e.Args) >= 1 {
				aJS, _ := transpileExprWithScope(e.Args[0], rowScope)
				return fmt.Sprintf("String(%s).replace(/^\\s+/, '')", aJS), nil
			}
		case "RTRIM":
			if len(e.Args) >= 1 {
				aJS, _ := transpileExprWithScope(e.Args[0], rowScope)
				return fmt.Sprintf("String(%s).replace(/\\s+$/, '')", aJS), nil
			}
		case "TRIM":
			if len(e.Args) >= 1 {
				aJS, _ := transpileExprWithScope(e.Args[0], rowScope)
				return fmt.Sprintf("String(%s).trim()", aJS), nil
			}
		case "GETDATE", "NOW":
			return "new Date().toISOString()", nil
		case "LEN", "LENGTH":
			if len(e.Args) >= 1 {
				argJS, _ := transpileExprWithScope(e.Args[0], rowScope)
				return fmt.Sprintf("(%s ? String(%s).length : 0)", argJS, argJS), nil
			}
		case "SUBSTRING":
			if len(e.Args) >= 3 {
				strJS, _ := transpileExprWithScope(e.Args[0], rowScope)
				startJS, _ := transpileExprWithScope(e.Args[1], rowScope)
				lenJS, _ := transpileExprWithScope(e.Args[2], rowScope)
				return fmt.Sprintf("(%s ? String(%s).substring((%s) - 1, (%s) - 1 + (%s)) : \"\")", strJS, strJS, startJS, startJS, lenJS), nil
			}
		case "REPLACE":
			if len(e.Args) >= 3 {
				strJS, _ := transpileExprWithScope(e.Args[0], rowScope)
				fromJS, _ := transpileExprWithScope(e.Args[1], rowScope)
				toJS, _ := transpileExprWithScope(e.Args[2], rowScope)
				return fmt.Sprintf("(%s ? String(%s).split(%s).join(%s) : \"\")", strJS, strJS, fromJS, toJS), nil
			}
		}

		argStrs := make([]string, len(e.Args))
		for i, a := range e.Args {
			s, err := transpileExprWithScope(a, rowScope)
			if err != nil {
				return "", err
			}
			argStrs[i] = s
		}
		return fmt.Sprintf("%s(%s)", e.Name, strings.Join(argStrs, ", ")), nil

	case *UnaryExpr:
		subJS, err := transpileExprWithScope(e.Expr, rowScope)
		if err != nil {
			return "", err
		}
		switch e.Op {
		case TokenMinus:
			return fmt.Sprintf("(-%s)", subJS), nil
		case TokenNot:
			return fmt.Sprintf("(!%s)", subJS), nil
		default:
			return "", fmt.Errorf("unsupported unary operator: %v", e.Op)
		}

	case *BinaryExpr:
		leftJS, err := transpileExprWithScope(e.Left, rowScope)
		if err != nil {
			return "", err
		}
		rightJS, err := transpileExprWithScope(e.Right, rowScope)
		if err != nil {
			return "", err
		}

		var opStr string
		switch e.Op {
		case TokenPlus:
			opStr = "+"
		case TokenMinus:
			opStr = "-"
		case TokenAsterisk:
			opStr = "*"
		case TokenSlash:
			opStr = "/"
		case TokenEq:
			opStr = "==="
		case TokenNeq:
			opStr = "!=="
		case TokenLt:
			opStr = "<"
		case TokenLte:
			opStr = "<="
		case TokenGt:
			opStr = ">"
		case TokenGte:
			opStr = ">="
		case TokenAnd:
			opStr = "&&"
		case TokenOr:
			opStr = "||"
		default:
			return "", fmt.Errorf("unsupported binary operator: %v", e.Op)
		}
		return fmt.Sprintf("(%s %s %s)", leftJS, opStr, rightJS), nil

	default:
		return "", fmt.Errorf("unsupported expression type: %T", expr)
	}
}

func cleanIdent(name string) string {
	return strings.TrimPrefix(name, "@")
}

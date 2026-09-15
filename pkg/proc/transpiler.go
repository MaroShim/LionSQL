package proc

import (
	"fmt"
	"strings"

	"sqltiger/pkg/storage"
)

// Transpile converts a parsed T-SQL procedure AST into standard JavaScript code.
func Transpile(proc *CreateProcStmt) (string, error) {
	var b strings.Builder

	// Function header
	b.WriteString(fmt.Sprintf("function %s(params, db) {\n", proc.Name))

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

	case *TxStmt:
		switch s.Action {
		case TxBegin:
			return fmt.Sprintf("%sdb.beginTransaction();\n", indent), nil
		case TxCommit:
			return fmt.Sprintf("%sdb.commitTransaction();\n", indent), nil
		case TxRollback:
			return fmt.Sprintf("%sdb.rollbackTransaction();\n", indent), nil
		}
		return "", nil

	case *SelectIntoStmt:
		intoVar := cleanIdent(s.IntoVar)
		pkExpr, err := transpileExpr(s.WhereVal)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s%s = db.get(%q, %s, %q);\n", indent, intoVar, s.Table, pkExpr, s.Column), nil

	case *InsertStmt:
		pairs := make([]string, len(s.Columns))
		for i, col := range s.Columns {
			valJS, err := transpileExpr(s.Values[i])
			if err != nil {
				return "", err
			}
			pairs[i] = fmt.Sprintf("%q: %s", col, valJS)
		}
		return fmt.Sprintf("%sdb.insert(%q, { %s });\n", indent, s.Table, strings.Join(pairs, ", ")), nil

	case *UpdateStmt:
		pkExpr, err := transpileExpr(s.WhereVal)
		if err != nil {
			return "", err
		}
		pairs := make([]string, len(s.Assignments))
		for i, a := range s.Assignments {
			valJS, err := transpileExpr(a.Value)
			if err != nil {
				return "", err
			}
			pairs[i] = fmt.Sprintf("%q: %s", a.Column, valJS)
		}
		return fmt.Sprintf("%sdb.update(%q, %s, { %s });\n", indent, s.Table, pkExpr, strings.Join(pairs, ", ")), nil

	case *DeleteStmt:
		pkExpr, err := transpileExpr(s.WhereVal)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%sdb.delete(%q, %s);\n", indent, s.Table, pkExpr), nil

	default:
		return "", fmt.Errorf("unsupported statement for JS transpilation: %T", stmt)
	}
}

func transpileExpr(expr Expr) (string, error) {
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
		return cleanIdent(e.Name), nil

	case *ColumnRefExpr:
		// In JS context, column references resolve from row scope or variable
		return fmt.Sprintf("row[%q]", e.Name), nil

	case *UnaryExpr:
		subJS, err := transpileExpr(e.Expr)
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
		leftJS, err := transpileExpr(e.Left)
		if err != nil {
			return "", err
		}
		rightJS, err := transpileExpr(e.Right)
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

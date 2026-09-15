package proc

import (
	"fmt"
	"strconv"
	"strings"

	"sqlserver/pkg/storage"
)

// Precedence levels for Pratt parsing
const (
	_ int = iota
	precLowest
	precOr
	precAnd
	precEquals      // = != <>
	precLessGreater // < > <= >=
	precSum         // + -
	precProduct     // * /
	precPrefix      // -X, NOT X
)

var precedences = map[TokenType]int{
	TokenOr:       precOr,
	TokenAnd:      precAnd,
	TokenEq:       precEquals,
	TokenNeq:      precEquals,
	TokenLt:       precLessGreater,
	TokenLte:      precLessGreater,
	TokenGt:       precLessGreater,
	TokenGte:      precLessGreater,
	TokenPlus:     precSum,
	TokenMinus:    precSum,
	TokenAsterisk: precProduct,
	TokenSlash:    precProduct,
}

// Parser parses T-SQL tokens into an AST.
type Parser struct {
	l         *Lexer
	curToken  Token
	peekToken Token
	errors    []string
}

// NewParser creates a new Parser.
func NewParser(l *Lexer) *Parser {
	p := &Parser{l: l}
	p.nextToken()
	p.nextToken()
	return p
}

func (p *Parser) nextToken() {
	p.curToken = p.peekToken
	p.peekToken = p.l.NextToken()
}

func (p *Parser) curTokenIs(t TokenType) bool {
	return p.curToken.Type == t
}

func (p *Parser) peekTokenIs(t TokenType) bool {
	return p.peekToken.Type == t
}

func (p *Parser) expectPeek(t TokenType) bool {
	if p.peekTokenIs(t) {
		p.nextToken()
		return true
	}
	p.peekError(t)
	return false
}

func (p *Parser) peekError(t TokenType) {
	msg := fmt.Sprintf("expected next token to be %v, got %v (%q) at line %d:%d",
		t, p.peekToken.Type, p.peekToken.Literal, p.peekToken.Line, p.peekToken.Col)
	p.errors = append(p.errors, msg)
}

func (p *Parser) Errors() []string {
	return p.errors
}

// ParseProcedure parses a full CREATE PROCEDURE script.
func (p *Parser) ParseProcedure() (*CreateProcStmt, error) {
	if !p.curTokenIs(TokenCreate) {
		return nil, fmt.Errorf("expected CREATE, got %s", p.curToken.Literal)
	}
	p.nextToken()

	if !p.curTokenIs(TokenProcedure) && !p.curTokenIs(TokenProc) {
		return nil, fmt.Errorf("expected PROCEDURE or PROC, got %s", p.curToken.Literal)
	}
	p.nextToken()

	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected procedure name, got %s", p.curToken.Literal)
	}
	procName := p.curToken.Literal
	p.nextToken()

	params := make([]ParamDef, 0)
	// Parse parameter list if any (starts with @)
	for p.curTokenIs(TokenVariable) {
		paramName := p.curToken.Literal
		p.nextToken()

		if !p.curTokenIs(TokenIdent) {
			return nil, fmt.Errorf("expected type for parameter %s, got %s", paramName, p.curToken.Literal)
		}
		paramType := parseDataType(p.curToken.Literal)
		params = append(params, ParamDef{Name: paramName, Type: paramType})
		p.nextToken()

		if p.curTokenIs(TokenComma) {
			p.nextToken()
		}
	}

	if !p.curTokenIs(TokenAs) {
		return nil, fmt.Errorf("expected AS, got %s", p.curToken.Literal)
	}
	p.nextToken()

	body, err := p.parseStatement()
	if err != nil {
		return nil, err
	}

	return &CreateProcStmt{
		Name:   procName,
		Params: params,
		Body:   body,
	}, nil
}

func parseDataType(s string) storage.DataType {
	switch strings.ToUpper(s) {
	case "INT", "INTEGER", "BIGINT":
		return storage.TypeInt
	case "VARCHAR", "TEXT", "NVARCHAR", "CHAR":
		return storage.TypeVarChar
	case "FLOAT", "DECIMAL", "DOUBLE", "NUMERIC":
		return storage.TypeFloat
	case "BIT", "BOOL", "BOOLEAN":
		return storage.TypeBool
	case "DATETIME", "TIMESTAMP":
		return storage.TypeDateTime
	default:
		return storage.TypeVarChar
	}
}

func (p *Parser) parseStatement() (Statement, error) {
	switch p.curToken.Type {
	case TokenBegin:
		if p.peekTokenIs(TokenTransaction) || p.peekTokenIs(TokenTran) {
			return p.parseTx()
		}
		return p.parseBlock()
	case TokenDeclare:
		return p.parseDeclare()
	case TokenSet:
		return p.parseSet()
	case TokenIf:
		return p.parseIf()
	case TokenWhile:
		return p.parseWhile()
	case TokenReturn:
		return p.parseReturn()
	case TokenPrint:
		return p.parsePrint()
	case TokenRaiserror:
		return p.parseRaiserror()
	case TokenCommit, TokenRollback:
		return p.parseTx()
	case TokenSelect:
		return p.parseSelect()
	case TokenInsert:
		return p.parseInsert()
	case TokenUpdate:
		return p.parseUpdate()
	case TokenDelete:
		return p.parseDelete()
	case TokenExec:
		return p.parseExec()
	default:
		return nil, fmt.Errorf("unexpected token in statement: %s at %d:%d",
			p.curToken.Literal, p.curToken.Line, p.curToken.Col)
	}
}

func (p *Parser) parseBlock() (Statement, error) {
	p.nextToken() // eat BEGIN
	var stmts []Statement

	for !p.curTokenIs(TokenEnd) && !p.curTokenIs(TokenEOF) {
		stmt, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		if stmt != nil {
			stmts = append(stmts, stmt)
		}
		if p.curTokenIs(TokenSemicolon) {
			p.nextToken()
		}
	}

	if !p.curTokenIs(TokenEnd) {
		return nil, fmt.Errorf("expected END at end of block, got %s", p.curToken.Literal)
	}
	p.nextToken() // eat END

	return &BlockStmt{Statements: stmts}, nil
}

func (p *Parser) parseDeclare() (Statement, error) {
	p.nextToken() // eat DECLARE
	if !p.curTokenIs(TokenVariable) {
		return nil, fmt.Errorf("expected variable name in DECLARE, got %s", p.curToken.Literal)
	}
	varName := p.curToken.Literal
	p.nextToken()

	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected data type for %s, got %s", varName, p.curToken.Literal)
	}
	dt := parseDataType(p.curToken.Literal)
	p.nextToken()

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &DeclareStmt{Name: varName, Type: dt}, nil
}

func (p *Parser) parseSet() (Statement, error) {
	p.nextToken() // eat SET
	if !p.curTokenIs(TokenVariable) {
		return nil, fmt.Errorf("expected variable name in SET, got %s", p.curToken.Literal)
	}
	varName := p.curToken.Literal
	p.nextToken()

	if !p.curTokenIs(TokenEq) && !p.curTokenIs(TokenAssign) {
		return nil, fmt.Errorf("expected = in SET, got %s", p.curToken.Literal)
	}
	p.nextToken()

	expr := p.parseExpression(precLowest)
	if expr == nil {
		return nil, fmt.Errorf("expected expression in SET %s", varName)
	}

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &SetStmt{Name: varName, Value: expr}, nil
}

func (p *Parser) parseIf() (Statement, error) {
	p.nextToken() // eat IF
	cond := p.parseExpression(precLowest)
	if cond == nil {
		return nil, fmt.Errorf("expected condition in IF")
	}

	thenStmt, err := p.parseStatement()
	if err != nil {
		return nil, err
	}

	var elseStmt Statement
	if p.curTokenIs(TokenElse) {
		p.nextToken() // eat ELSE
		elseStmt, err = p.parseStatement()
		if err != nil {
			return nil, err
		}
	}

	return &IfStmt{
		Condition: cond,
		Then:      thenStmt,
		Else:      elseStmt,
	}, nil
}

func (p *Parser) parseWhile() (Statement, error) {
	p.nextToken() // eat WHILE
	cond := p.parseExpression(precLowest)
	if cond == nil {
		return nil, fmt.Errorf("expected condition in WHILE")
	}

	body, err := p.parseStatement()
	if err != nil {
		return nil, err
	}

	return &WhileStmt{
		Condition: cond,
		Body:      body,
	}, nil
}

func (p *Parser) parseReturn() (Statement, error) {
	p.nextToken() // eat RETURN
	var val Expr
	if !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEnd) && !p.curTokenIs(TokenEOF) {
		val = p.parseExpression(precLowest)
	}
	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}
	return &ReturnStmt{Value: val}, nil
}

func (p *Parser) parsePrint() (Statement, error) {
	p.nextToken() // eat PRINT
	expr := p.parseExpression(precLowest)
	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}
	return &PrintStmt{Expr: expr}, nil
}

func (p *Parser) parseRaiserror() (Statement, error) {
	p.nextToken() // eat RAISERROR
	if !p.curTokenIs(TokenLParen) {
		return nil, fmt.Errorf("expected ( after RAISERROR")
	}
	p.nextToken()

	if !p.curTokenIs(TokenString) {
		return nil, fmt.Errorf("expected string message in RAISERROR")
	}
	msg := p.curToken.Literal
	p.nextToken()

	// Skip severity and state parameters if present
	for p.curTokenIs(TokenComma) {
		p.nextToken()
		p.parseExpression(precLowest)
	}

	if !p.curTokenIs(TokenRParen) {
		return nil, fmt.Errorf("expected ) after RAISERROR")
	}
	p.nextToken()

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &RaiseErrorStmt{Message: msg}, nil
}

func (p *Parser) parseTx() (Statement, error) {
	switch p.curToken.Type {
	case TokenBegin:
		p.nextToken() // eat BEGIN
		p.nextToken() // eat TRAN / TRANSACTION
		if p.curTokenIs(TokenSemicolon) {
			p.nextToken()
		}
		return &TxStmt{Action: TxBegin}, nil
	case TokenCommit:
		p.nextToken() // eat COMMIT
		if p.curTokenIs(TokenTran) || p.curTokenIs(TokenTransaction) {
			p.nextToken()
		}
		if p.curTokenIs(TokenSemicolon) {
			p.nextToken()
		}
		return &TxStmt{Action: TxCommit}, nil
	case TokenRollback:
		p.nextToken() // eat ROLLBACK
		if p.curTokenIs(TokenTran) || p.curTokenIs(TokenTransaction) {
			p.nextToken()
		}
		if p.curTokenIs(TokenSemicolon) {
			p.nextToken()
		}
		return &TxStmt{Action: TxRollback}, nil
	default:
		return nil, fmt.Errorf("invalid transaction statement")
	}
}

func (p *Parser) parseSelect() (Statement, error) {
	p.nextToken() // eat SELECT

	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected column in SELECT, got %s", p.curToken.Literal)
	}
	colName := p.curToken.Literal
	p.nextToken()

	if !p.curTokenIs(TokenInto) {
		return nil, fmt.Errorf("currently only SELECT col INTO @var is supported in stored procedure scripts")
	}
	p.nextToken()

	if !p.curTokenIs(TokenVariable) {
		return nil, fmt.Errorf("expected @variable after INTO, got %s", p.curToken.Literal)
	}
	varName := p.curToken.Literal
	p.nextToken()

	if !p.curTokenIs(TokenFrom) {
		return nil, fmt.Errorf("expected FROM, got %s", p.curToken.Literal)
	}
	p.nextToken()

	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected table name, got %s", p.curToken.Literal)
	}
	tblName := p.curToken.Literal
	p.nextToken()

	var whereCol string
	var whereVal Expr
	if p.curTokenIs(TokenWhere) {
		p.nextToken()
		if !p.curTokenIs(TokenIdent) {
			return nil, fmt.Errorf("expected column in WHERE, got %s", p.curToken.Literal)
		}
		whereCol = p.curToken.Literal
		p.nextToken()

		if !p.curTokenIs(TokenEq) {
			return nil, fmt.Errorf("expected = in WHERE clause, got %s", p.curToken.Literal)
		}
		p.nextToken()

		whereVal = p.parseExpression(precLowest)
	}

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &SelectIntoStmt{
		Column:   colName,
		IntoVar:  varName,
		Table:    tblName,
		WhereCol: whereCol,
		WhereVal: whereVal,
	}, nil
}

func (p *Parser) parseInsert() (Statement, error) {
	p.nextToken() // eat INSERT
	if p.curTokenIs(TokenInto) {
		p.nextToken()
	}
	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected table name in INSERT, got %s", p.curToken.Literal)
	}
	tblName := p.curToken.Literal
	p.nextToken()

	var cols []string
	if p.curTokenIs(TokenLParen) {
		p.nextToken()
		for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
			if !p.curTokenIs(TokenIdent) {
				return nil, fmt.Errorf("expected column name in INSERT, got %s", p.curToken.Literal)
			}
			cols = append(cols, p.curToken.Literal)
			p.nextToken()
			if p.curTokenIs(TokenComma) {
				p.nextToken()
			}
		}
		if !p.curTokenIs(TokenRParen) {
			return nil, fmt.Errorf("expected ) closing columns in INSERT")
		}
		p.nextToken()
	}

	if !p.curTokenIs(TokenValues) {
		return nil, fmt.Errorf("expected VALUES in INSERT, got %s", p.curToken.Literal)
	}
	p.nextToken()

	if !p.curTokenIs(TokenLParen) {
		return nil, fmt.Errorf("expected ( after VALUES")
	}
	p.nextToken()

	var vals []Expr
	for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
		expr := p.parseExpression(precLowest)
		if expr == nil {
			return nil, fmt.Errorf("expected value expression in INSERT")
		}
		vals = append(vals, expr)
		if p.curTokenIs(TokenComma) {
			p.nextToken()
		}
	}

	if !p.curTokenIs(TokenRParen) {
		return nil, fmt.Errorf("expected ) after VALUES")
	}
	p.nextToken()

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &InsertStmt{
		Table:   tblName,
		Columns: cols,
		Values:  vals,
	}, nil
}

func (p *Parser) parseUpdate() (Statement, error) {
	p.nextToken() // eat UPDATE
	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected table name in UPDATE, got %s", p.curToken.Literal)
	}
	tblName := p.curToken.Literal
	p.nextToken()

	if !p.curTokenIs(TokenSet) {
		return nil, fmt.Errorf("expected SET in UPDATE, got %s", p.curToken.Literal)
	}
	p.nextToken()

	var assigns []UpdateAssignment
	for {
		if !p.curTokenIs(TokenIdent) {
			return nil, fmt.Errorf("expected column name in UPDATE SET, got %s", p.curToken.Literal)
		}
		colName := p.curToken.Literal
		p.nextToken()

		if !p.curTokenIs(TokenEq) && !p.curTokenIs(TokenAssign) {
			return nil, fmt.Errorf("expected = in UPDATE SET, got %s", p.curToken.Literal)
		}
		p.nextToken()

		valExpr := p.parseExpression(precLowest)
		assigns = append(assigns, UpdateAssignment{Column: colName, Value: valExpr})

		if p.curTokenIs(TokenComma) {
			p.nextToken()
		} else {
			break
		}
	}

	var whereCol string
	var whereVal Expr
	if p.curTokenIs(TokenWhere) {
		p.nextToken()
		if !p.curTokenIs(TokenIdent) {
			return nil, fmt.Errorf("expected column in UPDATE WHERE, got %s", p.curToken.Literal)
		}
		whereCol = p.curToken.Literal
		p.nextToken()

		if !p.curTokenIs(TokenEq) {
			return nil, fmt.Errorf("expected = in UPDATE WHERE, got %s", p.curToken.Literal)
		}
		p.nextToken()

		whereVal = p.parseExpression(precLowest)
	}

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &UpdateStmt{
		Table:       tblName,
		Assignments: assigns,
		WhereCol:    whereCol,
		WhereVal:    whereVal,
	}, nil
}

func (p *Parser) parseDelete() (Statement, error) {
	p.nextToken() // eat DELETE
	if p.curTokenIs(TokenFrom) {
		p.nextToken()
	}
	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected table name in DELETE, got %s", p.curToken.Literal)
	}
	tblName := p.curToken.Literal
	p.nextToken()

	var whereCol string
	var whereVal Expr
	if p.curTokenIs(TokenWhere) {
		p.nextToken()
		if !p.curTokenIs(TokenIdent) {
			return nil, fmt.Errorf("expected column in DELETE WHERE, got %s", p.curToken.Literal)
		}
		whereCol = p.curToken.Literal
		p.nextToken()

		if !p.curTokenIs(TokenEq) {
			return nil, fmt.Errorf("expected = in DELETE WHERE, got %s", p.curToken.Literal)
		}
		p.nextToken()

		whereVal = p.parseExpression(precLowest)
	}

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &DeleteStmt{
		Table:    tblName,
		WhereCol: whereCol,
		WhereVal: whereVal,
	}, nil
}

func (p *Parser) parseExec() (Statement, error) {
	p.nextToken() // eat EXEC
	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected procedure name in EXEC, got %s", p.curToken.Literal)
	}
	name := p.curToken.Literal
	p.nextToken()

	var args []Expr
	for !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEnd) && !p.curTokenIs(TokenEOF) {
		arg := p.parseExpression(precLowest)
		if arg != nil {
			args = append(args, arg)
		}
		if p.curTokenIs(TokenComma) {
			p.nextToken()
		} else {
			break
		}
	}

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &ExecStmt{ProcName: name, Args: args}, nil
}

// Pratt expression parsing
func (p *Parser) parseExpression(precedence int) Expr {
	prefix := p.parsePrefix()
	if prefix == nil {
		return nil
	}

	leftExp := prefix

	for !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenComma) && !p.curTokenIs(TokenRParen) && precedence < p.curPrecedence() {
		op := p.curToken.Type
		p.nextToken()
		rightExp := p.parseExpression(precedences[op])
		leftExp = &BinaryExpr{
			Left:  leftExp,
			Op:    op,
			Right: rightExp,
		}
	}

	return leftExp
}

func (p *Parser) curPrecedence() int {
	if p, ok := precedences[p.curToken.Type]; ok {
		return p
	}
	return precLowest
}

func (p *Parser) parsePrefix() Expr {
	switch p.curToken.Type {
	case TokenInt:
		val, _ := strconv.ParseInt(p.curToken.Literal, 10, 64)
		expr := &LiteralExpr{Val: storage.IntValue(val)}
		p.nextToken()
		return expr
	case TokenFloat:
		val, _ := strconv.ParseFloat(p.curToken.Literal, 64)
		expr := &LiteralExpr{Val: storage.FloatValue(val)}
		p.nextToken()
		return expr
	case TokenString:
		expr := &LiteralExpr{Val: storage.StringValue(p.curToken.Literal)}
		p.nextToken()
		return expr
	case TokenBool:
		val := strings.ToUpper(p.curToken.Literal) == "TRUE"
		expr := &LiteralExpr{Val: storage.BoolValue(val)}
		p.nextToken()
		return expr
	case TokenVariable:
		expr := &VariableExpr{Name: p.curToken.Literal}
		p.nextToken()
		return expr
	case TokenIdent:
		expr := &ColumnRefExpr{Name: p.curToken.Literal}
		p.nextToken()
		return expr
	case TokenMinus:
		p.nextToken()
		right := p.parseExpression(precPrefix)
		return &UnaryExpr{Op: TokenMinus, Expr: right}
	case TokenNot:
		p.nextToken()
		right := p.parseExpression(precPrefix)
		return &UnaryExpr{Op: TokenNot, Expr: right}
	case TokenLParen:
		p.nextToken()
		exp := p.parseExpression(precLowest)
		if !p.curTokenIs(TokenRParen) {
			p.peekError(TokenRParen)
			return nil
		}
		p.nextToken()
		return exp
	default:
		return nil
	}
}

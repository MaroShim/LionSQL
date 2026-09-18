package proc

import (
	"fmt"
	"strconv"
	"strings"

	"sqlion/pkg/storage"
)

// Precedence levels for Pratt parsing
const (
	_ int = iota
	precLowest
	precOr
	precAnd
	precNot
	precEquals      // = != <> IN BETWEEN LIKE IS
	precLessGreater // < > <= >=
	precSum         // + -
	precProduct     // * /
	precPrefix      // -X, NOT X
)

var precedences = map[TokenType]int{
	TokenOr:       precOr,
	TokenAnd:      precAnd,
	TokenIn:       precEquals,
	TokenBetween:  precEquals,
	TokenLike:     precEquals,
	TokenIs:       precEquals,
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

		// Skip optional (length) e.g. VARCHAR(50)
		if p.curTokenIs(TokenLParen) {
			p.nextToken()
			if p.curTokenIs(TokenInt) {
				p.nextToken()
			}
			if p.curTokenIs(TokenRParen) {
				p.nextToken()
			}
		}

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
		if p.peekTokenIs(TokenTry) {
			return p.parseTryCatch()
		}
		if p.peekTokenIs(TokenTransaction) || p.peekTokenIs(TokenTran) {
			return p.parseTx()
		}
		return p.parseBlock()
	case TokenDeclare:
		return p.parseDeclare()
	case TokenOpen:
		return p.parseOpen()
	case TokenFetch:
		return p.parseFetch()
	case TokenClose:
		return p.parseClose()
	case TokenDeallocate:
		return p.parseDeallocate()
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
	case TokenThrow:
		return p.parseThrow()
	case TokenBreak:
		p.nextToken()
		if p.curTokenIs(TokenSemicolon) {
			p.nextToken()
		}
		return &BreakStmt{}, nil
	case TokenContinue:
		p.nextToken()
		if p.curTokenIs(TokenSemicolon) {
			p.nextToken()
		}
		return &ContinueStmt{}, nil
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

func (p *Parser) parseOpen() (Statement, error) {
	p.nextToken() // eat OPEN
	curName := p.curToken.Literal
	p.nextToken()
	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}
	return &OpenCursorStmt{Name: curName}, nil
}

func (p *Parser) parseClose() (Statement, error) {
	p.nextToken() // eat CLOSE
	curName := p.curToken.Literal
	p.nextToken()
	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}
	return &CloseCursorStmt{Name: curName}, nil
}

func (p *Parser) parseDeallocate() (Statement, error) {
	p.nextToken() // eat DEALLOCATE
	curName := p.curToken.Literal
	p.nextToken()
	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}
	return &DeallocateCursorStmt{Name: curName}, nil
}

func (p *Parser) parseFetch() (Statement, error) {
	p.nextToken() // eat FETCH
	if p.curTokenIs(TokenNext) {
		p.nextToken()
	}
	if p.curTokenIs(TokenFrom) {
		p.nextToken()
	}
	curName := p.curToken.Literal
	p.nextToken()

	if !p.curTokenIs(TokenInto) {
		return nil, fmt.Errorf("expected INTO in FETCH, got %s", p.curToken.Literal)
	}
	p.nextToken()

	intoVars := make([]string, 0)
	for p.curTokenIs(TokenVariable) {
		intoVars = append(intoVars, p.curToken.Literal)
		p.nextToken()
		if p.curTokenIs(TokenComma) {
			p.nextToken()
		} else {
			break
		}
	}

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &FetchNextStmt{CursorName: curName, IntoVars: intoVars}, nil
}

func (p *Parser) parseTryCatch() (Statement, error) {
	p.nextToken() // eat BEGIN
	p.nextToken() // eat TRY

	var tryStmts []Statement
	for !p.curTokenIs(TokenEnd) && !p.curTokenIs(TokenEOF) {
		stmt, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		if stmt != nil {
			tryStmts = append(tryStmts, stmt)
		}
		if p.curTokenIs(TokenSemicolon) {
			p.nextToken()
		}
	}

	if !p.curTokenIs(TokenEnd) {
		return nil, fmt.Errorf("expected END TRY, got %s", p.curToken.Literal)
	}
	p.nextToken() // eat END
	if !p.curTokenIs(TokenTry) {
		return nil, fmt.Errorf("expected TRY after END, got %s", p.curToken.Literal)
	}
	p.nextToken() // eat TRY

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	if !p.curTokenIs(TokenBegin) {
		return nil, fmt.Errorf("expected BEGIN CATCH, got %s", p.curToken.Literal)
	}
	p.nextToken() // eat BEGIN
	if !p.curTokenIs(TokenCatch) {
		return nil, fmt.Errorf("expected CATCH after BEGIN, got %s", p.curToken.Literal)
	}
	p.nextToken() // eat CATCH

	var catchStmts []Statement
	for !p.curTokenIs(TokenEnd) && !p.curTokenIs(TokenEOF) {
		stmt, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		if stmt != nil {
			catchStmts = append(catchStmts, stmt)
		}
		if p.curTokenIs(TokenSemicolon) {
			p.nextToken()
		}
	}

	if !p.curTokenIs(TokenEnd) {
		return nil, fmt.Errorf("expected END CATCH, got %s", p.curToken.Literal)
	}
	p.nextToken() // eat END
	if !p.curTokenIs(TokenCatch) {
		return nil, fmt.Errorf("expected CATCH after END, got %s", p.curToken.Literal)
	}
	p.nextToken() // eat CATCH

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &TryCatchStmt{
		TryBody:   &BlockStmt{Statements: tryStmts},
		CatchBody: &BlockStmt{Statements: catchStmts},
	}, nil
}

func (p *Parser) parseThrow() (Statement, error) {
	p.nextToken() // eat THROW
	msg := "An error occurred"

	if p.curTokenIs(TokenInt) {
		p.nextToken() // eat errNum
		if p.curTokenIs(TokenComma) {
			p.nextToken() // eat comma
			if p.curTokenIs(TokenString) {
				msg = p.curToken.Literal
				p.nextToken()
			}
			if p.curTokenIs(TokenComma) {
				p.nextToken()
				if p.curTokenIs(TokenInt) {
					p.nextToken()
				}
			}
		}
	} else if p.curTokenIs(TokenString) {
		msg = p.curToken.Literal
		p.nextToken()
	}

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &ThrowStmt{Message: msg}, nil
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

	// Check if this is DECLARE @name / name CURSOR FOR SELECT ...
	name := p.curToken.Literal
	p.nextToken()

	if p.curTokenIs(TokenCursor) {
		p.nextToken() // eat CURSOR
		if !p.curTokenIs(TokenFor) {
			return nil, fmt.Errorf("expected FOR after CURSOR, got %s", p.curToken.Literal)
		}
		p.nextToken() // eat FOR

		if !p.curTokenIs(TokenSelect) {
			return nil, fmt.Errorf("expected SELECT in CURSOR FOR, got %s", p.curToken.Literal)
		}
		p.nextToken() // eat SELECT

		cols := make([]string, 0)
		for !p.curTokenIs(TokenFrom) && !p.curTokenIs(TokenEOF) {
			if p.curTokenIs(TokenIdent) || p.curTokenIs(TokenAsterisk) {
				cols = append(cols, p.curToken.Literal)
				p.nextToken()
			}
			if p.curTokenIs(TokenComma) {
				p.nextToken()
			}
		}

		if !p.curTokenIs(TokenFrom) {
			return nil, fmt.Errorf("expected FROM in cursor query")
		}
		p.nextToken() // eat FROM

		tblName := p.curToken.Literal
		p.nextToken()

		var whereExpr Expr
		if p.curTokenIs(TokenWhere) {
			p.nextToken()
			whereExpr = p.parseExpression(precLowest)
		}

		if p.curTokenIs(TokenSemicolon) {
			p.nextToken()
		}

		return &DeclareCursorStmt{
			Name:    name,
			Table:   tblName,
			Columns: cols,
			Where:   whereExpr,
		}, nil
	}

	// Normal variable declaration
	var stmts []Statement
	varName := name
	for {
		if !p.curTokenIs(TokenIdent) {
			return nil, fmt.Errorf("expected data type for %s, got %s", varName, p.curToken.Literal)
		}
		dt := parseDataType(p.curToken.Literal)
		p.nextToken()

		// Skip optional (length) e.g. VARCHAR(50)
		if p.curTokenIs(TokenLParen) {
			p.nextToken()
			if p.curTokenIs(TokenInt) {
				p.nextToken()
			}
			if p.curTokenIs(TokenRParen) {
				p.nextToken()
			}
		}

		stmts = append(stmts, &DeclareStmt{Name: varName, Type: dt})

		if p.curTokenIs(TokenComma) {
			p.nextToken()
			if !p.curTokenIs(TokenVariable) {
				return nil, fmt.Errorf("expected variable name after comma in DECLARE")
			}
			varName = p.curToken.Literal
			p.nextToken()
		} else {
			break
		}
	}

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	if len(stmts) == 1 {
		return stmts[0], nil
	}
	return &BlockStmt{Statements: stmts}, nil
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
		if p.curTokenIs(TokenIdent) && p.peekTokenIs(TokenEq) {
			whereCol = p.curToken.Literal
			p.nextToken()
			p.nextToken() // eat =
			whereVal = p.parseExpression(precLowest)
		} else {
			whereVal = p.parseExpression(precLowest)
		}
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

	var whereExpr Expr
	if p.curTokenIs(TokenWhere) {
		p.nextToken()
		whereExpr = p.parseExpression(precLowest)
	}

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	// Check if this is simple single-column PK equality
	if bin, ok := whereExpr.(*BinaryExpr); ok && bin.Op == TokenEq {
		if colRef, isCol := bin.Left.(*ColumnRefExpr); isCol && strings.EqualFold(colRef.Name, "id") {
			return &UpdateStmt{
				Table:       tblName,
				Assignments: assigns,
				WhereCol:    colRef.Name,
				WhereVal:    bin.Right,
			}, nil
		}
	}

	return &UpdateWhereStmt{
		Table:       tblName,
		Assignments: assigns,
		Where:       whereExpr,
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

	var whereExpr Expr
	if p.curTokenIs(TokenWhere) {
		p.nextToken()
		whereExpr = p.parseExpression(precLowest)
	}

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	if bin, ok := whereExpr.(*BinaryExpr); ok && bin.Op == TokenEq {
		if colRef, isCol := bin.Left.(*ColumnRefExpr); isCol && strings.EqualFold(colRef.Name, "id") {
			return &DeleteStmt{
				Table:    tblName,
				WhereCol: colRef.Name,
				WhereVal: bin.Right,
			}, nil
		}
	}

	return &DeleteWhereStmt{
		Table: tblName,
		Where: whereExpr,
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
		switch p.curToken.Type {
		case TokenIn:
			p.nextToken() // eat IN
			if !p.curTokenIs(TokenLParen) {
				return nil
			}
			p.nextToken() // eat (
			var list []Expr
			for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
				item := p.parseExpression(precLowest)
				if item != nil {
					list = append(list, item)
				}
				if p.curTokenIs(TokenComma) {
					p.nextToken()
				} else {
					break
				}
			}
			if !p.curTokenIs(TokenRParen) {
				return nil
			}
			p.nextToken() // eat )
			leftExp = &InExpr{Expr: leftExp, Not: false, List: list}

		case TokenBetween:
			p.nextToken() // eat BETWEEN
			low := p.parseExpression(precSum)
			if !p.curTokenIs(TokenAnd) {
				return nil
			}
			p.nextToken() // eat AND
			high := p.parseExpression(precSum)
			leftExp = &BetweenExpr{Expr: leftExp, Not: false, Low: low, High: high}

		case TokenLike:
			p.nextToken() // eat LIKE
			pattern := p.parseExpression(precSum)
			leftExp = &LikeExpr{Expr: leftExp, Not: false, Pattern: pattern}

		case TokenIs:
			p.nextToken() // eat IS
			isNot := false
			if p.curTokenIs(TokenNot) {
				isNot = true
				p.nextToken() // eat NOT
			}
			if !p.curTokenIs(TokenNull) {
				return nil
			}
			p.nextToken() // eat NULL
			leftExp = &IsNullExpr{Expr: leftExp, Not: isNot}

		default:
			op := p.curToken.Type
			p.nextToken()
			rightExp := p.parseExpression(precedences[op])
			leftExp = &BinaryExpr{
				Left:  leftExp,
				Op:    op,
				Right: rightExp,
			}
		}
	}

	return leftExp
}

func (p *Parser) curPrecedence() int {
	if prec, ok := precedences[p.curToken.Type]; ok {
		return prec
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
	case TokenNull:
		expr := &LiteralExpr{Val: storage.NullValue(storage.TypeUnknown)}
		p.nextToken()
		return expr
	case TokenCase:
		return p.parseCase()
	case TokenIsNull, TokenCoalesce:
		funcName := p.curToken.Literal
		p.nextToken()
		if p.curTokenIs(TokenLParen) {
			return p.parseFunctionCall(funcName)
		}
		return &ColumnRefExpr{Name: funcName}
	case TokenVariable:
		expr := &VariableExpr{Name: p.curToken.Literal}
		p.nextToken()
		return expr
	case TokenIdent:
		ident := p.curToken.Literal
		p.nextToken()
		if p.curTokenIs(TokenLParen) {
			return p.parseFunctionCall(ident)
		}
		return &ColumnRefExpr{Name: ident}
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
		// Check for scalar subquery (SELECT col FROM tbl WHERE ...)
		if p.curTokenIs(TokenSelect) {
			p.nextToken() // eat SELECT
			col := p.curToken.Literal
			p.nextToken()
			if !p.curTokenIs(TokenFrom) {
				return nil
			}
			p.nextToken() // eat FROM
			tbl := p.curToken.Literal
			p.nextToken()
			var where Expr
			if p.curTokenIs(TokenWhere) {
				p.nextToken()
				where = p.parseExpression(precLowest)
			}
			if !p.curTokenIs(TokenRParen) {
				return nil
			}
			p.nextToken() // eat )
			return &ScalarSubqueryExpr{Column: col, Table: tbl, Where: where}
		}

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

func (p *Parser) parseCase() Expr {
	p.nextToken() // eat CASE
	var whens []CaseWhen

	for p.curTokenIs(TokenWhen) {
		p.nextToken() // eat WHEN
		cond := p.parseExpression(precLowest)
		if cond == nil || !p.curTokenIs(TokenThen) {
			return nil
		}
		p.nextToken() // eat THEN
		res := p.parseExpression(precLowest)
		if res == nil {
			return nil
		}
		whens = append(whens, CaseWhen{Condition: cond, Result: res})
	}

	var elseExpr Expr
	if p.curTokenIs(TokenElse) {
		p.nextToken() // eat ELSE
		elseExpr = p.parseExpression(precLowest)
	}

	if !p.curTokenIs(TokenEnd) {
		return nil
	}
	p.nextToken() // eat END

	return &CaseExpr{
		Whens: whens,
		Else:  elseExpr,
	}
}

func (p *Parser) parseFunctionCall(name string) Expr {
	p.nextToken() // eat (
	var args []Expr

	for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
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

	if !p.curTokenIs(TokenRParen) {
		return nil
	}
	p.nextToken() // eat )

	return &FunctionCallExpr{
		Name: strings.ToUpper(name),
		Args: args,
	}
}

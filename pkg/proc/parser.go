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
	precJSON        // -> ->>
	precPrefix      // -X, NOT X
)

var precedences = map[TokenType]int{
	TokenOr:              precOr,
	TokenAnd:             precAnd,
	TokenNot:             precEquals,
	TokenIn:              precEquals,
	TokenBetween:         precEquals,
	TokenLike:            precEquals,
	TokenIs:              precEquals,
	TokenEq:              precEquals,
	TokenNeq:             precEquals,
	TokenLt:              precLessGreater,
	TokenLte:             precLessGreater,
	TokenGt:              precLessGreater,
	TokenGte:             precLessGreater,
	TokenPlus:            precSum,
	TokenMinus:           precSum,
	TokenAsterisk:        precProduct,
	TokenSlash:           precProduct,
	TokenJSONExtract:     precJSON,
	TokenJSONExtractText: precJSON,
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

// ParseExpr parses an expression starting at lowest precedence.
func (p *Parser) ParseExpr() Expr {
	return p.parseExpression(precLowest)
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

// Parse parses a CREATE PROCEDURE script.
func Parse(sql string) (*CreateProcStmt, error) {
	p := NewParser(NewLexer(sql))
	return p.ParseProcedure()
}

// ParseFunction parses a CREATE FUNCTION script.
func ParseFunction(sql string) (*CreateFunctionStmt, error) {
	p := NewParser(NewLexer(sql))
	return p.ParseFunction()
}

// ParseTrigger parses a CREATE TRIGGER script.
func ParseTrigger(sql string) (*CreateTriggerStmt, error) {
	p := NewParser(NewLexer(sql))
	return p.ParseTrigger()
}

// ParseScript parses any CREATE PROCEDURE, CREATE FUNCTION, or CREATE TRIGGER script.
func ParseScript(sql string) (Node, error) {
	p := NewParser(NewLexer(sql))
	return p.ParseScript()
}

// ParseScript parses any CREATE PROCEDURE, CREATE FUNCTION, or CREATE TRIGGER script.
func (p *Parser) ParseScript() (Node, error) {
	if p.curTokenIs(TokenCreate) {
		if p.peekTokenIs(TokenOr) {
			return p.ParseProcedure()
		}
		switch p.peekToken.Type {
		case TokenProcedure, TokenProc:
			return p.ParseProcedure()
		case TokenFunction:
			return p.ParseFunction()
		case TokenTrigger:
			return p.ParseTrigger()
		default:
			return nil, fmt.Errorf("expected PROCEDURE, FUNCTION, or TRIGGER after CREATE, got %s", p.peekToken.Literal)
		}
	} else if p.curTokenIs(TokenAlter) {
		switch p.peekToken.Type {
		case TokenProcedure, TokenProc:
			return p.ParseProcedure()
		case TokenFunction:
			return p.ParseFunction()
		case TokenTrigger:
			return p.ParseTrigger()
		default:
			return nil, fmt.Errorf("expected PROCEDURE, FUNCTION, or TRIGGER after ALTER, got %s", p.peekToken.Literal)
		}
	}
	return nil, fmt.Errorf("expected CREATE or ALTER, got %s", p.curToken.Literal)
}

// ParseProcedure parses a full CREATE PROCEDURE script.
func (p *Parser) ParseProcedure() (*CreateProcStmt, error) {
	isAlter := false
	isOrAlter := false

	if p.curTokenIs(TokenCreate) {
		p.nextToken()
		// Check for CREATE OR ALTER
		if p.curTokenIs(TokenOr) {
			p.nextToken()
			if p.curTokenIs(TokenAlter) {
				isOrAlter = true
				p.nextToken()
			} else {
				return nil, fmt.Errorf("expected ALTER after CREATE OR, got %s", p.curToken.Literal)
			}
		}
	} else if p.curTokenIs(TokenAlter) {
		isAlter = true
		p.nextToken()
	} else {
		return nil, fmt.Errorf("expected CREATE or ALTER, got %s", p.curToken.Literal)
	}

	if !p.curTokenIs(TokenProcedure) && !p.curTokenIs(TokenProc) {
		return nil, fmt.Errorf("expected PROCEDURE or PROC, got %s", p.curToken.Literal)
	}
	p.nextToken()

	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected procedure name, got %s", p.curToken.Literal)
	}
	procName := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken()

	params := make([]ParamDef, 0)
	hasParen := false
	if p.curTokenIs(TokenLParen) {
		hasParen = true
		p.nextToken()
	}

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

	if hasParen {
		if !p.curTokenIs(TokenRParen) {
			return nil, fmt.Errorf("expected ) after procedure parameters, got %s", p.curToken.Literal)
		}
		p.nextToken()
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
		Name:    procName,
		Params:  params,
		Body:    body,
		OrAlter: isOrAlter,
		IsAlter: isAlter,
	}, nil
}

// ParseFunction parses a full CREATE FUNCTION script.
func (p *Parser) ParseFunction() (*CreateFunctionStmt, error) {
	if !p.curTokenIs(TokenCreate) {
		return nil, fmt.Errorf("expected CREATE, got %s", p.curToken.Literal)
	}
	p.nextToken()

	if !p.curTokenIs(TokenFunction) {
		return nil, fmt.Errorf("expected FUNCTION, got %s", p.curToken.Literal)
	}
	p.nextToken()

	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected function name, got %s", p.curToken.Literal)
	}
	funcName := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken()

	params := make([]ParamDef, 0)
	hasParen := false
	if p.curTokenIs(TokenLParen) {
		hasParen = true
		p.nextToken()
	}

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

	if hasParen {
		if !p.curTokenIs(TokenRParen) {
			return nil, fmt.Errorf("expected ) after function parameters, got %s", p.curToken.Literal)
		}
		p.nextToken()
	}

	if !p.curTokenIs(TokenReturns) {
		return nil, fmt.Errorf("expected RETURNS, got %s", p.curToken.Literal)
	}
	p.nextToken()

	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected return data type after RETURNS, got %s", p.curToken.Literal)
	}
	retType := parseDataType(p.curToken.Literal)
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

	if !p.curTokenIs(TokenAs) {
		return nil, fmt.Errorf("expected AS, got %s", p.curToken.Literal)
	}
	p.nextToken()

	body, err := p.parseStatement()
	if err != nil {
		return nil, err
	}

	return &CreateFunctionStmt{
		Name:       funcName,
		Params:     params,
		ReturnType: retType,
		Body:       body,
	}, nil
}

// ParseTrigger parses a full CREATE TRIGGER script.
func (p *Parser) ParseTrigger() (*CreateTriggerStmt, error) {
	if !p.curTokenIs(TokenCreate) {
		return nil, fmt.Errorf("expected CREATE, got %s", p.curToken.Literal)
	}
	p.nextToken()

	if !p.curTokenIs(TokenTrigger) {
		return nil, fmt.Errorf("expected TRIGGER, got %s", p.curToken.Literal)
	}
	p.nextToken()

	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected trigger name, got %s", p.curToken.Literal)
	}
	trgName := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken()

	if !p.curTokenIs(TokenOn) {
		return nil, fmt.Errorf("expected ON after trigger name, got %s", p.curToken.Literal)
	}
	p.nextToken()

	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected table name in ON clause, got %s", p.curToken.Literal)
	}
	tableName := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken()

	timing := "AFTER"
	if p.curTokenIs(TokenAfter) {
		timing = "AFTER"
		p.nextToken()
	} else if p.curTokenIs(TokenInstead) {
		p.nextToken()
		if !p.curTokenIs(TokenOf) {
			return nil, fmt.Errorf("expected OF after INSTEAD, got %s", p.curToken.Literal)
		}
		timing = "INSTEAD OF"
		p.nextToken()
	}

	events := make([]string, 0)
	for {
		switch p.curToken.Type {
		case TokenInsert:
			events = append(events, "INSERT")
			p.nextToken()
		case TokenUpdate:
			events = append(events, "UPDATE")
			p.nextToken()
		case TokenDelete:
			events = append(events, "DELETE")
			p.nextToken()
		}

		if p.curTokenIs(TokenComma) {
			p.nextToken()
		} else {
			break
		}
	}

	if len(events) == 0 {
		return nil, fmt.Errorf("expected at least one event (INSERT, UPDATE, DELETE) in trigger")
	}

	if !p.curTokenIs(TokenAs) {
		return nil, fmt.Errorf("expected AS in trigger, got %s", p.curToken.Literal)
	}
	p.nextToken()

	body, err := p.parseStatement()
	if err != nil {
		return nil, err
	}

	return &CreateTriggerStmt{
		Name:   trgName,
		Table:  tableName,
		Timing: timing,
		Events: events,
		Body:   body,
	}, nil
}

func cleanSchemaPrefix(name string) string {
	s := strings.TrimSpace(name)
	parts := strings.Split(s, ".")
	for i, p := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(p), "[]\"'`")
	}
	cleaned := strings.Join(parts, ".")
	lower := strings.ToLower(cleaned)
	if strings.HasPrefix(lower, "dbo.") {
		cleaned = cleaned[4:]
	} else if strings.HasPrefix(lower, "public.") {
		cleaned = cleaned[7:]
	}
	return cleaned
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
	case "DATE":
		return storage.TypeDate
	case "TIME":
		return storage.TypeTime
	case "UUID", "UNIQUEIDENTIFIER":
		return storage.TypeUUID
	case "VARBINARY", "BINARY", "BYTEA":
		return storage.TypeBinary
	default:
		return storage.TypeVarChar
	}
}

// ParseStatement parses a single statement from the current token position.
func (p *Parser) ParseStatement() (Statement, error) {
	return p.parseStatement()
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
	case TokenCommit, TokenRollback, TokenSave, TokenSavepoint:
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
	case TokenGoto:
		return p.parseGoto()
	case TokenIdent:
		if p.peekTokenIs(TokenColon) {
			label := p.curToken.Literal
			p.nextToken() // move to ':'
			p.nextToken() // eat ':'
			return &LabelStmt{Label: label}, nil
		}
		return nil, fmt.Errorf("unexpected token in statement: %s at %d:%d",
			p.curToken.Literal, p.curToken.Line, p.curToken.Col)
	default:
		return nil, fmt.Errorf("unexpected token in statement: %s at %d:%d",
			p.curToken.Literal, p.curToken.Line, p.curToken.Col)
	}
}

func (p *Parser) parseGoto() (Statement, error) {
	p.nextToken() // eat GOTO
	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected label name after GOTO, got %s at %d:%d", p.curToken.Literal, p.curToken.Line, p.curToken.Col)
	}
	label := p.curToken.Literal
	p.nextToken()
	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}
	return &GotoStmt{Label: label}, nil
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

	// 1. Variable assignment: SET @var = expr
	if p.curTokenIs(TokenVariable) {
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

	// 2. T-SQL Session Options: SET <option> [ON | OFF | value]
	options := make(map[string]string)
	for {
		if p.curTokenIs(TokenSemicolon) || p.curTokenIs(TokenEOF) {
			break
		}
		optName := p.curToken.Literal
		p.nextToken()

		var optVal string = "ON"
		if p.curTokenIs(TokenEq) || p.curTokenIs(TokenAssign) {
			p.nextToken()
			optVal = p.curToken.Literal
			p.nextToken()
		} else if strings.EqualFold(p.curToken.Literal, "ON") || strings.EqualFold(p.curToken.Literal, "OFF") {
			optVal = strings.ToUpper(p.curToken.Literal)
			p.nextToken()
		} else if !p.curTokenIs(TokenComma) && !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
			optVal = p.curToken.Literal
			p.nextToken()
		}
		options[strings.ToUpper(optName)] = optVal

		if p.curTokenIs(TokenComma) {
			p.nextToken()
			continue
		}
		break
	}

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &SetOptionStmt{Options: options}, nil
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
	case TokenSave, TokenSavepoint:
		isSavepointKw := p.curTokenIs(TokenSavepoint)
		p.nextToken() // eat SAVE / SAVEPOINT
		if !isSavepointKw && (p.curTokenIs(TokenTran) || p.curTokenIs(TokenTransaction)) {
			p.nextToken() // eat TRAN / TRANSACTION
		}
		if !p.curTokenIs(TokenIdent) {
			return nil, fmt.Errorf("expected savepoint name, got %s", p.curToken.Literal)
		}
		spName := p.curToken.Literal
		p.nextToken()
		if p.curTokenIs(TokenSemicolon) {
			p.nextToken()
		}
		return &TxStmt{Action: TxSavepoint, Name: spName}, nil
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
		var spName string
		if p.curTokenIs(TokenIdent) {
			spName = p.curToken.Literal
			p.nextToken()
		}
		if p.curTokenIs(TokenSemicolon) {
			p.nextToken()
		}
		if spName != "" && !strings.EqualFold(spName, "WORK") {
			return &TxStmt{Action: TxRollbackTo, Name: spName}, nil
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

	// Check if this is simple single-column equality
	if bin, ok := whereExpr.(*BinaryExpr); ok && bin.Op == TokenEq {
		if colRef, isCol := bin.Left.(*ColumnRefExpr); isCol {
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
		if colRef, isCol := bin.Left.(*ColumnRefExpr); isCol {
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
	if p.curTokenIs(TokenLParen) {
		p.nextToken() // eat '('
		expr := p.parseExpression(precLowest)
		if expr == nil {
			return nil, fmt.Errorf("expected expression in dynamic EXEC")
		}
		if !p.curTokenIs(TokenRParen) {
			return nil, fmt.Errorf("expected ')' after dynamic EXEC expression, got %s", p.curToken.Literal)
		}
		p.nextToken() // eat ')'
		if p.curTokenIs(TokenSemicolon) {
			p.nextToken()
		}
		return &DynamicExecStmt{Expr: expr}, nil
	}
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
			if p.curTokenIs(TokenSelect) {
				p.nextToken() // eat SELECT
				sel, err := p.parseGeneralSelect()
				if err != nil {
					return nil
				}
				if !p.curTokenIs(TokenRParen) {
					return nil
				}
				p.nextToken() // eat )
				leftExp = &InExpr{Expr: leftExp, Not: false, Query: sel}
				break
			}
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

		case TokenNot:
			p.nextToken() // eat NOT
			if p.curTokenIs(TokenLike) {
				p.nextToken() // eat LIKE
				pattern := p.parseExpression(precSum)
				leftExp = &LikeExpr{Expr: leftExp, Not: true, Pattern: pattern}
				break
			}
			if p.curTokenIs(TokenBetween) {
				p.nextToken() // eat BETWEEN
				low := p.parseExpression(precSum)
				if !p.curTokenIs(TokenAnd) {
					return nil
				}
				p.nextToken() // eat AND
				high := p.parseExpression(precSum)
				leftExp = &BetweenExpr{Expr: leftExp, Not: true, Low: low, High: high}
				break
			}
			if p.curTokenIs(TokenIn) {
				p.nextToken() // eat IN
				if !p.curTokenIs(TokenLParen) {
					return nil
				}
				p.nextToken() // eat (
				if p.curTokenIs(TokenSelect) {
					p.nextToken() // eat SELECT
					sel, err := p.parseGeneralSelect()
					if err != nil {
						return nil
					}
					if !p.curTokenIs(TokenRParen) {
						return nil
					}
					p.nextToken() // eat )
					leftExp = &InExpr{Expr: leftExp, Not: true, Query: sel}
					break
				}
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
				leftExp = &InExpr{Expr: leftExp, Not: true, List: list}
				break
			}
			rightExp := p.parseExpression(precedences[TokenNot])
			leftExp = &BinaryExpr{
				Left:  leftExp,
				Op:    TokenNot,
				Right: rightExp,
			}

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
	case TokenAsterisk:
		p.nextToken()
		return &ColumnRefExpr{Name: "*"}
	case TokenCount, TokenSum, TokenAvg, TokenMin, TokenMax, TokenLeft, TokenRight:
		funcName := p.curToken.Literal
		p.nextToken()
		if p.curTokenIs(TokenLParen) {
			return p.parseFunctionCall(funcName)
		}
		return &ColumnRefExpr{Name: funcName}
	case TokenExists:
		p.nextToken() // eat EXISTS
		if !p.curTokenIs(TokenLParen) {
			return nil
		}
		p.nextToken() // eat (
		if !p.curTokenIs(TokenSelect) {
			return nil
		}
		p.nextToken() // eat SELECT
		sel, err := p.parseGeneralSelect()
		if err != nil {
			return nil
		}
		if !p.curTokenIs(TokenRParen) {
			return nil
		}
		p.nextToken() // eat )
		return &ExistsExpr{Query: sel, Not: false}
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
		p.nextToken() // eat NOT
		if p.curTokenIs(TokenExists) {
			p.nextToken() // eat EXISTS
			if !p.curTokenIs(TokenLParen) {
				return nil
			}
			p.nextToken() // eat (
			if !p.curTokenIs(TokenSelect) {
				return nil
			}
			p.nextToken() // eat SELECT
			sel, err := p.parseGeneralSelect()
			if err != nil {
				return nil
			}
			if !p.curTokenIs(TokenRParen) {
				return nil
			}
			p.nextToken() // eat )
			return &ExistsExpr{Query: sel, Not: true}
		}
		right := p.parseExpression(precPrefix)
		return &UnaryExpr{Op: TokenNot, Expr: right}
	case TokenLParen:
		p.nextToken()
		// Check for scalar subquery (SELECT ...)
		if p.curTokenIs(TokenSelect) {
			p.nextToken() // eat SELECT
			sel, err := p.parseGeneralSelect()
			if err == nil && sel != nil && p.curTokenIs(TokenRParen) {
				p.nextToken() // eat )
				sub := &ScalarSubqueryExpr{
					Query: sel,
					Table: sel.From.Name,
					Where: sel.Where,
				}
				if len(sel.Projections) > 0 {
					sub.Column = sel.Projections[0].String()
				}
				return sub
			}
			return nil
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

	fn := &FunctionCallExpr{
		Name: name,
		Args: args,
	}

	if p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "WITHIN") {
		p.nextToken() // eat WITHIN
		if p.curTokenIs(TokenGroup) || (p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "GROUP")) {
			p.nextToken() // eat GROUP
			if p.curTokenIs(TokenLParen) {
				p.nextToken() // eat (
				if p.curTokenIs(TokenOrder) || (p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "ORDER")) {
					p.nextToken() // eat ORDER
					if p.curTokenIs(TokenBy) || (p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "BY")) {
						p.nextToken() // eat BY
						orderExpr := p.parseExpression(precLowest)
						isDesc := false
						if p.curTokenIs(TokenDesc) {
							isDesc = true
							p.nextToken()
						} else if p.curTokenIs(TokenAsc) {
							p.nextToken()
						}
						if p.curTokenIs(TokenRParen) {
							p.nextToken() // eat )
						}
						return &WithinGroupExpr{
							Function: fn,
							OrderBy:  orderExpr,
							Desc:     isDesc,
						}
					}
				}
			}
		}
	}

	return fn
}

// ParseSQL parses a single interactive or procedural SQL statement into an AST Statement.
func ParseSQL(sql string) (Statement, error) {
	p := NewParser(NewLexer(sql))
	return p.ParseSQLStatement()
}

// ParseSQLStatement parses any DQL, DML, DDL, or procedural statement.
func (p *Parser) ParseSQLStatement() (Statement, error) {
	switch p.curToken.Type {
	case TokenExplain:
		p.nextToken() // eat EXPLAIN
		isAnalyze := false
		if p.curTokenIs(TokenAnalyze) {
			isAnalyze = true
			p.nextToken()
		}
		var inner Statement
		var err error
		if p.curTokenIs(TokenWith) {
			inner, err = p.parseWith()
		} else if p.curTokenIs(TokenSelect) {
			p.nextToken()
			inner, err = p.parseGeneralSelect()
		} else {
			return nil, fmt.Errorf("expected SELECT or WITH after EXPLAIN")
		}
		if err != nil {
			return nil, err
		}
		if sel, ok := inner.(*GeneralSelectStmt); ok {
			sel.IsExplain = true
			sel.IsAnalyze = isAnalyze
			return sel, nil
		}
		return inner, nil

	case TokenWith:
		return p.parseWith()

	case TokenSelect:
		p.nextToken() // eat SELECT
		return p.parseGeneralSelect()

	case TokenInsert:
		return p.parseGeneralInsert()

	case TokenUpdate:
		return p.parseUpdate()

	case TokenDelete:
		return p.parseDelete()

	case TokenCreate:
		if p.peekTokenIs(TokenTable) {
			return p.parseCreateTable()
		}
		if p.peekTokenIs(TokenIndex) || p.peekTokenIs(TokenUnique) {
			return p.parseCreateIndex()
		}
		if p.peekTokenIs(TokenProcedure) || p.peekTokenIs(TokenProc) {
			return p.ParseProcedure()
		}
		if p.peekTokenIs(TokenFunction) {
			return p.ParseFunction()
		}
		if p.peekTokenIs(TokenTrigger) {
			return p.ParseTrigger()
		}
		return nil, fmt.Errorf("unsupported CREATE statement: %s", p.peekToken.Literal)

	case TokenDrop:
		p.nextToken() // eat DROP
		if p.curTokenIs(TokenTable) {
			return p.parseDropTable()
		}
		if p.curTokenIs(TokenIndex) {
			return p.parseDropIndex()
		}
		return nil, fmt.Errorf("unsupported DROP statement: %s", p.curToken.Literal)

	case TokenBegin:
		if p.peekTokenIs(TokenTry) {
			return p.parseTryCatch()
		}
		if p.peekTokenIs(TokenTransaction) || p.peekTokenIs(TokenTran) || p.peekTokenIs(TokenEOF) || p.peekTokenIs(TokenSemicolon) {
			return p.parseTx()
		}
		return p.parseBlock()

	case TokenCommit, TokenRollback, TokenSave, TokenSavepoint:
		return p.parseTx()

	case TokenSet:
		return p.parseSet()

	case TokenExec:
		return p.parseExec()

	default:
		return nil, fmt.Errorf("unexpected token at statement start: %s", p.curToken.Literal)
	}
}

func (p *Parser) parseGeneralSelect() (*GeneralSelectStmt, error) {
	stmt := &GeneralSelectStmt{Limit: -1}

	if p.curTokenIs(TokenDistinct) {
		stmt.Distinct = true
		p.nextToken() // eat DISTINCT
	}

	// 1. Projections
	for !p.curTokenIs(TokenFrom) && !p.curTokenIs(TokenEOF) && !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenRParen) {
		expr := p.parseExpression(precLowest)
		if expr == nil {
			return nil, fmt.Errorf("expected projection expression, got %s", p.curToken.Literal)
		}
		alias := ""
		if p.curTokenIs(TokenAs) {
			p.nextToken() // eat AS
			if p.curTokenIs(TokenIdent) || p.curTokenIs(TokenString) {
				alias = cleanSchemaPrefix(p.curToken.Literal)
				p.nextToken()
			}
		} else if p.curTokenIs(TokenIdent) && !p.curTokenIs(TokenFrom) && !p.curTokenIs(TokenWhere) && !p.curTokenIs(TokenGroup) && !p.curTokenIs(TokenHaving) && !p.curTokenIs(TokenOrder) && !p.curTokenIs(TokenLimit) && !p.curTokenIs(TokenJoin) && !p.curTokenIs(TokenInner) && !p.curTokenIs(TokenLeft) {
			alias = cleanSchemaPrefix(p.curToken.Literal)
			p.nextToken()
		}

		stmt.Projections = append(stmt.Projections, Projection{Expr: expr, Alias: alias})

		if p.curTokenIs(TokenComma) {
			p.nextToken()
		} else {
			break
		}
	}

	// 2. FROM clause
	if p.curTokenIs(TokenFrom) {
		p.nextToken() // eat FROM
		if !p.curTokenIs(TokenIdent) {
			return nil, fmt.Errorf("expected table name after FROM, got %s", p.curToken.Literal)
		}
		tblName := cleanSchemaPrefix(p.curToken.Literal)
		p.nextToken()

		var tvfArgs []Expr
		isTVF := false
		if p.curTokenIs(TokenLParen) {
			isTVF = true
			p.nextToken() // eat (
			for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
				arg := p.parseExpression(precLowest)
				if arg != nil {
					tvfArgs = append(tvfArgs, arg)
				}
				if p.curTokenIs(TokenComma) {
					p.nextToken()
				} else {
					break
				}
			}
			if !p.curTokenIs(TokenRParen) {
				return nil, fmt.Errorf("expected ) closing TVF arguments")
			}
			p.nextToken() // eat )
		}

		alias := tblName
		if p.curTokenIs(TokenAs) {
			p.nextToken() // eat AS
			if p.curTokenIs(TokenIdent) {
				alias = cleanSchemaPrefix(p.curToken.Literal)
				p.nextToken()
			}
		} else if p.curTokenIs(TokenIdent) && !p.curTokenIs(TokenWhere) && !p.curTokenIs(TokenGroup) && !p.curTokenIs(TokenHaving) && !p.curTokenIs(TokenOrder) && !p.curTokenIs(TokenLimit) && !p.curTokenIs(TokenJoin) && !p.curTokenIs(TokenInner) && !p.curTokenIs(TokenLeft) && !p.curTokenIs(TokenOn) {
			alias = cleanSchemaPrefix(p.curToken.Literal)
			p.nextToken()
		}

		stmt.From = TableRef{Name: tblName, Alias: alias, IsTVF: isTVF, TVFArgs: tvfArgs}
	}

	// 3. JOIN clauses
	for p.curTokenIs(TokenJoin) || p.curTokenIs(TokenInner) || p.curTokenIs(TokenLeft) {
		joinType := "INNER"
		if p.curTokenIs(TokenLeft) {
			joinType = "LEFT"
			p.nextToken() // eat LEFT
			if p.curTokenIs(TokenOuter) {
				p.nextToken() // eat OUTER
			}
		} else if p.curTokenIs(TokenInner) {
			p.nextToken() // eat INNER
		}

		if !p.curTokenIs(TokenJoin) {
			return nil, fmt.Errorf("expected JOIN, got %s", p.curToken.Literal)
		}
		p.nextToken() // eat JOIN

		if !p.curTokenIs(TokenIdent) {
			return nil, fmt.Errorf("expected table name in JOIN, got %s", p.curToken.Literal)
		}
		jTbl := cleanSchemaPrefix(p.curToken.Literal)
		p.nextToken()

		jAlias := jTbl
		if p.curTokenIs(TokenAs) {
			p.nextToken()
			if p.curTokenIs(TokenIdent) {
				jAlias = cleanSchemaPrefix(p.curToken.Literal)
				p.nextToken()
			}
		} else if p.curTokenIs(TokenIdent) && !p.curTokenIs(TokenOn) {
			jAlias = cleanSchemaPrefix(p.curToken.Literal)
			p.nextToken()
		}

		if !p.curTokenIs(TokenOn) {
			return nil, fmt.Errorf("expected ON after JOIN table, got %s", p.curToken.Literal)
		}
		p.nextToken() // eat ON

		onExpr := p.parseExpression(precLowest)
		stmt.Joins = append(stmt.Joins, JoinClause{
			Type:  joinType,
			Table: TableRef{Name: jTbl, Alias: jAlias},
			On:    onExpr,
		})
	}

	// 4. WHERE clause
	if p.curTokenIs(TokenWhere) {
		p.nextToken() // eat WHERE
		stmt.Where = p.parseExpression(precLowest)
	}

	// 5. GROUP BY clause
	if p.curTokenIs(TokenGroup) {
		p.nextToken() // eat GROUP
		if !p.curTokenIs(TokenBy) {
			return nil, fmt.Errorf("expected BY after GROUP, got %s", p.curToken.Literal)
		}
		p.nextToken() // eat BY
		if p.curTokenIs(TokenRollup) || (p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "ROLLUP")) {
			stmt.GroupByMod = GroupByRollup
			p.nextToken() // eat ROLLUP
			if p.curTokenIs(TokenLParen) {
				p.nextToken() // eat (
				for {
					gExpr := p.parseExpression(precLowest)
					if gExpr != nil {
						stmt.GroupBy = append(stmt.GroupBy, gExpr)
					}
					if p.curTokenIs(TokenComma) {
						p.nextToken()
					} else {
						break
					}
				}
				if p.curTokenIs(TokenRParen) {
					p.nextToken() // eat )
				}
			}
		} else if p.curTokenIs(TokenCube) || (p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "CUBE")) {
			stmt.GroupByMod = GroupByCube
			p.nextToken() // eat CUBE
			if p.curTokenIs(TokenLParen) {
				p.nextToken() // eat (
				for {
					gExpr := p.parseExpression(precLowest)
					if gExpr != nil {
						stmt.GroupBy = append(stmt.GroupBy, gExpr)
					}
					if p.curTokenIs(TokenComma) {
						p.nextToken()
					} else {
						break
					}
				}
				if p.curTokenIs(TokenRParen) {
					p.nextToken() // eat )
				}
			}
		} else {
			for {
				gExpr := p.parseExpression(precLowest)
				if gExpr != nil {
					stmt.GroupBy = append(stmt.GroupBy, gExpr)
				}
				if p.curTokenIs(TokenComma) {
					p.nextToken()
				} else {
					break
				}
			}
			if p.curTokenIs(TokenWith) {
				p.nextToken() // eat WITH
				if p.curTokenIs(TokenRollup) || (p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "ROLLUP")) {
					stmt.GroupByMod = GroupByRollup
					p.nextToken()
				} else if p.curTokenIs(TokenCube) || (p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "CUBE")) {
					stmt.GroupByMod = GroupByCube
					p.nextToken()
				}
			}
		}
	}

	// 6. HAVING clause
	if p.curTokenIs(TokenHaving) {
		p.nextToken() // eat HAVING
		stmt.Having = p.parseExpression(precLowest)
	}

	// 7. ORDER BY clause
	if p.curTokenIs(TokenOrder) {
		p.nextToken() // eat ORDER
		if !p.curTokenIs(TokenBy) {
			return nil, fmt.Errorf("expected BY after ORDER, got %s", p.curToken.Literal)
		}
		p.nextToken() // eat BY
		for {
			oExpr := p.parseExpression(precLowest)
			desc := false
			if p.curTokenIs(TokenDesc) {
				desc = true
				p.nextToken()
			} else if p.curTokenIs(TokenAsc) {
				p.nextToken()
			}
			if oExpr != nil {
				stmt.OrderBy = append(stmt.OrderBy, OrderByItem{Expr: oExpr, Desc: desc})
			}
			if p.curTokenIs(TokenComma) {
				p.nextToken()
			} else {
				break
			}
		}
	}

	// 8. LIMIT and OFFSET clauses
	for p.curTokenIs(TokenLimit) || p.curTokenIs(TokenOffset) {
		if p.curTokenIs(TokenLimit) {
			p.nextToken() // eat LIMIT
			if p.curTokenIs(TokenInt) {
				n, _ := strconv.ParseInt(p.curToken.Literal, 10, 64)
				stmt.Limit = n
				p.nextToken()
			}
		} else if p.curTokenIs(TokenOffset) {
			p.nextToken() // eat OFFSET
			if p.curTokenIs(TokenInt) {
				n, _ := strconv.ParseInt(p.curToken.Literal, 10, 64)
				stmt.Offset = n
				p.nextToken()
			}
		}
	}

	return stmt, nil
}

func (p *Parser) parseGeneralInsert() (Statement, error) {
	p.nextToken() // eat INSERT
	if p.curTokenIs(TokenInto) {
		p.nextToken()
	}
	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected table name in INSERT, got %s", p.curToken.Literal)
	}
	tblName := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken()

	var cols []string
	if p.curTokenIs(TokenLParen) {
		p.nextToken()
		for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
			if !p.curTokenIs(TokenIdent) {
				return nil, fmt.Errorf("expected column name in INSERT, got %s", p.curToken.Literal)
			}
			cols = append(cols, cleanSchemaPrefix(p.curToken.Literal))
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

	var tuples [][]Expr
	for p.curTokenIs(TokenLParen) {
		p.nextToken() // eat (
		var vals []Expr
		for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
			expr := p.parseExpression(precLowest)
			if expr == nil {
				return nil, fmt.Errorf("expected value expression in INSERT tuple")
			}
			vals = append(vals, expr)
			if p.curTokenIs(TokenComma) {
				p.nextToken()
			}
		}
		if !p.curTokenIs(TokenRParen) {
			return nil, fmt.Errorf("expected ) closing VALUES tuple")
		}
		p.nextToken() // eat )
		tuples = append(tuples, vals)

		if p.curTokenIs(TokenComma) {
			p.nextToken()
		} else {
			break
		}
	}

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &GeneralInsertStmt{
		Table:   tblName,
		Columns: cols,
		Tuples:  tuples,
	}, nil
}

func (p *Parser) parseCreateTable() (*CreateTableStmt, error) {
	p.nextToken() // eat CREATE
	if !p.curTokenIs(TokenTable) {
		return nil, fmt.Errorf("expected TABLE after CREATE")
	}
	p.nextToken()

	ifNotExists := false
	if p.curTokenIs(TokenIf) {
		p.nextToken()
		if !p.curTokenIs(TokenNot) {
			return nil, fmt.Errorf("expected NOT after IF in CREATE TABLE")
		}
		p.nextToken()
		if !p.curTokenIs(TokenExists) {
			return nil, fmt.Errorf("expected EXISTS after NOT in CREATE TABLE")
		}
		p.nextToken()
		ifNotExists = true
	}

	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected table name, got %s", p.curToken.Literal)
	}
	tableName := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken()

	if !p.curTokenIs(TokenLParen) {
		return nil, fmt.Errorf("expected ( in CREATE TABLE")
	}
	p.nextToken()

	var cols []ColumnDef
	for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
		if p.curTokenIs(TokenIdent) {
			ident := p.curToken.Literal
			upper := strings.ToUpper(ident)
			if upper == "PRIMARY" || upper == "CONSTRAINT" {
				p.nextToken()
				if strings.ToUpper(p.curToken.Literal) == "KEY" {
					p.nextToken()
				}
				if p.curTokenIs(TokenLParen) {
					p.nextToken()
					pkCol := cleanSchemaPrefix(p.curToken.Literal)
					p.nextToken()
					if p.curTokenIs(TokenRParen) {
						p.nextToken()
					}
					for i := range cols {
						if strings.EqualFold(cols[i].Name, pkCol) {
							cols[i].PrimaryKey = true
						}
					}
				}
				if p.curTokenIs(TokenComma) {
					p.nextToken()
				}
				continue
			}

			colName := cleanSchemaPrefix(ident)
			p.nextToken()

			dt := parseDataType(p.curToken.Literal)
			p.nextToken()

			// Skip optional length e.g. VARCHAR(255)
			if p.curTokenIs(TokenLParen) {
				p.nextToken()
				if p.curTokenIs(TokenInt) {
					p.nextToken()
				}
				if p.curTokenIs(TokenRParen) {
					p.nextToken()
				}
			}

			isPK := false
			isNotNull := false

			for p.curTokenIs(TokenIdent) || p.curTokenIs(TokenNot) || p.curTokenIs(TokenNull) {
				tUpper := strings.ToUpper(p.curToken.Literal)
				if tUpper == "PRIMARY" {
					p.nextToken()
					if strings.ToUpper(p.curToken.Literal) == "KEY" {
						p.nextToken()
					}
					isPK = true
					isNotNull = true
				} else if p.curTokenIs(TokenNot) {
					p.nextToken()
					if p.curTokenIs(TokenNull) {
						p.nextToken()
						isNotNull = true
					}
				} else {
					p.nextToken()
				}
			}

			cols = append(cols, ColumnDef{
				Name:       colName,
				Type:       dt,
				PrimaryKey: isPK,
				Nullable:   !isNotNull,
			})
		}

		if p.curTokenIs(TokenComma) {
			p.nextToken()
		} else {
			break
		}
	}

	if p.curTokenIs(TokenRParen) {
		p.nextToken()
	}
	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &CreateTableStmt{
		Name:        tableName,
		IfNotExists: ifNotExists,
		Columns:     cols,
	}, nil
}

func (p *Parser) parseCreateIndex() (*CreateIndexStmt, error) {
	p.nextToken() // eat CREATE
	isUnique := false
	if p.curTokenIs(TokenUnique) {
		isUnique = true
		p.nextToken()
	}
	if !p.curTokenIs(TokenIndex) {
		return nil, fmt.Errorf("expected INDEX after CREATE [UNIQUE]")
	}
	p.nextToken()

	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected index name")
	}
	idxName := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken()

	if !p.curTokenIs(TokenOn) {
		return nil, fmt.Errorf("expected ON after index name")
	}
	p.nextToken()

	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected table name in CREATE INDEX")
	}
	tblName := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken()

	if !p.curTokenIs(TokenLParen) {
		return nil, fmt.Errorf("expected ( in CREATE INDEX")
	}
	p.nextToken()

	var cols []string
	for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
		if p.curTokenIs(TokenIdent) {
			cols = append(cols, cleanSchemaPrefix(p.curToken.Literal))
			p.nextToken()
		}
		if p.curTokenIs(TokenComma) {
			p.nextToken()
		} else {
			break
		}
	}
	if p.curTokenIs(TokenRParen) {
		p.nextToken()
	}
	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &CreateIndexStmt{
		Name:    idxName,
		Table:   tblName,
		Columns: cols,
		Unique:  isUnique,
	}, nil
}

func (p *Parser) parseDropTable() (*DropTableStmt, error) {
	p.nextToken() // eat TABLE
	ifExists := false
	if p.curTokenIs(TokenIf) {
		p.nextToken()
		if p.curTokenIs(TokenExists) {
			ifExists = true
			p.nextToken()
		}
	}
	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected table name in DROP TABLE")
	}
	tblName := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken()
	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &DropTableStmt{
		Name:     tblName,
		IfExists: ifExists,
	}, nil
}

func (p *Parser) parseDropIndex() (*DropIndexStmt, error) {
	p.nextToken() // eat INDEX
	ifExists := false
	if p.curTokenIs(TokenIf) {
		p.nextToken()
		if p.curTokenIs(TokenExists) {
			ifExists = true
			p.nextToken()
		}
	}
	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected index name in DROP INDEX")
	}
	idxName := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken()

	tblName := ""
	if p.curTokenIs(TokenOn) {
		p.nextToken()
		if p.curTokenIs(TokenIdent) {
			tblName = cleanSchemaPrefix(p.curToken.Literal)
			p.nextToken()
		}
	}
	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &DropIndexStmt{
		Name:     idxName,
		Table:    tblName,
		IfExists: ifExists,
	}, nil
}

func (p *Parser) parseWith() (*GeneralSelectStmt, error) {
	p.nextToken() // eat WITH
	var ctes []CTEClause

	for {
		if !p.curTokenIs(TokenIdent) {
			return nil, fmt.Errorf("expected CTE name after WITH, got %s", p.curToken.Literal)
		}
		cteName := cleanSchemaPrefix(p.curToken.Literal)
		p.nextToken()

		if !p.curTokenIs(TokenAs) {
			return nil, fmt.Errorf("expected AS after CTE name, got %s", p.curToken.Literal)
		}
		p.nextToken()

		if !p.curTokenIs(TokenLParen) {
			return nil, fmt.Errorf("expected ( after AS in CTE")
		}
		p.nextToken() // eat (

		if !p.curTokenIs(TokenSelect) {
			return nil, fmt.Errorf("expected SELECT inside CTE")
		}
		p.nextToken() // eat SELECT

		innerSelect, err := p.parseGeneralSelect()
		if err != nil {
			return nil, err
		}

		if !p.curTokenIs(TokenRParen) {
			return nil, fmt.Errorf("expected ) closing CTE")
		}
		p.nextToken() // eat )

		ctes = append(ctes, CTEClause{
			Name:     cteName,
			Query:    innerSelect,
			InnerSQL: innerSelect.String(),
		})

		if p.curTokenIs(TokenComma) {
			p.nextToken()
		} else {
			break
		}
	}

	if !p.curTokenIs(TokenSelect) {
		return nil, fmt.Errorf("expected main SELECT after CTEs, got %s", p.curToken.Literal)
	}
	p.nextToken() // eat SELECT

	mainSelect, err := p.parseGeneralSelect()
	if err != nil {
		return nil, err
	}
	mainSelect.CTEs = ctes
	return mainSelect, nil
}


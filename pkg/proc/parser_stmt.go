package proc

import (
	"fmt"
	"strings"
)

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


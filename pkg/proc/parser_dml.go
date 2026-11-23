package proc

import (
	"fmt"
	"strconv"
	"strings"
)

func (p *Parser) parseSelect() (Statement, error) {
	p.nextToken() // eat SELECT

	if p.curTokenIs(TokenIdent) && p.peekTokenIs(TokenInto) {
		colName := p.curToken.Literal
		p.nextToken() // eat colName; now curToken is INTO, peekToken is after INTO

		if p.peekTokenIs(TokenVariable) {
			p.nextToken() // eat INTO
			varName := p.curToken.Literal
			p.nextToken()

			if !p.curTokenIs(TokenFrom) {
				return nil, fmt.Errorf("expected FROM, got %s", p.curToken.Literal)
			}
			p.nextToken()

			if !p.curTokenIs(TokenIdent) && !p.curTokenIs(TokenVariable) {
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
				if len(p.errors) > 0 {
					return nil, fmt.Errorf("syntax error in WHERE clause: %s", strings.Join(p.errors, "; "))
				}
				if whereVal == nil {
					return nil, fmt.Errorf("syntax error: empty or invalid expression in WHERE clause")
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

		// Not TokenVariable: SELECT col INTO <table_name> FROM ...
		p.nextToken() // eat INTO; curToken is target table
		intoTbl := p.curToken.Literal
		p.nextToken() // eat target table
		stmt := &GeneralSelectStmt{
			Limit:       -1,
			Projections: []Projection{{Expr: &ColumnRefExpr{Name: colName}}},
			IntoTable:   intoTbl,
		}
		res, err := p.parseGeneralSelectClauses(stmt)
		if err != nil {
			return nil, err
		}
		if p.curTokenIs(TokenSemicolon) {
			p.nextToken()
		}
		return res, nil
	}

	stmt, err := p.parseGeneralSelect()
	if err != nil {
		return nil, err
	}
	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}
	return stmt, nil
}

func (p *Parser) parseOutputClause() (cols []string, intoVars []string, intoTable string, intoTableCols []string) {
	if !p.curTokenIs(TokenOutput) {
		return nil, nil, "", nil
	}
	p.nextToken() // eat OUTPUT
	for !p.curTokenIs(TokenInto) && !p.curTokenIs(TokenSelect) && !p.curTokenIs(TokenValues) && !p.curTokenIs(TokenWhere) && !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
		col := cleanSchemaPrefix(p.curToken.Literal)
		p.nextToken()
		if p.curTokenIs(TokenAs) {
			p.nextToken()
			col = cleanSchemaPrefix(p.curToken.Literal)
			p.nextToken()
		}
		cols = append(cols, col)
		if p.curTokenIs(TokenComma) {
			p.nextToken()
		} else {
			break
		}
	}
	if p.curTokenIs(TokenInto) {
		p.nextToken() // eat INTO
		if !p.curTokenIs(TokenSelect) && !p.curTokenIs(TokenValues) && !p.curTokenIs(TokenWhere) && !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
			target := p.curToken.Literal
			p.nextToken()
			if p.curTokenIs(TokenLParen) {
				p.nextToken() // eat (
				for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
					intoTableCols = append(intoTableCols, cleanSchemaPrefix(p.curToken.Literal))
					p.nextToken()
					if p.curTokenIs(TokenComma) {
						p.nextToken()
					}
				}
				if p.curTokenIs(TokenRParen) {
					p.nextToken() // eat )
				}
				intoTable = target
				intoVars = []string{cleanVarName(target)}
			} else if p.curTokenIs(TokenComma) {
				intoVars = append(intoVars, cleanVarName(target))
				for p.curTokenIs(TokenComma) {
					p.nextToken() // eat ,
					if !p.curTokenIs(TokenSelect) && !p.curTokenIs(TokenValues) && !p.curTokenIs(TokenWhere) && !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
						intoVars = append(intoVars, cleanVarName(p.curToken.Literal))
						p.nextToken()
					}
				}
			} else {
				intoTable = target
				intoVars = []string{cleanVarName(target)}
			}
		}
	}
	return cols, intoVars, intoTable, intoTableCols
}

func (p *Parser) parseReturningClause() (cols []string, intoVars []string, intoTable string, intoTableCols []string) {
	if !p.curTokenIs(TokenReturning) {
		return nil, nil, "", nil
	}
	p.nextToken() // eat RETURNING
	for !p.curTokenIs(TokenInto) && !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
		col := cleanSchemaPrefix(p.curToken.Literal)
		p.nextToken()
		if p.curTokenIs(TokenAs) {
			p.nextToken()
			col = cleanSchemaPrefix(p.curToken.Literal)
			p.nextToken()
		}
		cols = append(cols, col)
		if p.curTokenIs(TokenComma) {
			p.nextToken()
		} else {
			break
		}
	}
	if p.curTokenIs(TokenInto) {
		p.nextToken() // eat INTO
		if !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
			target := p.curToken.Literal
			p.nextToken()
			if p.curTokenIs(TokenLParen) {
				p.nextToken() // eat (
				for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
					intoTableCols = append(intoTableCols, cleanSchemaPrefix(p.curToken.Literal))
					p.nextToken()
					if p.curTokenIs(TokenComma) {
						p.nextToken()
					}
				}
				if p.curTokenIs(TokenRParen) {
					p.nextToken() // eat )
				}
				intoTable = target
				intoVars = []string{cleanVarName(target)}
			} else if p.curTokenIs(TokenComma) {
				intoVars = append(intoVars, cleanVarName(target))
				for p.curTokenIs(TokenComma) {
					p.nextToken() // eat ,
					if !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
						intoVars = append(intoVars, cleanVarName(p.curToken.Literal))
						p.nextToken()
					}
				}
			} else {
				intoTable = target
				intoVars = []string{cleanVarName(target)}
			}
		}
	}
	return cols, intoVars, intoTable, intoTableCols
}

func (p *Parser) parseOutputCols() []string {
	cols, _, _, _ := p.parseOutputClause()
	return cols
}

func (p *Parser) parseInsert() (Statement, error) {
	return p.parseGeneralInsert()
}

func (p *Parser) parseUpdate() (Statement, error) {
	p.nextToken() // eat UPDATE
	if !p.curTokenIs(TokenIdent) && !p.curTokenIs(TokenVariable) {
		return nil, fmt.Errorf("expected table name in UPDATE, got %s", p.curToken.Literal)
	}
	tblName := cleanSchemaPrefix(p.curToken.Literal)
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
		colName := cleanSchemaPrefix(p.curToken.Literal)
		p.nextToken()

		var compoundBinaryOp TokenType
		var isCompound bool
		if op, ok := IsCompoundAssign(p.curToken.Type); ok {
			compoundBinaryOp = op
			isCompound = true
		} else if !p.curTokenIs(TokenEq) && !p.curTokenIs(TokenAssign) {
			return nil, fmt.Errorf("expected = in UPDATE SET, got %s", p.curToken.Literal)
		}
		p.nextToken()

		valExpr := p.parseExpression(precLowest)
		if len(p.errors) > 0 {
			return nil, fmt.Errorf("syntax error in SET clause: %s", strings.Join(p.errors, "; "))
		}
		if valExpr == nil {
			return nil, fmt.Errorf("syntax error: empty or invalid expression in SET clause")
		}
		if isCompound {
			valExpr = &BinaryExpr{
				Left:  &ColumnRefExpr{Name: colName},
				Op:    compoundBinaryOp,
				Right: valExpr,
			}
		}
		assigns = append(assigns, UpdateAssignment{Column: colName, Value: valExpr})

		if p.curTokenIs(TokenComma) {
			p.nextToken()
		} else {
			break
		}
	}

	outputCols, intoVars, intoTable, intoTableCols := p.parseOutputClause()

	// MSSQL allows UPDATE <table> SET ... [FROM <source_table_or_join>] [WHERE ...]
	if p.curTokenIs(TokenFrom) {
		p.nextToken()
		hasFromTokens := false
		parenDepth := 0
		for (!p.curTokenIs(TokenWhere) || parenDepth > 0) && !p.curTokenIs(TokenReturning) && !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
			hasFromTokens = true
			if p.curTokenIs(TokenLParen) {
				parenDepth++
			} else if p.curTokenIs(TokenRParen) && parenDepth > 0 {
				parenDepth--
			}
			p.nextToken()
		}
		if !hasFromTokens {
			return nil, fmt.Errorf("expected table name or join after FROM in UPDATE")
		}
	}

	var whereExpr Expr
	if p.curTokenIs(TokenWhere) {
		p.nextToken()
		whereExpr = p.parseExpression(precLowest)
		if len(p.errors) > 0 {
			return nil, fmt.Errorf("syntax error in WHERE clause: %s", strings.Join(p.errors, "; "))
		}
		if whereExpr == nil {
			return nil, fmt.Errorf("syntax error: empty or invalid expression in WHERE clause")
		}
		if ContainsWindowFunction(whereExpr) {
			return nil, fmt.Errorf("window functions are not allowed in WHERE")
		}
	}

	if p.curTokenIs(TokenReturning) {
		rCols, rVars, rTable, rTableCols := p.parseReturningClause()
		outputCols = append(outputCols, rCols...)
		intoVars = append(intoVars, rVars...)
		if intoTable == "" {
			intoTable = rTable
			intoTableCols = rTableCols
		}
	}

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	// Check if this is simple single-column equality
	if bin, ok := whereExpr.(*BinaryExpr); ok && bin.Op == TokenEq {
		if colRef, isCol := bin.Left.(*ColumnRefExpr); isCol {
			return &UpdateStmt{
				Table:         tblName,
				Assignments:   assigns,
				WhereCol:      cleanSchemaPrefix(colRef.Name),
				WhereVal:      bin.Right,
				OutputCols:    outputCols,
				IntoVars:      intoVars,
				IntoTable:     intoTable,
				IntoTableCols: intoTableCols,
			}, nil
		}
	}

	return &UpdateWhereStmt{
		Table:         tblName,
		Assignments:   assigns,
		OutputCols:    outputCols,
		IntoVars:      intoVars,
		IntoTable:     intoTable,
		IntoTableCols: intoTableCols,
		Where:         whereExpr,
	}, nil
}

func (p *Parser) parseDelete() (Statement, error) {
	p.nextToken() // eat DELETE
	if p.curTokenIs(TokenFrom) {
		p.nextToken()
	}
	if !p.curTokenIs(TokenIdent) && !p.curTokenIs(TokenVariable) {
		return nil, fmt.Errorf("expected table name in DELETE, got %s", p.curToken.Literal)
	}
	tblName := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken()

	outputCols, intoVars, intoTable, intoTableCols := p.parseOutputClause()

	// MSSQL allows DELETE <table> ... [FROM <source_table_or_join>] [WHERE ...]
	if p.curTokenIs(TokenFrom) {
		p.nextToken()
		hasFromTokens := false
		parenDepth := 0
		for (!p.curTokenIs(TokenWhere) || parenDepth > 0) && !p.curTokenIs(TokenReturning) && !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
			hasFromTokens = true
			if p.curTokenIs(TokenLParen) {
				parenDepth++
			} else if p.curTokenIs(TokenRParen) && parenDepth > 0 {
				parenDepth--
			}
			p.nextToken()
		}
		if !hasFromTokens {
			return nil, fmt.Errorf("expected table name or join after FROM in DELETE")
		}
	}

	var whereExpr Expr
	if p.curTokenIs(TokenWhere) {
		p.nextToken()
		whereExpr = p.parseExpression(precLowest)
		if len(p.errors) > 0 {
			return nil, fmt.Errorf("syntax error in WHERE clause: %s", strings.Join(p.errors, "; "))
		}
		if whereExpr == nil {
			return nil, fmt.Errorf("syntax error: empty or invalid expression in WHERE clause")
		}
		if ContainsWindowFunction(whereExpr) {
			return nil, fmt.Errorf("window functions are not allowed in WHERE")
		}
	}

	if p.curTokenIs(TokenReturning) {
		rCols, rVars, rTable, rTableCols := p.parseReturningClause()
		outputCols = append(outputCols, rCols...)
		intoVars = append(intoVars, rVars...)
		if intoTable == "" {
			intoTable = rTable
			intoTableCols = rTableCols
		}
	}

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	if bin, ok := whereExpr.(*BinaryExpr); ok && bin.Op == TokenEq {
		if colRef, isCol := bin.Left.(*ColumnRefExpr); isCol {
			return &DeleteStmt{
				Table:         tblName,
				WhereCol:      cleanSchemaPrefix(colRef.Name),
				WhereVal:      bin.Right,
				OutputCols:    outputCols,
				IntoVars:      intoVars,
				IntoTable:     intoTable,
				IntoTableCols: intoTableCols,
			}, nil
		}
	}

	return &DeleteWhereStmt{
		Table:         tblName,
		OutputCols:    outputCols,
		IntoVars:      intoVars,
		IntoTable:     intoTable,
		IntoTableCols: intoTableCols,
		Where:         whereExpr,
	}, nil
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
			return p.ParseCreateTable()
		}
		if p.peekTokenIs(TokenIndex) || p.peekTokenIs(TokenUnique) {
			return p.ParseCreateIndex()
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

func (p *Parser) parseTableHints() []string {
	if !p.curTokenIs(TokenWith) {
		return nil
	}
	if p.peekToken.Type != TokenLParen {
		return nil
	}
	p.nextToken() // eat WITH
	p.nextToken() // eat (
	var hints []string
	for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
		hintName := p.curToken.Literal
		p.nextToken()
		if p.curTokenIs(TokenEq) {
			p.nextToken() // eat =
		}
		if p.curTokenIs(TokenLParen) {
			p.nextToken() // eat (
			inner := ""
			if !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
				inner = p.curToken.Literal
				p.nextToken()
			}
			if p.curTokenIs(TokenRParen) {
				p.nextToken() // eat )
			}
			hintName = fmt.Sprintf("%s(%s)", hintName, inner)
		} else if strings.EqualFold(hintName, "INDEX") && (p.curTokenIs(TokenIdent) || p.curTokenIs(TokenInt)) {
			hintName = fmt.Sprintf("INDEX(%s)", p.curToken.Literal)
			p.nextToken()
		}
		hints = append(hints, hintName)
		if p.curTokenIs(TokenComma) {
			p.nextToken() // eat ,
		} else {
			break
		}
	}
	if p.curTokenIs(TokenRParen) {
		p.nextToken() // eat )
	}
	return hints
}

func (p *Parser) isJoinHint() bool {
	return p.curTokenIs(TokenMerge) || (p.curTokenIs(TokenIdent) && (strings.EqualFold(p.curToken.Literal, "HASH") || strings.EqualFold(p.curToken.Literal, "LOOP") || strings.EqualFold(p.curToken.Literal, "MERGE")))
}

func (p *Parser) parseGeneralSelect() (*GeneralSelectStmt, error) {
	stmt := &GeneralSelectStmt{Limit: -1}

	for p.curTokenIs(TokenDistinct) || p.curTokenIs(TokenTop) || p.curTokenIs(TokenAll) || (p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "ALL")) {
		if p.curTokenIs(TokenDistinct) {
			stmt.Distinct = true
			p.nextToken() // eat DISTINCT
		} else if p.curTokenIs(TokenAll) || (p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "ALL")) {
			p.nextToken() // eat ALL (default SQL quantifier)
		} else if p.curTokenIs(TokenTop) {
			p.nextToken() // eat TOP
			hasParen := false
			if p.curTokenIs(TokenLParen) {
				hasParen = true
				p.nextToken() // eat (
			}
			if p.curTokenIs(TokenInt) {
				n, _ := strconv.ParseInt(p.curToken.Literal, 10, 64)
				stmt.Limit = n
				p.nextToken()
			}
			if hasParen && p.curTokenIs(TokenRParen) {
				p.nextToken() // eat )
			}
			if p.curTokenIs(TokenPercent) {
				stmt.TopPercent = true
				p.nextToken() // eat PERCENT
			}
			if p.curTokenIs(TokenWith) {
				p.nextToken() // eat WITH
				if p.curTokenIs(TokenTies) || (p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "TIES")) {
					stmt.TopWithTies = true
					p.nextToken() // eat TIES
				}
			}
		}
	}

	// 1. Projections
	for !p.curTokenIs(TokenFrom) && !p.curTokenIs(TokenInto) && !p.curTokenIs(TokenWhere) && !p.curTokenIs(TokenGroup) && !p.curTokenIs(TokenHaving) && !p.curTokenIs(TokenOrder) && !p.curTokenIs(TokenLimit) && !p.curTokenIs(TokenOffset) && !p.curTokenIs(TokenFetch) && !p.curTokenIs(TokenEOF) && !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenRParen) && !isReservedAfterProjection(p.curToken.Type) {
		if p.curTokenIs(TokenVariable) && p.peekTokenIs(TokenEq) {
			varName := p.curToken.Literal
			p.nextToken() // eat @var
			p.nextToken() // eat =
			expr := p.parseExpression(precLowest)
			if expr == nil {
				return nil, fmt.Errorf("expected expression after %s =", varName)
			}
			stmt.Projections = append(stmt.Projections, Projection{Expr: expr, Alias: varName, IsAssign: true})
			if p.curTokenIs(TokenComma) {
				p.nextToken()
				continue
			}
			break
		}

		p.allowStar = true
		expr := p.parseExpression(precLowest)
		p.allowStar = false
		if expr == nil {
			return nil, fmt.Errorf("expected projection expression, got %s", p.curToken.Literal)
		}
		alias := ""
		if p.curTokenIs(TokenAs) {
			p.nextToken() // eat AS
			if !isReservedAfterProjection(p.curToken.Type) && !isSetOpIdent(p.curToken) {
				alias = cleanSchemaPrefix(p.curToken.Literal)
				p.nextToken()
			}
		} else if (p.curTokenIs(TokenIdent) || p.curTokenIs(TokenString)) && !isReservedAfterProjection(p.curToken.Type) && !isSetOpIdent(p.curToken) {
			alias = cleanSchemaPrefix(p.curToken.Literal)
			p.nextToken()
		}

		stmt.Projections = append(stmt.Projections, Projection{Expr: expr, Alias: alias, IsAssign: false})

		if p.curTokenIs(TokenComma) {
			p.nextToken()
		} else {
			break
		}
	}

	// Check No-Mixing Invariant (Msg 141): Variable assignment cannot be mixed with regular data-retrieval
	hasAssign := false
	hasNonAssign := false
	for _, proj := range stmt.Projections {
		if proj.IsAssign {
			hasAssign = true
		} else {
			hasNonAssign = true
		}
	}
	if hasAssign && hasNonAssign {
		return nil, fmt.Errorf("Msg 141, Level 15, State 1: A SELECT statement that assigns a value to a variable must not be combined with data-retrieval operations.")
	}

	if p.curTokenIs(TokenInto) {
		p.nextToken() // eat INTO
		if p.curTokenIs(TokenIdent) || p.curTokenIs(TokenVariable) {
			stmt.IntoTable = p.curToken.Literal
			p.nextToken()
		}
	}

	return p.parseGeneralSelectClauses(stmt)
}

func (p *Parser) parseGeneralSelectClauses(stmt *GeneralSelectStmt) (*GeneralSelectStmt, error) {
	// 2. FROM clause
	fromParens := 0
	if p.curTokenIs(TokenFrom) {
		p.nextToken() // eat FROM

		for p.curTokenIs(TokenLParen) && !p.peekTokenIs(TokenSelect) && !p.peekTokenIs(TokenWith) {
			fromParens++
			p.nextToken() // eat (
		}

		if p.curTokenIs(TokenLParen) {
			p.nextToken() // eat (
			var subSel *GeneralSelectStmt
			var err error
			if p.curTokenIs(TokenWith) {
				subSel, err = p.parseWith()
			} else {
				if p.curTokenIs(TokenSelect) {
					p.nextToken() // eat SELECT
				}
				subSel, err = p.parseGeneralSelect()
			}
			if err != nil {
				return nil, err
			}
			if !p.curTokenIs(TokenRParen) {
				return nil, fmt.Errorf("expected ) closing subquery in FROM, got %s", p.curToken.Literal)
			}
			p.nextToken() // eat )

			alias := ""
			if p.curTokenIs(TokenAs) {
				p.nextToken() // eat AS
				if p.curTokenIs(TokenIdent) {
					alias = cleanSchemaPrefix(p.curToken.Literal)
					p.nextToken()
				}
			} else if p.curTokenIs(TokenIdent) && !isReservedAfterFromOrJoin(p.curToken.Type) && !isSetOpIdent(p.curToken) {
				alias = cleanSchemaPrefix(p.curToken.Literal)
				p.nextToken()
			}
			colAliases := p.parseColumnAliasList()
			fromHints := p.parseTableHints()

			stmt.From = TableRef{
				Name:          subSel.String(),
				Alias:         alias,
				ColumnAliases: colAliases,
				Subquery:      subSel,
				Hints:         fromHints,
			}
		} else {
			if !p.curTokenIs(TokenIdent) && !p.curTokenIs(TokenVariable) {
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
			} else if (p.curTokenIs(TokenIdent) || p.curTokenIs(TokenMerge)) && !isReservedAfterFromOrJoin(p.curToken.Type) && !isSetOpIdent(p.curToken) {
				if p.isJoinHint() && p.peekToken.Type == TokenJoin {
					// Do not consume HASH/LOOP/MERGE as alias when followed by JOIN
				} else {
					alias = cleanSchemaPrefix(p.curToken.Literal)
					p.nextToken()
				}
			}
			colAliases := p.parseColumnAliasList()
			fromHints := p.parseTableHints()

			stmt.From = TableRef{Name: tblName, Alias: alias, ColumnAliases: colAliases, IsTVF: isTVF, TVFArgs: tvfArgs, Hints: fromHints}
		}

		for fromParens > 0 && p.curTokenIs(TokenRParen) {
			p.nextToken() // eat )
			fromParens--
			if p.curTokenIs(TokenAs) {
				p.nextToken()
				if p.curTokenIs(TokenIdent) {
					p.nextToken()
				}
			} else if p.curTokenIs(TokenIdent) && !isReservedAfterFromOrJoin(p.curToken.Type) {
				p.nextToken()
			}
		}
	}

	// 3. JOIN & APPLY clauses
	for p.curTokenIs(TokenComma) || p.curTokenIs(TokenJoin) || p.curTokenIs(TokenInner) || p.curTokenIs(TokenLeft) || p.curTokenIs(TokenRight) || p.curTokenIs(TokenFull) || p.curTokenIs(TokenCross) || p.curTokenIs(TokenOuter) ||
		p.isJoinHint() {
		joinType := "INNER"
		isApply := false
		if p.curTokenIs(TokenComma) {
			p.nextToken() // eat ,
			joinType = "CROSS"
		} else if p.isJoinHint() {
			joinHint := strings.ToUpper(p.curToken.Literal)
			p.nextToken()
			if !p.curTokenIs(TokenJoin) {
				return nil, fmt.Errorf("expected JOIN after %s, got %s", joinHint, p.curToken.Literal)
			}
			p.nextToken() // eat JOIN
			joinType = "INNER " + joinHint
		} else if p.curTokenIs(TokenCross) {
			p.nextToken() // eat CROSS
			if p.curTokenIs(TokenApply) {
				p.nextToken() // eat APPLY
				joinType = "CROSS APPLY"
				isApply = true
			} else if p.curTokenIs(TokenJoin) {
				p.nextToken() // eat JOIN
				joinType = "CROSS"
				if strings.EqualFold(p.curToken.Literal, "LATERAL") {
					p.nextToken() // eat LATERAL
					joinType = "CROSS APPLY"
					isApply = true
				}
			} else {
				return nil, fmt.Errorf("expected APPLY or JOIN after CROSS, got %s", p.curToken.Literal)
			}
		} else if p.curTokenIs(TokenOuter) {
			p.nextToken() // eat OUTER
			if p.curTokenIs(TokenApply) {
				p.nextToken() // eat APPLY
				joinType = "OUTER APPLY"
				isApply = true
			} else {
				return nil, fmt.Errorf("expected APPLY after OUTER, got %s", p.curToken.Literal)
			}
		} else if p.curTokenIs(TokenLeft) {
			joinType = "LEFT"
			p.nextToken() // eat LEFT
			if p.curTokenIs(TokenOuter) {
				p.nextToken() // eat OUTER
			}
			var joinHint string
			if p.isJoinHint() {
				joinHint = strings.ToUpper(p.curToken.Literal)
				p.nextToken()
			}
			if p.curTokenIs(TokenApply) {
				p.nextToken() // eat APPLY
				joinType = "OUTER APPLY"
				isApply = true
			} else {
				if !p.curTokenIs(TokenJoin) {
					return nil, fmt.Errorf("expected JOIN, got %s", p.curToken.Literal)
				}
				p.nextToken() // eat JOIN
				if strings.EqualFold(p.curToken.Literal, "LATERAL") {
					p.nextToken() // eat LATERAL
					joinType = "OUTER APPLY"
					isApply = true
				} else if joinHint != "" {
					joinType = joinType + " " + joinHint
				}
			}
		} else if p.curTokenIs(TokenRight) {
			joinType = "RIGHT"
			p.nextToken() // eat RIGHT
			if p.curTokenIs(TokenOuter) {
				p.nextToken() // eat OUTER
			}
			var joinHint string
			if p.isJoinHint() {
				joinHint = strings.ToUpper(p.curToken.Literal)
				p.nextToken()
			}
			if !p.curTokenIs(TokenJoin) {
				return nil, fmt.Errorf("expected JOIN after RIGHT, got %s", p.curToken.Literal)
			}
			p.nextToken() // eat JOIN
			if strings.EqualFold(p.curToken.Literal, "LATERAL") {
				p.nextToken() // eat LATERAL
				joinType = "OUTER APPLY"
				isApply = true
			} else if joinHint != "" {
				joinType = joinType + " " + joinHint
			}
		} else if p.curTokenIs(TokenFull) {
			joinType = "FULL"
			p.nextToken() // eat FULL
			if p.curTokenIs(TokenOuter) {
				p.nextToken() // eat OUTER
			}
			var joinHint string
			if p.isJoinHint() {
				joinHint = strings.ToUpper(p.curToken.Literal)
				p.nextToken()
			}
			if !p.curTokenIs(TokenJoin) {
				return nil, fmt.Errorf("expected JOIN after FULL, got %s", p.curToken.Literal)
			}
			p.nextToken() // eat JOIN
			if joinHint != "" {
				joinType = joinType + " " + joinHint
			}
		} else if p.curTokenIs(TokenInner) {
			p.nextToken() // eat INNER
			var joinHint string
			if p.isJoinHint() {
				joinHint = strings.ToUpper(p.curToken.Literal)
				p.nextToken()
			}
			if !p.curTokenIs(TokenJoin) {
				return nil, fmt.Errorf("expected JOIN, got %s", p.curToken.Literal)
			}
			p.nextToken() // eat JOIN
			if strings.EqualFold(p.curToken.Literal, "LATERAL") {
				p.nextToken() // eat LATERAL
				joinType = "CROSS APPLY"
				isApply = true
			} else if joinHint != "" {
				joinType = "INNER " + joinHint
			}
		} else if p.curTokenIs(TokenJoin) {
			p.nextToken() // eat JOIN
			if strings.EqualFold(p.curToken.Literal, "LATERAL") {
				p.nextToken() // eat LATERAL
				joinType = "CROSS APPLY"
				isApply = true
			}
		}

		if p.curTokenIs(TokenLParen) && (p.peekTokenIs(TokenSelect) || p.peekTokenIs(TokenWith)) {
			p.nextToken() // eat (
			var subSel *GeneralSelectStmt
			var err error
			if p.curTokenIs(TokenWith) {
				subSel, err = p.parseWith()
			} else {
				if p.curTokenIs(TokenSelect) {
					p.nextToken() // eat SELECT
				}
				subSel, err = p.parseGeneralSelect()
			}
			if err != nil {
				return nil, err
			}
			if !p.curTokenIs(TokenRParen) {
				return nil, fmt.Errorf("expected ) closing subquery in JOIN, got %s", p.curToken.Literal)
			}
			p.nextToken() // eat )

			jAlias := ""
			if p.curTokenIs(TokenAs) {
				p.nextToken()
				if p.curTokenIs(TokenIdent) {
					jAlias = cleanSchemaPrefix(p.curToken.Literal)
					p.nextToken()
				}
			} else if (p.curTokenIs(TokenIdent) || p.curTokenIs(TokenMerge)) && !isReservedAfterFromOrJoin(p.curToken.Type) && !isSetOpIdent(p.curToken) {
				if p.isJoinHint() && p.peekToken.Type == TokenJoin {
					// Do not consume HASH/LOOP/MERGE as alias when followed by JOIN
				} else {
					jAlias = cleanSchemaPrefix(p.curToken.Literal)
					p.nextToken()
				}
			}
			colAliases := p.parseColumnAliasList()
			jHints := p.parseTableHints()

			var onExpr Expr
			if !isApply && joinType != "CROSS" {
				if !p.curTokenIs(TokenOn) {
					return nil, fmt.Errorf("expected ON after JOIN table, got %s", p.curToken.Literal)
				}
				p.nextToken() // eat ON
				onExpr = p.parseExpression(precLowest)
				if len(p.errors) > 0 {
					return nil, fmt.Errorf("syntax error in ON clause: %s", strings.Join(p.errors, "; "))
				}
				if onExpr == nil {
					return nil, fmt.Errorf("syntax error: empty or invalid expression in ON clause")
				}
			} else if joinType == "CROSS" && p.curTokenIs(TokenOn) {
				return nil, fmt.Errorf("syntax error: CROSS JOIN or comma-separated table list cannot have an ON clause")
			}

			stmt.Joins = append(stmt.Joins, JoinClause{
				Type:  joinType,
				Table: TableRef{Name: subSel.String(), Alias: jAlias, ColumnAliases: colAliases, Subquery: subSel, Hints: jHints},
				On:    onExpr,
			})
		} else {
			joinParens := 0
			for p.curTokenIs(TokenLParen) && !p.peekTokenIs(TokenSelect) && !p.peekTokenIs(TokenWith) {
				joinParens++
				p.nextToken() // eat (
			}

			if !p.curTokenIs(TokenIdent) && !p.curTokenIs(TokenVariable) {
				return nil, fmt.Errorf("expected table name in JOIN, got %s", p.curToken.Literal)
			}
			jTbl := cleanSchemaPrefix(p.curToken.Literal)
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
					return nil, fmt.Errorf("expected ) closing TVF arguments in JOIN")
				}
				p.nextToken() // eat )
			}

			for joinParens > 0 && p.curTokenIs(TokenRParen) {
				p.nextToken() // eat )
				joinParens--
			}

			jAlias := jTbl
			if p.curTokenIs(TokenAs) {
				p.nextToken()
				if p.curTokenIs(TokenIdent) {
					jAlias = cleanSchemaPrefix(p.curToken.Literal)
					p.nextToken()
				}
			} else if (p.curTokenIs(TokenIdent) || p.curTokenIs(TokenMerge)) && !isReservedAfterFromOrJoin(p.curToken.Type) && !isSetOpIdent(p.curToken) {
				if p.isJoinHint() && p.peekToken.Type == TokenJoin {
					// Do not consume HASH/LOOP/MERGE as alias when followed by JOIN
				} else {
					jAlias = cleanSchemaPrefix(p.curToken.Literal)
					p.nextToken()
				}
			}
			colAliases := p.parseColumnAliasList()
			jHints := p.parseTableHints()

			var onExpr Expr
			if !isApply && joinType != "CROSS" {
				if !p.curTokenIs(TokenOn) {
					return nil, fmt.Errorf("expected ON after JOIN table, got %s", p.curToken.Literal)
				}
				p.nextToken() // eat ON
				onExpr = p.parseExpression(precLowest)
				if len(p.errors) > 0 {
					return nil, fmt.Errorf("syntax error in ON clause: %s", strings.Join(p.errors, "; "))
				}
				if onExpr == nil {
					return nil, fmt.Errorf("syntax error: empty or invalid expression in ON clause")
				}
			} else if joinType == "CROSS" && p.curTokenIs(TokenOn) {
				return nil, fmt.Errorf("syntax error: CROSS JOIN or comma-separated table list cannot have an ON clause")
			}

			stmt.Joins = append(stmt.Joins, JoinClause{
				Type:  joinType,
				Table: TableRef{Name: jTbl, Alias: jAlias, ColumnAliases: colAliases, IsTVF: isTVF, TVFArgs: tvfArgs, Hints: jHints},
				On:    onExpr,
			})
		}

		for fromParens > 0 && p.curTokenIs(TokenRParen) {
			p.nextToken() // eat )
			fromParens--
			if p.curTokenIs(TokenAs) {
				p.nextToken()
				if p.curTokenIs(TokenIdent) {
					p.nextToken()
				}
			} else if p.curTokenIs(TokenIdent) && !isReservedAfterFromOrJoin(p.curToken.Type) {
				p.nextToken()
			}
		}
	}

	for fromParens > 0 && p.curTokenIs(TokenRParen) {
		p.nextToken() // eat )
		fromParens--
		if p.curTokenIs(TokenAs) {
			p.nextToken()
			if p.curTokenIs(TokenIdent) {
				p.nextToken()
			}
		} else if p.curTokenIs(TokenIdent) && !isReservedAfterFromOrJoin(p.curToken.Type) {
			p.nextToken()
		}
	}

	// 4. WHERE clause
	if p.curTokenIs(TokenWhere) {
		p.nextToken() // eat WHERE
		stmt.Where = p.parseExpression(precLowest)
		if len(p.errors) > 0 {
			return nil, fmt.Errorf("syntax error in WHERE clause: %s", strings.Join(p.errors, "; "))
		}
		if stmt.Where == nil {
			return nil, fmt.Errorf("syntax error: empty or invalid expression in WHERE clause")
		}
		if ContainsWindowFunction(stmt.Where) {
			return nil, fmt.Errorf("window functions are not allowed in WHERE")
		}
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
					if len(p.errors) > 0 {
						return nil, fmt.Errorf("syntax error in GROUP BY clause: %s", strings.Join(p.errors, "; "))
					}
					if gExpr == nil {
						return nil, fmt.Errorf("syntax error: empty or invalid expression in GROUP BY clause")
					}
					stmt.GroupBy = append(stmt.GroupBy, gExpr)
					if p.curTokenIs(TokenComma) {
						p.nextToken()
					} else {
						break
					}
				}
				if p.curTokenIs(TokenRParen) {
					p.nextToken() // eat )
				} else {
					return nil, fmt.Errorf("expected ) closing ROLLUP in GROUP BY")
				}
			}
		} else if p.curTokenIs(TokenCube) || (p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "CUBE")) {
			stmt.GroupByMod = GroupByCube
			p.nextToken() // eat CUBE
			if p.curTokenIs(TokenLParen) {
				p.nextToken() // eat (
				for {
					gExpr := p.parseExpression(precLowest)
					if len(p.errors) > 0 {
						return nil, fmt.Errorf("syntax error in GROUP BY clause: %s", strings.Join(p.errors, "; "))
					}
					if gExpr == nil {
						return nil, fmt.Errorf("syntax error: empty or invalid expression in GROUP BY clause")
					}
					stmt.GroupBy = append(stmt.GroupBy, gExpr)
					if p.curTokenIs(TokenComma) {
						p.nextToken()
					} else {
						break
					}
				}
				if p.curTokenIs(TokenRParen) {
					p.nextToken() // eat )
				} else {
					return nil, fmt.Errorf("expected ) closing CUBE in GROUP BY")
				}
			}
		} else {
			for {
				gExpr := p.parseExpression(precLowest)
				if len(p.errors) > 0 {
					return nil, fmt.Errorf("syntax error in GROUP BY clause: %s", strings.Join(p.errors, "; "))
				}
				if gExpr == nil {
					return nil, fmt.Errorf("syntax error: empty or invalid expression in GROUP BY clause")
				}
				stmt.GroupBy = append(stmt.GroupBy, gExpr)
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
		if len(stmt.GroupBy) == 0 {
			return nil, fmt.Errorf("syntax error: empty or invalid expression in GROUP BY clause")
		}
	}

	// 6. HAVING clause
	if p.curTokenIs(TokenHaving) {
		p.nextToken() // eat HAVING
		stmt.Having = p.parseExpression(precLowest)
		if len(p.errors) > 0 {
			return nil, fmt.Errorf("syntax error in HAVING clause: %s", strings.Join(p.errors, "; "))
		}
		if stmt.Having == nil {
			return nil, fmt.Errorf("syntax error: empty or invalid expression in HAVING clause")
		}
		if ContainsWindowFunction(stmt.Having) {
			return nil, fmt.Errorf("window functions are not allowed in HAVING")
		}
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
			if len(p.errors) > 0 {
				return nil, fmt.Errorf("syntax error in ORDER BY clause: %s", strings.Join(p.errors, "; "))
			}
			if oExpr == nil {
				return nil, fmt.Errorf("syntax error: empty or invalid expression in ORDER BY clause")
			}
			desc := false
			if p.curTokenIs(TokenDesc) {
				desc = true
				p.nextToken()
			} else if p.curTokenIs(TokenAsc) {
				p.nextToken()
			}
			var nullsFirst *bool
			if p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "NULLS") {
				p.nextToken() // eat NULLS
				if p.curTokenIs(TokenFirst) || (p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "FIRST")) {
					nf := true
					nullsFirst = &nf
					p.nextToken()
				} else if p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "LAST") {
					nf := false
					nullsFirst = &nf
					p.nextToken()
				}
			}
			stmt.OrderBy = append(stmt.OrderBy, OrderByItem{Expr: oExpr, Desc: desc, NullsFirst: nullsFirst})
			if p.curTokenIs(TokenComma) {
				p.nextToken()
			} else {
				break
			}
		}
		if len(stmt.OrderBy) == 0 {
			return nil, fmt.Errorf("syntax error: empty or invalid expression in ORDER BY clause")
		}
	}

	// 8. LIMIT, OFFSET and FETCH clauses (Standard ANSI SQL:2008 & T-SQL)
	for p.curTokenIs(TokenLimit) || p.curTokenIs(TokenOffset) || p.curTokenIs(TokenFetch) {
		if p.curTokenIs(TokenLimit) {
			p.nextToken() // eat LIMIT
			if p.curTokenIs(TokenInt) {
				n, err := strconv.ParseInt(p.curToken.Literal, 10, 64)
				if err != nil || n < 0 {
					return nil, fmt.Errorf("syntax error: LIMIT must not be negative")
				}
				stmt.Limit = n
				p.nextToken()
			} else if p.curTokenIs(TokenMinus) {
				return nil, fmt.Errorf("syntax error: LIMIT must not be negative")
			} else if p.curTokenIs(TokenVariable) || p.curTokenIs(TokenLParen) {
				stmt.LimitExpr = p.parseExpression(precLowest)
				if len(p.errors) > 0 {
					return nil, fmt.Errorf("syntax error in LIMIT clause: %s", strings.Join(p.errors, "; "))
				}
				if stmt.LimitExpr == nil {
					return nil, fmt.Errorf("syntax error: empty or invalid expression in LIMIT clause")
				}
			} else {
				return nil, fmt.Errorf("syntax error: empty or invalid expression in LIMIT clause")
			}
		} else if p.curTokenIs(TokenOffset) {
			p.nextToken() // eat OFFSET
			if p.curTokenIs(TokenInt) {
				n, err := strconv.ParseInt(p.curToken.Literal, 10, 64)
				if err != nil || n < 0 {
					return nil, fmt.Errorf("syntax error: OFFSET must not be negative")
				}
				stmt.Offset = n
				p.nextToken()
			} else if p.curTokenIs(TokenMinus) {
				return nil, fmt.Errorf("syntax error: OFFSET must not be negative")
			} else if p.curTokenIs(TokenVariable) || p.curTokenIs(TokenLParen) {
				stmt.OffsetExpr = p.parseExpression(precLowest)
				if len(p.errors) > 0 {
					return nil, fmt.Errorf("syntax error in OFFSET clause: %s", strings.Join(p.errors, "; "))
				}
				if stmt.OffsetExpr == nil {
					return nil, fmt.Errorf("syntax error: empty or invalid expression in OFFSET clause")
				}
			} else {
				return nil, fmt.Errorf("syntax error: empty or invalid expression in OFFSET clause")
			}
			// Optional ROW or ROWS (e.g. OFFSET 10 ROWS)
			if p.curTokenIs(TokenRow) || p.curTokenIs(TokenRows) {
				p.nextToken()
			}
		} else if p.curTokenIs(TokenFetch) {
			p.nextToken() // eat FETCH
			// Optional FIRST or NEXT (e.g. FETCH NEXT 5 ROWS ONLY)
			if p.curTokenIs(TokenFirst) || p.curTokenIs(TokenNext) {
				p.nextToken()
			}
			if p.curTokenIs(TokenInt) {
				n, err := strconv.ParseInt(p.curToken.Literal, 10, 64)
				if err != nil || n < 0 {
					return nil, fmt.Errorf("syntax error: FETCH count must not be negative")
				}
				stmt.Limit = n
				p.nextToken()
			} else if p.curTokenIs(TokenMinus) {
				return nil, fmt.Errorf("syntax error: FETCH count must not be negative")
			} else if p.curTokenIs(TokenVariable) || p.curTokenIs(TokenLParen) {
				stmt.LimitExpr = p.parseExpression(precLowest)
				if len(p.errors) > 0 {
					return nil, fmt.Errorf("syntax error in FETCH clause: %s", strings.Join(p.errors, "; "))
				}
				if stmt.LimitExpr == nil {
					return nil, fmt.Errorf("syntax error: empty or invalid expression in FETCH clause")
				}
			} else {
				return nil, fmt.Errorf("syntax error: empty or invalid expression in FETCH clause")
			}
			// Optional ROW or ROWS
			if p.curTokenIs(TokenRow) || p.curTokenIs(TokenRows) {
				p.nextToken()
			}
			// Optional ONLY
			if p.curTokenIs(TokenOnly) {
				p.nextToken()
			}
		}
	}

	// 8.5. Set operations: UNION [ALL], INTERSECT, EXCEPT / MINUS
	for isSetOpIdent(p.curToken) {
		op := strings.ToUpper(p.curToken.Literal)
		p.nextToken() // eat UNION / INTERSECT / EXCEPT / MINUS
		if p.curTokenIs(TokenAll) || (p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "ALL")) {
			op = op + " ALL"
			p.nextToken() // eat ALL
		} else if p.curTokenIs(TokenDistinct) {
			p.nextToken() // eat DISTINCT
		}
		if p.curTokenIs(TokenSelect) {
			p.nextToken() // eat SELECT
		} else {
			return nil, fmt.Errorf("expected SELECT after %s, got %s", op, p.curToken.Literal)
		}
		rightStmt, err := p.parseGeneralSelect()
		if err != nil {
			return nil, err
		}
		// Avoid deeply nested SetOps on rightStmt; flatten onto stmt.SetOps
		subOps := rightStmt.SetOps
		rightStmt.SetOps = nil
		stmt.SetOps = append(stmt.SetOps, SetOpBranch{Op: op, Query: rightStmt})
		stmt.SetOps = append(stmt.SetOps, subOps...)
	}

	// 9. Query hints: OPTION ( RECOMPILE, MAXDOP 1, HASH JOIN, LOOP JOIN, MERGE JOIN, FORCE ORDER, ... )
	if p.curTokenIs(TokenOption) {
		p.nextToken() // eat OPTION
		if p.curTokenIs(TokenLParen) {
			p.nextToken() // eat (
			for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
				var hintParts []string
				for !p.curTokenIs(TokenComma) && !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
					hintParts = append(hintParts, p.curToken.Literal)
					p.nextToken()
				}
				if len(hintParts) > 0 {
					stmt.QueryHints = append(stmt.QueryHints, strings.Join(hintParts, " "))
				}
				if p.curTokenIs(TokenComma) {
					p.nextToken() // eat ,
				}
			}
			if p.curTokenIs(TokenRParen) {
				p.nextToken() // eat )
			}
		}
	}

	// 10. Trailing FOR clause: FOR UPDATE, FOR SHARE, FOR JSON, FOR XML, etc.
	if p.curTokenIs(TokenFor) {
		p.nextToken() // eat FOR
		upper := strings.ToUpper(p.curToken.Literal)
		if upper == "UPDATE" || upper == "SHARE" || upper == "JSON" || upper == "XML" || upper == "NO" || upper == "KEY" {
			p.nextToken() // eat UPDATE / SHARE / JSON / XML
			for !p.curTokenIs(TokenEOF) && !p.curTokenIs(TokenSemicolon) && !isReservedClauseKeyword(p.curToken.Type) {
				p.nextToken()
			}
		} else {
			return nil, fmt.Errorf("syntax error: expected UPDATE, SHARE, JSON, or XML after FOR, got %q", p.curToken.Literal)
		}
	}

	if isReservedClauseKeyword(p.curToken.Type) {
		return nil, fmt.Errorf("syntax error: unexpected clause %q at end of statement", p.curToken.Literal)
	}

	return stmt, nil
}

func (p *Parser) parseGeneralInsert() (Statement, error) {
	p.nextToken() // eat INSERT
	if p.curTokenIs(TokenInto) {
		p.nextToken()
	}
	if !p.curTokenIs(TokenIdent) && !p.curTokenIs(TokenVariable) {
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

	outputCols, intoVars, intoTable, intoTableCols := p.parseOutputClause()

	if p.curTokenIs(TokenSelect) {
		p.nextToken() // eat SELECT
		sel, err := p.parseGeneralSelect()
		if err != nil {
			return nil, err
		}
		if p.curTokenIs(TokenReturning) {
			rCols, rVars, rTable, rTableCols := p.parseReturningClause()
			outputCols = append(outputCols, rCols...)
			intoVars = append(intoVars, rVars...)
			if intoTable == "" {
				intoTable = rTable
				intoTableCols = rTableCols
			}
		}
		if p.curTokenIs(TokenSemicolon) {
			p.nextToken()
		}
		return &GeneralInsertStmt{
			Table:         tblName,
			Columns:       cols,
			OutputCols:    outputCols,
			IntoVars:      intoVars,
			IntoTable:     intoTable,
			IntoTableCols: intoTableCols,
			Select:        sel,
		}, nil
	}

	if p.curTokenIs(TokenExec) {
		execStmt, err := p.parseExec()
		if err != nil {
			return nil, err
		}
		return &GeneralInsertStmt{
			Table:         tblName,
			Columns:       cols,
			OutputCols:    outputCols,
			IntoVars:      intoVars,
			IntoTable:     intoTable,
			IntoTableCols: intoTableCols,
			Exec:          execStmt.(*ExecStmt),
		}, nil
	}

	if !p.curTokenIs(TokenValues) {
		return nil, fmt.Errorf("expected VALUES, SELECT, or EXEC in INSERT, got %s", p.curToken.Literal)
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
	if len(tuples) == 0 {
		return nil, fmt.Errorf("syntax error: empty or invalid VALUES clause in INSERT")
	}

	if p.curTokenIs(TokenReturning) {
		rCols, rVars, rTable, rTableCols := p.parseReturningClause()
		outputCols = append(outputCols, rCols...)
		intoVars = append(intoVars, rVars...)
		if intoTable == "" {
			intoTable = rTable
			intoTableCols = rTableCols
		}
	}

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &GeneralInsertStmt{
		Table:         tblName,
		Columns:       cols,
		OutputCols:    outputCols,
		IntoVars:      intoVars,
		IntoTable:     intoTable,
		IntoTableCols: intoTableCols,
		Tuples:        tuples,
	}, nil
}


func (p *Parser) parseWith() (*GeneralSelectStmt, error) {
	p.nextToken() // eat WITH
	if p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "RECURSIVE") {
		p.nextToken() // eat RECURSIVE
	}
	var ctes []CTEClause

	for {
		if !p.curTokenIs(TokenIdent) {
			return nil, fmt.Errorf("expected CTE name after WITH, got %s", p.curToken.Literal)
		}
		cteName := cleanSchemaPrefix(p.curToken.Literal)
		p.nextToken()

		var cteCols []string
		if p.curTokenIs(TokenLParen) {
			p.nextToken() // eat (
			for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
				if p.curTokenIs(TokenIdent) || p.curTokenIs(TokenString) {
					cteCols = append(cteCols, cleanSchemaPrefix(p.curToken.Literal))
				}
				p.nextToken()
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

		innerSQL := innerSelect.String()
		// If CTE body contains set operations (e.g., UNION ALL in recursive CTE), collect remaining tokens up to closing )
		if !p.curTokenIs(TokenRParen) {
			depth := 1
			var extra []string
			for depth > 0 && !p.curTokenIs(TokenEOF) {
				if p.curTokenIs(TokenLParen) {
					depth++
					extra = append(extra, "(")
				} else if p.curTokenIs(TokenRParen) {
					depth--
					if depth == 0 {
						break
					}
					extra = append(extra, ")")
				} else if p.curTokenIs(TokenString) {
					extra = append(extra, "'"+strings.ReplaceAll(p.curToken.Literal, "'", "''")+"'")
				} else {
					extra = append(extra, p.curToken.Literal)
				}
				p.nextToken()
			}
			if len(extra) > 0 {
				var sb strings.Builder
				for i, tok := range extra {
					if i > 0 {
						prev := extra[i-1]
						if tok != "," && tok != ")" && tok != "." && prev != "(" && prev != "." {
							sb.WriteByte(' ')
						}
					}
					sb.WriteString(tok)
				}
				innerSQL = innerSQL + " " + sb.String()
			}
		}

		if !p.curTokenIs(TokenRParen) {
			return nil, fmt.Errorf("expected ) closing CTE")
		}
		p.nextToken() // eat )

		ctes = append(ctes, CTEClause{
			Name:     cteName,
			Columns:  cteCols,
			Query:    innerSelect,
			InnerSQL: innerSQL,
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

func (p *Parser) parseColumnAliasList() []string {
	if !p.curTokenIs(TokenLParen) {
		return nil
	}
	p.nextToken() // eat (
	var cols []string
	for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
		if p.curTokenIs(TokenIdent) || p.curTokenIs(TokenString) {
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
		p.nextToken() // eat )
	}
	return cols
}


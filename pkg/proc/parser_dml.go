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
		p.nextToken() // eat col
		p.nextToken() // eat INTO

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

	stmt, err := p.parseGeneralSelect()
	if err != nil {
		return nil, err
	}
	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}
	return stmt, nil
}

func (p *Parser) parseOutputCols() []string {
	if !p.curTokenIs(TokenOutput) {
		return nil
	}
	p.nextToken() // eat OUTPUT
	var cols []string
	for !p.curTokenIs(TokenSelect) && !p.curTokenIs(TokenValues) && !p.curTokenIs(TokenWhere) && !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
		col := p.curToken.Literal
		p.nextToken()
		if p.curTokenIs(TokenAs) {
			p.nextToken()
			col = p.curToken.Literal
			p.nextToken()
		}
		cols = append(cols, col)
		if p.curTokenIs(TokenComma) {
			p.nextToken()
		} else {
			break
		}
	}
	return cols
}

func (p *Parser) parseInsert() (Statement, error) {
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

	outputCols := p.parseOutputCols()

	if p.curTokenIs(TokenSelect) {
		p.nextToken() // eat SELECT
		sel, err := p.parseGeneralSelect()
		if err != nil {
			return nil, err
		}
		if p.curTokenIs(TokenSemicolon) {
			p.nextToken()
		}
		return &GeneralInsertStmt{
			Table:      tblName,
			Columns:    cols,
			OutputCols: outputCols,
			Select:     sel,
		}, nil
	}

	if !p.curTokenIs(TokenValues) {
		return nil, fmt.Errorf("expected VALUES or SELECT after table name")
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
		Table:      tblName,
		Columns:    cols,
		Values:     vals,
		OutputCols: outputCols,
	}, nil
}

func (p *Parser) parseUpdate() (Statement, error) {
	p.nextToken() // eat UPDATE
	if !p.curTokenIs(TokenIdent) {
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

	outputCols := p.parseOutputCols()

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
				WhereCol:    cleanSchemaPrefix(colRef.Name),
				WhereVal:    bin.Right,
				OutputCols:  outputCols,
			}, nil
		}
	}

	return &UpdateWhereStmt{
		Table:       tblName,
		Assignments: assigns,
		OutputCols:  outputCols,
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
	tblName := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken()

	outputCols := p.parseOutputCols()

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
				Table:      tblName,
				WhereCol:   cleanSchemaPrefix(colRef.Name),
				WhereVal:   bin.Right,
				OutputCols: outputCols,
			}, nil
		}
	}

	return &DeleteWhereStmt{
		Table:      tblName,
		OutputCols: outputCols,
		Where:      whereExpr,
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
			return p.parseCreateTable()
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

func (p *Parser) parseGeneralSelect() (*GeneralSelectStmt, error) {
	stmt := &GeneralSelectStmt{Limit: -1}

	for p.curTokenIs(TokenDistinct) || p.curTokenIs(TokenTop) {
		if p.curTokenIs(TokenDistinct) {
			stmt.Distinct = true
			p.nextToken() // eat DISTINCT
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
		} else if (p.curTokenIs(TokenIdent) || p.curTokenIs(TokenString)) && !isReservedAfterProjection(p.curToken.Type) {
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
		if p.curTokenIs(TokenLParen) {
			p.nextToken() // eat (
			if p.curTokenIs(TokenSelect) {
				p.nextToken() // eat SELECT
			}
			subSel, err := p.parseGeneralSelect()
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
			} else if p.curTokenIs(TokenIdent) && !isReservedAfterFromOrJoin(p.curToken.Type) {
				alias = cleanSchemaPrefix(p.curToken.Literal)
				p.nextToken()
			}
			fromHints := p.parseTableHints()

			stmt.From = TableRef{
				Name:     subSel.String(),
				Alias:    alias,
				Subquery: subSel,
				Hints:    fromHints,
			}
		} else {
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
			} else if p.curTokenIs(TokenIdent) && !isReservedAfterFromOrJoin(p.curToken.Type) {
				if (strings.EqualFold(p.curToken.Literal, "HASH") || strings.EqualFold(p.curToken.Literal, "LOOP") || strings.EqualFold(p.curToken.Literal, "MERGE")) && p.peekToken.Type == TokenJoin {
					// Do not consume HASH/LOOP/MERGE as alias when followed by JOIN
				} else {
					alias = cleanSchemaPrefix(p.curToken.Literal)
					p.nextToken()
				}
			}
			fromHints := p.parseTableHints()

			stmt.From = TableRef{Name: tblName, Alias: alias, IsTVF: isTVF, TVFArgs: tvfArgs, Hints: fromHints}
		}
	}

	// 3. JOIN & APPLY clauses
	for p.curTokenIs(TokenJoin) || p.curTokenIs(TokenInner) || p.curTokenIs(TokenLeft) || p.curTokenIs(TokenRight) || p.curTokenIs(TokenFull) || p.curTokenIs(TokenCross) || p.curTokenIs(TokenOuter) ||
		(p.curTokenIs(TokenIdent) && (strings.EqualFold(p.curToken.Literal, "HASH") || strings.EqualFold(p.curToken.Literal, "LOOP") || strings.EqualFold(p.curToken.Literal, "MERGE"))) {
		joinType := "INNER"
		isApply := false
		if p.curTokenIs(TokenIdent) && (strings.EqualFold(p.curToken.Literal, "HASH") || strings.EqualFold(p.curToken.Literal, "LOOP") || strings.EqualFold(p.curToken.Literal, "MERGE")) {
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
			if p.curTokenIs(TokenIdent) && (strings.EqualFold(p.curToken.Literal, "HASH") || strings.EqualFold(p.curToken.Literal, "LOOP") || strings.EqualFold(p.curToken.Literal, "MERGE")) {
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
				if joinHint != "" {
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
			if p.curTokenIs(TokenIdent) && (strings.EqualFold(p.curToken.Literal, "HASH") || strings.EqualFold(p.curToken.Literal, "LOOP") || strings.EqualFold(p.curToken.Literal, "MERGE")) {
				joinHint = strings.ToUpper(p.curToken.Literal)
				p.nextToken()
			}
			if !p.curTokenIs(TokenJoin) {
				return nil, fmt.Errorf("expected JOIN after RIGHT, got %s", p.curToken.Literal)
			}
			p.nextToken() // eat JOIN
			if joinHint != "" {
				joinType = joinType + " " + joinHint
			}
		} else if p.curTokenIs(TokenFull) {
			joinType = "FULL"
			p.nextToken() // eat FULL
			if p.curTokenIs(TokenOuter) {
				p.nextToken() // eat OUTER
			}
			var joinHint string
			if p.curTokenIs(TokenIdent) && (strings.EqualFold(p.curToken.Literal, "HASH") || strings.EqualFold(p.curToken.Literal, "LOOP") || strings.EqualFold(p.curToken.Literal, "MERGE")) {
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
			if p.curTokenIs(TokenIdent) && (strings.EqualFold(p.curToken.Literal, "HASH") || strings.EqualFold(p.curToken.Literal, "LOOP") || strings.EqualFold(p.curToken.Literal, "MERGE")) {
				joinHint = strings.ToUpper(p.curToken.Literal)
				p.nextToken()
			}
			if !p.curTokenIs(TokenJoin) {
				return nil, fmt.Errorf("expected JOIN, got %s", p.curToken.Literal)
			}
			p.nextToken() // eat JOIN
			if joinHint != "" {
				joinType = "INNER " + joinHint
			}
		} else if p.curTokenIs(TokenJoin) {
			p.nextToken() // eat JOIN
		}

		if p.curTokenIs(TokenLParen) {
			p.nextToken() // eat (
			if p.curTokenIs(TokenSelect) {
				p.nextToken() // eat SELECT
			}
			subSel, err := p.parseGeneralSelect()
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
			} else if p.curTokenIs(TokenIdent) && !isReservedAfterFromOrJoin(p.curToken.Type) {
				if (strings.EqualFold(p.curToken.Literal, "HASH") || strings.EqualFold(p.curToken.Literal, "LOOP") || strings.EqualFold(p.curToken.Literal, "MERGE")) && p.peekToken.Type == TokenJoin {
					// Do not consume HASH/LOOP/MERGE as alias when followed by JOIN
				} else {
					jAlias = cleanSchemaPrefix(p.curToken.Literal)
					p.nextToken()
				}
			}
			jHints := p.parseTableHints()

			var onExpr Expr
			if !isApply {
				if !p.curTokenIs(TokenOn) {
					return nil, fmt.Errorf("expected ON after JOIN table, got %s", p.curToken.Literal)
				}
				p.nextToken() // eat ON
				onExpr = p.parseExpression(precLowest)
			}

			stmt.Joins = append(stmt.Joins, JoinClause{
				Type:  joinType,
				Table: TableRef{Name: subSel.String(), Alias: jAlias, Subquery: subSel, Hints: jHints},
				On:    onExpr,
			})
		} else {
			if !p.curTokenIs(TokenIdent) {
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

			jAlias := jTbl
			if p.curTokenIs(TokenAs) {
				p.nextToken()
				if p.curTokenIs(TokenIdent) {
					jAlias = cleanSchemaPrefix(p.curToken.Literal)
					p.nextToken()
				}
			} else if p.curTokenIs(TokenIdent) && !isReservedAfterFromOrJoin(p.curToken.Type) {
				if (strings.EqualFold(p.curToken.Literal, "HASH") || strings.EqualFold(p.curToken.Literal, "LOOP") || strings.EqualFold(p.curToken.Literal, "MERGE")) && p.peekToken.Type == TokenJoin {
					// Do not consume HASH/LOOP/MERGE as alias when followed by JOIN
				} else {
					jAlias = cleanSchemaPrefix(p.curToken.Literal)
					p.nextToken()
				}
			}
			jHints := p.parseTableHints()

			var onExpr Expr
			if !isApply {
				if !p.curTokenIs(TokenOn) {
					return nil, fmt.Errorf("expected ON after JOIN table, got %s", p.curToken.Literal)
				}
				p.nextToken() // eat ON
				onExpr = p.parseExpression(precLowest)
			}

			stmt.Joins = append(stmt.Joins, JoinClause{
				Type:  joinType,
				Table: TableRef{Name: jTbl, Alias: jAlias, IsTVF: isTVF, TVFArgs: tvfArgs, Hints: jHints},
				On:    onExpr,
			})
		}
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

	// 8. LIMIT, OFFSET and FETCH clauses (Standard ANSI SQL:2008 & T-SQL)
	for p.curTokenIs(TokenLimit) || p.curTokenIs(TokenOffset) || p.curTokenIs(TokenFetch) {
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
				n, _ := strconv.ParseInt(p.curToken.Literal, 10, 64)
				stmt.Limit = n
				p.nextToken()
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

	outputCols := p.parseOutputCols()

	if p.curTokenIs(TokenSelect) {
		p.nextToken() // eat SELECT
		sel, err := p.parseGeneralSelect()
		if err != nil {
			return nil, err
		}
		if p.curTokenIs(TokenSemicolon) {
			p.nextToken()
		}
		return &GeneralInsertStmt{
			Table:      tblName,
			Columns:    cols,
			OutputCols: outputCols,
			Select:     sel,
		}, nil
	}

	if !p.curTokenIs(TokenValues) {
		return nil, fmt.Errorf("expected VALUES or SELECT in INSERT, got %s", p.curToken.Literal)
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
		Table:      tblName,
		Columns:    cols,
		OutputCols: outputCols,
		Tuples:     tuples,
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


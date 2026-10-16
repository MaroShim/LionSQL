package proc

import (
	"fmt"
	"strings"
)

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

		// Skip optional (length/precision, scale) e.g. VARCHAR(50), DECIMAL(15, 2)
		if p.curTokenIs(TokenLParen) {
			p.nextToken()
			for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
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

		// Skip optional (length/precision, scale) e.g. VARCHAR(50), DECIMAL(15, 2)
		if p.curTokenIs(TokenLParen) {
			p.nextToken()
			for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
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

	// Skip optional (length/precision, scale) e.g. VARCHAR(50), DECIMAL(15, 2)
	if p.curTokenIs(TokenLParen) {
		p.nextToken()
		for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
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


func (p *Parser) ParseCreateIndex() (*CreateIndexStmt, error) {
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
	var exprStr string
	var exprCol string
	var exprPath string

	isExpr := p.curTokenIs(TokenLParen) ||
		(p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "JSON_VALUE")) ||
		(p.curTokenIs(TokenIdent) && (p.peekTokenIs(TokenJSONExtract) || p.peekTokenIs(TokenJSONExtractText)))

	if isExpr {
		expr := p.parseExpression(precLowest)
		if expr == nil {
			return nil, fmt.Errorf("invalid expression in CREATE INDEX")
		}
		if p.curTokenIs(TokenRParen) {
			p.nextToken()
		}

		exprStr = expr.String()
		col, path, ok := ExtractJSONExprInfo(expr)
		if ok {
			exprCol = col
			exprPath = path
			cols = append(cols, col)
		} else {
			cols = append(cols, exprStr)
		}
	} else {
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
	}

	var includeCols []string
	if strings.EqualFold(p.curToken.Literal, "INCLUDE") {
		p.nextToken()
		if p.curTokenIs(TokenLParen) {
			p.nextToken()
			for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
				if p.curTokenIs(TokenIdent) {
					includeCols = append(includeCols, cleanSchemaPrefix(p.curToken.Literal))
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
		}
	}

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &CreateIndexStmt{
		Name:        idxName,
		Table:       tblName,
		Columns:     cols,
		IncludeCols: includeCols,
		Unique:      isUnique,
		Expression:  exprStr,
		ExprColumn:  exprCol,
		ExprPath:    exprPath,
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


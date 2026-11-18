package proc

import (
	"fmt"
	"strconv"
	"strings"

	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
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

		// Optional 'AS' keyword e.g. @param AS VARCHAR(40)
		if p.curTokenIs(TokenAs) {
			p.nextToken()
		}

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

		// Optional default value e.g. = NULL, = 0, = 'x'
		if p.curTokenIs(TokenAssign) || p.curTokenIs(TokenEq) {
			p.nextToken()
			if def := p.parseExpression(precLowest); def != nil {
				params[len(params)-1].Default = def
			} else if p.curTokenIs(TokenIdent) || p.curTokenIs(TokenInt) || p.curTokenIs(TokenFloat) || p.curTokenIs(TokenString) || p.curTokenIs(TokenBinary) || p.curTokenIs(TokenNull) {
				p.nextToken()
			}
		}

		// Skip optional NULL keyword e.g. @p INT NULL
		if p.curTokenIs(TokenNull) {
			p.nextToken()
		}

		// Optional OUTPUT / OUT keyword marks a by-reference parameter
		if p.curTokenIs(TokenOutput) || (p.curTokenIs(TokenIdent) && (strings.EqualFold(p.curToken.Literal, "OUTPUT") || strings.EqualFold(p.curToken.Literal, "OUT"))) {
			params[len(params)-1].Output = true
			p.nextToken()
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

	var body Statement
	if p.curTokenIs(TokenLParen) {
		// Single paren-enclosed statement e.g. CREATE PROCEDURE p AS (SELECT ...)
		p.nextToken() // eat '('
		var err error
		body, err = p.parseStatement()
		if err != nil {
			return nil, err
		}
		if p.curTokenIs(TokenRParen) {
			p.nextToken() // eat ')'
		}
	} else if p.curTokenIs(TokenBegin) {
		var err error
		body, err = p.parseStatement()
		if err != nil {
			return nil, err
		}
	} else {
		// Multi-statement procedure body without explicit BEGIN ... END
		var stmts []Statement
		for !p.curTokenIs(TokenEOF) {
			s, err := p.parseStatement()
			if err != nil {
				return nil, err
			}
			if s != nil {
				stmts = append(stmts, s)
			}
			if p.curTokenIs(TokenSemicolon) {
				p.nextToken()
			}
		}
		if len(stmts) == 1 {
			body = stmts[0]
		} else {
			body = &BlockStmt{Statements: stmts}
		}
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

	// Optional OR ALTER / OR REPLACE
	if p.curTokenIs(TokenOr) {
		p.nextToken()
		if !p.curTokenIs(TokenAlter) && !strings.EqualFold(p.curToken.Literal, "REPLACE") {
			return nil, fmt.Errorf("expected ALTER or REPLACE after OR, got %s", p.curToken.Literal)
		}
		p.nextToken()
	}

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

		// Optional 'AS' keyword e.g. @param AS VARCHAR(40)
		if p.curTokenIs(TokenAs) {
			p.nextToken()
		}

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

		// Skip optional default value e.g. = NULL, = 0
		if p.curTokenIs(TokenAssign) || p.curTokenIs(TokenEq) {
			p.nextToken()
			if p.curTokenIs(TokenIdent) || p.curTokenIs(TokenInt) || p.curTokenIs(TokenFloat) || p.curTokenIs(TokenString) || p.curTokenIs(TokenBinary) || p.curTokenIs(TokenNull) {
				p.nextToken()
			}
		}

		// Skip optional NULL keyword e.g. @p INT NULL
		if p.curTokenIs(TokenNull) {
			p.nextToken()
		}

		// Skip optional OUTPUT / OUT keyword
		if p.curTokenIs(TokenOutput) || (p.curTokenIs(TokenIdent) && (strings.EqualFold(p.curToken.Literal, "OUTPUT") || strings.EqualFold(p.curToken.Literal, "OUT"))) {
			p.nextToken()
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

	if !p.curTokenIs(TokenIdent) && !p.curTokenIs(TokenTrigger) && !p.curTokenIs(TokenTable) {
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

	var body Statement
	if p.curTokenIs(TokenString) {
		// PostgreSQL dollar-quoted or string literal body: AS $$ ... $$ LANGUAGE plpgsql;
		p.nextToken() // eat string
		// skip optional LANGUAGE and trailing semicolons
		for p.curTokenIs(TokenIdent) || p.curTokenIs(TokenSemicolon) {
			p.nextToken()
		}
		body = &BlockStmt{Statements: []Statement{}}
	} else if p.curTokenIs(TokenLParen) {
		// Single paren-enclosed statement e.g. CREATE PROCEDURE p AS (SELECT ...)
		p.nextToken() // eat '('
		var err error
		body, err = p.parseStatement()
		if err != nil {
			return nil, err
		}
		if p.curTokenIs(TokenRParen) {
			p.nextToken() // eat ')'
		}
	} else if p.curTokenIs(TokenBegin) {
		var err error
		body, err = p.parseStatement()
		if err != nil {
			return nil, err
		}
	} else {
		// Multi-statement procedure body without explicit BEGIN ... END
		var stmts []Statement
		for !p.curTokenIs(TokenEOF) {
			s, err := p.parseStatement()
			if err != nil {
				return nil, err
			}
			if s != nil {
				stmts = append(stmts, s)
			}
			if p.curTokenIs(TokenSemicolon) {
				p.nextToken()
			}
		}
		if len(stmts) == 1 {
			body = stmts[0]
		} else {
			body = &BlockStmt{Statements: stmts}
		}
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

	// Optional OR ALTER / OR REPLACE
	if p.curTokenIs(TokenOr) {
		p.nextToken()
		if !p.curTokenIs(TokenAlter) && !strings.EqualFold(p.curToken.Literal, "REPLACE") {
			return nil, fmt.Errorf("expected ALTER or REPLACE after OR, got %s", p.curToken.Literal)
		}
		p.nextToken()
	}

	// Optional TEMP / TEMPORARY
	if p.curTokenIs(TokenIdent) && (strings.EqualFold(p.curToken.Literal, "TEMP") || strings.EqualFold(p.curToken.Literal, "TEMPORARY")) {
		p.nextToken()
	}

	if !p.curTokenIs(TokenTrigger) {
		return nil, fmt.Errorf("expected TRIGGER, got %s", p.curToken.Literal)
	}
	p.nextToken()

	// Optional IF NOT EXISTS
	if p.curTokenIs(TokenIf) {
		p.nextToken()
		if p.curTokenIs(TokenNot) {
			p.nextToken()
			if p.curTokenIs(TokenExists) {
				p.nextToken()
			}
		}
	}

	if !p.curTokenIs(TokenIdent) {
		return nil, fmt.Errorf("expected trigger name, got %s", p.curToken.Literal)
	}
	trgName := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken()

	var tableName string
	var timing = "AFTER"
	events := make([]string, 0)

	if p.curTokenIs(TokenOn) {
		// T-SQL Syntax: CREATE TRIGGER trg ON table [AFTER|INSTEAD OF] [INSERT, UPDATE, DELETE] AS BEGIN ... END
		p.nextToken()
		if !p.curTokenIs(TokenIdent) {
			return nil, fmt.Errorf("expected table name in ON clause, got %s", p.curToken.Literal)
		}
		tableName = cleanSchemaPrefix(p.curToken.Literal)
		p.nextToken()

		if p.curTokenIs(TokenAfter) || p.curTokenIs(TokenFor) {
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
			return nil, fmt.Errorf("expected at least one trigger event (INSERT, UPDATE, DELETE), got %s", p.curToken.Literal)
		}
	} else {
		// SQLite Syntax: CREATE TRIGGER trg [BEFORE | AFTER | INSTEAD OF | FOR] [INSERT | UPDATE [OF col,...] | DELETE] ON table ...
		if p.curTokenIs(TokenBefore) {
			timing = "BEFORE"
			p.nextToken()
		} else if p.curTokenIs(TokenAfter) || p.curTokenIs(TokenFor) {
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

		for {
			switch p.curToken.Type {
			case TokenInsert:
				events = append(events, "INSERT")
				p.nextToken()
			case TokenUpdate:
				events = append(events, "UPDATE")
				p.nextToken()
				if p.curTokenIs(TokenOf) {
					p.nextToken()
					for p.curTokenIs(TokenIdent) {
						p.nextToken()
						if p.curTokenIs(TokenComma) {
							p.nextToken()
						} else {
							break
						}
					}
				}
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

		if !p.curTokenIs(TokenOn) {
			return nil, fmt.Errorf("expected ON after trigger event, got %s", p.curToken.Literal)
		}
		p.nextToken()

		if !p.curTokenIs(TokenIdent) {
			return nil, fmt.Errorf("expected table name in ON clause, got %s", p.curToken.Literal)
		}
		tableName = cleanSchemaPrefix(p.curToken.Literal)
		p.nextToken()

		// Optional: FOR EACH ROW
		if p.curTokenIs(TokenFor) {
			p.nextToken()
			for !p.curTokenIs(TokenBegin) && !p.curTokenIs(TokenAs) && !p.curTokenIs(TokenWhen) && !p.curTokenIs(TokenEOF) && !(p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "EXECUTE")) {
				p.nextToken()
			}
		}

		// Optional: WHEN condition
		if p.curTokenIs(TokenWhen) {
			p.nextToken()
			for !p.curTokenIs(TokenBegin) && !p.curTokenIs(TokenAs) && !p.curTokenIs(TokenEOF) && !(p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "EXECUTE")) {
				p.nextToken()
			}
		}
	}

	if len(events) == 0 {
		return nil, fmt.Errorf("expected at least one event (INSERT, UPDATE, DELETE) in trigger")
	}

	if p.curTokenIs(TokenAs) {
		p.nextToken()
	}

	var body Statement
	if p.curTokenIs(TokenIdent) && strings.EqualFold(p.curToken.Literal, "EXECUTE") {
		p.nextToken() // eat EXECUTE
		for !p.curTokenIs(TokenEOF) && !p.curTokenIs(TokenSemicolon) {
			p.nextToken()
		}
		body = &BlockStmt{Statements: []Statement{}}
	} else if p.curTokenIs(TokenLParen) {
		// Single paren-enclosed statement e.g. CREATE PROCEDURE p AS (SELECT ...)
		p.nextToken() // eat '('
		var err error
		body, err = p.parseStatement()
		if err != nil {
			return nil, err
		}
		if p.curTokenIs(TokenRParen) {
			p.nextToken() // eat ')'
		}
	} else if p.curTokenIs(TokenBegin) {
		var err error
		body, err = p.parseStatement()
		if err != nil {
			return nil, err
		}
	} else {
		// Multi-statement procedure body without explicit BEGIN ... END
		var stmts []Statement
		for !p.curTokenIs(TokenEOF) {
			s, err := p.parseStatement()
			if err != nil {
				return nil, err
			}
			if s != nil {
				stmts = append(stmts, s)
			}
			if p.curTokenIs(TokenSemicolon) {
				p.nextToken()
			}
		}
		if len(stmts) == 1 {
			body = stmts[0]
		} else {
			body = &BlockStmt{Statements: stmts}
		}
	}

	return &CreateTriggerStmt{
		Name:   trgName,
		Table:  tableName,
		Timing: timing,
		Events: events,
		Body:   body,
	}, nil
}


// CleanDefaultExpr strips redundant outer parentheses from a SQL DEFAULT expression
// while preserving function call argument parentheses (e.g. "((0))" -> "0", "(NEWID7())" -> "NEWID7()").
func CleanDefaultExpr(s string) string {
	s = strings.TrimSpace(s)
	for strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		inner := s[1 : len(s)-1]
		depth := 0
		balanced := true
		inSingleQuote := false
		for i := 0; i < len(inner); i++ {
			ch := inner[i]
			if ch == '\'' {
				inSingleQuote = !inSingleQuote
				continue
			}
			if inSingleQuote {
				continue
			}
			if ch == '(' {
				depth++
			} else if ch == ')' {
				depth--
				if depth < 0 {
					balanced = false
					break
				}
			}
		}
		if balanced && depth == 0 {
			if strings.HasSuffix(s, "()") && !strings.HasPrefix(inner, "(") && strings.Index(s, "(") == len(s)-2 {
				break
			}
			s = strings.TrimSpace(inner)
		} else {
			break
		}
	}
	return s
}

// ParseCreateTable parses a CREATE [TEMP|TEMPORARY] TABLE [IF NOT EXISTS] statement into a CreateTableStmt AST.
func (p *Parser) ParseCreateTable() (*CreateTableStmt, error) {
	if !p.curTokenIs(TokenCreate) {
		return nil, fmt.Errorf("expected CREATE, got %s", p.curToken.Literal)
	}
	p.nextToken() // eat CREATE

	isTemp := false
	if p.curTokenIs(TokenIdent) && (strings.EqualFold(p.curToken.Literal, "TEMP") || strings.EqualFold(p.curToken.Literal, "TEMPORARY")) {
		isTemp = true
		p.nextToken()
	}

	if !p.curTokenIs(TokenTable) {
		return nil, fmt.Errorf("expected TABLE after CREATE, got %s", p.curToken.Literal)
	}
	p.nextToken() // eat TABLE

	ifNotExists := false
	if p.curTokenIs(TokenIf) || strings.EqualFold(p.curToken.Literal, "IF") {
		p.nextToken()
		if p.curTokenIs(TokenNot) || strings.EqualFold(p.curToken.Literal, "NOT") {
			p.nextToken()
		}
		if p.curTokenIs(TokenExists) || strings.EqualFold(p.curToken.Literal, "EXISTS") {
			ifNotExists = true
			p.nextToken()
		}
	}

	if p.curToken.Literal == "" || p.curTokenIs(TokenLParen) || p.curTokenIs(TokenEOF) {
		return nil, fmt.Errorf("invalid CREATE TABLE syntax: missing table name")
	}

	var nameTokens []string
	for !p.curTokenIs(TokenLParen) && !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
		nameTokens = append(nameTokens, p.curToken.IdentLiteral())
		p.nextToken()
	}
	var b strings.Builder
	for i, tok := range nameTokens {
		if i > 0 {
			prev := nameTokens[i-1]
			if prev != "" && tok != "" &&
				(isIdentCharByte(prev[len(prev)-1]) && isIdentCharByte(tok[0])) &&
				(prev != "{" && tok != "}") {
				b.WriteString(tok)
				continue
			}
			b.WriteByte(' ')
		}
		b.WriteString(tok)
	}
	rawTableName := b.String()
	if len(nameTokens) == 3 && nameTokens[0] == "{" && nameTokens[2] == "}" {
		rawTableName = "{" + nameTokens[1] + "}"
	} else {
		rawTableName = strings.ReplaceAll(rawTableName, " . ", ".")
		rawTableName = strings.ReplaceAll(rawTableName, ". ", ".")
		rawTableName = strings.ReplaceAll(rawTableName, " .", ".")
	}
	tableName := cleanSchemaPrefix(rawTableName)
	if tableName == "" {
		return nil, fmt.Errorf("invalid CREATE TABLE syntax: missing table name")
	}

	if !p.curTokenIs(TokenLParen) {
		return nil, fmt.Errorf("invalid CREATE TABLE syntax: missing opening parenthesis")
	}
	p.nextToken() // eat '('

	stmt := &CreateTableStmt{
		Name:            tableName,
		IfNotExists:     ifNotExists,
		Temporary:       isTemp,
		Durability:      storage.DurabilitySchemaAndData,
		MemoryOptimized: false,
	}

	if err := p.parseTableBody(stmt); err != nil {
		return nil, err
	}

	if !p.curTokenIs(TokenRParen) {
		return nil, fmt.Errorf("invalid CREATE TABLE syntax: unbalanced parentheses")
	}
	p.nextToken() // eat ')'

	// Parse trailing clauses: WITH (...) and/or PARTITION BY ...
	for !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
		if p.curTokenIs(TokenWith) || strings.EqualFold(p.curToken.Literal, "WITH") {
			p.nextToken()
			if p.curTokenIs(TokenLParen) {
				p.nextToken()
				for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
					key := strings.ToUpper(p.curToken.Literal)
					p.nextToken()
					if p.curTokenIs(TokenAssign) || p.curTokenIs(TokenEq) {
						p.nextToken()
						val := strings.ToUpper(p.curToken.Literal)
						p.nextToken()
						switch key {
						case "DURABILITY":
							if val == "SCHEMA_ONLY" {
								stmt.Durability = storage.DurabilitySchemaOnly
							} else if val == "SCHEMA_AND_DATA" {
								stmt.Durability = storage.DurabilitySchemaAndData
							}
						case "MEMORY_OPTIMIZED":
							if val == "ON" || val == "TRUE" || val == "1" {
								stmt.MemoryOptimized = true
							} else if val == "OFF" || val == "FALSE" || val == "0" {
								stmt.MemoryOptimized = false
							}
						}
					}
					if p.curTokenIs(TokenComma) {
						p.nextToken()
					}
				}
				if p.curTokenIs(TokenRParen) {
					p.nextToken()
				}
			}
		} else if strings.EqualFold(p.curToken.Literal, "PARTITION") {
			p.nextToken() // eat PARTITION
			if p.curTokenIs(TokenBy) || strings.EqualFold(p.curToken.Literal, "BY") {
				p.nextToken() // eat BY
			}
			partDef, err := p.parsePartitionByClause()
			if err != nil {
				return nil, err
			}
			stmt.Partition = partDef
		} else if strings.EqualFold(p.curToken.Literal, "ON") || strings.EqualFold(p.curToken.Literal, "TEXTIMAGE_ON") || strings.EqualFold(p.curToken.Literal, "FILESTREAM_ON") {
			p.nextToken()
			if !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
				if p.curTokenIs(TokenLParen) {
					_ = p.parseRawParenContent()
				} else {
					p.nextToken()
					if p.curTokenIs(TokenLParen) {
						_ = p.parseRawParenContent()
					}
				}
			}
		} else {
			break
		}
	}

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return stmt, nil
}

func (p *Parser) parsePartitionByClause() (*storage.PartitionDef, error) {
	if p.curTokenIs(TokenEOF) || p.curTokenIs(TokenSemicolon) {
		return nil, fmt.Errorf("expected partition expression after PARTITION BY")
	}
	firstTok := p.curToken.Literal
	p.nextToken()
	if p.curTokenIs(TokenLParen) {
		fn := strings.ToUpper(strings.TrimSpace(firstTok))
		p.nextToken() // eat '('
		col := cleanSchemaPrefix(p.curToken.Literal)
		p.nextToken()
		if p.curTokenIs(TokenRParen) {
			p.nextToken()
		}
		return &storage.PartitionDef{
			Expr:   fmt.Sprintf("%s(%s)", fn, col),
			Column: col,
			Func:   fn,
		}, nil
	}
	col := cleanSchemaPrefix(firstTok)
	return &storage.PartitionDef{
		Expr:   col,
		Column: col,
		Func:   "COLUMN",
	}, nil
}

func (p *Parser) parseTableBody(stmt *CreateTableStmt) error {
	hasRowVersion := false
	for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
		if p.curTokenIs(TokenComma) {
			p.nextToken()
			continue
		}

		// Check if this item is a table-level constraint:
		// [CONSTRAINT <name>] (PRIMARY KEY | FOREIGN KEY | UNIQUE | CHECK)
		if p.isTableConstraintStart() {
			if err := p.parseTableLevelConstraint(stmt); err != nil {
				return err
			}
		} else {
			col, err := p.parseSingleColumnDef(stmt.Name)
			if err != nil {
				return err
			}
			if col.IsRowVersion {
				if hasRowVersion {
					return fmt.Errorf("a table can only have one rowversion column")
				}
				hasRowVersion = true
			}
			if col.ForeignKey != nil {
				stmt.ForeignKeys = append(stmt.ForeignKeys, *col.ForeignKey)
			}
			stmt.Columns = append(stmt.Columns, col)
		}

		if p.curTokenIs(TokenComma) {
			p.nextToken()
		} else {
			break
		}
	}

	// Apply table-level PRIMARY KEY columns onto matching column definitions
	for _, pkCol := range stmt.PrimaryKeyCols {
		for i := range stmt.Columns {
			if strings.EqualFold(stmt.Columns[i].Name, pkCol) {
				stmt.Columns[i].PrimaryKey = true
				stmt.Columns[i].Nullable = false
			}
		}
	}

	return nil
}

func (p *Parser) isTableConstraintStart() bool {
	upper := strings.ToUpper(p.curToken.Literal)
	peekUpper := strings.ToUpper(p.peekToken.Literal)
	switch upper {
	case "CONSTRAINT":
		return true
	case "PRIMARY":
		return peekUpper == "KEY"
	case "FOREIGN":
		return peekUpper == "KEY"
	case "UNIQUE":
		return p.peekTokenIs(TokenLParen) || peekUpper == "CLUSTERED" || peekUpper == "NONCLUSTERED"
	case "CHECK":
		return p.peekTokenIs(TokenLParen)
	default:
		return false
	}
}

func (p *Parser) parseTableLevelConstraint(stmt *CreateTableStmt) error {
	var cName string
	if strings.EqualFold(p.curToken.Literal, "CONSTRAINT") {
		p.nextToken() // eat CONSTRAINT
		cName = cleanSchemaPrefix(p.curToken.Literal)
		p.nextToken()
	}
	return p.parseTableLevelConstraintAfterName(stmt, cName)
}

func (p *Parser) parseTableLevelConstraintAfterName(stmt *CreateTableStmt, cName string) error {
	kw := strings.ToUpper(p.curToken.Literal)
	switch kw {
	case "PRIMARY":
		p.nextToken() // eat PRIMARY
		if strings.EqualFold(p.curToken.Literal, "KEY") {
			p.nextToken() // eat KEY
		}
		if strings.EqualFold(p.curToken.Literal, "CLUSTERED") || strings.EqualFold(p.curToken.Literal, "NONCLUSTERED") {
			p.nextToken()
		}
		cols := p.parseIdentListInParens()
		stmt.PrimaryKeyCols = append(stmt.PrimaryKeyCols, cols...)
		p.skipConstraintTrailingOptions()
		return nil

	case "UNIQUE":
		p.nextToken() // eat UNIQUE
		if strings.EqualFold(p.curToken.Literal, "CLUSTERED") || strings.EqualFold(p.curToken.Literal, "NONCLUSTERED") {
			p.nextToken()
		}
		cols := p.parseIdentListInParens()
		if len(cols) > 0 {
			if cName == "" {
				cName = fmt.Sprintf("uq_%s_%s", stmt.Name, strings.Join(cols, "_"))
			}
			stmt.UniqueIndexes = append(stmt.UniqueIndexes, storage.IndexSchema{
				Name:      cName,
				TableName: stmt.Name,
				Columns:   cols,
				Unique:    true,
			})
		}
		p.skipConstraintTrailingOptions()
		return nil

	case "FOREIGN":
		p.nextToken() // eat FOREIGN
		if strings.EqualFold(p.curToken.Literal, "KEY") {
			p.nextToken() // eat KEY
		}
		childCols := p.parseIdentListInParens()
		fk, err := p.parseReferencesClause(cName, stmt.Name, childCols)
		if err != nil {
			return err
		}
		stmt.ForeignKeys = append(stmt.ForeignKeys, *fk)
		return nil

	case "CHECK":
		p.nextToken() // eat CHECK
		chkExpr := p.parseRawParenContent()
		if chkExpr != "" {
			stmt.CheckConstraints = append(stmt.CheckConstraints, chkExpr)
		}
		return nil

	default:
		// Skip unknown table-level constraint safely until comma or closing paren
		p.skipUntilCommaOrRParen()
		return nil
	}
}

func (p *Parser) skipConstraintTrailingOptions() {
	for !p.curTokenIs(TokenComma) && !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
		if p.curTokenIs(TokenLParen) {
			_ = p.parseRawParenContent()
		} else {
			p.nextToken()
		}
	}
}

func (p *Parser) parseIdentListInParens() []string {
	var cols []string
	if !p.curTokenIs(TokenLParen) {
		return cols
	}
	p.nextToken() // eat '('
	for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
		if p.curTokenIs(TokenComma) {
			p.nextToken()
			continue
		}
		col := cleanSchemaPrefix(p.curToken.Literal)
		if col != "" {
			cols = append(cols, col)
		}
		p.nextToken()
		if p.curTokenIs(TokenAsc) || p.curTokenIs(TokenDesc) || strings.EqualFold(p.curToken.Literal, "ASC") || strings.EqualFold(p.curToken.Literal, "DESC") {
			p.nextToken()
		}
		if p.curTokenIs(TokenComma) {
			p.nextToken()
		} else {
			break
		}
	}
	if p.curTokenIs(TokenRParen) {
		p.nextToken() // eat ')'
	}
	return cols
}

func (p *Parser) parseReferencesClause(constraintName, childTable string, childCols []string) (*storage.ForeignKeySchema, error) {
	if !strings.EqualFold(p.curToken.Literal, "REFERENCES") {
		return nil, fmt.Errorf("expected REFERENCES in FOREIGN KEY constraint, got %s", p.curToken.Literal)
	}
	p.nextToken() // eat REFERENCES

	if p.curTokenIs(TokenEOF) || p.curTokenIs(TokenComma) || p.curTokenIs(TokenRParen) {
		return nil, fmt.Errorf("expected parent table name after REFERENCES")
	}
	parentTable := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken()

	var parentCols []string
	if p.curTokenIs(TokenLParen) {
		parentCols = p.parseIdentListInParens()
	}

	onDelete := storage.ActionNoAction
	onUpdate := storage.ActionNoAction

	for !p.curTokenIs(TokenComma) && !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
		if p.curTokenIs(TokenOn) || strings.EqualFold(p.curToken.Literal, "ON") {
			p.nextToken() // eat ON
			isDelete := p.curTokenIs(TokenDelete) || strings.EqualFold(p.curToken.Literal, "DELETE")
			isUpdate := p.curTokenIs(TokenUpdate) || strings.EqualFold(p.curToken.Literal, "UPDATE")
			if isDelete || isUpdate {
				p.nextToken() // eat DELETE or UPDATE
				act := strings.ToUpper(p.curToken.Literal)
				p.nextToken()
				if (act == "SET" || act == "NO") && !p.curTokenIs(TokenComma) && !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
					act = act + " " + strings.ToUpper(p.curToken.Literal)
					p.nextToken()
				}
				parsedAct := storage.ParseForeignKeyAction(act)
				if isDelete {
					onDelete = parsedAct
				} else {
					onUpdate = parsedAct
				}
				continue
			}
		}
		if strings.EqualFold(p.curToken.Literal, "NOT") || strings.EqualFold(p.curToken.Literal, "NULL") ||
			strings.EqualFold(p.curToken.Literal, "DEFAULT") || strings.EqualFold(p.curToken.Literal, "PRIMARY") ||
			strings.EqualFold(p.curToken.Literal, "UNIQUE") || strings.EqualFold(p.curToken.Literal, "CHECK") ||
			strings.EqualFold(p.curToken.Literal, "IDENTITY") || strings.EqualFold(p.curToken.Literal, "CONSTRAINT") {
			break
		}
		p.nextToken()
	}

	if constraintName == "" && len(childCols) > 0 {
		constraintName = fmt.Sprintf("fk_%s_%s", parentTable, childCols[0])
	}

	return &storage.ForeignKeySchema{
		Name:          constraintName,
		ChildTable:    childTable,
		ChildColumns:  childCols,
		ParentTable:   parentTable,
		ParentColumns: parentCols,
		OnDelete:      onDelete,
		OnUpdate:      onUpdate,
	}, nil
}

func isIdentCharByte(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '#' || ch == '$' || ch == '@'
}

func isDataTypeKeyword(s string) bool {
	upper := strings.ToUpper(strings.Trim(s, "[]\"'`"))
	switch upper {
	case "INT", "INTEGER", "BIGINT", "SMALLINT", "TINYINT", "INT2", "INT4", "INT8", "BIT", "BOOLEAN", "BOOL",
		"FLOAT", "REAL", "DOUBLE", "FLOAT4", "FLOAT8", "DECIMAL", "NUMERIC", "MONEY", "SMALLMONEY",
		"VARCHAR", "NVARCHAR", "CHAR", "NCHAR", "TEXT", "NTEXT", "BPCHAR", "SYSNAME",
		"DATE", "DATETIME", "DATETIME2", "SMALLDATETIME", "DATETIMEOFFSET", "TIME", "TIMETZ", "TIMESTAMP", "TIMESTAMPTZ",
		"BINARY", "VARBINARY", "BYTEA", "BLOB", "IMAGE", "ROWVERSION", "UNIQUEIDENTIFIER", "UUID", "GUID", "XML", "JSON", "JSONB":
		return true
	default:
		return false
	}
}

func (p *Parser) parseSingleColumnDef(tableName string) (ColumnDef, error) {
	// Collect column name tokens (handles 123a, @, $, etc.)
	var nameParts []string
	for !p.curTokenIs(TokenLParen) && !p.curTokenIs(TokenComma) && !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
		upper := strings.ToUpper(p.curToken.Literal)
		if len(nameParts) > 0 && (upper == "AS" || isDataTypeKeyword(upper)) {
			break
		}
		// If we already have 1 valid identifier, do not consume a second identifier token into the column name
		if len(nameParts) == 1 {
			first := nameParts[0]
			if len(first) > 0 && (isLetter(rune(first[0])) || first[0] == '_' || first[0] == '[' || first[0] == '"' || first[0] == '`') {
				break
			}
		}
		lit := p.curToken.IdentLiteral()
		if lit == "" {
			lit = p.curToken.Literal
		}
		nameParts = append(nameParts, lit)
		p.nextToken()
		if p.curTokenIs(TokenComma) || p.curTokenIs(TokenRParen) {
			break
		}
		if len(nameParts) == 1 && (nameParts[0] == "@" || nameParts[0] == "$") {
			break
		}
	}
	colName := cleanSchemaPrefix(strings.Join(nameParts, ""))
	if colName == "" {
		colName = "col"
	}

	// Case 1: single identifier without type: CREATE TABLE t (a) or CREATE TABLE t (a, b int)
	if p.curTokenIs(TokenComma) || p.curTokenIs(TokenRParen) {
		dt := storage.TypeInt
		if isDataTypeKeyword(colName) {
			dt = parseDataType(colName)
		}
		return ColumnDef{
			Name:     colName,
			Type:     dt,
			Nullable: true,
		}, nil
	}

	// Case 2: Computed column: `col AS expr`
	if strings.EqualFold(p.curToken.Literal, "AS") {
		p.nextToken() // eat AS
		return p.parseComputedColumn(colName, tableName)
	}

	// Case 3: Type before column name: `CREATE TABLE t (int a)`
	if isDataTypeKeyword(colName) && !isDataTypeKeyword(p.curToken.Literal) && !p.peekTokenIs(TokenLParen) && (p.curTokenIs(TokenIdent) || (p.curToken.Literal != "" && isLetter(rune(p.curToken.Literal[0])))) {
		actualName := cleanSchemaPrefix(p.curToken.IdentLiteral())
		actualType := colName
		p.nextToken()
		dt := parseDataType(actualType)
		return ColumnDef{
			Name:     actualName,
			Type:     dt,
			Nullable: true,
		}, nil
	}

	// Case 4: Standard column definition with type and constraints
	return p.parseColumnTypeAndConstraints(colName, tableName)
}

func (p *Parser) parseComputedColumn(colName, tableName string) (ColumnDef, error) {
	depth := 0
	var exprTokens []string
	isNotNull := false
	isUnique := false

	for !p.curTokenIs(TokenEOF) && !p.curTokenIs(TokenSemicolon) {
		if depth == 0 {
			if p.curTokenIs(TokenComma) || p.curTokenIs(TokenRParen) {
				break
			}
			upper := strings.ToUpper(p.curToken.Literal)
			if upper == "PERSISTED" {
				p.nextToken() // eat PERSISTED
				continue
			}
			if upper == "NOT" && strings.EqualFold(p.peekToken.Literal, "NULL") {
				isNotNull = true
				p.nextToken() // eat NOT
				p.nextToken() // eat NULL
				continue
			}
			if upper == "NULL" {
				p.nextToken() // eat NULL
				continue
			}
			if upper == "UNIQUE" {
				isUnique = true
				p.nextToken() // eat UNIQUE
				continue
			}
		}

		if p.curTokenIs(TokenLParen) {
			depth++
			exprTokens = append(exprTokens, "(")
			p.nextToken()
			continue
		}
		if p.curTokenIs(TokenRParen) {
			depth--
			exprTokens = append(exprTokens, ")")
			p.nextToken()
			continue
		}

		lit := p.curToken.Literal
		if p.curTokenIs(TokenString) {
			lit = "'" + strings.ReplaceAll(lit, "'", "''") + "'"
		}
		exprTokens = append(exprTokens, lit)
		p.nextToken()
	}

	computedExpr := strings.Join(exprTokens, " ")

	return ColumnDef{
		Name:         colName,
		Type:         storage.TypeVarChar,
		Nullable:     !isNotNull,
		Unique:       isUnique,
		CheckExpr:    computedExpr,
		ComputedExpr: computedExpr,
		IsComputed:   true,
	}, nil
}

func (p *Parser) parseColumnTypeAndConstraints(colName, tableName string) (ColumnDef, error) {
	rawType := strings.ToUpper(strings.Trim(p.curToken.Literal, "[]\"'`"))
	hasTypeParen := p.peekTokenIs(TokenLParen)
	dt, prec, scale, _ := p.parseTypeSpec()

	isSerial := rawType == "SERIAL" || rawType == "BIGSERIAL" || rawType == "SMALLSERIAL"
	isRowVersion := rawType == "ROWVERSION"
	isFixed := rawType == "CHAR" || rawType == "NCHAR" || rawType == "BPCHAR" || rawType == "BINARY"

	if rawType == "FLOAT" {
		if prec > 0 && prec <= 24 {
			dt = storage.TypeReal
			prec = 24
		} else {
			dt = storage.TypeFloat
			prec = 53
		}
		scale = 0
	} else if dt == storage.TypeReal {
		prec = 24
		scale = 0
	} else if dt == storage.TypeFloat {
		prec = 53
		scale = 0
	} else if dt == storage.TypeDecimal {
		if rawType == "MONEY" {
			prec = 19
			scale = 4
		} else if rawType == "SMALLMONEY" {
			prec = 10
			scale = 4
		} else if !hasTypeParen && prec == 0 {
			prec = 18
			scale = 2
		}
	} else if isRowVersion {
		dt = storage.TypeBinary
		prec = 8
		scale = 0
	} else if dt == storage.TypeVarChar || dt == storage.TypeBinary {
		if prec == 0 && isFixed {
			prec = 1
		}
	} else if dt == storage.TypeInt {
		// keep rawType (TINYINT, SMALLINT, INT, BIGINT)
	} else {
		prec = 0
		scale = 0
	}

	isPK := false
	isIdentity := isSerial || rawType == "IDENTITY"
	isNotNull := isSerial || isRowVersion
	isUnique := false
	isGeneratedIdentity := false
	var defaultExpr string
	var checkExpr string
	var colFK *storage.ForeignKeySchema
	var currentConstraintName string

	for !p.curTokenIs(TokenComma) && !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
		upper := strings.ToUpper(p.curToken.Literal)
		switch upper {
		case "CONSTRAINT":
			p.nextToken()
			currentConstraintName = cleanSchemaPrefix(p.curToken.Literal)
			p.nextToken()

		case "PRIMARY":
			p.nextToken()
			if strings.EqualFold(p.curToken.Literal, "KEY") {
				p.nextToken()
			}
			if strings.EqualFold(p.curToken.Literal, "CLUSTERED") || strings.EqualFold(p.curToken.Literal, "NONCLUSTERED") {
				p.nextToken()
			}
			if p.curTokenIs(TokenAsc) || p.curTokenIs(TokenDesc) {
				p.nextToken()
			}
			isPK = true
			isNotNull = true

		case "NOT":
			p.nextToken()
			if p.curTokenIs(TokenNull) || strings.EqualFold(p.curToken.Literal, "NULL") {
				p.nextToken()
				isNotNull = true
			}

		case "NULL":
			p.nextToken()

		case "UNIQUE":
			p.nextToken()
			if strings.EqualFold(p.curToken.Literal, "CLUSTERED") || strings.EqualFold(p.curToken.Literal, "NONCLUSTERED") {
				p.nextToken()
			}
			isUnique = true

		case "IDENTITY", "AUTO_INCREMENT", "AUTOINCREMENT":
			p.nextToken()
			if p.curTokenIs(TokenLParen) {
				_ = p.parseRawParenContent()
			}
			isIdentity = true

		case "GENERATED":
			p.nextToken()
			for !p.curTokenIs(TokenComma) && !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
				if strings.EqualFold(p.curToken.Literal, "IDENTITY") {
					p.nextToken()
					if p.curTokenIs(TokenLParen) {
						_ = p.parseRawParenContent()
					}
					isGeneratedIdentity = true
					isIdentity = true
					isNotNull = true
					break
				}
				p.nextToken()
			}

		case "DEFAULT":
			p.nextToken() // eat DEFAULT
			val := p.parseDDLDefaultValue()
			if !isGeneratedIdentity {
				defaultExpr = val
			}

		case "CHECK":
			p.nextToken() // eat CHECK
			checkExpr = p.parseRawParenContent()

		case "REFERENCES":
			fk, err := p.parseReferencesClause(currentConstraintName, tableName, []string{colName})
			if err != nil {
				return ColumnDef{}, err
			}
			colFK = fk
			currentConstraintName = ""

		default:
			if p.curTokenIs(TokenLParen) {
				_ = p.parseRawParenContent()
			} else {
				p.nextToken()
			}
		}
	}

	if isGeneratedIdentity {
		defaultExpr = ""
	}

	return ColumnDef{
		Name:         colName,
		Type:         dt,
		RawType:      rawType,
		IsFixed:      isFixed,
		PrimaryKey:   isPK,
		Nullable:     !isNotNull,
		Identity:     isIdentity,
		IsRowVersion: isRowVersion,
		Unique:       isUnique,
		Precision:    prec,
		Scale:        scale,
		Default:      defaultExpr,
		CheckExpr:    checkExpr,
		ForeignKey:   colFK,
	}, nil
}

func (p *Parser) parseDDLDefaultValue() string {
	if p.curTokenIs(TokenEOF) || p.curTokenIs(TokenComma) || p.curTokenIs(TokenRParen) {
		return ""
	}
	if p.curTokenIs(TokenString) {
		lit := "'" + strings.ReplaceAll(p.curToken.Literal, "'", "''") + "'"
		p.nextToken()
		return lit
	}
	if p.curTokenIs(TokenLParen) {
		content := p.parseRawParenContent()
		return CleanDefaultExpr("(" + content + ")")
	}
	if p.curTokenIs(TokenMinus) || p.curTokenIs(TokenPlus) {
		sign := p.curToken.Literal
		p.nextToken()
		val := p.curToken.Literal
		p.nextToken()
		if sign == "-" {
			return "-" + val
		}
		return val
	}
	tokLit := p.curToken.Literal
	upper := strings.ToUpper(tokLit)
	if upper == "NOT" || upper == "NULL" || upper == "PRIMARY" || upper == "UNIQUE" || upper == "CHECK" || upper == "REFERENCES" || upper == "CONSTRAINT" {
		if upper == "NULL" {
			p.nextToken()
		}
		return ""
	}
	p.nextToken()
	if p.curTokenIs(TokenLParen) {
		args := p.parseRawParenContent()
		return tokLit + "(" + args + ")"
	}
	return CleanDefaultExpr(tokLit)
}

func (p *Parser) parseRawParenContent() string {
	if !p.curTokenIs(TokenLParen) {
		return ""
	}
	p.nextToken() // eat outer '('
	depth := 1
	var b strings.Builder
	prevLit := ""
	for depth > 0 && !p.curTokenIs(TokenEOF) {
		if p.curTokenIs(TokenLParen) {
			depth++
			if prevLit != "" && prevLit != "(" && !isFuncLikeIdent(prevLit) {
				b.WriteByte(' ')
			}
			b.WriteByte('(')
			prevLit = "("
			p.nextToken()
			continue
		}
		if p.curTokenIs(TokenRParen) {
			depth--
			if depth == 0 {
				p.nextToken() // eat matching outer ')'
				break
			}
			b.WriteByte(')')
			prevLit = ")"
			p.nextToken()
			continue
		}
		lit := p.curToken.Literal
		if p.curTokenIs(TokenString) {
			lit = "'" + strings.ReplaceAll(lit, "'", "''") + "'"
		}
		if b.Len() > 0 && prevLit != "(" && lit != "," && lit != ")" {
			b.WriteByte(' ')
		}
		b.WriteString(lit)
		prevLit = lit
		p.nextToken()
	}
	return strings.TrimSpace(b.String())
}

func isFuncLikeIdent(s string) bool {
	if s == "" {
		return false
	}
	upper := strings.ToUpper(s)
	switch upper {
	case "AND", "OR", "NOT", "IN", "CHECK", "VALUES", "=", "<>", "!=", "<", ">", "<=", ">=", "+", "-", "*", "/":
		return false
	default:
		return isLetter(rune(s[0]))
	}
}

func (p *Parser) skipUntilCommaOrRParen() {
	depth := 0
	for !p.curTokenIs(TokenEOF) {
		if p.curTokenIs(TokenLParen) {
			depth++
		} else if p.curTokenIs(TokenRParen) {
			if depth == 0 {
				return
			}
			depth--
		} else if p.curTokenIs(TokenComma) && depth == 0 {
			return
		}
		p.nextToken()
	}
}

func (p *Parser) parseColumnDefs() ([]ColumnDef, error) {
	stmt := &CreateTableStmt{}
	if err := p.parseTableBody(stmt); err != nil {
		return nil, err
	}
	return stmt.Columns, nil
}

// ParseColumnDefs parses column definitions inside parenthesis.
func (p *Parser) ParseColumnDefs() ([]ColumnDef, error) {
	return p.parseColumnDefs()
}

// ParseAlterTable parses an ALTER TABLE statement into an AlterTableStmt AST.
func (p *Parser) ParseAlterTable() (*AlterTableStmt, error) {
	if !p.curTokenIs(TokenAlter) {
		return nil, fmt.Errorf("invalid ALTER TABLE syntax: expected ALTER, got %s", p.curToken.Literal)
	}
	p.nextToken() // eat ALTER
	if !p.curTokenIs(TokenTable) {
		return nil, fmt.Errorf("invalid ALTER TABLE syntax: expected TABLE, got %s", p.curToken.Literal)
	}
	p.nextToken() // eat TABLE

	ifExists := false
	if p.curTokenIs(TokenIf) || strings.EqualFold(p.curToken.Literal, "IF") {
		p.nextToken()
		if p.curTokenIs(TokenExists) || strings.EqualFold(p.curToken.Literal, "EXISTS") {
			ifExists = true
			p.nextToken()
		}
	}

	if p.curToken.Literal == "" || p.curTokenIs(TokenEOF) || p.curTokenIs(TokenSemicolon) {
		return nil, fmt.Errorf("invalid ALTER TABLE syntax: missing table name")
	}
	tableName := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken()

	stmt := &AlterTableStmt{
		Table:    tableName,
		IfExists: ifExists,
	}

	for !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
		if p.curTokenIs(TokenComma) {
			p.nextToken()
			continue
		}
		actions, err := p.parseAlterTableAction(tableName)
		if err != nil {
			return nil, err
		}
		stmt.Actions = append(stmt.Actions, actions...)
		if p.curTokenIs(TokenComma) {
			p.nextToken()
		} else {
			break
		}
	}

	if len(stmt.Actions) == 0 {
		return nil, fmt.Errorf("unsupported ALTER TABLE action: empty action")
	}

	return stmt, nil
}

func (p *Parser) parseAlterTableAction(tableName string) ([]AlterTableAction, error) {
	kw := strings.ToUpper(p.curToken.Literal)
	switch kw {
	case "DROP":
		p.nextToken() // eat DROP
		sub := strings.ToUpper(p.curToken.Literal)
		switch sub {
		case "PARTITION":
			p.nextToken() // eat PARTITION
			partName := strings.Trim(strings.TrimSpace(p.curToken.Literal), "';\" ")
			p.nextToken()
			return []AlterTableAction{{
				Type:          AlterDropPartition,
				PartitionName: partName,
			}}, nil

		case "CONSTRAINT", "FOREIGN":
			p.nextToken() // eat CONSTRAINT or FOREIGN
			if sub == "FOREIGN" && strings.EqualFold(p.curToken.Literal, "KEY") {
				p.nextToken() // eat KEY
			}
			dropIfExists := false
			if p.curTokenIs(TokenIf) || strings.EqualFold(p.curToken.Literal, "IF") {
				p.nextToken()
				if p.curTokenIs(TokenExists) || strings.EqualFold(p.curToken.Literal, "EXISTS") {
					dropIfExists = true
					p.nextToken()
				}
			}
			cName := cleanSchemaPrefix(p.curToken.Literal)
			p.nextToken()
			if p.curTokenIs(TokenCascade) || p.curTokenIs(TokenRestrict) || strings.EqualFold(p.curToken.Literal, "CASCADE") || strings.EqualFold(p.curToken.Literal, "RESTRICT") {
				p.nextToken()
			}
			return []AlterTableAction{{
				Type:           AlterDropConstraint,
				IfExists:       dropIfExists,
				ConstraintName: cName,
			}}, nil

		default:
			if sub == "COLUMN" {
				p.nextToken() // eat COLUMN
			}
			dropIfExists := false
			if p.curTokenIs(TokenIf) || strings.EqualFold(p.curToken.Literal, "IF") {
				p.nextToken()
				if p.curTokenIs(TokenExists) || strings.EqualFold(p.curToken.Literal, "EXISTS") {
					dropIfExists = true
					p.nextToken()
				}
			}
			var dropCols []string
			for !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
				if p.curTokenIs(TokenIf) || strings.EqualFold(p.curToken.Literal, "IF") {
					p.nextToken()
					if p.curTokenIs(TokenExists) || strings.EqualFold(p.curToken.Literal, "EXISTS") {
						dropIfExists = true
						p.nextToken()
					}
				}
				colName := cleanSchemaPrefix(p.curToken.Literal)
				if colName != "" {
					dropCols = append(dropCols, colName)
				}
				p.nextToken()
				if p.curTokenIs(TokenComma) {
					nextUpper := strings.ToUpper(p.peekToken.Literal)
					if nextUpper == "ADD" || nextUpper == "DROP" || nextUpper == "ALTER" || nextUpper == "RENAME" || nextUpper == "SET" {
						break
					}
					p.nextToken() // eat ',' and continue reading next dropped column name
				} else {
					break
				}
			}
			return []AlterTableAction{{
				Type:        AlterDropColumn,
				IfExists:    dropIfExists,
				DropColumns: dropCols,
			}}, nil
		}

	case "RENAME":
		p.nextToken() // eat RENAME
		if strings.EqualFold(p.curToken.Literal, "TO") {
			p.nextToken() // eat TO
			newTbl := cleanSchemaPrefix(p.curToken.Literal)
			p.nextToken()
			return []AlterTableAction{{
				Type:    AlterRenameTable,
				NewName: newTbl,
			}}, nil
		}
		if strings.EqualFold(p.curToken.Literal, "COLUMN") {
			p.nextToken() // eat COLUMN
		}
		oldCol := cleanSchemaPrefix(p.curToken.Literal)
		p.nextToken()
		if strings.EqualFold(p.curToken.Literal, "TO") {
			p.nextToken() // eat TO
		}
		newCol := cleanSchemaPrefix(p.curToken.Literal)
		p.nextToken()
		return []AlterTableAction{{
			Type:       AlterRenameColumn,
			ColumnName: oldCol,
			NewName:    newCol,
		}}, nil

	case "SET":
		p.nextToken() // eat SET
		if p.curTokenIs(TokenLParen) {
			_ = p.parseRawParenContent()
		} else {
			p.skipUntilCommaOrRParen()
		}
		return []AlterTableAction{{
			Type: AlterSetStorageParams,
		}}, nil

	case "ALTER":
		p.nextToken() // eat ALTER
		if strings.EqualFold(p.curToken.Literal, "COLUMN") {
			p.nextToken() // eat COLUMN
		}
		colName := cleanSchemaPrefix(p.curToken.Literal)
		p.nextToken()

		op := strings.ToUpper(p.curToken.Literal)
		switch op {
		case "DROP":
			p.nextToken() // eat DROP
			target := strings.ToUpper(p.curToken.Literal)
			if target == "DEFAULT" {
				p.nextToken()
				return []AlterTableAction{{
					Type:       AlterColumnDropDefault,
					ColumnName: colName,
				}}, nil
			}
			if target == "NOT" {
				p.nextToken() // eat NOT
				if p.curTokenIs(TokenNull) || strings.EqualFold(p.curToken.Literal, "NULL") {
					p.nextToken()
				}
				return []AlterTableAction{{
					Type:       AlterColumnDropNotNull,
					ColumnName: colName,
				}}, nil
			}
			return nil, fmt.Errorf("unsupported ALTER COLUMN DROP target: %s", target)

		case "SET":
			p.nextToken() // eat SET
			target := strings.ToUpper(p.curToken.Literal)
			if target == "DEFAULT" {
				p.nextToken() // eat DEFAULT
				defExpr := p.parseDDLDefaultValue()
				return []AlterTableAction{{
					Type:        AlterColumnSetDefault,
					ColumnName:  colName,
					DefaultExpr: defExpr,
				}}, nil
			}
			if target == "NOT" {
				p.nextToken() // eat NOT
				if p.curTokenIs(TokenNull) || strings.EqualFold(p.curToken.Literal, "NULL") {
					p.nextToken()
				}
				return []AlterTableAction{{
					Type:       AlterColumnSetNotNull,
					ColumnName: colName,
				}}, nil
			}
			if target == "DATA" {
				p.nextToken() // eat DATA
				if strings.EqualFold(p.curToken.Literal, "TYPE") {
					p.nextToken() // eat TYPE
				}
				return p.parseAlterColumnTypeAction(colName)
			}
			return nil, fmt.Errorf("unsupported ALTER COLUMN SET target: %s", target)

		case "TYPE":
			p.nextToken() // eat TYPE
			return p.parseAlterColumnTypeAction(colName)

		default:
			// T-SQL style: ALTER TABLE t ALTER COLUMN c <new_type> [NOT NULL | NULL]
			return p.parseAlterColumnTypeAction(colName)
		}

	case "ADD":
		p.nextToken() // eat ADD
		var actions []AlterTableAction
		for !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
			if strings.EqualFold(p.curToken.Literal, "ADD") {
				p.nextToken()
			}
			curUpper := strings.ToUpper(p.curToken.Literal)
			if curUpper == "DEFAULT" {
				p.nextToken() // eat DEFAULT
				act, err := p.parseAlterTableDefaultExprAndFor(tableName, "")
				if err != nil {
					return nil, err
				}
				actions = append(actions, act)
			} else if curUpper == "CONSTRAINT" {
				p.nextToken() // eat CONSTRAINT
				cName := cleanSchemaPrefix(p.curToken.Literal)
				p.nextToken() // eat constraint name
				if strings.EqualFold(p.curToken.Literal, "DEFAULT") {
					p.nextToken() // eat DEFAULT
					act, err := p.parseAlterTableDefaultExprAndFor(tableName, cName)
					if err != nil {
						return nil, err
					}
					actions = append(actions, act)
				} else {
					tmpStmt := &CreateTableStmt{Name: tableName}
					if err := p.parseTableLevelConstraintAfterName(tmpStmt, cName); err != nil {
						return nil, err
					}
					if len(tmpStmt.PrimaryKeyCols) > 0 {
						actions = append(actions, AlterTableAction{
							Type:           AlterAddPrimaryKey,
							ConstraintCols: tmpStmt.PrimaryKeyCols,
						})
					}
					for _, uq := range tmpStmt.UniqueIndexes {
						actions = append(actions, AlterTableAction{
							Type:           AlterAddUnique,
							ConstraintName: uq.Name,
							ConstraintCols: uq.Columns,
						})
					}
					for i := range tmpStmt.ForeignKeys {
						fk := tmpStmt.ForeignKeys[i]
						actions = append(actions, AlterTableAction{
							Type:           AlterAddForeignKey,
							ConstraintName: fk.Name,
							ForeignKey:     &fk,
						})
					}
					for _, chk := range tmpStmt.CheckConstraints {
						actions = append(actions, AlterTableAction{
							Type:      AlterAddCheck,
							CheckExpr: chk,
						})
					}
				}
			} else if p.isTableConstraintStart() {
				tmpStmt := &CreateTableStmt{Name: tableName}
				if err := p.parseTableLevelConstraint(tmpStmt); err != nil {
					return nil, err
				}
				if len(tmpStmt.PrimaryKeyCols) > 0 {
					actions = append(actions, AlterTableAction{
						Type:           AlterAddPrimaryKey,
						ConstraintCols: tmpStmt.PrimaryKeyCols,
					})
				}
				for _, uq := range tmpStmt.UniqueIndexes {
					actions = append(actions, AlterTableAction{
						Type:           AlterAddUnique,
						ConstraintName: uq.Name,
						ConstraintCols: uq.Columns,
					})
				}
				for i := range tmpStmt.ForeignKeys {
					fk := tmpStmt.ForeignKeys[i]
					actions = append(actions, AlterTableAction{
						Type:           AlterAddForeignKey,
						ConstraintName: fk.Name,
						ForeignKey:     &fk,
					})
				}
				for _, chk := range tmpStmt.CheckConstraints {
					actions = append(actions, AlterTableAction{
						Type:      AlterAddCheck,
						CheckExpr: chk,
					})
				}
			} else {
				if strings.EqualFold(p.curToken.Literal, "COLUMN") {
					p.nextToken() // eat COLUMN
				}
				ifNotExists := false
				if p.curTokenIs(TokenIf) || strings.EqualFold(p.curToken.Literal, "IF") {
					p.nextToken()
					if p.curTokenIs(TokenNot) || strings.EqualFold(p.curToken.Literal, "NOT") {
						p.nextToken()
					}
					if p.curTokenIs(TokenExists) || strings.EqualFold(p.curToken.Literal, "EXISTS") {
						ifNotExists = true
						p.nextToken()
					}
				}
				colDef, err := p.parseSingleColumnDef(tableName)
				if err != nil {
					return nil, err
				}
				actions = append(actions, AlterTableAction{
					Type:        AlterAddColumn,
					IfNotExists: ifNotExists,
					Column:      colDef,
				})
			}

			if p.curTokenIs(TokenComma) {
				nextUpper := strings.ToUpper(p.peekToken.Literal)
				if nextUpper == "DROP" || nextUpper == "ALTER" || nextUpper == "RENAME" || nextUpper == "SET" {
					break
				}
				p.nextToken() // eat ',' and continue ADD list
			} else {
				break
			}
		}
		return actions, nil

	default:
		return nil, fmt.Errorf("unsupported ALTER TABLE action: %s", p.curToken.Literal)
	}
}

func (p *Parser) parseAlterColumnTypeAction(colName string) ([]AlterTableAction, error) {
	colDef, err := p.parseColumnTypeAndConstraints(colName, "")
	if err != nil {
		return nil, err
	}
	return []AlterTableAction{{
		Type:       AlterColumnType,
		ColumnName: colName,
		Column:     colDef,
	}}, nil
}

func (p *Parser) parseAlterTableDefaultExprAndFor(tableName, cName string) (AlterTableAction, error) {
	depth := 0
	var b strings.Builder
	prevLit := ""
	for !p.curTokenIs(TokenEOF) && !p.curTokenIs(TokenSemicolon) {
		if depth == 0 && strings.EqualFold(p.curToken.Literal, "FOR") {
			break
		}
		if p.curTokenIs(TokenLParen) {
			depth++
			if prevLit != "" && prevLit != "(" && !isFuncLikeIdent(prevLit) {
				b.WriteByte(' ')
			}
			b.WriteByte('(')
			prevLit = "("
			p.nextToken()
			continue
		}
		if p.curTokenIs(TokenRParen) {
			depth--
			b.WriteByte(')')
			prevLit = ")"
			p.nextToken()
			continue
		}
		lit := p.curToken.Literal
		if p.curTokenIs(TokenString) {
			lit = "'" + strings.ReplaceAll(lit, "'", "''") + "'"
		}
		if b.Len() > 0 && prevLit != "(" && lit != "," && lit != ")" {
			b.WriteByte(' ')
		}
		b.WriteString(lit)
		prevLit = lit
		p.nextToken()
	}

	defExpr := CleanDefaultExpr(strings.TrimSpace(b.String()))
	if !strings.EqualFold(p.curToken.Literal, "FOR") {
		return AlterTableAction{}, fmt.Errorf("expected FOR in ALTER TABLE ADD DEFAULT, got %s", p.curToken.Literal)
	}
	p.nextToken() // eat FOR

	colName := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken() // eat colName

	// Optional WITH VALUES
	if strings.EqualFold(p.curToken.Literal, "WITH") {
		p.nextToken()
		if strings.EqualFold(p.curToken.Literal, "VALUES") {
			p.nextToken()
		}
	}

	return AlterTableAction{
		Type:           AlterColumnSetDefault,
		ColumnName:     colName,
		DefaultExpr:    defExpr,
		ConstraintName: cName,
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

	ifNotExists := false
	if p.curTokenIs(TokenIf) || strings.EqualFold(p.curToken.Literal, "IF") {
		p.nextToken()
		if strings.EqualFold(p.curToken.Literal, "NOT") {
			p.nextToken()
		}
		if p.curTokenIs(TokenExists) || strings.EqualFold(p.curToken.Literal, "EXISTS") {
			p.nextToken()
			ifNotExists = true
		}
	}

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

	// Optional USING <method> (e.g. USING GIN, USING BTREE, USING HASH, USING BRIN)
	if strings.EqualFold(p.curToken.Literal, "USING") {
		p.nextToken() // eat USING
		if p.curTokenIs(TokenIdent) {
			p.nextToken() // eat method
		}
	}

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
		(p.curTokenIs(TokenIdent) && (p.peekTokenIs(TokenLParen) || p.peekTokenIs(TokenJSONExtract) || p.peekTokenIs(TokenJSONExtractText)))

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
			if p.curTokenIs(TokenIdent) || p.curToken.Literal != "" {
				cols = append(cols, cleanSchemaPrefix(p.curToken.Literal))
				p.nextToken()
				if p.curTokenIs(TokenAsc) || p.curTokenIs(TokenDesc) || strings.EqualFold(p.curToken.Literal, "ASC") || strings.EqualFold(p.curToken.Literal, "DESC") {
					p.nextToken()
				}
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
			includeCols = p.parseIdentListInParens()
		}
	}

	whereClause := ""
	if p.curTokenIs(TokenWhere) || strings.EqualFold(p.curToken.Literal, "WHERE") {
		p.nextToken()
		var whereTokens []string
		for !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
			whereTokens = append(whereTokens, p.curToken.Literal)
			p.nextToken()
		}
		whereClause = strings.Join(whereTokens, " ")
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
		IfNotExists: ifNotExists,
		WhereClause: whereClause,
		Expression:  exprStr,
		ExprColumn:  exprCol,
		ExprPath:    exprPath,
	}, nil
}

// ParseDropTable parses DROP TABLE [IF EXISTS] t1 [, t2 ...] [CASCADE | RESTRICT].
func (p *Parser) ParseDropTable() (*DropTableStmt, error) {
	if p.curTokenIs(TokenDrop) {
		p.nextToken() // eat DROP
	}
	return p.parseDropTable()
}

func (p *Parser) parseDropTable() (*DropTableStmt, error) {
	if !p.curTokenIs(TokenTable) {
		return nil, fmt.Errorf("invalid DROP TABLE syntax: expected TABLE, got %s", p.curToken.Literal)
	}
	p.nextToken() // eat TABLE

	ifExists := false
	if p.curTokenIs(TokenIf) || strings.EqualFold(p.curToken.Literal, "IF") {
		p.nextToken()
		if p.curTokenIs(TokenExists) || strings.EqualFold(p.curToken.Literal, "EXISTS") {
			ifExists = true
			p.nextToken()
		}
	}
	if p.curToken.Literal == "" || p.curTokenIs(TokenEOF) || p.curTokenIs(TokenSemicolon) {
		return nil, fmt.Errorf("invalid DROP TABLE syntax: missing table name")
	}

	var names []string
	for !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
		if p.curTokenIs(TokenCascade) || strings.EqualFold(p.curToken.Literal, "CASCADE") ||
			p.curTokenIs(TokenRestrict) || strings.EqualFold(p.curToken.Literal, "RESTRICT") {
			break
		}
		name := cleanSchemaPrefix(p.curToken.Literal)
		if name != "" {
			names = append(names, name)
		}
		p.nextToken()
		if p.curTokenIs(TokenComma) {
			p.nextToken()
		} else {
			break
		}
	}

	if len(names) == 0 {
		return nil, fmt.Errorf("invalid DROP TABLE syntax: missing table name")
	}

	cascade := false
	for !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
		if p.curTokenIs(TokenCascade) || strings.EqualFold(p.curToken.Literal, "CASCADE") {
			cascade = true
		}
		p.nextToken()
	}
	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &DropTableStmt{
		Name:     names[0],
		Names:    names,
		IfExists: ifExists,
		Cascade:  cascade,
	}, nil
}

// ParseDropIndex parses DROP INDEX [IF EXISTS] <name> [ON <table>] [CASCADE | RESTRICT].
func (p *Parser) ParseDropIndex() (*DropIndexStmt, error) {
	if p.curTokenIs(TokenDrop) {
		p.nextToken() // eat DROP
	}
	return p.parseDropIndex()
}

func (p *Parser) parseDropIndex() (*DropIndexStmt, error) {
	if !p.curTokenIs(TokenIndex) {
		return nil, fmt.Errorf("invalid DROP INDEX syntax: expected INDEX, got %s", p.curToken.Literal)
	}
	p.nextToken() // eat INDEX

	if strings.EqualFold(p.curToken.Literal, "CONCURRENTLY") {
		p.nextToken()
	}

	ifExists := false
	if p.curTokenIs(TokenIf) || strings.EqualFold(p.curToken.Literal, "IF") {
		p.nextToken()
		if p.curTokenIs(TokenExists) || strings.EqualFold(p.curToken.Literal, "EXISTS") {
			ifExists = true
			p.nextToken()
		}
	}
	if p.curToken.Literal == "" || p.curTokenIs(TokenEOF) || p.curTokenIs(TokenSemicolon) {
		return nil, fmt.Errorf("invalid DROP INDEX syntax: expected DROP INDEX [IF EXISTS] <name> [ON <table>] [CASCADE]")
	}
	idxName := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken()

	tblName := ""
	cascade := false
	for !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
		if p.curTokenIs(TokenOn) || strings.EqualFold(p.curToken.Literal, "ON") {
			p.nextToken()
			if p.curToken.Literal != "" && !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
				tblName = cleanSchemaPrefix(p.curToken.Literal)
				p.nextToken()
			}
			continue
		}
		if p.curTokenIs(TokenCascade) || strings.EqualFold(p.curToken.Literal, "CASCADE") {
			cascade = true
		}
		p.nextToken()
	}
	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	return &DropIndexStmt{
		Name:     idxName,
		Table:    tblName,
		IfExists: ifExists,
		Cascade:  cascade,
	}, nil
}

// ParseTruncate parses TRUNCATE [TABLE] [ONLY] t1 [*] [, t2 ...] [RESTART IDENTITY | CONTINUE IDENTITY] [CASCADE | RESTRICT].
func (p *Parser) ParseTruncate() (*TruncateStmt, error) {
	if !p.curTokenIs(TokenTruncate) && !strings.EqualFold(p.curToken.Literal, "TRUNCATE") {
		return nil, fmt.Errorf("invalid TRUNCATE TABLE syntax")
	}
	p.nextToken() // eat TRUNCATE
	if p.curTokenIs(TokenTable) || strings.EqualFold(p.curToken.Literal, "TABLE") {
		p.nextToken() // eat TABLE
	}

	var tables []string
	cascade := false
	restartIdentity := true

	for !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
		upper := strings.ToUpper(p.curToken.Literal)
		if upper == "ONLY" {
			p.nextToken()
			continue
		}
		if upper == "RESTART" {
			p.nextToken()
			if strings.EqualFold(p.curToken.Literal, "IDENTITY") {
				p.nextToken()
			}
			restartIdentity = true
			continue
		}
		if upper == "CONTINUE" {
			p.nextToken()
			if strings.EqualFold(p.curToken.Literal, "IDENTITY") {
				p.nextToken()
			}
			restartIdentity = false
			continue
		}
		if p.curTokenIs(TokenCascade) || upper == "CASCADE" {
			cascade = true
			p.nextToken()
			continue
		}
		if p.curTokenIs(TokenRestrict) || upper == "RESTRICT" {
			cascade = false
			p.nextToken()
			continue
		}
		if p.curTokenIs(TokenLParen) {
			p.nextToken()
			continue
		}
		if p.curTokenIs(TokenRParen) || p.curTokenIs(TokenAsterisk) || p.curTokenIs(TokenComma) {
			p.nextToken()
			continue
		}

		tbl := cleanSchemaPrefix(strings.TrimSuffix(p.curToken.Literal, "*"))
		if tbl != "" {
			tables = append(tables, tbl)
		}
		p.nextToken()
	}

	if p.curTokenIs(TokenSemicolon) {
		p.nextToken()
	}

	if len(tables) == 0 {
		return nil, fmt.Errorf("invalid TRUNCATE TABLE syntax: missing table name")
	}

	return &TruncateStmt{
		Tables:          tables,
		Cascade:         cascade,
		RestartIdentity: restartIdentity,
	}, nil
}

// ParseDropView parses DROP VIEW [IF EXISTS] <name>.
func (p *Parser) ParseDropView() (*DropViewStmt, error) {
	if !p.curTokenIs(TokenDrop) {
		return nil, fmt.Errorf("invalid DROP VIEW syntax")
	}
	p.nextToken() // eat DROP
	if !strings.EqualFold(p.curToken.Literal, "VIEW") {
		return nil, fmt.Errorf("invalid DROP VIEW syntax")
	}
	p.nextToken() // eat VIEW

	ifExists := false
	if p.curTokenIs(TokenIf) || strings.EqualFold(p.curToken.Literal, "IF") {
		p.nextToken()
		if p.curTokenIs(TokenExists) || strings.EqualFold(p.curToken.Literal, "EXISTS") {
			ifExists = true
			p.nextToken()
		}
	}

	if p.curToken.Literal == "" || p.curTokenIs(TokenEOF) || p.curTokenIs(TokenSemicolon) {
		return nil, fmt.Errorf("invalid DROP VIEW syntax: missing view name")
	}
	viewName := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken()

	return &DropViewStmt{
		Name:     viewName,
		IfExists: ifExists,
	}, nil
}

// ParseCreateStatistics parses CREATE STATISTICS <name> ON <table> (<cols...>).
func (p *Parser) ParseCreateStatistics() (*CreateStatisticsStmt, error) {
	if !p.curTokenIs(TokenCreate) {
		return nil, fmt.Errorf("invalid CREATE STATISTICS syntax: expected CREATE STATISTICS <name> ON <table> (<cols>)")
	}
	p.nextToken() // eat CREATE
	if !strings.EqualFold(p.curToken.Literal, "STATISTICS") {
		return nil, fmt.Errorf("invalid CREATE STATISTICS syntax: expected CREATE STATISTICS <name> ON <table> (<cols>)")
	}
	p.nextToken() // eat STATISTICS

	if p.curToken.Literal == "" || p.curTokenIs(TokenOn) || p.curTokenIs(TokenEOF) {
		return nil, fmt.Errorf("invalid CREATE STATISTICS syntax: expected CREATE STATISTICS <name> ON <table> (<cols>)")
	}
	statName := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken()

	if !p.curTokenIs(TokenOn) && !strings.EqualFold(p.curToken.Literal, "ON") {
		return nil, fmt.Errorf("invalid CREATE STATISTICS syntax: expected CREATE STATISTICS <name> ON <table> (<cols>)")
	}
	p.nextToken() // eat ON

	if p.curToken.Literal == "" || p.curTokenIs(TokenLParen) || p.curTokenIs(TokenEOF) {
		return nil, fmt.Errorf("invalid CREATE STATISTICS syntax: expected CREATE STATISTICS <name> ON <table> (<cols>)")
	}
	tableName := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken()

	if !p.curTokenIs(TokenLParen) {
		return nil, fmt.Errorf("invalid CREATE STATISTICS syntax: expected CREATE STATISTICS <name> ON <table> (<cols>)")
	}
	cols := p.parseIdentListInParens()

	return &CreateStatisticsStmt{
		Name:    statName,
		Table:   tableName,
		Columns: cols,
	}, nil
}

// ParseDropStatistics parses DROP STATISTICS <name> ON <table> or DROP STATISTICS <table>.<name>.
func (p *Parser) ParseDropStatistics() (*DropStatisticsStmt, error) {
	if !p.curTokenIs(TokenDrop) {
		return nil, fmt.Errorf("invalid DROP STATISTICS syntax: expected DROP STATISTICS <name> ON <table> or DROP STATISTICS <table>.<name>")
	}
	p.nextToken() // eat DROP
	if !strings.EqualFold(p.curToken.Literal, "STATISTICS") {
		return nil, fmt.Errorf("invalid DROP STATISTICS syntax: expected DROP STATISTICS <name> ON <table> or DROP STATISTICS <table>.<name>")
	}
	p.nextToken() // eat STATISTICS

	if p.curToken.Literal == "" || p.curTokenIs(TokenEOF) || p.curTokenIs(TokenSemicolon) {
		return nil, fmt.Errorf("invalid DROP STATISTICS syntax: expected DROP STATISTICS <name> ON <table> or DROP STATISTICS <table>.<name>")
	}

	rawFirst := p.curToken.Literal
	p.nextToken()

	if p.curTokenIs(TokenOn) || strings.EqualFold(p.curToken.Literal, "ON") {
		p.nextToken() // eat ON
		tableName := cleanSchemaPrefix(p.curToken.Literal)
		p.nextToken()
		return &DropStatisticsStmt{
			Name:  cleanSchemaPrefix(rawFirst),
			Table: tableName,
		}, nil
	}

	cleaned := strings.Trim(strings.TrimSpace(rawFirst), "[]\"'`")
	if idx := strings.LastIndex(cleaned, "."); idx != -1 {
		tbl := cleanSchemaPrefix(cleaned[:idx])
		stat := cleanSchemaPrefix(cleaned[idx+1:])
		return &DropStatisticsStmt{
			Name:  stat,
			Table: tbl,
		}, nil
	}

	return &DropStatisticsStmt{
		Name: cleanSchemaPrefix(rawFirst),
	}, nil
}

// ParseReindex parses REINDEX [TABLE | INDEX] <target>.
func (p *Parser) ParseReindex() (*ReindexStmt, error) {
	if !strings.EqualFold(p.curToken.Literal, "REINDEX") {
		return nil, fmt.Errorf("invalid REINDEX syntax")
	}
	p.nextToken() // eat REINDEX
	if p.curTokenIs(TokenTable) || p.curTokenIs(TokenIndex) ||
		strings.EqualFold(p.curToken.Literal, "TABLE") || strings.EqualFold(p.curToken.Literal, "INDEX") {
		p.nextToken()
	}
	if p.curToken.Literal == "" || p.curTokenIs(TokenEOF) || p.curTokenIs(TokenSemicolon) {
		return nil, fmt.Errorf("invalid REINDEX syntax: missing target name")
	}
	target := cleanSchemaPrefix(p.curToken.Literal)
	p.nextToken()
	return &ReindexStmt{Target: target}, nil
}

// ParseServerCursorDeclare parses DECLARE <name> [BINARY] [INSENSITIVE] [NO SCROLL|SCROLL] CURSOR [WITH|WITHOUT HOLD] FOR <query>.
func ParseServerCursorDeclare(query string) (*ServerCursorDeclareStmt, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(query), ";")
	if !sqlutil.HasPrefixKeyword(trimmed, "DECLARE") {
		return nil, fmt.Errorf("invalid DECLARE CURSOR syntax")
	}
	rest := strings.TrimSpace(trimmed[len("DECLARE"):])

	forIdx := sqlutil.FindTopLevelKeyword(rest, "FOR")
	if forIdx == -1 {
		return nil, fmt.Errorf("syntax error: DECLARE CURSOR requires 'FOR <query>'")
	}

	header := strings.TrimSpace(rest[:forIdx])
	cursorQuery := strings.TrimSpace(rest[forIdx+len("FOR"):])
	if cursorQuery == "" {
		return nil, fmt.Errorf("syntax error: empty query in DECLARE CURSOR")
	}

	p := NewParser(NewLexer(header))
	if p.curTokenIs(TokenEOF) || p.curToken.Literal == "" {
		return nil, fmt.Errorf("syntax error: cursor name required")
	}
	cursorName := cleanSchemaPrefix(p.curToken.Literal)
	if cursorName == "" {
		return nil, fmt.Errorf("syntax error: invalid cursor name")
	}
	p.nextToken()

	stmt := &ServerCursorDeclareStmt{
		Name:  cursorName,
		Query: cursorQuery,
	}

	var prevUpper string
	for !p.curTokenIs(TokenEOF) {
		upper := strings.ToUpper(p.curToken.Literal)
		switch upper {
		case "BINARY":
			stmt.Binary = true
		case "SCROLL":
			if prevUpper != "NO" {
				stmt.Scroll = true
			} else {
				stmt.Scroll = false
			}
		case "HOLD":
			if prevUpper == "WITH" {
				stmt.WithHold = true
			}
		}
		prevUpper = upper
		p.nextToken()
	}

	return stmt, nil
}

// ParseServerCursorFetchOrMove parses FETCH or MOVE [direction] [count] [FROM|IN] <cursor_name>.
func ParseServerCursorFetchOrMove(query string) (*ServerCursorFetchStmt, error) {
	p := NewParser(NewLexer(query))
	if p.curTokenIs(TokenFetch) || strings.EqualFold(p.curToken.Literal, "FETCH") || strings.EqualFold(p.curToken.Literal, "MOVE") {
		p.nextToken()
	}

	var tokens []string
	for !p.curTokenIs(TokenSemicolon) && !p.curTokenIs(TokenEOF) {
		if p.curTokenIs(TokenMinus) && p.peekTokenIs(TokenInt) {
			p.nextToken()
			tokens = append(tokens, "-"+p.curToken.Literal)
			p.nextToken()
			continue
		}
		tokens = append(tokens, p.curToken.Literal)
		p.nextToken()
	}

	if len(tokens) == 0 {
		return nil, fmt.Errorf("syntax error: cursor name required")
	}

	cursorName := cleanSchemaPrefix(tokens[len(tokens)-1])
	tokens = tokens[:len(tokens)-1]

	if len(tokens) > 0 {
		lastTok := strings.ToUpper(tokens[len(tokens)-1])
		if lastTok == "FROM" || lastTok == "IN" {
			tokens = tokens[:len(tokens)-1]
		}
	}

	stmt := &ServerCursorFetchStmt{
		Direction:  "FORWARD",
		Count:      1,
		CursorName: cursorName,
	}

	if len(tokens) == 0 {
		return stmt, nil
	}

	upper0 := strings.ToUpper(tokens[0])
	switch len(tokens) {
	case 1:
		switch upper0 {
		case "NEXT", "PRIOR", "FIRST", "LAST", "FORWARD", "BACKWARD":
			stmt.Direction = upper0
			return stmt, nil
		case "ALL":
			stmt.Direction = "FORWARD"
			stmt.Count = 0
			stmt.IsAll = true
			return stmt, nil
		default:
			n, err := strconv.Atoi(tokens[0])
			if err != nil {
				return nil, fmt.Errorf("syntax error: unexpected token %q", tokens[0])
			}
			if n >= 0 {
				stmt.Direction = "FORWARD"
				stmt.Count = n
			} else {
				stmt.Direction = "BACKWARD"
				stmt.Count = -n
			}
			return stmt, nil
		}

	case 2:
		upper1 := strings.ToUpper(tokens[1])
		switch upper0 {
		case "FORWARD", "BACKWARD":
			stmt.Direction = upper0
			if upper1 == "ALL" {
				stmt.Count = 0
				stmt.IsAll = true
				return stmt, nil
			}
			n, err := strconv.Atoi(tokens[1])
			if err != nil {
				return nil, fmt.Errorf("syntax error in %s count: %w", upper0, err)
			}
			stmt.Count = n
			return stmt, nil

		case "ABSOLUTE", "RELATIVE":
			stmt.Direction = upper0
			n, err := strconv.Atoi(tokens[1])
			if err != nil {
				return nil, fmt.Errorf("syntax error in %s count: %w", upper0, err)
			}
			stmt.Count = n
			return stmt, nil

		default:
			return nil, fmt.Errorf("syntax error: unrecognized cursor direction %q", tokens[0])
		}

	default:
		return nil, fmt.Errorf("syntax error: too many arguments for cursor operation")
	}
}

// ParseServerCursorOpenOrClose parses OPEN <name> or CLOSE <name|ALL>.
func ParseServerCursorOpenOrClose(query string, expectedVerb string) (string, error) {
	p := NewParser(NewLexer(query))
	if !strings.EqualFold(p.curToken.Literal, expectedVerb) {
		return "", fmt.Errorf("syntax error: expected %s", expectedVerb)
	}
	p.nextToken()
	if p.curTokenIs(TokenSemicolon) || p.curTokenIs(TokenEOF) || p.curToken.Literal == "" {
		return "", fmt.Errorf("syntax error: cursor name required for %s", expectedVerb)
	}
	return cleanSchemaPrefix(p.curToken.Literal), nil
}



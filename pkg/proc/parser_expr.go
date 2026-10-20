package proc

import (
	"fmt"
	"strconv"
	"strings"

	"lionsql/pkg/storage"
)

// ParseExpr parses an expression starting at lowest precedence.
func (p *Parser) ParseExpr() Expr {
	return p.parseExpression(precLowest)
}

// ParseExpression parses an expression starting from the specified precedence level.
func (p *Parser) ParseExpression(precedence int) Expr {
	return p.parseExpression(precedence)
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

		case TokenDoubleColon:
			p.nextToken() // eat ::
			if !p.curTokenIs(TokenIdent) {
				return nil
			}
			dt, prec, scale := p.parseTypeSpec()
			leftExp = &CastExpr{
				Expr:       leftExp,
				TargetType: dt,
				Precision:  prec,
				Scale:      scale,
				Try:        false,
			}

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
	case TokenCast:
		return p.parseCast(false)
	case TokenTryCast:
		return p.parseCast(true)
	case TokenConvert:
		return p.parseConvert(false)
	case TokenTryConvert:
		return p.parseConvert(true)
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
		upperIdent := strings.ToUpper(ident)
		if upperIdent == "CURRENT_TIMESTAMP" || upperIdent == "CURRENT_DATE" || upperIdent == "CURRENT_TIME" || upperIdent == "GETDATE" {
			return &FunctionCallExpr{Name: upperIdent}
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

func (p *Parser) parseTypeSpec() (storage.DataType, int, int) {
	dt := parseDataType(p.curToken.Literal)
	p.nextToken()
	prec := 0
	scale := 0
	if p.curTokenIs(TokenLParen) {
		p.nextToken()
		if p.curTokenIs(TokenInt) {
			prec, _ = strconv.Atoi(p.curToken.Literal)
			p.nextToken()
		}
		if p.curTokenIs(TokenComma) {
			p.nextToken()
			if p.curTokenIs(TokenInt) {
				scale, _ = strconv.Atoi(p.curToken.Literal)
				p.nextToken()
			}
		}
		if p.curTokenIs(TokenRParen) {
			p.nextToken()
		}
	}
	return dt, prec, scale
}

func (p *Parser) parseCast(try bool) Expr {
	p.nextToken() // eat CAST or TRY_CAST
	if !p.curTokenIs(TokenLParen) {
		return nil
	}
	p.nextToken() // eat (
	expr := p.parseExpression(precLowest)
	if expr == nil {
		return nil
	}
	if !p.curTokenIs(TokenAs) {
		return nil
	}
	p.nextToken() // eat AS
	if !p.curTokenIs(TokenIdent) {
		return nil
	}
	dt, prec, scale := p.parseTypeSpec()
	if !p.curTokenIs(TokenRParen) {
		return nil
	}
	p.nextToken() // eat )
	return &CastExpr{
		Expr:       expr,
		TargetType: dt,
		Precision:  prec,
		Scale:      scale,
		Try:        try,
	}
}

func (p *Parser) parseConvert(try bool) Expr {
	p.nextToken() // eat CONVERT or TRY_CONVERT
	if !p.curTokenIs(TokenLParen) {
		return nil
	}
	p.nextToken() // eat (
	if !p.curTokenIs(TokenIdent) {
		return nil
	}
	dt, prec, scale := p.parseTypeSpec()
	if !p.curTokenIs(TokenComma) {
		return nil
	}
	p.nextToken() // eat ,
	expr := p.parseExpression(precLowest)
	if expr == nil {
		return nil
	}
	style := 0
	if p.curTokenIs(TokenComma) {
		p.nextToken() // eat ,
		if p.curTokenIs(TokenInt) {
			style, _ = strconv.Atoi(p.curToken.Literal)
			p.nextToken()
		} else {
			styleExpr := p.parseExpression(precLowest)
			if lit, ok := styleExpr.(*LiteralExpr); ok && lit.Val.Type == storage.TypeInt {
				style = int(lit.Val.IVal)
			}
		}
	}
	if !p.curTokenIs(TokenRParen) {
		return nil
	}
	p.nextToken() // eat )
	return &CastExpr{
		Expr:       expr,
		TargetType: dt,
		Precision:  prec,
		Scale:      scale,
		Style:      style,
		Try:        try,
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


// ExtractJSONExprInfo inspects an expression to determine if it represents a JSON extraction
// expression such as:
//   - col->>'key'
//   - col->'a'->>'b'
//   - col->'tags'->>0
//   - JSON_VALUE(col, '$.path')
// It returns the column name, normalized JSON path (starting with $), and true if recognized.
func ExtractJSONExprInfo(expr Expr) (colName, jsonPath string, ok bool) {
	if expr == nil {
		return "", "", false
	}
	switch e := expr.(type) {
	case *BinaryExpr:
		if e.Op != TokenJSONExtract && e.Op != TokenJSONExtractText {
			return "", "", false
		}
		var segments []string
		curr := e
		for {
			var seg string
			switch r := curr.Right.(type) {
			case *LiteralExpr:
				if r.Val.Type == storage.TypeVarChar {
					seg = "." + r.Val.SVal
				} else if r.Val.Type == storage.TypeInt {
					seg = fmt.Sprintf("[%d]", r.Val.IVal)
				} else {
					seg = "." + r.Val.String()
				}
			case *ColumnRefExpr:
				seg = "." + cleanSchemaPrefix(r.Name)
			default:
				return "", "", false
			}
			segments = append([]string{seg}, segments...)

			if leftBin, isBin := curr.Left.(*BinaryExpr); isBin && (leftBin.Op == TokenJSONExtract || leftBin.Op == TokenJSONExtractText) {
				curr = leftBin
			} else if leftCol, isCol := curr.Left.(*ColumnRefExpr); isCol {
				colName = cleanSchemaPrefix(leftCol.Name)
				break
			} else {
				return "", "", false
			}
		}

		var pathBuilder strings.Builder
		pathBuilder.WriteString("$")
		for _, seg := range segments {
			pathBuilder.WriteString(seg)
		}
		return colName, pathBuilder.String(), true

	case *FunctionCallExpr:
		if strings.EqualFold(e.Name, "JSON_VALUE") && len(e.Args) >= 2 {
			if colRef, isCol := e.Args[0].(*ColumnRefExpr); isCol {
				colName = cleanSchemaPrefix(colRef.Name)
				if lit, isLit := e.Args[1].(*LiteralExpr); isLit {
					rawPath := lit.Val.SVal
					if rawPath == "" && !lit.Val.IsNull {
						rawPath = lit.Val.String()
					}
					rawPath = strings.TrimSpace(rawPath)
					if !strings.HasPrefix(rawPath, "$") {
						if strings.HasPrefix(rawPath, "[") {
							rawPath = "$" + rawPath
						} else {
							rawPath = "$." + rawPath
						}
					}
					return colName, rawPath, true
				}
			}
		}
	}
	return "", "", false
}


package proc

import (
	"fmt"
	"strings"

	"lionsql/pkg/storage"
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

// PrecLowest represents the lowest operator precedence level for expression parsing.
const PrecLowest = precLowest

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
	TokenDoubleColon:     precPrefix,
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

func (p *Parser) addError(msg string) {
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


// CleanSchemaPrefix strips schema/database qualification, brackets, and quotes from identifiers.
// For system catalog views (sys.*, information_schema.*, pg_catalog.*), the system schema prefix is preserved.
// For default user schemas (dbo, public, guest) or multi-part database prefixes ([db].[dbo].[tbl], db..tbl),
// the base table name is returned.
func CleanSchemaPrefix(name string) string {
	s := strings.TrimSpace(name)
	if s == "" {
		return ""
	}
	// If the string contains arithmetic, logical, or expression operators, it is an expression, not an identifier.
	if strings.ContainsAny(s, "+-*/(),=><!~^&|;") {
		return s
	}
	// If it contains whitespace and does not start with bracket or quote, it is not a single identifier.
	if !strings.HasPrefix(s, "[") && !strings.HasPrefix(s, "\"") && !strings.HasPrefix(s, "`") {
		if strings.ContainsAny(s, " \t\r\n") {
			return s
		}
	}
	parts := strings.Split(s, ".")
	var cleanedParts []string
	for _, p := range parts {
		trimmed := strings.Trim(strings.TrimSpace(p), "[]\"'`")
		if trimmed != "" {
			cleanedParts = append(cleanedParts, trimmed)
		}
	}
	if len(cleanedParts) == 0 {
		return ""
	}
	if len(cleanedParts) == 1 {
		res := cleanedParts[0]
		if strings.HasPrefix(res, "##") {
			return strings.ToLower(res)
		}
		return res
	}

	last := cleanedParts[len(cleanedParts)-1]
	secondLast := strings.ToLower(cleanedParts[len(cleanedParts)-2])

	// Check for system catalog views
	if secondLast == "sys" || secondLast == "information_schema" || secondLast == "pg_catalog" || secondLast == "system" {
		return secondLast + "." + last
	}

	// Default user schemas: dbo, public, guest
	if secondLast == "dbo" || secondLast == "public" || secondLast == "guest" {
		return last
	}

	// Multi-part identifiers (e.g. mydb.dbo.accounts, mydb..accounts, server.mydb.dbo.accounts):
	// Under LionSQL's instance-level isolation, database catalog prefixes are stripped,
	// returning the target table name.
	if len(cleanedParts) >= 3 || len(parts) >= 3 {
		return last
	}

	// 2-part identifier that is not a recognized schema (e.g. alias.column or custom_schema.table):
	// Return normalized form "prefix.suffix"
	return strings.Join(cleanedParts, ".")
}

func cleanSchemaPrefix(name string) string {
	return CleanSchemaPrefix(name)
}

func isReservedAfterFromOrJoin(tok TokenType) bool {
	switch tok {
	case TokenWhere, TokenGroup, TokenHaving, TokenOrder, TokenLimit,
		TokenJoin, TokenInner, TokenLeft, TokenRight, TokenFull, TokenCross, TokenOuter, TokenApply,
		TokenOn, TokenWith, TokenFor, TokenOffset, TokenFetch, TokenOption,
		TokenSemicolon, TokenRParen, TokenEOF, TokenComma:
		return true
	default:
		return false
	}
}

func isReservedAfterProjection(tok TokenType) bool {
	switch tok {
	case TokenFrom, TokenWhere, TokenGroup, TokenHaving, TokenOrder, TokenLimit,
		TokenJoin, TokenInner, TokenLeft, TokenRight, TokenFull, TokenCross, TokenOuter, TokenApply,
		TokenOn, TokenWith, TokenFor, TokenOffset, TokenFetch, TokenOption,
		TokenSemicolon, TokenRParen, TokenEOF, TokenComma:
		return true
	default:
		return false
	}
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


// ParseSQL parses a single interactive or procedural SQL statement into an AST Statement.
func ParseSQL(sql string) (Statement, error) {
	p := NewParser(NewLexer(sql))
	return p.ParseSQLStatement()
}


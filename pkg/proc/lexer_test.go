package proc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLexer_BacktickIdentifiers(t *testing.T) {
	input := "SELECT `id`, `user``name`, `orders`.`amount`, `db`.`schema`.`tbl` FROM `accounts` AS `a`;"
	l := NewLexer(input)

	expected := []struct {
		typ TokenType
		lit string
	}{
		{TokenSelect, "SELECT"},
		{TokenIdent, "id"},
		{TokenComma, ","},
		{TokenIdent, "user`name"},
		{TokenComma, ","},
		{TokenIdent, "orders.amount"},
		{TokenComma, ","},
		{TokenIdent, "db.schema.tbl"},
		{TokenFrom, "FROM"},
		{TokenIdent, "accounts"},
		{TokenAs, "AS"},
		{TokenIdent, "a"},
		{TokenSemicolon, ";"},
		{TokenEOF, ""},
	}

	for i, exp := range expected {
		tok := l.NextToken()
		if tok.Type != exp.typ {
			t.Fatalf("[%d] expected token type %v, got %v (lit: %q)", i, exp.typ, tok.Type, tok.Literal)
		}
		if tok.Literal != exp.lit {
			t.Fatalf("[%d] expected literal %q, got %q", i, exp.lit, tok.Literal)
		}
	}
}

func TestLexer_Regression_P0_24_UTF8MultibyteIdentifiers(t *testing.T) {
	input := "SELECT 이름, 고객_번호, @사용자변수, #임시_테이블 FROM dbo.고객_테이블 WHERE 姓名 = '홍길동';"
	l := NewLexer(input)

	expected := []struct {
		typ TokenType
		lit string
	}{
		{TokenSelect, "SELECT"},
		{TokenIdent, "이름"},
		{TokenComma, ","},
		{TokenIdent, "고객_번호"},
		{TokenComma, ","},
		{TokenVariable, "@사용자변수"},
		{TokenComma, ","},
		{TokenIdent, "#임시_테이블"},
		{TokenFrom, "FROM"},
		{TokenIdent, "dbo.고객_테이블"},
		{TokenWhere, "WHERE"},
		{TokenIdent, "姓名"},
		{TokenEq, "="},
		{TokenString, "홍길동"},
		{TokenSemicolon, ";"},
		{TokenEOF, ""},
	}

	for i, exp := range expected {
		tok := l.NextToken()
		if tok.Type != exp.typ {
			t.Fatalf("[%d] expected token type %v, got %v (lit: %q)", i, exp.typ, tok.Type, tok.Literal)
		}
		if tok.Literal != exp.lit {
			t.Fatalf("[%d] expected literal %q, got %q", i, exp.lit, tok.Literal)
		}
	}
}

func TestLexer_LegacyComparisonAndWhitespace(t *testing.T) {
	input := "SELECT a !< 1, b !> 2, c != 3, d ! < 4, e ! > 5, f ! = 6, g < > 7, h < = 8, i > = 9;"
	l := NewLexer(input)

	expected := []struct {
		typ TokenType
	}{
		{TokenSelect},
		{TokenIdent}, {TokenGte}, {TokenInt}, {TokenComma}, // a !< 1 (>=)
		{TokenIdent}, {TokenLte}, {TokenInt}, {TokenComma}, // b !> 2 (<=)
		{TokenIdent}, {TokenNeq}, {TokenInt}, {TokenComma}, // c != 3 (!=)
		{TokenIdent}, {TokenGte}, {TokenInt}, {TokenComma}, // d ! < 4 (>=)
		{TokenIdent}, {TokenLte}, {TokenInt}, {TokenComma}, // e ! > 5 (<=)
		{TokenIdent}, {TokenNeq}, {TokenInt}, {TokenComma}, // f ! = 6 (!=)
		{TokenIdent}, {TokenNeq}, {TokenInt}, {TokenComma}, // g < > 7 (<>)
		{TokenIdent}, {TokenLte}, {TokenInt}, {TokenComma}, // h < = 8 (<=)
		{TokenIdent}, {TokenGte}, {TokenInt}, {TokenSemicolon}, // i > = 9 (>=)
		{TokenEOF},
	}

	for i, exp := range expected {
		tok := l.NextToken()
		if tok.Type != exp.typ {
			t.Fatalf("[%d] expected token type %v, got %v (lit: %q)", i, exp.typ, tok.Type, tok.Literal)
		}
	}
}

func TestLexer_QuantifiedKeywords(t *testing.T) {
	input := "ALL ANY SOME"
	l := NewLexer(input)

	if tok := l.NextToken(); tok.Type != TokenAll || tok.Literal != "ALL" {
		t.Fatalf("expected ALL, got %v (%q)", tok.Type, tok.Literal)
	}
	if tok := l.NextToken(); tok.Type != TokenAny || tok.Literal != "ANY" {
		t.Fatalf("expected ANY, got %v (%q)", tok.Type, tok.Literal)
	}
	if tok := l.NextToken(); tok.Type != TokenSome || tok.Literal != "SOME" {
		t.Fatalf("expected SOME, got %v (%q)", tok.Type, tok.Literal)
	}
}

func TestLexer_ScientificNotationAndLeadingDot(t *testing.T) {
	input := "1e5 1.5E-3 2e+10 3E4 .5 .123"
	l := NewLexer(input)

	expected := []struct {
		typ TokenType
		lit string
	}{
		{TokenFloat, "1e5"},
		{TokenFloat, "1.5E-3"},
		{TokenFloat, "2e+10"},
		{TokenFloat, "3E4"},
		{TokenFloat, ".5"},
		{TokenFloat, ".123"},
		{TokenEOF, ""},
	}

	for i, exp := range expected {
		tok := l.NextToken()
		if tok.Type != exp.typ {
			t.Fatalf("[%d] expected token type %v, got %v (lit: %q)", i, exp.typ, tok.Type, tok.Literal)
		}
		if tok.Literal != exp.lit {
			t.Fatalf("[%d] expected literal %q, got %q", i, exp.lit, tok.Literal)
		}
	}
}

func TestParser_ScientificNotationAndLeadingDot(t *testing.T) {
	sql := `
CREATE PROCEDURE TestScientificNum
AS
BEGIN
    SELECT 1e5, 1.5E-3, 2e+10, 3E4, .5, .123;
END;
`
	prog, err := Compile(sql)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}
	if prog == nil {
		t.Fatalf("expected compiled program, got nil")
	}
}

func TestLexer_DollarQuotedStringsAndPositionalParams(t *testing.T) {
	input := `SELECT $1, $12, $$body with 'quotes' and $1 inside$$, $ FROM tbl`
	l := NewLexer(input)

	expected := []struct {
		typ TokenType
		lit string
	}{
		{TokenSelect, "SELECT"},
		{TokenVariable, "$1"},
		{TokenComma, ","},
		{TokenVariable, "$12"},
		{TokenComma, ","},
		{TokenString, "body with 'quotes' and $1 inside"},
		{TokenComma, ","},
		{TokenIllegal, "$"},
		{TokenFrom, "FROM"},
		{TokenIdent, "tbl"},
		{TokenEOF, ""},
	}

	for i, exp := range expected {
		tok := l.NextToken()
		if tok.Type != exp.typ {
			t.Fatalf("[%d] expected token type %v, got %v (lit: %q)", i, exp.typ, tok.Type, tok.Literal)
		}
		if tok.Literal != exp.lit {
			t.Fatalf("[%d] expected literal %q, got %q", i, exp.lit, tok.Literal)
		}
	}
}

func TestLexer_ForwardProgressOnStandaloneDotAndSymbols(t *testing.T) {
	// Verify that standalone '.', '!', '$', and arbitrary symbols never stall the lexer
	input := "SELECT a . b, ! , $ , ? FROM [dbo]..[tbl]"
	l := NewLexer(input)

	maxSteps := 50
	steps := 0
	for {
		tok := l.NextToken()
		steps++
		if tok.Type == TokenEOF {
			break
		}
		if steps >= maxSteps {
			t.Fatalf("lexer failed to make forward progress (exceeded %d steps), stuck at token %+v", maxSteps, tok)
		}
	}
}

// TestArch_LexerEncapsulation enforces that no file in pkg/proc outside of lexer.go
// directly accesses internal Lexer cursor fields or methods (ch, pos, readPos, readChar, peekChar).
func TestArch_LexerEncapsulation(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("failed to read pkg/proc directory: %v", err)
	}

	forbiddenMembers := map[string]bool{
		"readChar":          true,
		"peekChar":          true,
		"readPos":           true,
		"advancePastIndex":  true,
		"peekNonSpaceByte":  true,
		"consumeRawDotChain": true,
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || name == "lexer.go" {
			continue
		}

		f, err := parser.ParseFile(fset, filepath.Clean(name), nil, 0)
		if err != nil {
			t.Fatalf("failed to parse %s: %v", name, err)
		}

		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			member := sel.Sel.Name
			if forbiddenMembers[member] {
				pos := fset.Position(sel.Pos())
				t.Errorf("[ARCH-GUARD] %s:%d:%d accesses private Lexer member .%s outside lexer.go",
					name, pos.Line, pos.Column, member)
			}
			// Check direct .ch or .pos access on lexer variable 'l'
			if ident, ok := sel.X.(*ast.Ident); ok && (ident.Name == "l" || ident.Name == "lexer") {
				if member == "ch" || member == "pos" || member == "input" {
					pos := fset.Position(sel.Pos())
					t.Errorf("[ARCH-GUARD] %s:%d:%d accesses private Lexer field %s.%s outside lexer.go",
						name, pos.Line, pos.Column, ident.Name, member)
				}
			}
			return true
		})
	}
}

func TestLexer_BracketIdentifierEscaping(t *testing.T) {
	input := "SELECT [id], [user]]name], [col]]]]val], [db].[schema].[tbl]]name] FROM [accounts]]archive];"
	l := NewLexer(input)

	expected := []struct {
		typ TokenType
		lit string
	}{
		{TokenSelect, "SELECT"},
		{TokenIdent, "[id]"},
		{TokenComma, ","},
		{TokenIdent, "[user]]name]"},
		{TokenComma, ","},
		{TokenIdent, "[col]]]]val]"},
		{TokenComma, ","},
		{TokenIdent, "[db].[schema].[tbl]]name]"},
		{TokenFrom, "FROM"},
		{TokenIdent, "[accounts]]archive]"},
		{TokenSemicolon, ";"},
		{TokenEOF, ""},
	}

	for i, exp := range expected {
		tok := l.NextToken()
		if tok.Type != exp.typ {
			t.Fatalf("[%d] expected token type %v, got %v (lit: %q)", i, exp.typ, tok.Type, tok.Literal)
		}
		if tok.Literal != exp.lit {
			t.Fatalf("[%d] expected literal %q, got %q", i, exp.lit, tok.Literal)
		}
	}
}

func TestLexer_UnclosedBracketIdentifier(t *testing.T) {
	input := "SELECT [unclosed_ident"
	l := NewLexer(input)

	_ = l.NextToken() // SELECT
	tok := l.NextToken()
	if tok.Type != TokenIllegal {
		t.Fatalf("expected TokenIllegal for unclosed bracket identifier, got %v (lit: %q)", tok.Type, tok.Literal)
	}
	if tok.Literal != "[unclosed_ident" {
		t.Fatalf("expected literal %q, got %q", "[unclosed_ident", tok.Literal)
	}
}

func TestLexer_DollarQuotedString(t *testing.T) {
	// 1. Properly closed dollar-quoted string
	input := "$$hello world$$"
	l := NewLexer(input)
	tok := l.NextToken()
	if tok.Type != TokenString {
		t.Fatalf("expected TokenString, got %v", tok.Type)
	}
	if tok.Literal != "hello world" {
		t.Fatalf("expected 'hello world', got %q", tok.Literal)
	}

	// 2. Unclosed dollar-quoted string must return TokenIllegal
	inputUnclosed := "$$unclosed string literal"
	l2 := NewLexer(inputUnclosed)
	tok2 := l2.NextToken()
	if tok2.Type != TokenIllegal {
		t.Fatalf("expected TokenIllegal for unclosed $$, got %v (lit: %q)", tok2.Type, tok2.Literal)
	}
	if tok2.Literal != "$$unclosed string literal" {
		t.Fatalf("expected literal %q, got %q", "$$unclosed string literal", tok2.Literal)
	}
}


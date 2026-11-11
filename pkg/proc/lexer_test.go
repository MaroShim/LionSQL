package proc

import (
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


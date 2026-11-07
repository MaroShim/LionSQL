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


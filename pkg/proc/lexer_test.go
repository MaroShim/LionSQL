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

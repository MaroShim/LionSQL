package proc

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Lexer tokenizes T-SQL procedural scripts.
type Lexer struct {
	input   string
	pos     int
	readPos int
	ch      rune
	line    int
	col     int
}

// NewLexer creates a new Lexer for the given input.
func NewLexer(input string) *Lexer {
	l := &Lexer{
		input: input,
		line:  1,
		col:   0,
	}
	l.readChar()
	return l
}

func (l *Lexer) readChar() {
	if l.readPos >= len(l.input) {
		l.ch = 0
		l.pos = l.readPos
	} else {
		r, w := utf8.DecodeRuneInString(l.input[l.readPos:])
		l.ch = r
		l.pos = l.readPos
		l.readPos += w
	}
	l.col++
}

func (l *Lexer) peekChar() rune {
	if l.readPos >= len(l.input) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.input[l.readPos:])
	return r
}

// NextToken returns the next lexical token.
func (l *Lexer) NextToken() Token {
	l.skipWhitespaceAndComments()

	tokLine := l.line
	tokCol := l.col

	var tok Token

	switch l.ch {
	case 0:
		tok = Token{Type: TokenEOF, Literal: "", Line: tokLine, Col: tokCol}
	case '=':
		tok = Token{Type: TokenEq, Literal: "=", Line: tokLine, Col: tokCol}
	case '+':
		tok = Token{Type: TokenPlus, Literal: "+", Line: tokLine, Col: tokCol}
	case '-':
		if l.peekChar() == '>' {
			l.readChar()
			if l.peekChar() == '>' {
				l.readChar()
				tok = Token{Type: TokenJSONExtractText, Literal: "->>", Line: tokLine, Col: tokCol}
			} else {
				tok = Token{Type: TokenJSONExtract, Literal: "->", Line: tokLine, Col: tokCol}
			}
		} else {
			tok = Token{Type: TokenMinus, Literal: "-", Line: tokLine, Col: tokCol}
		}
	case '*':
		tok = Token{Type: TokenAsterisk, Literal: "*", Line: tokLine, Col: tokCol}
	case '/':
		tok = Token{Type: TokenSlash, Literal: "/", Line: tokLine, Col: tokCol}
	case '%':
		tok = Token{Type: TokenPercent, Literal: "%", Line: tokLine, Col: tokCol}
	case ',':
		tok = Token{Type: TokenComma, Literal: ",", Line: tokLine, Col: tokCol}
	case ';':
		tok = Token{Type: TokenSemicolon, Literal: ";", Line: tokLine, Col: tokCol}
	case ':':
		if l.peekChar() == ':' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TokenDoubleColon, Literal: string(ch) + string(l.ch), Line: tokLine, Col: tokCol}
		} else {
			tok = Token{Type: TokenColon, Literal: ":", Line: tokLine, Col: tokCol}
		}
	case '(':
		tok = Token{Type: TokenLParen, Literal: "(", Line: tokLine, Col: tokCol}
	case ')':
		tok = Token{Type: TokenRParen, Literal: ")", Line: tokLine, Col: tokCol}
	case '!':
		if l.peekChar() == '=' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TokenNeq, Literal: string(ch) + string(l.ch), Line: tokLine, Col: tokCol}
		} else {
			tok = Token{Type: TokenIllegal, Literal: string(l.ch), Line: tokLine, Col: tokCol}
		}
	case '<':
		if l.peekChar() == '=' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TokenLte, Literal: string(ch) + string(l.ch), Line: tokLine, Col: tokCol}
		} else if l.peekChar() == '>' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TokenNeq, Literal: string(ch) + string(l.ch), Line: tokLine, Col: tokCol}
		} else {
			tok = Token{Type: TokenLt, Literal: "<", Line: tokLine, Col: tokCol}
		}
	case '>':
		if l.peekChar() == '=' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TokenGte, Literal: string(ch) + string(l.ch), Line: tokLine, Col: tokCol}
		} else {
			tok = Token{Type: TokenGt, Literal: ">", Line: tokLine, Col: tokCol}
		}
	case '|':
		if l.peekChar() == '|' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TokenConcat, Literal: string(ch) + string(l.ch), Line: tokLine, Col: tokCol}
		} else {
			tok = Token{Type: TokenIllegal, Literal: string(l.ch), Line: tokLine, Col: tokCol}
		}
	case '\'':
		tok.Type = TokenString
		tok.Literal = l.readString()
		tok.Line = tokLine
		tok.Col = tokCol
		return tok
	case '"':
		tok.Type = TokenIdent
		tok.Literal = l.readQuotedIdentifier()
		tok.Line = tokLine
		tok.Col = tokCol
		return tok
	case '[':
		tok.Type = TokenIdent
		tok.Literal = l.readBracketedIdentifier()
		tok.Line = tokLine
		tok.Col = tokCol
		return tok
	case '`':
		tok.Type = TokenIdent
		tok.Literal = l.readBacktickIdentifier()
		tok.Line = tokLine
		tok.Col = tokCol
		return tok
	case '@':
		tok.Type = TokenVariable
		tok.Literal = l.readVariable()
		tok.Line = tokLine
		tok.Col = tokCol
		return tok
	default:
		if (l.ch == 'N' || l.ch == 'n' || l.ch == 'X' || l.ch == 'x') && l.peekChar() == '\'' {
			l.readChar() // skip 'N', 'n', 'X', or 'x'
			tok.Type = TokenString
			tok.Literal = l.readString()
			tok.Line = tokLine
			tok.Col = tokCol
			return tok
		}
		if isLetter(l.ch) {
			ident := l.readIdentifier()
			upper := strings.ToUpper(ident)
			if kwType, ok := keywords[upper]; ok {
				tok = Token{Type: kwType, Literal: upper, Line: tokLine, Col: tokCol}
			} else {
				tok = Token{Type: TokenIdent, Literal: ident, Line: tokLine, Col: tokCol}
			}
			return tok
		} else if isDigit(l.ch) {
			if l.ch == '0' && (l.peekChar() == 'x' || l.peekChar() == 'X') {
				hexLit := l.readHexLiteral()
				tok = Token{Type: TokenString, Literal: hexLit, Line: tokLine, Col: tokCol}
				return tok
			}
			lit, isFloat := l.readNumber()
			if isFloat {
				tok = Token{Type: TokenFloat, Literal: lit, Line: tokLine, Col: tokCol}
			} else {
				tok = Token{Type: TokenInt, Literal: lit, Line: tokLine, Col: tokCol}
			}
			return tok
		} else {
			tok = Token{Type: TokenIllegal, Literal: string(l.ch), Line: tokLine, Col: tokCol}
		}
	}

	l.readChar()
	return tok
}

func (l *Lexer) skipWhitespaceAndComments() {
	for {
		if l.ch == ' ' || l.ch == '\t' || l.ch == '\r' {
			l.readChar()
		} else if l.ch == '\n' {
			l.line++
			l.col = 0
			l.readChar()
		} else if l.ch == '-' && l.peekChar() == '-' {
			// Single-line comment
			for l.ch != '\n' && l.ch != 0 {
				l.readChar()
			}
		} else if l.ch == '/' && l.peekChar() == '*' {
			// Multi-line comment
			l.readChar()
			l.readChar()
			for !(l.ch == '*' && l.peekChar() == '/') && l.ch != 0 {
				if l.ch == '\n' {
					l.line++
					l.col = 0
				}
				l.readChar()
			}
			if l.ch == '*' {
				l.readChar() // eat *
				l.readChar() // eat /
			}
		} else {
			break
		}
	}
}

func (l *Lexer) readBracketedIdentifier() string {
	startPos := l.pos
	l.readChar() // skip '['
	for l.ch != ']' && l.ch != 0 {
		l.readChar()
	}
	if l.ch == ']' {
		l.readChar() // skip ']'
	}
	// Check if chained with . e.g. [dbo].[sp_name], [db].[schema].[table], [db]..[table]
	for l.ch == '.' {
		if l.peekChar() == '.' {
			l.readChar() // eat '.'
			l.readChar() // eat second '.'
			if l.ch == '[' {
				l.readBracketedIdentifier()
			} else if l.ch == '"' {
				l.readQuotedIdentifier()
			} else if l.ch == '`' {
				l.readBacktickIdentifier()
			} else if isLetter(l.ch) {
				l.readIdentifier()
			}
		} else if l.peekChar() == '[' || l.peekChar() == '"' || l.peekChar() == '`' || isLetter(l.peekChar()) || l.peekChar() == '*' {
			l.readChar() // eat '.'
			if l.ch == '[' {
				l.readBracketedIdentifier()
			} else if l.ch == '"' {
				l.readQuotedIdentifier()
			} else if l.ch == '`' {
				l.readBacktickIdentifier()
			} else if l.ch == '*' {
				l.readChar() // eat '*'
			} else {
				l.readIdentifier()
			}
		} else {
			break
		}
	}
	return l.input[startPos:l.pos]
}

func (l *Lexer) readQuotedIdentifier() string {
	l.readChar() // skip initial '"'
	var b strings.Builder
	for {
		if l.ch == 0 {
			break
		}
		if l.ch == '"' {
			if l.peekChar() == '"' {
				// Escaped quote ""
				b.WriteRune('"')
				l.readChar()
				l.readChar()
				continue
			}
			l.readChar() // skip closing quote
			break
		}
		b.WriteRune(l.ch)
		l.readChar()
	}
	// Check if dot-qualified e.g. "dbo"."customers"
	for l.ch == '.' {
		if l.peekChar() == '.' {
			l.readChar() // eat '.'
			l.readChar() // eat second '.'
			if l.ch == '"' {
				return b.String() + ".." + l.readQuotedIdentifier()
			} else if l.ch == '[' {
				return b.String() + ".." + l.readBracketedIdentifier()
			} else if l.ch == '`' {
				return b.String() + ".." + l.readBacktickIdentifier()
			} else if isLetter(l.ch) {
				return b.String() + ".." + l.readIdentifier()
			}
		} else if l.peekChar() == '"' || l.peekChar() == '[' || l.peekChar() == '`' || isLetter(l.peekChar()) || l.peekChar() == '*' {
			l.readChar() // eat '.'
			if l.ch == '"' {
				return b.String() + "." + l.readQuotedIdentifier()
			} else if l.ch == '[' {
				return b.String() + "." + l.readBracketedIdentifier()
			} else if l.ch == '`' {
				return b.String() + "." + l.readBacktickIdentifier()
			} else if l.ch == '*' {
				l.readChar() // eat '*'
				return b.String() + ".*"
			} else {
				return b.String() + "." + l.readIdentifier()
			}
		} else {
			break
		}
	}
	return b.String()
}

func (l *Lexer) readBacktickIdentifier() string {
	l.readChar() // skip initial '`'
	var b strings.Builder
	for {
		if l.ch == 0 {
			break
		}
		if l.ch == '`' {
			if l.peekChar() == '`' {
				// Escaped backtick ``
				b.WriteRune('`')
				l.readChar()
				l.readChar()
				continue
			}
			l.readChar() // skip closing backtick
			break
		}
		b.WriteRune(l.ch)
		l.readChar()
	}
	// Check if dot-qualified e.g. `dbo`.`customers`
	for l.ch == '.' {
		if l.peekChar() == '.' {
			l.readChar() // eat '.'
			l.readChar() // eat second '.'
			if l.ch == '`' {
				return b.String() + ".." + l.readBacktickIdentifier()
			} else if l.ch == '"' {
				return b.String() + ".." + l.readQuotedIdentifier()
			} else if l.ch == '[' {
				return b.String() + ".." + l.readBracketedIdentifier()
			} else if isLetter(l.ch) {
				return b.String() + ".." + l.readIdentifier()
			}
		} else if l.peekChar() == '`' || l.peekChar() == '"' || l.peekChar() == '[' || isLetter(l.peekChar()) || l.peekChar() == '*' {
			l.readChar() // eat '.'
			if l.ch == '`' {
				return b.String() + "." + l.readBacktickIdentifier()
			} else if l.ch == '"' {
				return b.String() + "." + l.readQuotedIdentifier()
			} else if l.ch == '[' {
				return b.String() + "." + l.readBracketedIdentifier()
			} else if l.ch == '*' {
				l.readChar() // eat '*'
				return b.String() + ".*"
			} else {
				return b.String() + "." + l.readIdentifier()
			}
		} else {
			break
		}
	}
	return b.String()
}

func (l *Lexer) readIdentifier() string {
	startPos := l.pos
	for isLetter(l.ch) || isDigit(l.ch) || l.ch == '_' {
		l.readChar()
	}
	// Check if chained with . e.g. dbo.sp_name, db.dbo.table, db..table, a.*
	for l.ch == '.' {
		if l.peekChar() == '.' {
			l.readChar() // eat '.'
			l.readChar() // eat second '.'
			if l.ch == '[' {
				l.readBracketedIdentifier()
			} else if l.ch == '"' {
				l.readQuotedIdentifier()
			} else if l.ch == '`' {
				l.readBacktickIdentifier()
			} else if isLetter(l.ch) {
				for isLetter(l.ch) || isDigit(l.ch) || l.ch == '_' {
					l.readChar()
				}
			}
		} else if isLetter(l.peekChar()) || isDigit(l.peekChar()) || l.peekChar() == '_' || l.peekChar() == '[' || l.peekChar() == '"' || l.peekChar() == '`' || l.peekChar() == '*' {
			l.readChar() // eat '.'
			if l.ch == '[' {
				l.readBracketedIdentifier()
			} else if l.ch == '"' {
				l.readQuotedIdentifier()
			} else if l.ch == '`' {
				l.readBacktickIdentifier()
			} else if l.ch == '*' {
				l.readChar() // eat '*'
			} else {
				for isLetter(l.ch) || isDigit(l.ch) || l.ch == '_' {
					l.readChar()
				}
			}
		} else {
			break
		}
	}
	return l.input[startPos:l.pos]
}

func (l *Lexer) readVariable() string {
	startPos := l.pos
	l.readChar() // eat first '@'
	if l.ch == '@' {
		l.readChar() // eat second '@' for @@FETCH_STATUS etc
	}
	for isLetter(l.ch) || isDigit(l.ch) || l.ch == '_' {
		l.readChar()
	}
	return l.input[startPos:l.pos]
}

func (l *Lexer) readHexLiteral() string {
	startPos := l.pos
	l.readChar() // '0'
	l.readChar() // 'x' or 'X'
	for isDigit(l.ch) || (l.ch >= 'a' && l.ch <= 'f') || (l.ch >= 'A' && l.ch <= 'F') {
		l.readChar()
	}
	return l.input[startPos:l.pos]
}

func (l *Lexer) readNumber() (string, bool) {
	startPos := l.pos
	isFloat := false
	for isDigit(l.ch) {
		l.readChar()
	}
	if l.ch == '.' && isDigit(l.peekChar()) {
		isFloat = true
		l.readChar() // eat '.'
		for isDigit(l.ch) {
			l.readChar()
		}
	}
	return l.input[startPos:l.pos], isFloat
}

func (l *Lexer) readString() string {
	l.readChar() // skip initial quote
	var b strings.Builder
	for {
		if l.ch == 0 {
			break
		}
		if l.ch == '\'' {
			if l.peekChar() == '\'' {
				// Escaped quote ''
				b.WriteRune('\'')
				l.readChar()
				l.readChar()
				continue
			}
			l.readChar() // skip closing quote
			break
		}
		b.WriteRune(l.ch)
		l.readChar()
	}
	return b.String()
}

func isLetter(ch rune) bool {
	return unicode.IsLetter(ch) || ch == '_' || ch == '#'
}

func isDigit(ch rune) bool {
	return '0' <= ch && ch <= '9'
}

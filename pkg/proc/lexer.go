package proc

import (
	"strings"
	"unicode"
)

// Lexer tokenizes T-SQL procedural scripts.
type Lexer struct {
	input   string
	pos     int
	readPos int
	ch      byte
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
	} else {
		l.ch = l.input[l.readPos]
	}
	l.pos = l.readPos
	l.readPos++
	l.col++
}

func (l *Lexer) peekChar() byte {
	if l.readPos >= len(l.input) {
		return 0
	}
	return l.input[l.readPos]
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
		tok = Token{Type: TokenMinus, Literal: "-", Line: tokLine, Col: tokCol}
	case '*':
		tok = Token{Type: TokenAsterisk, Literal: "*", Line: tokLine, Col: tokCol}
	case '/':
		tok = Token{Type: TokenSlash, Literal: "/", Line: tokLine, Col: tokCol}
	case ',':
		tok = Token{Type: TokenComma, Literal: ",", Line: tokLine, Col: tokCol}
	case ';':
		tok = Token{Type: TokenSemicolon, Literal: ";", Line: tokLine, Col: tokCol}
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
	case '\'':
		tok.Type = TokenString
		tok.Literal = l.readString()
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

func (l *Lexer) readIdentifier() string {
	startPos := l.pos
	for isLetter(l.ch) || isDigit(l.ch) || l.ch == '_' {
		l.readChar()
	}
	return l.input[startPos:l.pos]
}

func (l *Lexer) readVariable() string {
	startPos := l.pos
	l.readChar() // eat '@'
	for isLetter(l.ch) || isDigit(l.ch) || l.ch == '_' {
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
				b.WriteByte('\'')
				l.readChar()
				l.readChar()
				continue
			}
			l.readChar() // skip closing quote
			break
		}
		b.WriteByte(l.ch)
		l.readChar()
	}
	return b.String()
}

func isLetter(ch byte) bool {
	return unicode.IsLetter(rune(ch)) || ch == '_'
}

func isDigit(ch byte) bool {
	return '0' <= ch && ch <= '9'
}

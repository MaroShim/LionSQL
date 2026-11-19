package proc

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Lexer tokenizes T-SQL and multi-dialect SQL scripts into a stream of Tokens.
// Internal cursor state (pos, readPos, ch) is strictly encapsulated; callers must
// interact exclusively via NextToken() and ConsumeDotIfPresent().
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

// ConsumeDotIfPresent skips horizontal/vertical whitespace and comments, and if the
// current character is a dot '.', consumes it and returns true.
func (l *Lexer) ConsumeDotIfPresent() bool {
	l.skipWhitespaceAndComments()
	if l.ch == '.' {
		l.readChar()
		return true
	}
	return false
}

// NextToken returns the next lexical token while enforcing a strict forward-progress
// invariant so that no token branch can ever stall on the same character offset.
func (l *Lexer) NextToken() Token {
	l.skipWhitespaceAndComments()

	tokLine := max(l.line, 1)
	tokCol := max(l.col, 1)

	if l.ch == 0 {
		return Token{Type: TokenEOF, Literal: "", Line: tokLine, Col: tokCol}
	}

	startPos := l.pos
	tok := l.scanToken(tokLine, tokCol)
	tok.Pos = startPos
	tok.End = l.pos

	// Defensive Forward-Progress Invariant ([RULE:LexicalSafety]):
	// If any branch forgot to advance the cursor on a non-EOF rune, force advancement
	// to prevent infinite tokenization loops.
	if l.ch != 0 && l.pos == startPos {
		l.readChar()
	}

	return tok
}

func (l *Lexer) scanToken(tokLine, tokCol int) Token {
	switch l.ch {
	case '=':
		l.readChar()
		return Token{Type: TokenEq, Literal: "=", Line: tokLine, Col: tokCol}
	case '+':
		if l.peekChar() == '=' {
			l.readChar()
			l.readChar()
			return Token{Type: TokenPlusAssign, Literal: "+=", Line: tokLine, Col: tokCol}
		}
		l.readChar()
		return Token{Type: TokenPlus, Literal: "+", Line: tokLine, Col: tokCol}
	case '-':
		if l.peekChar() == '>' {
			l.readChar()
			if l.peekChar() == '>' {
				l.readChar()
				l.readChar()
				return Token{Type: TokenJSONExtractText, Literal: "->>", Line: tokLine, Col: tokCol}
			}
			l.readChar()
			return Token{Type: TokenJSONExtract, Literal: "->", Line: tokLine, Col: tokCol}
		} else if l.peekChar() == '=' {
			l.readChar()
			l.readChar()
			return Token{Type: TokenMinusAssign, Literal: "-=", Line: tokLine, Col: tokCol}
		}
		l.readChar()
		return Token{Type: TokenMinus, Literal: "-", Line: tokLine, Col: tokCol}
	case '*':
		if l.peekChar() == '=' {
			l.readChar()
			l.readChar()
			return Token{Type: TokenAsteriskAssign, Literal: "*=", Line: tokLine, Col: tokCol}
		}
		l.readChar()
		return Token{Type: TokenAsterisk, Literal: "*", Line: tokLine, Col: tokCol}
	case '/':
		if l.peekChar() == '=' {
			l.readChar()
			l.readChar()
			return Token{Type: TokenSlashAssign, Literal: "/=", Line: tokLine, Col: tokCol}
		}
		l.readChar()
		return Token{Type: TokenSlash, Literal: "/", Line: tokLine, Col: tokCol}
	case '%':
		if l.peekChar() == '=' {
			l.readChar()
			l.readChar()
			return Token{Type: TokenModuloAssign, Literal: "%=", Line: tokLine, Col: tokCol}
		}
		l.readChar()
		return Token{Type: TokenPercent, Literal: "%", Line: tokLine, Col: tokCol}
	case ',':
		l.readChar()
		return Token{Type: TokenComma, Literal: ",", Line: tokLine, Col: tokCol}
	case ';':
		l.readChar()
		return Token{Type: TokenSemicolon, Literal: ";", Line: tokLine, Col: tokCol}
	case ':':
		if l.peekChar() == ':' {
			l.readChar()
			l.readChar()
			return Token{Type: TokenDoubleColon, Literal: "::", Line: tokLine, Col: tokCol}
		}
		l.readChar()
		return Token{Type: TokenColon, Literal: ":", Line: tokLine, Col: tokCol}
	case '(':
		l.readChar()
		return Token{Type: TokenLParen, Literal: "(", Line: tokLine, Col: tokCol}
	case ')':
		l.readChar()
		return Token{Type: TokenRParen, Literal: ")", Line: tokLine, Col: tokCol}
	case '!':
		return l.readBangComparison(tokLine, tokCol)
	case '<':
		return l.readLessComparison(tokLine, tokCol)
	case '>':
		return l.readGreaterComparison(tokLine, tokCol)
	case '|':
		if l.peekChar() == '|' {
			l.readChar()
			l.readChar()
			return Token{Type: TokenConcat, Literal: "||", Line: tokLine, Col: tokCol}
		} else if l.peekChar() == '=' {
			l.readChar()
			l.readChar()
			return Token{Type: TokenBitOrAssign, Literal: "|=", Line: tokLine, Col: tokCol}
		}
		l.readChar()
		return Token{Type: TokenBitwiseOr, Literal: "|", Line: tokLine, Col: tokCol}
	case '&':
		if l.peekChar() == '&' {
			l.readChar()
			l.readChar()
			return Token{Type: TokenAnd, Literal: "&&", Line: tokLine, Col: tokCol}
		} else if l.peekChar() == '=' {
			l.readChar()
			l.readChar()
			return Token{Type: TokenBitAndAssign, Literal: "&=", Line: tokLine, Col: tokCol}
		}
		l.readChar()
		return Token{Type: TokenBitwiseAnd, Literal: "&", Line: tokLine, Col: tokCol}
	case '^':
		if l.peekChar() == '=' {
			l.readChar()
			l.readChar()
			return Token{Type: TokenBitXorAssign, Literal: "^=", Line: tokLine, Col: tokCol}
		}
		l.readChar()
		return Token{Type: TokenBitwiseXor, Literal: "^", Line: tokLine, Col: tokCol}
	case '~':
		l.readChar()
		return Token{Type: TokenBitwiseNot, Literal: "~", Line: tokLine, Col: tokCol}
	case '\'':
		return Token{Type: TokenString, Literal: l.readString(), Line: tokLine, Col: tokCol}
	case '"':
		return Token{Type: TokenIdent, Literal: l.readQuotedIdentifier(), Line: tokLine, Col: tokCol}
	case '[':
		return Token{Type: TokenIdent, Literal: l.readBracketedIdentifier(), Line: tokLine, Col: tokCol}
	case '`':
		return Token{Type: TokenIdent, Literal: l.readBacktickIdentifier(), Line: tokLine, Col: tokCol}
	case '@':
		return Token{Type: TokenVariable, Literal: l.readVariable(), Line: tokLine, Col: tokCol}
	case '$':
		return l.readDollarToken(tokLine, tokCol)
	case '.':
		if isDigit(l.peekChar()) {
			lit, isFloat := l.readNumber()
			if isFloat {
				return Token{Type: TokenFloat, Literal: lit, Line: tokLine, Col: tokCol}
			}
			return Token{Type: TokenInt, Literal: lit, Line: tokLine, Col: tokCol}
		}
		if l.peekChar() == '.' || isLetter(l.peekChar()) || l.peekChar() == '[' || l.peekChar() == '"' || l.peekChar() == '`' {
			startPos := l.pos
			l.consumeRawDotChain()
			return Token{Type: TokenIdent, Literal: l.input[startPos:l.pos], Raw: l.input[startPos:l.pos], Line: tokLine, Col: tokCol}
		}
		l.readChar()
		return Token{Type: TokenIllegal, Literal: ".", Line: tokLine, Col: tokCol}
	default:
		if (l.ch == 'N' || l.ch == 'n' || l.ch == 'X' || l.ch == 'x') && l.peekChar() == '\'' {
			l.readChar() // skip 'N', 'n', 'X', or 'x'
			return Token{Type: TokenString, Literal: l.readString(), Line: tokLine, Col: tokCol}
		}
		if isLetter(l.ch) {
			ident := l.readIdentifier()
			upper := strings.ToUpper(ident)
			if kwType, ok := keywords[upper]; ok {
				return Token{Type: kwType, Literal: upper, Raw: ident, Line: tokLine, Col: tokCol}
			}
			return Token{Type: TokenIdent, Literal: ident, Raw: ident, Line: tokLine, Col: tokCol}
		}
		if isDigit(l.ch) {
			if l.ch == '0' && (l.peekChar() == 'x' || l.peekChar() == 'X') {
				return Token{Type: TokenBinary, Literal: l.readHexLiteral(), Line: tokLine, Col: tokCol}
			}
			lit, isFloat := l.readNumber()
			if isFloat {
				return Token{Type: TokenFloat, Literal: lit, Line: tokLine, Col: tokCol}
			}
			return Token{Type: TokenInt, Literal: lit, Line: tokLine, Col: tokCol}
		}
		chLit := string(l.ch)
		l.readChar()
		return Token{Type: TokenIllegal, Literal: chLit, Line: tokLine, Col: tokCol}
	}
}

// peekNonSpaceByte looks ahead from l.readPos skipping ASCII spaces/tabs/newlines,
// returning the index and byte of the next non-space character if any.
func (l *Lexer) peekNonSpaceByte() (int, byte, bool) {
	pPos := l.readPos
	for pPos < len(l.input) && (l.input[pPos] == ' ' || l.input[pPos] == '\t' || l.input[pPos] == '\r' || l.input[pPos] == '\n') {
		pPos++
	}
	if pPos < len(l.input) {
		return pPos, l.input[pPos], true
	}
	return pPos, 0, false
}

// advancePastIndex advances the lexer cursor past targetPos (inclusive) and one additional step
// so l.ch points to the rune immediately following targetPos.
func (l *Lexer) advancePastIndex(targetPos int) {
	for l.readPos <= targetPos {
		l.readChar()
	}
	l.readChar()
}

func (l *Lexer) readBangComparison(tokLine, tokCol int) Token {
	// In T-SQL, '!' is valid in !=, !<, !>, optionally separated by whitespace (BABEL-3379 / BABEL-4114)
	if pPos, nextCh, ok := l.peekNonSpaceByte(); ok {
		switch nextCh {
		case '=':
			l.advancePastIndex(pPos)
			return Token{Type: TokenNeq, Literal: "!=", Line: tokLine, Col: tokCol}
		case '<':
			l.advancePastIndex(pPos)
			return Token{Type: TokenGte, Literal: "!<", Line: tokLine, Col: tokCol}
		case '>':
			l.advancePastIndex(pPos)
			return Token{Type: TokenLte, Literal: "!>", Line: tokLine, Col: tokCol}
		}
	}
	chLit := string(l.ch)
	l.readChar()
	return Token{Type: TokenIllegal, Literal: chLit, Line: tokLine, Col: tokCol}
}

func (l *Lexer) readLessComparison(tokLine, tokCol int) Token {
	if pPos, nextCh, ok := l.peekNonSpaceByte(); ok && (nextCh == '=' || nextCh == '>') {
		l.advancePastIndex(pPos)
		if nextCh == '=' {
			return Token{Type: TokenLte, Literal: "<=", Line: tokLine, Col: tokCol}
		}
		return Token{Type: TokenNeq, Literal: "<>", Line: tokLine, Col: tokCol}
	}
	l.readChar()
	return Token{Type: TokenLt, Literal: "<", Line: tokLine, Col: tokCol}
}

func (l *Lexer) readGreaterComparison(tokLine, tokCol int) Token {
	if pPos, nextCh, ok := l.peekNonSpaceByte(); ok && nextCh == '=' {
		l.advancePastIndex(pPos)
		return Token{Type: TokenGte, Literal: ">=", Line: tokLine, Col: tokCol}
	}
	l.readChar()
	return Token{Type: TokenGt, Literal: ">", Line: tokLine, Col: tokCol}
}

func (l *Lexer) readDollarToken(tokLine, tokCol int) Token {
	if l.peekChar() == '$' {
		l.readChar() // eat first '$'
		l.readChar() // eat second '$'
		startPos := l.pos
		for l.ch != 0 {
			if l.ch == '$' && l.peekChar() == '$' {
				content := l.input[startPos:l.pos]
				l.readChar() // eat closing '$'
				l.readChar() // eat closing '$'
				return Token{Type: TokenString, Literal: content, Line: tokLine, Col: tokCol}
			}
			l.readChar()
		}
		return Token{Type: TokenString, Literal: l.input[startPos:l.pos], Line: tokLine, Col: tokCol}
	}
	if isDigit(l.peekChar()) {
		startPos := l.pos
		l.readChar() // eat '$'
		for isDigit(l.ch) {
			l.readChar()
		}
		return Token{Type: TokenVariable, Literal: l.input[startPos:l.pos], Line: tokLine, Col: tokCol}
	}
	if isLetter(l.peekChar()) {
		ident := l.readIdentifier()
		return Token{Type: TokenIdent, Literal: ident, Raw: ident, Line: tokLine, Col: tokCol}
	}
	l.readChar()
	return Token{Type: TokenIllegal, Literal: "$", Line: tokLine, Col: tokCol}
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
	l.readBracketedSegment()
	l.consumeRawDotChain()
	return l.input[startPos:l.pos]
}

func (l *Lexer) readBracketedSegment() {
	l.readChar() // skip '['
	for l.ch != ']' && l.ch != 0 {
		l.readChar()
	}
	if l.ch == ']' {
		l.readChar() // skip ']'
	}
}

func (l *Lexer) readQuotedIdentifier() string {
	return l.readDelimitedIdentifier('"')
}

func (l *Lexer) readBacktickIdentifier() string {
	return l.readDelimitedIdentifier('`')
}

// readDelimitedIdentifier reads a double-quoted ("...") or backtick-quoted (`...`)
// identifier segment, unescaping doubled delimiters, and appends any dot-qualified suffix.
func (l *Lexer) readDelimitedIdentifier(quote rune) string {
	l.readChar() // skip initial delimiter
	var b strings.Builder
	for l.ch != 0 {
		if l.ch == quote {
			if l.peekChar() == quote {
				b.WriteRune(quote)
				l.readChar()
				l.readChar()
				continue
			}
			l.readChar() // skip closing delimiter
			break
		}
		b.WriteRune(l.ch)
		l.readChar()
	}
	return b.String() + l.readChainedSuffix()
}

// readChainedSuffix reads optional .part or ..part chains following a quoted/backtick identifier.
func (l *Lexer) readChainedSuffix() string {
	if l.ch != '.' {
		return ""
	}
	if l.peekChar() == '.' {
		l.readChar() // eat '.'
		l.readChar() // eat second '.'
		switch {
		case l.ch == '"':
			return ".." + l.readQuotedIdentifier()
		case l.ch == '[':
			return ".." + l.readBracketedIdentifier()
		case l.ch == '`':
			return ".." + l.readBacktickIdentifier()
		case isLetter(l.ch):
			return ".." + l.readIdentifier()
		default:
			return ""
		}
	}
	next := l.peekChar()
	if next == '"' || next == '[' || next == '`' || isLetter(next) || next == '*' {
		l.readChar() // eat '.'
		switch {
		case l.ch == '"':
			return "." + l.readQuotedIdentifier()
		case l.ch == '[':
			return "." + l.readBracketedIdentifier()
		case l.ch == '`':
			return "." + l.readBacktickIdentifier()
		case l.ch == '*':
			l.readChar() // eat '*'
			return ".*"
		default:
			return "." + l.readIdentifier()
		}
	}
	return ""
}

func (l *Lexer) readIdentifier() string {
	startPos := l.pos
	l.readBareIdentSegment()
	l.consumeRawDotChain()
	return l.input[startPos:l.pos]
}

func (l *Lexer) readBareIdentSegment() {
	for isLetter(l.ch) || isDigit(l.ch) || l.ch == '_' {
		l.readChar()
	}
}

// consumeRawDotChain advances the cursor across chained dot-qualifications
// (e.g. [dbo].[sp_name], db..table, dbo.orders, a.*) so callers can slice l.input[startPos:l.pos].
func (l *Lexer) consumeRawDotChain() {
	for l.ch == '.' {
		if l.peekChar() == '.' {
			l.readChar() // eat '.'
			l.readChar() // eat second '.'
			switch {
			case l.ch == '[':
				l.readBracketedSegment()
			case l.ch == '"':
				_ = l.readDelimitedIdentifier('"')
			case l.ch == '`':
				_ = l.readDelimitedIdentifier('`')
			case isLetter(l.ch):
				l.readBareIdentSegment()
			}
		} else if next := l.peekChar(); isLetter(next) || isDigit(next) || next == '_' || next == '[' || next == '"' || next == '`' || next == '*' {
			l.readChar() // eat '.'
			switch {
			case l.ch == '[':
				l.readBracketedSegment()
			case l.ch == '"':
				_ = l.readDelimitedIdentifier('"')
			case l.ch == '`':
				_ = l.readDelimitedIdentifier('`')
			case l.ch == '*':
				l.readChar() // eat '*'
			default:
				l.readBareIdentSegment()
			}
		} else {
			break
		}
	}
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
	if l.ch == '.' && isDigit(l.peekChar()) {
		isFloat = true
		l.readChar() // eat '.'
		for isDigit(l.ch) {
			l.readChar()
		}
	} else {
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
	}

	if l.ch == 'e' || l.ch == 'E' {
		pCh := l.peekChar()
		isExp := false
		if isDigit(pCh) {
			isExp = true
		} else if pCh == '+' || pCh == '-' {
			nextPos := l.readPos + 1
			if nextPos < len(l.input) && isDigit(rune(l.input[nextPos])) {
				isExp = true
			}
		}
		if isExp {
			isFloat = true
			l.readChar() // eat 'e' or 'E'
			if l.ch == '+' || l.ch == '-' {
				l.readChar() // eat '+' or '-'
			}
			for isDigit(l.ch) {
				l.readChar()
			}
		}
	}

	return l.input[startPos:l.pos], isFloat
}

func (l *Lexer) readString() string {
	l.readChar() // skip initial quote
	var b strings.Builder
	for l.ch != 0 {
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
	return unicode.IsLetter(ch) || ch == '_' || ch == '#' || ch == '$'
}

func isDigit(ch rune) bool {
	return '0' <= ch && ch <= '9'
}

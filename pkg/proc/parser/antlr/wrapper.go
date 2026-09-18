package parser

import (
	"fmt"
	"strings"

	"github.com/antlr4-go/antlr/v4"
)

// SyntaxError captures error diagnostics from ANTLR.
type SyntaxError struct {
	Line    int
	Column  int
	Message string
}

func (e SyntaxError) Error() string {
	return fmt.Sprintf("line %d:%d %s", e.Line, e.Column, e.Message)
}

// ErrorListener collects syntax errors during parsing.
type ErrorListener struct {
	*antlr.DefaultErrorListener
	Errors []SyntaxError
}

func NewErrorListener() *ErrorListener {
	return &ErrorListener{
		DefaultErrorListener: antlr.NewDefaultErrorListener(),
		Errors:               make([]SyntaxError, 0),
	}
}

func (l *ErrorListener) SyntaxError(recognizer antlr.Recognizer, offendingSymbol interface{}, line, column int, msg string, e antlr.RecognitionException) {
	l.Errors = append(l.Errors, SyntaxError{
		Line:    line,
		Column:  column,
		Message: msg,
	})
}

// ParseResult holds the output of an ANTLR T-SQL parse.
type ParseResult struct {
	Tree    ITsql_fileContext
	Errors  []SyntaxError
	Parser  *TSqlParser
	Lexer   *TSqlLexer
}

// ParseTSql parses a complete T-SQL script using the official full ANTLR4 grammar.
func ParseTSql(script string) (*ParseResult, error) {
	input := antlr.NewInputStream(script)
	lexer := NewTSqlLexer(input)
	lexerErrListener := NewErrorListener()
	lexer.RemoveErrorListeners()
	lexer.AddErrorListener(lexerErrListener)

	tokens := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)
	parser := NewTSqlParser(tokens)
	parserErrListener := NewErrorListener()
	parser.RemoveErrorListeners()
	parser.AddErrorListener(parserErrListener)

	tree := parser.Tsql_file()

	allErrors := append(lexerErrListener.Errors, parserErrListener.Errors...)
	if len(allErrors) > 0 {
		var errMsgs []string
		for _, err := range allErrors {
			errMsgs = append(errMsgs, err.Error())
		}
		return &ParseResult{
			Tree:   tree,
			Errors: allErrors,
			Parser: parser,
			Lexer:  lexer,
		}, fmt.Errorf("T-SQL syntax error: %s", strings.Join(errMsgs, "; "))
	}

	return &ParseResult{
		Tree:   tree,
		Errors: nil,
		Parser: parser,
		Lexer:  lexer,
	}, nil
}

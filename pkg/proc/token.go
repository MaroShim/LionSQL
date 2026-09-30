package proc

import "fmt"

// TokenType defines lexical token kinds.
type TokenType int

const (
	TokenEOF TokenType = iota
	TokenIllegal

	// Identifiers & Literals
	TokenIdent    // table_name, column_name
	TokenVariable // @var_name
	TokenInt      // 123
	TokenString   // 'hello'
	TokenFloat    // 12.34
	TokenBool     // TRUE, FALSE

	// Operators & Punctuation
	TokenAssign    // =
	TokenEq        // =
	TokenNeq       // != or <>
	TokenLt        // <
	TokenLte       // <=
	TokenGt        // >
	TokenGte       // >=
	TokenPlus      // +
	TokenMinus     // -
	TokenAsterisk  // *
	TokenSlash     // /
	TokenComma     // ,
	TokenSemicolon // ;
	TokenColon     // :
	TokenLParen    // (
	TokenRParen    // )
	TokenJSONExtract     // ->
	TokenJSONExtractText // ->>

	// Keywords
	TokenCreate
	TokenAlter
	TokenProcedure
	TokenProc
	TokenAs
	TokenBegin
	TokenEnd
	TokenDeclare
	TokenSet
	TokenSelect
	TokenInto
	TokenFrom
	TokenWhere
	TokenInsert
	TokenValues
	TokenUpdate
	TokenDelete
	TokenIf
	TokenElse
	TokenWhile
	TokenReturn
	TokenTransaction
	TokenTran
	TokenCommit
	TokenRollback
	TokenRaiserror
	TokenPrint
	TokenExec
	TokenAnd
	TokenOr
	TokenNot
	TokenTry
	TokenCatch
	TokenCase
	TokenWhen
	TokenThen
	TokenThrow
	TokenIsNull
	TokenCoalesce
	TokenNull
	TokenBreak
	TokenContinue
	TokenIn
	TokenBetween
	TokenLike
	TokenIs
	TokenCursor
	TokenOpen
	TokenFetch
	TokenNext
	TokenClose
	TokenDeallocate
	TokenFor
	TokenFunction
	TokenReturns
	TokenTrigger
	TokenOn
	TokenAfter
	TokenInstead
	TokenOf
	TokenWith
	TokenJoin
	TokenInner
	TokenLeft
	TokenRight
	TokenOuter
	TokenCross
	TokenGroup
	TokenBy
	TokenHaving
	TokenOrder
	TokenAsc
	TokenDesc
	TokenLimit
	TokenTable
	TokenIndex
	TokenUnique
	TokenDrop
	TokenExplain
	TokenAnalyze
	TokenExists
	TokenCount
	TokenSum
	TokenAvg
	TokenMin
	TokenMax
	TokenGoto
	TokenSave
	TokenSavepoint
	TokenDistinct
)

var keywords = map[string]TokenType{
	"DISTINCT":    TokenDistinct,
	"SAVE":        TokenSave,
	"SAVEPOINT":   TokenSavepoint,
	"GOTO":        TokenGoto,
	"CREATE":      TokenCreate,
	"ALTER":       TokenAlter,
	"PROCEDURE":   TokenProcedure,
	"PROC":        TokenProc,
	"FUNCTION":    TokenFunction,
	"RETURNS":     TokenReturns,
	"TRIGGER":     TokenTrigger,
	"ON":          TokenOn,
	"AFTER":       TokenAfter,
	"INSTEAD":     TokenInstead,
	"OF":          TokenOf,
	"AS":          TokenAs,
	"BEGIN":       TokenBegin,
	"END":         TokenEnd,
	"DECLARE":     TokenDeclare,
	"SET":         TokenSet,
	"SELECT":      TokenSelect,
	"INTO":        TokenInto,
	"FROM":        TokenFrom,
	"WHERE":       TokenWhere,
	"FOR":         TokenFor,
	"INSERT":      TokenInsert,
	"VALUES":      TokenValues,
	"UPDATE":      TokenUpdate,
	"DELETE":      TokenDelete,
	"IF":          TokenIf,
	"ELSE":        TokenElse,
	"WHILE":       TokenWhile,
	"RETURN":      TokenReturn,
	"TRANSACTION": TokenTransaction,
	"TRAN":        TokenTran,
	"COMMIT":      TokenCommit,
	"ROLLBACK":    TokenRollback,
	"RAISERROR":   TokenRaiserror,
	"PRINT":       TokenPrint,
	"EXEC":        TokenExec,
	"EXECUTE":     TokenExec,
	"AND":         TokenAnd,
	"OR":          TokenOr,
	"NOT":         TokenNot,
	"TRY":         TokenTry,
	"CATCH":       TokenCatch,
	"CASE":        TokenCase,
	"WHEN":        TokenWhen,
	"THEN":        TokenThen,
	"THROW":       TokenThrow,
	"ISNULL":      TokenIsNull,
	"COALESCE":    TokenCoalesce,
	"NULL":        TokenNull,
	"BREAK":       TokenBreak,
	"CONTINUE":    TokenContinue,
	"IN":          TokenIn,
	"BETWEEN":     TokenBetween,
	"LIKE":        TokenLike,
	"IS":          TokenIs,
	"CURSOR":      TokenCursor,
	"OPEN":        TokenOpen,
	"FETCH":       TokenFetch,
	"NEXT":        TokenNext,
	"CLOSE":       TokenClose,
	"DEALLOCATE":  TokenDeallocate,
	"TRUE":        TokenBool,
	"FALSE":       TokenBool,
	"WITH":        TokenWith,
	"JOIN":        TokenJoin,
	"INNER":       TokenInner,
	"LEFT":        TokenLeft,
	"RIGHT":       TokenRight,
	"OUTER":       TokenOuter,
	"CROSS":       TokenCross,
	"GROUP":       TokenGroup,
	"BY":          TokenBy,
	"HAVING":      TokenHaving,
	"ORDER":       TokenOrder,
	"ASC":         TokenAsc,
	"DESC":        TokenDesc,
	"LIMIT":       TokenLimit,
	"TABLE":       TokenTable,
	"INDEX":       TokenIndex,
	"UNIQUE":      TokenUnique,
	"DROP":        TokenDrop,
	"EXPLAIN":     TokenExplain,
	"ANALYZE":     TokenAnalyze,
	"EXISTS":      TokenExists,
	"COUNT":       TokenCount,
	"SUM":         TokenSum,
	"AVG":         TokenAvg,
	"MIN":         TokenMin,
	"MAX":         TokenMax,
}

func (t TokenType) String() string {
	switch t {
	case TokenAssign, TokenEq:
		return "="
	case TokenNeq:
		return "!="
	case TokenLt:
		return "<"
	case TokenLte:
		return "<="
	case TokenGt:
		return ">"
	case TokenGte:
		return ">="
	case TokenPlus:
		return "+"
	case TokenMinus:
		return "-"
	case TokenAsterisk:
		return "*"
	case TokenSlash:
		return "/"
	case TokenComma:
		return ","
	case TokenSemicolon:
		return ";"
	case TokenColon:
		return ":"
	case TokenLParen:
		return "("
	case TokenRParen:
		return ")"
	case TokenAnd:
		return "AND"
	case TokenOr:
		return "OR"
	case TokenNot:
		return "NOT"
	case TokenIn:
		return "IN"
	case TokenBetween:
		return "BETWEEN"
	case TokenLike:
		return "LIKE"
	case TokenIs:
		return "IS"
	case TokenJSONExtract:
		return "->"
	case TokenJSONExtractText:
		return "->>"
	default:
		for k, v := range keywords {
			if v == t {
				return k
			}
		}
		return fmt.Sprintf("Token(%d)", int(t))
	}
}

// Token represents a lexical token.
type Token struct {
	Type    TokenType
	Literal string
	Line    int
	Col     int
}

func (t Token) String() string {
	return fmt.Sprintf("[%v: %q at %d:%d]", t.Type, t.Literal, t.Line, t.Col)
}


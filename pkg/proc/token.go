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
	TokenDoubleColon     // ::
	TokenConcat          // ||
	TokenBitwiseOr       // |
	TokenBitwiseAnd      // &
	TokenBitwiseXor      // ^
	TokenBitwiseNot      // ~

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
	TokenBefore
	TokenAfter
	TokenInstead
	TokenOf
	TokenWith
	TokenJoin
	TokenInner
	TokenLeft
	TokenRight
	TokenFull
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
	TokenTruncate
	TokenCascade
	TokenRestrict
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
	TokenOutput
	TokenRollup
	TokenCube
	TokenGroupingSets
	TokenOffset
	TokenRow
	TokenRows
	TokenFirst
	TokenOnly
	TokenTop
	TokenPercent
	TokenTies
	TokenCast
	TokenTryCast
	TokenConvert
	TokenTryConvert
	TokenApply
	TokenOption
	TokenNolock
	TokenReturning
	TokenConflict
	TokenDiv
	TokenPlusAssign      // +=
	TokenMinusAssign     // -=
	TokenAsteriskAssign  // *=
	TokenSlashAssign     // /=
	TokenModuloAssign    // %=
	TokenBitAndAssign    // &=
	TokenBitXorAssign    // ^=
	TokenBitOrAssign     // |=
	TokenWaitfor         // WAITFOR
	TokenDelay           // DELAY
	TokenTime            // TIME
)

var keywords = map[string]TokenType{
	"WAITFOR":       TokenWaitfor,
	"DELAY":         TokenDelay,
	"TIME":          TokenTime,
	"RETURNING":     TokenReturning,
	"CONFLICT":      TokenConflict,
	"CAST":          TokenCast,
	"TRY_CAST":      TokenTryCast,
	"CONVERT":       TokenConvert,
	"TRY_CONVERT":   TokenTryConvert,
	"OFFSET":        TokenOffset,
	"ROW":           TokenRow,
	"ROWS":          TokenRows,
	"FIRST":         TokenFirst,
	"ONLY":          TokenOnly,
	"TOP":           TokenTop,
	"PERCENT":       TokenPercent,
	"TIES":          TokenTies,
	"ROLLUP":        TokenRollup,
	"CUBE":          TokenCube,
	"GROUPING_SETS": TokenGroupingSets,
	"OUTPUT":        TokenOutput,
	"DISTINCT":      TokenDistinct,
	"SAVE":          TokenSave,
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
	"BEFORE":      TokenBefore,
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
	"FULL":        TokenFull,
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
	"TRUNCATE":    TokenTruncate,
	"CASCADE":     TokenCascade,
	"RESTRICT":    TokenRestrict,
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
	"APPLY":       TokenApply,
	"OPTION":      TokenOption,
	"NOLOCK":      TokenNolock,
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
	case TokenDiv:
		return "DIV"
	case TokenPercent:
		return "%"
	case TokenConcat:
		return "||"
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
	case TokenDoubleColon:
		return "::"
	case TokenBitwiseOr:
		return "|"
	case TokenBitwiseAnd:
		return "&"
	case TokenBitwiseXor:
		return "^"
	case TokenBitwiseNot:
		return "~"
	case TokenPlusAssign:
		return "+="
	case TokenMinusAssign:
		return "-="
	case TokenAsteriskAssign:
		return "*="
	case TokenSlashAssign:
		return "/="
	case TokenModuloAssign:
		return "%="
	case TokenBitAndAssign:
		return "&="
	case TokenBitXorAssign:
		return "^="
	case TokenBitOrAssign:
		return "|="
	default:
		for k, v := range keywords {
			if v == t {
				return k
			}
		}
		return fmt.Sprintf("Token(%d)", int(t))
	}
}

// IsCompoundAssign reports whether tok is a T-SQL compound assignment operator
// (+=, -=, *=, /=, %=, &=, ^=, |=), and returns the corresponding binary operator token.
func IsCompoundAssign(tok TokenType) (TokenType, bool) {
	switch tok {
	case TokenPlusAssign:
		return TokenPlus, true
	case TokenMinusAssign:
		return TokenMinus, true
	case TokenAsteriskAssign:
		return TokenAsterisk, true
	case TokenSlashAssign:
		return TokenSlash, true
	case TokenModuloAssign:
		return TokenPercent, true
	case TokenBitAndAssign:
		return TokenBitwiseAnd, true
	case TokenBitXorAssign:
		return TokenBitwiseXor, true
	case TokenBitOrAssign:
		return TokenBitwiseOr, true
	default:
		return TokenIllegal, false
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


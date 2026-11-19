package proc

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"lionsql/pkg/storage"
)

// ValidateBuiltinArity validates the argument count (arity) for built-in scalar, aggregate, window, and system functions.
func ValidateBuiltinArity(name string, argc int) error {
	upper := strings.ToUpper(cleanBuiltinName(name))
	if _, ok := customScalarFunctions.Load(upper); ok {
		return nil
	}
	lower := strings.ToLower(upper)

	switch upper {
	// 0 arguments
	case "GETDATE", "SYSDATETIME", "SYSUTCDATETIME", "SYSDATETIMEOFFSET", "GETUTCDATE",
		"STATEMENT_TIMESTAMP", "CLOCK_TIMESTAMP", "TIMEOFDAY", "CURDATE", "CURTIME",
		"NEWID", "NEWID7", "UUIDV7", "UUID_GENERATE_V7", "NEWSEQUENTIALID",
		"PI", "SCOPE_IDENTITY", "HOST_NAME", "HOST_ID", "APP_NAME",
		"ERROR_NUMBER", "ERROR_MESSAGE", "ERROR_LINE", "ERROR_SEVERITY", "ERROR_STATE", "ERROR_PROCEDURE",
		"XACT_STATE", "CONTEXT_INFO", "ORIGINAL_LOGIN", "SYSTEM_USER",
		"ROW_NUMBER", "RANK", "DENSE_RANK", "PERCENT_RANK", "CUME_DIST":
		if argc != 0 {
			return fmt.Errorf("The %s function requires 0 argument(s).", lower)
		}

	// 0 to 1 arguments
	case "RAND", "RANDOM", "GEN_RANDOM_UUID", "NOW", "CURRENT_TIMESTAMP", "CURRENT_DATE", "CURRENT_TIME",
		"DB_NAME", "DB_ID", "SCHEMA_NAME", "SCHEMA_ID", "SUSER_NAME", "SUSER_SNAME", "USER_NAME", "USER_ID", "SUSER_ID",
		"CURRENT_SCHEMA", "CURRENT_SCHEMAS", "CURRENT_DATABASE", "PERMISSIONS":
		if argc > 1 {
			return fmt.Errorf("The %s function requires 0 to 1 arguments.", lower)
		}

	// 1 argument
	case "ASCII", "UNICODE", "CHAR", "NCHAR", "CHR", "UPPER", "LOWER", "INITCAP",
		"LEN", "LENGTH", "CHAR_LENGTH", "CHARACTER_LENGTH", "DATALENGTH", "OCTET_LENGTH", "BIT_LENGTH",
		"REVERSE", "SPACE", "SOUNDEX", "ISNUMERIC", "ISDATE",
		"ABS", "SIGN", "SQRT", "CBRT", "SQUARE", "EXP", "LOG10", "LN",
		"SIN", "COS", "TAN", "COT", "ASIN", "ACOS", "ATAN", "DEGREES", "RADIANS",
		"FLOOR", "CEILING", "CEIL", "BIT_COUNT",
		"YEAR", "MONTH", "DAY",
		"MD5", "SHA256", "HEX", "UNHEX", "COMPRESS", "DECOMPRESS", "QUOTE_IDENT", "QUOTE_LITERAL",
		"IDENT_CURRENT", "IDENT_SEED", "IDENT_INCR",
		"SERVERPROPERTY", "SESSIONPROPERTY", "CONNECTIONPROPERTY", "SESSION_CONTEXT",
		"TYPE_ID", "TYPE_NAME", "HAS_DBACCESS", "PWDENCRYPT", "NEXTVAL",
		"NTILE", "FIRST_VALUE", "LAST_VALUE",
		"COUNT", "COUNT_BIG", "SUM", "TOTAL", "AVG", "MIN", "MAX",
		"STDEV", "STDEVP", "STDDEV", "STDDEV_POP", "STDDEV_SAMP",
		"VAR", "VARP", "VAR_POP", "VAR_SAMP", "VARIANCE",
		"CHECKSUM_AGG", "BOOL_AND", "BOOL_OR", "EVERY", "ARRAY_AGG", "JSON_AGG", "JSONB_AGG", "GROUPING", "MEDIAN":
		if argc != 1 {
			return fmt.Errorf("The %s function requires 1 argument(s).", lower)
		}

	// 1 to 2 arguments
	case "LTRIM", "RTRIM", "TRIM", "BTRIM", "EOMONTH", "LOG", "QUOTENAME", "STRING_ESCAPE",
		"ISJSON", "JSON_QUERY", "TRUNC", "TRUNCATE",
		"OBJECT_ID", "OBJECT_NAME", "OBJECT_SCHEMA_NAME", "IS_SRVROLEMEMBER", "IS_MEMBER", "CURRENT_SETTING",
		"TO_DATE", "TO_TIMESTAMP", "GROUP_CONCAT":
		if argc < 1 || argc > 2 {
			return fmt.Errorf("The %s function requires 1 to 2 arguments.", lower)
		}

	// 1 to 3 arguments
	case "STR", "LEAD", "LAG":
		if argc < 1 || argc > 3 {
			return fmt.Errorf("The %s function requires 1 to 3 arguments.", lower)
		}

	// 2 arguments
	case "POWER", "POW", "ATN2", "ATAN2", "MOD", "LEFT_SHIFT", "RIGHT_SHIFT", "GET_BIT",
		"LEFT", "RIGHT", "REPLICATE", "REPEAT", "DIFFERENCE", "PARSENAME", "PATINDEX", "STRPOS", "INSTR", "TO_CHAR",
		"GREP", "REGEXP_LIKE",
		"NULLIF", "ISNULL", "NVL", "IFNULL",
		"DATEPART", "DATE_PART", "EXTRACT", "DATENAME", "DATETRUNC",
		"TODATETIMEOFFSET", "SWITCHOFFSET",
		"JSON_VALUE", "JSON_PATH_EXISTS",
		"HASHBYTES", "COL_LENGTH", "COL_NAME", "DATABASEPROPERTYEX", "OBJECTPROPERTY", "COLLATIONPROPERTY",
		"FILEPROPERTY", "FILEGROUPPROPERTY", "FULLTEXTSERVICEPROPERTY",
		"STRING_AGG", "NTH_VALUE", "APPROX_PERCENTILE":
		if argc != 2 {
			return fmt.Errorf("The %s function requires 2 argument(s).", lower)
		}

	// 2 to 3 arguments
	case "ROUND", "CHARINDEX", "SUBSTRING", "SUBSTR", "LPAD", "RPAD", "FORMAT",
		"DATE_TRUNC", "SET_BIT", "SET_CONFIG", "SETVAL", "PWDCOMPARE", "AWK", "STRING_SPLIT":
		if argc < 2 || argc > 3 {
			return fmt.Errorf("The %s function requires 2 to 3 arguments.", lower)
		}

	// 3 arguments
	case "REPLACE", "TRANSLATE", "SPLIT_PART", "DATEADD", "DATEDIFF", "DATEDIFF_BIG",
		"DATEFROMPARTS", "IIF", "JSON_MODIFY", "COLUMNPROPERTY", "INDEXPROPERTY":
		if argc != 3 {
			return fmt.Errorf("The %s function requires 3 argument(s).", lower)
		}

	// 3 to 4 arguments
	case "DATE_BUCKET", "DATEBUCKET":
		if argc < 3 || argc > 4 {
			return fmt.Errorf("The %s function requires 3 to 4 arguments.", lower)
		}

	// 4 arguments
	case "STUFF":
		if argc != 4 {
			return fmt.Errorf("The %s function requires 4 argument(s).", lower)
		}

	// 5 arguments
	case "TIMEFROMPARTS", "SMALLDATETIMEFROMPARTS":
		if argc != 5 {
			return fmt.Errorf("The %s function requires 5 argument(s).", lower)
		}

	// 7 arguments
	case "DATETIMEFROMPARTS":
		if argc != 7 {
			return fmt.Errorf("The %s function requires 7 argument(s).", lower)
		}

	// 8 arguments
	case "DATETIME2FROMPARTS":
		if argc != 8 {
			return fmt.Errorf("The %s function requires 8 argument(s).", lower)
		}

	// 10 arguments
	case "DATETIMEOFFSETFROMPARTS":
		if argc != 10 {
			return fmt.Errorf("The %s function requires 10 argument(s).", lower)
		}

	// 1 or more arguments
	case "COALESCE", "GREATEST", "LEAST", "CONCAT", "FORMATMESSAGE", "CHECKSUM", "BINARY_CHECKSUM":
		if argc < 1 {
			return fmt.Errorf("The %s function requires at least 1 argument(s).", lower)
		}

	// 2 or more arguments
	case "CHOOSE", "CONCAT_WS":
		if argc < 2 {
			return fmt.Errorf("The %s function requires at least 2 argument(s).", lower)
		}
	}
	return nil
}

// ScalarFunction represents an in-process native Go scalar function callback.
type ScalarFunction func(args []storage.Value) storage.Value

// customScalarFunctions stores user-registered in-process Go scalar UDFs.
var customScalarFunctions sync.Map

// RegisterScalarFunction registers a native Go scalar function into the runtime UDF catalog.
func RegisterScalarFunction(name string, fn ScalarFunction) {
	upper := strings.ToUpper(cleanBuiltinName(name))
	customScalarFunctions.Store(upper, fn)
}

// UnregisterScalarFunction removes a user-registered native Go scalar function from the catalog.
func UnregisterScalarFunction(name string) {
	upper := strings.ToUpper(cleanBuiltinName(name))
	customScalarFunctions.Delete(upper)
}

// HasScalarFunction checks whether a user-registered native Go scalar function exists.
func HasScalarFunction(name string) bool {
	upper := strings.ToUpper(cleanBuiltinName(name))
	_, ok := customScalarFunctions.Load(upper)
	return ok
}

// EvalBuiltinWithErr evaluates standard T-SQL scalar functions and returns an error on domain or parameter errors.
func EvalBuiltinWithErr(name string, args []storage.Value) (storage.Value, error) {
	upper := strings.ToUpper(cleanBuiltinName(name))

	// 1. Fast-path lookup for registered native Go scalar functions
	if val, ok := customScalarFunctions.Load(upper); ok {
		if fn, isFn := val.(ScalarFunction); isFn && fn != nil {
			return fn(args), nil
		}
	}

	if err := ValidateBuiltinArity(upper, len(args)); err != nil {
		return storage.NullValue(storage.TypeUnknown), err
	}

	if isDateBuiltin(upper) {
		return evalDateBuiltinWithErr(upper, args)
	}
	if isStringBuiltin(upper) {
		return evalStringBuiltinWithErr(upper, args)
	}
	if isMathBuiltin(upper) {
		return evalMathBuiltinWithErr(upper, args)
	}
	if isJSONBuiltin(upper) {
		return evalJSONBuiltin(upper, args), nil
	}
	if isSysBuiltin(upper) {
		return evalSysBuiltinWithErr(upper, args)
	}

	if len(args) > 0 {
		return args[0], nil
	}
	return storage.NullValue(storage.TypeUnknown), nil
}

// EvalBuiltin evaluates standard T-SQL scalar functions and registered native Go scalar UDFs.
func EvalBuiltin(name string, args []storage.Value) storage.Value {
	v, _ := EvalBuiltinWithErr(name, args)
	return v
}

// IsBuiltinFunction reports whether the named function is a built-in scalar function.
func IsBuiltinFunction(name string) bool {
	upper := strings.ToUpper(cleanBuiltinName(name))
	if _, ok := customScalarFunctions.Load(upper); ok {
		return true
	}
	return isDateBuiltin(upper) ||
		isStringBuiltin(upper) ||
		isMathBuiltin(upper) ||
		isJSONBuiltin(upper) ||
		isSysBuiltin(upper)
}

func cleanBuiltinName(name string) string {
	n := strings.TrimSpace(name)
	if dot := strings.LastIndex(n, "."); dot != -1 {
		n = n[dot+1:]
	}
	return strings.Trim(n, "[]\"`")
}

func valToString(v storage.Value) string {
	if v.IsNull {
		return ""
	}
	switch v.Type {
	case storage.TypeVarChar, storage.TypeJSON:
		return v.SVal
	case storage.TypeInt:
		return strconv.FormatInt(v.IVal, 10)
	case storage.TypeReal:
		return strconv.FormatFloat(v.FVal, 'f', -1, 32)
	case storage.TypeFloat:
		return strconv.FormatFloat(v.FVal, 'f', -1, 64)
	case storage.TypeBool:
		if v.BVal {
			return "1"
		}
		return "0"
	case storage.TypeDateTime:
		return v.TVal.Format("2006-01-02 15:04:05")
	case storage.TypeDecimal:
		if v.DVal.Scale() == 4 {
			// In SQL Server, MONEY/SMALLMONEY converted to character in string builtins defaults to 2 decimal places (Style 0)
			return v.DVal.Rescale(2).String()
		}
		return v.String()
	default:
		return v.String()
	}
}

func toFloatNum(v storage.Value) (float64, bool) {
	if v.IsNull {
		return 0, false
	}
	switch v.Type {
	case storage.TypeInt:
		return float64(v.IVal), true
	case storage.TypeFloat, storage.TypeReal:
		return v.FVal, true
	case storage.TypeDecimal:
		return v.Decimal().Float64(), true
	case storage.TypeVarChar:
		f, err := strconv.ParseFloat(strings.TrimSpace(v.SVal), 64)
		return f, err == nil
	case storage.TypeBool:
		if v.BVal {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

func toIntNum(v storage.Value) (int64, bool) {
	if v.IsNull {
		return 0, false
	}
	switch v.Type {
	case storage.TypeInt:
		return v.IVal, true
	case storage.TypeFloat, storage.TypeReal:
		return int64(v.FVal), true
	case storage.TypeDecimal:
		return v.Decimal().Int64(), true
	case storage.TypeVarChar:
		i, err := strconv.ParseInt(strings.TrimSpace(v.SVal), 10, 64)
		return i, err == nil
	case storage.TypeBool:
		if v.BVal {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

func isTruthyStorageValue(v storage.Value) bool {
	if v.IsNull {
		return false
	}
	switch v.Type {
	case storage.TypeBool:
		return v.BVal
	case storage.TypeInt:
		return v.IVal != 0
	case storage.TypeFloat, storage.TypeReal:
		return v.FVal != 0
	case storage.TypeVarChar:
		s := strings.TrimSpace(v.SVal)
		return len(s) > 0 && !strings.EqualFold(s, "false") && s != "0"
	default:
		return true
	}
}


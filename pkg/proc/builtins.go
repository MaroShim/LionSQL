package proc

import (
	"strconv"
	"strings"
	"sync"

	"sqlion/pkg/storage"
)

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

// EvalBuiltin evaluates standard T-SQL scalar functions and registered native Go scalar UDFs.
func EvalBuiltin(name string, args []storage.Value) storage.Value {
	upper := strings.ToUpper(cleanBuiltinName(name))

	// 1. Fast-path lookup for registered native Go scalar functions
	if val, ok := customScalarFunctions.Load(upper); ok {
		if fn, isFn := val.(ScalarFunction); isFn && fn != nil {
			return fn(args)
		}
	}

	if isDateBuiltin(upper) {
		return evalDateBuiltin(upper, args)
	}
	if isStringBuiltin(upper) {
		return evalStringBuiltin(upper, args)
	}
	if isMathBuiltin(upper) {
		return evalMathBuiltin(upper, args)
	}
	if isJSONBuiltin(upper) {
		return evalJSONBuiltin(upper, args)
	}
	if isSysBuiltin(upper) {
		return evalSysBuiltin(upper, args)
	}

	if len(args) > 0 {
		return args[0]
	}
	return storage.NullValue(storage.TypeUnknown)
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
	case storage.TypeFloat:
		return strconv.FormatFloat(v.FVal, 'f', -1, 64)
	case storage.TypeBool:
		if v.BVal {
			return "1"
		}
		return "0"
	case storage.TypeDateTime:
		return v.TVal.Format("2006-01-02 15:04:05")
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
	case storage.TypeFloat:
		return v.FVal, true
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

func isTruthyStorageValue(v storage.Value) bool {
	if v.IsNull {
		return false
	}
	switch v.Type {
	case storage.TypeBool:
		return v.BVal
	case storage.TypeInt:
		return v.IVal != 0
	case storage.TypeFloat:
		return v.FVal != 0
	case storage.TypeVarChar:
		s := strings.TrimSpace(v.SVal)
		return len(s) > 0 && !strings.EqualFold(s, "false") && s != "0"
	default:
		return true
	}
}


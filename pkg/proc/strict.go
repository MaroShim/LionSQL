package proc

import (
	"lionsql/pkg/storage"
)

// HasAnyNull returns true if any of the provided values is NULL.
func HasAnyNull(args []storage.Value) bool {
	for i := range args {
		if args[i].IsNull {
			return true
		}
	}
	return false
}

// HasNullAt returns true if the argument at index idx is missing or NULL.
func HasNullAt(args []storage.Value, idx int) bool {
	if idx < 0 || idx >= len(args) {
		return true
	}
	return args[idx].IsNull
}

// Strict returns a decorated ScalarFunction that immediately returns a NULL value
// of targetType if any argument is NULL.
func Strict(targetType storage.DataType, fn ScalarFunction) ScalarFunction {
	return func(args []storage.Value) storage.Value {
		if len(args) == 0 || HasAnyNull(args) {
			return storage.NullValue(targetType)
		}
		return fn(args)
	}
}

// StrictUnary returns a decorated function for a 1-argument strict scalar operation.
func StrictUnary(targetType storage.DataType, fn func(a storage.Value) storage.Value) ScalarFunction {
	return func(args []storage.Value) storage.Value {
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(targetType)
		}
		return fn(args[0])
	}
}

// StrictBinary returns a decorated function for a 2-argument strict scalar operation.
func StrictBinary(targetType storage.DataType, fn func(a, b storage.Value) storage.Value) ScalarFunction {
	return func(args []storage.Value) storage.Value {
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(targetType)
		}
		return fn(args[0], args[1])
	}
}

// RegisterStrictScalarFunction registers a native Go scalar function that adheres to
// standard SQL strict NULL propagation (returns NULL if any argument is NULL).
func RegisterStrictScalarFunction(name string, targetType storage.DataType, fn ScalarFunction) {
	RegisterScalarFunction(name, Strict(targetType, fn))
}

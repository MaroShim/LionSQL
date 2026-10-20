package proc

import (
	"lionsql/pkg/storage"
)

// ParseLiteralDefault parses a literal SQL expression or default value string into a storage.Value.
// It delegates to storage.ParseLiteralDefault.
func ParseLiteralDefault(s string, targetType storage.DataType) storage.Value {
	return storage.ParseLiteralDefault(s, targetType)
}

package sqlutil

import "strings"

// sqlDataTypes contains standard SQL and T-SQL data type names.
var sqlDataTypes = map[string]struct{}{
	"BIGINT":           {},
	"INT":              {},
	"INTEGER":          {},
	"SMALLINT":         {},
	"TINYINT":          {},
	"BIT":              {},
	"BOOLEAN":          {},
	"BOOL":             {},
	"DECIMAL":          {},
	"NUMERIC":          {},
	"MONEY":            {},
	"SMALLMONEY":       {},
	"FLOAT":            {},
	"REAL":             {},
	"DOUBLE":           {},
	"PRECISION":        {},
	"DATE":             {},
	"DATETIME":         {},
	"DATETIME2":        {},
	"DATETIMEOFFSET":   {},
	"SMALLDATETIME":    {},
	"TIME":             {},
	"TIMESTAMP":        {},
	"CHAR":             {},
	"CHARACTER":        {},
	"VARYING":          {},
	"VARCHAR":          {},
	"TEXT":             {},
	"NCHAR":            {},
	"NVARCHAR":         {},
	"NTEXT":            {},
	"BINARY":           {},
	"VARBINARY":        {},
	"IMAGE":            {},
	"UUID":             {},
	"UNIQUEIDENTIFIER": {},
	"JSON":             {},
	"JSONB":            {},
	"XML":              {},
	"BYTEA":            {},
	"SQL_VARIANT":      {},
	"SYSNAME":          {},
	"ROWVERSION":       {},
	"HIERARCHYID":      {},
	"GEOGRAPHY":        {},
	"GEOMETRY":         {},
}

// IsSQLTypeName reports whether s is a standard SQL / T-SQL data type name.
func IsSQLTypeName(s string) bool {
	clean := strings.ToUpper(strings.TrimSpace(s))
	_, ok := sqlDataTypes[clean]
	return ok
}

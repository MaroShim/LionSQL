package virtual

import (
	"strings"
)

// IsVirtualTable reports whether tableName identifies any registered virtual catalog table or view,
// including pg_catalog.*, information_schema.*, and sys.* views.
func IsVirtualTable(tableName string) bool {
	norm := strings.ToLower(strings.TrimSpace(tableName))
	norm = strings.Trim(norm, "[]\"'`()")
	norm = strings.TrimPrefix(norm, "::")
	if strings.Contains(norm, "(") {
		norm = norm[:strings.Index(norm, "(")]
		norm = strings.TrimSpace(norm)
	}
	if strings.Contains(norm, ".") {
		parts := strings.Split(norm, ".")
		if len(parts) == 3 {
			// strip database prefix, e.g. "lionsql.sys.tables" -> "sys.tables"
			norm = parts[1] + "." + parts[2]
		}
	}
	norm = strings.TrimPrefix(norm, "dbo.")

	if IsPgCatalogTable(norm) {
		return true
	}
	return IsSysCatalogTable(norm)
}

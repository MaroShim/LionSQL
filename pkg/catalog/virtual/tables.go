package virtual

import (
	"strings"

	"lionsql/pkg/sqlutil"
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

// parseVirtualSelectClauses extracts the projection expressions and WHERE clause
// from a virtual catalog SELECT query, respecting quote boundaries and comments.
func parseVirtualSelectClauses(sql string) (projStr string, whereClause string) {
	projStr = "*"
	if fromIdx := sqlutil.FindTopLevelKeyword(sql, "FROM"); fromIdx != -1 {
		selectPart := sql[:fromIdx]
		if selectIdx := sqlutil.FindTopLevelKeyword(selectPart, "SELECT"); selectIdx != -1 {
			projStr = strings.TrimSpace(selectPart[selectIdx+6:])
		}
	}
	if whereIdx := sqlutil.FindTopLevelKeyword(sql, "WHERE"); whereIdx != -1 {
		whereClause = strings.TrimSpace(sql[whereIdx+5:])
		if orderIdx := sqlutil.FindTopLevelKeyword(whereClause, "ORDER BY"); orderIdx != -1 {
			whereClause = strings.TrimSpace(whereClause[:orderIdx])
		}
	}
	return projStr, whereClause
}

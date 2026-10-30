package virtual

import (
	"strings"
)

// containsFold returns true if s contains substr (case-insensitive for ASCII).
// It does not allocate memory on the heap.
func containsFold(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	firstLower := substr[0]
	firstUpper := substr[0]
	if firstLower >= 'a' && firstLower <= 'z' {
		firstUpper = firstLower - ('a' - 'A')
	} else if firstUpper >= 'A' && firstUpper <= 'Z' {
		firstLower = firstUpper + ('a' - 'A')
	}

	limit := len(s) - len(substr)
	for i := 0; i <= limit; i++ {
		c := s[i]
		if c == firstLower || c == firstUpper {
			match := true
			for j := 1; j < len(substr); j++ {
				sc := s[i+j]
				tc := substr[j]
				if sc == tc {
					continue
				}
				if sc >= 'a' && sc <= 'z' {
					sc -= 'a' - 'A'
				}
				if tc >= 'a' && tc <= 'z' {
					tc -= 'a' - 'A'
				}
				if sc != tc {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

// hasCatalogCandidate quickly checks if sql contains any catalog prefix ("sys.", "pg_", "information_schema.")
// case-insensitively without heap allocations.
func hasCatalogCandidate(sql string) bool {
	return containsFold(sql, "sys.") || containsFold(sql, "[sys].") ||
		containsFold(sql, "pg_") || containsFold(sql, "[pg_") ||
		containsFold(sql, "information_schema.") || containsFold(sql, "[information_schema].")
}

// matchAny returns true if s contains any of the provided substrings.
func matchAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// containsEq returns true if s contains left = right, with arbitrary whitespace around '='.
// Assumes s, left, right are uppercase.
func containsEq(s, left, right string) bool {
	pos := 0
	for {
		idx := strings.Index(s[pos:], left)
		if idx < 0 {
			return false
		}
		// Word boundary check: left must not be a suffix of another identifier unless dot-prefixed
		absIdx := pos + idx
		if absIdx > 0 && !strings.HasPrefix(left, ".") {
			prev := s[absIdx-1]
			if (prev >= 'A' && prev <= 'Z') || (prev >= '0' && prev <= '9') || prev == '_' {
				pos = absIdx + 1
				continue
			}
		}

		curr := absIdx + len(left)
		for curr < len(s) && (s[curr] == ' ' || s[curr] == '\t' || s[curr] == '\r' || s[curr] == '\n') {
			curr++
		}
		if curr < len(s) && s[curr] == '=' {
			curr++
			for curr < len(s) && (s[curr] == ' ' || s[curr] == '\t' || s[curr] == '\r' || s[curr] == '\n') {
				curr++
			}
			if strings.HasPrefix(s[curr:], right) {
				return true
			}
		}
		pos = absIdx + 1
	}
}

// IsVirtualCatalogJoinQuery returns true if the SQL query matches a known virtual catalog cross-join query pattern.
func IsVirtualCatalogJoinQuery(sql string) bool {
	// Zero-alloc fast-path: reject non-catalog queries before string allocation.
	if !hasCatalogCandidate(sql) {
		return false
	}

	upper := strings.ToUpper(sql)
	upper = strings.ReplaceAll(strings.ReplaceAll(upper, "[", ""), "]", "")

	if strings.Contains(upper, "SYS.COLUMNS") && matchAny(upper, "JOIN", "SYS.TYPES") {
		return true
	}
	if strings.Contains(upper, "SYS.INDEXES") && matchAny(upper, "JOIN", "IS_PRIMARY_KEY") {
		return true
	}
	if matchAny(upper, "SYS.INDEX_COLUMNS", "SYS.ALL_OBJECTS", "SYS.SYSCOMMENTS", "SYS.SQL_MODULES", "FN_HELPCOLLATIONS") {
		return true
	}
	if strings.Contains(upper, "INFORMATION_SCHEMA.REFERENTIAL_CONSTRAINTS") && matchAny(upper, "JOIN", "SCHEMANAME") {
		return true
	}
	if strings.Contains(upper, "SYS.PARAMETERS") && strings.Contains(upper, "JOIN") {
		return true
	}
	if strings.Contains(upper, "SYS.TABLES") && matchAny(upper, "JOIN", "TABLEROWCOUNT", "OBJECTCOMMENT", "SYS.SCHEMAS") {
		return true
	}
	if strings.Contains(upper, "SYS.OBJECTS") && (matchAny(upper, "TRIGGERNAME", "JOIN", "SYS.SCHEMAS", "CODETEXT") || containsEq(upper, "TYPE", "'TR'")) {
		return true
	}
	if strings.Contains(upper, "INFORMATION_SCHEMA.COLUMNS") && strings.Contains(upper, "SYS.COMPUTED_COLUMNS") {
		return true
	}
	if strings.Contains(upper, "INFORMATION_SCHEMA.TABLES") && strings.Contains(upper, "PG_CLASS") {
		return true
	}
	if strings.Contains(upper, "PG_CONSTRAINT") && matchAny(upper, "JOIN", "UNNEST", "SRC_TBL", "CONKEY") {
		return true
	}
	if strings.Contains(upper, "PG_INDEX") && matchAny(upper, "JOIN", "PG_NAMESPACE", "INDEX_NAME", "INDKEY") {
		return true
	}
	if strings.Contains(upper, "PG_TRIGGER") && strings.Contains(upper, "JOIN") {
		return true
	}
	if strings.Contains(upper, "PG_CLASS") && matchAny(upper, "PG_NAMESPACE", "VIEW_DEFS", "RELNAMESPACE") {
		return true
	}
	if strings.Contains(upper, "PG_PROC") && strings.Contains(upper, "PG_NAMESPACE") {
		return true
	}
	if strings.Contains(upper, "PG_ATTRIBUTE") && matchAny(upper, "PG_CLASS", "JOIN") {
		return true
	}
	if matchAny(upper, "PG_SHDESCRIPTION", "PG_CONVERSION") {
		return true
	}
	return false
}

// Execute routes and executes an intercepted virtual catalog join query against the provided Source.
func Execute(src Source, sql string) (*Result, error) {
	upper := strings.ToUpper(sql)
	upper = strings.ReplaceAll(strings.ReplaceAll(upper, "[", ""), "]", "")

	fromColumns := strings.Contains(upper, "FROM SYS.COLUMNS") || strings.Contains(upper, "FROM SYS.ALL_COLUMNS")

	// 1. SQL Server Primary Keys
	if !fromColumns && strings.Contains(upper, "SYS.INDEXES") && containsEq(upper, "IS_PRIMARY_KEY", "1") {
		return execSysIndexesPrimaryKey(src, sql)
	}

	// 2. SQL Server Secondary Indexes
	if !fromColumns && strings.Contains(upper, "SYS.INDEXES") && containsEq(upper, "IS_PRIMARY_KEY", "0") {
		return execSysIndexesSecondary(src, sql)
	}

	// 2.5. SQL Server XORM GetIndexes (sys.indexes + sys.index_columns + sys.columns)
	if !fromColumns && strings.Contains(upper, "SYS.INDEXES") && strings.Contains(upper, "SYS.INDEX_COLUMNS") && strings.Contains(upper, "SYS.COLUMNS") {
		return execXORMIndexes(src, sql)
	}

	// 3. SQL Server Index Columns
	if !fromColumns && strings.Contains(upper, "SYS.INDEX_COLUMNS") {
		return execSysIndexColumns(src, sql)
	}

	// 4. SQL Server Columns joined with types
	if strings.Contains(upper, "SYS.COLUMNS") && (strings.Contains(upper, "SYS.TYPES") || strings.Contains(upper, "ISIDENTITY") || strings.Contains(upper, "IS_IDENTITY") || strings.Contains(upper, "DATATYPE") || strings.Contains(upper, "MAX_LENGTH")) {
		return execSysColumnsJoinedWithTypes(src, sql)
	}

	// 5. SQL Server Column comments (MS_Description)
	if strings.Contains(upper, "COLUMNCOMMENT") && strings.Contains(upper, "SYS.COLUMNS") {
		return execSysColumnsExtendedPropertyComment(sql)
	}

	// 6. SQL Server Information Schema columns with computed columns
	if strings.Contains(upper, "INFORMATION_SCHEMA.COLUMNS") && strings.Contains(upper, "SYS.COMPUTED_COLUMNS") {
		return execInformationSchemaColumnsWithComputed(src, sql)
	}

	// 7. SQL Server Referential Constraints (Foreign Keys)
	if strings.Contains(upper, "INFORMATION_SCHEMA.REFERENTIAL_CONSTRAINTS") {
		return execReferentialConstraintsForeignKey(src, sql)
	}

	// 8. SQL Server Table row count & size
	if strings.Contains(upper, "TABLEROWCOUNT") || (strings.Contains(upper, "SYS.TABLES") && strings.Contains(upper, "SYS.PARTITIONS")) {
		return execSysTablesRowCount(src, sql)
	}

	// 9. SQL Server Parameters
	if strings.Contains(upper, "SYS.PARAMETERS") {
		return execSysParameters(sql)
	}

	// 10. SQL Server Triggers
	if strings.Contains(upper, "TRIGGERNAME") || (strings.Contains(upper, "SYS.OBJECTS") && containsEq(upper, "TYPE", "'TR'")) {
		return execSysObjectsTriggers(sql)
	}

	// 11. SQL Server SQL Code (syscomments / sys.sql_modules)
	if strings.Contains(upper, "SYS.SYSCOMMENTS") || strings.Contains(upper, "SYS.SQL_MODULES") || (strings.Contains(upper, "SYS.OBJECTS") && (strings.Contains(upper, "CODETEXT") || strings.Contains(upper, "DEFINITION"))) {
		return execSysCommentsSqlCode(src, sql)
	}

	// 12. SQL Server Programmables (procedures, functions)
	if strings.Contains(upper, "SQLOBJECTTYPE") || (strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "('P', 'IF', 'FN', 'TF')")) {
		return execSysObjectsProgrammables(src, sql)
	}

	// 13. SQL Server Views
	if strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "SYS.SCHEMAS") && containsEq(upper, "TYPE", "'V'") {
		return execSysObjectsViews(src, sql)
	}

	// 14. SQL Server Tables metadata
	if strings.Contains(upper, "SYS.TABLES") && (strings.Contains(upper, "SYS.SCHEMAS") || strings.Contains(upper, "PURENAME") || strings.Contains(upper, "OBJECTCOMMENT")) {
		return execSysTablesMetadata(src, sql)
	}

	// 15. SQL Server Master Object metadata
	if strings.Contains(upper, "SYS.OBJECTS") && (strings.Contains(upper, "SYS.SCHEMAS") || strings.Contains(upper, "PURENAME")) {
		return execSysObjectsMetadata(src, sql)
	}

	// 15.5. SQL Server Collation List: fn_helpcollations()
	if strings.Contains(upper, "FN_HELPCOLLATIONS") {
		return execFnHelpCollations(sql)
	}

	// 16. PostgreSQL Information Schema tables with persistence
	if strings.Contains(upper, "INFORMATION_SCHEMA.TABLES") && strings.Contains(upper, "PG_CLASS") {
		return execInformationSchemaTablesWithPersistenceSelect(src, sql)
	}

	// 17. PostgreSQL Constraints: Foreign keys, Primary keys, Unique constraints
	if strings.Contains(upper, "PG_CONSTRAINT") {
		if strings.Contains(upper, "SRC_TBL") || containsEq(upper, "CONTYPE", "'F'") || strings.Contains(upper, "REF_TABLE") {
			return execForeignKeySelect(src, sql)
		}
		if !strings.Contains(upper, "PG_ATTRIBUTE") {
			return execPgConstraintPrimaryKeySelect(src, sql)
		}
	}

	// 18. PostgreSQL Column metadata query: pg_attribute
	if strings.Contains(upper, "PG_ATTRIBUTE") {
		return execColumnMetadataSelect(src, sql)
	}

	// 18. PostgreSQL Indexes
	if strings.Contains(upper, "PG_INDEX") {
		return execPgIndexSelect(src, sql)
	}

	// 19. PostgreSQL Triggers
	if strings.Contains(upper, "PG_TRIGGER") {
		return execPgTriggerSelect(src, sql)
	}

	// 20. PostgreSQL Column metadata query: pg_attribute + pg_class
	if strings.Contains(upper, "PG_ATTRIBUTE") && strings.Contains(upper, "PG_CLASS") {
		return execColumnMetadataSelect(src, sql)
	}

	// 21. PostgreSQL Procedure / Function metadata query
	if strings.Contains(upper, "PG_PROC") && strings.Contains(upper, "PG_NAMESPACE") {
		return execProcSelect(src, sql)
	}

	// 22. PostgreSQL pg_shdescription queries
	if strings.Contains(upper, "PG_SHDESCRIPTION") {
		return execPgShdescriptionSelect(sql)
	}

	// 23. PostgreSQL pg_conversion query
	if strings.Contains(upper, "PG_CONVERSION") {
		return execPgConversionSelect(sql)
	}

	// 24. SQL Server sys.all_objects query
	if strings.Contains(upper, "SYS.ALL_OBJECTS") || (strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "EXTENDED_PROPERTIES")) {
		return execSysAllObjectsJoined(src, sql)
	}

	// 25. PostgreSQL Class + Namespace: Tables and Views
	return execClassSelect(src, sql)
}

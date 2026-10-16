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
	return containsFold(sql, "sys.") || containsFold(sql, "pg_") || containsFold(sql, "information_schema.")
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

	if strings.Contains(upper, "SYS.COLUMNS") && matchAny(upper, "JOIN", "SYS.TYPES") {
		return true
	}
	if strings.Contains(upper, "SYS.INDEXES") && matchAny(upper, "JOIN", "IS_PRIMARY_KEY") {
		return true
	}
	if matchAny(upper, "SYS.INDEX_COLUMNS", "SYS.ALL_OBJECTS", "SYS.SYSCOMMENTS") {
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

	// 1. DbGate SQL Server Primary Keys
	if strings.Contains(upper, "SYS.INDEXES") && containsEq(upper, "IS_PRIMARY_KEY", "1") {
		return execDbGateMSSQLPrimaryKeySelect(src, sql)
	}

	// 2. DbGate SQL Server Indexes
	if strings.Contains(upper, "SYS.INDEXES") && containsEq(upper, "IS_PRIMARY_KEY", "0") {
		return execDbGateMSSQLIndexSelect(src, sql)
	}

	// 3. DbGate SQL Server Index Columns
	if strings.Contains(upper, "SYS.INDEX_COLUMNS") {
		return execDbGateMSSQLIndexColumnSelect(src, sql)
	}

	// 4. DbGate & Modern GUI SQL Server column query:
	if strings.Contains(upper, "SYS.COLUMNS") && (strings.Contains(upper, "SYS.TYPES") || strings.Contains(upper, "ISIDENTITY") || strings.Contains(upper, "IS_IDENTITY") || strings.Contains(upper, "DATATYPE")) {
		return execDbGateMSSQLColumnSelect(src, sql)
	}

	// 5. DbGate SQL Server column comments (MS_Description)
	if strings.Contains(upper, "COLUMNCOMMENT") && strings.Contains(upper, "SYS.COLUMNS") {
		return execDbGateMSSQLColumnCommentSelect(sql)
	}

	// 6. Beekeeper Studio SQL Server column query:
	if strings.Contains(upper, "INFORMATION_SCHEMA.COLUMNS") && strings.Contains(upper, "SYS.COMPUTED_COLUMNS") {
		return execBeekeeperMSSQLColumnSelect(src, sql)
	}

	// 7. DbGate SQL Server Foreign Keys
	if strings.Contains(upper, "INFORMATION_SCHEMA.REFERENTIAL_CONSTRAINTS") {
		return execDbGateMSSQLForeignKeySelect(src, sql)
	}

	// 8. DbGate SQL Server Table Row Count
	if strings.Contains(upper, "TABLEROWCOUNT") || (strings.Contains(upper, "SYS.TABLES") && strings.Contains(upper, "SYS.PARTITIONS")) {
		return execDbGateMSSQLRowCountSelect(src, sql)
	}

	// 9. DbGate SQL Server Parameters
	if strings.Contains(upper, "SYS.PARAMETERS") {
		return execDbGateMSSQLParamSelect(sql)
	}

	// 10. DbGate SQL Server Triggers
	if strings.Contains(upper, "TRIGGERNAME") || (strings.Contains(upper, "SYS.OBJECTS") && containsEq(upper, "TYPE", "'TR'")) {
		return execDbGateMSSQLTriggerSelect(sql)
	}

	// 11. DbGate SQL Server SQL Code (loadSqlCode)
	if strings.Contains(upper, "SYS.SYSCOMMENTS") || (strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "CODETEXT")) {
		return execDbGateMSSQLLoadSqlCodeSelect(src, sql)
	}

	// 12. DbGate SQL Server Programmables
	if strings.Contains(upper, "SQLOBJECTTYPE") || (strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "('P', 'IF', 'FN', 'TF')")) {
		return execDbGateMSSQLProgrammablesSelect(src, sql)
	}

	// 13. DbGate SQL Server Views
	if strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "SYS.SCHEMAS") && containsEq(upper, "TYPE", "'V'") {
		return execDbGateMSSQLViewSelect(src, sql)
	}

	// 14. DbGate SQL Server Tables (tables.js)
	if strings.Contains(upper, "SYS.TABLES") && strings.Contains(upper, "SYS.SCHEMAS") && (strings.Contains(upper, "PURENAME") || strings.Contains(upper, "OBJECTCOMMENT")) {
		return execDbGateMSSQLTableSelect(src, sql)
	}

	// 15. DbGate SQL Server Object Modifications / Master Object List (modifications.js)
	if strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "SYS.SCHEMAS") && strings.Contains(upper, "PURENAME") {
		return execDbGateMSSQLObjectSelect(src, sql)
	}

	// 16. Beekeeper Studio Postgres table query:
	if strings.Contains(upper, "INFORMATION_SCHEMA.TABLES") && strings.Contains(upper, "PG_CLASS") {
		return execBeekeeperTableSelect(src, sql)
	}

	// 17. Constraints: Foreign keys, Primary keys, Unique constraints
	if strings.Contains(upper, "PG_CONSTRAINT") {
		if strings.Contains(upper, "SRC_TBL") || containsEq(upper, "CONTYPE", "'F'") || strings.Contains(upper, "REF_TABLE") || strings.Contains(upper, "CONFRELID") {
			return execForeignKeySelect(src, sql)
		}
		return execDbGatePrimaryKeySelect(src, sql)
	}

	// 18. Indexes (DbGate indexes.js, indexcols.js)
	if strings.Contains(upper, "PG_INDEX") {
		return execDbGateIndexSelect(src, sql)
	}

	// 19. Triggers (DbGate triggers.js)
	if strings.Contains(upper, "PG_TRIGGER") {
		return execDbGateTriggerSelect(src, sql)
	}

	// 20. Column metadata query: pg_attribute + pg_class (DbGate columns.js)
	if strings.Contains(upper, "PG_ATTRIBUTE") && strings.Contains(upper, "PG_CLASS") {
		return execColumnMetadataSelect(src, sql)
	}

	// 21. Procedure / Function metadata query
	if strings.Contains(upper, "PG_PROC") && strings.Contains(upper, "PG_NAMESPACE") {
		return execProcSelect(src, sql)
	}

	// 22. DBeaver PostgreSQL pg_shdescription queries
	if strings.Contains(upper, "PG_SHDESCRIPTION") {
		return execPgShdescriptionSelect(sql)
	}

	// 23. DBeaver PostgreSQL pg_conversion query
	if strings.Contains(upper, "PG_CONVERSION") {
		return execPgConversionSelect(sql)
	}

	// 24. DBeaver SQL Server sys.all_objects query
	if strings.Contains(upper, "SYS.ALL_OBJECTS") || (strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "EXTENDED_PROPERTIES")) {
		return execDBeaverMSSQLAllObjectsSelect(src, sql)
	}

	// 25. Class + Namespace: Tables and Views (DbGate tableList.js, views.js, DBeaver)
	return execClassSelect(src, sql)
}

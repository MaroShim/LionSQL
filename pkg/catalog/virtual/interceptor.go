package virtual

import (
	"strings"

	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
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

	// Structural AST verification: ensure query actually references a virtual catalog table
	qc := proc.ExtractQueryCharacteristics(sql)
	if len(qc.TargetTables) > 0 {
		hasVirtual := false
		for _, t := range qc.TargetTables {
			if IsVirtualTable(t) {
				hasVirtual = true
				break
			}
		}
		if !hasVirtual {
			return false
		}
	}

	upper := strings.ToUpper(sql)
	upper = strings.ReplaceAll(strings.ReplaceAll(upper, "[", ""), "]", "")

	if (strings.Contains(upper, "SYS.COLUMNS") || strings.Contains(upper, "SYS.ALL_COLUMNS")) && matchAny(upper, "JOIN", "SYS.TYPES", "SYS.TABLES") {
		return true
	}
	if strings.Contains(upper, "SYS.INDEXES") && matchAny(upper, "JOIN", "IS_PRIMARY_KEY", "TYPE_DESC", "HAS_FILTER", "INDEX_ID") {
		return true
	}
	if matchAny(upper, "SYS.INDEX_COLUMNS", "SYS.KEY_CONSTRAINTS", "SYS.FOREIGN_KEYS", "SYS.CHECK_CONSTRAINTS", "SYS.DEFAULT_CONSTRAINTS", "SYS.STATS", "SYS.TRIGGERS", "SYS.ALL_OBJECTS", "SYS.SYSCOMMENTS", "SYS.SQL_MODULES", "SYS.PROCEDURES", "SYS.ALL_PARAMETERS", "FN_HELPCOLLATIONS") {
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

// RouteRule defines a single table-driven dispatch entry for virtual catalog queries.
type RouteRule struct {
	Name    string
	Match   func(upper string, qc *proc.QueryCharacteristics) bool
	Handler func(src Source, sql string) (*Result, error)
}

// catalogRouteTable is the authoritative, static dispatch table for virtual catalog queries.
// Rules are evaluated with deterministic priority: most specific compound signatures first,
// general fallbacks last.
var catalogRouteTable = []RouteRule{
	// --- Microsoft SQL Server (sys.* / INFORMATION_SCHEMA) ---
	{
		Name: "MSSQL_FnHelpCollations",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "FN_HELPCOLLATIONS")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execFnHelpCollations(sql)
		},
	},
	{
		Name: "MSSQL_XORMGetIndexes",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			fromCols := strings.Contains(upper, "FROM SYS.COLUMNS") || strings.Contains(upper, "FROM SYS.ALL_COLUMNS")
			return !fromCols && strings.Contains(upper, "OBJECT_NAME") && strings.Contains(upper, "SYS.INDEXES") && strings.Contains(upper, "SYS.INDEX_COLUMNS") && strings.Contains(upper, "SYS.COLUMNS")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execXORMIndexes(src, sql)
		},
	},
	{
		Name: "MSSQL_SysIndexesPrimary",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			fromCols := strings.Contains(upper, "FROM SYS.COLUMNS") || strings.Contains(upper, "FROM SYS.ALL_COLUMNS")
			return !fromCols && strings.Contains(upper, "SYS.INDEXES") && containsEq(upper, "IS_PRIMARY_KEY", "1")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysIndexesPrimaryKey(src, sql)
		},
	},
	{
		Name: "MSSQL_SysIndexesSecondary",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			fromCols := strings.Contains(upper, "FROM SYS.COLUMNS") || strings.Contains(upper, "FROM SYS.ALL_COLUMNS")
			return !fromCols && strings.Contains(upper, "SYS.INDEXES") && containsEq(upper, "IS_PRIMARY_KEY", "0")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysIndexesSecondary(src, sql)
		},
	},
	{
		Name: "MSSQL_SysIndexColumns",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			fromCols := strings.Contains(upper, "FROM SYS.COLUMNS") || strings.Contains(upper, "FROM SYS.ALL_COLUMNS")
			return !fromCols && strings.Contains(upper, "SYS.INDEX_COLUMNS")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysIndexColumns(src, sql)
		},
	},
	{
		Name: "MSSQL_ColumnsJoinedWithTypes",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			hasCols := strings.Contains(upper, "SYS.COLUMNS") || strings.Contains(upper, "SYS.ALL_COLUMNS")
			if !hasCols {
				return false
			}
			// Guard: If this is a table query (primary projection is Table, URN is /Table@Name without /Column@Name),
			// do NOT hijack to column handler.
			if strings.Contains(upper, "TBL.NAME AS NAME") || (strings.Contains(upper, "/TABLE@NAME=") && !strings.Contains(upper, "/COLUMN@NAME=")) {
				return false
			}
			return strings.Contains(upper, "/COLUMN@NAME=") || strings.Contains(upper, "CLMNS.NAME") ||
				strings.Contains(upper, "CLMNS.COLUMN_ID") || strings.Contains(upper, "CLMNS.OBJECT_ID") ||
				strings.Contains(upper, "FROM SYS.COLUMNS") || strings.Contains(upper, "FROM SYS.ALL_COLUMNS") ||
				strings.Contains(upper, "JOIN SYS.COLUMNS") || strings.Contains(upper, "JOIN SYS.ALL_COLUMNS") ||
				strings.Contains(upper, "SYS.TYPES") || strings.Contains(upper, "IS_IDENTITY") || strings.Contains(upper, "MAX_LENGTH")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysColumnsJoinedWithTypes(src, sql)
		},
	},
	{
		Name: "MSSQL_KeyConstraints",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "SYS.KEY_CONSTRAINTS")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysKeyConstraintsMSSQL(src, sql)
		},
	},
	{
		Name: "MSSQL_ForeignKeys",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "SYS.FOREIGN_KEYS")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysForeignKeysMSSQL(src, sql)
		},
	},
	{
		Name: "MSSQL_CheckConstraints",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "SYS.CHECK_CONSTRAINTS")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysCheckConstraintsMSSQL(src, sql)
		},
	},
	{
		Name: "MSSQL_DefaultConstraints",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "SYS.DEFAULT_CONSTRAINTS")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysDefaultConstraintsMSSQL(src, sql)
		},
	},
	{
		Name: "MSSQL_Stats",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "SYS.STATS")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysStatsMSSQL(src, sql)
		},
	},
	{
		Name: "MSSQL_ColumnComments",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "COLUMNCOMMENT") && (strings.Contains(upper, "SYS.COLUMNS") || strings.Contains(upper, "SYS.ALL_COLUMNS"))
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysColumnsExtendedPropertyComment(sql)
		},
	},
	{
		Name: "MSSQL_InfoSchemaColumnsComputed",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "INFORMATION_SCHEMA.COLUMNS") && strings.Contains(upper, "SYS.COMPUTED_COLUMNS")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execInformationSchemaColumnsWithComputed(src, sql)
		},
	},
	{
		Name: "MSSQL_ReferentialConstraints",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "INFORMATION_SCHEMA.REFERENTIAL_CONSTRAINTS")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execReferentialConstraintsForeignKey(src, sql)
		},
	},
	{
		Name: "MSSQL_TableRowCount",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "TABLEROWCOUNT") || (strings.Contains(upper, "SYS.TABLES") && strings.Contains(upper, "SYS.PARTITIONS"))
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysTablesRowCount(src, sql)
		},
	},
	{
		Name: "MSSQL_Parameters",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "SYS.PARAMETERS") || strings.Contains(upper, "SYS.ALL_PARAMETERS")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysParameters(src, sql)
		},
	},
	{
		Name: "MSSQL_Triggers",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "TRIGGERNAME") || (strings.Contains(upper, "SYS.OBJECTS") && containsEq(upper, "TYPE", "'TR'"))
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysObjectsTriggers(sql)
		},
	},
	{
		Name: "MSSQL_SysProceduresSSMS",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			if strings.Contains(upper, "SYS.PARAMETERS") || strings.Contains(upper, "SYS.ALL_PARAMETERS") || strings.Contains(upper, "/PARAM@NAME=") {
				return false
			}
			if strings.Contains(upper, "STOREDPROCEDURE") {
				return true
			}
			if strings.Contains(upper, "SYS.PROCEDURES") || strings.Contains(upper, "SYSPROCEDURES") {
				return true
			}
			if (strings.Contains(upper, "SYS.ALL_OBJECTS") || strings.Contains(upper, "SYS.OBJECTS")) &&
				(strings.Contains(upper, "'P'") || strings.Contains(upper, "N'P'")) &&
				!strings.Contains(upper, "/TABLE@NAME=") && !strings.Contains(upper, "/VIEW@NAME=") && !strings.Contains(upper, "/INDEX@NAME=") {
				return true
			}
			return false
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysProceduresForSSMS(src, sql)
		},
	},
	{
		Name: "MSSQL_CommentsSqlCode",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "SYS.SYSCOMMENTS") || strings.Contains(upper, "SYS.SQL_MODULES") || (strings.Contains(upper, "SYS.OBJECTS") && (strings.Contains(upper, "CODETEXT") || strings.Contains(upper, "DEFINITION")))
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysCommentsSqlCode(src, sql)
		},
	},
	{
		Name: "MSSQL_Programmables",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "SQLOBJECTTYPE") || (strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "('P', 'IF', 'FN', 'TF')"))
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysObjectsProgrammables(src, sql)
		},
	},
	{
		Name: "MSSQL_Views",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "SYS.SCHEMAS") && containsEq(upper, "TYPE", "'V'")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysObjectsViews(src, sql)
		},
	},
	{
		Name: "MSSQL_SysTableIndexesSSMS",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			if !strings.Contains(upper, "SYS.TABLES") || !strings.Contains(upper, "SYS.INDEXES") {
				return false
			}
			// Guard: If this is a table query (primary projection is Table), do NOT hijack to index handler.
			// Note: '[' and ']' are stripped in upper!
			if strings.Contains(upper, "TBL.NAME AS NAME") || (strings.Contains(upper, "/TABLE@NAME=") && !strings.Contains(upper, "/INDEX@NAME=")) {
				return false
			}
			return strings.Contains(upper, "/INDEX@NAME=") || strings.Contains(upper, "I.OBJECT_ID") ||
				strings.Contains(upper, "I.INDEX_ID") || strings.Contains(upper, "I.NAME") ||
				strings.Contains(upper, "INDEXTYPE") || (strings.Contains(upper, "TYPE_DESC") && !strings.Contains(upper, "TBL.CREATE_DATE"))
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysIndexesForSSMS(src, sql)
		},
	},
	{
		Name: "MSSQL_SysTablesJoinedIndexes",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "SYS.TABLES") && (strings.Contains(upper, "SYS.INDEXES") || strings.Contains(upper, "DATA_SPACES"))
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysTablesJoinedIndexes(src, sql)
		},
	},
	{
		Name: "MSSQL_TablesMetadata",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "SYS.TABLES") && (strings.Contains(upper, "SYS.SCHEMAS") || strings.Contains(upper, "PURENAME") || strings.Contains(upper, "OBJECTCOMMENT"))
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysTablesMetadata(src, sql)
		},
	},
	{
		Name: "MSSQL_ObjectsMetadata",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "SYS.OBJECTS") && (strings.Contains(upper, "SYS.SCHEMAS") || strings.Contains(upper, "PURENAME"))
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysObjectsMetadata(src, sql)
		},
	},

	// --- PostgreSQL (pg_* / INFORMATION_SCHEMA) ---
	{
		Name: "PG_InfoSchemaTablesPersistence",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "INFORMATION_SCHEMA.TABLES") && strings.Contains(upper, "PG_CLASS")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execInformationSchemaTablesWithPersistenceSelect(src, sql)
		},
	},
	{
		Name: "PG_ForeignKeys",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "PG_CONSTRAINT") && (strings.Contains(upper, "SRC_TBL") || containsEq(upper, "CONTYPE", "'F'") || strings.Contains(upper, "REF_TABLE"))
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execForeignKeySelect(src, sql)
		},
	},
	{
		Name: "PG_ConstraintsPrimary",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "PG_CONSTRAINT") && !strings.Contains(upper, "PG_ATTRIBUTE")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execPgConstraintPrimaryKeySelect(src, sql)
		},
	},
	{
		Name: "PG_Indexes",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "PG_INDEX")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execPgIndexSelect(src, sql)
		},
	},
	{
		Name: "PG_Triggers",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "PG_TRIGGER")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execPgTriggerSelect(src, sql)
		},
	},
	{
		Name: "PG_ProceduresFunctions",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "PG_PROC") && strings.Contains(upper, "PG_NAMESPACE")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execProcSelect(src, sql)
		},
	},
	{
		Name: "PG_ShDescription",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "PG_SHDESCRIPTION")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execPgShdescriptionSelect(sql)
		},
	},
	{
		Name: "PG_Conversion",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "PG_CONVERSION")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execPgConversionSelect(sql)
		},
	},
	{
		Name: "MSSQL_SysAllObjects",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "SYS.ALL_OBJECTS") || (strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "EXTENDED_PROPERTIES"))
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execSysAllObjectsJoined(src, sql)
		},
	},
	{
		Name: "PG_ColumnMetadataAttribute",
		Match: func(upper string, qc *proc.QueryCharacteristics) bool {
			return strings.Contains(upper, "PG_ATTRIBUTE")
		},
		Handler: func(src Source, sql string) (*Result, error) {
			return execColumnMetadataSelect(src, sql)
		},
	},
}

// Execute routes and executes an intercepted virtual catalog join query against the provided Source.
func Execute(src Source, sql string) (*Result, error) {
	upper := strings.ToUpper(sql)
	upper = strings.ReplaceAll(strings.ReplaceAll(upper, "[", ""), "]", "")

	var qc *proc.QueryCharacteristics
	if sqlutil.ContainsKeyword(sql, "FROM", false) || sqlutil.ContainsKeyword(sql, "JOIN", false) {
		characteristics := proc.ExtractQueryCharacteristics(sql)
		qc = &characteristics
	}

	for _, rule := range catalogRouteTable {
		if rule.Match(upper, qc) {
			return rule.Handler(src, sql)
		}
	}

	// Default fallback: PostgreSQL Class + Namespace: Tables and Views
	return execClassSelect(src, sql)
}

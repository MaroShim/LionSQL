package virtual

import (
	"strings"
)

// IsVirtualCatalogJoinQuery returns true if the SQL query matches a known virtual catalog cross-join query pattern.
func IsVirtualCatalogJoinQuery(sql string) bool {
	upper := strings.ToUpper(sql)
	if strings.Contains(upper, "SYS.COLUMNS") && (strings.Contains(upper, "JOIN") || strings.Contains(upper, "SYS.TYPES")) {
		return true
	}
	if strings.Contains(upper, "SYS.INDEXES") && (strings.Contains(upper, "JOIN") || strings.Contains(upper, "IS_PRIMARY_KEY")) {
		return true
	}
	if strings.Contains(upper, "SYS.INDEX_COLUMNS") {
		return true
	}
	if strings.Contains(upper, "INFORMATION_SCHEMA.REFERENTIAL_CONSTRAINTS") && (strings.Contains(upper, "JOIN") || strings.Contains(upper, "SCHEMANAME")) {
		return true
	}
	if strings.Contains(upper, "SYS.PARAMETERS") && strings.Contains(upper, "JOIN") {
		return true
	}
	if strings.Contains(upper, "SYS.TABLES") && (strings.Contains(upper, "JOIN") || strings.Contains(upper, "TABLEROWCOUNT") || strings.Contains(upper, "OBJECTCOMMENT")) {
		return true
	}
	if strings.Contains(upper, "SYS.OBJECTS") && (strings.Contains(upper, "TRIGGERNAME") || strings.Contains(upper, "O.TYPE = 'TR'") || strings.Contains(upper, "O.TYPE='TR'") || strings.Contains(upper, "JOIN")) {
		return true
	}
	if (strings.Contains(upper, "SYS.OBJECTS") || strings.Contains(upper, "SYS.TABLES")) && strings.Contains(upper, "SYS.SCHEMAS") {
		return true
	}
	if strings.Contains(upper, "SYS.SYSCOMMENTS") || (strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "CODETEXT")) {
		return true
	}
	if strings.Contains(upper, "INFORMATION_SCHEMA.COLUMNS") && strings.Contains(upper, "SYS.COMPUTED_COLUMNS") {
		return true
	}
	if strings.Contains(upper, "INFORMATION_SCHEMA.TABLES") && strings.Contains(upper, "PG_CLASS") {
		return true
	}
	if strings.Contains(upper, "PG_CONSTRAINT") && (strings.Contains(upper, "JOIN") || strings.Contains(upper, "UNNEST") || strings.Contains(upper, "SRC_TBL") || strings.Contains(upper, "CONKEY")) {
		return true
	}
	if strings.Contains(upper, "PG_INDEX") && (strings.Contains(upper, "JOIN") || strings.Contains(upper, "PG_NAMESPACE") || strings.Contains(upper, "INDEX_NAME") || strings.Contains(upper, "INDKEY")) {
		return true
	}
	if strings.Contains(upper, "PG_TRIGGER") && strings.Contains(upper, "JOIN") {
		return true
	}
	if strings.Contains(upper, "PG_CLASS") && (strings.Contains(upper, "PG_NAMESPACE") || strings.Contains(upper, "VIEW_DEFS") || strings.Contains(upper, "RELNAMESPACE")) {
		return true
	}
	if strings.Contains(upper, "PG_PROC") && strings.Contains(upper, "PG_NAMESPACE") {
		return true
	}
	if strings.Contains(upper, "PG_ATTRIBUTE") && (strings.Contains(upper, "PG_CLASS") || strings.Contains(upper, "JOIN")) {
		return true
	}
	if strings.Contains(upper, "PG_SHDESCRIPTION") {
		return true
	}
	if strings.Contains(upper, "PG_CONVERSION") {
		return true
	}
	if strings.Contains(upper, "SYS.ALL_OBJECTS") {
		return true
	}
	return false
}

// Execute routes and executes an intercepted virtual catalog join query against the provided Source.
func Execute(src Source, sql string) (*Result, error) {
	upper := strings.ToUpper(sql)

	// 1. DbGate SQL Server Primary Keys
	if strings.Contains(upper, "SYS.INDEXES") && (strings.Contains(upper, "IS_PRIMARY_KEY = 1") || strings.Contains(upper, "IS_PRIMARY_KEY=1")) {
		return execDbGateMSSQLPrimaryKeySelect(src, sql)
	}

	// 2. DbGate SQL Server Indexes
	if strings.Contains(upper, "SYS.INDEXES") && (strings.Contains(upper, "IS_PRIMARY_KEY=0") || strings.Contains(upper, "IS_PRIMARY_KEY = 0")) {
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
	if strings.Contains(upper, "TRIGGERNAME") || (strings.Contains(upper, "SYS.OBJECTS") && (strings.Contains(upper, "TYPE = 'TR'") || strings.Contains(upper, "TYPE='TR'"))) {
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
	if strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "SYS.SCHEMAS") && (strings.Contains(upper, "TYPE='V'") || strings.Contains(upper, "TYPE = 'V'")) {
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
		if strings.Contains(upper, "SRC_TBL") || strings.Contains(upper, "CONTYPE = 'F'") || strings.Contains(upper, "REF_TABLE") || strings.Contains(upper, "CONFRELID") {
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

package engine

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"sqlion/pkg/storage"
)

func isHeidiSQLCatalogJoinQuery(sql string) bool {
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

func (s *Coordinator) execHeidiSQLCatalogJoinSelect(sql string, start time.Time) (*QueryResult, error) {
	upper := strings.ToUpper(sql)

	// 1. DbGate SQL Server Primary Keys
	if strings.Contains(upper, "SYS.INDEXES") && (strings.Contains(upper, "IS_PRIMARY_KEY = 1") || strings.Contains(upper, "IS_PRIMARY_KEY=1")) {
		return s.execDbGateMSSQLPrimaryKeySelect(sql, start)
	}

	// 2. DbGate SQL Server Indexes
	if strings.Contains(upper, "SYS.INDEXES") && (strings.Contains(upper, "IS_PRIMARY_KEY=0") || strings.Contains(upper, "IS_PRIMARY_KEY = 0")) {
		return s.execDbGateMSSQLIndexSelect(sql, start)
	}

	// 3. DbGate SQL Server Index Columns
	if strings.Contains(upper, "SYS.INDEX_COLUMNS") {
		return s.execDbGateMSSQLIndexColumnSelect(sql, start)
	}

	// 4. DbGate & Modern GUI SQL Server column query:
	if strings.Contains(upper, "SYS.COLUMNS") && (strings.Contains(upper, "SYS.TYPES") || strings.Contains(upper, "ISIDENTITY") || strings.Contains(upper, "IS_IDENTITY") || strings.Contains(upper, "DATATYPE")) {
		return s.execDbGateMSSQLColumnSelect(sql, start)
	}

	// 5. DbGate SQL Server column comments (MS_Description)
	if strings.Contains(upper, "COLUMNCOMMENT") && strings.Contains(upper, "SYS.COLUMNS") {
		return s.execDbGateMSSQLColumnCommentSelect(sql, start)
	}

	// 6. Beekeeper Studio SQL Server column query:
	if strings.Contains(upper, "INFORMATION_SCHEMA.COLUMNS") && strings.Contains(upper, "SYS.COMPUTED_COLUMNS") {
		return s.execBeekeeperMSSQLColumnSelect(sql, start)
	}

	// 7. DbGate SQL Server Foreign Keys
	if strings.Contains(upper, "INFORMATION_SCHEMA.REFERENTIAL_CONSTRAINTS") {
		return s.execDbGateMSSQLForeignKeySelect(sql, start)
	}

	// 8. DbGate SQL Server Table Row Count
	if strings.Contains(upper, "TABLEROWCOUNT") || (strings.Contains(upper, "SYS.TABLES") && strings.Contains(upper, "SYS.PARTITIONS")) {
		return s.execDbGateMSSQLRowCountSelect(sql, start)
	}

	// 9. DbGate SQL Server Parameters
	if strings.Contains(upper, "SYS.PARAMETERS") {
		return s.execDbGateMSSQLParamSelect(sql, start)
	}

	// 10. DbGate SQL Server Triggers
	if strings.Contains(upper, "TRIGGERNAME") || (strings.Contains(upper, "SYS.OBJECTS") && (strings.Contains(upper, "TYPE = 'TR'") || strings.Contains(upper, "TYPE='TR'"))) {
		return s.execDbGateMSSQLTriggerSelect(sql, start)
	}

	// 11. DbGate SQL Server SQL Code (loadSqlCode)
	if strings.Contains(upper, "SYS.SYSCOMMENTS") || (strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "CODETEXT")) {
		return s.execDbGateMSSQLLoadSqlCodeSelect(sql, start)
	}

	// 12. DbGate SQL Server Programmables
	if strings.Contains(upper, "SQLOBJECTTYPE") || (strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "('P', 'IF', 'FN', 'TF')")) {
		return s.execDbGateMSSQLProgrammablesSelect(sql, start)
	}

	// 13. DbGate SQL Server Views
	if strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "SYS.SCHEMAS") && (strings.Contains(upper, "TYPE='V'") || strings.Contains(upper, "TYPE = 'V'")) {
		return s.execDbGateMSSQLViewSelect(sql, start)
	}

	// 14. DbGate SQL Server Tables (tables.js)
	if strings.Contains(upper, "SYS.TABLES") && strings.Contains(upper, "SYS.SCHEMAS") && (strings.Contains(upper, "PURENAME") || strings.Contains(upper, "OBJECTCOMMENT")) {
		return s.execDbGateMSSQLTableSelect(sql, start)
	}

	// 15. DbGate SQL Server Object Modifications / Master Object List (modifications.js)
	if strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "SYS.SCHEMAS") && strings.Contains(upper, "PURENAME") {
		return s.execDbGateMSSQLObjectSelect(sql, start)
	}

	// 16. Beekeeper Studio Postgres table query:
	if strings.Contains(upper, "INFORMATION_SCHEMA.TABLES") && strings.Contains(upper, "PG_CLASS") {
		return s.execBeekeeperTableSelect(sql, start)
	}

	// 12. Constraints: Foreign keys, Primary keys, Unique constraints
	if strings.Contains(upper, "PG_CONSTRAINT") {
		if strings.Contains(upper, "SRC_TBL") || strings.Contains(upper, "CONTYPE = 'F'") || strings.Contains(upper, "REF_TABLE") || strings.Contains(upper, "CONFRELID") {
			return s.execHeidiSQLForeignKeySelect(sql, start)
		}
		return s.execDbGatePrimaryKeySelect(sql, start)
	}

	// 13. Indexes (DbGate indexes.js, indexcols.js)
	if strings.Contains(upper, "PG_INDEX") {
		return s.execDbGateIndexSelect(sql, start)
	}

	// 14. Triggers (DbGate triggers.js)
	if strings.Contains(upper, "PG_TRIGGER") {
		return s.execDbGateTriggerSelect(sql, start)
	}

	// 15. Column metadata query: pg_attribute + pg_class (DbGate columns.js, HeidiSQL)
	if strings.Contains(upper, "PG_ATTRIBUTE") && strings.Contains(upper, "PG_CLASS") {
		return s.execHeidiSQLColumnMetadataSelect(sql, start)
	}

	// 16. Procedure / Function metadata query
	if strings.Contains(upper, "PG_PROC") && strings.Contains(upper, "PG_NAMESPACE") {
		return s.execHeidiSQLProcSelect(sql, start)
	}

	// 17. DBeaver PostgreSQL pg_shdescription queries
	if strings.Contains(upper, "PG_SHDESCRIPTION") {
		return s.execDBeaverPgShdescriptionSelect(sql, start)
	}

	// 18. DBeaver PostgreSQL pg_conversion query
	if strings.Contains(upper, "PG_CONVERSION") {
		return s.execDBeaverPgConversionSelect(sql, start)
	}

	// 19. DBeaver SQL Server sys.all_objects query
	if strings.Contains(upper, "SYS.ALL_OBJECTS") || (strings.Contains(upper, "SYS.OBJECTS") && strings.Contains(upper, "EXTENDED_PROPERTIES")) {
		return s.execDBeaverMSSQLAllObjectsSelect(sql, start)
	}

	// 20. Class + Namespace: Tables and Views (DbGate tableList.js, views.js, HeidiSQL)
	return s.execHeidiSQLClassSelect(sql, start)
}

func (s *Coordinator) execDbGatePrimaryKeySelect(sql string, start time.Time) (*QueryResult, error) {
	targetSchema := "public"
	nspRe := regexp.MustCompile(`(?is)n\.nspname\s*=\s*'([^']+)'`)
	if m := nspRe.FindStringSubmatch(sql); len(m) == 2 {
		targetSchema = m[1]
	}

	columns := []string{
		"constraint_schema", "constraint_name", "schema_name",
		"pure_name", "table_name", "column_name",
	}

	var allRows []map[string]any
	tables, _ := s.engine.ListTables()
	for _, t := range tables {
		for _, c := range t.Columns {
			if c.PrimaryKey {
				allRows = append(allRows, map[string]any{
					"constraint_schema": targetSchema,
					"constraint_name":   "pk_" + t.Name,
					"schema_name":       targetSchema,
					"pure_name":         t.Name,
					"table_name":        t.Name,
					"column_name":       c.Name,
				})
			}
		}
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         allRows,
		RowsAffected: int64(len(allRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execDbGateIndexSelect(sql string, start time.Time) (*QueryResult, error) {
	upper := strings.ToUpper(sql)
	targetSchema := "public"

	if strings.Contains(upper, "ATTNAME") && strings.Contains(upper, "ATTNUM") {
		columns := []string{"column_name", "attnum", "oid"}
		var allRows []map[string]any
		tables, _ := s.engine.ListTables()
		for tIdx, t := range tables {
			toid := tableBaseOID(tIdx)
			for i, c := range t.Columns {
				allRows = append(allRows, map[string]any{
					"column_name": c.Name,
					"attnum":      int16(i + 1),
					"oid":         toid,
				})
			}
		}
		return &QueryResult{
			Success:      true,
			Command:      "SELECT",
			Columns:      columns,
			Rows:         allRows,
			RowsAffected: int64(len(allRows)),
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	columns := []string{
		"table_name", "pure_name", "schema_name", "index_name",
		"is_primary", "is_unique", "indkey", "indoption", "oid",
	}
	var allRows []map[string]any
	tables, _ := s.engine.ListTables()
	for tIdx, t := range tables {
		toid := tableBaseOID(tIdx)
		hasPK := false
		for _, c := range t.Columns {
			if c.PrimaryKey {
				hasPK = true
				break
			}
		}
		if hasPK {
			allRows = append(allRows, map[string]any{
				"table_name":  t.Name,
				"pure_name":   t.Name,
				"schema_name": targetSchema,
				"index_name":  "pk_" + t.Name,
				"is_primary":  true,
				"is_unique":   true,
				"indkey":      "1",
				"indoption":   "0",
				"oid":         toid,
			})
		}
		for _, idx := range t.Indexes {
			allRows = append(allRows, map[string]any{
				"table_name":  t.Name,
				"pure_name":   t.Name,
				"schema_name": targetSchema,
				"index_name":  idx.Name,
				"is_primary":  false,
				"is_unique":   idx.Unique,
				"indkey":      "1",
				"indoption":   "0",
				"oid":         toid,
			})
		}
	}
	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         allRows,
		RowsAffected: int64(len(allRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execDbGateTriggerSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{
		"schema_name", "pure_name", "table_name", "trigger_name",
		"timing", "event", "action_statement",
	}
	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         []map[string]any{},
		RowsAffected: 0,
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execHeidiSQLProcSelect(sql string, start time.Time) (*QueryResult, error) {
	upper := strings.ToUpper(sql)
	targetSchema := "public"
	nspRe := regexp.MustCompile(`(?is)n\.nspname\s*=\s*'([^']+)'`)
	if m := nspRe.FindStringSubmatch(sql); len(m) == 2 {
		targetSchema = m[1]
	}

	whereClause := ""
	projStr := "*"
	if whereIdx := strings.Index(upper, "WHERE"); whereIdx != -1 {
		whereClause = sql[whereIdx+5:]
		if orderIdx := strings.Index(strings.ToUpper(whereClause), "ORDER BY"); orderIdx != -1 {
			whereClause = whereClause[:orderIdx]
		}
	}
	if fromIdx := strings.Index(upper, "FROM"); fromIdx != -1 {
		selectPart := sql[:fromIdx]
		if selectIdx := strings.Index(strings.ToUpper(selectPart), "SELECT"); selectIdx != -1 {
			projStr = strings.TrimSpace(selectPart[selectIdx+6:])
		}
	}

	columns := []string{
		"schema_name", "pure_name", "object_name", "table_name", "object_kind",
		"size_bytes", "data_length", "index_length", "reltuples",
		"comment", "viewdef", "content", "hash", "proargtypes",
	}
	var allRows []map[string]any
	if s.catalog != nil {
		for _, p := range s.catalog.ListProcedures() {
			allRows = append(allRows, map[string]any{
				"schema_name":  targetSchema,
				"pure_name":    p.Name,
				"object_name":  p.Name,
				"table_name":   p.Name,
				"object_kind":  "p",
				"size_bytes":   nil,
				"data_length":  nil,
				"index_length": nil,
				"reltuples":    nil,
				"comment":      nil,
				"viewdef":      p.Body,
				"content":      p.Body,
				"hash":         "proc_hash",
				"proargtypes":  nil,
			})
		}
		for _, f := range s.catalog.ListFunctions() {
			allRows = append(allRows, map[string]any{
				"schema_name":  targetSchema,
				"pure_name":    f.Name,
				"object_name":  f.Name,
				"table_name":   f.Name,
				"object_kind":  "f",
				"size_bytes":   nil,
				"data_length":  nil,
				"index_length": nil,
				"reltuples":    nil,
				"comment":      nil,
				"viewdef":      f.Body,
				"content":      f.Body,
				"hash":         "func_hash",
				"proargtypes":  nil,
			})
		}
	}
	return buildVirtualCatalogResult(columns, allRows, projStr, whereClause, "", -1, start)
}

func (s *Coordinator) execHeidiSQLClassSelect(sql string, start time.Time) (*QueryResult, error) {
	upper := strings.ToUpper(sql)
	whereClause := ""
	projStr := "*"
	if whereIdx := strings.Index(upper, "WHERE"); whereIdx != -1 {
		whereClause = sql[whereIdx+5:]
		if orderIdx := strings.Index(strings.ToUpper(whereClause), "ORDER BY"); orderIdx != -1 {
			whereClause = whereClause[:orderIdx]
		}
	}
	if fromIdx := strings.Index(upper, "FROM"); fromIdx != -1 {
		selectPart := sql[:fromIdx]
		if selectIdx := strings.Index(strings.ToUpper(selectPart), "SELECT"); selectIdx != -1 {
			projStr = strings.TrimSpace(selectPart[selectIdx+6:])
		}
	}

	columns := []string{
		"oid", "relname", "relnamespace", "reltype", "reloftype",
		"relowner", "relam", "relfilenode", "reltablespace",
		"relpages", "reltuples", "relallvisible", "reltoastrelid",
		"relhasindex", "relisshared", "relpersistence", "relkind",
		"relnatts", "relchecks", "relhasrules", "relhastriggers",
		"relhassubclass", "relrowsecurity", "relforcerowsecurity",
		"relispopulated", "relreplident", "relispartition",
		"relrewrite", "relfrozenxid", "relminmxid", "relacl", "reloptions",
		"description", "partition_expr", "partition_key",
		"schema_name", "pure_name", "object_name", "table_name", "object_kind",
		"size_bytes", "data_length", "index_length",
		"comment", "viewdef", "content", "hash", "proargtypes",
	}

	schemas := []struct {
		name string
		oid  int
	}{
		{"public", 2200},
		{"dbo", 2201},
	}

	var allRows []map[string]any

	tables := s.getSortedTables()
	views := s.getSortedViews()

	// If query targets views specifically (DbGate views.js, HeidiSQL relkind = 'v', matviews)
	if strings.Contains(upper, "RELKIND = 'V'") || strings.Contains(upper, "VIEW_DEFS") || strings.Contains(upper, "MATVIEW") {
		for _, sch := range schemas {
			for vIdx, v := range views {
				allRows = append(allRows, map[string]any{
					"oid":            20000 + vIdx,
					"relname":        v.Name,
					"nspname":        sch.name,
					"relkind":        "v",
					"relnamespace":   sch.oid,
					"relpersistence": "p",
					"relispopulated": true,
					"description":    nil,
					"partition_expr": nil,
					"partition_key":  nil,
					"schema_name":    sch.name,
					"pure_name":      v.Name,
					"object_name":    v.Name,
					"table_name":     v.Name,
					"object_kind":    "v",
					"size_bytes":     int64(0),
					"data_length":    int64(0),
					"index_length":   int64(0),
					"reltuples":      float32(0),
					"comment":        nil,
					"viewdef":        v.Query,
					"content":        v.Query,
					"hash":           "view_hash",
					"proargtypes":    nil,
				})
			}
		}
		return buildVirtualCatalogResult(columns, allRows, projStr, whereClause, "", -1, start)
	}

	// Standard tables query (DbGate tableList.js, HeidiSQL, DBeaver)
	for _, sch := range schemas {
		for tIdx, t := range tables {
			toid := tableBaseOID(tIdx)
			allRows = append(allRows, map[string]any{
				"oid":                 toid,
				"relname":             t.Name,
				"nspname":             sch.name,
				"relkind":             "r",
				"relnamespace":        sch.oid,
				"reltype":             0,
				"reloftype":           0,
				"relowner":            10,
				"relam":               2,
				"relfilenode":         toid,
				"reltablespace":       1663,
				"relpages":            int64(10),
				"reltuples":           float32(1000),
				"relallvisible":       int64(0),
				"reltoastrelid":       0,
				"relhasindex":         len(t.Indexes) > 0,
				"relisshared":         false,
				"relpersistence":      "p",
				"relnatts":            len(t.Columns),
				"relchecks":           0,
				"relhasrules":         false,
				"relhastriggers":      false,
				"relhassubclass":      false,
				"relrowsecurity":      false,
				"relforcerowsecurity": false,
				"relispopulated":      true,
				"relreplident":        "d",
				"relispartition":      false,
				"relrewrite":          0,
				"relfrozenxid":        0,
				"relminmxid":          0,
				"relacl":              nil,
				"reloptions":          nil,
				"description":         nil,
				"partition_expr":      nil,
				"partition_key":       nil,
				"schema_name":         sch.name,
				"pure_name":           t.Name,
				"object_name":         t.Name,
				"table_name":          t.Name,
				"object_kind":         "r",
				"size_bytes":          int64(16384),
				"data_length":         int64(16384),
				"index_length":        int64(8192),
				"comment":             nil,
				"viewdef":             nil,
				"content":             nil,
				"hash":                "table_hash",
				"proargtypes":         nil,
			})
		}
		for vIdx, v := range views {
			allRows = append(allRows, map[string]any{
				"oid":            20000 + vIdx,
				"relname":        v.Name,
				"nspname":        sch.name,
				"relkind":        "v",
				"relnamespace":   sch.oid,
				"relpersistence": "p",
				"relispopulated": true,
				"description":    nil,
				"partition_expr": nil,
				"partition_key":  nil,
				"schema_name":    sch.name,
				"pure_name":      v.Name,
				"object_name":    v.Name,
				"table_name":     v.Name,
				"object_kind":    "v",
				"size_bytes":     int64(0),
				"data_length":    int64(0),
				"index_length":   int64(0),
				"reltuples":      float32(0),
				"comment":        nil,
				"viewdef":        v.Query,
				"content":        v.Query,
				"hash":           "view_hash",
				"proargtypes":    nil,
			})
		}
	}

	return buildVirtualCatalogResult(columns, allRows, projStr, whereClause, "", -1, start)
}

func (s *Coordinator) execHeidiSQLColumnMetadataSelect(sql string, start time.Time) (*QueryResult, error) {
	targetTable := ""
	tblRe := regexp.MustCompile(`(?is)(?:pgc|c)\.relname\s*=\s*'([^']+)'`)
	if m := tblRe.FindStringSubmatch(sql); len(m) == 2 {
		targetTable = m[1]
	}

	targetSchema := "public"
	nspRe := regexp.MustCompile(`(?is)n\.nspname\s*=\s*'([^']+)'`)
	if m := nspRe.FindStringSubmatch(sql); len(m) == 2 {
		targetSchema = m[1]
	}

	columns := []string{
		"schema_name", "pure_name", "table_name",
		"postgres_table_id", "column_name", "postgres_column_id",
		"attnum", "atttypid", "data_type", "is_nullable",
		"char_max_length", "character_maximum_length",
		"numeric_precision", "numeric_scale",
		"default_value", "column_default", "column_comment",
	}

	var allRows []map[string]any
	tables, _ := s.engine.ListTables()

	for tIdx, t := range tables {
		if targetTable != "" && !strings.EqualFold(t.Name, targetTable) {
			continue
		}
		toid := tableBaseOID(tIdx)
		for i, c := range t.Columns {
			oid := pgTypeOID(c.Type)
			dtName := pgTypeName(c.Type)
			isNullable := "YES"
			if c.PrimaryKey || !c.Nullable {
				isNullable = "NO"
			}
			var maxLen any = nil
			if c.Type == storage.TypeVarChar && c.Precision > 0 {
				maxLen = int64(c.Precision)
			}
			var numPrec, numScale any = nil, nil
			if c.Type == storage.TypeDecimal {
				numPrec = int64(c.Precision)
				numScale = int64(c.Scale)
			} else if c.Type == storage.TypeInt {
				numPrec = int64(32)
				numScale = int64(0)
			}
			allRows = append(allRows, map[string]any{
				"schema_name":              targetSchema,
				"pure_name":                t.Name,
				"table_name":               t.Name,
				"postgres_table_id":        toid,
				"column_name":              c.Name,
				"postgres_column_id":       int16(i + 1),
				"attnum":                   int16(i + 1),
				"atttypid":                 oid,
				"data_type":                dtName,
				"is_nullable":              isNullable,
				"char_max_length":          maxLen,
				"character_maximum_length": maxLen,
				"numeric_precision":        numPrec,
				"numeric_scale":            numScale,
				"default_value":            nil,
				"column_default":           nil,
				"column_comment":           nil,
			})
		}
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         allRows,
		RowsAffected: int64(len(allRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execHeidiSQLForeignKeySelect(sql string, start time.Time) (*QueryResult, error) {
	targetTable := ""
	tblRe := regexp.MustCompile(`(?is)src_tbl\.relname\s*=\s*'([^']+)'`)
	if m := tblRe.FindStringSubmatch(sql); len(m) == 2 {
		targetTable = m[1]
	}

	targetSchema := "public"
	nspRe := regexp.MustCompile(`(?is)src_ns\.nspname\s*=\s*'([^']+)'`)
	if m := nspRe.FindStringSubmatch(sql); len(m) == 2 {
		targetSchema = m[1]
	}

	columns := []string{
		"constraint_name",
		"update_rule",
		"delete_rule",
		"table_schema",
		"table_name",
		"columns",
		"ref_schema",
		"ref_table",
		"ref_columns",
		"ord_position",
	}

	var allRows []map[string]any
	tables, _ := s.engine.ListTables()

	for _, t := range tables {
		if targetTable != "" && !strings.EqualFold(t.Name, targetTable) {
			continue
		}
		for _, fk := range t.ForeignKeys {
			updRule := "NO ACTION"
			switch fk.OnUpdate {
			case storage.ActionCascade:
				updRule = "CASCADE"
			case storage.ActionSetNull:
				updRule = "SET NULL"
			case storage.ActionSetDefault:
				updRule = "SET DEFAULT"
			case storage.ActionRestrict:
				updRule = "RESTRICT"
			}

			delRule := "NO ACTION"
			switch fk.OnDelete {
			case storage.ActionCascade:
				delRule = "CASCADE"
			case storage.ActionSetNull:
				delRule = "SET NULL"
			case storage.ActionSetDefault:
				delRule = "SET DEFAULT"
			case storage.ActionRestrict:
				delRule = "RESTRICT"
			}

			var posList []string
			for p := 1; p <= len(fk.ChildColumns); p++ {
				posList = append(posList, fmt.Sprintf("%d", p))
			}

			allRows = append(allRows, map[string]any{
				"constraint_name": fk.Name,
				"update_rule":     updRule,
				"delete_rule":     delRule,
				"table_schema":    targetSchema,
				"table_name":      t.Name,
				"columns":         strings.Join(fk.ChildColumns, ","),
				"ref_schema":      targetSchema,
				"ref_table":       fk.ParentTable,
				"ref_columns":     strings.Join(fk.ParentColumns, ","),
				"ord_position":    strings.Join(posList, ","),
			})
		}
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         allRows,
		RowsAffected: int64(len(allRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execBeekeeperTableSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{"schema", "name", "tabletype", "parenttype"}
	var allRows []map[string]any

	targetSchema := ""
	schemaRe := regexp.MustCompile(`(?is)table_schema\s*=\s*'([^']+)'`)
	if m := schemaRe.FindStringSubmatch(sql); len(m) == 2 {
		targetSchema = m[1]
	}

	tables, _ := s.engine.ListTables()
	for _, t := range tables {
		schemas := []string{"public"}
		if targetSchema != "" {
			schemas = []string{targetSchema}
		}
		for _, sch := range schemas {
			allRows = append(allRows, map[string]any{
				"schema":     sch,
				"name":       t.Name,
				"tabletype":  "r",
				"parenttype": "r",
			})
		}
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         allRows,
		RowsAffected: int64(len(allRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execDBeaverPgShdescriptionSelect(sql string, start time.Time) (*QueryResult, error) {
	upper := strings.ToUpper(sql)
	if strings.Contains(upper, "PG_ROLES") {
		columns := []string{
			"oid", "rolname", "rolsuper", "rolinherit", "rolcreaterole",
			"rolcreatedb", "rolcanlogin", "rolreplication", "rolconnlimit",
			"rolpassword", "rolvaliduntil", "rolbypassrls", "rolconfig",
			"description",
		}
		allRows := []map[string]any{
			{
				"oid":            10,
				"rolname":        "postgres",
				"rolsuper":       true,
				"rolinherit":     true,
				"rolcreaterole":  true,
				"rolcreatedb":    true,
				"rolcanlogin":    true,
				"rolreplication": true,
				"rolconnlimit":   -1,
				"rolpassword":    "********",
				"rolvaliduntil":  nil,
				"rolbypassrls":   true,
				"rolconfig":      nil,
				"description":    nil,
			},
			{
				"oid":            11,
				"rolname":        "admin",
				"rolsuper":       true,
				"rolinherit":     true,
				"rolcreaterole":  true,
				"rolcreatedb":    true,
				"rolcanlogin":    true,
				"rolreplication": true,
				"rolconnlimit":   -1,
				"rolpassword":    "********",
				"rolvaliduntil":  nil,
				"rolbypassrls":   true,
				"rolconfig":      nil,
				"description":    nil,
			},
		}
		return buildVirtualCatalogResult(columns, allRows, "*", "", "rolname", -1, start)
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      []string{"description"},
		Rows:         []map[string]any{},
		RowsAffected: 0,
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execDBeaverPgConversionSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{"encid", "encname"}
	allRows := []map[string]any{
		{"encid": 6, "encname": "UTF8"},
		{"encid": 0, "encname": "SQL_ASCII"},
	}
	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         allRows,
		RowsAffected: int64(len(allRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

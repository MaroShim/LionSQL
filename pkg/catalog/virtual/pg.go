package virtual

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"sqlion/pkg/storage"
)

var (
	reGuiNspnameEq     = regexp.MustCompile(`(?is)n\.nspname\s*=\s*'([^']+)'`)
	reGuiRelnameEq     = regexp.MustCompile(`(?is)(?:pgc|c)\.relname\s*=\s*'([^']+)'`)
	reGuiSrcRelnameEq  = regexp.MustCompile(`(?is)src_tbl\.relname\s*=\s*'([^']+)'`)
	reGuiSrcNspnameEq  = regexp.MustCompile(`(?is)src_ns\.nspname\s*=\s*'([^']+)'`)
	reGuiTableSchemaEq = regexp.MustCompile(`(?is)table_schema\s*=\s*'([^']+)'`)
)

func tableBaseOID(idx int) int {
	return 16384 + idx
}

func pgTypeOID(t storage.DataType) int {
	switch t {
	case storage.TypeInt:
		return 23 // int4
	case storage.TypeFloat:
		return 701 // float8
	case storage.TypeDecimal:
		return 1700 // numeric
	case storage.TypeBool:
		return 16 // bool
	case storage.TypeDateTime:
		return 1114 // timestamp
	case storage.TypeDate:
		return 1082 // date
	case storage.TypeTime:
		return 1083 // time
	case storage.TypeUUID:
		return 2950 // uuid
	case storage.TypeBinary:
		return 17 // bytea
	case storage.TypeJSON:
		return 114 // json
	default:
		return 1043 // varchar
	}
}

func pgTypeName(t storage.DataType) string {
	switch t {
	case storage.TypeInt:
		return "int4"
	case storage.TypeFloat:
		return "float8"
	case storage.TypeDecimal:
		return "numeric"
	case storage.TypeBool:
		return "bool"
	case storage.TypeDateTime:
		return "timestamp"
	case storage.TypeDate:
		return "date"
	case storage.TypeTime:
		return "time"
	case storage.TypeUUID:
		return "uuid"
	case storage.TypeBinary:
		return "bytea"
	case storage.TypeJSON:
		return "json"
	default:
		return "varchar"
	}
}

// pgTypeLen maps a storage DataType to pg_type.typlen (-1 = varlena).
func pgTypeLen(t storage.DataType) int {
	switch t {
	case storage.TypeInt:
		return 4
	case storage.TypeFloat:
		return 8
	case storage.TypeBool:
		return 1
	case storage.TypeDateTime, storage.TypeDate, storage.TypeTime, storage.TypeDecimal:
		return 8
	case storage.TypeUUID:
		return 16
	default:
		return -1
	}
}

// pgTypeMod returns the atttypmod for a column (-1 = no modifier).
func pgTypeMod(t storage.DataType, maxLen int) int {
	if t == storage.TypeVarChar && maxLen > 0 {
		return maxLen + 4
	}
	return -1
}

func execDbGatePrimaryKeySelect(src Source, sql string) (*Result, error) {
	targetSchema := "public"
	if m := reGuiNspnameEq.FindStringSubmatch(sql); len(m) == 2 {
		targetSchema = m[1]
	}

	columns := []string{
		"constraint_schema", "constraint_name", "schema_name",
		"pure_name", "table_name", "column_name",
	}

	var allRows []map[string]any
	tables, _ := src.ListTables()
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

	return &Result{
		Columns: columns,
		Rows:    allRows,
	}, nil
}

func execDbGateIndexSelect(src Source, sql string) (*Result, error) {
	upper := strings.ToUpper(sql)
	targetSchema := "public"

	if strings.Contains(upper, "ATTNAME") && strings.Contains(upper, "ATTNUM") {
		columns := []string{"column_name", "attnum", "oid"}
		var allRows []map[string]any
		tables, _ := src.ListTables()
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
		return &Result{
			Columns: columns,
			Rows:    allRows,
		}, nil
	}

	columns := []string{
		"table_name", "pure_name", "schema_name", "index_name",
		"is_primary", "is_unique", "indkey", "indoption", "oid",
	}
	var allRows []map[string]any
	tables, _ := src.ListTables()
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
	return &Result{
		Columns: columns,
		Rows:    allRows,
	}, nil
}

func execDbGateTriggerSelect(src Source, sql string) (*Result, error) {
	columns := []string{
		"schema_name", "pure_name", "table_name", "trigger_name",
		"timing", "event", "action_statement",
	}
	return &Result{
		Columns: columns,
		Rows:    []map[string]any{},
	}, nil
}

func execProcSelect(src Source, sql string) (*Result, error) {
	upper := strings.ToUpper(sql)
	targetSchema := "public"
	if m := reGuiNspnameEq.FindStringSubmatch(sql); len(m) == 2 {
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
		"proname", "prosrc",
	}
	var allRows []map[string]any
	for _, p := range src.ListProcedures() {
		allRows = append(allRows, map[string]any{
			"schema_name":  targetSchema,
			"pure_name":    p.Name,
			"object_name":  p.Name,
			"table_name":   p.Name,
			"proname":      p.Name,
			"prosrc":       p.Body,
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
	for _, f := range src.ListFunctions() {
		allRows = append(allRows, map[string]any{
			"schema_name":  targetSchema,
			"pure_name":    f.Name,
			"object_name":  f.Name,
			"table_name":   f.Name,
			"proname":      f.Name,
			"prosrc":       f.Body,
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
	return FormatResult(columns, allRows, projStr, whereClause, "", -1)
}

func execClassSelect(src Source, sql string) (*Result, error) {
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
	tables := src.GetSortedTables()
	views := src.GetSortedViews()

	// If query targets views specifically (DbGate views.js, relkind = 'v', matviews)
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
		return FormatResult(columns, allRows, projStr, whereClause, "", -1)
	}

	// Standard tables query (DbGate tableList.js, DBeaver)
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

	return FormatResult(columns, allRows, projStr, whereClause, "", -1)
}

func execColumnMetadataSelect(src Source, sql string) (*Result, error) {
	targetTable := ""
	if m := reGuiRelnameEq.FindStringSubmatch(sql); len(m) == 2 {
		targetTable = m[1]
	}

	targetSchema := "public"
	if m := reGuiNspnameEq.FindStringSubmatch(sql); len(m) == 2 {
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
	tables, _ := src.ListTables()

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

	return &Result{
		Columns: columns,
		Rows:    allRows,
	}, nil
}

func execForeignKeySelect(src Source, sql string) (*Result, error) {
	targetTable := ""
	if m := reGuiSrcRelnameEq.FindStringSubmatch(sql); len(m) == 2 {
		targetTable = m[1]
	}

	targetSchema := "public"
	if m := reGuiSrcNspnameEq.FindStringSubmatch(sql); len(m) == 2 {
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
	tables, _ := src.ListTables()

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

	return &Result{
		Columns: columns,
		Rows:    allRows,
	}, nil
}

func execBeekeeperTableSelect(src Source, sql string) (*Result, error) {
	columns := []string{"schema", "name", "tabletype", "parenttype"}
	var allRows []map[string]any

	targetSchema := ""
	if m := reGuiTableSchemaEq.FindStringSubmatch(sql); len(m) == 2 {
		targetSchema = m[1]
	}

	tables, _ := src.ListTables()
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

	return &Result{
		Columns: columns,
		Rows:    allRows,
	}, nil
}

func execPgShdescriptionSelect(sql string) (*Result, error) {
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
		return FormatResult(columns, allRows, "*", "", "rolname", -1)
	}

	return &Result{
		Columns: []string{"description"},
		Rows:    []map[string]any{},
	}, nil
}

func execPgConversionSelect(sql string) (*Result, error) {
	columns := []string{"encid", "encname"}
	allRows := []map[string]any{
		{"encid": 6, "encname": "UTF8"},
		{"encid": 0, "encname": "SQL_ASCII"},
	}
	return &Result{
		Columns: columns,
		Rows:    allRows,
	}, nil
}

// VirtualCatalogView defines a generator function that builds the virtual resultset for a catalog table.
type VirtualCatalogView func(src Source, whereClause string) (columns []string, rows []map[string]any)

var catalogViewRegistry = make(map[string]VirtualCatalogView)

func registerCatalogView(name string, view VirtualCatalogView) {
	catalogViewRegistry[strings.ToLower(strings.TrimSpace(name))] = view
}

// IsPgCatalogTable reports whether tableName identifies a registered pg_catalog or information_schema virtual view.
func IsPgCatalogTable(tableName string) bool {
	_, ok := catalogViewRegistry[strings.ToLower(strings.TrimSpace(tableName))]
	return ok
}

// ExecPgCatalogSelect handles SELECT queries against registered pg_catalog.* and information_schema.* virtual views.
func ExecPgCatalogSelect(src Source, tableName, projStr, whereClause, orderByClause string, limitCount int) (*Result, error) {
	norm := strings.ToLower(strings.TrimSpace(tableName))
	view, ok := catalogViewRegistry[norm]
	if !ok {
		return nil, fmt.Errorf("unknown pg_catalog or information_schema view: %s", tableName)
	}
	columns, allRows := view(src, whereClause)
	return FormatResult(columns, allRows, projStr, whereClause, orderByClause, limitCount)
}

func init() {
	regPG := func(name string, gen VirtualCatalogView) {
		registerCatalogView("pg_catalog."+name, gen)
		registerCatalogView(name, gen)
	}

	regPG("pg_database", generatePgDatabase)
	regPG("pg_namespace", generatePgNamespace)
	regPG("pg_class", generatePgClass)
	regPG("pg_tables", generatePgTables)
	regPG("pg_attribute", generatePgAttribute)
	regPG("pg_type", generatePgType)
	regPG("pg_constraint", generatePgConstraint)
	regPG("pg_index", generatePgIndex)
	regPG("pg_roles", generatePgRoles)
	regPG("pg_user", generatePgUser)
	regPG("pg_am", generatePgAm)
	regPG("pg_settings", generatePgSettings)
	regPG("pg_description", generatePgDescription)
	regPG("pg_shdescription", generatePgShdescription)
	regPG("pg_tablespace", generatePgTablespace)
	regPG("pg_collation", generatePgCollation)
	regPG("pg_conversion", generatePgConversion)
	regPG("pg_proc", generatePgProc)
	regPG("pg_stat_user_tables", generatePgStatUserTables)
	regPG("pg_stat_user_indexes", generatePgStatUserIndexes)
	regPG("pg_statio_user_tables", generatePgStatioUserTables)
	regPG("pg_locks", generatePgLocks)
	regPG("pg_stat_activity", generatePgStatActivity)
	regPG("pg_sequences", generatePgSequences)
	regPG("pg_views", generatePgViews)
	regPG("pg_matviews", generatePgMatviews)
	regPG("pg_aggregate", generatePgAggregate)
	regPG("pg_operator", generatePgOperator)
	regPG("pg_depend", generatePgDepend)
	regPG("pg_inherits", generatePgInherits)
	regPG("pg_trigger", generatePgTrigger)
	regPG("pg_range", generatePgRange)
	regPG("pg_enum", generatePgEnum)
	regPG("pg_attrdef", generatePgAttrdef)
	regPG("pg_partitioned_table", generatePgPartitionedTable)
	regPG("pg_extension", generatePgExtension)
	regPG("pg_available_extensions", generatePgAvailableExtensions)
}

func generatePgDatabase(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"oid", "datname", "datdba", "encoding", "datcollate", "datctype",
		"datistemplate", "datallowconn", "datconnlimit", "datlastsysoid",
		"datfrozenxid", "datminmxid", "dattablespace", "datacl"}
	allRows := []map[string]any{
		{
			"oid":           1,
			"datname":       "postgres",
			"datdba":        10,
			"encoding":      6,
			"datcollate":    "en_US.utf8",
			"datctype":      "en_US.utf8",
			"datistemplate": false,
			"datallowconn":  true,
			"datconnlimit":  -1,
			"datlastsysoid": 14000,
			"datfrozenxid":  726,
			"datminmxid":    1,
			"dattablespace": 1663,
			"datacl":        nil,
		},
	}
	return columns, allRows
}

func generatePgNamespace(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"oid", "nspname", "nspowner", "nspacl"}
	allRows := []map[string]any{
		{"oid": 11, "nspname": "pg_catalog", "nspowner": 10, "nspacl": nil},
		{"oid": 12, "nspname": "information_schema", "nspowner": 10, "nspacl": nil},
		{"oid": 2200, "nspname": "public", "nspowner": 10, "nspacl": nil},
		{"oid": 2201, "nspname": "dbo", "nspowner": 10, "nspacl": nil},
	}
	return columns, allRows
}

func generatePgClass(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"oid", "relname", "relnamespace", "reltype", "reloftype",
		"relowner", "relam", "relfilenode", "reltablespace",
		"relpages", "reltuples", "relallvisible", "reltoastrelid",
		"relhasindex", "relisshared", "relpersistence", "relkind",
		"relnatts", "relchecks", "relhasrules", "relhastriggers",
		"relhassubclass", "relrowsecurity", "relforcerowsecurity",
		"relispopulated", "relreplident", "relispartition",
		"relrewrite", "relfrozenxid", "relminmxid", "relacl", "reloptions",
	}
	tables, _ := src.ListTables()
	var allRows []map[string]any
	for i, t := range tables {
		oid := tableBaseOID(i)
		allRows = append(allRows, map[string]any{
			"oid":                 oid,
			"relname":             t.Name,
			"relnamespace":        2200,
			"reltype":             0,
			"reloftype":           0,
			"relowner":            10,
			"relam":               2, // heap
			"relfilenode":         oid,
			"reltablespace":       1663,
			"relpages":            int64(10),
			"reltuples":           float64(1000),
			"relallvisible":       int64(0),
			"reltoastrelid":       0,
			"relhasindex":         len(t.Indexes) > 0,
			"relisshared":         false,
			"relpersistence":      "p",
			"relkind":             "r",
			"relnatts":            int16(len(t.Columns)),
			"relchecks":           int16(0),
			"relhasrules":         false,
			"relhastriggers":      false,
			"relhassubclass":      false,
			"relrowsecurity":      false,
			"relforcerowsecurity": false,
			"relispopulated":      true,
			"relreplident":        "d",
			"relispartition":      false,
			"relrewrite":          0,
			"relfrozenxid":        726,
			"relminmxid":          1,
			"relacl":              nil,
			"reloptions":          nil,
		})
	}
	return columns, allRows
}

func generatePgTables(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"schemaname", "tablename", "tableowner", "tablespace",
		"hasindexes", "hasrules", "hastriggers", "rowsecurity"}
	tables, _ := src.ListTables()
	var allRows []map[string]any
	for _, t := range tables {
		allRows = append(allRows, map[string]any{
			"schemaname":  "public",
			"tablename":   t.Name,
			"tableowner":  "postgres",
			"tablespace":  nil,
			"hasindexes":  len(t.Indexes) > 0,
			"hasrules":    false,
			"hastriggers": false,
			"rowsecurity": false,
		})
	}
	return columns, allRows
}

func generatePgAttribute(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"attrelid", "attname", "atttypid", "attstattarget", "attlen",
		"attnum", "attndims", "attcacheoff", "atttypmod", "attbyval",
		"attalign", "attstorage", "attcompression", "attnotnull",
		"atthasdef", "atthasmissing", "attidentity", "attgenerated",
		"attisdropped", "attislocal", "attinhcount", "attcollation",
		"attacl", "attoptions", "attfdwoptions", "attmissingval",
	}
	tables, _ := src.ListTables()
	var allRows []map[string]any
	for tIdx, t := range tables {
		toid := tableBaseOID(tIdx)
		for cIdx, c := range t.Columns {
			oid := pgTypeOID(c.Type)
			allRows = append(allRows, map[string]any{
				"attrelid":       toid,
				"attname":        c.Name,
				"atttypid":       oid,
				"attstattarget":  -1,
				"attlen":         pgTypeLen(c.Type),
				"attnum":         int16(cIdx + 1),
				"attndims":       0,
				"attcacheoff":    -1,
				"atttypmod":      pgTypeMod(c.Type, c.Precision),
				"attbyval":       false,
				"attalign":       "i",
				"attstorage":     "x",
				"attcompression": "",
				"attnotnull":     !c.Nullable,
				"atthasdef":      false,
				"atthasmissing":  false,
				"attidentity":    "",
				"attgenerated":   "",
				"attisdropped":   false,
				"attislocal":     true,
				"attinhcount":    0,
				"attcollation":   0,
				"attacl":         nil,
				"attoptions":     nil,
				"attfdwoptions":  nil,
				"attmissingval":  nil,
			})
		}
	}
	return columns, allRows
}

func generatePgType(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"oid", "typname", "typnamespace", "typowner", "typlen",
		"typbyval", "typtype", "typcategory", "typispreferred",
		"typisdefined", "typdelim", "typrelid", "typsubscript",
		"typelem", "typarray", "typinput", "typoutput",
		"typreceive", "typsend", "typmodin", "typmodout",
		"typanalyze", "typalign", "typstorage", "typnotnull",
		"typbasetype", "typtypmod", "typndims", "typcollation",
		"typdefaultbin", "typdefault", "typacl",
	}
	baseTypes := []struct {
		oid      int
		name     string
		length   int
		category string
	}{
		{16, "bool", 1, "B"},
		{17, "bytea", -1, "U"},
		{20, "int8", 8, "N"},
		{21, "int2", 2, "N"},
		{23, "int4", 4, "N"},
		{25, "text", -1, "S"},
		{26, "oid", 4, "N"},
		{114, "json", -1, "U"},
		{700, "float4", 4, "N"},
		{701, "float8", 8, "N"},
		{1042, "bpchar", -1, "S"},
		{1043, "varchar", -1, "S"},
		{1082, "date", 4, "D"},
		{1083, "time", 8, "D"},
		{1114, "timestamp", 8, "D"},
		{1184, "timestamptz", 8, "D"},
		{1700, "numeric", -1, "N"},
		{2950, "uuid", 16, "U"},
		{3802, "jsonb", -1, "U"},
	}
	var allRows []map[string]any
	for _, bt := range baseTypes {
		allRows = append(allRows, map[string]any{
			"oid":            bt.oid,
			"typname":        bt.name,
			"typnamespace":   11,
			"typowner":       10,
			"typlen":         bt.length,
			"typbyval":       bt.length > 0 && bt.length <= 8,
			"typtype":        "b",
			"typcategory":    bt.category,
			"typispreferred": false,
			"typisdefined":   true,
			"typdelim":       ",",
			"typrelid":       0,
			"typsubscript":   "-",
			"typelem":        0,
			"typarray":       0,
			"typinput":       bt.name + "in",
			"typoutput":      bt.name + "out",
			"typreceive":     bt.name + "recv",
			"typsend":        bt.name + "send",
			"typmodin":       "-",
			"typmodout":      "-",
			"typanalyze":     "-",
			"typalign":       "i",
			"typstorage":     "x",
			"typnotnull":     false,
			"typbasetype":    0,
			"typtypmod":      -1,
			"typndims":       0,
			"typcollation":   0,
			"typdefaultbin":  nil,
			"typdefault":     nil,
			"typacl":         nil,
		})
	}
	return columns, allRows
}

func generatePgConstraint(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"oid", "conname", "connamespace", "contype", "condeferrable",
		"condeferred", "convalidated", "conrelid", "contypid",
		"conindid", "conparentid", "confrelid", "confupdtype",
		"confdeltype", "confmatchtype", "conislocal", "coninhcount",
		"connoinherit", "conkey", "confkey", "conpfeqop", "conppeqop",
		"conffeqop", "conexclop", "conbin",
	}
	tables, _ := src.ListTables()
	var allRows []map[string]any
	conOID := 50000
	for tIdx, t := range tables {
		toid := tableBaseOID(tIdx)
		// PRIMARY KEY constraint
		var pkCols []int16
		for cIdx, c := range t.Columns {
			if c.PrimaryKey {
				pkCols = append(pkCols, int16(cIdx+1))
			}
		}
		if len(pkCols) > 0 {
			allRows = append(allRows, map[string]any{
				"oid":           conOID,
				"conname":       fmt.Sprintf("%s_pkey", t.Name),
				"connamespace":  2200,
				"contype":       "p",
				"condeferrable": false,
				"condeferred":   false,
				"convalidated":  true,
				"conrelid":      toid,
				"contypid":      0,
				"conindid":      toid + 100,
				"conparentid":   0,
				"confrelid":     0,
				"confupdtype":   " ",
				"confdeltype":   " ",
				"confmatchtype": " ",
				"conislocal":    true,
				"coninhcount":   0,
				"connoinherit":  true,
				"conkey":        pkCols,
				"confkey":       nil,
				"conpfeqop":     nil,
				"conppeqop":     nil,
				"conffeqop":     nil,
				"conexclop":     nil,
				"conbin":        nil,
			})
			conOID++
		}
		// UNIQUE INDEX constraints
		for _, idx := range t.Indexes {
			if !idx.Unique {
				continue
			}
			var ukCols []int16
			for _, idxCol := range idx.Columns {
				for cIdx, c := range t.Columns {
					if strings.EqualFold(c.Name, idxCol) {
						ukCols = append(ukCols, int16(cIdx+1))
						break
					}
				}
			}
			allRows = append(allRows, map[string]any{
				"oid":           conOID,
				"conname":       idx.Name,
				"connamespace":  2200,
				"contype":       "u",
				"condeferrable": false,
				"condeferred":   false,
				"convalidated":  true,
				"conrelid":      toid,
				"contypid":      0,
				"conindid":      toid + 200,
				"conparentid":   0,
				"confrelid":     0,
				"confupdtype":   " ",
				"confdeltype":   " ",
				"confmatchtype": " ",
				"conislocal":    true,
				"coninhcount":   0,
				"connoinherit":  true,
				"conkey":        ukCols,
				"confkey":       nil,
				"conpfeqop":     nil,
				"conppeqop":     nil,
				"conffeqop":     nil,
				"conexclop":     nil,
				"conbin":        nil,
			})
			conOID++
		}
		// FOREIGN KEY constraints
		for _, fk := range t.ForeignKeys {
			refOID := 0
			for rIdx, rt := range tables {
				if strings.EqualFold(rt.Name, fk.ParentTable) {
					refOID = tableBaseOID(rIdx)
					break
				}
			}
			var fkCols, fkRefCols []int16
			for _, fkCol := range fk.ChildColumns {
				for cIdx, c := range t.Columns {
					if strings.EqualFold(c.Name, fkCol) {
						fkCols = append(fkCols, int16(cIdx+1))
						break
					}
				}
			}
			for _, refCol := range fk.ParentColumns {
				for rIdx, rt := range tables {
					if strings.EqualFold(rt.Name, fk.ParentTable) {
						for cIdx, c := range rt.Columns {
							if strings.EqualFold(c.Name, refCol) {
								_ = rIdx
								fkRefCols = append(fkRefCols, int16(cIdx+1))
								break
							}
						}
						break
					}
				}
			}
			allRows = append(allRows, map[string]any{
				"oid":           conOID,
				"conname":       fk.Name,
				"connamespace":  2200,
				"contype":       "f",
				"condeferrable": false,
				"condeferred":   false,
				"convalidated":  true,
				"conrelid":      toid,
				"contypid":      0,
				"conindid":      0,
				"conparentid":   0,
				"confrelid":     refOID,
				"confupdtype":   "a",
				"confdeltype":   "a",
				"confmatchtype": "s",
				"conislocal":    true,
				"coninhcount":   0,
				"connoinherit":  false,
				"conkey":        fkCols,
				"confkey":       fkRefCols,
				"conpfeqop":     nil,
				"conppeqop":     nil,
				"conffeqop":     nil,
				"conexclop":     nil,
				"conbin":        nil,
			})
			conOID++
		}
	}
	return columns, allRows
}

func generatePgIndex(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"indexrelid", "indrelid", "indnatts", "indnkeyatts",
		"indisunique", "indisprimary", "indisexclusion", "indimmediate",
		"indisclustered", "indisvalid", "indcheckxmin", "indisready",
		"indislive", "indisreplident", "indkey", "indcollation",
		"indclass", "indoption", "indexprs", "indpred",
	}
	tables, _ := src.ListTables()
	var allRows []map[string]any
	idxOID := 20000
	for tIdx, t := range tables {
		toid := tableBaseOID(tIdx)
		// Implicit PK index
		var pkCols []int16
		for cIdx, c := range t.Columns {
			if c.PrimaryKey {
				pkCols = append(pkCols, int16(cIdx+1))
			}
		}
		if len(pkCols) > 0 {
			allRows = append(allRows, map[string]any{
				"indexrelid":     toid + 100,
				"indrelid":       toid,
				"indnatts":       int16(len(pkCols)),
				"indnkeyatts":    int16(len(pkCols)),
				"indisunique":    true,
				"indisprimary":   true,
				"indisexclusion": false,
				"indimmediate":   true,
				"indisclustered": false,
				"indisvalid":     true,
				"indcheckxmin":   false,
				"indisready":     true,
				"indislive":      true,
				"indisreplident": false,
				"indkey":         pkCols,
				"indcollation":   nil,
				"indclass":       nil,
				"indoption":      nil,
				"indexprs":       nil,
				"indpred":        nil,
			})
			idxOID++
		}
		// Explicit indexes
		for _, idx := range t.Indexes {
			var idxCols []int16
			for _, idxCol := range idx.Columns {
				for cIdx, c := range t.Columns {
					if strings.EqualFold(c.Name, idxCol) {
						idxCols = append(idxCols, int16(cIdx+1))
						break
					}
				}
			}
			allRows = append(allRows, map[string]any{
				"indexrelid":     idxOID,
				"indrelid":       toid,
				"indnatts":       int16(len(idxCols)),
				"indnkeyatts":    int16(len(idxCols)),
				"indisunique":    idx.Unique,
				"indisprimary":   false,
				"indisexclusion": false,
				"indimmediate":   true,
				"indisclustered": false,
				"indisvalid":     true,
				"indcheckxmin":   false,
				"indisready":     true,
				"indislive":      true,
				"indisreplident": false,
				"indkey":         idxCols,
				"indcollation":   nil,
				"indclass":       nil,
				"indoption":      nil,
				"indexprs":       nil,
				"indpred":        nil,
			})
			idxOID++
		}
	}
	return columns, allRows
}

func generatePgRoles(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"oid", "rolname", "rolsuper", "rolinherit", "rolcreaterole",
		"rolcreatedb", "rolcanlogin", "rolreplication", "rolconnlimit",
		"rolpassword", "rolvaliduntil", "rolbypassrls", "rolconfig",
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
		},
	}
	return columns, allRows
}

func generatePgUser(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"usename", "usesysid", "usecreatedb", "usesuper", "userepl",
		"usebypassrls", "passwd", "valuntil", "useconfig",
	}
	allRows := []map[string]any{
		{
			"usename":      "postgres",
			"usesysid":     10,
			"usecreatedb":  true,
			"usesuper":     true,
			"userepl":      true,
			"usebypassrls": true,
			"passwd":       "********",
			"valuntil":     nil,
			"useconfig":    nil,
		},
	}
	return columns, allRows
}

func generatePgAm(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"oid", "amname", "amhandler", "amtype"}
	allRows := []map[string]any{
		{"oid": 403, "amname": "btree", "amhandler": "bthandler", "amtype": "i"},
		{"oid": 405, "amname": "hash", "amhandler": "hashhandler", "amtype": "i"},
		{"oid": 783, "amname": "gist", "amhandler": "gisthandler", "amtype": "i"},
		{"oid": 2742, "amname": "gin", "amhandler": "ginhandler", "amtype": "i"},
	}
	return columns, allRows
}

func generatePgSettings(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"name", "setting", "unit", "category", "short_desc",
		"extra_desc", "context", "vartype", "source",
		"min_val", "max_val", "enumvals", "boot_val", "reset_val",
		"sourcefile", "sourceline", "pending_restart",
	}
	settings := []struct {
		name     string
		setting  string
		category string
		desc     string
		vartype  string
	}{
		{"server_version", "15.0 (LionSQL)", "Preset Options", "Shows the server version.", "string"},
		{"server_version_num", "150000", "Preset Options", "Shows the server version as an integer.", "integer"},
		{"client_encoding", "UTF8", "Client Connection Defaults / Locale and Formatting", "Sets the client's character set encoding.", "string"},
		{"server_encoding", "UTF8", "Client Connection Defaults / Locale and Formatting", "Shows the server (database) character set encoding.", "string"},
		{"TimeZone", "UTC", "Client Connection Defaults / Locale and Formatting", "Sets the time zone for displaying and interpreting time stamps.", "string"},
		{"DateStyle", "ISO, MDY", "Client Connection Defaults / Locale and Formatting", "Sets the display format for date and time values.", "string"},
		{"IntervalStyle", "postgres", "Client Connection Defaults / Locale and Formatting", "Sets the display format for interval values.", "string"},
		{"standard_conforming_strings", "on", "Version and Platform Compatibility / Previous PostgreSQL Versions", "Causes ... to be treated literally in strings.", "bool"},
		{"integer_datetimes", "on", "Preset Options", "Datetimes are integer based.", "bool"},
		{"max_connections", "100", "Connections and Authentication / Connection Settings", "Sets the maximum number of concurrent connections.", "integer"},
		{"work_mem", "4MB", "Resource Usage / Memory", "Sets the maximum memory to be used for query workspaces.", "integer"},
		{"search_path", "\"$user\", public", "Client Connection Defaults / Statement Behavior", "Sets the schema search order for names that are not schema-qualified.", "string"},
	}
	var allRows []map[string]any
	for _, st := range settings {
		allRows = append(allRows, map[string]any{
			"name":            st.name,
			"setting":         st.setting,
			"unit":            nil,
			"category":        st.category,
			"short_desc":      st.desc,
			"extra_desc":      nil,
			"context":         "user",
			"vartype":         st.vartype,
			"source":          "default",
			"min_val":         nil,
			"max_val":         nil,
			"enumvals":        nil,
			"boot_val":        st.setting,
			"reset_val":       st.setting,
			"sourcefile":      nil,
			"sourceline":      nil,
			"pending_restart": false,
		})
	}
	return columns, allRows
}

func generatePgDescription(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"objoid", "classoid", "objsubid", "description"}
	return columns, []map[string]any{}
}

func generatePgShdescription(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"objoid", "classoid", "description"}
	return columns, []map[string]any{}
}

func generatePgTablespace(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"oid", "spcname", "spcowner", "spcacl", "spcoptions"}
	allRows := []map[string]any{
		{"oid": 1663, "spcname": "pg_default", "spcowner": 10, "spcacl": nil, "spcoptions": nil},
		{"oid": 1664, "spcname": "pg_global", "spcowner": 10, "spcacl": nil, "spcoptions": nil},
	}
	return columns, allRows
}

func generatePgCollation(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"oid", "collname", "collnamespace", "collowner", "collprovider", "collisdeterministic", "collencoding", "collcollate", "collctype", "collversion"}
	allRows := []map[string]any{
		{"oid": 100, "collname": "default", "collnamespace": 11, "collowner": 10, "collprovider": "c", "collisdeterministic": true, "collencoding": 6, "collcollate": "en_US.utf8", "collctype": "en_US.utf8", "collversion": nil},
	}
	return columns, allRows
}

func generatePgConversion(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"oid", "conname", "connamespace", "conowner", "conforencoding", "contoencoding", "conproc", "condefault"}
	allRows := []map[string]any{
		{"oid": 1000, "conname": "ascii_to_utf8", "connamespace": 11, "conowner": 10, "conforencoding": 0, "contoencoding": 6, "conproc": 0, "condefault": true},
		{"oid": 1001, "conname": "utf8_to_ascii", "connamespace": 11, "conowner": 10, "conforencoding": 6, "contoencoding": 0, "conproc": 0, "condefault": true},
	}
	return columns, allRows
}

func generatePgProc(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"oid", "proname", "pronamespace", "proowner", "prolang",
		"procost", "prorows", "provariadic", "prosupport",
		"prokind", "prosecdef", "proleakproof", "proisstrict",
		"proretset", "provolatile", "proparallel", "pronargs",
		"pronargdefaults", "prorettype", "proargtypes",
		"proallargtypes", "proargmodes", "proargnames",
		"proargdefaults", "protrftypes", "prosrc", "probin",
		"proconfig", "proacl",
	}
	var allRows []map[string]any
	procOID := 30000
	for _, p := range src.ListProcedures() {
		allRows = append(allRows, map[string]any{
			"oid":             procOID,
			"proname":         p.Name,
			"pronamespace":    2200,
			"proowner":        10,
			"prolang":         14,
			"procost":         float32(100),
			"prorows":         float32(0),
			"provariadic":     0,
			"prosupport":      "-",
			"prokind":         "p",
			"prosecdef":       false,
			"proleakproof":    false,
			"proisstrict":     false,
			"proretset":       false,
			"provolatile":     "v",
			"proparallel":     "u",
			"pronargs":        int16(len(p.Params)),
			"pronargdefaults": int16(0),
			"prorettype":      2278, // void
			"proargtypes":     nil,
			"proallargtypes":  nil,
			"proargmodes":     nil,
			"proargnames":     nil,
			"proargdefaults":  nil,
			"protrftypes":     nil,
			"prosrc":          p.Body,
			"probin":          nil,
			"proconfig":       nil,
			"proacl":          nil,
		})
		procOID++
	}
	return columns, allRows
}

func generatePgStatUserTables(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"relid", "schemaname", "relname",
		"seq_scan", "seq_tup_read", "idx_scan", "idx_tup_fetch",
		"n_tup_ins", "n_tup_upd", "n_tup_del", "n_tup_hot_upd",
		"n_live_tup", "n_dead_tup", "n_mod_since_analyze",
		"n_ins_since_vacuum", "last_vacuum", "last_autovacuum",
		"last_analyze", "last_autoanalyze",
		"vacuum_count", "autovacuum_count",
		"analyze_count", "autoanalyze_count",
	}
	tables, _ := src.ListTables()
	var allRows []map[string]any
	for i, t := range tables {
		allRows = append(allRows, map[string]any{
			"relid":               tableBaseOID(i),
			"schemaname":          "public",
			"relname":             t.Name,
			"seq_scan":            int64(0),
			"seq_tup_read":        int64(0),
			"idx_scan":            int64(0),
			"idx_tup_fetch":       int64(0),
			"n_tup_ins":           int64(0),
			"n_tup_upd":           int64(0),
			"n_tup_del":           int64(0),
			"n_tup_hot_upd":       int64(0),
			"n_live_tup":          int64(0),
			"n_dead_tup":          int64(0),
			"n_mod_since_analyze": int64(0),
			"n_ins_since_vacuum":  int64(0),
			"last_vacuum":         nil,
			"last_autovacuum":     nil,
			"last_analyze":        nil,
			"last_autoanalyze":    nil,
			"vacuum_count":        int64(0),
			"autovacuum_count":    int64(0),
			"analyze_count":       int64(0),
			"autoanalyze_count":   int64(0),
		})
	}
	return columns, allRows
}

func generatePgStatUserIndexes(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"relid", "indexrelid", "schemaname", "relname", "indexrelname",
		"idx_scan", "idx_tup_read", "idx_tup_fetch"}
	tables, _ := src.ListTables()
	var allRows []map[string]any
	idxOID := 20000
	for i, t := range tables {
		toid := tableBaseOID(i)
		for _, idx := range t.Indexes {
			allRows = append(allRows, map[string]any{
				"relid":         toid,
				"indexrelid":    idxOID,
				"schemaname":    "public",
				"relname":       t.Name,
				"indexrelname":  idx.Name,
				"idx_scan":      int64(0),
				"idx_tup_read":  int64(0),
				"idx_tup_fetch": int64(0),
			})
			idxOID++
		}
	}
	return columns, allRows
}

func generatePgStatioUserTables(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"relid", "schemaname", "relname",
		"heap_blks_read", "heap_blks_hit",
		"idx_blks_read", "idx_blks_hit",
		"toast_blks_read", "toast_blks_hit",
		"tidx_blks_read", "tidx_blks_hit",
	}
	tables, _ := src.ListTables()
	var allRows []map[string]any
	for i, t := range tables {
		allRows = append(allRows, map[string]any{
			"relid":           tableBaseOID(i),
			"schemaname":      "public",
			"relname":         t.Name,
			"heap_blks_read":  int64(0),
			"heap_blks_hit":   int64(0),
			"idx_blks_read":   int64(0),
			"idx_blks_hit":    int64(0),
			"toast_blks_read": int64(0),
			"toast_blks_hit":  int64(0),
			"tidx_blks_read":  int64(0),
			"tidx_blks_hit":   int64(0),
		})
	}
	return columns, allRows
}

func generatePgLocks(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"locktype", "database", "relation", "page", "tuple",
		"virtualxid", "transactionid", "classid", "objid",
		"objsubid", "virtualtransaction", "pid", "mode", "granted", "fastpath",
	}
	return columns, []map[string]any{}
}

func generatePgStatActivity(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"datid", "datname", "pid", "leader_pid", "usesysid", "usename",
		"application_name", "client_addr", "client_hostname", "client_port",
		"backend_start", "xact_start", "query_start", "state_change",
		"wait_event_type", "wait_event", "state", "backend_xid",
		"backend_xmin", "query", "backend_type",
	}
	allRows := []map[string]any{
		{
			"datid":            1,
			"datname":          "postgres",
			"pid":              1,
			"leader_pid":       nil,
			"usesysid":         10,
			"usename":          "postgres",
			"application_name": "lionsql",
			"client_addr":      nil,
			"client_hostname":  nil,
			"client_port":      -1,
			"backend_start":    time.Now().Format("2006-01-02 15:04:05"),
			"xact_start":       nil,
			"query_start":      nil,
			"state_change":     nil,
			"wait_event_type":  nil,
			"wait_event":       nil,
			"state":            "active",
			"backend_xid":      nil,
			"backend_xmin":     nil,
			"query":            "",
			"backend_type":     "client backend",
		},
	}
	return columns, allRows
}

func generatePgSequences(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{
		"schemaname", "sequencename", "sequenceowner",
		"data_type", "start_value", "min_value", "max_value",
		"increment_by", "cycle", "cache_size", "last_value",
	}
	return columns, []map[string]any{}
}

func generatePgViews(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"schemaname", "viewname", "viewowner", "definition"}
	var allRows []map[string]any
	for _, v := range src.ListViews() {
		allRows = append(allRows, map[string]any{
			"schemaname": "public",
			"viewname":   v.Name,
			"viewowner":  "postgres",
			"definition": v.Query,
		})
	}
	return columns, allRows
}

func generatePgMatviews(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"schemaname", "matviewname", "matviewowner",
		"tablespace", "hasindexes", "ispopulated", "definition"}
	return columns, []map[string]any{}
}

func generatePgAggregate(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"aggfnoid", "aggkind", "aggnumdirectargs", "aggtransfn",
		"aggfinalfn", "aggcombinefn", "aggserialfn", "aggdeserialfn",
		"aggmtransfn", "aggminvtransfn", "aggmfinalfn",
		"aggfinalextra", "aggmfinalextra", "aggfinalmodify",
		"aggmfinalmodify", "aggsortop", "aggtranstype",
		"aggtransspace", "aggmtranstype", "aggmtransspace",
		"agginitval", "aggminitval"}
	return columns, []map[string]any{}
}

func generatePgOperator(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"oid", "oprname", "oprnamespace", "oprowner",
		"oprkind", "oprcanmerge", "oprcanhash",
		"oprleft", "oprright", "oprresult", "oprcom",
		"oprnegate", "oprcode", "oprrest", "oprjoin"}
	return columns, []map[string]any{}
}

func generatePgDepend(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"classid", "objid", "objsubid", "refclassid",
		"refobjid", "refobjsubid", "deptype"}
	return columns, []map[string]any{}
}

func generatePgInherits(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"inhrelid", "inhparent", "inhseqno", "inhdetachpending"}
	return columns, []map[string]any{}
}

func generatePgTrigger(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"oid", "tgrelid", "tgparentid", "tgname", "tgfoid",
		"tgtype", "tgenabled", "tgisinternal", "tgconstrrelid",
		"tgconstrindid", "tgconstraint", "tgdeferrable",
		"tginitdeferred", "tgnargs", "tgattr", "tgargs",
		"tgqual", "tgoldtable", "tgnewtable"}
	return columns, []map[string]any{}
}

func generatePgRange(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"rngtypid", "rngsubtype", "rngmultitypid",
		"rngcollation", "rngsubopc", "rngcanonical", "rngsubdiff"}
	return columns, []map[string]any{}
}

func generatePgEnum(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"oid", "enumtypid", "enumsortorder", "enumlabel"}
	return columns, []map[string]any{}
}

func generatePgAttrdef(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"oid", "adrelid", "adnum", "adbin"}
	return columns, []map[string]any{}
}

func generatePgPartitionedTable(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"partrelid", "partstrat", "partnatts", "partdefid",
		"partattrs", "partclass", "partcollation", "partexprs"}
	return columns, []map[string]any{}
}

func generatePgExtension(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"oid", "extname", "extowner", "extnamespace",
		"extrelocatable", "extversion", "extconfig", "extcondition"}
	return columns, []map[string]any{}
}

func generatePgAvailableExtensions(src Source, whereClause string) ([]string, []map[string]any) {
	columns := []string{"name", "default_version", "installed_version", "comment"}
	return columns, []map[string]any{}
}

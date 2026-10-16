package virtual

import (
	"fmt"
	"regexp"
	"strings"

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

package virtual

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/storage"
)

var (
	reMssqlObjectIdIn       = regexp.MustCompile(`(?is)(?:[a-z0-9_]+\.)?object_id\s+in\s*\(([^)]+)\)`)
	reMssqlObjectIDEq       = regexp.MustCompile(`(?is)(?:[a-z0-9_]+\.)?object_id\s*=\s*['"]?(\d+)['"]?`)
	reMssqlObjectIdCallEq   = regexp.MustCompile(`(?is)(?:[a-z0-9_]+\.)?object_id\s*=\s*(?:[a-z0-9_]+\.)*object_id\s*\(\s*N?'([^']+)'(?:\s*,\s*N?'[^']+')?\s*\)`)
	reMssqlParentObjectIdIn = regexp.MustCompile(`(?is)(?:[a-z0-9_]+\.)?parent_object_id\s+in\s*\(([^)]+)\)`)
	reMssqlTableNameEq      = regexp.MustCompile(`(?is)(?:table_name|c\.name|t\.name|ic\.table_name|\[?t\]?\.\[?name\]?|\[?c\]?\.\[?name\]?|\[?table_name\]?)\s*=\s*N?'([^']+)'`)
	reMssqlSchemaNameEq     = regexp.MustCompile(`(?is)(?:table_schema|s\.name|ic\.table_schema|\[?s\]?\.\[?name\]?|\[?table_schema\]?)\s*=\s*N?'([^']+)'`)
)

func cleanTargetTableName(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, "[]\"'`")
	if idx := strings.LastIndex(s, "."); idx != -1 {
		s = s[idx+1:]
	}
	return strings.Trim(strings.TrimSpace(s), "[]\"'`")
}

func parseTargetObjectIDs(sql string, src Source) map[int]bool {
	if m := reMssqlObjectIdIn.FindStringSubmatch(sql); len(m) == 2 {
		res := make(map[int]bool)
		for _, part := range strings.Split(m[1], ",") {
			cleaned := strings.Trim(strings.TrimSpace(part), "'\" ")
			if id, err := strconv.Atoi(cleaned); err == nil {
				res[id] = true
			}
		}
		if len(res) > 0 {
			return res
		}
	}
	if m := reMssqlObjectIDEq.FindStringSubmatch(sql); len(m) == 2 {
		if id, err := strconv.Atoi(m[1]); err == nil {
			return map[int]bool{id: true}
		}
	}
	if m := reMssqlObjectIdCallEq.FindStringSubmatch(sql); len(m) == 2 {
		targetName := cleanTargetTableName(m[1])
		if src != nil {
			for tIdx, t := range src.GetSortedTables() {
				if strings.EqualFold(t.Name, targetName) {
					return map[int]bool{1000 + tIdx: true}
				}
			}
		}
	}
	if m := reMssqlTableNameEq.FindStringSubmatch(sql); len(m) == 2 {
		targetName := cleanTargetTableName(m[1])
		if src != nil {
			for tIdx, t := range src.GetSortedTables() {
				if strings.EqualFold(t.Name, targetName) {
					return map[int]bool{1000 + tIdx: true}
				}
			}
		}
	}
	return nil
}

func formatMSSQLDataType(c storage.Column) string {
	switch c.Type {
	case storage.TypeInt:
		return "int"
	case storage.TypeFloat:
		return "float"
	case storage.TypeDecimal:
		if c.Precision > 0 && c.Scale > 0 {
			return fmt.Sprintf("decimal(%d,%d)", c.Precision, c.Scale)
		} else if c.Precision > 0 {
			return fmt.Sprintf("decimal(%d)", c.Precision)
		}
		return "decimal(18,2)"
	case storage.TypeVarChar:
		prec := c.Precision
		if prec <= 0 {
			prec = 255
		}
		return fmt.Sprintf("varchar(%d)", prec)
	case storage.TypeBool:
		return "bit"
	case storage.TypeDateTime:
		return "datetime"
	case storage.TypeDate:
		return "date"
	case storage.TypeTime:
		return "time(0)"
	case storage.TypeUUID:
		return "uniqueidentifier"
	case storage.TypeBinary:
		return "varbinary(max)"
	case storage.TypeJSON:
		return "nvarchar(max)"
	default:
		return "varchar(255)"
	}
}

func execInformationSchemaColumnsWithComputed(src Source, sql string) (*Result, error) {
	columns := []string{
		"table_schema", "table_name", "column_name", "ordinal_position",
		"column_default", "is_nullable", "is_generated", "data_type",
	}

	targetTable := ""
	if m := reMssqlTableNameEq.FindStringSubmatch(sql); len(m) == 2 {
		targetTable = cleanIdentifier(m[1])
	}

	targetSchema := "dbo"
	if m := reMssqlSchemaNameEq.FindStringSubmatch(sql); len(m) == 2 {
		targetSchema = cleanIdentifier(m[1])
	}

	tables, _ := src.ListTables()
	var allRows []map[string]any

	for _, t := range tables {
		if targetTable != "" && !strings.EqualFold(t.Name, targetTable) {
			continue
		}
		for i, c := range t.Columns {
			nullable := "NO"
			if c.Nullable {
				nullable = "YES"
			}
			allRows = append(allRows, map[string]any{
				"table_schema":     targetSchema,
				"table_name":       t.Name,
				"column_name":      c.Name,
				"ordinal_position": i + 1,
				"column_default":   nil,
				"is_nullable":      nullable,
				"is_generated":     "NO",
				"data_type":        formatMSSQLDataType(c),
			})
		}
	}

	return &Result{
		Columns: columns,
		Rows:    allRows,
	}, nil
}

func execSysColumnsJoinedWithTypes(src Source, sql string) (*Result, error) {
	columns := []string{
		"columnName", "dataType", "objectId", "isIdentity",
		"maxLength", "precision", "scale", "isNullable",
		"charMaxLength", "defaultValue", "defaultConstraint",
		"computedExpression", "isPersisted", "columnId",
		"numericPrecision", "numericScale", "isSparse", "columnComment",
	}

	targetIDs := parseTargetObjectIDs(sql, src)
	tables := src.GetSortedTables()
	var allRows []map[string]any

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}
		for cIdx, c := range t.Columns {
			dataType := "varchar"
			maxLen := 255
			prec := 0
			scale := 0
			var charMaxLen any = nil
			var numPrec any = nil
			var numScale any = nil

			switch c.Type {
			case storage.TypeInt:
				dataType = "int"
				maxLen = 4
				prec = 10
				numPrec = 10
				numScale = 0
			case storage.TypeFloat:
				dataType = "float"
				maxLen = 8
				prec = 53
				numPrec = 53
			case storage.TypeDecimal:
				dataType = "decimal"
				maxLen = 17
				prec = 18
				scale = 2
				if c.Precision > 0 {
					prec = c.Precision
				}
				if c.Scale > 0 {
					scale = c.Scale
				}
				numPrec = prec
				numScale = scale
			case storage.TypeVarChar:
				dataType = "varchar"
				maxLen = 255
				if c.Precision > 0 {
					maxLen = c.Precision
				}
				charMaxLen = maxLen
			case storage.TypeBool:
				dataType = "bit"
				maxLen = 1
				prec = 1
			case storage.TypeDateTime:
				dataType = "datetime"
				maxLen = 8
			case storage.TypeDate:
				dataType = "date"
				maxLen = 3
			case storage.TypeTime:
				dataType = "time"
				maxLen = 5
				prec = 16
				scale = 7
			case storage.TypeUUID:
				dataType = "uniqueidentifier"
				maxLen = 16
			case storage.TypeBinary:
				dataType = "varbinary"
				maxLen = 8000
			case storage.TypeJSON:
				dataType = "nvarchar"
				maxLen = -1
			}

			isPKVal := int64(0)
			if c.PrimaryKey {
				isPKVal = int64(1)
			}
			var icColID any = nil
			if c.PrimaryKey {
				icColID = int64(cIdx + 1)
			}

			allRows = append(allRows, map[string]any{
				"columnName":         c.Name,
				"name":               c.Name,
				"column_name":        c.Name,
				"a.name":             c.Name,
				"b.name":             dataType,
				"c.name":             c.Name,
				"dataType":           dataType,
				"data_type":          dataType,
				"type_name":          dataType,
				"ctype":              dataType,
				"objectId":           objID,
				"object_id":          objID,
				"a.object_id":        objID,
				"c.object_id":        objID,
				"isIdentity":         false,
				"is_identity":        false,
				"maxLength":          int64(maxLen),
				"max_length":         int64(maxLen),
				"a.max_length":       int64(maxLen),
				"precision":          int64(prec),
				"a.precision":        int64(prec),
				"scale":              int64(scale),
				"a.scale":            int64(scale),
				"isNullable":         c.Nullable,
				"is_nullable":        c.Nullable,
				"a.is_nullable":      c.Nullable,
				"nullable":           c.Nullable,
				"charMaxLength":      charMaxLen,
				"defaultValue":       nil,
				"defaultConstraint":  nil,
				"vdefault":           "",
				"computedExpression": nil,
				"isPersisted":        nil,
				"columnId":           int64(cIdx + 1),
				"column_id":          int64(cIdx + 1),
				"a.column_id":        int64(cIdx + 1),
				"ic.column_id":       icColID,
				"numericPrecision":   numPrec,
				"numericScale":       numScale,
				"isSparse":           false,
				"columnComment":      nil,
				"system_type_id":     int(c.Type),
				"user_type_id":       int(c.Type),
				"type":               "U",
				"type_desc":          "USER_TABLE",
				"tableName":          t.Name,
				"table_name":         t.Name,
				"schemaName":         "dbo",
				"schema_name":        "dbo",
				"o.type":             "U",
				"o.object_id":        objID,
				"o.name":             t.Name,
				"u.name":             "dbo",
				"is_primary_key":     c.PrimaryKey,
				"is_pk":              isPKVal,
				"default_is_null":    true,
				"defaultIsNull":      true,
				"isnull(p.is_primary_key, 0)": isPKVal,
			})
		}
	}

	return FormatResultWithSQL(columns, allRows, sql)
}

func execFnHelpCollations(sql string) (*Result, error) {
	columns := []string{"name", "description"}
	allRows := []map[string]any{
		{"name": "Latin1_General_CS_AS", "description": "Latin1-General, case-sensitive, accent-sensitive, kanatype-insensitive, width-insensitive"},
		{"name": "Latin1_General_CI_AS", "description": "Latin1-General, case-insensitive, accent-sensitive, kanatype-insensitive, width-insensitive"},
		{"name": "SQL_Latin1_General_CP1_CS_AS", "description": "SQL Server collation, code page 1252, case-sensitive, accent-sensitive"},
		{"name": "SQL_Latin1_General_CP1_CI_AS", "description": "SQL Server collation, code page 1252, case-insensitive, accent-sensitive"},
	}

	projStr := "*"
	upper := strings.ToUpper(sql)
	if idxFrom := strings.Index(upper, "FROM"); idxFrom != -1 {
		idxSel := strings.Index(upper, "SELECT")
		if idxSel != -1 && idxSel < idxFrom {
			projStr = strings.TrimSpace(sql[idxSel+6 : idxFrom])
		}
	}
	whereClause := ""
	if idxWhere := strings.Index(upper, "WHERE"); idxWhere != -1 {
		whereClause = strings.TrimSpace(sql[idxWhere+5:])
	}
	return FormatResult(columns, allRows, projStr, whereClause, "", -1)
}

func execSysIndexesPrimaryKey(src Source, sql string) (*Result, error) {
	columns := []string{
		"objectId", "object_id", "pureName", "pure_name", "name", "index_name", "schemaName", "schema_name", "columnName", "column_name", "constraintName", "constraint_name", "is_primary_key", "is_unique", "type_desc", "index_id",
	}

	targetIDs := parseTargetObjectIDs(sql, src)
	tables := src.GetSortedTables()
	var allRows []map[string]any

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}
		for _, c := range t.Columns {
			if c.PrimaryKey {
				allRows = append(allRows, map[string]any{
					"objectId":        objID,
					"object_id":       objID,
					"pureName":        t.Name,
					"pure_name":       t.Name,
					"name":            "PK_" + t.Name,
					"index_name":      "PK_" + t.Name,
					"schemaName":      "dbo",
					"schema_name":     "dbo",
					"columnName":      c.Name,
					"column_name":     c.Name,
					"constraintName":  "PK_" + t.Name,
					"constraint_name": "PK_" + t.Name,
					"is_primary_key":  true,
					"is_unique":       true,
					"type_desc":       "CLUSTERED",
					"index_id":        1,
				})
			}
		}
	}

	return FormatResultWithSQL(columns, allRows, sql)
}

func execSysIndexesSecondary(src Source, sql string) (*Result, error) {
	columns := []string{
		"object_id", "objectId", "name", "index_name", "constraintName", "constraint_name", "indexType", "type_desc", "isUnique", "is_unique", "is_primary_key", "index_id", "is_unique_constraint", "filterDefinition",
	}

	targetIDs := parseTargetObjectIDs(sql, src)
	tables := src.GetSortedTables()
	var allRows []map[string]any

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}
		for idxNum, idx := range t.Indexes {
			allRows = append(allRows, map[string]any{
				"object_id":            objID,
				"objectId":             objID,
				"constraintName":       idx.Name,
				"constraint_name":      idx.Name,
				"name":                 idx.Name,
				"index_name":           idx.Name,
				"indexType":            "NONCLUSTERED",
				"type_desc":            "NONCLUSTERED",
				"isUnique":             idx.Unique,
				"is_unique":            idx.Unique,
				"is_primary_key":       false,
				"index_id":             idxNum + 2,
				"is_unique_constraint": false,
				"filterDefinition":     nil,
			})
		}
	}

	return FormatResultWithSQL(columns, allRows, sql)
}

func execSysIndexColumns(src Source, sql string) (*Result, error) {
	columns := []string{
		"object_id", "index_id", "column_id", "columnName", "isDescending", "isIncludedColumn",
	}

	targetIDs := parseTargetObjectIDs(sql, src)
	tables := src.GetSortedTables()
	var allRows []map[string]any

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}
		for idxNum, idx := range t.Indexes {
			for _, colName := range idx.Columns {
				colID := 1
				for cIdx, c := range t.Columns {
					if strings.EqualFold(c.Name, colName) {
						colID = cIdx + 1
						break
					}
				}
				allRows = append(allRows, map[string]any{
					"object_id":        objID,
					"objectId":         objID,
					"index_id":         idxNum + 2,
					"column_id":        colID,
					"columnName":       colName,
					"column_name":      colName,
					"name":             colName,
					"isDescending":     false,
					"isIncludedColumn": false,
				})
			}
		}
	}

	return FormatResultWithSQL(columns, allRows, sql)
}

func execReferentialConstraintsForeignKey(src Source, sql string) (*Result, error) {
	columns := []string{
		"schemaName", "pureName", "columnName", "refSchemaName",
		"refTableName", "refColumnName", "constraintName",
		"updateAction", "deleteAction", "objectId",
	}

	targetIDs := parseTargetObjectIDs(sql, src)
	tables := src.GetSortedTables()
	var allRows []map[string]any

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}
		for _, fk := range t.ForeignKeys {
			upAction := fk.OnUpdate.String()
			delAction := fk.OnDelete.String()
			maxCols := len(fk.ChildColumns)
			if len(fk.ParentColumns) > maxCols {
				maxCols = len(fk.ParentColumns)
			}
			if maxCols == 0 {
				allRows = append(allRows, map[string]any{
					"schemaName":           "dbo",
					"schema_name":          "dbo",
					"pureName":             t.Name,
					"pure_name":            t.Name,
					"name":                 fk.Name,
					"columnName":           "",
					"column_name":          "",
					"refSchemaName":        "dbo",
					"ref_schema_name":      "dbo",
					"refTableName":         fk.ParentTable,
					"ref_table_name":       fk.ParentTable,
					"parent_table":         t.Name,
					"ref_table":            fk.ParentTable,
					"refColumnName":        "",
					"ref_column_name":      "",
					"constraintName":       fk.Name,
					"constraint_name":      fk.Name,
					"updateAction":         upAction,
					"update_action":        upAction,
					"deleteAction":         delAction,
					"delete_action":        delAction,
					"objectId":             objID,
					"object_id":            objID,
					"parent_object_id":     objID,
					"referenced_object_id": 1000,
				})
				continue
			}
			for i := 0; i < maxCols; i++ {
				childCol := ""
				if i < len(fk.ChildColumns) {
					childCol = fk.ChildColumns[i]
				}
				parentCol := ""
				if i < len(fk.ParentColumns) {
					parentCol = fk.ParentColumns[i]
				}
				allRows = append(allRows, map[string]any{
					"schemaName":           "dbo",
					"schema_name":          "dbo",
					"pureName":             t.Name,
					"pure_name":            t.Name,
					"name":                 fk.Name,
					"columnName":           childCol,
					"column_name":          childCol,
					"refSchemaName":        "dbo",
					"ref_schema_name":      "dbo",
					"refTableName":         fk.ParentTable,
					"ref_table_name":       fk.ParentTable,
					"parent_table":         t.Name,
					"ref_table":            fk.ParentTable,
					"refColumnName":        parentCol,
					"ref_column_name":      parentCol,
					"constraintName":       fk.Name,
					"constraint_name":      fk.Name,
					"updateAction":         upAction,
					"update_action":        upAction,
					"deleteAction":         delAction,
					"delete_action":        delAction,
					"objectId":             objID,
					"object_id":            objID,
					"parent_object_id":     objID,
					"referenced_object_id": 1000,
				})
			}
		}
	}

	return FormatResultWithSQL(columns, allRows, sql)
}

func execSysTablesRowCount(src Source, sql string) (*Result, error) {
	upper := strings.ToUpper(sql)
	hasStorageSizeCols := strings.Contains(upper, "TOTALSIZE") || strings.Contains(upper, "USEDSIZE")
	var columns []string
	if hasStorageSizeCols {
		columns = []string{"name", "rows", "totalSize", "usedSize"}
	} else {
		columns = []string{"objectId", "tableRowCount"}
	}

	targetIDs := parseTargetObjectIDs(sql, src)
	tables := src.GetSortedTables()
	var allRows []map[string]any

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}
		cnt := int64(0)
		if countProvider, ok := src.(interface{ GetTableRowCount(string) int64 }); ok {
			cnt = countProvider.GetTableRowCount(t.Name)
		}
		allRows = append(allRows, map[string]any{
			"name":          t.Name,
			"rows":          cnt,
			"totalSize":     int64(16),
			"usedSize":      int64(8),
			"objectId":      objID,
			"tableRowCount": cnt,
		})
	}

	return &Result{
		Columns: columns,
		Rows:    allRows,
	}, nil
}

func execSysColumnsExtendedPropertyComment(sql string) (*Result, error) {
	columns := []string{"objectId", "columnComment", "columnId"}
	return &Result{
		Columns: columns,
		Rows:    []map[string]any{},
	}, nil
}

func execSysParameters(sql string) (*Result, error) {
	columns := []string{
		"parentObjectId", "objectId", "parameterObjectId", "pureName",
		"parameterName", "dataType", "charMaxLength", "parameterMode",
		"numericPrecision", "numericScale", "parameterIndex", "schemaName",
	}
	return &Result{
		Columns: columns,
		Rows:    []map[string]any{},
	}, nil
}

func execSysObjectsTriggers(sql string) (*Result, error) {
	columns := []string{
		"modifyDate", "objectId", "triggerName", "schemaName",
		"tableName", "triggerTiming", "eventType", "definition",
	}
	return &Result{
		Columns: columns,
		Rows:    []map[string]any{},
	}, nil
}

func execSysObjectsMetadata(src Source, sql string) (*Result, error) {
	columns := []string{"objectId", "object_id", "modifyDate", "modify_date", "createDate", "create_date", "type", "type_desc", "pureName", "pure_name", "name", "schemaName", "schema_name"}
	defaultDate := time.Now().UTC().Truncate(time.Second)
	var allRows []map[string]any

	tables := src.GetSortedTables()
	for tIdx, t := range tables {
		allRows = append(allRows, map[string]any{
			"objectId":    1000 + tIdx,
			"object_id":   1000 + tIdx,
			"modifyDate":  defaultDate,
			"modify_date": defaultDate,
			"createDate":  defaultDate,
			"create_date": defaultDate,
			"type":        "U",
			"type_desc":   "USER_TABLE",
			"pureName":    t.Name,
			"pure_name":   t.Name,
			"name":        t.Name,
			"schemaName":  "dbo",
			"schema_name": "dbo",
		})
	}

	views := src.GetSortedViews()
	for vIdx, v := range views {
		allRows = append(allRows, map[string]any{
			"objectId":    2000 + vIdx,
			"object_id":   2000 + vIdx,
			"modifyDate":  defaultDate,
			"modify_date": defaultDate,
			"createDate":  defaultDate,
			"create_date": defaultDate,
			"type":        "V",
			"type_desc":   "VIEW",
			"pureName":    v.Name,
			"pure_name":   v.Name,
			"name":        v.Name,
			"schemaName":  "dbo",
			"schema_name": "dbo",
		})
	}

	procs := src.GetSortedProcedures()
	for pIdx, p := range procs {
		allRows = append(allRows, map[string]any{
			"objectId":    3000 + pIdx,
			"object_id":   3000 + pIdx,
			"modifyDate":  defaultDate,
			"modify_date": defaultDate,
			"createDate":  defaultDate,
			"create_date": defaultDate,
			"type":        "P",
			"type_desc":   "SQL_STORED_PROCEDURE",
			"pureName":    p.Name,
			"pure_name":   p.Name,
			"name":        p.Name,
			"schemaName":  "dbo",
			"schema_name": "dbo",
		})
	}

	return FormatResultWithSQL(columns, allRows, sql)
}

func execSysTablesMetadata(src Source, sql string) (*Result, error) {
	columns := []string{"pureName", "pure_name", "name", "schemaName", "schema_name", "objectId", "object_id", "schema_id", "type", "type_desc", "createDate", "create_date", "modifyDate", "modify_date", "objectComment", "object_comment"}
	defaultDate := time.Now().UTC().Truncate(time.Second)
	var allRows []map[string]any

	tables := src.GetSortedTables()
	for tIdx, t := range tables {
		allRows = append(allRows, map[string]any{
			"pureName":       t.Name,
			"pure_name":      t.Name,
			"name":           t.Name,
			"table_name":     t.Name,
			"schemaName":     "dbo",
			"schema_name":    "dbo",
			"objectId":       1000 + tIdx,
			"object_id":      1000 + tIdx,
			"schema_id":      1,
			"type":           "U",
			"type_desc":      "USER_TABLE",
			"createDate":     defaultDate,
			"create_date":    defaultDate,
			"modifyDate":     defaultDate,
			"modify_date":    defaultDate,
			"objectComment":  nil,
			"object_comment": nil,
		})
	}

	return FormatResultWithSQL(columns, allRows, sql)
}

func execSysObjectsViews(src Source, sql string) (*Result, error) {
	columns := []string{"pureName", "pure_name", "name", "schemaName", "schema_name", "objectId", "object_id", "createDate", "create_date", "modifyDate", "modify_date", "type", "type_desc"}
	defaultDate := time.Now().UTC().Truncate(time.Second)
	var allRows []map[string]any

	views := src.GetSortedViews()
	for vIdx, v := range views {
		allRows = append(allRows, map[string]any{
			"pureName":    v.Name,
			"pure_name":   v.Name,
			"name":        v.Name,
			"schemaName":  "dbo",
			"schema_name": "dbo",
			"objectId":    2000 + vIdx,
			"object_id":   2000 + vIdx,
			"createDate":  defaultDate,
			"create_date": defaultDate,
			"modifyDate":  defaultDate,
			"modify_date": defaultDate,
			"type":        "V",
			"type_desc":   "VIEW",
		})
	}

	return FormatResultWithSQL(columns, allRows, sql)
}

func execSysObjectsProgrammables(src Source, sql string) (*Result, error) {
	columns := []string{"pureName", "pure_name", "name", "schemaName", "schema_name", "objectId", "object_id", "createDate", "create_date", "modifyDate", "modify_date", "sqlObjectType", "type", "type_desc"}
	defaultDate := time.Now().UTC().Truncate(time.Second)
	var allRows []map[string]any

	procs := src.GetSortedProcedures()
	for pIdx, p := range procs {
		allRows = append(allRows, map[string]any{
			"pureName":      p.Name,
			"pure_name":     p.Name,
			"name":          p.Name,
			"schemaName":    "dbo",
			"schema_name":   "dbo",
			"objectId":      3000 + pIdx,
			"object_id":     3000 + pIdx,
			"createDate":    defaultDate,
			"create_date":   defaultDate,
			"modifyDate":    defaultDate,
			"modify_date":   defaultDate,
			"sqlObjectType": "P",
			"type":          "P",
			"type_desc":     "SQL_STORED_PROCEDURE",
		})
	}

	return FormatResultWithSQL(columns, allRows, sql)
}

func execSysCommentsSqlCode(src Source, sql string) (*Result, error) {
	columns := []string{"pureName", "pure_name", "name", "schemaName", "schema_name", "codeText", "definition", "text", "object_id", "objectId", "id"}
	var allRows []map[string]any

	views := src.GetSortedViews()
	for vIdx, v := range views {
		allRows = append(allRows, map[string]any{
			"pureName":    v.Name,
			"pure_name":   v.Name,
			"name":        v.Name,
			"schemaName":  "dbo",
			"schema_name": "dbo",
			"codeText":    v.Query,
			"definition":  v.Query,
			"text":        v.Query,
			"objectId":    2000 + vIdx,
			"object_id":   2000 + vIdx,
			"id":          2000 + vIdx,
		})
	}
	procs := src.GetSortedProcedures()
	for pIdx, p := range procs {
		allRows = append(allRows, map[string]any{
			"pureName":    p.Name,
			"pure_name":   p.Name,
			"name":        p.Name,
			"schemaName":  "dbo",
			"schema_name": "dbo",
			"codeText":    p.Body,
			"definition":  p.Body,
			"text":        p.Body,
			"objectId":    3000 + pIdx,
			"object_id":   3000 + pIdx,
			"id":          3000 + pIdx,
		})
	}

	return FormatResultWithSQL(columns, allRows, sql)
}

func execSysAllObjectsJoined(src Source, sql string) (*Result, error) {
	columns := []string{
		"name", "object_id", "principal_id", "schema_id", "parent_object_id",
		"type", "type_desc", "create_date", "modify_date",
		"is_ms_shipped", "is_published", "is_schema_published",
		"description", "is_external",
	}
	defaultDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var allRows []map[string]any

	tables := src.GetSortedTables()
	for tIdx, t := range tables {
		allRows = append(allRows, map[string]any{
			"name":                t.Name,
			"object_id":           1000 + tIdx,
			"principal_id":        nil,
			"schema_id":           1,
			"parent_object_id":    0,
			"type":                "U ",
			"type_desc":           "USER_TABLE",
			"create_date":         defaultDate,
			"modify_date":         defaultDate,
			"is_ms_shipped":       false,
			"is_published":        false,
			"is_schema_published": false,
			"description":         nil,
			"is_external":         false,
		})
	}

	views := src.GetSortedViews()
	for vIdx, v := range views {
		allRows = append(allRows, map[string]any{
			"name":                v.Name,
			"object_id":           2000 + vIdx,
			"principal_id":        nil,
			"schema_id":           1,
			"parent_object_id":    0,
			"type":                "V ",
			"type_desc":           "VIEW",
			"create_date":         defaultDate,
			"modify_date":         defaultDate,
			"is_ms_shipped":       false,
			"is_published":        false,
			"is_schema_published": false,
			"description":         nil,
			"is_external":         false,
		})
	}

	return FormatResult(columns, allRows, "*", "", "name", -1)
}

package virtual

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

// ExtractMssqlTableNameEq extracts table name equality filter from MSSQL catalog query.
func ExtractMssqlTableNameEq(sql string) (string, bool) {
	return sqlutil.ExtractEqualityFilter(sql, "table_name", "tbl.name", "c.name", "t.name", "ic.table_name", "tbl.table_name", "name")
}

// ExtractMssqlSchemaNameEq extracts schema name equality filter from MSSQL catalog query.
func ExtractMssqlSchemaNameEq(sql string) (string, bool) {
	return sqlutil.ExtractEqualityFilter(sql, "table_schema", "tbl.schema_id", "s.name", "ic.table_schema", "name")
}

func cleanTargetTableName(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, "[]\"'`")
	if idx := strings.LastIndex(s, "."); idx != -1 {
		s = s[idx+1:]
	}
	return strings.Trim(strings.TrimSpace(s), "[]\"'`")
}

func parseTargetObjectIDs(sql string, src Source) map[int]bool {
	if pred, ok := sqlutil.ParseInPredicate(sql); ok && strings.HasSuffix(strings.ToUpper(pred.Column), "OBJECT_ID") {
		res := make(map[int]bool, len(pred.Items))
		for _, part := range pred.Items {
			cleaned := strings.Trim(strings.TrimSpace(part), "'\" ")
			if id, err := strconv.Atoi(cleaned); err == nil {
				res[id] = true
			}
		}
		if len(res) > 0 {
			return res
		}
	}
	if val, ok := sqlutil.ExtractEqualityFilter(sql, "object_id", "tbl.object_id", "clmns.object_id", "i.object_id", "t.object_id", "k.parent_object_id", "fk.parent_object_id", "chk.parent_object_id", "def.parent_object_id", "tr.parent_id", "s.object_id", "parent_object_id"); ok {
		if id, err := strconv.Atoi(val); err == nil {
			return map[int]bool{id: true}
		}
	}
	if tblName, ok := sqlutil.ExtractObjectIdCall(sql); ok {
		targetName := cleanTargetTableName(tblName)
		if src != nil {
			for tIdx, t := range src.GetSortedTables() {
				if strings.EqualFold(t.Name, targetName) {
					return map[int]bool{1000 + tIdx: true}
				}
			}
		}
	}
	if tbl, ok := ExtractMssqlTableNameEq(sql); ok {
		targetName := cleanTargetTableName(tbl)
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
	if tbl, ok := ExtractMssqlTableNameEq(sql); ok {
		targetTable = cleanIdentifier(tbl)
	}

	targetSchema := "dbo"
	if sch, ok := ExtractMssqlSchemaNameEq(sql); ok {
		targetSchema = cleanIdentifier(sch)
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
		"Name", "ID", "Nullable", "Identity", "RowGuidCol", "IsFileStream",
		"IsSparse", "IsColumnSet", "Computed", "IsDeterministic", "IsPersisted",
		"Length", "Precision", "Scale", "Collation", "DataType", "SystemType",
		"Default", "DefaultConstraint", "Urn",
		"columnName", "dataType", "objectId", "isIdentity",
		"maxLength", "precision", "scale", "isNullable",
		"charMaxLength", "defaultValue", "defaultConstraint",
		"computedExpression", "isPersisted", "columnId",
		"numericPrecision", "numericScale", "isSparse", "columnComment",
	}

	targetIDs := parseTargetObjectIDs(sql, src)
	tables := src.GetSortedTables()
	var allRows []map[string]any

	currDb := "master"
	if dbs, ok := src.(interface{ GetCurrentDatabase() string }); ok {
		if cur := dbs.GetCurrentDatabase(); cur != "" {
			currDb = cur
		}
	}
	serverName := "MSSQLSERVER"

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
			var collName any = nil
			if c.Type == storage.TypeVarChar {
				collName = "SQL_Latin1_General_CP1_CI_AS"
			}

			colUrn := fmt.Sprintf("Server[@Name='%s']/Database[@Name='%s']/Table[@Name='%s' and @Schema='dbo']/Column[@Name='%s']", serverName, currDb, t.Name, c.Name)

			allRows = append(allRows, map[string]any{
				"Name":                 c.Name,
				"ID":                   int32(cIdx + 1),
				"Nullable":             c.Nullable,
				"Identity":             c.Identity,
				"RowGuidCol":           false,
				"IsFileStream":         false,
				"IsSparse":             false,
				"IsColumnSet":          false,
				"Computed":             false,
				"IsDeterministic":      false,
				"IsPersisted":          false,
				"Length":               int32(maxLen),
				"Precision":            int32(prec),
				"Scale":                int32(scale),
				"Collation":            collName,
				"DataType":             dataType,
				"SystemType":           dataType,
				"Default":              c.Default,
				"DefaultConstraint":    "",
				"Urn":                  colUrn,
				"clmns.name":           c.Name,
				"clmns.column_id":      int32(cIdx + 1),
				"clmns.is_nullable":    c.Nullable,
				"clmns.is_identity":    c.Identity,
				"clmns.is_rowguidcol":  false,
				"clmns.is_filestream":  false,
				"clmns.is_sparse":      false,
				"clmns.is_column_set":  false,
				"clmns.is_computed":    false,
				"clmns.max_length":     int64(maxLen),
				"clmns.precision":      int64(prec),
				"clmns.scale":          int64(scale),
				"clmns.collation_name": collName,
				"usrt.name":            dataType,
				"s1clmns.name":         dataType,
				"columnName":           c.Name,
				"name":                 c.Name,
				"column_name":          c.Name,
				"a.name":               c.Name,
				"b.name":               dataType,
				"c.name":               c.Name,
				"collation_name":       collName,
				"a.collation_name":     collName,
				"dataType":             dataType,
				"data_type":            dataType,
				"type_name":            dataType,
				"ctype":                dataType,
				"objectId":             objID,
				"object_id":            objID,
				"a.object_id":          objID,
				"c.object_id":          objID,
				"isIdentity":           false,
				"is_identity":          false,
				"maxLength":            int64(maxLen),
				"max_length":           int64(maxLen),
				"a.max_length":         int64(maxLen),
				"precision":            int64(prec),
				"a.precision":          int64(prec),
				"scale":                int64(scale),
				"a.scale":              int64(scale),
				"isNullable":           c.Nullable,
				"is_nullable":          c.Nullable,
				"a.is_nullable":        c.Nullable,
				"nullable":             c.Nullable,
				"charMaxLength":        charMaxLen,
				"defaultValue":         nil,
				"defaultConstraint":    nil,
				"vdefault":             "",
				"computedExpression":   nil,
				"columnId":             int64(cIdx + 1),
				"column_id":            int64(cIdx + 1),
				"a.column_id":          int64(cIdx + 1),
				"ic.column_id":         icColID,
				"numericPrecision":     numPrec,
				"numericScale":         numScale,
				"isSparse":             false,
				"columnComment":        nil,
				"system_type_id":       int(c.Type),
				"user_type_id":         int(c.Type),
				"type":                 "U",
				"type_desc":            "USER_TABLE",
				"tableName":            t.Name,
				"table_name":           t.Name,
				"tbl.name":             t.Name,
				"tbl.schema_id":        int32(1),
				"tbl.object_id":        objID,
				"schema_id":            int32(1),
				"schemaName":           "dbo",
				"schema_name":          "dbo",
				"o.type":               "U",
				"o.object_id":          objID,
				"o.name":               t.Name,
				"u.name":               "dbo",
				"is_primary_key":       c.PrimaryKey,
				"is_pk":                isPKVal,
				"default_is_null":      true,
				"defaultIsNull":        true,
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

	projStr, whereClause := parseVirtualSelectClauses(sql)
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
		"IndexID", "ColumnID", "KeyOrdinal", "Name", "Descending",
		"object_id", "index_id", "column_id", "columnName", "column_name", "name",
		"key_ordinal", "is_descending_key", "isDescending", "isIncludedColumn", "is_included_column",
	}

	targetIDs := parseTargetObjectIDs(sql, src)
	tables := src.GetSortedTables()
	var allRows []map[string]any

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}

		// 1. Primary Key columns (index_id = 1)
		pkColOrdinal := 1
		for cIdx, c := range t.Columns {
			if c.PrimaryKey {
				allRows = append(allRows, map[string]any{
					"IndexID":              int32(1),
					"index_id":             int32(1),
					"ic.index_id":          int32(1),
					"ColumnID":             int32(cIdx + 1),
					"column_id":            int32(cIdx + 1),
					"ic.column_id":         int32(cIdx + 1),
					"clmns.column_id":      int32(cIdx + 1),
					"KeyOrdinal":           int32(pkColOrdinal),
					"key_ordinal":          int32(pkColOrdinal),
					"ic.key_ordinal":       int32(pkColOrdinal),
					"Name":                 c.Name,
					"name":                 c.Name,
					"columnName":           c.Name,
					"column_name":          c.Name,
					"clmns.name":           c.Name,
					"Descending":           false,
					"isDescending":         false,
					"is_descending_key":    false,
					"ic.is_descending_key": false,
					"isIncludedColumn":     false,
					"is_included_column":   false,
					"object_id":            objID,
					"objectId":             objID,
					"ic.object_id":         objID,
					"tbl.object_id":        objID,
					"tbl.name":             t.Name,
					"tbl.schema_id":        int32(1),
					"schema_id":            int32(1),
					"tableName":            t.Name,
					"table_name":           t.Name,
				})
				pkColOrdinal++
			}
		}

		// 2. Secondary index columns (index_id = idxNum + 2)
		for idxNum, idx := range t.Indexes {
			for kOrd, colName := range idx.Columns {
				colID := 1
				for cIdx, c := range t.Columns {
					if strings.EqualFold(c.Name, colName) {
						colID = cIdx + 1
						break
					}
				}
				allRows = append(allRows, map[string]any{
					"IndexID":              int32(idxNum + 2),
					"index_id":             int32(idxNum + 2),
					"ic.index_id":          int32(idxNum + 2),
					"ColumnID":             int32(colID),
					"column_id":            int32(colID),
					"ic.column_id":         int32(colID),
					"clmns.column_id":      int32(colID),
					"KeyOrdinal":           int32(kOrd + 1),
					"key_ordinal":          int32(kOrd + 1),
					"ic.key_ordinal":       int32(kOrd + 1),
					"Name":                 colName,
					"name":                 colName,
					"columnName":           colName,
					"column_name":          colName,
					"clmns.name":           colName,
					"Descending":           false,
					"isDescending":         false,
					"is_descending_key":    false,
					"ic.is_descending_key": false,
					"isIncludedColumn":     false,
					"is_included_column":   false,
					"object_id":            objID,
					"objectId":             objID,
					"ic.object_id":         objID,
					"tbl.object_id":        objID,
					"tbl.name":             t.Name,
					"tbl.schema_id":        int32(1),
					"schema_id":            int32(1),
					"tableName":            t.Name,
					"table_name":           t.Name,
				})
			}
		}
	}

	return FormatResultWithSQL(columns, allRows, sql)
}

func execXORMIndexes(src Source, sql string) (*Result, error) {
	columns := []string{"INDEX_NAME", "COLUMN_NAME", "IS_UNIQUE", "key_ordinal", "column_id", "index_id"}
	targetTable := ""
	if tbl, ok := sqlutil.ExtractEqualityFilter(sql, "object_name(ixs.object_id)", "object_name(i.object_id)", "object_name(object_id)", "object_name", "table_name"); ok {
		targetTable = tbl
	} else if tbl, ok := sqlutil.ExtractObjectIdCall(sql); ok {
		targetTable = tbl
	}
	targetTable = sqlutil.CleanLeafIdentifier(targetTable)

	upper := strings.ToUpper(sql)
	wantPK := containsEq(upper, "IS_PRIMARY_KEY", "1")
	wantSecondary := containsEq(upper, "IS_PRIMARY_KEY", "0")
	if !wantPK && !wantSecondary {
		wantPK = true
		wantSecondary = true
	}

	tables := src.GetSortedTables()
	var allRows []map[string]any

	for tIdx, t := range tables {
		if targetTable != "" && !strings.EqualFold(t.Name, targetTable) {
			continue
		}
		objID := 1000 + tIdx

		// 1. Primary Key Index (if requested)
		if wantPK {
			pkName := "PK_" + t.Name
			var pkCols []string
			for _, col := range t.Columns {
				if col.PrimaryKey {
					pkCols = append(pkCols, col.Name)
				}
			}
			for colOrdinal, colName := range pkCols {
				keyOrdinal := int64(colOrdinal + 1)
				allRows = append(allRows, map[string]any{
					"INDEX_NAME":         pkName,
					"COLUMN_NAME":        colName,
					"IS_UNIQUE":          true,
					"name":               pkName,
					"column_name":        colName,
					"is_unique":          true,
					"ixs.name":           pkName,
					"col.name":           colName,
					"ixs.is_unique":      true,
					"is_primary_key":     true,
					"ixs.is_primary_key": 1,
					"key_ordinal":        keyOrdinal,
					"ixcs.key_ordinal":   keyOrdinal,
					"column_id":          keyOrdinal,
					"ixcs.column_id":     keyOrdinal,
					"col.column_id":      keyOrdinal,
					"index_id":           int64(1),
					"ixs.index_id":       int64(1),
					"ixcs.index_id":      int64(1),
					"tableName":          t.Name,
					"table_name":         t.Name,
					"object_id":          objID,
					"ixs.object_id":      objID,
				})
			}
		}

		// 2. Secondary Indexes (if requested)
		if wantSecondary {
			for idxNum, idx := range t.Indexes {
				for colOrdinal, colName := range idx.Columns {
					keyOrdinal := int64(colOrdinal + 1)
					allRows = append(allRows, map[string]any{
						"INDEX_NAME":         idx.Name,
						"COLUMN_NAME":        colName,
						"IS_UNIQUE":          idx.Unique,
						"name":               idx.Name,
						"column_name":        colName,
						"is_unique":          idx.Unique,
						"ixs.name":           idx.Name,
						"col.name":           colName,
						"ixs.is_unique":      idx.Unique,
						"is_primary_key":     false,
						"ixs.is_primary_key": 0,
						"key_ordinal":        keyOrdinal,
						"ixcs.key_ordinal":   keyOrdinal,
						"column_id":          keyOrdinal,
						"ixcs.column_id":     keyOrdinal,
						"col.column_id":      keyOrdinal,
						"index_id":           int64(idxNum + 2),
						"ixs.index_id":       int64(idxNum + 2),
						"ixcs.index_id":      int64(idxNum + 2),
						"tableName":          t.Name,
						"table_name":         t.Name,
						"object_id":          objID,
						"ixs.object_id":      objID,
					})
				}
			}
		}
	}

	projStr, whereClause, orderByClause := ExtractSQLParts(sql)
	var remConds []string
	if whereClause != "" {
		conds := sqlutil.SplitAndConditions(whereClause)
		for _, c := range conds {
			if sqlutil.ContainsKeyword(c, "OBJECT_NAME", true) || sqlutil.ContainsKeyword(c, "OBJECT_ID", true) || sqlutil.ContainsKeyword(c, "IS_PRIMARY_KEY", true) {
				continue
			}
			remConds = append(remConds, c)
		}
	}
	remWhere := strings.Join(remConds, " AND ")

	return FormatResult(columns, allRows, projStr, remWhere, orderByClause, -1)
}

func execReferentialConstraintsForeignKey(src Source, sql string) (*Result, error) {
	columns := []string{
		"schemaName", "schema_name", "pureName", "pure_name", "name",
		"columnName", "column_name", "refSchemaName", "ref_schema_name",
		"refTableName", "ref_table_name", "parent_table", "ref_table",
		"child_table", "referenced_table",
		"refColumnName", "ref_column_name", "constraintName", "constraint_name",
		"updateAction", "update_action", "deleteAction", "delete_action",
		"objectId", "object_id", "parent_object_id", "referenced_object_id",
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
			cleanParent := sqlutil.CleanLeafIdentifier(fk.ParentTable)
			refSchema := "dbo"
			trimmedParent := strings.Trim(strings.TrimSpace(fk.ParentTable), "[]\"'`")
			if dot := strings.Index(trimmedParent, "."); dot != -1 {
				refSchema = strings.Trim(trimmedParent[:dot], "[]\"'`")
			}

			refObjID := 0
			for rIdx, rt := range tables {
				if strings.EqualFold(rt.Name, cleanParent) {
					refObjID = 1000 + rIdx
					break
				}
			}

			fkName := fk.Name
			if fkName == "" {
				fkName = fmt.Sprintf("FK_%s_%s", t.Name, cleanParent)
			}

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
					"name":                 fkName,
					"columnName":           "",
					"column_name":          "",
					"refSchemaName":        refSchema,
					"ref_schema_name":      refSchema,
					"refTableName":         cleanParent,
					"ref_table_name":       cleanParent,
					"parent_table":         t.Name,
					"child_table":          t.Name,
					"ref_table":            cleanParent,
					"referenced_table":     cleanParent,
					"refColumnName":        "",
					"ref_column_name":      "",
					"constraintName":       fkName,
					"constraint_name":      fkName,
					"updateAction":         upAction,
					"update_action":        upAction,
					"deleteAction":         delAction,
					"delete_action":        delAction,
					"objectId":             objID,
					"object_id":            objID,
					"parent_object_id":     objID,
					"referenced_object_id": refObjID,
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
					"name":                 fkName,
					"columnName":           childCol,
					"column_name":          childCol,
					"refSchemaName":        refSchema,
					"ref_schema_name":      refSchema,
					"refTableName":         cleanParent,
					"ref_table_name":       cleanParent,
					"parent_table":         t.Name,
					"child_table":          t.Name,
					"ref_table":            cleanParent,
					"referenced_table":     cleanParent,
					"refColumnName":        parentCol,
					"ref_column_name":      parentCol,
					"constraintName":       fkName,
					"constraint_name":      fkName,
					"updateAction":         upAction,
					"update_action":        upAction,
					"deleteAction":         delAction,
					"delete_action":        delAction,
					"objectId":             objID,
					"object_id":            objID,
					"parent_object_id":     objID,
					"referenced_object_id": refObjID,
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

func execSysParameters(src Source, sql string) (*Result, error) {
	columns := []string{
		"Name", "name", "parameter_name", "parameterName", "pureName", "pure_name",
		"ID", "id", "parameter_id", "parameterId", "parameterIndex",
		"Urn", "urn",
		"DataType", "dataType", "data_type",
		"Length", "max_length", "charMaxLength",
		"NumericPrecision", "numeric_precision", "precision",
		"NumericScale", "numeric_scale", "scale",
		"IsOutputParameter", "is_output", "isOutput", "parameterMode",
		"HasDefaultValue", "has_default_value",
		"DefaultValue", "default_value",
		"object_id", "objectId", "parent_object_id", "parentObjectId", "parameterObjectId",
		"user_type_id", "system_type_id",
		"Schema", "schema", "schemaName", "schema_name",
		"type", "sp.type", "type_desc", "sp.name", "procedure_name",
	}
	var allRows []map[string]any
	if src == nil {
		return FormatResultWithSQL(columns, allRows, sql)
	}

	currDb := "master"
	if dbs, ok := src.(interface{ GetCurrentDatabase() string }); ok {
		if cur := dbs.GetCurrentDatabase(); cur != "" {
			currDb = cur
		}
	}
	serverName := "MSSQLSERVER"

	procs := src.GetSortedProcedures()
	for pIdx, p := range procs {
		objID := int32(3000 + pIdx)
		for paramIdx, param := range p.Params {
			paramID := int32(paramIdx + 1)
			urn := fmt.Sprintf("Server[@Name='%s']/Database[@Name='%s']/StoredProcedure[@Name='%s' and @Schema='dbo']/Param[@Name='%s']", serverName, currDb, p.Name, param.Name)
			allRows = append(allRows, map[string]any{
				"Name":              param.Name,
				"name":              param.Name,
				"parameter_name":    param.Name,
				"parameterName":     param.Name,
				"pureName":          param.Name,
				"pure_name":         param.Name,
				"ID":                paramID,
				"id":                paramID,
				"parameter_id":      paramID,
				"parameterId":       paramID,
				"parameterIndex":    paramID,
				"Urn":               urn,
				"urn":               urn,
				"DataType":          param.Type.String(),
				"dataType":          param.Type.String(),
				"data_type":         param.Type.String(),
				"SystemType":        param.Type.String(),
				"system_type":       param.Type.String(),
				"systemType":        param.Type.String(),
				"Length":            int16(8),
				"max_length":        int16(8),
				"charMaxLength":     int16(8),
				"NumericPrecision":  int32(10),
				"numeric_precision": int32(10),
				"precision":         int32(10),
				"NumericScale":      int32(0),
				"numeric_scale":     int32(0),
				"scale":             int32(0),
				"IsOutputParameter": false,
				"is_output":         false,
				"isOutput":          false,
				"IsCursorParameter": false,
				"is_cursor_ref":     false,
				"parameterMode":     "IN",
				"HasDefaultValue":   false,
				"has_default_value": false,
				"DefaultValue":      nil,
				"default_value":     nil,
				"object_id":         objID,
				"objectId":          objID,
				"IDText":            objID,
				"id_text":           objID,
				"DatabaseName":      currDb,
				"database_name":     currDb,
				"ParamName":         param.Name,
				"param_name":        param.Name,
				"ParentSysObj":      false,
				"parent_sys_obj":    false,
				"Number":            int16(1),
				"number":            int16(1),
				"parent_object_id":  objID,
				"parentObjectId":    objID,
				"parameterObjectId": objID,
				"user_type_id":      int32(56),
				"system_type_id":    int32(56),
				"Schema":            "dbo",
				"schema":            "dbo",
				"schemaName":        "dbo",
				"schema_name":       "dbo",
				"type":               "P ",
				"sp.type":            "P ",
				"type_desc":          "SQL_STORED_PROCEDURE",
				"sp.name":            p.Name,
				"procedure_name":     p.Name,
			})
		}
	}
	return FormatResultWithSQL(columns, allRows, sql)
}

func execSysObjectsTriggers(sql string) (*Result, error) {
	columns := []string{
		"Name", "ID", "IsEnabled", "Urn",
		"modifyDate", "objectId", "triggerName", "schemaName",
		"tableName", "triggerTiming", "eventType", "definition",
		"name", "object_id", "parent_id", "is_disabled",
	}
	var allRows []map[string]any
	return FormatResultWithSQL(columns, allRows, sql)
}

func execSysObjectsMetadata(src Source, sql string) (*Result, error) {
	columns := []string{"objectId", "object_id", "modifyDate", "modify_date", "createDate", "create_date", "type", "type_desc", "pureName", "pure_name", "name", "schemaName", "schema_name", "schema", "table", "s.name", "o.name", "role", "select", "update", "insert", "delete"}
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
			"schema":      "dbo",
			"table":       t.Name,
			"s.name":      "dbo",
			"o.name":      t.Name,
			"schema_id":   1,
			"role":        nil,
			"select":      1,
			"update":      1,
			"insert":      1,
			"delete":      1,
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
			"schema":      "dbo",
			"table":       v.Name,
			"s.name":      "dbo",
			"o.name":      v.Name,
			"schema_id":   1,
			"role":        nil,
			"select":      1,
			"update":      1,
			"insert":      1,
			"delete":      1,
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
			"schema":      "dbo",
			"table":       p.Name,
			"s.name":      "dbo",
			"o.name":      p.Name,
			"schema_id":   1,
			"role":        nil,
			"select":      1,
			"update":      1,
			"insert":      1,
			"delete":      1,
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

func execSysTablesJoinedIndexes(src Source, sql string) (*Result, error) {
	columns := []string{
		"Name", "name", "Schema", "schema", "Urn", "urn", "CreateDate", "create_date",
		"ModifyDate", "modify_date", "Owner", "owner",
		"IsMemoryOptimized", "is_memory_optimized", "HasClusteredColumnStoreIndex",
		"RemoteDataArchiveEnabled", "TemporalType", "temporal_type", "IsPartitioned",
		"HasSparseColumn", "IsNode", "is_node", "IsEdge", "is_edge",
		"HasNonClusteredIndex", "HasClusteredIndex", "HasPrimaryClusteredIndex",
		"HasXmlIndex", "HasXmlData", "HasSpatialData", "HasNonClusteredColumnStoreIndex",
		"HistoryTableID", "history_table_id", "HistoryTableName", "history_table_name",
		"HistoryTableSchema", "history_table_schema",
		"IsFileTable", "is_filetable", "IsExternal", "is_external",
		"object_id", "schema_id", "type", "type_desc",
	}
	defaultDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var allRows []map[string]any

	if src == nil {
		return FormatResultWithSQL(columns, allRows, sql)
	}

	currDb := "master"
	if dbs, ok := src.(interface{ GetCurrentDatabase() string }); ok {
		if cur := dbs.GetCurrentDatabase(); cur != "" {
			currDb = cur
		}
	}
	serverName := "MSSQLSERVER"

	tables := src.GetSortedTables()
	for tIdx, t := range tables {
		hasPK := false
		for _, col := range t.Columns {
			if col.PrimaryKey {
				hasPK = true
				break
			}
		}

		urn := fmt.Sprintf("Server[@Name='%s']/Database[@Name='%s']/Table[@Name='%s' and @Schema='dbo']", serverName, currDb, t.Name)

		row := map[string]any{
			"Name":                            t.Name,
			"name":                            t.Name,
			"tbl.name":                        t.Name,
			"Schema":                          "dbo",
			"schema":                          "dbo",
			"schema_name":                     "dbo",
			"Urn":                             urn,
			"CreateDate":                      defaultDate,
			"create_date":                     defaultDate,
			"tbl.create_date":                 defaultDate,
			"ModifyDate":                      defaultDate,
			"modify_date":                     defaultDate,
			"Owner":                           "dbo",
			"owner":                           "dbo",
			"IsMemoryOptimized":               false,
			"is_memory_optimized":             false,
			"tbl.is_memory_optimized":         false,
			"HasClusteredColumnStoreIndex":    false,
			"RemoteDataArchiveEnabled":        false,
			"TemporalType":                    int32(0),
			"temporal_type":                   int32(0),
			"tbl.temporal_type":               int32(0),
			"IsPartitioned":                   false,
			"HasSparseColumn":                 false,
			"IsNode":                          false,
			"is_node":                         false,
			"tbl.is_node":                     false,
			"IsEdge":                          false,
			"is_edge":                         false,
			"tbl.is_edge":                     false,
			"HasNonClusteredIndex":            false,
			"HasClusteredIndex":               true,
			"HasPrimaryClusteredIndex":        hasPK,
			"HasXmlIndex":                     false,
			"HasXmlData":                      false,
			"HasSpatialData":                  false,
			"HasNonClusteredColumnStoreIndex": false,
			"HistoryTableID":                  int32(0),
			"history_table_id":                int32(0),
			"HistoryTableName":               "",
			"history_table_name":             "",
			"HistoryTableSchema":             "",
			"history_table_schema":           "",
			"IsFileTable":                    false,
			"is_filetable":                   false,
			"tbl.is_filetable":               false,
			"IsExternal":                     false,
			"is_external":                    false,
			"tbl.is_external":                false,
			"object_id":                       1000 + tIdx,
			"tbl.object_id":                   1000 + tIdx,
			"schema_id":                       1,
			"tbl.schema_id":                   1,
			"type":                            "U",
			"type_desc":                       "USER_TABLE",
			"is_ms_shipped":                   false,
			"tbl.is_ms_shipped":               false,
			"idx.object_id":                   1000 + tIdx,
			"idx.index_id":                    1,
			"idx.type":                        1,
			"idx.is_primary_key":              hasPK,
			"idx.is_unique_constraint":        false,
			"dsidx.data_space_id":             1,
			"dsidx.type":                      "FG",
		}
		allRows = append(allRows, row)
	}

	return FormatResultWithSQL(columns, allRows, sql)
}

func execSysIndexesForSSMS(src Source, sql string) (*Result, error) {
	columns := []string{
		"Name", "ID", "IndexType", "TypeDesc", "IsPrimaryKey", "IsUnique",
		"IsUniqueConstraint", "HasFilter", "FilterDefinition", "Urn",
		"name", "index_id", "type", "type_desc", "is_primary_key", "is_unique",
		"is_unique_constraint", "has_filter", "filter_definition", "object_id",
	}

	targetIDs := parseTargetObjectIDs(sql, src)
	tables := src.GetSortedTables()
	var allRows []map[string]any

	currDb := "master"
	if dbs, ok := src.(interface{ GetCurrentDatabase() string }); ok {
		if cur := dbs.GetCurrentDatabase(); cur != "" {
			currDb = cur
		}
	}
	serverName := "MSSQLSERVER"

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}

		// 1. Primary Key Clustered Index (if table has primary key)
		hasPK := false
		for _, c := range t.Columns {
			if c.PrimaryKey {
				hasPK = true
				break
			}
		}
		if hasPK {
			pkName := "PK_" + t.Name
			urn := fmt.Sprintf("Server[@Name='%s']/Database[@Name='%s']/Table[@Name='%s' and @Schema='dbo']/Index[@Name='%s']", serverName, currDb, t.Name, pkName)
			allRows = append(allRows, map[string]any{
				"Name":                 pkName,
				"name":                 pkName,
				"i.name":               pkName,
				"ID":                   int32(1),
				"index_id":             int32(1),
				"i.index_id":           int32(1),
				"IndexType":            int32(1), // Clustered
				"type":                 int32(1),
				"i.type":               int32(1),
				"TypeDesc":             "CLUSTERED",
				"type_desc":            "CLUSTERED",
				"i.type_desc":          "CLUSTERED",
				"IsPrimaryKey":         true,
				"is_primary_key":       true,
				"i.is_primary_key":     true,
				"IsUnique":             true,
				"is_unique":            true,
				"i.is_unique":          true,
				"IsUniqueConstraint":   false,
				"is_unique_constraint": false,
				"HasFilter":            false,
				"has_filter":           false,
				"FilterDefinition":     "",
				"filter_definition":    "",
				"object_id":            objID,
				"i.object_id":          objID,
				"tbl.object_id":        objID,
				"tbl.name":             t.Name,
				"tbl.schema_id":        int32(1),
				"schema_id":            int32(1),
				"tableName":            t.Name,
				"table_name":           t.Name,
				"Urn":                  urn,
			})
		}

		// 2. Secondary Indexes
		for idxNum, idx := range t.Indexes {
			idxID := int32(idxNum + 2)
			urn := fmt.Sprintf("Server[@Name='%s']/Database[@Name='%s']/Table[@Name='%s' and @Schema='dbo']/Index[@Name='%s']", serverName, currDb, t.Name, idx.Name)
			allRows = append(allRows, map[string]any{
				"Name":                 idx.Name,
				"name":                 idx.Name,
				"i.name":               idx.Name,
				"ID":                   idxID,
				"index_id":             idxID,
				"i.index_id":           idxID,
				"IndexType":            int32(2), // Non-Clustered
				"type":                 int32(2),
				"i.type":               int32(2),
				"TypeDesc":             "NONCLUSTERED",
				"type_desc":            "NONCLUSTERED",
				"i.type_desc":          "NONCLUSTERED",
				"IsPrimaryKey":         false,
				"is_primary_key":       false,
				"i.is_primary_key":     false,
				"IsUnique":             idx.Unique,
				"is_unique":            idx.Unique,
				"i.is_unique":          idx.Unique,
				"IsUniqueConstraint":   idx.Unique,
				"is_unique_constraint": idx.Unique,
				"HasFilter":            false,
				"has_filter":           false,
				"FilterDefinition":     "",
				"filter_definition":    "",
				"object_id":            objID,
				"i.object_id":          objID,
				"tbl.object_id":        objID,
				"tbl.name":             t.Name,
				"tbl.schema_id":        int32(1),
				"schema_id":            int32(1),
				"tableName":            t.Name,
				"table_name":           t.Name,
				"Urn":                  urn,
			})
		}
	}

	return FormatResultWithSQL(columns, allRows, sql)
}

func execSysKeyConstraintsMSSQL(src Source, sql string) (*Result, error) {
	columns := []string{
		"Name", "ID", "KeyType", "TypeDesc", "IsSystemNamed", "Urn",
		"name", "type", "type_desc", "is_system_named", "object_id", "parent_object_id", "schema_id",
	}

	targetIDs := parseTargetObjectIDs(sql, src)
	tables := src.GetSortedTables()
	var allRows []map[string]any

	currDb := "master"
	if dbs, ok := src.(interface{ GetCurrentDatabase() string }); ok {
		if cur := dbs.GetCurrentDatabase(); cur != "" {
			currDb = cur
		}
	}
	serverName := "MSSQLSERVER"

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}

		hasPK := false
		for _, c := range t.Columns {
			if c.PrimaryKey {
				hasPK = true
				break
			}
		}
		if hasPK {
			pkName := "PK_" + t.Name
			urn := fmt.Sprintf("Server[@Name='%s']/Database[@Name='%s']/Table[@Name='%s' and @Schema='dbo']/Key[@Name='%s']", serverName, currDb, t.Name, pkName)
			allRows = append(allRows, map[string]any{
				"Name":               pkName,
				"name":               pkName,
				"k.name":             pkName,
				"ID":                 int32(objID + 50000),
				"object_id":          int32(objID + 50000),
				"k.object_id":        int32(objID + 50000),
				"parent_object_id":   objID,
				"k.parent_object_id": objID,
				"tbl.object_id":      objID,
				"tbl.name":           t.Name,
				"tbl.schema_id":      int32(1),
				"tableName":          t.Name,
				"table_name":         t.Name,
				"KeyType":            "PK",
				"type":               "PK",
				"k.type":             "PK",
				"TypeDesc":           "PRIMARY_KEY_CONSTRAINT",
				"type_desc":          "PRIMARY_KEY_CONSTRAINT",
				"k.type_desc":        "PRIMARY_KEY_CONSTRAINT",
				"IsSystemNamed":      false,
				"is_system_named":    false,
				"schema_id":          int32(1),
				"Urn":                urn,
			})
		}
	}

	return FormatResultWithSQL(columns, allRows, sql)
}

func execSysForeignKeysMSSQL(src Source, sql string) (*Result, error) {
	columns := []string{
		"Name", "ID", "Urn", "name", "object_id", "parent_object_id", "referenced_object_id",
		"is_disabled", "is_not_trusted", "delete_referential_action", "update_referential_action",
	}

	targetIDs := parseTargetObjectIDs(sql, src)
	tables := src.GetSortedTables()
	var allRows []map[string]any

	currDb := "master"
	if dbs, ok := src.(interface{ GetCurrentDatabase() string }); ok {
		if cur := dbs.GetCurrentDatabase(); cur != "" {
			currDb = cur
		}
	}
	serverName := "MSSQLSERVER"

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}

		for fkIdx, fk := range t.ForeignKeys {
			fkName := fk.Name
			if fkName == "" {
				fkName = fmt.Sprintf("FK_%s_%d", t.Name, fkIdx+1)
			}
			refObjID := 1000
			for otherIdx, otherT := range tables {
				if strings.EqualFold(otherT.Name, fk.ParentTable) {
					refObjID = 1000 + otherIdx
					break
				}
			}
			fkID := int32(objID + 60000 + fkIdx)
			urn := fmt.Sprintf("Server[@Name='%s']/Database[@Name='%s']/Table[@Name='%s' and @Schema='dbo']/ForeignKey[@Name='%s']", serverName, currDb, t.Name, fkName)
			allRows = append(allRows, map[string]any{
				"Name":                      fkName,
				"name":                      fkName,
				"fk.name":                   fkName,
				"ID":                        fkID,
				"object_id":                 fkID,
				"fk.object_id":              fkID,
				"parent_object_id":          objID,
				"fk.parent_object_id":       objID,
				"referenced_object_id":      refObjID,
				"fk.referenced_object_id":   refObjID,
				"tbl.object_id":             objID,
				"tbl.name":                  t.Name,
				"tbl.schema_id":             int32(1),
				"schema_id":                 int32(1),
				"tableName":                 t.Name,
				"table_name":                t.Name,
				"is_disabled":               false,
				"is_not_trusted":            false,
				"delete_referential_action": int32(0),
				"update_referential_action": int32(0),
				"Urn":                       urn,
			})
		}
	}

	return FormatResultWithSQL(columns, allRows, sql)
}

func execSysCheckConstraintsMSSQL(src Source, sql string) (*Result, error) {
	columns := []string{
		"Name", "ID", "Definition", "is_disabled", "parent_object_id", "object_id", "Urn",
	}
	var allRows []map[string]any
	return FormatResultWithSQL(columns, allRows, sql)
}

func execSysDefaultConstraintsMSSQL(src Source, sql string) (*Result, error) {
	columns := []string{
		"Name", "ID", "Definition", "is_disabled", "parent_object_id", "parent_column_id", "object_id", "Urn",
	}

	targetIDs := parseTargetObjectIDs(sql, src)
	tables := src.GetSortedTables()
	var allRows []map[string]any

	currDb := "master"
	if dbs, ok := src.(interface{ GetCurrentDatabase() string }); ok {
		if cur := dbs.GetCurrentDatabase(); cur != "" {
			currDb = cur
		}
	}
	serverName := "MSSQLSERVER"

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}

		for cIdx, c := range t.Columns {
			if c.Default != "" {
				defName := fmt.Sprintf("DF_%s_%s", t.Name, c.Name)
				defID := int32(objID*100 + cIdx + 1)
				urn := fmt.Sprintf("Server[@Name='%s']/Database[@Name='%s']/Table[@Name='%s' and @Schema='dbo']/DefaultConstraint[@Name='%s']", serverName, currDb, t.Name, defName)
				allRows = append(allRows, map[string]any{
					"Name":                 defName,
					"name":                 defName,
					"def.name":             defName,
					"ID":                   defID,
					"object_id":            defID,
					"def.object_id":        defID,
					"Definition":           c.Default,
					"definition":           c.Default,
					"def.definition":       c.Default,
					"is_disabled":          false,
					"parent_object_id":     objID,
					"def.parent_object_id": objID,
					"parent_column_id":     int32(cIdx + 1),
					"def.parent_column_id": int32(cIdx + 1),
					"tbl.object_id":        objID,
					"tbl.name":             t.Name,
					"tbl.schema_id":        int32(1),
					"schema_id":            int32(1),
					"tableName":            t.Name,
					"table_name":           t.Name,
					"Urn":                  urn,
				})
			}
		}
	}

	return FormatResultWithSQL(columns, allRows, sql)
}

func execSysStatsMSSQL(src Source, sql string) (*Result, error) {
	columns := []string{
		"Name", "ID", "stats_id", "object_id", "auto_created", "user_created", "no_recompute", "has_filter", "filter_definition", "Urn",
	}

	targetIDs := parseTargetObjectIDs(sql, src)
	tables := src.GetSortedTables()
	var allRows []map[string]any

	currDb := "master"
	if dbs, ok := src.(interface{ GetCurrentDatabase() string }); ok {
		if cur := dbs.GetCurrentDatabase(); cur != "" {
			currDb = cur
		}
	}
	serverName := "MSSQLSERVER"

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}

		hasPK := false
		for _, c := range t.Columns {
			if c.PrimaryKey {
				hasPK = true
				break
			}
		}
		if hasPK {
			pkName := "PK_" + t.Name
			urn := fmt.Sprintf("Server[@Name='%s']/Database[@Name='%s']/Table[@Name='%s' and @Schema='dbo']/Statistic[@Name='%s']", serverName, currDb, t.Name, pkName)
			allRows = append(allRows, map[string]any{
				"Name":              pkName,
				"name":              pkName,
				"s.name":            pkName,
				"ID":                int32(1),
				"stats_id":          int32(1),
				"s.stats_id":        int32(1),
				"object_id":         objID,
				"s.object_id":       objID,
				"tbl.object_id":     objID,
				"tbl.name":          t.Name,
				"tbl.schema_id":     int32(1),
				"schema_id":         int32(1),
				"tableName":         t.Name,
				"table_name":        t.Name,
				"auto_created":      false,
				"user_created":      false,
				"no_recompute":      false,
				"has_filter":        false,
				"filter_definition": "",
				"Urn":               urn,
			})
		}

		for idxNum, idx := range t.Indexes {
			sID := int32(idxNum + 2)
			urn := fmt.Sprintf("Server[@Name='%s']/Database[@Name='%s']/Table[@Name='%s' and @Schema='dbo']/Statistic[@Name='%s']", serverName, currDb, t.Name, idx.Name)
			allRows = append(allRows, map[string]any{
				"Name":              idx.Name,
				"name":              idx.Name,
				"s.name":            idx.Name,
				"ID":                sID,
				"stats_id":          sID,
				"s.stats_id":        sID,
				"object_id":         objID,
				"s.object_id":       objID,
				"tbl.object_id":     objID,
				"tbl.name":          t.Name,
				"tbl.schema_id":     int32(1),
				"schema_id":         int32(1),
				"tableName":         t.Name,
				"table_name":        t.Name,
				"auto_created":      false,
				"user_created":      false,
				"no_recompute":      false,
				"has_filter":        false,
				"filter_definition": "",
				"Urn":               urn,
			})
		}
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
	columns := []string{
		"pureName", "pure_name", "name", "schemaName", "schema_name", "codeText", "definition", "text", "object_id", "objectId", "id",
		"uses_ansi_nulls", "uses_quoted_identifier", "is_schema_bound", "uses_database_collation", "is_recompiled", "null_on_null_input", "execute_as_principal_id", "uses_native_compilation",
	}
	var allRows []map[string]any

	views := src.GetSortedViews()
	for vIdx, v := range views {
		allRows = append(allRows, map[string]any{
			"pureName":                v.Name,
			"pure_name":               v.Name,
			"name":                    v.Name,
			"schemaName":              "dbo",
			"schema_name":             "dbo",
			"codeText":                v.Query,
			"definition":              v.Query,
			"text":                    v.Query,
			"objectId":                2000 + vIdx,
			"object_id":               2000 + vIdx,
			"id":                      2000 + vIdx,
			"uses_ansi_nulls":         true,
			"uses_quoted_identifier":   true,
			"is_schema_bound":         false,
			"uses_database_collation": false,
			"is_recompiled":           false,
			"null_on_null_input":      false,
			"execute_as_principal_id": nil,
			"uses_native_compilation": int32(0),
		})
	}
	procs := src.GetSortedProcedures()
	for pIdx, p := range procs {
		allRows = append(allRows, map[string]any{
			"pureName":                p.Name,
			"pure_name":               p.Name,
			"name":                    p.Name,
			"schemaName":              "dbo",
			"schema_name":             "dbo",
			"codeText":                p.Body,
			"definition":              p.Body,
			"text":                    p.Body,
			"objectId":                3000 + pIdx,
			"object_id":               3000 + pIdx,
			"id":                      3000 + pIdx,
			"uses_ansi_nulls":         true,
			"uses_quoted_identifier":   true,
			"is_schema_bound":         false,
			"uses_database_collation": false,
			"is_recompiled":           false,
			"null_on_null_input":      false,
			"execute_as_principal_id": nil,
			"uses_native_compilation": int32(0),
		})
	}

	return FormatResultWithSQL(columns, allRows, sql)
}

func execSysProceduresForSSMS(src Source, sql string) (*Result, error) {
	columns := []string{
		"Name", "name", "sp.name", "ssp.name", "pureName", "pure_name", "routine_name",
		"Schema", "schema", "SCHEMA_NAME(sp.schema_id)", "schema_id", "schemaName", "schema_name", "routine_schema",
		"ID", "id", "object_id", "sp.object_id", "ssp.object_id", "objectId",
		"Urn", "urn",
		"CreateDate", "create_date", "sp.create_date", "created_date",
		"DateLastModified", "modify_date", "sp.modify_date", "ModifyDate",
		"IsExecutionReplicated", "is_execution_replicated",
		"ImplementationType", "implementation_type",
		"Owner", "owner",
		"IsEncrypted", "is_encrypted",
		"IsNativelyCompiled", "is_natively_compiled",
		"AnsiNullsStatus", "ansi_nulls_status",
		"QuotedIdentifierStatus", "quoted_identifier_status",
		"IsSchemaBound", "is_schema_bound",
		"IsSystemObject", "is_ms_shipped", "sp.is_ms_shipped",
		"type", "sp.type", "type_desc", "routine_type",
		"is_published", "is_schema_published", "is_auto_executed",
		"definition", "codeText", "text",
	}
	defaultDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var allRows []map[string]any

	if src == nil {
		return FormatResultWithSQL(columns, allRows, sql)
	}

	currDb := "master"
	if dbs, ok := src.(interface{ GetCurrentDatabase() string }); ok {
		if cur := dbs.GetCurrentDatabase(); cur != "" {
			currDb = cur
		}
	}
	serverName := "MSSQLSERVER"

	procs := src.GetSortedProcedures()
	for pIdx, p := range procs {
		objID := int32(3000 + pIdx)
		createdDate := p.CreatedAt
		if createdDate.IsZero() {
			createdDate = defaultDate
		}
		updatedDate := p.UpdatedAt
		if updatedDate.IsZero() {
			updatedDate = createdDate
		}
		urn := fmt.Sprintf("Server[@Name='%s']/Database[@Name='%s']/StoredProcedure[@Name='%s' and @Schema='dbo']", serverName, currDb, p.Name)

		row := map[string]any{
			"Name":                      p.Name,
			"name":                      p.Name,
			"sp.name":                   p.Name,
			"ssp.name":                  p.Name,
			"pureName":                  p.Name,
			"pure_name":                 p.Name,
			"routine_name":              p.Name,
			"Schema":                    "dbo",
			"schema":                    "dbo",
			"SCHEMA_NAME(sp.schema_id)":  "dbo",
			"schema_id":                 int32(1),
			"sp.schema_id":              int32(1),
			"schemaName":                "dbo",
			"schema_name":               "dbo",
			"routine_schema":            "dbo",
			"ID":                        objID,
			"id":                        objID,
			"object_id":                 objID,
			"sp.object_id":              objID,
			"ssp.object_id":             objID,
			"objectId":                  objID,
			"Urn":                       urn,
			"urn":                       urn,
			"CreateDate":                createdDate,
			"create_date":               createdDate,
			"sp.create_date":            createdDate,
			"created_date":              createdDate,
			"DateLastModified":          updatedDate,
			"modify_date":               updatedDate,
			"sp.modify_date":            updatedDate,
			"ModifyDate":                updatedDate,
			"IsExecutionReplicated":     int32(0),
			"is_execution_replicated":   int32(0),
			"ImplementationType":        int32(0),
			"implementation_type":       int32(0),
			"Owner":                     "dbo",
			"owner":                     "dbo",
			"IsEncrypted":               false,
			"is_encrypted":              false,
			"IsNativelyCompiled":        int32(0),
			"is_natively_compiled":      int32(0),
			"AnsiNullsStatus":           true,
			"ansi_nulls_status":         true,
			"QuotedIdentifierStatus":    true,
			"quoted_identifier_status":  true,
			"IsSchemaBound":             false,
			"is_schema_bound":           false,
			"IsSystemObject":            false,
			"is_ms_shipped":             false,
			"sp.is_ms_shipped":          false,
			"type":                      "P ",
			"sp.type":                   "P ",
			"type_desc":                 "SQL_STORED_PROCEDURE",
			"routine_type":              "PROCEDURE",
			"is_published":              false,
			"is_schema_published":       false,
			"is_auto_executed":          false,
			"definition":                p.Body,
			"codeText":                  p.Body,
			"text":                      p.Body,
		}
		allRows = append(allRows, row)
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

	procs := src.GetSortedProcedures()
	for pIdx, p := range procs {
		createdDate := p.CreatedAt
		if createdDate.IsZero() {
			createdDate = defaultDate
		}
		updatedDate := p.UpdatedAt
		if updatedDate.IsZero() {
			updatedDate = createdDate
		}
		allRows = append(allRows, map[string]any{
			"name":                p.Name,
			"object_id":           3000 + pIdx,
			"principal_id":        nil,
			"schema_id":           1,
			"parent_object_id":    0,
			"type":                "P ",
			"type_desc":           "SQL_STORED_PROCEDURE",
			"create_date":         createdDate,
			"modify_date":         updatedDate,
			"is_ms_shipped":       false,
			"is_published":        false,
			"is_schema_published": false,
			"description":         nil,
			"is_external":         false,
		})
	}

	return FormatResultWithSQL(columns, allRows, sql)
}

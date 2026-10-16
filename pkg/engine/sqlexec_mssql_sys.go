package engine

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"sqlion/pkg/catalog"
	"sqlion/pkg/storage"
)

func (s *Coordinator) getSortedTables() []*storage.TableSchema {
	if s.engine == nil {
		return nil
	}
	tables, _ := s.engine.ListTables()
	sort.Slice(tables, func(i, j int) bool {
		return tables[i].Name < tables[j].Name
	})
	return tables
}

func (s *Coordinator) getSortedViews() []*catalog.View {
	if s.catalog == nil {
		return nil
	}
	views := s.catalog.ListViews()
	sort.Slice(views, func(i, j int) bool {
		return views[i].Name < views[j].Name
	})
	return views
}

func (s *Coordinator) getSortedProcedures() []*catalog.Procedure {
	if s.catalog == nil {
		return nil
	}
	procs := s.catalog.ListProcedures()
	sort.Slice(procs, func(i, j int) bool {
		return procs[i].Name < procs[j].Name
	})
	return procs
}

func parseTargetObjectIDs(sql string) map[int]bool {
	inRe := regexp.MustCompile(`(?is)(?:[a-z0-9_]+\.)?object_id\s+in\s*\(([^)]+)\)`)
	if m := inRe.FindStringSubmatch(sql); len(m) == 2 {
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
	eqRe := regexp.MustCompile(`(?is)(?:[a-z0-9_]+\.)?object_id\s*=\s*['"]?(\d+)['"]?`)
	if m := eqRe.FindStringSubmatch(sql); len(m) == 2 {
		if id, err := strconv.Atoi(m[1]); err == nil {
			return map[int]bool{id: true}
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

func (s *Coordinator) execBeekeeperMSSQLColumnSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{
		"table_schema", "table_name", "column_name", "ordinal_position",
		"column_default", "is_nullable", "is_generated", "data_type",
	}

	targetTable := ""
	tblRe := regexp.MustCompile(`(?is)(?:ic\.)?table_name\s*=\s*'([^']+)'`)
	if m := tblRe.FindStringSubmatch(sql); len(m) == 2 {
		targetTable = cleanIdentifier(m[1])
	}

	targetSchema := "dbo"
	schRe := regexp.MustCompile(`(?is)(?:ic\.)?table_schema\s*=\s*'([^']+)'`)
	if m := schRe.FindStringSubmatch(sql); len(m) == 2 {
		targetSchema = cleanIdentifier(m[1])
	}

	tables, _ := s.engine.ListTables()
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

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         allRows,
		RowsAffected: int64(len(allRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execDbGateMSSQLColumnSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{
		"columnName", "dataType", "objectId", "isIdentity",
		"maxLength", "precision", "scale", "isNullable",
		"charMaxLength", "defaultValue", "defaultConstraint",
		"computedExpression", "isPersisted", "columnId",
		"numericPrecision", "numericScale", "isSparse", "columnComment",
	}

	targetIDs := parseTargetObjectIDs(sql)
	tables := s.getSortedTables()
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
				maxLen = 9
				prec = c.Precision
				if prec <= 0 {
					prec = 18
				}
				scale = c.Scale
				if scale < 0 {
					scale = 2
				}
				numPrec = prec
				numScale = scale
			case storage.TypeVarChar:
				dataType = "varchar"
				maxLen = c.Precision
				if maxLen <= 0 {
					maxLen = 255
				}
				charMaxLen = maxLen
			case storage.TypeBool:
				dataType = "bit"
				maxLen = 1
				prec = 1
			case storage.TypeDateTime:
				dataType = "datetime"
				maxLen = 8
				prec = 23
				scale = 3
			case storage.TypeDate:
				dataType = "date"
				maxLen = 3
				prec = 10
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

			allRows = append(allRows, map[string]any{
				"columnName":         c.Name,
				"dataType":           dataType,
				"objectId":           objID,
				"isIdentity":         false,
				"maxLength":          maxLen,
				"precision":          prec,
				"scale":              scale,
				"isNullable":         c.Nullable,
				"charMaxLength":      charMaxLen,
				"defaultValue":       nil,
				"defaultConstraint":  nil,
				"computedExpression": nil,
				"isPersisted":        nil,
				"columnId":           cIdx + 1,
				"numericPrecision":   numPrec,
				"numericScale":       numScale,
				"isSparse":           false,
				"columnComment":      nil,
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

func (s *Coordinator) execDbGateMSSQLPrimaryKeySelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{
		"objectId", "pureName", "schemaName", "columnName", "constraintName",
	}

	targetIDs := parseTargetObjectIDs(sql)
	tables := s.getSortedTables()
	var allRows []map[string]any

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}
		for _, c := range t.Columns {
			if c.PrimaryKey {
				allRows = append(allRows, map[string]any{
					"objectId":       objID,
					"pureName":       t.Name,
					"schemaName":     "dbo",
					"columnName":     c.Name,
					"constraintName": "PK_" + t.Name,
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

func (s *Coordinator) execDbGateMSSQLIndexSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{
		"object_id", "constraintName", "indexType", "isUnique",
		"index_id", "is_unique_constraint", "filterDefinition",
	}

	targetIDs := parseTargetObjectIDs(sql)
	tables := s.getSortedTables()
	var allRows []map[string]any

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}
		for idxNum, idx := range t.Indexes {
			allRows = append(allRows, map[string]any{
				"object_id":            objID,
				"constraintName":       idx.Name,
				"indexType":            "NONCLUSTERED",
				"isUnique":             idx.Unique,
				"index_id":             idxNum + 2,
				"is_unique_constraint": false,
				"filterDefinition":     nil,
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

func (s *Coordinator) execDbGateMSSQLIndexColumnSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{
		"object_id", "index_id", "column_id", "columnName", "isDescending", "isIncludedColumn",
	}

	targetIDs := parseTargetObjectIDs(sql)
	tables := s.getSortedTables()
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
					"index_id":         idxNum + 2,
					"column_id":        colID,
					"columnName":       colName,
					"isDescending":     false,
					"isIncludedColumn": false,
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

func (s *Coordinator) execDbGateMSSQLForeignKeySelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{
		"schemaName", "pureName", "columnName", "refSchemaName",
		"refTableName", "refColumnName", "constraintName",
		"updateAction", "deleteAction", "objectId",
	}

	targetIDs := parseTargetObjectIDs(sql)
	tables := s.getSortedTables()
	var allRows []map[string]any

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}
		for _, fk := range t.ForeignKeys {
			allRows = append(allRows, map[string]any{
				"schemaName":     "dbo",
				"pureName":       t.Name,
				"columnName":     strings.Join(fk.ChildColumns, ", "),
				"refSchemaName":  "dbo",
				"refTableName":   fk.ParentTable,
				"refColumnName":  strings.Join(fk.ParentColumns, ", "),
				"constraintName": fk.Name,
				"updateAction":   "NO ACTION",
				"deleteAction":   "NO ACTION",
				"objectId":       objID,
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

func (s *Coordinator) execDbGateMSSQLRowCountSelect(sql string, start time.Time) (*QueryResult, error) {
	upper := strings.ToUpper(sql)
	isDBeaver := strings.Contains(upper, "TOTALSIZE") || strings.Contains(upper, "USEDSIZE")
	var columns []string
	if isDBeaver {
		columns = []string{"name", "rows", "totalSize", "usedSize"}
	} else {
		columns = []string{"objectId", "tableRowCount"}
	}

	targetIDs := parseTargetObjectIDs(sql)
	tables := s.getSortedTables()
	var allRows []map[string]any

	var sess storage.Session
	if s.engine != nil {
		sess, _ = s.engine.NewSession()
	}
	if sess != nil {
		defer sess.Close()
	}

	for tIdx, t := range tables {
		objID := 1000 + tIdx
		if targetIDs != nil && !targetIDs[objID] {
			continue
		}
		var cnt int64 = 0
		if s.catalog != nil {
			if st, ok := s.catalog.GetTableStats(t.Name); ok && st != nil && st.RowCount > 0 {
				cnt = st.RowCount
			}
		}
		if cnt == 0 && sess != nil {
			tx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
			if err == nil {
				cursor, err := tx.Scan(t.Name)
				if err == nil {
					for cursor.Next() {
						cnt++
					}
					cursor.Close()
				}
				_ = tx.Rollback()
			}
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

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         allRows,
		RowsAffected: int64(len(allRows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execDbGateMSSQLColumnCommentSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{"objectId", "columnComment", "columnId"}
	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      columns,
		Rows:         []map[string]any{},
		RowsAffected: 0,
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execDbGateMSSQLParamSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{
		"parentObjectId", "objectId", "parameterObjectId", "pureName",
		"parameterName", "dataType", "charMaxLength", "parameterMode",
		"numericPrecision", "numericScale", "parameterIndex", "schemaName",
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

func (s *Coordinator) execDbGateMSSQLTriggerSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{
		"modifyDate", "objectId", "triggerName", "schemaName",
		"tableName", "triggerTiming", "eventType", "definition",
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

func (s *Coordinator) execDbGateMSSQLObjectSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{"objectId", "modifyDate", "type", "pureName", "schemaName"}
	defaultDate := time.Now().UTC().Truncate(time.Second)
	var allRows []map[string]any

	tables := s.getSortedTables()
	for tIdx, t := range tables {
		allRows = append(allRows, map[string]any{
			"objectId":   1000 + tIdx,
			"modifyDate": defaultDate,
			"type":       "U",
			"pureName":   t.Name,
			"schemaName": "dbo",
		})
	}

	views := s.getSortedViews()
	for vIdx, v := range views {
		allRows = append(allRows, map[string]any{
			"objectId":   2000 + vIdx,
			"modifyDate": defaultDate,
			"type":       "V",
			"pureName":   v.Name,
			"schemaName": "dbo",
		})
	}
	procs := s.getSortedProcedures()
	for pIdx, p := range procs {
		allRows = append(allRows, map[string]any{
			"objectId":   3000 + pIdx,
			"modifyDate": defaultDate,
			"type":       "P",
			"pureName":   p.Name,
			"schemaName": "dbo",
		})
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

func (s *Coordinator) execDbGateMSSQLTableSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{"pureName", "schemaName", "objectId", "createDate", "modifyDate", "objectComment"}
	defaultDate := time.Now().UTC().Truncate(time.Second)
	var allRows []map[string]any

	tables := s.getSortedTables()
	for tIdx, t := range tables {
		allRows = append(allRows, map[string]any{
			"pureName":      t.Name,
			"schemaName":    "dbo",
			"objectId":      1000 + tIdx,
			"createDate":    defaultDate,
			"modifyDate":    defaultDate,
			"objectComment": nil,
		})
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

func (s *Coordinator) execDbGateMSSQLViewSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{"pureName", "schemaName", "objectId", "createDate", "modifyDate"}
	defaultDate := time.Now().UTC().Truncate(time.Second)
	var allRows []map[string]any

	views := s.getSortedViews()
	for vIdx, v := range views {
		allRows = append(allRows, map[string]any{
			"pureName":   v.Name,
			"schemaName": "dbo",
			"objectId":   2000 + vIdx,
			"createDate": defaultDate,
			"modifyDate": defaultDate,
		})
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

func (s *Coordinator) execDbGateMSSQLProgrammablesSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{"pureName", "schemaName", "objectId", "createDate", "modifyDate", "sqlObjectType"}
	defaultDate := time.Now().UTC().Truncate(time.Second)
	var allRows []map[string]any

	procs := s.getSortedProcedures()
	for pIdx, p := range procs {
		allRows = append(allRows, map[string]any{
			"pureName":      p.Name,
			"schemaName":    "dbo",
			"objectId":      3000 + pIdx,
			"createDate":    defaultDate,
			"modifyDate":    defaultDate,
			"sqlObjectType": "P",
		})
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

func (s *Coordinator) execDbGateMSSQLLoadSqlCodeSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{"pureName", "schemaName", "codeText"}
	var allRows []map[string]any

	if s.catalog != nil {
		for _, v := range s.catalog.ListViews() {
			allRows = append(allRows, map[string]any{
				"pureName":   v.Name,
				"schemaName": "dbo",
				"codeText":   v.Query,
			})
		}
		for _, p := range s.catalog.ListProcedures() {
			allRows = append(allRows, map[string]any{
				"pureName":   p.Name,
				"schemaName": "dbo",
				"codeText":   p.Body,
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

func (s *Coordinator) execDBeaverMSSQLAllObjectsSelect(sql string, start time.Time) (*QueryResult, error) {
	columns := []string{
		"name", "object_id", "principal_id", "schema_id", "parent_object_id",
		"type", "type_desc", "create_date", "modify_date",
		"is_ms_shipped", "is_published", "is_schema_published",
		"description", "is_external",
	}
	defaultDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var allRows []map[string]any

	tables := s.getSortedTables()
	for tIdx, t := range tables {
		allRows = append(allRows, map[string]any{
			"name":                t.Name,
			"object_id":           1000 + tIdx,
			"principal_id":        nil,
			"schema_id":           1,
			"parent_object_id":    0,
			"type":                "U",
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

	views := s.getSortedViews()
	for vIdx, v := range views {
		allRows = append(allRows, map[string]any{
			"name":                v.Name,
			"object_id":           2000 + vIdx,
			"principal_id":        nil,
			"schema_id":           1,
			"parent_object_id":    0,
			"type":                "V",
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

	return buildVirtualCatalogResult(columns, allRows, "*", "", "name", -1, start)
}

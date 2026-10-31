package engine

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"lionsql/pkg/catalog/virtual"
)

var (
	reJdbcTempSpExec = regexp.MustCompile(`(?is)INSERT\s+INTO\s+@([a-zA-Z0-9_]+)\s+EXEC\s+(sp_[a-zA-Z0-9_]+)\s*(.*?);`)
	reJdbcSelectFrom = regexp.MustCompile(`(?is)(SELECT\s+.*?\s+FROM\s+@[a-zA-Z0-9_]+.*)$`)
)

func isJdbcMetadataBatch(query string) bool {
	return strings.Contains(query, "INSERT INTO @") && strings.Contains(query, "EXEC sp_")
}

func parseJdbcSpArgs(raw string) []string {
	parts := strings.Split(raw, ",")
	res := make([]string, len(parts))
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if idx := strings.Index(p, "="); idx != -1 {
			p = strings.TrimSpace(p[idx+1:])
		}
		p = strings.Trim(p, "'\" \t\r\n")
		res[i] = p
	}
	return res
}

func (s *Coordinator) execJdbcMetadataBatch(query string, start time.Time, cs *ClientSession) (*QueryResult, error) {
	mExec := reJdbcTempSpExec.FindStringSubmatch(query)
	if len(mExec) < 4 {
		return nil, fmt.Errorf("not a matched jdbc metadata batch")
	}

	spName := strings.ToLower(mExec[2])
	rawArgs := mExec[3]

	selectMatch := reJdbcSelectFrom.FindStringSubmatch(query)
	selectSQL := ""
	if len(selectMatch) >= 2 {
		selectSQL = selectMatch[1]
	}

	dbName := "master"
	if cs != nil && cs.Database != "" {
		dbName = cs.Database
	}

	switch {
	case strings.HasPrefix(spName, "sp_columns"):
		return s.execJdbcTempSpColumns(rawArgs, selectSQL, dbName, start)
	case strings.HasPrefix(spName, "sp_fkeys"):
		return s.execJdbcTempSpFkeys(rawArgs, selectSQL, dbName, start)
	case strings.HasPrefix(spName, "sp_pkeys"):
		return s.execJdbcTempSpPkeys(rawArgs, selectSQL, dbName, start)
	default:
		return nil, fmt.Errorf("unsupported jdbc metadata sp: %s", spName)
	}
}

func (s *Coordinator) execJdbcTempSpColumns(rawArgs, selectSQL, dbName string, start time.Time) (*QueryResult, error) {
	args := parseJdbcSpArgs(rawArgs)
	tblName := ""
	if len(args) > 0 && !strings.EqualFold(args[0], "null") {
		tblName = args[0]
	}

	columns := []string{
		"TABLE_QUALIFIER", "TABLE_OWNER", "TABLE_NAME", "COLUMN_NAME", "DATA_TYPE", "TYPE_NAME",
		"PRECISION", "LENGTH", "SCALE", "RADIX", "NULLABLE", "REMARKS", "COLUMN_DEF",
		"SQL_DATA_TYPE", "SQL_DATETIME_SUB", "CHAR_OCTET_LENGTH", "ORDINAL_POSITION",
		"IS_NULLABLE", "SS_IS_SPARSE", "SS_IS_COLUMN_SET", "SS_IS_COMPUTED", "SS_IS_IDENTITY",
		"SS_UDT_CATALOG_NAME", "SS_UDT_SCHEMA_NAME", "SS_UDT_ASSEMBLY_TYPE_NAME",
		"SS_XML_SCHEMACOLLECTION_CATALOG_NAME", "SS_XML_SCHEMACOLLECTION_SCHEMA_NAME",
		"SS_XML_SCHEMACOLLECTION_NAME", "SS_DATA_TYPE",
	}

	var allRows []map[string]any
	if tblName != "" {
		tbl, err := s.engine.GetTable(tblName)
		if err == nil && tbl != nil {
			for i, c := range tbl.Columns {
				row := mapStorageColumnToSpColumn(c, tbl.Name, dbName, i+1)
				row["SS_IS_SPARSE"] = 0
				row["SS_IS_COLUMN_SET"] = 0
				row["SS_IS_COMPUTED"] = 0
				row["SS_IS_IDENTITY"] = 0
				if c.Identity {
					row["SS_IS_IDENTITY"] = 1
				}
				allRows = append(allRows, row)
			}
		}
	}

	res, err := virtual.FormatResultWithSQL(columns, allRows, selectSQL)
	if err != nil || res == nil {
		return &QueryResult{
			Success:      true,
			Command:      "SELECT",
			Columns:      columns,
			Rows:         allRows,
			RowsAffected: int64(len(allRows)),
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      res.Columns,
		Rows:         res.Rows,
		RowsAffected: int64(len(res.Rows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execJdbcTempSpFkeys(rawArgs, selectSQL, dbName string, start time.Time) (*QueryResult, error) {
	args := parseJdbcSpArgs(rawArgs)
	pkTbl := ""
	if len(args) > 0 && !strings.EqualFold(args[0], "null") {
		pkTbl = args[0]
	}
	fkTbl := ""
	if len(args) > 3 && !strings.EqualFold(args[3], "null") {
		fkTbl = args[3]
	}

	columns := []string{
		"PKTABLE_QUALIFIER", "PKTABLE_OWNER", "PKTABLE_NAME", "PKCOLUMN_NAME",
		"FKTABLE_QUALIFIER", "FKTABLE_OWNER", "FKTABLE_NAME", "FKCOLUMN_NAME",
		"KEY_SEQ", "UPDATE_RULE", "DELETE_RULE", "FK_NAME", "PK_NAME", "DEFERRABILITY",
	}

	var allRows []map[string]any
	tables, _ := s.engine.ListTables()
	for _, t := range tables {
		for _, fk := range t.ForeignKeys {
			if pkTbl != "" && !strings.EqualFold(fk.ParentTable, pkTbl) {
				continue
			}
			if fkTbl != "" && !strings.EqualFold(t.Name, fkTbl) {
				continue
			}
			row := map[string]any{
				"PKTABLE_QUALIFIER": dbName,
				"PKTABLE_OWNER":     "dbo",
				"PKTABLE_NAME":      fk.ParentTable,
				"PKCOLUMN_NAME":     strings.Join(fk.ParentColumns, ","),
				"FKTABLE_QUALIFIER": dbName,
				"FKTABLE_OWNER":     "dbo",
				"FKTABLE_NAME":      t.Name,
				"FKCOLUMN_NAME":     strings.Join(fk.ChildColumns, ","),
				"KEY_SEQ":           1,
				"UPDATE_RULE":       3,
				"DELETE_RULE":       3,
				"FK_NAME":           fk.Name,
				"PK_NAME":           "PK_" + fk.ParentTable,
				"DEFERRABILITY":     7,
			}
			allRows = append(allRows, row)
		}
	}

	res, err := virtual.FormatResultWithSQL(columns, allRows, selectSQL)
	if err != nil || res == nil {
		return &QueryResult{
			Success:      true,
			Command:      "SELECT",
			Columns:      columns,
			Rows:         allRows,
			RowsAffected: int64(len(allRows)),
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      res.Columns,
		Rows:         res.Rows,
		RowsAffected: int64(len(res.Rows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

func (s *Coordinator) execJdbcTempSpPkeys(rawArgs, selectSQL, dbName string, start time.Time) (*QueryResult, error) {
	args := parseJdbcSpArgs(rawArgs)
	tblName := ""
	if len(args) > 0 && !strings.EqualFold(args[0], "null") {
		tblName = args[0]
	}

	columns := []string{
		"TABLE_QUALIFIER", "TABLE_OWNER", "TABLE_NAME", "COLUMN_NAME", "KEY_SEQ", "PK_NAME",
	}

	var allRows []map[string]any
	if tblName != "" {
		tbl, err := s.engine.GetTable(tblName)
		if err == nil && tbl != nil {
			seq := 1
			for _, c := range tbl.Columns {
				if c.PrimaryKey {
					row := map[string]any{
						"TABLE_QUALIFIER": dbName,
						"TABLE_OWNER":     "dbo",
						"TABLE_NAME":      tbl.Name,
						"COLUMN_NAME":     c.Name,
						"KEY_SEQ":         seq,
						"PK_NAME":         "PK_" + tbl.Name,
					}
					allRows = append(allRows, row)
					seq++
				}
			}
		}
	}

	res, err := virtual.FormatResultWithSQL(columns, allRows, selectSQL)
	if err != nil || res == nil {
		return &QueryResult{
			Success:      true,
			Command:      "SELECT",
			Columns:      columns,
			Rows:         allRows,
			RowsAffected: int64(len(allRows)),
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      res.Columns,
		Rows:         res.Rows,
		RowsAffected: int64(len(res.Rows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

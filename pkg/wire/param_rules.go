package wire

import (
	"strconv"
	"strings"

	"lionsql/pkg/engine"
	"lionsql/pkg/planner"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

// ParamRule defines a lookup entry for catalog and table column parameter OID inference.
type ParamRule struct {
	ColumnToken string // Uppercase token, e.g. "NSPNAME", "RELNAME", "OID", "ID"
	ScalarOID   int32  // PostgreSQL OID for scalar value (e.g. 25 for text, 26 for oid, 23 for int4)
	ArrayOID    int32  // PostgreSQL OID for array value (e.g. 1009 for _text, 1028 for _oid, 1007 for _int4)
}

// defaultParamOIDRules is the immutable lookup table for inferring parameter OIDs in system catalog queries.
var defaultParamOIDRules = map[string]ParamRule{
	"OID": {
		ColumnToken: "OID",
		ScalarOID:   26,   // oid
		ArrayOID:    1028, // _oid
	},
	"NSPNAME": {
		ColumnToken: "NSPNAME",
		ScalarOID:   25,   // text
		ArrayOID:    1009, // _text
	},
	"RELNAME": {
		ColumnToken: "RELNAME",
		ScalarOID:   25,   // text
		ArrayOID:    1009, // _text
	},
	"ATTNAME": {
		ColumnToken: "ATTNAME",
		ScalarOID:   25,   // text
		ArrayOID:    1009, // _text
	},
	"TYPNAME": {
		ColumnToken: "TYPNAME",
		ScalarOID:   25,   // text
		ArrayOID:    1009, // _text
	},
	"NAME": {
		ColumnToken: "NAME",
		ScalarOID:   25,   // text
		ArrayOID:    1009, // _text
	},
	"ID": {
		ColumnToken: "ID",
		ScalarOID:   23,   // int4
		ArrayOID:    1007, // _int4
	},
}

// defaultCastTypeOIDRules maps canonical lowercase PostgreSQL type names (used in ::type and ::type[] casts)
// to their corresponding scalar and array PostgreSQL OIDs.
var defaultCastTypeOIDRules = map[string]ParamRule{
	"int":                      {ScalarOID: 23, ArrayOID: 1007},
	"int4":                     {ScalarOID: 23, ArrayOID: 1007},
	"integer":                  {ScalarOID: 23, ArrayOID: 1007},
	"serial":                   {ScalarOID: 23, ArrayOID: 1007},
	"int8":                     {ScalarOID: 20, ArrayOID: 1016},
	"bigint":                   {ScalarOID: 20, ArrayOID: 1016},
	"bigserial":                {ScalarOID: 20, ArrayOID: 1016},
	"int2":                     {ScalarOID: 21, ArrayOID: 1005},
	"smallint":                 {ScalarOID: 21, ArrayOID: 1005},
	"bool":                     {ScalarOID: 16, ArrayOID: 1000},
	"boolean":                  {ScalarOID: 16, ArrayOID: 1000},
	"float4":                   {ScalarOID: 700, ArrayOID: 1021},
	"real":                     {ScalarOID: 700, ArrayOID: 1021},
	"float8":                   {ScalarOID: 701, ArrayOID: 1022},
	"double":                   {ScalarOID: 701, ArrayOID: 1022},
	"double precision":         {ScalarOID: 701, ArrayOID: 1022},
	"float":                    {ScalarOID: 701, ArrayOID: 1022},
	"numeric":                  {ScalarOID: 1700, ArrayOID: 1231},
	"decimal":                  {ScalarOID: 1700, ArrayOID: 1231},
	"timestamp":                {ScalarOID: 1114, ArrayOID: 1115},
	"datetime":                 {ScalarOID: 1114, ArrayOID: 1115},
	"timestamptz":              {ScalarOID: 1184, ArrayOID: 1185},
	"timestamp with time zone": {ScalarOID: 1184, ArrayOID: 1185},
	"date":                     {ScalarOID: 1082, ArrayOID: 1182},
	"uuid":                     {ScalarOID: 2950, ArrayOID: 2951},
	"json":                     {ScalarOID: 114, ArrayOID: 199},
	"jsonb":                    {ScalarOID: 3802, ArrayOID: 3807},
	"varchar":                  {ScalarOID: 25, ArrayOID: 1015},
	"character varying":        {ScalarOID: 25, ArrayOID: 1015},
	"text":                     {ScalarOID: 25, ArrayOID: 1009},
	"char":                     {ScalarOID: 25, ArrayOID: 1009},
	"bpchar":                   {ScalarOID: 25, ArrayOID: 1009},
}

// LookupParamOID resolves the parameter OID for a given column token and array flag from the table.
func LookupParamOID(columnToken string, isAny bool) int32 {
	if rule, ok := defaultParamOIDRules[columnToken]; ok {
		if isAny {
			return rule.ArrayOID
		}
		return rule.ScalarOID
	}
	return 0
}

// castNameToPgOID resolves a PostgreSQL cast type name (e.g. "int4", "text[]") to its PostgreSQL OID.
func castNameToPgOID(castType string) int32 {
	t := strings.ToLower(strings.TrimSpace(castType))
	isArray := false
	if strings.HasSuffix(t, "[]") {
		isArray = true
		t = strings.TrimSpace(strings.TrimSuffix(t, "[]"))
	}
	if idx := strings.Index(t, "("); idx != -1 {
		t = strings.TrimSpace(t[:idx])
	}
	if rule, ok := defaultCastTypeOIDRules[t]; ok {
		if isArray {
			return rule.ArrayOID
		}
		return rule.ScalarOID
	}
	if isArray {
		return 1009 // _text fallback for array casts
	}
	return 0
}

func isWritableStmtPrefix(sqlStr string) bool {
	fields := strings.Fields(strings.TrimSpace(sqlStr))
	if len(fields) == 0 {
		return false
	}
	switch strings.ToUpper(fields[0]) {
	case "INSERT", "UPDATE", "DELETE", "UPSERT", "REPLACE", "MERGE":
		return true
	default:
		return false
	}
}

func isWritableWithQuery(query string) bool {
	ctes, mainSQL := planner.ParseCTEs(query)
	if len(ctes) == 0 {
		return false
	}
	if isWritableStmtPrefix(mainSQL) {
		return true
	}
	for _, cte := range ctes {
		if isWritableStmtPrefix(cte.InnerSQL) {
			return true
		}
	}
	return false
}

func (s *Server) lookupTableSchema(tableName string) *storage.TableSchema {
	if s.engine == nil || tableName == "" {
		return nil
	}
	cleanTbl := cleanColumnIdentifier(tableName)
	if tbl, err := s.engine.GetTable(cleanTbl); err == nil && tbl != nil {
		return tbl
	}
	if tbl, err := s.engine.GetTable(strings.ToLower(cleanTbl)); err == nil && tbl != nil {
		return tbl
	}
	allTables, _ := s.engine.ListTables()
	for _, t := range allTables {
		if strings.EqualFold(t.Name, cleanTbl) {
			return t
		}
	}
	return nil
}

func (s *Server) describeDMLReturningColumns(dml *engine.DMLStatement) []ColumnMeta {
	if dml == nil || strings.TrimSpace(dml.Output) == "" {
		return nil
	}
	tbl := s.lookupTableSchema(dml.Table)
	rawCols := sqlutil.SplitTopLevel(dml.Output, ',')
	var cols []ColumnMeta
	for _, rc := range rawCols {
		rc = strings.TrimSpace(rc)
		if rc == "" {
			continue
		}
		upperRC := strings.ToUpper(rc)
		if upperRC == "*" || upperRC == "INSERTED.*" || upperRC == "DELETED.*" {
			if tbl != nil {
				for _, c := range tbl.Columns {
					cols = append(cols, ColumnMeta{
						Name:     c.Name,
						DataType: typeToPgOID(c.Type),
					})
				}
			}
			continue
		}
		exprStr := rc
		outName := ""
		if lhs, alias, hasAs := sqlutil.SplitAsAlias(rc); hasAs {
			exprStr = lhs
			outName = cleanColumnIdentifier(alias)
		} else {
			outName = cleanColumnIdentifier(rc)
			if dotIdx := strings.LastIndex(outName, "."); dotIdx != -1 {
				outName = outName[dotIdx+1:]
			}
		}
		lookupCol := cleanColumnIdentifier(exprStr)
		if dotIdx := strings.LastIndex(lookupCol, "."); dotIdx != -1 {
			lookupCol = lookupCol[dotIdx+1:]
		}
		oid := int32(25)
		if tbl != nil {
			for _, c := range tbl.Columns {
				if strings.EqualFold(c.Name, lookupCol) || strings.EqualFold(c.Name, outName) {
					oid = typeToPgOID(c.Type)
					break
				}
			}
		}
		cols = append(cols, ColumnMeta{
			Name:     outName,
			DataType: oid,
		})
	}
	return cols
}

func (s *Server) describeWithQueryColumns(query string) ([]ColumnMeta, bool) {
	ctes, mainSQL := planner.ParseCTEs(query)
	if len(ctes) == 0 {
		return nil, false
	}

	cteSchemas := make(map[string][]ColumnMeta, len(ctes))
	for _, cte := range ctes {
		var cols []ColumnMeta
		if dml, err := engine.ParseDMLCommand(cte.InnerSQL); err == nil && dml != nil {
			cols = s.describeDMLReturningColumns(dml)
		} else if sqlutil.HasPrefixKeyword(cte.InnerSQL, "SELECT") {
			if fIdx := sqlutil.FindTopLevelKeyword(cte.InnerSQL, "FROM"); fIdx != -1 {
				sPart := strings.TrimSpace(cte.InnerSQL[:fIdx])
				if rem, ok := sqlutil.TrimPrefixKeyword(sPart, "SELECT"); ok {
					sPart = rem
				}
				if rem, ok := sqlutil.TrimPrefixKeyword(sPart, "DISTINCT"); ok {
					sPart = rem
				}
				rawCols := sqlutil.SplitTopLevel(sPart, ',')
				var colNames []string
				for _, rc := range rawCols {
					rc = strings.TrimSpace(rc)
					if rc == "" {
						continue
					}
					var outName string
					if _, alias, hasAs := sqlutil.SplitAsAlias(rc); hasAs {
						outName = cleanColumnIdentifier(alias)
					} else {
						outName = cleanColumnIdentifier(rc)
						if dotIdx := strings.LastIndex(outName, "."); dotIdx != -1 {
							outName = outName[dotIdx+1:]
						}
					}
					colNames = append(colNames, outName)
				}
				oids := s.resolveColumnOIDs(cte.InnerSQL, colNames, nil)
				for idx, cn := range colNames {
					cols = append(cols, ColumnMeta{Name: cn, DataType: oids[idx]})
				}
			}
		}
		if len(cte.Columns) == len(cols) {
			for idx := range cols {
				cols[idx].Name = cte.Columns[idx]
			}
		}
		cteSchemas[strings.ToLower(cte.Name)] = cols
	}

	mainTrim := strings.TrimSpace(mainSQL)
	if isWritableStmtPrefix(mainTrim) {
		if dml, err := engine.ParseDMLCommand(mainTrim); err == nil && dml != nil {
			cols := s.describeDMLReturningColumns(dml)
			return cols, len(cols) > 0
		}
		return nil, false
	}

	if !sqlutil.HasPrefixKeyword(mainTrim, "SELECT") {
		return nil, false
	}

	fIdx := sqlutil.FindTopLevelKeyword(mainTrim, "FROM")
	sPart := mainTrim
	fromPart := ""
	if fIdx != -1 {
		sPart = strings.TrimSpace(mainTrim[:fIdx])
		fromPart = strings.TrimSpace(mainTrim[fIdx+4:])
		for _, kw := range []string{"WHERE", "GROUP BY", "HAVING", "ORDER BY", "LIMIT", "OFFSET"} {
			if kwIdx := sqlutil.FindTopLevelKeyword(fromPart, kw); kwIdx != -1 {
				fromPart = strings.TrimSpace(fromPart[:kwIdx])
			}
		}
	}
	if rem, ok := sqlutil.TrimPrefixKeyword(sPart, "SELECT"); ok {
		sPart = rem
	}
	if rem, ok := sqlutil.TrimPrefixKeyword(sPart, "DISTINCT"); ok {
		sPart = rem
	}

	aliasToCTE := make(map[string]string)
	var fromSources []string
	if fromPart != "" {
		for _, item := range sqlutil.SplitTopLevel(fromPart, ',') {
			fields := strings.Fields(strings.TrimSpace(item))
			if len(fields) == 0 {
				continue
			}
			srcName := strings.ToLower(cleanColumnIdentifier(fields[0]))
			fromSources = append(fromSources, srcName)
			aliasToCTE[srcName] = srcName
			if len(fields) >= 2 {
				alias := strings.ToLower(cleanColumnIdentifier(fields[len(fields)-1]))
				if !strings.EqualFold(fields[1], "JOIN") && !strings.EqualFold(fields[1], "INNER") && !strings.EqualFold(fields[1], "LEFT") {
					aliasToCTE[alias] = srcName
				}
			}
		}
	}

	rawProjs := sqlutil.SplitTopLevel(sPart, ',')
	var outCols []ColumnMeta
	for _, rawCol := range rawProjs {
		rawCol = strings.TrimSpace(rawCol)
		if rawCol == "" {
			continue
		}
		if rawCol == "*" {
			for _, src := range fromSources {
				if cList, ok := cteSchemas[src]; ok {
					outCols = append(outCols, cList...)
				} else if tbl := s.lookupTableSchema(src); tbl != nil {
					for _, c := range tbl.Columns {
						outCols = append(outCols, ColumnMeta{Name: c.Name, DataType: typeToPgOID(c.Type)})
					}
				}
			}
			continue
		}

		exprPart := rawCol
		outColName := ""
		if lhs, alias, hasAs := sqlutil.SplitAsAlias(rawCol); hasAs {
			exprPart = strings.TrimSpace(lhs)
			outColName = cleanColumnIdentifier(alias)
		} else {
			outColName = cleanColumnIdentifier(rawCol)
			if dotIdx := strings.LastIndex(outColName, "."); dotIdx != -1 {
				outColName = outColName[dotIdx+1:]
			}
		}

		cleanExpr := cleanColumnIdentifier(exprPart)
		var oid int32
		if dotIdx := strings.LastIndex(cleanExpr, "."); dotIdx != -1 {
			prefix := strings.ToLower(cleanColumnIdentifier(cleanExpr[:dotIdx]))
			bare := cleanColumnIdentifier(cleanExpr[dotIdx+1:])
			if cteName, ok := aliasToCTE[prefix]; ok {
				if cList, ok := cteSchemas[cteName]; ok {
					for _, cm := range cList {
						if strings.EqualFold(cm.Name, bare) {
							oid = cm.DataType
							break
						}
					}
				}
			}
		}
		if oid == 0 {
			for _, cList := range cteSchemas {
				for _, cm := range cList {
					if strings.EqualFold(cm.Name, outColName) || strings.EqualFold(cm.Name, cleanExpr) {
						oid = cm.DataType
						break
					}
				}
				if oid != 0 {
					break
				}
			}
		}
		if oid == 0 {
			resolved := s.resolveColumnOIDs(query, []string{outColName}, nil)
			if len(resolved) > 0 {
				oid = resolved[0]
			}
		}
		if oid == 0 {
			oid = 25
		}
		outCols = append(outCols, ColumnMeta{
			Name:     outColName,
			DataType: oid,
		})
	}
	return outCols, len(outCols) > 0
}

func extractParamIdxFromExpr(expr string, count int) int {
	s := strings.TrimSpace(expr)
	if idx := strings.Index(s, "::"); idx != -1 {
		s = strings.TrimSpace(s[:idx])
	}
	s = strings.Trim(s, "()")
	if !strings.HasPrefix(s, "$") {
		return 0
	}
	pIdx, err := strconv.Atoi(s[1:])
	if err != nil || pIdx < 1 || pIdx > count {
		return 0
	}
	return pIdx
}

func findAllParamIdxsInExpr(expr string, count int) []int {
	var res []int
	for i := 0; i < len(expr); i++ {
		if expr[i] == '$' && i+1 < len(expr) && expr[i+1] >= '0' && expr[i+1] <= '9' {
			j := i + 1
			for j < len(expr) && expr[j] >= '0' && expr[j] <= '9' {
				j++
			}
			if pIdx, err := strconv.Atoi(expr[i+1 : j]); err == nil && pIdx >= 1 && pIdx <= count {
				res = append(res, pIdx)
			}
			i = j - 1
		}
	}
	return res
}

func (s *Server) inferStmtDMLParamTypes(stmtSQL string, count int, oids []int32) {
	dml, err := engine.ParseDMLCommand(stmtSQL)
	if err != nil || dml == nil {
		return
	}
	tbl := s.lookupTableSchema(dml.Table)
	if tbl == nil {
		return
	}

	lookupColOID := func(colName string) int32 {
		cName := cleanColumnIdentifier(colName)
		if dotIdx := strings.LastIndex(cName, "."); dotIdx != -1 {
			cName = cName[dotIdx+1:]
		}
		for _, c := range tbl.Columns {
			if strings.EqualFold(c.Name, cName) {
				return typeToPgOID(c.Type)
			}
		}
		return 0
	}

	// A. INSERT INTO tbl (cols) VALUES (...) or INSERT INTO tbl (cols) SELECT ...
	if dml.Values != "" || dml.Select != "" {
		var cols []string
		if dml.Columns != "" {
			cols = sqlutil.SplitTopLevel(dml.Columns, ',')
		} else {
			for _, c := range tbl.Columns {
				cols = append(cols, c.Name)
			}
		}

		valTrim := strings.TrimSpace(dml.Values)
		if dml.Select != "" {
			valTrim = strings.TrimSpace(dml.Select)
		}
		if sqlutil.HasPrefixKeyword(valTrim, "SELECT") {
			sPart := valTrim
			if fIdx := sqlutil.FindTopLevelKeyword(valTrim, "FROM"); fIdx != -1 {
				sPart = strings.TrimSpace(valTrim[:fIdx])
			}
			if rem, ok := sqlutil.TrimPrefixKeyword(sPart, "SELECT"); ok {
				sPart = rem
			}
			if rem, ok := sqlutil.TrimPrefixKeyword(sPart, "DISTINCT"); ok {
				sPart = rem
			}
			projs := sqlutil.SplitTopLevel(sPart, ',')
			for i, proj := range projs {
				if i >= len(cols) {
					break
				}
				if pIdx := extractParamIdxFromExpr(proj, count); pIdx >= 1 {
					if oid := lookupColOID(cols[i]); oid != 0 && oids[pIdx-1] == 0 {
						oids[pIdx-1] = oid
					}
				}
			}
		} else if valTrim != "" {
			tuples := sqlutil.ExtractTopLevelTuples(valTrim)
			if len(tuples) == 0 {
				tuples = []string{strings.Trim(valTrim, "() ")}
			}
			for _, tupleStr := range tuples {
				vals := sqlutil.SplitTopLevel(tupleStr, ',')
				for i, v := range vals {
					if i >= len(cols) {
						break
					}
					if pIdx := extractParamIdxFromExpr(v, count); pIdx >= 1 {
						if oid := lookupColOID(cols[i]); oid != 0 && oids[pIdx-1] == 0 {
							oids[pIdx-1] = oid
						}
					}
				}
			}
		}
	}

	// B. UPDATE tbl SET col = expr
	if dml.Set != "" {
		for _, pair := range sqlutil.SplitSetClauses(dml.Set) {
			oid := lookupColOID(pair.Column)
			if oid == 0 {
				continue
			}
			for _, pIdx := range findAllParamIdxsInExpr(pair.Value, count) {
				if oids[pIdx-1] == 0 {
					oids[pIdx-1] = oid
				}
			}
		}
	}

	// C. WHERE clause comparisons against tbl columns (e.g. WHERE item_id = $1 AND (quantity - reserved_quantity) >= $2)
	if dml.Where != "" {
		for _, c := range tbl.Columns {
			colOID := typeToPgOID(c.Type)
			if colOID == 0 {
				continue
			}
			if !sqlutil.ContainsKeyword(dml.Where, c.Name, true) {
				continue
			}
			for _, clause := range sqlutil.SplitAndConditions(dml.Where) {
				if sqlutil.ContainsKeyword(clause, c.Name, true) {
					for _, pIdx := range findAllParamIdxsInExpr(clause, count) {
						if oids[pIdx-1] == 0 {
							oids[pIdx-1] = colOID
						}
					}
				}
			}
		}
	}
}

func inferExplicitCastParamTypes(query string, count int, oids []int32) {
	sqlutil.ScanPgParams(query, func(tok sqlutil.PgParamToken) {
		if tok.Index >= 1 && tok.Index <= count {
			rem := strings.TrimSpace(query[tok.End:])
			if strings.HasPrefix(rem, "::") {
				castRest := strings.TrimSpace(rem[2:])
				k := 0
				for k < len(castRest) && ((castRest[k] >= 'a' && castRest[k] <= 'z') || (castRest[k] >= 'A' && castRest[k] <= 'Z') || (castRest[k] >= '0' && castRest[k] <= '9') || castRest[k] == '_' || castRest[k] == '[' || castRest[k] == ']') {
					k++
				}
				if k > 0 {
					if castOID := castNameToPgOID(castRest[:k]); castOID != 0 {
						oids[tok.Index-1] = castOID
					}
				}
			}
		}
	})
}

func (s *Server) inferParamTypes(query string, count int) []int32 {
	oids := make([]int32, count)
	if count == 0 {
		return oids
	}

	// 0. Explicit PostgreSQL ::type casts (e.g. $4::text, $2::int4, $1::text[]) take highest priority
	inferExplicitCastParamTypes(query, count, oids)

	if s.engine == nil {
		return oids
	}

	// 1. Try CTE-aware DML / SELECT parameter resolution
	trimmed := strings.TrimSpace(query)
	if sqlutil.HasPrefixKeyword(trimmed, "WITH") {
		ctes, mainSQL := planner.ParseCTEs(trimmed)
		for _, cte := range ctes {
			s.inferStmtDMLParamTypes(cte.InnerSQL, count, oids)
		}
		if mainSQL != "" {
			s.inferStmtDMLParamTypes(mainSQL, count, oids)
		}
	} else {
		s.inferStmtDMLParamTypes(trimmed, count, oids)
	}

	// 2. For remaining unresolved parameters, use inferParamOID
	for i := 0; i < count; i++ {
		if oids[i] == 0 {
			oids[i] = inferParamOID(query, i+1)
		}
	}
	return oids
}

// InferParamOID resolves the parameter OID for a given parameter index in a SQL query.
func InferParamOID(query string, paramIndex int) int32 {
	return inferParamOID(query, paramIndex)
}

func inferParamOID(query string, paramIndex int) int32 {
	var resolvedOID int32

	sqlutil.ScanPgParams(query, func(token sqlutil.PgParamToken) {
		if resolvedOID != 0 || token.Index != paramIndex {
			return
		}
		rawPrefix := query[:token.Start]
		prefix := strings.TrimSpace(strings.ToUpper(rawPrefix))
		isAny := false
		if strings.HasSuffix(prefix, "(") {
			prefix = strings.TrimSpace(strings.TrimSuffix(prefix, "("))
		}
		if strings.HasSuffix(prefix, "ANY") {
			prefix = strings.TrimSpace(strings.TrimSuffix(prefix, "ANY"))
			isAny = true
		}
		if strings.HasSuffix(prefix, "=") || strings.HasSuffix(prefix, "<>") {
			prefix = strings.TrimSpace(prefix[:len(prefix)-1])
			tokens := strings.Fields(prefix)
			if len(tokens) > 0 {
				lastToken := tokens[len(tokens)-1]
				if dotIdx := strings.LastIndex(lastToken, "."); dotIdx != -1 {
					lastToken = lastToken[dotIdx+1:]
				}
				lastToken = strings.Trim(lastToken, "[]\"'`")
				if oid := LookupParamOID(lastToken, isAny); oid != 0 {
					resolvedOID = oid
				}
			}
		}
	})

	return resolvedOID
}

package engine

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/auth"
	"lionsql/pkg/storage"
)

// PKLookupPlan stores compiled plan metadata for direct storage bypass.
type PKLookupPlan struct {
	TemplateKey   uint64
	TableName     string
	PKColName     string
	PKColType     storage.DataType
	IsSelectAll   bool
	ResultColumns []string
	SchemaVersion uint64
}

// pkParsedQuery contains preliminary syntactic elements extracted from a fast-path candidate.
type pkParsedQuery struct {
	tableName    string
	colName      string
	literalVal   string
	isSelectAll  bool
	selectedCols []string
	templateKey  uint64
}

const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

// fnv1a64Fold calculates a 64-bit FNV-1a hash case-insensitively (ASCII fold).
func fnv1a64Fold(s string) uint64 {
	var h uint64 = fnvOffset64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		h ^= uint64(c)
		h *= fnvPrime64
	}
	return h
}

// isLiteral checks if a string represents a literal value rather than an identifier.
func isLiteral(s string) bool {
	if s == "" {
		return false
	}
	// Quoted string literals: 'abc', N'abc', "abc"
	if (strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 2) ||
		((strings.HasPrefix(s, "N'") || strings.HasPrefix(s, "n'")) && strings.HasSuffix(s, "'") && len(s) >= 3) {
		return true
	}
	// Numeric literals
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return true
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return true
	}
	if s[0] == '-' || (s[0] >= '0' && s[0] <= '9') {
		return true
	}
	return false
}

// cleanLiteral unquotes and unescapes a literal value string.
func cleanLiteral(s string) string {
	if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 2 {
		s = s[1 : len(s)-1]
		return strings.ReplaceAll(s, "''", "'")
	}
	if (strings.HasPrefix(s, "N'") || strings.HasPrefix(s, "n'")) && strings.HasSuffix(s, "'") && len(s) >= 3 {
		s = s[2 : len(s)-1]
		return strings.ReplaceAll(s, "''", "'")
	}
	return s
}

// splitTableAndAlias extracts the base table name and optional alias from a FROM clause table specification.
// e.g. "[mydb]..[accounts] a" -> "[mydb]..[accounts]", "a"
//      "accounts AS a" -> "accounts", "a"
//      "[dbo].[users]" -> "[dbo].[users]", ""
func splitTableAndAlias(tablePart string) (string, string) {
	tablePart = strings.TrimSpace(tablePart)
	upper := strings.ToUpper(tablePart)
	if idx := strings.Index(upper, " AS "); idx != -1 {
		return strings.TrimSpace(tablePart[:idx]), strings.TrimSpace(tablePart[idx+4:])
	}
	// If ends with bracket, quote, or backtick, it has no trailing alias
	if strings.HasSuffix(tablePart, "]") || strings.HasSuffix(tablePart, "\"") || strings.HasSuffix(tablePart, "`") {
		return tablePart, ""
	}
	// Find last space outside brackets
	depth := 0
	lastSpace := -1
	for i := 0; i < len(tablePart); i++ {
		switch tablePart[i] {
		case '[':
			depth++
		case ']':
			depth--
		case ' ', '\t':
			if depth == 0 {
				lastSpace = i
			}
		}
	}
	if lastSpace != -1 {
		tbl := strings.TrimSpace(tablePart[:lastSpace])
		alias := strings.TrimSpace(tablePart[lastSpace+1:])
		if tbl != "" && alias != "" {
			return tbl, alias
		}
	}
	return tablePart, ""
}

// parsePKLookupQuery performs fast zero-alloc/minimal-alloc syntax extraction for simple PK point lookups.
// Returns (parsedQuery, true) if the statement matches the simple single-PK point lookup pattern, or (nil, false) otherwise.
func parsePKLookupQuery(query string) (*pkParsedQuery, bool) {
	q := strings.TrimRight(strings.TrimSpace(query), ";")
	if len(q) < 14 { // Minimum length: "SELECT * FROM t WHERE id=1"
		return nil, false
	}

	// Must start with SELECT
	upperQ := strings.ToUpper(q)
	if !strings.HasPrefix(upperQ, "SELECT") || (len(q) > 6 && q[6] != ' ' && q[6] != '\t' && q[6] != '\n') {
		return nil, false
	}

	// Reject queries with complex construct characters
	if strings.Contains(q, "(") || strings.Contains(q, ")") {
		return nil, false
	}

	// Disallow complex SQL clauses
	if strings.Contains(upperQ, " JOIN ") ||
		strings.Contains(upperQ, " GROUP ") ||
		strings.Contains(upperQ, " HAVING ") ||
		strings.Contains(upperQ, " ORDER ") ||
		strings.Contains(upperQ, " UNION ") ||
		strings.Contains(upperQ, " EXCEPT ") ||
		strings.Contains(upperQ, " INTERSECT ") ||
		strings.Contains(upperQ, " INTO ") ||
		strings.Contains(upperQ, " AND ") ||
		strings.Contains(upperQ, " OR ") ||
		strings.Contains(upperQ, " LIKE ") ||
		strings.Contains(upperQ, " IN ") ||
		strings.Contains(upperQ, " BETWEEN ") ||
		strings.Contains(upperQ, " APPLY ") ||
		strings.Contains(upperQ, " JSON ") {
		return nil, false
	}

	// Strip optional LIMIT 1
	if strings.HasSuffix(upperQ, " LIMIT 1") {
		q = strings.TrimSpace(q[:len(q)-len(" LIMIT 1")])
		upperQ = strings.ToUpper(q)
	}

	// Check and strip optional TOP 1 after SELECT
	selectPrefix := "SELECT"
	afterSelect := strings.TrimSpace(q[6:])
	upperAfter := strings.ToUpper(afterSelect)
	if strings.HasPrefix(upperAfter, "TOP 1 ") || strings.HasPrefix(upperAfter, "TOP 1\t") {
		afterSelect = strings.TrimSpace(afterSelect[6:])
		upperAfter = strings.ToUpper(afterSelect)
	}

	// Find FROM keyword
	fromIdx := strings.Index(upperAfter, " FROM ")
	if fromIdx == -1 {
		return nil, false
	}

	projStr := strings.TrimSpace(afterSelect[:fromIdx])
	restAfterFrom := strings.TrimSpace(afterSelect[fromIdx+6:])
	upperRest := strings.ToUpper(restAfterFrom)

	// Find WHERE keyword
	whereIdx := strings.Index(upperRest, " WHERE ")
	if whereIdx == -1 {
		return nil, false
	}

	tablePart := strings.TrimSpace(restAfterFrom[:whereIdx])
	wherePart := strings.TrimSpace(restAfterFrom[whereIdx+7:])

	// Disallow multi-table FROM
	if strings.Contains(tablePart, ",") || tablePart == "" || projStr == "" || wherePart == "" {
		return nil, false
	}

	// Must have single '=' and no comparison operators like !=, <>, <=, >=, ==
	if strings.Contains(wherePart, "!=") ||
		strings.Contains(wherePart, "<>") ||
		strings.Contains(wherePart, "<=") ||
		strings.Contains(wherePart, ">=") ||
		strings.Contains(wherePart, "==") {
		return nil, false
	}

	eqIdx := strings.Index(wherePart, "=")
	if eqIdx == -1 {
		return nil, false
	}
	if strings.Index(wherePart[eqIdx+1:], "=") != -1 {
		return nil, false
	}

	leftPart := strings.TrimSpace(wherePart[:eqIdx])
	rightPart := strings.TrimSpace(wherePart[eqIdx+1:])
	if leftPart == "" || rightPart == "" {
		return nil, false
	}

	var colIdent, rawLiteral string
	if isLiteral(rightPart) && !isLiteral(leftPart) {
		colIdent = leftPart
		rawLiteral = rightPart
	} else if isLiteral(leftPart) && !isLiteral(rightPart) {
		colIdent = rightPart
		rawLiteral = leftPart
	} else {
		return nil, false
	}

	tableStr, _ := splitTableAndAlias(tablePart)
	cleanTable := cleanIdentifier(tableStr)
	cleanCol := cleanIdentifier(colIdent)
	if idx := strings.LastIndex(cleanCol, "."); idx != -1 {
		cleanCol = cleanCol[idx+1:]
	}

	cleanLit := cleanLiteral(rawLiteral)

	// Process Projections
	var isSelectAll bool
	var selectedCols []string
	if projStr == "*" {
		isSelectAll = true
	} else {
		rawCols := strings.Split(projStr, ",")
		for _, c := range rawCols {
			c = strings.TrimSpace(c)
			if c == "" || strings.Contains(strings.ToUpper(c), " AS ") {
				return nil, false
			}
			sc := cleanIdentifier(c)
			if idx := strings.LastIndex(sc, "."); idx != -1 {
				sc = sc[idx+1:]
			}
			selectedCols = append(selectedCols, sc)
		}
		if len(selectedCols) == 0 {
			return nil, false
		}
	}

	// Build normalized template for 64-bit fingerprint hashing
	var tpl strings.Builder
	tpl.WriteString(selectPrefix)
	tpl.WriteByte(' ')
	if isSelectAll {
		tpl.WriteByte('*')
	} else {
		tpl.WriteString(strings.Join(selectedCols, ","))
	}
	tpl.WriteString(" FROM ")
	tpl.WriteString(cleanTable)
	tpl.WriteString(" WHERE ")
	tpl.WriteString(cleanCol)
	tpl.WriteString(" = ?")

	tplKey := fnv1a64Fold(tpl.String())

	return &pkParsedQuery{
		tableName:    cleanTable,
		colName:      cleanCol,
		literalVal:   cleanLit,
		isSelectAll:  isSelectAll,
		selectedCols: selectedCols,
		templateKey:  tplKey,
	}, true
}

// parsePKValue converts literal string to typed storage.Value according to the target PK column type.
func parsePKValue(lit string, dt storage.DataType) (storage.Value, error) {
	switch dt {
	case storage.TypeInt:
		i, err := strconv.ParseInt(lit, 10, 64)
		if err != nil {
			return storage.Value{}, err
		}
		return storage.IntValue(i), nil
	case storage.TypeVarChar, storage.TypeJSON:
		return storage.StringValue(lit), nil
	case storage.TypeUUID:
		return storage.Value{Type: storage.TypeUUID, SVal: lit}, nil
	case storage.TypeFloat:
		f, err := strconv.ParseFloat(lit, 64)
		if err != nil {
			return storage.Value{}, err
		}
		return storage.FloatValue(f), nil
	default:
		return storage.Value{}, fmt.Errorf("unsupported PK data type: %v", dt)
	}
}

// tryPKLookupBypass attempts to execute simple single-PK queries by bypassing the full parser, CBO, and VDBE stack.
// Returns (result, err, true) if handled via fast-path storage bypass, or (nil, nil, false) to fall back.
func (s *Coordinator) tryPKLookupBypass(query string, start time.Time, cs *ClientSession) (*QueryResult, error, bool) {
	pq, ok := parsePKLookupQuery(query)
	if !ok {
		return nil, nil, false
	}

	currSchemaVer := s.schemaVersion.Load()

	s.pkBypassMu.RLock()
	plan, found := s.pkBypassCache[pq.templateKey]
	s.pkBypassMu.RUnlock()

	if !found || plan == nil || plan.SchemaVersion != currSchemaVer {
		// Verify table existence and metadata
		if s.isVirtualCatalogTable(pq.tableName) {
			return nil, nil, false
		}
		if s.catalog != nil {
			if v, err := s.catalog.GetView(pq.tableName); err == nil && v != nil {
				return nil, nil, false
			}
		}

		schema, err := s.engine.GetTable(pq.tableName)
		if err != nil || schema == nil {
			if errors.Is(err, storage.ErrTableNotFound) {
				return nil, fmt.Errorf("table %q not found: %w", pq.tableName, err), true
			}
			return nil, nil, false
		}

		pkCol, err := schema.PrimaryKeyColumn()
		if err != nil || pkCol == nil {
			return nil, nil, false
		}

		if !strings.EqualFold(pq.colName, pkCol.Name) {
			return nil, nil, false
		}

		var resultCols []string
		if pq.isSelectAll {
			resultCols = make([]string, len(schema.Columns))
			for i, c := range schema.Columns {
				resultCols[i] = c.Name
			}
		} else {
			resultCols = make([]string, len(pq.selectedCols))
			for i, sc := range pq.selectedCols {
				c, _, foundCol := schema.FindColumn(sc)
				if !foundCol {
					return nil, nil, false
				}
				resultCols[i] = c.Name
			}
		}

		plan = &PKLookupPlan{
			TemplateKey:   pq.templateKey,
			TableName:     schema.Name,
			PKColName:     pkCol.Name,
			PKColType:     pkCol.Type,
			IsSelectAll:   pq.isSelectAll,
			ResultColumns: resultCols,
			SchemaVersion: currSchemaVer,
		}

		s.pkBypassMu.Lock()
		if len(s.pkBypassCache) >= 2048 {
			s.pkBypassCache = make(map[uint64]*PKLookupPlan, 256)
		}
		s.pkBypassCache[pq.templateKey] = plan
		s.pkBypassMu.Unlock()
	}

	// RBAC table access validation
	if err := s.checkTableAccess(cs, plan.TableName, auth.PrivSelect); err != nil {
		return nil, err, true
	}

	// Convert literal to typed storage Value
	pkVal, err := parsePKValue(pq.literalVal, plan.PKColType)
	if err != nil {
		return nil, nil, false
	}

	// Acquire transaction handle
	var tx storage.Transaction
	if cs != nil && cs.Tx != nil {
		tx = cs.Tx
	} else {
		session, closeFn, err := s.acquireSessionForQuery(cs)
		if err != nil {
			return nil, nil, false
		}
		defer closeFn()

		t, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, nil, false
		}
		tx = t
		defer tx.Rollback()
	}

	// Query storage engine directly via primary key
	rec, err := tx.Get(plan.TableName, pkVal)
	if err != nil {
		if errors.Is(err, storage.ErrKeyNotFound) {
			return &QueryResult{
				Success:      true,
				Command:      "SELECT",
				Columns:      plan.ResultColumns,
				Rows:         []map[string]any{},
				RowsAffected: 0,
				DurationUS:   time.Since(start).Microseconds(),
			}, nil, true
		}
		if errors.Is(err, storage.ErrTableNotFound) {
			s.InvalidatePKBypassCache()
			return nil, fmt.Errorf("table %q not found: %w", plan.TableName, err), true
		}
		return nil, nil, false
	}

	// Format result row according to projected columns
	row := make(map[string]any, len(plan.ResultColumns))
	for _, colName := range plan.ResultColumns {
		row[colName] = formatStorageValue(rec[colName])
	}

	if s.metrics != nil {
		s.metrics.RecordPKBypassHit()
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      plan.ResultColumns,
		Rows:         []map[string]any{row},
		RowsAffected: 1,
		DurationUS:   time.Since(start).Microseconds(),
	}, nil, true
}

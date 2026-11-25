package engine

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

// PointLookupCandidate contains pre-parsed components of a single-table PK point lookup.
type PointLookupCandidate struct {
	TableName    string
	PKColumn     string
	ParamMarker  string // e.g. "$1", "?", "@p1", or empty if literal
	LiteralValue string // string or numeric literal if not parameterized
	Projections  []string
}

var disallowedPointLookupKeywords = []string{
	"JOIN", "GROUP BY", "ORDER BY", "HAVING", "LIMIT", "TOP",
	"UNION", "INTERSECT", "EXCEPT", "WITH", "DISTINCT",
}

// ParsePointLookupCandidate inspects an SQL query to determine if it is a pure single-table
// point lookup on an equality predicate (e.g. SELECT * FROM tbl WHERE pk = $1 or WHERE pk = 123).
//
// Conforms strictly to [Invariant 7: Zero-Regex] and [RULE:RadicalKISS].
func ParsePointLookupCandidate(query string) *PointLookupCandidate {
	trimmed := strings.TrimRight(strings.TrimSpace(query), ";")
	if !sqlutil.HasPrefixKeyword(trimmed, "SELECT") {
		return nil
	}

	upper := strings.ToUpper(trimmed)
	for _, kw := range disallowedPointLookupKeywords {
		if sqlutil.ContainsKeyword(upper, kw, true) {
			return nil
		}
	}

	fromIdx := sqlutil.FindTopLevelKeyword(trimmed, "FROM")
	if fromIdx == -1 {
		return nil
	}

	projStr := strings.TrimSpace(trimmed[6:fromIdx])
	if projStr == "" {
		return nil
	}

	afterFrom := strings.TrimSpace(trimmed[fromIdx+4:])
	whereIdx := sqlutil.FindTopLevelKeyword(afterFrom, "WHERE")
	if whereIdx == -1 {
		return nil
	}

	tablePart := strings.TrimSpace(afterFrom[:whereIdx])
	if tablePart == "" || sqlutil.FindTopLevelChar(tablePart, ',') != -1 {
		return nil
	}

	// Reject subqueries in FROM e.g. FROM (SELECT ...)
	if strings.HasPrefix(tablePart, "(") {
		return nil
	}

	wherePart := strings.TrimSpace(afterFrom[whereIdx+5:])
	if wherePart == "" {
		return nil
	}

	// Disallow complex boolean disjunctions and range/set operators in WHERE
	disallowedWhereKeywords := []string{"AND", "OR", "LIKE", "IN", "BETWEEN", "IS", "NOT"}
	for _, kw := range disallowedWhereKeywords {
		if sqlutil.ContainsKeyword(wherePart, kw, true) {
			return nil
		}
	}

	if sqlutil.FindTopLevelChar(wherePart, '<') != -1 ||
		sqlutil.FindTopLevelChar(wherePart, '>') != -1 ||
		sqlutil.FindTopLevelChar(wherePart, '!') != -1 {
		return nil
	}

	eqIdx := sqlutil.FindTopLevelChar(wherePart, '=')
	if eqIdx == -1 {
		return nil
	}
	if sqlutil.FindTopLevelChar(wherePart[eqIdx+1:], '=') != -1 {
		return nil
	}

	leftPart := strings.TrimSpace(wherePart[:eqIdx])
	rightPart := strings.TrimSpace(wherePart[eqIdx+1:])
	if leftPart == "" || rightPart == "" {
		return nil
	}

	// Normalize table name and optional alias
	tableFields := strings.Fields(tablePart)
	tblName := tableFields[0]
	tblAlias := ""
	if len(tableFields) == 2 {
		tblAlias = tableFields[1]
	} else if len(tableFields) == 3 && strings.EqualFold(tableFields[1], "AS") {
		tblAlias = tableFields[2]
	} else if len(tableFields) > 3 {
		return nil
	}

	// Identify which side is column identifier and which side is parameter / value
	var colPart, valPart string
	if isParameterOrLiteral(rightPart) && !isParameterOrLiteral(leftPart) {
		colPart = leftPart
		valPart = rightPart
	} else if isParameterOrLiteral(leftPart) && !isParameterOrLiteral(rightPart) {
		colPart = rightPart
		valPart = leftPart
	} else {
		// Fallback check
		colPart = leftPart
		valPart = rightPart
	}

	cleanCol := cleanIdentifier(colPart)
	// Strip optional table/alias qualifier: e.g. u.id -> id
	if dotIdx := strings.LastIndex(cleanCol, "."); dotIdx != -1 {
		qual := cleanCol[:dotIdx]
		if !strings.EqualFold(qual, cleanIdentifier(tblName)) && (tblAlias == "" || !strings.EqualFold(qual, cleanIdentifier(tblAlias))) {
			return nil
		}
		cleanCol = cleanCol[dotIdx+1:]
	}

	// Split projections
	rawProjs := sqlutil.SplitTopLevel(projStr, ',')
	var projections []string
	for _, p := range rawProjs {
		pt := strings.TrimSpace(p)
		if pt == "" {
			continue
		}
		fnName := pt
		if openParen := strings.Index(pt, "("); openParen != -1 {
			fnName = strings.TrimSpace(pt[:openParen])
		}
		if proc.IsAggregateFunctionName(fnName) {
			return nil
		}
		projections = append(projections, pt)
	}

	cand := &PointLookupCandidate{
		TableName:   cleanIdentifier(tblName),
		PKColumn:    cleanCol,
		Projections: projections,
	}

	if isParamMarker(valPart) {
		cand.ParamMarker = strings.ToUpper(valPart)
	} else {
		cand.LiteralValue = valPart
	}

	return cand
}

func isParameterOrLiteral(s string) bool {
	if isParamMarker(s) {
		return true
	}
	if (strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 2) ||
		(strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") && len(s) >= 2) {
		return true
	}
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return true
	}
	return false
}

func isParamMarker(s string) bool {
	if s == "?" {
		return true
	}
	if strings.HasPrefix(s, "$") && len(s) > 1 {
		if _, err := strconv.Atoi(s[1:]); err == nil {
			return true
		}
	}
	if strings.HasPrefix(s, "@") && len(s) > 1 {
		return true
	}
	return false
}

// ExecutePointLookup executes a direct primary key point lookup bypassing Volcano CBO,
// planner AST allocation, and iterator state machines.
//
// Conforms to [RULE:ZeroAllocation], [RULE:RadicalKISS], and [Invariant 18: ZeroParameterSniffing].
func (s *Coordinator) ExecutePointLookup(
	sessID string,
	tableName string,
	pkVal storage.Value,
	projections []string,
) (*QueryResult, error) {
	start := time.Now()

	cs, err := s.GetOrCreateSession(sessID)
	if err != nil {
		return nil, err
	}

	cleanTable := cleanIdentifier(tableName)
	schema, err := s.getTable(cs, cleanTable)
	if err != nil {
		return nil, fmt.Errorf("table %q not found: %w", tableName, err)
	}

	if !schema.HasPrimaryKey() {
		return nil, fmt.Errorf("table %q has no primary key defined for point lookup", tableName)
	}

	if schema.IsCompositePK() {
		return nil, fmt.Errorf("composite primary key point lookup is not supported on direct fast-path")
	}

	// Determine transaction context:
	// If session has an active transaction (explicit BEGIN), reuse it.
	// Otherwise, run within an ephemeral autocommit read-only transaction.
	cs.Mu.Lock()
	var tx storage.Transaction
	var autocommitSess storage.Session
	if cs.Tx() != nil {
		tx = cs.Tx()
	}
	cs.Mu.Unlock()

	if tx == nil {
		if s.engine == nil {
			return nil, fmt.Errorf("no storage engine configured")
		}
		var sessErr error
		autocommitSess, sessErr = s.engine.NewSession()
		if sessErr != nil {
			return nil, fmt.Errorf("failed to create storage session: %w", sessErr)
		}
		defer autocommitSess.Close()

		var beginErr error
		tx, beginErr = autocommitSess.BeginTx(storage.TxOptions{ReadOnly: true})
		if beginErr != nil {
			return nil, fmt.Errorf("failed to begin autocommit transaction: %w", beginErr)
		}
		defer func() {
			_ = tx.Rollback()
		}()
	}

	// Physical table point lookup
	row, getErr := tx.GetRow(schema.Name, pkVal)
	if getErr != nil && !errors.Is(getErr, storage.ErrKeyNotFound) {
		return nil, fmt.Errorf("point lookup error: %w", getErr)
	}

	// Prepare column metadata
	cols := make([]string, 0, len(projections))
	var colIndices []int

	isWildcard := len(projections) == 0 || (len(projections) == 1 && strings.TrimSpace(projections[0]) == "*")
	if isWildcard {
		for idx, col := range schema.Columns {
			cols = append(cols, col.Name)
			colIndices = append(colIndices, idx)
		}
	} else {
		for _, proj := range projections {
			projTrim := strings.TrimSpace(proj)
			if projTrim == "*" {
				for idx, col := range schema.Columns {
					cols = append(cols, col.Name)
					colIndices = append(colIndices, idx)
				}
				continue
			}
			expr, alias, hasAs := sqlutil.SplitAsAlias(projTrim)
			var colTarget, aliasTarget string
			if hasAs {
				colTarget = cleanIdentifier(expr)
				aliasTarget = cleanIdentifier(alias)
			} else {
				colTarget = cleanIdentifier(projTrim)
				aliasTarget = colTarget
			}
			_, idx, ok := schema.FindColumn(colTarget)
			if !ok {
				return nil, fmt.Errorf("column %q does not exist in table %q", colTarget, tableName)
			}
			cols = append(cols, aliasTarget)
			colIndices = append(colIndices, idx)
		}
	}

	// If key not found or row empty, return 0 rows
	if errors.Is(getErr, storage.ErrKeyNotFound) || len(row.Values) == 0 {
		return &QueryResult{
			Success:      true,
			Command:      "SELECT",
			Columns:      cols,
			Rows:         nil,
			Values:       nil,
			RowsAffected: 0,
			DurationUS:   time.Since(start).Microseconds(),
		}, nil
	}

	// Materialize row
	rowMap := make(map[string]any, len(cols))
	vals := make([]any, len(cols))
	for i, colIdx := range colIndices {
		if colIdx >= 0 && colIdx < len(row.Values) {
			v := row.Values[colIdx]
			anyVal := v.Interface()
			rowMap[cols[i]] = anyVal
			vals[i] = anyVal
		} else {
			rowMap[cols[i]] = nil
			vals[i] = nil
		}
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      cols,
		Rows:         []map[string]any{rowMap},
		Values:       [][]any{vals},
		RowsAffected: 1,
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}

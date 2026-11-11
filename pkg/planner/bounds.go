package planner

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

// extractSimpleColIdent returns a clean column name if expr represents a column reference (e.g. col or t.col).
func extractSimpleColIdent(expr proc.Expr) (string, bool) {
	if expr == nil {
		return "", false
	}
	switch e := expr.(type) {
	case *proc.ColumnRefExpr:
		return e.Name, true
	case *proc.LiteralExpr:
		return "", false
	default:
		s := strings.TrimSpace(e.String())
		if s == "" || strings.ContainsAny(s, " ()+-*/=><!&|;") {
			return "", false
		}
		return s, true
	}
}

// extractExprRawVal extracts a string representation of an expression's RHS value, stripping outer quotes if present.
func extractExprRawVal(expr proc.Expr) string {
	if expr == nil {
		return ""
	}
	if lit, ok := expr.(*proc.LiteralExpr); ok {
		if lit.Val.IsNull {
			return "NULL"
		}
		return lit.Val.String()
	}
	return strings.Trim(strings.TrimSpace(expr.String()), "'\"")
}

func hasColRef(expr proc.Expr) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *proc.ColumnRefExpr:
		return true
	case *proc.UnaryExpr:
		return hasColRef(e.Expr)
	case *proc.BinaryExpr:
		return hasColRef(e.Left) || hasColRef(e.Right)
	case *proc.FunctionCallExpr:
		for _, a := range e.Args {
			if hasColRef(a) {
				return true
			}
		}
		return false
	case *proc.CastExpr:
		return hasColRef(e.Expr)
	case *proc.BetweenExpr:
		return hasColRef(e.Expr) || hasColRef(e.Low) || hasColRef(e.High)
	case *proc.InExpr:
		if hasColRef(e.Expr) {
			return true
		}
		for _, it := range e.List {
			if hasColRef(it) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func isNullExpr(expr proc.Expr) bool {
	if expr == nil {
		return true
	}
	if lit, ok := expr.(*proc.LiteralExpr); ok && lit.Val.IsNull {
		return true
	}
	s := strings.TrimSpace(expr.String())
	return strings.EqualFold(s, "NULL")
}

// parseASTComparison parses a single condition string using the Pratt AST parser
// and returns (rawCol, op, rawVal, ok) if it is a binary comparison or LIKE on a column.
func parseASTComparison(cond string) (rawCol string, op string, rawVal string, ok bool) {
	p := proc.NewParser(proc.NewLexer(cond))
	expr := p.ParseExpr()
	if expr == nil || len(p.Errors()) > 0 {
		return "", "", "", false
	}
	switch e := expr.(type) {
	case *proc.BinaryExpr:
		if isNullExpr(e.Left) || isNullExpr(e.Right) {
			return "", "", "", false
		}
		var opStr, flippedOp string
		switch e.Op {
		case proc.TokenEq, proc.TokenAssign:
			opStr, flippedOp = "=", "="
		case proc.TokenLt:
			opStr, flippedOp = "<", ">"
		case proc.TokenLte:
			opStr, flippedOp = "<=", ">="
		case proc.TokenGt:
			opStr, flippedOp = ">", "<"
		case proc.TokenGte:
			opStr, flippedOp = ">=", "<="
		case proc.TokenLike:
			opStr = "LIKE"
		default:
			return "", "", "", false
		}
		if col, isCol := extractSimpleColIdent(e.Left); isCol && !hasColRef(e.Right) {
			return col, opStr, strings.TrimSpace(e.Right.String()), true
		}
		if flippedOp != "" {
			if col, isCol := extractSimpleColIdent(e.Right); isCol && !hasColRef(e.Left) {
				return col, flippedOp, strings.TrimSpace(e.Left.String()), true
			}
		}
	case *proc.LikeExpr:
		if !e.Not {
			if col, isCol := extractSimpleColIdent(e.Expr); isCol && !hasColRef(e.Pattern) {
				return col, "LIKE", strings.TrimSpace(e.Pattern.String()), true
			}
		}
	}
	return "", "", "", false
}

// IndexRangeCond stores lower and upper bounds for an index range scan.
type IndexRangeCond struct {
	HasMin bool
	MinVal string
	HasMax bool
	MaxVal string
}

// CleanIdentifier strips database/schema qualification, brackets, and quotes from identifiers, returning lower-cased base column name.
func CleanIdentifier(s string) string {
	raw := strings.TrimSpace(s)
	if dot := strings.LastIndex(raw, "."); dot != -1 {
		raw = raw[dot+1:]
	}
	return strings.ToLower(strings.Trim(raw, "\"`[]"))
}

// SplitOnAnd splits a WHERE clause on top-level AND keywords,
// respecting parentheses depth, string literals, and BETWEEN ... AND constructs.
func SplitOnAnd(where string) []string {
	where = strings.TrimSpace(where)
	if where == "" {
		return nil
	}
	if sqlutil.HasTopLevelOr(where) {
		return []string{where}
	}
	var parts []string
	depth := 0
	start := 0
	inString := false
	var quoteChar byte
	inBetween := false

	upper := strings.ToUpper(where)
	for i := 0; i < len(upper); i++ {
		c := where[i]
		if inString {
			if c == quoteChar {
				inString = false
			}
			continue
		}
		if c == '\'' || c == '"' {
			inString = true
			quoteChar = c
			continue
		}

		switch c {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}

		if depth == 0 {
			// Check for BETWEEN keyword
			if i+7 <= len(upper) && (i == 0 || sqlutil.IsWordBoundary(upper[i-1])) && upper[i:i+7] == "BETWEEN" && (i+7 == len(upper) || sqlutil.IsWordBoundary(upper[i+7])) {
				inBetween = true
				i += 6
				continue
			}

			// Check for AND keyword
			if i+3 <= len(upper) && (i == 0 || sqlutil.IsWordBoundary(upper[i-1])) && (upper[i:i+3] == "AND" && (i+3 == len(upper) || sqlutil.IsWordBoundary(upper[i+3]))) {
				if inBetween {
					// This AND belongs to BETWEEN ... AND ...
					inBetween = false
					i += 2
					continue
				}
				parts = append(parts, strings.TrimSpace(where[start:i]))
				i += 3
				start = i
			}
		}
	}
	if start < len(where) {
		trimmed := strings.TrimSpace(where[start:])
		if trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}

// stripEnclosingParens removes matched outer parenthesis layers without damaging split sub-expressions.
func stripEnclosingParens(s string) string {
	for {
		s = strings.TrimSpace(s)
		if !strings.HasPrefix(s, "(") || !strings.HasSuffix(s, ")") {
			return s
		}
		depth := 0
		closedEarly := false
		for i := 0; i < len(s)-1; i++ {
			if s[i] == '(' {
				depth++
			} else if s[i] == ')' {
				depth--
				if depth == 0 {
					closedEarly = true
					break
				}
			}
		}
		if closedEarly {
			return s
		}
		s = s[1 : len(s)-1]
	}
}

// ExtractIndexLeadingColumn extracts the leading indexed column name from an IndexCond string.
// e.g. "(order_id > 100)" -> "order_id", "([orders].[id] = 5)" -> "id", "(valid = 1)" -> "valid"
func ExtractIndexLeadingColumn(cond string) string {
	cond = strings.Trim(strings.TrimSpace(cond), "()")
	cond = strings.TrimSpace(cond)
	if cond == "" {
		return ""
	}
	idx := strings.IndexAny(cond, " \t\r\n=><!")
	first := cond
	if idx != -1 {
		first = cond[:idx]
	}
	if dot := strings.LastIndex(first, "."); dot != -1 {
		first = first[dot+1:]
	}
	return strings.ToLower(strings.Trim(first, "\"`[]"))
}

// HasDisjunction reports whether an expression contains an OR operator outside
// string literals, comments, and identifiers.
func HasDisjunction(expr string) bool {
	return sqlutil.ContainsKeyword(expr, "OR", true)
}

func compareBoundStrings(a, b string) int {
	if fa, errA := strconv.ParseFloat(a, 64); errA == nil {
		if fb, errB := strconv.ParseFloat(b, 64); errB == nil {
			if fa < fb {
				return -1
			}
			if fa > fb {
				return 1
			}
			return 0
		}
	}
	return strings.Compare(a, b)
}

// ParseWhereConditions extracts equality conditions and range conditions from a WHERE clause.
// Returns eqs: map[col_name]value_string, ranges: map[col_name]*IndexRangeCond.
func ParseWhereConditions(where string) (map[string]string, map[string]*IndexRangeCond) {
	eqs := make(map[string]string)
	ranges := make(map[string]*IndexRangeCond)
	if sqlutil.HasTopLevelOr(where) {
		return eqs, ranges
	}
	parts := SplitOnAnd(where)

	cleanVal := func(s string) string {
		return strings.Trim(strings.TrimSpace(s), "'\"")
	}

	setMinBound := func(col, val string) {
		r, ok := ranges[col]
		if !ok {
			r = &IndexRangeCond{}
			ranges[col] = r
		}
		if !r.HasMin || compareBoundStrings(val, r.MinVal) > 0 {
			r.HasMin = true
			r.MinVal = val
		}
	}

	setMaxBound := func(col, val string) {
		r, ok := ranges[col]
		if !ok {
			r = &IndexRangeCond{}
			ranges[col] = r
		}
		if !r.HasMax || compareBoundStrings(val, r.MaxVal) < 0 {
			r.HasMax = true
			r.MaxVal = val
		}
	}

	for _, part := range parts {
		part = stripEnclosingParens(strings.TrimSpace(part))
		if HasDisjunction(part) {
			continue
		}
		p := proc.NewParser(proc.NewLexer(part))
		rootExpr := p.ParseExpr()
		if rootExpr == nil || len(p.Errors()) > 0 {
			continue
		}
		for _, expr := range splitASTAndExpr(rootExpr) {
			switch e := expr.(type) {
			case *proc.BinaryExpr:
				op := e.Op
				colRaw, isCol := extractSimpleColIdent(e.Left)
				rightExpr := e.Right
				if !isCol {
					if rCol, rIsCol := extractSimpleColIdent(e.Right); rIsCol {
						colRaw = rCol
						isCol = true
						rightExpr = e.Left
						switch op {
						case proc.TokenLt:
							op = proc.TokenGt
						case proc.TokenLte:
							op = proc.TokenGte
						case proc.TokenGt:
							op = proc.TokenLt
						case proc.TokenGte:
							op = proc.TokenLte
						}
					}
				}
				if !isCol {
					continue
				}
				col := CleanIdentifier(colRaw)
				val := cleanVal(extractExprRawVal(rightExpr))
				switch op {
				case proc.TokenEq, proc.TokenAssign:
					eqs[col] = val
				case proc.TokenGte, proc.TokenGt:
					setMinBound(col, val)
				case proc.TokenLte, proc.TokenLt:
					setMaxBound(col, val)
				}
			case *proc.BetweenExpr:
				if e.Not {
					continue
				}
				colRaw, isCol := extractSimpleColIdent(e.Expr)
				if !isCol {
					continue
				}
				col := CleanIdentifier(colRaw)
				setMinBound(col, cleanVal(extractExprRawVal(e.Low)))
				setMaxBound(col, cleanVal(extractExprRawVal(e.High)))
			}
		}
	}
	return eqs, ranges
}

// BuildCompositePrefixKey builds a \x00-separated composite key string from the
// prefix columns of a composite index given an equality-condition value map.
func BuildCompositePrefixKey(cols []string, eqColVals map[string]string, schema *storage.TableSchema) string {
	var sb strings.Builder
	sb.Grow(64)
	for i, col := range cols {
		val, ok := eqColVals[strings.ToLower(col)]
		if !ok {
			break
		}
		if i > 0 {
			sb.WriteByte(0)
		}
		// Format the value using storage.EncodeIndexKeyPart for consistent composite key encoding.
		colMeta, _, found := schema.FindColumn(col)
		if found && colMeta != nil {
			sv := ParseValueToType(val, colMeta.Type)
			sb.WriteString(storage.EncodeIndexKeyPart(sv))
		} else {
			sb.WriteString(storage.EncodeIndexKeyPart(storage.StringValue(val)))
		}
	}
	return sb.String()
}

// ParseValueToType converts a string literal representation into a typed storage.Value.
func ParseValueToType(s string, t storage.DataType) storage.Value {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "NULL") {
		return storage.NullValue(t)
	}
	s = strings.Trim(s, "'\"")
	switch t {
	case storage.TypeInt:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return storage.IntValue(n)
		}
	case storage.TypeFloat:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return storage.FloatValue(f)
		}
	case storage.TypeDecimal:
		return storage.DecimalValue(s)
	case storage.TypeBool:
		if b, err := strconv.ParseBool(s); err == nil {
			return storage.BoolValue(b)
		}
	case storage.TypeDateTime:
		if tVal, err := time.Parse(time.RFC3339, s); err == nil {
			return storage.DateTimeValue(tVal)
		}
	case storage.TypeDate:
		return storage.DateValueFromString(s)
	case storage.TypeTime:
		return storage.TimeValueFromString(s)
	case storage.TypeUUID:
		return storage.UUIDValue(s)
	case storage.TypeBinary:
		return storage.BinaryValueFromString(s)
	}
	return storage.StringValue(s)
}

// FindIndexAndBounds finds the best candidate index for a table schema given a WHERE clause,
// returning the matched IndexSchema, lowVal, highVal, and ok=true if an index scan can be used.
func FindIndexAndBounds(schema *storage.TableSchema, whereClause string) (*storage.IndexSchema, *storage.Value, *storage.Value, bool) {
	if schema == nil || len(schema.Indexes) == 0 || whereClause == "" {
		return nil, nil, nil, false
	}
	if sqlutil.HasTopLevelOr(whereClause) {
		return nil, nil, nil, false
	}
	eqColVals, rangeCols := ParseWhereConditions(whereClause)
	eqColSet := make(map[string]bool, len(eqColVals))
	for col := range eqColVals {
		eqColSet[col] = true
	}

	// 1. Try composite index first
	idx, prefixLen := schema.FindBestCompositeIndex(eqColSet)
	if idx != nil && len(idx.Columns) > 1 {
		if prefixLen > 0 {
			prefixKey := BuildCompositePrefixKey(idx.Columns[:prefixLen], eqColVals, schema)
			// Check if next column has a range condition
			if prefixLen < len(idx.Columns) {
				nextCol := strings.ToLower(idx.Columns[prefixLen])
				if r, ok := rangeCols[nextCol]; ok && r != nil {
					nextMeta, _, found := schema.FindColumn(nextCol)
					if found && nextMeta != nil {
						lowStr := prefixKey
						highStr := prefixKey + "\xff\xff\xff\xff"
						if r.HasMin {
							encMin := storage.EncodeIndexKeyPart(ParseValueToType(r.MinVal, nextMeta.Type))
							lowStr = prefixKey + "\x00" + encMin
						}
						if r.HasMax {
							encMax := storage.EncodeIndexKeyPart(ParseValueToType(r.MaxVal, nextMeta.Type))
							highStr = prefixKey + "\x00" + encMax + "\xff\xff\xff\xff"
						}
						lowVal := storage.StringValue(lowStr)
						highVal := storage.StringValue(highStr)
						return idx, &lowVal, &highVal, true
					}
				}
			}
			// Pure prefix match on composite index
			lowVal := storage.StringValue(prefixKey)
			highVal := storage.StringValue(prefixKey + "\xff\xff\xff\xff")
			return idx, &lowVal, &highVal, true
		}
	}

	// 2. Try single-column index
	for i := range schema.Indexes {
		candIdx := &schema.Indexes[i]
		if len(candIdx.Columns) != 1 {
			continue
		}
		colName := strings.ToLower(candIdx.Columns[0])
		colMeta, _, found := schema.FindColumn(colName)
		if !found || colMeta == nil {
			continue
		}
		// Check equality
		if valStr, ok := eqColVals[colName]; ok {
			parsed := ParseValueToType(valStr, colMeta.Type)
			return candIdx, &parsed, &parsed, true
		}
		// Check range
		if r, ok := rangeCols[colName]; ok && r != nil {
			var lowVal, highVal *storage.Value
			if r.HasMin {
				v := ParseValueToType(r.MinVal, colMeta.Type)
				lowVal = &v
			}
			if r.HasMax {
				v := ParseValueToType(r.MaxVal, colMeta.Type)
				highVal = &v
			}
			return candIdx, lowVal, highVal, true
		}
	}

	// 3. Try functional expression index
	for i := range schema.Indexes {
		candIdx := &schema.Indexes[i]
		if !candIdx.IsExpression() {
			continue
		}
		targetCol := candIdx.ExprColumn
		if targetCol == "" && len(candIdx.Columns) > 0 {
			targetCol = candIdx.Columns[0]
		}
		normCandPath := strings.TrimSpace(candIdx.ExprPath)
		if !strings.HasPrefix(normCandPath, "$") {
			if strings.HasPrefix(normCandPath, "[") {
				normCandPath = "$" + normCandPath
			} else {
				normCandPath = "$." + normCandPath
			}
		}

		conds := sqlutil.SplitAndConditions(whereClause)
		for _, cond := range conds {
			cond = strings.TrimSpace(cond)
			p := proc.NewParser(proc.NewLexer(cond))
			if expr := p.ParseExpression(proc.PrecLowest); expr != nil {
				if bin, ok := expr.(*proc.BinaryExpr); ok && (bin.Op == proc.TokenAssign || bin.Op == proc.TokenEq) {
					colName, jsonPath, isJSON := proc.ExtractJSONExprInfo(bin.Left)
					if isJSON {
						if dot := strings.Index(colName, "."); dot != -1 {
							colName = colName[dot+1:]
						}
						if strings.EqualFold(colName, targetCol) && strings.EqualFold(jsonPath, normCandPath) {
							if lit, isLit := bin.Right.(*proc.LiteralExpr); isLit {
								val := lit.Val
								return candIdx, &val, &val, true
							}
							valStr := strings.Trim(strings.TrimSpace(bin.Right.String()), "'\"")
							val := storage.StringValue(valStr)
							return candIdx, &val, &val, true
						}
					}
				}
			}
		}
	}

	return nil, nil, nil, false
}

// ParseIndexCond parses an index condition string (which can be a single condition or AND-separated conditions)
// and returns the lower and upper bounds for an index scan.
func ParseIndexCond(cond string, schema *storage.TableSchema, targetIndexName ...string) (*storage.Value, *storage.Value) {
	cond = stripEnclosingParens(strings.TrimSpace(cond))
	if cond == "" {
		return nil, nil
	}

	// 1. Try functional expression index condition first: e.g. "doc ->> 'city' = 'Seoul'" or "JSON_VALUE(doc, '$.city') = 'Seoul'"
	p := proc.NewParser(proc.NewLexer(cond))
	if expr := p.ParseExpression(proc.PrecLowest); expr != nil {
		if bin, ok := expr.(*proc.BinaryExpr); ok && (bin.Op == proc.TokenAssign || bin.Op == proc.TokenEq) {
			colName, _, isJSON := proc.ExtractJSONExprInfo(bin.Left)
			if isJSON || colName != "" {
				if lit, isLit := bin.Right.(*proc.LiteralExpr); isLit {
					v := lit.Val
					return &v, &v
				}
				valStr := strings.Trim(strings.TrimSpace(bin.Right.String()), "'\"")
				v := storage.StringValue(valStr)
				return &v, &v
			}
		}
	}

	// 2. Parse general WHERE conditions (supporting =, >=, >, <=, <, BETWEEN)
	eqColVals, rangeCols := ParseWhereConditions(cond)

	// Helper to extract bounds for a specific column
	extractColBounds := func(colName string) (*storage.Value, *storage.Value, bool) {
		lowerCol := strings.ToLower(colName)
		if valStr, ok := eqColVals[lowerCol]; ok {
			if schema != nil {
				if colMeta, _, found := schema.FindColumn(colName); found && colMeta != nil {
					parsed := ParseValueToType(valStr, colMeta.Type)
					return &parsed, &parsed, true
				}
			}
			v := storage.StringValue(valStr)
			return &v, &v, true
		}
		if r, ok := rangeCols[lowerCol]; ok && r != nil {
			var lowVal, highVal *storage.Value
			var colType storage.DataType = storage.TypeVarChar
			if schema != nil {
				if colMeta, _, found := schema.FindColumn(colName); found && colMeta != nil {
					colType = colMeta.Type
				}
			}
			if r.HasMin {
				v := ParseValueToType(r.MinVal, colType)
				lowVal = &v
			}
			if r.HasMax {
				v := ParseValueToType(r.MaxVal, colType)
				highVal = &v
			}
			if lowVal != nil || highVal != nil {
				return lowVal, highVal, true
			}
		}
		return nil, nil, false
	}

	// A. If targetIndexName is explicitly specified, prioritize columns of that index
	if len(targetIndexName) > 0 && targetIndexName[0] != "" && schema != nil {
		var targetIdx *storage.IndexSchema
		if idx, _, found := schema.FindIndex(targetIndexName[0]); found && idx != nil {
			targetIdx = idx
		} else {
			for i := range schema.Indexes {
				if strings.EqualFold(schema.Indexes[i].Name, targetIndexName[0]) {
					targetIdx = &schema.Indexes[i]
					break
				}
			}
		}
		if targetIdx != nil {
			if len(targetIdx.Columns) > 1 {
				var prefixCols []string
				for _, col := range targetIdx.Columns {
					if _, ok := eqColVals[strings.ToLower(col)]; ok {
						prefixCols = append(prefixCols, col)
					} else {
						break
					}
				}
				if len(prefixCols) > 0 {
					prefixKey := BuildCompositePrefixKey(prefixCols, eqColVals, schema)
					lowVal := storage.StringValue(prefixKey)
					highVal := storage.StringValue(prefixKey + "\xff\xff\xff\xff")
					return &lowVal, &highVal
				}
			}
			for _, col := range targetIdx.Columns {
				if low, high, found := extractColBounds(col); found {
					return low, high
				}
			}
		}
	}

	// B. If schema has indexes, prioritize columns belonging to indexes
	if schema != nil && len(schema.Indexes) > 0 {
		for i := range schema.Indexes {
			for _, col := range schema.Indexes[i].Columns {
				if low, high, found := extractColBounds(col); found {
					return low, high
				}
			}
		}
	}

	// C. Deterministic fallback: sort column names to prevent random map iteration
	eqCols := make([]string, 0, len(eqColVals))
	for colName := range eqColVals {
		eqCols = append(eqCols, colName)
	}
	sort.Strings(eqCols)
	for _, colName := range eqCols {
		if low, high, found := extractColBounds(colName); found {
			return low, high
		}
	}

	rangeColNames := make([]string, 0, len(rangeCols))
	for colName := range rangeCols {
		rangeColNames = append(rangeColNames, colName)
	}
	sort.Strings(rangeColNames)
	for _, colName := range rangeColNames {
		if low, high, found := extractColBounds(colName); found {
			return low, high
		}
	}

	// 3. Simple fallback for "col = val" if regex or parser missed it
	if eqIdx := strings.LastIndex(cond, "="); eqIdx != -1 {
		valStr := strings.Trim(strings.TrimSpace(cond[eqIdx+1:]), "'\"")
		v := storage.StringValue(valStr)
		return &v, &v
	}

	return nil, nil
}

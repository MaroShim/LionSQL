package planner

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"sqlion/pkg/proc"
	"sqlion/pkg/storage"
)

var (
	eqBoundRe      = regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s*=\s*(.+?)\s*$`)
	gteBoundRe     = regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s*>=\s*(.+?)\s*$`)
	gtBoundRe      = regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s*>\s*(.+?)\s*$`)
	lteBoundRe     = regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s*<=\s*(.+?)\s*$`)
	ltBoundRe      = regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s*<\s*(.+?)\s*$`)
	betweenBoundRe = regexp.MustCompile(`(?i)^\s*([a-zA-Z0-9_.]+)\s+BETWEEN\s+(.+?)\s+AND\s+(.+?)\s*$`)
)

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
			if i+7 <= len(upper) && (i == 0 || upper[i-1] == ' ' || upper[i-1] == ')') && upper[i:i+7] == "BETWEEN" && (i+7 == len(upper) || upper[i+7] == ' ') {
				inBetween = true
				i += 6
				continue
			}

			// Check for AND keyword
			if i+3 <= len(upper) && (i == 0 || upper[i-1] == ' ' || upper[i-1] == ')') && (upper[i:i+3] == "AND" && (i+3 == len(upper) || upper[i+3] == ' ' || upper[i+3] == '(')) {
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

// ParseWhereConditions extracts equality conditions and range conditions from a WHERE clause.
// Returns eqs: map[col_name]value_string, ranges: map[col_name]*IndexRangeCond.
func ParseWhereConditions(where string) (map[string]string, map[string]*IndexRangeCond) {
	eqs := make(map[string]string)
	ranges := make(map[string]*IndexRangeCond)
	parts := SplitOnAnd(where)

	cleanVal := func(s string) string {
		return strings.Trim(strings.TrimSpace(s), "'\"")
	}

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if m := eqBoundRe.FindStringSubmatch(part); len(m) == 3 {
			col := CleanIdentifier(m[1])
			eqs[col] = cleanVal(m[2])
		} else if m := betweenBoundRe.FindStringSubmatch(part); len(m) == 4 {
			col := CleanIdentifier(m[1])
			r, ok := ranges[col]
			if !ok {
				r = &IndexRangeCond{}
				ranges[col] = r
			}
			r.HasMin = true
			r.MinVal = cleanVal(m[2])
			r.HasMax = true
			r.MaxVal = cleanVal(m[3])
		} else if m := gteBoundRe.FindStringSubmatch(part); len(m) == 3 {
			col := CleanIdentifier(m[1])
			r, ok := ranges[col]
			if !ok {
				r = &IndexRangeCond{}
				ranges[col] = r
			}
			r.HasMin = true
			r.MinVal = cleanVal(m[2])
		} else if m := gtBoundRe.FindStringSubmatch(part); len(m) == 3 {
			col := CleanIdentifier(m[1])
			r, ok := ranges[col]
			if !ok {
				r = &IndexRangeCond{}
				ranges[col] = r
			}
			r.HasMin = true
			r.MinVal = cleanVal(m[2])
		} else if m := lteBoundRe.FindStringSubmatch(part); len(m) == 3 {
			col := CleanIdentifier(m[1])
			r, ok := ranges[col]
			if !ok {
				r = &IndexRangeCond{}
				ranges[col] = r
			}
			r.HasMax = true
			r.MaxVal = cleanVal(m[2])
		} else if m := ltBoundRe.FindStringSubmatch(part); len(m) == 3 {
			col := CleanIdentifier(m[1])
			r, ok := ranges[col]
			if !ok {
				r = &IndexRangeCond{}
				ranges[col] = r
			}
			r.HasMax = true
			r.MaxVal = cleanVal(m[2])
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

		conds := strings.Split(whereClause, " AND ")
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
func ParseIndexCond(cond string, schema *storage.TableSchema) (*storage.Value, *storage.Value) {
	cond = strings.Trim(strings.TrimSpace(cond), "()")
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

	// If there are equality conditions
	for colName, valStr := range eqColVals {
		if schema != nil {
			if colMeta, _, found := schema.FindColumn(colName); found && colMeta != nil {
				parsed := ParseValueToType(valStr, colMeta.Type)
				return &parsed, &parsed
			}
		}
		v := storage.StringValue(valStr)
		return &v, &v
	}

	// If there are range conditions
	for colName, r := range rangeCols {
		if r == nil {
			continue
		}
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
			return lowVal, highVal
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

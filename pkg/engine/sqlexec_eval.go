package engine

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/planner"
	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

// parseInCondition extracts column name, isNot, and valList from a single [NOT] IN condition.
func parseInCondition(whereStr string) (string, bool, string, bool) {
	whereStr = strings.TrimSpace(whereStr)
	if !strings.HasSuffix(whereStr, ")") {
		return "", false, "", false
	}
	openIdx := sqlutil.FindTopLevelChar(whereStr, '(')
	if openIdx == -1 {
		return "", false, "", false
	}
	// The opening parenthesis must be closed by the final character; otherwise the
	// condition is a compound expression (e.g. "c IN (1) AND (x)"), not a single IN predicate.
	if sqlutil.FindMatchingParen(whereStr, openIdx) != len(whereStr)-1 {
		return "", false, "", false
	}
	beforeParen := strings.TrimSpace(whereStr[:openIdx])
	valList := strings.TrimSpace(whereStr[openIdx+1 : len(whereStr)-1])

	// The left operand must be a single column identifier; anything containing
	// whitespace or operators (e.g. "a > 5 AND col") is a compound condition.
	singleColumn := func(raw string) (string, bool) {
		raw = strings.TrimSpace(raw)
		if raw == "" || strings.ContainsAny(raw, " \t\r\n=<>!()+-*/%,") {
			return "", false
		}
		col := cleanIdentifier(raw)
		return col, col != ""
	}

	if sqlutil.ContainsKeyword(beforeParen, "NOT IN", false) {
		if kwIdx := sqlutil.FindTopLevelKeyword(beforeParen, "NOT IN"); kwIdx != -1 {
			if col, ok := singleColumn(beforeParen[:kwIdx]); ok {
				return col, true, valList, true
			}
		}
	} else if sqlutil.ContainsKeyword(beforeParen, "IN", false) {
		if kwIdx := sqlutil.FindTopLevelKeyword(beforeParen, "IN"); kwIdx != -1 {
			if col, ok := singleColumn(beforeParen[:kwIdx]); ok {
				return col, false, valList, true
			}
		}
	}
	return "", false, "", false
}


func splitValues(s string) []string {
	return sqlutil.SplitTopLevel(s, ',')
}

// ParseLiteralValue parses a literal SQL expression or default value string into a storage.Value.
func ParseLiteralValue(s string, targetType storage.DataType) storage.Value {
	return parseLiteralValue(s, targetType)
}

func parseLiteralValue(s string, targetType storage.DataType) storage.Value {
	return proc.ParseLiteralDefault(s, targetType)
}

func formatStorageValue(v storage.Value) any {
	if v.IsNull {
		return nil
	}
	switch v.Type {
	case storage.TypeInt:
		return v.IVal
	case storage.TypeVarChar, storage.TypeJSON:
		return v.SVal
	case storage.TypeFloat:
		return v.FVal
	case storage.TypeReal:
		return float32(v.FVal)
	case storage.TypeDecimal:
		return v.Decimal().String()
	case storage.TypeBool:
		return v.BVal
	case storage.TypeDateTime:
		return v.TVal
	case storage.TypeDate:
		return v.String()
	case storage.TypeTime:
		return v.String()
	case storage.TypeUUID:
		return v.SVal
	case storage.TypeBinary:
		return []byte(v.BinVal)
	default:
		return v.String()
	}
}


func matchBoolLiteral(bVal bool, literal string) bool {
	literal = strings.TrimSpace(literal)
	if literal == "1" || strings.EqualFold(literal, "true") {
		return bVal
	}
	if literal == "0" || strings.EqualFold(literal, "false") {
		return !bVal
	}
	if f, err := strconv.ParseFloat(literal, 64); err == nil {
		if f == 1.0 {
			return bVal
		}
		if f == 0.0 {
			return !bVal
		}
	}
	return false
}

func parseValueToType(s string, t storage.DataType) storage.Value {
	return planner.ParseValueToType(s, t)
}


// ValidateWhereClauseWithSchemas verifies that whereStr is syntactically well-formed if non-empty,
// and optionally validates that all referenced column identifiers exist in schemas or outer names.
func ValidateWhereClauseWithSchemas(whereStr string, schemas ...*storage.TableSchema) error {
	whereStr = strings.TrimSpace(whereStr)
	if whereStr == "" {
		return nil
	}
	p := proc.NewParser(proc.NewLexer(whereStr))
	expr := p.ParseExpr()
	if len(p.Errors()) > 0 {
		return fmt.Errorf("syntax error in WHERE clause: %s", strings.Join(p.Errors(), "; "))
	}
	if expr == nil {
		return fmt.Errorf("syntax error: empty or invalid expression in WHERE clause")
	}
	for p.CurrentToken().Type == proc.TokenSemicolon {
		p.NextToken()
	}
	if p.CurrentToken().Type != proc.TokenEOF {
		return fmt.Errorf("syntax error: unexpected trailing token %q in WHERE clause", p.CurrentToken().Literal)
	}

	// T-SQL / SQL Standard semantics: Condition expressions cannot be non-boolean literals
	var checkConditionExpr func(e proc.Expr) error
	checkConditionExpr = func(e proc.Expr) error {
		if e == nil {
			return nil
		}
		switch node := e.(type) {
		case *proc.LiteralExpr:
			if node.Val.Type != storage.TypeBool {
				return fmt.Errorf("syntax error: an expression of non-boolean type specified in a context where a condition is expected")
			}
		case *proc.UnaryExpr:
			if node.Op == proc.TokenMinus || node.Op == proc.TokenPlus || node.Op == proc.TokenBitwiseNot {
				return fmt.Errorf("syntax error: an expression of non-boolean type specified in a context where a condition is expected")
			}
			return checkConditionExpr(node.Expr)
		case *proc.BinaryExpr:
			if node.Op == proc.TokenAnd || node.Op == proc.TokenOr {
				if err := checkConditionExpr(node.Left); err != nil {
					return err
				}
				return checkConditionExpr(node.Right)
			}
		}
		return nil
	}
	if err := checkConditionExpr(expr); err != nil {
		return err
	}

	scope := planner.NewTableScope()
	for _, sch := range schemas {
		if sch != nil {
			scope.Register(sch.Name, "", sch)
		}
	}

	if scope.HasSchemas() {
		if err := scope.ValidateExpr(expr); err != nil {
			return err
		}
	}

	return nil
}

// ValidateWhereClauseWithScope verifies that whereStr is syntactically well-formed if non-empty,
// checks disallowed expressions (e.g. aggregate functions), and checks column bindings against the table scope.
func ValidateWhereClauseWithScope(whereStr string, scope *planner.TableScope) error {
	whereStr = strings.TrimSpace(whereStr)
	if whereStr == "" {
		return nil
	}
	p := proc.NewParser(proc.NewLexer(whereStr))
	expr := p.ParseExpr()
	if len(p.Errors()) > 0 {
		return fmt.Errorf("syntax error in WHERE clause: %s", strings.Join(p.Errors(), "; "))
	}
	if expr == nil {
		return fmt.Errorf("syntax error: empty or invalid expression in WHERE clause")
	}

	for p.CurrentToken().Type == proc.TokenSemicolon {
		p.NextToken()
	}
	if p.CurrentToken().Type != proc.TokenEOF {
		return fmt.Errorf("syntax error: unexpected trailing token %q in WHERE clause", p.CurrentToken().Literal)
	}

	if err := planner.CheckDisallowedExprs(expr, planner.ScopeWhere); err != nil {
		return err
	}

	var checkConditionExpr func(e proc.Expr) error
	checkConditionExpr = func(e proc.Expr) error {
		if e == nil {
			return nil
		}
		switch node := e.(type) {
		case *proc.LiteralExpr:
			if node.Val.Type != storage.TypeBool {
				return fmt.Errorf("syntax error: an expression of non-boolean type specified in a context where a condition is expected")
			}
		case *proc.UnaryExpr:
			if node.Op == proc.TokenMinus || node.Op == proc.TokenPlus || node.Op == proc.TokenBitwiseNot {
				return fmt.Errorf("syntax error: an expression of non-boolean type specified in a context where a condition is expected")
			}
			return checkConditionExpr(node.Expr)
		case *proc.BinaryExpr:
			if node.Op == proc.TokenAnd || node.Op == proc.TokenOr {
				if err := checkConditionExpr(node.Left); err != nil {
					return err
				}
				return checkConditionExpr(node.Right)
			}
		}
		return nil
	}
	if err := checkConditionExpr(expr); err != nil {
		return err
	}

	if scope != nil && scope.HasSchemas() {
		if err := scope.ValidateExpr(expr); err != nil {
			return err
		}
	}

	return nil
}

// ValidateJoinOnClauseWithScope verifies that onStr in JOIN ON is syntactically well-formed,
// checks disallowed expressions (e.g. aggregate and window functions in ScopeJoinOn),
// and checks column bindings against the join scope.
func ValidateJoinOnClauseWithScope(onStr string, scope *planner.TableScope) error {
	onStr = strings.TrimSpace(onStr)
	if onStr == "" {
		return nil
	}
	p := proc.NewParser(proc.NewLexer(onStr))
	expr := p.ParseExpr()
	if len(p.Errors()) > 0 {
		return fmt.Errorf("syntax error in JOIN ON clause: %s", strings.Join(p.Errors(), "; "))
	}
	if expr == nil {
		return fmt.Errorf("syntax error: empty or invalid expression in JOIN ON clause")
	}

	for p.CurrentToken().Type == proc.TokenSemicolon {
		p.NextToken()
	}
	if p.CurrentToken().Type != proc.TokenEOF {
		return fmt.Errorf("syntax error: unexpected trailing token %q in JOIN ON clause", p.CurrentToken().Literal)
	}

	if err := planner.CheckDisallowedExprs(expr, planner.ScopeJoinOn); err != nil {
		return err
	}

	var checkConditionExpr func(e proc.Expr) error
	checkConditionExpr = func(e proc.Expr) error {
		if e == nil {
			return nil
		}
		switch node := e.(type) {
		case *proc.LiteralExpr:
			if node.Val.Type != storage.TypeBool {
				return fmt.Errorf("syntax error: an expression of non-boolean type specified in a context where a condition is expected")
			}
		case *proc.UnaryExpr:
			if node.Op == proc.TokenMinus || node.Op == proc.TokenPlus || node.Op == proc.TokenBitwiseNot {
				return fmt.Errorf("syntax error: an expression of non-boolean type specified in a context where a condition is expected")
			}
			return checkConditionExpr(node.Expr)
		case *proc.BinaryExpr:
			if node.Op == proc.TokenAnd || node.Op == proc.TokenOr {
				if err := checkConditionExpr(node.Left); err != nil {
					return err
				}
				return checkConditionExpr(node.Right)
			}
		}
		return nil
	}
	if err := checkConditionExpr(expr); err != nil {
		return err
	}

	if scope != nil && scope.HasSchemas() {
		if err := scope.ValidateExpr(expr); err != nil {
			return err
		}
	}

	return nil
}

// ValidateJoinScope validates that all JOIN ON conditions have valid, unambiguous column references
// within the scope of tables joined up to each JOIN clause.
func ValidateJoinScope(qInfo *planner.QueryInfo, schemas []*storage.TableSchema) error {
	if qInfo == nil {
		return nil
	}
	findSchema := func(tblName string) *storage.TableSchema {
		cleanTbl := strings.ToLower(proc.CleanSchemaPrefix(tblName))
		for _, sch := range schemas {
			if sch != nil && (strings.EqualFold(sch.Name, tblName) || strings.EqualFold(proc.CleanSchemaPrefix(sch.Name), cleanTbl)) {
				return sch
			}
		}
		return nil
	}

	joinScope := planner.NewTableScope()
	if qInfo.TableName != "" {
		joinScope.Register(qInfo.TableName, qInfo.TableAlias, findSchema(qInfo.TableName))
	} else if len(schemas) == 1 && qInfo.TableAlias != "" {
		joinScope.Register("", qInfo.TableAlias, schemas[0])
	}

	validateKey := func(key string, scope *planner.TableScope) error {
		key = strings.TrimSpace(key)
		if key == "" {
			return nil
		}
		p := proc.NewParser(proc.NewLexer(key))
		expr := p.ParseExpr()
		if expr != nil && scope != nil && scope.HasSchemas() {
			if err := planner.CheckDisallowedExprs(expr, planner.ScopeJoinOn); err != nil {
				return err
			}
			return scope.ValidateExpr(expr)
		}
		return nil
	}

	if len(qInfo.Joins) > 0 {
		for _, j := range qInfo.Joins {
			if j == nil {
				continue
			}
			joinScope.Register(j.Table, j.Alias, findSchema(j.Table))
			if err := validateKey(j.LeftKey, joinScope); err != nil {
				return err
			}
			if err := validateKey(j.RightKey, joinScope); err != nil {
				return err
			}
			if j.Filter != "" {
				if err := ValidateJoinOnClauseWithScope(j.Filter, joinScope); err != nil {
					return err
				}
			}
		}
	} else if qInfo.Join != nil {
		joinScope.Register(qInfo.Join.Table, qInfo.Join.Alias, findSchema(qInfo.Join.Table))
		if err := validateKey(qInfo.Join.LeftKey, joinScope); err != nil {
			return err
		}
		if err := validateKey(qInfo.Join.RightKey, joinScope); err != nil {
			return err
		}
		if qInfo.Join.Filter != "" {
			if err := ValidateJoinOnClauseWithScope(qInfo.Join.Filter, joinScope); err != nil {
				return err
			}
		}
	}

	return nil
}

// ValidateWhereClause verifies that whereStr is syntactically well-formed if non-empty.
func ValidateWhereClause(whereStr string) error {
	return ValidateWhereClauseWithSchemas(whereStr)
}

// ValidateAmbiguousProjections checks whether any unqualified column in projections exists in multiple schemas.
func ValidateAmbiguousProjections(projections []string, schemas []*storage.TableSchema) error {
	if len(schemas) <= 1 {
		return nil
	}
	for _, proj := range projections {
		projTrim := strings.TrimSpace(proj)
		if projTrim == "*" || strings.HasSuffix(projTrim, ".*") {
			continue
		}
		exprPart := projTrim
		if asIdx := sqlutil.FindTopLevelKeyword(projTrim, "AS"); asIdx != -1 {
			exprPart = strings.TrimSpace(projTrim[:asIdx])
		}
		for _, ref := range planner.ExtractColumnRefs(exprPart) {
			if ref.TableOrAlias == "" {
				matches := 0
				seenTables := make(map[string]bool)
				for _, sch := range schemas {
					if sch != nil && !seenTables[strings.ToLower(sch.Name)] {
						seenTables[strings.ToLower(sch.Name)] = true
						if _, _, ok := sch.FindColumn(ref.Column); ok {
							matches++
						}
					}
				}
				if matches > 1 {
					return fmt.Errorf("column reference %q is ambiguous", ref.Column)
				}
			}
		}
	}
	return nil
}

// BuildTableScope builds a planner.TableScope from QueryInfo and visible table schemas.
func BuildTableScope(qInfo *planner.QueryInfo, schemas []*storage.TableSchema) *planner.TableScope {
	scope := planner.NewTableScope()
	if qInfo == nil {
		return scope
	}
	// 1. Primary table
	if qInfo.TableName != "" {
		var priSch *storage.TableSchema
		cleanPri := strings.ToLower(proc.CleanSchemaPrefix(qInfo.TableName))
		for _, sch := range schemas {
			if sch != nil && (strings.EqualFold(sch.Name, qInfo.TableName) || strings.EqualFold(proc.CleanSchemaPrefix(sch.Name), cleanPri)) {
				priSch = sch
				break
			}
		}
		if priSch == nil && len(schemas) == 1 && len(qInfo.Joins) == 0 && qInfo.Join == nil {
			priSch = schemas[0]
		}
		scope.Register(qInfo.TableName, qInfo.TableAlias, priSch)
	} else if len(schemas) == 1 && qInfo.TableAlias != "" {
		scope.Register("", qInfo.TableAlias, schemas[0])
	}
	// 2. Joins
	for _, j := range qInfo.Joins {
		if j == nil {
			continue
		}
		var jSch *storage.TableSchema
		cleanJ := strings.ToLower(proc.CleanSchemaPrefix(j.Table))
		for _, sch := range schemas {
			if sch != nil && (strings.EqualFold(sch.Name, j.Table) || strings.EqualFold(proc.CleanSchemaPrefix(sch.Name), cleanJ)) {
				jSch = sch
				break
			}
		}
		scope.Register(j.Table, j.Alias, jSch)
	}
	if qInfo.Join != nil {
		var jSch *storage.TableSchema
		cleanJ := strings.ToLower(proc.CleanSchemaPrefix(qInfo.Join.Table))
		for _, sch := range schemas {
			if sch != nil && (strings.EqualFold(sch.Name, qInfo.Join.Table) || strings.EqualFold(proc.CleanSchemaPrefix(sch.Name), cleanJ)) {
				jSch = sch
				break
			}
		}
		scope.Register(qInfo.Join.Table, qInfo.Join.Alias, jSch)
	}
	// 3. CTEs
	for _, cte := range qInfo.CTEs {
		scope.Register(cte.Name, "", nil)
	}
	// 4. Projection aliases
	for _, proj := range qInfo.Projections {
		if asIdx := sqlutil.FindTopLevelKeyword(proj, "AS"); asIdx != -1 {
			alias := strings.TrimSpace(proj[asIdx+2:])
			scope.AddProjectionAlias(alias)
		}
	}
	return scope
}

// ValidateHavingClauseWithScope verifies that havingStr in SELECT is syntactically well-formed,
// checks disallowed expressions (ScopeHaving, which permits aggregates but bans window functions),
// and checks column references against the table scope.
func ValidateHavingClauseWithScope(havingStr string, scope *planner.TableScope) error {
	havingStr = strings.TrimSpace(havingStr)
	if havingStr == "" {
		return nil
	}
	p := proc.NewParser(proc.NewLexer(havingStr))
	expr := p.ParseExpr()
	if len(p.Errors()) > 0 {
		return fmt.Errorf("syntax error in HAVING clause: %s", strings.Join(p.Errors(), "; "))
	}
	if expr == nil {
		return fmt.Errorf("syntax error: empty or invalid expression in HAVING clause")
	}

	for p.CurrentToken().Type == proc.TokenSemicolon {
		p.NextToken()
	}
	if p.CurrentToken().Type != proc.TokenEOF {
		return fmt.Errorf("syntax error: unexpected trailing token %q in HAVING clause", p.CurrentToken().Literal)
	}

	if err := planner.CheckDisallowedExprs(expr, planner.ScopeHaving); err != nil {
		return err
	}

	if scope != nil && scope.HasSchemas() {
		if err := scope.ValidateExpr(expr); err != nil {
			return err
		}
	}

	return nil
}

// ValidateHavingClause verifies that havingStr in SELECT is non-empty and syntactically well-formed.
func ValidateHavingClause(havingStr string) error {
	return ValidateHavingClauseWithScope(havingStr, nil)
}

// ValidateSelectScope enforces semantic binder invariants across projections, WHERE, HAVING, ORDER BY, and GROUP BY.
func ValidateSelectScope(qInfo *planner.QueryInfo, schemas []*storage.TableSchema) error {
	if qInfo == nil {
		return nil
	}
	scope := BuildTableScope(qInfo, schemas)

	for _, proj := range qInfo.Projections {
		projTrim := strings.TrimSpace(proj)
		if projTrim == "*" {
			continue
		}
		if strings.HasSuffix(projTrim, ".*") {
			prefix := strings.TrimSuffix(projTrim, ".*")
			col := &proc.ColumnRefExpr{Name: prefix + ".*"}
			if err := scope.ValidateExpr(col); err != nil {
				return err
			}
			continue
		}
		exprPart := projTrim
		if asIdx := sqlutil.FindTopLevelKeyword(projTrim, "AS"); asIdx != -1 {
			exprPart = strings.TrimSpace(projTrim[:asIdx])
		}
		p := proc.NewParser(proc.NewLexer(exprPart))
		if expr := p.ParseExpr(); expr != nil {
			if err := planner.ValidateConstantDomainBounds(expr); err != nil {
				return err
			}
			if scope.HasSchemas() {
				if err := scope.ValidateExpr(expr); err != nil {
					return err
				}
			}
		}
	}

	if len(schemas) > 1 {
		if err := ValidateAmbiguousProjections(qInfo.Projections, schemas); err != nil {
			return err
		}
	}
	// PostgreSQL / SQL Standard compatibility (ERROR 42P10):
	// For SELECT DISTINCT, all ORDER BY expressions must appear in the SELECT projection list.
	isSelectStar := len(qInfo.Projections) == 1 && qInfo.Projections[0] == "*"
	if qInfo.Distinct && qInfo.OrderByClause != "" && !isSelectStar {
		orderItems := sqlutil.ParseOrderBy(qInfo.OrderByClause)
		for _, item := range orderItems {
			cleanOrderCol := strings.ToLower(cleanIdentifier(item.Column))
			if _, err := strconv.Atoi(cleanOrderCol); err == nil {
				continue // positional ordinal order by 1, 2
			}
			matched := false
			for _, proj := range qInfo.Projections {
				cleanProj := strings.ToLower(cleanIdentifier(proj))
				if cleanProj == cleanOrderCol || strings.HasSuffix(cleanProj, "."+cleanOrderCol) || strings.HasSuffix(cleanProj, " as "+cleanOrderCol) {
					matched = true
					break
				}
				parts := strings.Split(strings.ToLower(proj), " as ")
				if len(parts) == 2 && strings.TrimSpace(parts[1]) == cleanOrderCol {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("ERROR 42P10: for SELECT DISTINCT, ORDER BY expressions must appear in select list (%s)", item.Column)
			}
		}
	}
	if err := ValidateJoinScope(qInfo, schemas); err != nil {
		return err
	}
	if qInfo.WhereClause != "" {
		if err := ValidateWhereClauseWithScope(qInfo.WhereClause, scope); err != nil {
			return err
		}
	}
	if qInfo.HavingClause != "" {
		if err := ValidateHavingClauseWithScope(qInfo.HavingClause, scope); err != nil {
			return err
		}
	}
	if qInfo.OrderByClause != "" {
		if err := ValidateOrderByClauseWithScope(qInfo.OrderByClause, scope); err != nil {
			return err
		}
	}
	if len(qInfo.GroupByCols) > 0 {
		if err := ValidateGroupByClauseWithScope(strings.Join(qInfo.GroupByCols, ", "), scope); err != nil {
			return err
		}
	}
	return nil
}

// ValidateOrderByClauseWithScope verifies that orderStr in SELECT is syntactically well-formed,
// checks disallowed expressions, and checks for invalid or ambiguous column references.
func ValidateOrderByClauseWithScope(orderStr string, scope *planner.TableScope) error {
	orderStr = strings.TrimSpace(orderStr)
	if orderStr == "" {
		return fmt.Errorf("syntax error: empty or invalid expression in ORDER BY clause")
	}
	items := sqlutil.SplitTopLevel(orderStr, ',')
	if len(items) == 0 {
		return fmt.Errorf("syntax error: empty or invalid expression in ORDER BY clause")
	}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			return fmt.Errorf("syntax error: empty item in ORDER BY clause")
		}
		p := proc.NewParser(proc.NewLexer(item))
		expr := p.ParseExpr()
		if len(p.Errors()) > 0 {
			return fmt.Errorf("syntax error in ORDER BY clause: %s", strings.Join(p.Errors(), "; "))
		}
		if expr == nil {
			return fmt.Errorf("syntax error: empty or invalid expression in ORDER BY clause")
		}
		if lit, ok := expr.(*proc.LiteralExpr); ok {
			if lit.Val.IsNull || lit.Val.Type != storage.TypeInt {
				return fmt.Errorf("syntax error: a constant expression was encountered in the ORDER BY list")
			}
			if lit.Val.Type == storage.TypeInt && lit.Val.IVal <= 0 {
				return fmt.Errorf("syntax error: ORDER BY position number %d is out of range", lit.Val.IVal)
			}
		}
		if err := planner.CheckDisallowedExprs(expr, planner.ScopeOrderBy); err != nil {
			return err
		}
		if scope != nil && scope.HasSchemas() {
			if err := scope.ValidateOrderByExpr(expr); err != nil {
				return err
			}
		}
		if p.CurrentToken().Type == proc.TokenAsc || p.CurrentToken().Type == proc.TokenDesc {
			p.NextToken()
		}
		if p.CurrentToken().Type == proc.TokenIdent && strings.EqualFold(p.CurrentToken().Literal, "NULLS") {
			p.NextToken()
			if p.CurrentToken().Type == proc.TokenFirst || (p.CurrentToken().Type == proc.TokenIdent && strings.EqualFold(p.CurrentToken().Literal, "FIRST")) ||
				(p.CurrentToken().Type == proc.TokenIdent && strings.EqualFold(p.CurrentToken().Literal, "LAST")) {
				p.NextToken()
			}
		}
		for p.CurrentToken().Type == proc.TokenSemicolon {
			p.NextToken()
		}
		if p.CurrentToken().Type != proc.TokenEOF {
			return fmt.Errorf("syntax error: unexpected trailing token %q in ORDER BY clause", p.CurrentToken().Literal)
		}
	}
	return nil
}

// ValidateOrderByClauseWithSchemas verifies that orderStr in SELECT is syntactically well-formed,
// checks disallowed expressions, and checks for ambiguous column references across joined schemas.
func ValidateOrderByClauseWithSchemas(orderStr string, schemas ...*storage.TableSchema) error {
	scope := planner.NewTableScope()
	for _, sch := range schemas {
		if sch != nil {
			scope.Register(sch.Name, "", sch)
		}
	}
	return ValidateOrderByClauseWithScope(orderStr, scope)
}

// ValidateOrderByClause verifies that orderStr in SELECT is non-empty and syntactically well-formed.
func ValidateOrderByClause(orderStr string) error {
	return ValidateOrderByClauseWithSchemas(orderStr)
}

// ValidateGroupByClauseWithScope verifies that groupStr in SELECT is syntactically well-formed,
// checks disallowed expressions (e.g. aggregate functions, window functions, non-positive ordinals),
// and checks column references.
func ValidateGroupByClauseWithScope(groupStr string, scope *planner.TableScope) error {
	groupStr = strings.TrimSpace(groupStr)
	if groupStr == "" {
		return fmt.Errorf("syntax error: empty or invalid expression in GROUP BY clause")
	}
	items := sqlutil.SplitTopLevel(groupStr, ',')
	if len(items) == 0 {
		return fmt.Errorf("syntax error: empty or invalid expression in GROUP BY clause")
	}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			return fmt.Errorf("syntax error: empty item in GROUP BY clause")
		}
		p := proc.NewParser(proc.NewLexer(item))
		expr := p.ParseExpr()
		if len(p.Errors()) > 0 {
			return fmt.Errorf("syntax error in GROUP BY clause: %s", strings.Join(p.Errors(), "; "))
		}
		if expr == nil {
			return fmt.Errorf("syntax error: empty or invalid expression in GROUP BY clause")
		}
		if err := planner.CheckDisallowedExprs(expr, planner.ScopeGroupBy); err != nil {
			return err
		}
		var intVal *int64
		if lit, ok := expr.(*proc.LiteralExpr); ok && lit.Val.Type == storage.TypeInt && !lit.Val.IsNull {
			v := lit.Val.IVal
			intVal = &v
		} else if un, ok := expr.(*proc.UnaryExpr); ok && un.Op == proc.TokenMinus {
			if lit, ok := un.Expr.(*proc.LiteralExpr); ok && lit.Val.Type == storage.TypeInt && !lit.Val.IsNull {
				v := -lit.Val.IVal
				intVal = &v
			}
		}
		if intVal != nil && *intVal <= 0 {
			return fmt.Errorf("GROUP BY position %d is not in select list", *intVal)
		}
		if scope != nil && scope.HasSchemas() {
			if err := scope.ValidateExpr(expr); err != nil {
				return err
			}
		}
		for p.CurrentToken().Type == proc.TokenSemicolon {
			p.NextToken()
		}
		if p.CurrentToken().Type != proc.TokenEOF {
			return fmt.Errorf("syntax error: unexpected trailing token %q in GROUP BY clause", p.CurrentToken().Literal)
		}
	}
	return nil
}

// ValidateGroupByClauseWithSchemas verifies that groupStr in SELECT is syntactically well-formed,
// checks disallowed expressions (e.g. aggregate functions, window functions, non-positive ordinals),
// and checks for ambiguous column references across joined schemas.
func ValidateGroupByClauseWithSchemas(groupStr string, schemas ...*storage.TableSchema) error {
	scope := planner.NewTableScope()
	for _, sch := range schemas {
		if sch != nil {
			scope.Register(sch.Name, "", sch)
		}
	}
	return ValidateGroupByClauseWithScope(groupStr, scope)
}

// ValidateGroupByClause verifies that groupStr in SELECT is non-empty and syntactically well-formed.
func ValidateGroupByClause(groupStr string) error {
	return ValidateGroupByClauseWithSchemas(groupStr)
}

// ValidateSetClauseWithScope verifies that setStr in UPDATE or MERGE is non-empty, all assignment pairs are syntactically valid,
// target columns exist on targetSchema, and expressions only reference in-scope columns.
func ValidateSetClauseWithScope(setStr string, targetSchema *storage.TableSchema, scope *planner.TableScope) error {
	setStr = strings.TrimSpace(setStr)
	if setStr == "" {
		return fmt.Errorf("syntax error: empty or invalid SET clause")
	}
	rawParts := sqlutil.SplitTopLevel(setStr, ',')
	if len(rawParts) == 0 {
		return fmt.Errorf("syntax error: empty or invalid SET clause")
	}
	for _, p := range rawParts {
		p = strings.TrimSpace(p)
		if p == "" {
			return fmt.Errorf("syntax error: empty assignment or trailing comma in SET clause")
		}
		eqIdx := sqlutil.FindTopLevelChar(p, '=')
		if eqIdx == -1 {
			return fmt.Errorf("syntax error: missing = in SET assignment: %s", p)
		}
		col := strings.TrimSpace(p[:eqIdx])
		val := strings.TrimSpace(p[eqIdx+1:])
		if col == "" {
			return fmt.Errorf("syntax error: missing column name in SET assignment")
		}
		if val == "" {
			return fmt.Errorf("syntax error: empty expression in SET assignment for column %q", col)
		}

		if targetSchema != nil {
			cleanCol := cleanIdentifier(col)
			if dotIdx := strings.LastIndex(cleanCol, "."); dotIdx != -1 {
				cleanCol = cleanCol[dotIdx+1:]
			}
			if _, _, ok := targetSchema.FindColumn(cleanCol); !ok {
				return fmt.Errorf("column %q of relation %q does not exist", cleanCol, targetSchema.Name)
			}
		}

		parser := proc.NewParser(proc.NewLexer(val))
		expr := parser.ParseExpr()
		if len(parser.Errors()) > 0 {
			return fmt.Errorf("syntax error in SET expression for column %q: %s", col, strings.Join(parser.Errors(), "; "))
		}
		if expr == nil {
			return fmt.Errorf("syntax error: invalid expression in SET assignment for column %q", col)
		}
		for parser.CurrentToken().Type == proc.TokenSemicolon {
			parser.NextToken()
		}
		if parser.CurrentToken().Type != proc.TokenEOF {
			return fmt.Errorf("syntax error: unexpected trailing token %q in SET assignment for column %q", parser.CurrentToken().Literal, col)
		}

		if err := planner.CheckDisallowedExprs(expr, planner.ScopeWhere); err != nil {
			return err
		}

		if scope != nil && scope.HasSchemas() {
			if err := scope.ValidateExpr(expr); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateSetClause verifies that setStr in UPDATE or MERGE is non-empty and all assignment pairs are syntactically valid.
func ValidateSetClause(setStr string) error {
	return ValidateSetClauseWithScope(setStr, nil, nil)
}

func evaluateWhereCondition(rec storage.Record, whereStr string) bool {
	return evaluateWhereConditionWithSubquery(rec, whereStr, nil)
}

func evaluateWhereConditionWithSubquery(rec storage.Record, whereStr string, execSubquery proc.SubqueryExecutor) bool {
	b, _ := evaluateWhereConditionWithSubqueryErr(rec, whereStr, execSubquery)
	return b
}

func evaluateWhereConditionWithSubqueryErr(rec storage.Record, whereStr string, execSubquery proc.SubqueryExecutor) (bool, error) {
	whereStr = strings.TrimSpace(whereStr)
	if whereStr == "" || whereStr == "1=1" || whereStr == "1 = 1" || strings.EqualFold(whereStr, "NOT 1=0") || strings.EqualFold(whereStr, "NOT 1 = 0") {
		return true, nil
	}
	if whereStr == "1=0" || whereStr == "1 = 0" || strings.EqualFold(whereStr, "NOT 1=1") || strings.EqualFold(whereStr, "NOT 1 = 1") {
		return false, nil
	}

	rMap := make(map[string]any, len(rec))
	for k, v := range rec {
		rMap[k] = formatStorageValue(v)
	}
	return evaluateWhereConditionMapWithSubqueryErr(rMap, whereStr, execSubquery)
}

func evaluateWhereConditionMap(rec map[string]any, whereStr string) bool {
	return evaluateWhereConditionMapWithSubquery(rec, whereStr, nil)
}

func evaluateWhereConditionMapWithSubquery(rec map[string]any, whereStr string, execSubquery proc.SubqueryExecutor) bool {
	b, _ := evaluateWhereConditionMapWithSubqueryErr(rec, whereStr, execSubquery)
	return b
}

func evaluateWhereConditionMapWithSubqueryErr(rec map[string]any, whereStr string, execSubquery proc.SubqueryExecutor) (bool, error) {
	whereStr = strings.TrimSpace(whereStr)
	if whereStr == "" || whereStr == "1=1" || whereStr == "1 = 1" || strings.EqualFold(whereStr, "NOT 1=0") || strings.EqualFold(whereStr, "NOT 1 = 0") {
		return true, nil
	}
	if whereStr == "1=0" || whereStr == "1 = 0" || strings.EqualFold(whereStr, "NOT 1=1") || strings.EqualFold(whereStr, "NOT 1 = 1") {
		return false, nil
	}

	p := proc.NewParser(proc.NewLexer(whereStr))
	expr := p.ParseExpr()
	if expr != nil && len(p.Errors()) == 0 {
		return proc.EvalBoolExprWithErr(expr, rec, execSubquery)
	}

	orParts := sqlutil.SplitTopLevelKeyword(whereStr, "OR")
	if len(orParts) > 1 {
		for _, part := range orParts {
			match, err := evaluateWhereConditionMapWithSubqueryErr(rec, part, execSubquery)
			if err != nil {
				return false, err
			}
			if match {
				return true, nil
			}
		}
		return false, nil
	}

	andParts := sqlutil.SplitTopLevelKeyword(whereStr, "AND")
	if len(andParts) > 1 {
		for _, part := range andParts {
			match, err := evaluateWhereConditionMapWithSubqueryErr(rec, part, execSubquery)
			if err != nil {
				return false, err
			}
			if !match {
				return false, nil
			}
		}
		return true, nil
	}

	return evaluateSingleConditionMap(rec, whereStr), nil
}

func evaluateSingleConditionMap(rec map[string]any, whereStr string) bool {
	whereStr = strings.TrimSpace(whereStr)
	if strings.HasPrefix(whereStr, "(") && strings.HasSuffix(whereStr, ")") {
		inner := strings.TrimSpace(whereStr[1 : len(whereStr)-1])
		if !strings.Contains(inner, "(") && !strings.Contains(inner, ")") {
			whereStr = inner
		}
	}

	row := NewSQLRow(rec)

	if colName, isNot, valList, ok := parseInCondition(whereStr); ok {
		// Fast-Path: Materialized In-Memory Subquery HashSet ($O(1)$)
		trimmedValList := strings.TrimSpace(valList)
		if strings.HasPrefix(trimmedValList, "__subq_hashset_") && strings.HasSuffix(trimmedValList, "__") {
			if set, ok := LookupSubqueryHashSet(trimmedValList); ok {
				val, ok := row.Get(colName)
				if !ok || val == nil {
					return false
				}
				normKey, isNull := NormalizeSubqueryKey(val)
				return set.Evaluate(normKey, isNull, isNot)
			}
		}

		val, ok := row.Get(colName)
		if !ok || val == nil {
			return false
		}
		if sv, ok := val.(storage.Value); ok && sv.IsNull {
			return false
		}
		var isBool bool
		var bVal bool
		if b, ok := val.(bool); ok {
			isBool = true
			bVal = b
		} else if sv, ok := val.(storage.Value); ok && sv.Type == storage.TypeBool {
			isBool = true
			bVal = sv.BVal
		}

		strVal := fmt.Sprintf("%v", val)
		found := false
		hasNull := false
		for _, rawItem := range sqlutil.SplitTopLevel(valList, ',') {
			rawItem = strings.TrimSpace(rawItem)
			if strings.EqualFold(rawItem, "NULL") {
				hasNull = true
				continue
			}
			var item string
			if (strings.HasPrefix(rawItem, "N'") || strings.HasPrefix(rawItem, "n'")) && strings.HasSuffix(rawItem, "'") && len(rawItem) >= 3 {
				item = strings.ReplaceAll(rawItem[2:len(rawItem)-1], "''", "'")
			} else {
				item = strings.Trim(rawItem, "'\"")
			}
			if strings.EqualFold(strVal, item) ||
				strings.EqualFold(strings.TrimRight(strVal, " "), strings.TrimRight(item, " ")) ||
				(isBool && matchBoolLiteral(bVal, item)) {
				found = true
				break
			}
		}
		if found {
			return !isNot
		}
		if hasNull {
			// SQL-92 Three-Valued Logic (3VL):
			// If target value was not found in the list, but the list contains NULL,
			// the result of 'x IN (..., NULL)' is UNKNOWN, and 'x NOT IN (..., NULL)' is UNKNOWN.
			// In a WHERE filter predicate, UNKNOWN evaluates to false (row is filtered out).
			return false
		}
		if isNot {
			return true
		}
		return false
	}

	upperWhere := strings.ToUpper(whereStr)
	if strings.HasSuffix(upperWhere, " IS NOT NULL") {
		colName := cleanIdentifier(whereStr[:len(whereStr)-len(" IS NOT NULL")])
		val, ok := row.Get(colName)
		return ok && val != nil
	}
	if strings.HasSuffix(upperWhere, " IS NULL") {
		colName := cleanIdentifier(whereStr[:len(whereStr)-len(" IS NULL")])
		val, ok := row.Get(colName)
		return !ok || val == nil
	}

	ops := []string{" NOT LIKE ", " NOT ILIKE ", "!=", "<>", "<=", ">=", "!<", "!>", "=", "<", ">", " LIKE ", " ILIKE "}
	for _, op := range ops {
		idx := strings.Index(strings.ToUpper(whereStr), op)
		if idx != -1 {
			rawLeft := whereStr[:idx]
			var collate string
			if cIdx := sqlutil.FindTopLevelKeyword(rawLeft, "COLLATE"); cIdx != -1 {
				collate = strings.TrimSpace(rawLeft[cIdx+len("COLLATE"):])
				rawLeft = strings.TrimSpace(rawLeft[:cIdx])
			}
			leftCol := cleanIdentifier(rawLeft)
			rawRight := strings.TrimSpace(whereStr[idx+len(op):])
			var rightVal string
			var rightIsQuoted bool
			if (strings.HasPrefix(rawRight, "N'") || strings.HasPrefix(rawRight, "n'")) && strings.HasSuffix(rawRight, "'") && len(rawRight) >= 3 {
				rightVal = strings.ReplaceAll(rawRight[2:len(rawRight)-1], "''", "'")
				rightIsQuoted = true
			} else if (strings.HasPrefix(rawRight, "'") && strings.HasSuffix(rawRight, "'") && len(rawRight) >= 2) ||
				(strings.HasPrefix(rawRight, "\"") && strings.HasSuffix(rawRight, "\"") && len(rawRight) >= 2) {
				rightVal = strings.ReplaceAll(rawRight[1:len(rawRight)-1], "''", "'")
				rightIsQuoted = true
			} else {
				rightVal = strings.Trim(rawRight, "';\"")
			}

			if leftCol == "1" && rightVal == "1" && op == "=" {
				return true
			}
			if leftCol == "1" && rightVal == "0" && op == "=" {
				return false
			}

			val, ok := row.Get(leftCol)
			if !ok {
				return false
			}

			// In SQL three-valued logic, binary comparisons with NULL evaluate to UNKNOWN (false in WHERE filter).
			if val == nil || (!rightIsQuoted && strings.EqualFold(rightVal, "NULL")) {
				return false
			}
			if sv, ok := val.(storage.Value); ok && sv.IsNull {
				return false
			}

			var isBool bool
			var bVal bool
			if b, ok := val.(bool); ok {
				isBool = true
				bVal = b
			} else if sv, ok := val.(storage.Value); ok && sv.Type == storage.TypeBool {
				isBool = true
				bVal = sv.BVal
			}

			strVal := fmt.Sprintf("%v", val)
			if sv, ok := val.(storage.Value); ok {
				strVal = fmt.Sprintf("%v", sv.Interface())
			}
			switch strings.TrimSpace(op) {
			case "=":
				if isBool {
					return matchBoolLiteral(bVal, rightVal)
				}
				if collate != "" && sqlutil.IsCaseSensitiveCollation(collate) {
					return strVal == rightVal || strings.TrimRight(strVal, " ") == strings.TrimRight(rightVal, " ")
				}
				return strings.EqualFold(strVal, rightVal) || strings.EqualFold(strings.TrimRight(strVal, " "), strings.TrimRight(rightVal, " "))
			case "!=", "<>":
				if isBool {
					return !matchBoolLiteral(bVal, rightVal)
				}
				if collate != "" && sqlutil.IsCaseSensitiveCollation(collate) {
					return !(strVal == rightVal || strings.TrimRight(strVal, " ") == strings.TrimRight(rightVal, " "))
				}
				return !strings.EqualFold(strVal, rightVal) && !strings.EqualFold(strings.TrimRight(strVal, " "), strings.TrimRight(rightVal, " "))
			case ">", ">=", "!<", "<", "<=", "!>":
				isStringCol := false
				if _, ok := val.(string); ok {
					isStringCol = true
				} else if sv, ok := val.(storage.Value); ok && (sv.Type == storage.TypeVarChar || sv.Type == storage.TypeUUID) {
					isStringCol = true
				}

				// If it's a string column or explicitly quoted literal, do strict string comparison (no ParseFloat coercion)
				if isStringCol || rightIsQuoted {
					cmp := strings.Compare(strVal, rightVal)
					switch strings.TrimSpace(op) {
					case ">":
						return cmp > 0
					case ">=", "!<":
						return cmp >= 0
					case "<":
						return cmp < 0
					case "<=", "!>":
						return cmp <= 0
					}
				}

				// Otherwise numeric comparison
				n1, err1 := strconv.ParseFloat(strVal, 64)
				n2, err2 := strconv.ParseFloat(rightVal, 64)
				if err1 == nil && err2 == nil {
					switch strings.TrimSpace(op) {
					case ">":
						return n1 > n2
					case ">=", "!<":
						return n1 >= n2
					case "<":
						return n1 < n2
					case "<=", "!>":
						return n1 <= n2
					}
				}
				return strVal > rightVal
			case "LIKE", "ILIKE":
				isCS := strings.TrimSpace(op) != "ILIKE" && collate != "" && sqlutil.IsCaseSensitiveCollation(collate)
				return sqlutil.MatchLike(rightVal, strVal, !isCS)
			case "NOT LIKE", "NOT ILIKE":
				isCS := strings.TrimSpace(op) != "NOT ILIKE" && collate != "" && sqlutil.IsCaseSensitiveCollation(collate)
				return !sqlutil.MatchLike(rightVal, strVal, !isCS)
			}
		}
	}
	return true
}

// deduplicateRows removes duplicate rows from a result set for SELECT DISTINCT.
func deduplicateRows(rows []map[string]any) []map[string]any {
	return sqlutil.DeduplicateRows(rows)
}

func deduplicateRowsAndValues(rows []map[string]any, values [][]any) ([]map[string]any, [][]any) {
	return sqlutil.DeduplicateRowsAndValues(rows, values)
}

// rewriteViewsAsCTE detects view references in a SELECT query and rewrites the query as a CTE.
// For example:
//
//	SELECT name FROM v_eng WHERE salary > 80000
//	-> WITH v_eng AS (SELECT ... FROM employees WHERE dept='Engineering')
//	   SELECT name FROM v_eng WHERE salary > 80000
//
// Multiple views are accumulated into a single WITH clause.
// Returns the rewritten query and true if at least one view was found.
func (s *Coordinator) rewriteViewsAsCTE(query string) (string, bool) {
	views := s.catalog.ListViews()
	if len(views) == 0 {
		return query, false
	}

	// Collect all views referenced in this query (word-boundary match against view names).
	var cteParts []string
	for _, v := range views {
		if sqlutil.ContainsKeyword(query, v.Name, false) {
			cteParts = append(cteParts, fmt.Sprintf("%s AS (%s)", v.Name, v.Query))
		}
	}

	if len(cteParts) == 0 {
		return query, false
	}

	rewritten := "WITH " + strings.Join(cteParts, ", ") + " " + query
	return rewritten, true
}

// parseEqualityConditions parses AND-separated equality conditions (col = val) from a WHERE clause.
// Returns a map of lowercase column name -> raw value string (without surrounding quotes).
// Non-equality conditions (!=, <, >, LIKE, IN, etc.) are ignored.
func parseEqualityConditions(where string) map[string]string {
	result := make(map[string]string)
	// Split on AND (case-insensitive), skipping parenthesized groups.
	parts := splitOnAnd(where)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		eqIdx := sqlutil.FindTopLevelChar(part, '=')
		if eqIdx > 0 && eqIdx < len(part)-1 {
			prev := part[eqIdx-1]
			next := part[eqIdx+1]
			if prev != '!' && prev != '<' && prev != '>' && prev != '=' && next != '=' {
				rawCol := strings.TrimSpace(part[:eqIdx])
				if !strings.ContainsAny(rawCol, " \t\n(),") {
					if dot := strings.LastIndex(rawCol, "."); dot != -1 {
						rawCol = rawCol[dot+1:]
					}
					rawVal := strings.TrimSpace(part[eqIdx+1:])
					var val string
					if (strings.HasPrefix(rawVal, "N'") || strings.HasPrefix(rawVal, "n'")) && strings.HasSuffix(rawVal, "'") && len(rawVal) >= 3 {
						val = strings.ReplaceAll(rawVal[2:len(rawVal)-1], "''", "'")
					} else {
						val = strings.Trim(rawVal, "'\"")
					}
					result[strings.ToLower(cleanIdentifier(rawCol))] = val
				}
			}
		}
	}
	return result
}

// splitOnAnd splits a WHERE clause on top-level AND keywords,
// respecting parentheses depth so subexpressions are not split.
func splitOnAnd(where string) []string {
	return planner.SplitOnAnd(where)
}

// buildCompositePrefixKey builds a \x00-separated composite key string from the
// prefix columns of a composite index given an equality-condition value map.
func buildCompositePrefixKey(cols []string, eqColVals map[string]string, schema *storage.TableSchema) string {
	return planner.BuildCompositePrefixKey(cols, eqColVals, schema)
}

type indexRangeCond = planner.IndexRangeCond

func parseWhereConditions(where string) (map[string]string, map[string]*planner.IndexRangeCond) {
	return planner.ParseWhereConditions(where)
}

func (s *Coordinator) findIndexAndBounds(schema *storage.TableSchema, whereClause string) (*storage.IndexSchema, *storage.Value, *storage.Value, bool) {
	return planner.FindIndexAndBounds(schema, whereClause)
}


type orderColInfo struct {
	col string
	dir string // "ASC" or "DESC"
}

func parseOrderByClause(orderByClause string) []orderColInfo {
	if strings.TrimSpace(orderByClause) == "" {
		return nil
	}
	items := strings.Split(orderByClause, ",")
	var result []orderColInfo
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.Fields(item)
		if len(parts) == 0 {
			continue
		}
		col := strings.ToLower(cleanIdentifier(parts[0]))
		dir := "ASC"
		if len(parts) > 1 {
			dir = strings.ToUpper(parts[1])
		}
		result = append(result, orderColInfo{col: col, dir: dir})
	}
	return result
}

func getRemainingIndexColumns(idx *storage.IndexSchema, eqColVals map[string]string) []string {
	if idx == nil {
		return nil
	}
	var remaining []string
	for _, idxCol := range idx.Columns {
		idxColLower := strings.ToLower(idxCol)
		if eqColVals != nil && eqColVals[idxColLower] != "" {
			continue // fixed by equality condition
		}
		remaining = append(remaining, idxColLower)
	}
	return remaining
}

func isIndexDescendingOrder(orderByClause string, idx *storage.IndexSchema, eqColVals map[string]string) bool {
	orderItems := parseOrderByClause(orderByClause)
	if len(orderItems) == 0 || idx == nil {
		return false
	}
	remaining := getRemainingIndexColumns(idx, eqColVals)
	if len(orderItems) > len(remaining) {
		return false
	}
	for i, ord := range orderItems {
		if ord.dir != "DESC" {
			return false
		}
		if ord.col != remaining[i] {
			return false
		}
	}
	return true
}

func isIndexAscendingOrder(orderByClause string, idx *storage.IndexSchema, eqColVals map[string]string) bool {
	orderItems := parseOrderByClause(orderByClause)
	if len(orderItems) == 0 || idx == nil {
		return false
	}
	remaining := getRemainingIndexColumns(idx, eqColVals)
	if len(orderItems) > len(remaining) {
		return false
	}
	for i, ord := range orderItems {
		if ord.dir != "ASC" {
			return false
		}
		if ord.col != remaining[i] {
			return false
		}
	}
	return true
}

// execIndexSelect attempts to execute a simple SELECT using a composite or single-column index scan.
// It is called before the VDBE path when the WHERE clause matches a known index on the target table.
// Returns (result, true) on success, (nil, false) if no suitable index exists or the scan fails.
func (s *Coordinator) execIndexSelect(qInfo *planner.QueryInfo, tableName string, start time.Time, cs *ClientSession) (*QueryResult, bool) {
	schema, err := s.engine.GetTable(tableName)
	if err != nil {
		return nil, false
	}
	if len(schema.Indexes) == 0 {
		return nil, false
	}

	whereClause := qInfo.WhereClause
	idx, lowVal, highVal, ok := s.findIndexAndBounds(schema, whereClause)
	if !ok {
		return nil, false
	}

	// Acquire transaction.
	var tx storage.Transaction
	if cs != nil && cs.Tx() != nil {
		tx = cs.Tx()
	} else {
		session, err := s.engine.NewSession()
		if err != nil {
			return nil, false
		}
		defer session.Close()
		t, err := session.BeginTx(storage.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, false
		}
		tx = t
		defer tx.Rollback()
	}

	eqColVals, _ := parseWhereConditions(whereClause)
	descScan := isIndexDescendingOrder(qInfo.OrderByClause, idx, eqColVals)
	ascScan := isIndexAscendingOrder(qInfo.OrderByClause, idx, eqColVals)

	var cursor storage.Cursor
	if descScan {
		cursor, err = tx.IndexScanDesc(tableName, idx.Name, lowVal, highVal)
	} else {
		cursor, err = tx.IndexScan(tableName, idx.Name, lowVal, highVal)
	}
	if err != nil {
		return nil, false
	}
	defer cursor.Close()

	// Determine projection columns.
	projections := qInfo.Projections
	isSelectAll := len(projections) == 0 || (len(projections) == 1 && (projections[0] == "*" || projections[0] == ""))
	if !isSelectAll {
		for _, p := range projections {
			pTrim := strings.TrimSpace(p)
			if strings.ContainsAny(pTrim, "()->+/*") || strings.Contains(strings.ToUpper(pTrim), " AS ") {
				// Complex expression or aliased projection: delegate to Volcano engine
				return nil, false
			}
		}
	}
	var selectedCols []string
	if isSelectAll {
		for _, col := range schema.Columns {
			selectedCols = append(selectedCols, col.Name)
		}
	} else {
		for _, p := range projections {
			selectedCols = append(selectedCols, cleanIdentifier(strings.TrimSpace(p)))
		}
	}

	var rows []map[string]any
	for cursor.Next() {
		rec, err := cursor.Record()
		if err != nil {
			return nil, false
		}
		// Post-filter: apply full WHERE clause to handle non-indexed conditions.
		if !evaluateWhereCondition(rec, whereClause) {
			continue
		}
		row := make(map[string]any, len(selectedCols))
		for _, col := range selectedCols {
			row[col] = formatStorageValue(rec[col])
		}
		rows = append(rows, row)
	}

	// If ORDER BY is present and not satisfied by index scan, fall back to general execution.
	if qInfo.OrderByClause != "" && !descScan && !ascScan {
		return nil, false
	}

	// Apply LIMIT if present.
	if qInfo.LimitCount > 0 && int(qInfo.LimitCount) < len(rows) {
		rows = rows[:qInfo.LimitCount]
	}

	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      selectedCols,
		Rows:         rows,
		RowsAffected: int64(len(rows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, true
}

type outputProj struct {
	alias  string
	source string // "INSERTED", "DELETED", or ""
	column string
}

type outputClauseInfo struct {
	Projs     []outputProj
	IntoTable string
	IntoCols  []string
}

func parseOutputClauseInfo(raw string) outputClauseInfo {
	var info outputClauseInfo
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return info
	}

	projsPart := raw
	if intoIdx := sqlutil.FindTopLevelKeyword(raw, "INTO"); intoIdx != -1 {
		projsPart = strings.TrimSpace(raw[:intoIdx])
		intoPart := strings.TrimSpace(raw[intoIdx+len("INTO"):])
		if lp := sqlutil.FindTopLevelChar(intoPart, '('); lp != -1 {
			info.IntoTable = cleanIdentifier(strings.TrimSpace(intoPart[:lp]))
			rp := sqlutil.FindTopLevelChar(intoPart[lp:], ')')
			if rp != -1 {
				colsStr := intoPart[lp+1 : lp+rp]
				for _, c := range sqlutil.SplitTopLevel(colsStr, ',') {
					if col := cleanIdentifier(c); col != "" {
						info.IntoCols = append(info.IntoCols, col)
					}
				}
			}
		} else {
			info.IntoTable = cleanIdentifier(intoPart)
		}
	}

	info.Projs = parseOutputClause(projsPart)
	return info
}

func parseOutputClause(raw string) []outputProj {
	if intoIdx := sqlutil.FindTopLevelKeyword(raw, "INTO"); intoIdx != -1 {
		raw = strings.TrimSpace(raw[:intoIdx])
	}
	parts := sqlutil.SplitTopLevel(raw, ',')
	var projs []outputProj
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		alias := ""
		expr := p
		if idx := sqlutil.FindTopLevelKeyword(p, "AS"); idx != -1 {
			expr = strings.TrimSpace(p[:idx])
			alias = cleanIdentifier(p[idx+len("AS"):])
		}
		source := ""
		col := expr
		if dot := strings.LastIndex(expr, "."); dot != -1 {
			prefix := strings.ToUpper(cleanIdentifier(expr[:dot]))
			if prefix == "INSERTED" || prefix == "DELETED" {
				source = prefix
			}
			col = cleanIdentifier(expr[dot+1:])
		} else {
			col = cleanIdentifier(col)
		}
		if alias == "" {
			alias = col
		}
		projs = append(projs, outputProj{
			alias:  alias,
			source: source,
			column: col,
		})
	}
	return projs
}

func evaluateOutputRecord(projs []outputProj, oldRec, newRec storage.Record, schema *storage.TableSchema) (map[string]storage.Value, []string) {
	row := make(map[string]storage.Value, len(projs))
	var cols []string

	for _, p := range projs {
		if p.column == "*" {
			targetRec := newRec
			if p.source == "DELETED" {
				targetRec = oldRec
			}
			if schema != nil {
				for _, c := range schema.Columns {
					cols = append(cols, c.Name)
					if targetRec != nil {
						if v, ok := targetRec.Get(c.Name); ok {
							row[c.Name] = v
						} else {
							row[c.Name] = storage.NullValue(c.Type)
						}
					} else {
						row[c.Name] = storage.NullValue(c.Type)
					}
				}
			}
			continue
		}

		targetRec := newRec
		if p.source == "DELETED" {
			targetRec = oldRec
		} else if p.source == "" && newRec == nil && oldRec != nil {
			targetRec = oldRec
		}

		var sv storage.Value = storage.NullValue(storage.TypeVarChar)
		if targetRec != nil {
			if v, ok := targetRec.Get(p.column); ok {
				sv = v
			} else if schema != nil {
				if colSchema, _, found := schema.FindColumn(p.column); found {
					if v, ok := targetRec.Get(colSchema.Name); ok {
						sv = v
					}
				}
			}
		}
		row[p.alias] = sv
		cols = append(cols, p.alias)
	}
	return row, cols
}

func evaluateOutputRow(projs []outputProj, oldRec, newRec storage.Record, schema *storage.TableSchema) (map[string]any, []string) {
	rec, cols := evaluateOutputRecord(projs, oldRec, newRec, schema)
	row := make(map[string]any, len(cols))
	for _, c := range cols {
		row[c] = formatStorageValue(rec[c])
	}
	return row, cols
}

func appendOutputRow(projs []outputProj, oldRec, newRec storage.Record, schema *storage.TableSchema, outputRows *[]map[string]any, outputVals *[][]any) {
	if len(projs) == 0 {
		return
	}
	outRow, cols := evaluateOutputRow(projs, oldRec, newRec, schema)
	*outputRows = append(*outputRows, outRow)
	vals := make([]any, len(cols))
	for i, c := range cols {
		vals[i] = outRow[c]
	}
	*outputVals = append(*outputVals, vals)
}

// ensureExistsLimit1 rewrites an uncorrelated subquery inside an EXISTS clause
// to push down LIMIT 1, avoiding full-table buffering and enabling early exit on 1 row.
func ensureExistsLimit1(rawSQL string) string {
	trimmed := strings.TrimSpace(rawSQL)
	if trimmed == "" {
		return rawSQL
	}
	if sqlutil.ContainsKeyword(trimmed, "LIMIT", false) || sqlutil.ContainsKeyword(trimmed, "TOP", false) {
		return rawSQL
	}
	semi := ""
	if strings.HasSuffix(trimmed, ";") {
		semi = ";"
		trimmed = strings.TrimSpace(trimmed[:len(trimmed)-1])
	}
	return trimmed + " LIMIT 1" + semi
}

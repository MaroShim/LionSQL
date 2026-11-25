package execution

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

// FilterIterator evaluates a filter condition (WHERE or HAVING) over tuples streamed from a child iterator in row-by-row or vectorized chunk mode.
type FilterIterator struct {
	child            Iterator
	condition        string
	parsed           proc.Expr
	hasParsed        bool
	hasSubquery      bool
	subqueryExecutor func(sql string) ([]map[string]any, []string, error)
	sessionVars      map[string]any
	inChunk          Chunk
	outChunk         Chunk
	outIdx           int
}

// NewFilterIterator creates a new filter operator.
func NewFilterIterator(child Iterator, condition string) *FilterIterator {
	f := &FilterIterator{
		child:     child,
		condition: strings.TrimSpace(condition),
	}
	if f.condition != "" {
		p := proc.NewParser(proc.NewLexer(f.condition))
		expr := p.ParseExpr()
		if expr != nil && len(p.Errors()) == 0 {
			f.parsed = proc.FoldConstants(expr)
			f.hasParsed = true
			if lit, ok := f.parsed.(*proc.LiteralExpr); ok {
				if lit.Val.Type == storage.TypeBool {
					if lit.Val.BVal {
						f.condition = "1=1"
					} else {
						f.condition = "1=0"
					}
				}
			}
			f.hasSubquery = proc.ExprHasSubquery(f.parsed)
		} else {
			f.hasSubquery = sqlutil.ContainsKeyword(f.condition, "SELECT", true) || sqlutil.ContainsKeyword(f.condition, "EXISTS", true)
		}
	}
	return f
}

// SetSubqueryExecutor sets an optional subquery executor for evaluating correlated or scalar subqueries.
func (f *FilterIterator) SetSubqueryExecutor(exec func(sql string) ([]map[string]any, []string, error)) {
	f.subqueryExecutor = exec
}

// SetSessionVars sets session variables context for evaluating variable expressions (@var) in filter predicates.
func (f *FilterIterator) SetSessionVars(vars map[string]any) {
	f.sessionVars = vars
}

func (f *FilterIterator) Open(ctx context.Context) error {
	if f.child == nil {
		return fmt.Errorf("filter: child iterator is nil")
	}
	f.inChunk.Reset()
	f.outChunk.Reset()
	f.outIdx = 0
	return f.child.Open(ctx)
}

func (f *FilterIterator) NextChunk(ctx context.Context, dest *Chunk) error {
	dest.Reset()
	if f.child == nil {
		return io.EOF
	}

	for dest.Len() < DefaultChunkSize {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := PullChunk(ctx, f.child, &f.inChunk, DefaultChunkSize)
		if err != nil && err != io.EOF {
			return err
		}
		if f.inChunk.Len() == 0 && err == io.EOF {
			if dest.Len() > 0 {
				return nil
			}
			return io.EOF
		}

		// Vectorized predicate evaluation
		if f.condition == "" || f.condition == "1=1" || f.condition == "1 = 1" || strings.EqualFold(f.condition, "NOT 1=0") || strings.EqualFold(f.condition, "true") {
			dest.Tuples = append(dest.Tuples, f.inChunk.Tuples...)
		} else if f.condition == "1=0" || f.condition == "1 = 0" || strings.EqualFold(f.condition, "NOT 1=1") || strings.EqualFold(f.condition, "false") {
			// matches none
		} else {
			for _, t := range f.inChunk.Tuples {
				matched, evalErr := f.evaluateErr(t)
				if evalErr != nil {
					return evalErr
				}
				if matched {
					dest.Tuples = append(dest.Tuples, t)
				}
			}
		}

		if err == io.EOF {
			break
		}
	}

	if dest.Len() == 0 {
		return io.EOF
	}
	return nil
}

func (f *FilterIterator) Next(ctx context.Context) (Tuple, error) {
	if f.outIdx < len(f.outChunk.Tuples) {
		t := f.outChunk.Tuples[f.outIdx]
		f.outIdx++
		return t, nil
	}
	f.outChunk.Reset()
	f.outIdx = 0
	if err := f.NextChunk(ctx, &f.outChunk); err != nil {
		return Tuple{}, err
	}
	if len(f.outChunk.Tuples) == 0 {
		return Tuple{}, io.EOF
	}
	t := f.outChunk.Tuples[0]
	f.outIdx = 1
	return t, nil
}

func (f *FilterIterator) Close() error {
	if f.child != nil {
		return f.child.Close()
	}
	return nil
}

// EarlyAbort propagates the short-circuit termination to its child.
func (f *FilterIterator) EarlyAbort() {
	if a, ok := f.child.(EarlyAbortable); ok {
		a.EarlyAbort()
	}
}

func (f *FilterIterator) Schema() []string {
	if f.child != nil {
		return f.child.Schema()
	}
	return nil
}

func (f *FilterIterator) ColIndexMap() *ColumnIndexMap {
	if f.child != nil {
		return f.child.ColIndexMap()
	}
	return nil
}

func (f *FilterIterator) evaluate(t Tuple) bool {
	b, _ := f.evaluateErr(t)
	return b
}

func (f *FilterIterator) evaluateErr(t Tuple) (bool, error) {
	if f.condition == "" || f.condition == "1=1" || f.condition == "1 = 1" || strings.EqualFold(f.condition, "NOT 1=0") || strings.EqualFold(f.condition, "true") {
		return true, nil
	}
	if f.condition == "1=0" || f.condition == "1 = 0" || strings.EqualFold(f.condition, "NOT 1=1") || strings.EqualFold(f.condition, "false") {
		return false, nil
	}

	var subExec proc.SubqueryExecutor
	if f.hasSubquery && f.subqueryExecutor != nil {
		var evalMap map[string]any
		subExec = func(rawSubSQL string) ([]map[string]any, []string, error) {
			if evalMap == nil {
				evalMap = t.ToEvalMap()
			}
			paramSQL := sqlutil.ParameterizeSubquery(rawSubSQL, evalMap)
			return f.subqueryExecutor(paramSQL)
		}
	}

	if f.hasParsed && f.parsed != nil {
		return EvalTupleBoolExprWithContextErr(t, f.parsed, subExec, f.sessionVars)
	}

	return EvalConditionTupleWithContextErr(t, f.condition, subExec, f.sessionVars)
}

// EvalConditionTuple evaluates a SQL condition string against a Tuple.
func EvalConditionTuple(t Tuple, cond string) bool {
	return EvalConditionTupleWithSubquery(t, cond, nil)
}

// EvalConditionTupleWithSubquery evaluates a SQL condition string against a Tuple with optional SubqueryExecutor.
func EvalConditionTupleWithSubquery(t Tuple, cond string, subExec proc.SubqueryExecutor) bool {
	return EvalConditionTupleWithContext(t, cond, subExec, nil)
}

// EvalConditionTupleWithContext evaluates a SQL condition string against a Tuple with optional SubqueryExecutor and session variables.
func EvalConditionTupleWithContext(t Tuple, cond string, subExec proc.SubqueryExecutor, sessionVars map[string]any) bool {
	b, _ := EvalConditionTupleWithContextErr(t, cond, subExec, sessionVars)
	return b
}

// EvalConditionTupleWithContextErr evaluates a SQL condition string against a Tuple, returning any evaluation error.
func EvalConditionTupleWithContextErr(t Tuple, cond string, subExec proc.SubqueryExecutor, sessionVars map[string]any) (bool, error) {
	cond = strings.TrimSpace(cond)
	if cond == "" || cond == "1=1" || cond == "1 = 1" {
		return true, nil
	}
	if cond == "1=0" || cond == "1 = 0" {
		return false, nil
	}

	// Try AST expression parsing
	p := proc.NewParser(proc.NewLexer(cond))
	expr := p.ParseExpr()
	if expr != nil && len(p.Errors()) == 0 {
		return EvalTupleBoolExprWithContextErr(t, expr, subExec, sessionVars)
	}

	orParts := splitLogicalParts(cond, " OR ")
	if len(orParts) > 1 {
		for _, part := range orParts {
			matched, err := EvalConditionTupleWithContextErr(t, part, subExec, sessionVars)
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
		}
		return false, nil
	}

	andParts := splitLogicalParts(cond, " AND ")
	if len(andParts) > 1 {
		for _, part := range andParts {
			matched, err := EvalConditionTupleWithContextErr(t, part, subExec, sessionVars)
			if err != nil {
				return false, err
			}
			if !matched {
				return false, nil
			}
		}
		return true, nil
	}

	return evalSinglePredicate(t, cond), nil
}

func evalSinglePredicate(t Tuple, cond string) bool {
	cond = strings.TrimSpace(cond)
	if strings.HasPrefix(cond, "(") && strings.HasSuffix(cond, ")") {
		inner := strings.TrimSpace(cond[1 : len(cond)-1])
		if !strings.Contains(inner, "(") && !strings.Contains(inner, ")") {
			cond = inner
		}
	}

	// IS NULL / IS NOT NULL
	if pred, ok := sqlutil.ParseIsNullPredicate(cond); ok {
		val, ok := lookupVal(t, pred.Column)
		if pred.IsNot {
			return ok && val != nil
		}
		return !ok || val == nil
	}

	// IN / NOT IN
	if pred, ok := sqlutil.ParseInPredicate(cond); ok {
		val, ok := lookupVal(t, pred.Column)
		if !ok || val == nil {
			return false
		}
		// Fast-Path: Materialized In-Memory Subquery HashSet ($O(1)$)
		if len(pred.Items) == 1 {
			trimmedItem := strings.TrimSpace(pred.Items[0])
			if strings.HasPrefix(trimmedItem, "__subq_hashset_") && strings.HasSuffix(trimmedItem, "__") {
				if set, found := proc.LookupSubqueryHashSet(trimmedItem); found {
					normKey, isNull := proc.NormalizeSubqueryKey(val)
					return set.Evaluate(normKey, isNull, pred.IsNot)
				}
			}
		}
		strVal := fmt.Sprintf("%v", val)
		found := false
		for _, rawItem := range pred.Items {
			item := strings.Trim(strings.TrimSpace(rawItem), "'\"")
			if strings.EqualFold(strVal, item) {
				found = true
				break
			}
		}
		if pred.IsNot {
			return !found
		}
		return found
	}

	// Comparison operators
	ops := []string{"!=", "<>", "<=", ">=", "=", "<", ">", " LIKE "}
	for _, op := range ops {
		idx := strings.Index(strings.ToUpper(cond), op)
		if idx != -1 {
			leftCol := strings.TrimSpace(cond[:idx])
			rawRight := strings.TrimSpace(cond[idx+len(op):])
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

			val, ok := lookupVal(t, leftCol)
			if !ok || val == nil {
				return false
			}
			if sVal, ok := val.(storage.Value); ok && sVal.IsNull {
				return false
			}

			if !rightIsQuoted && strings.EqualFold(rightVal, "NULL") {
				return false
			}

			leftStr := fmt.Sprintf("%v", val)
			if sv, ok := val.(storage.Value); ok {
				leftStr = fmt.Sprintf("%v", sv.Interface())
			}
			var parsedRight any
			if rightIsQuoted {
				parsedRight = rightVal
			} else {
				parsedRight = parseLiteralValue(rightVal)
			}
			if parsedRight == nil {
				return false
			}
			cmp := CompareValues(val, parsedRight)

			switch op {
			case "=":
				return cmp == 0
			case "!=", "<>":
				return cmp != 0
			case "<":
				return cmp < 0
			case "<=":
				return cmp <= 0
			case ">":
				return cmp > 0
			case ">=":
				return cmp >= 0
			case " LIKE ":
				return sqlutil.MatchLike(rightVal, leftStr, true)
			}
		}
	}

	// Standalone boolean expression (e.g. GREP(msg, 'err'))
	if val, ok := lookupVal(t, cond); ok && val != nil {
		switch v := val.(type) {
		case bool:
			return v
		case int64:
			return v != 0
		case int:
			return v != 0
		case string:
			return len(v) > 0 && !strings.EqualFold(v, "false") && v != "0"
		}
	}

	return false
}

func lookupVal(t Tuple, col string) (any, bool) {
	col = strings.Trim(col, "\"`[]")
	if colIdx := sqlutil.FindTopLevelKeyword(col, "COLLATE"); colIdx != -1 {
		col = strings.TrimSpace(col[:colIdx])
	}
	if v, ok := t.GetOK(col); ok {
		return v, true
	}
	// Bare column if table prefix present (e.g. u.data -> data)
	// ONLY if the tuple's relation matches the qualifier prefix or has no known table qualifiers.
	if dotIdx := strings.Index(col, "."); dotIdx != -1 {
		prefix := strings.ToLower(cleanIdentifierParts(col[:dotIdx]))
		cmap := t.Index
		if cmap == nil || len(cmap.tableCols) == 0 || cmap.tableCols[prefix] != nil {
			bare := col[dotIdx+1:]
			if v, ok := t.GetOK(bare); ok {
				return v, true
			}
		}
	}

	// If column contains operators (e.g. ->, ->>, +, -, *, /, %) or functions (e.g. JSON_VALUE)
	if strings.ContainsAny(col, " +-*/%()") || strings.Contains(col, "->") {
		p := proc.NewParser(proc.NewLexer(col))
		expr := p.ParseExpr()
		if expr != nil && len(p.Errors()) == 0 {
			if res, ok := EvalTupleExpr(t, expr); ok {
				return res, true
			}
		}
	}
	return nil, false
}

func isPureInteger(s string) bool {
	if s == "" {
		return false
	}
	start := 0
	if s[0] == '-' || s[0] == '+' {
		start = 1
		for start < len(s) && s[start] == ' ' {
			start++
		}
	}
	if start >= len(s) {
		return false
	}
	if len(s)-start > 1 && s[start] == '0' {
		return false // Leading zero means code or formatted string
	}
	for i := start; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func parseLiteralValue(s string) any {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "NULL") {
		return nil
	}
	if strings.EqualFold(s, "TRUE") {
		return true
	}
	if strings.EqualFold(s, "FALSE") {
		return false
	}
	// Number parsing without coercion of formatted strings
	if isPureInteger(s) {
		normalized := s
		if (s[0] == '-' || s[0] == '+') && strings.Contains(s, " ") {
			normalized = string(s[0]) + strings.TrimSpace(s[1:])
		}
		if n, err := strconv.ParseInt(normalized, 10, 64); err == nil {
			return n
		}
	}
	if strings.Count(s, ".") == 1 {
		normalized := s
		if len(s) > 1 && (s[0] == '-' || s[0] == '+') && strings.Contains(s, " ") {
			normalized = string(s[0]) + strings.TrimSpace(s[1:])
		}
		if d, err := storage.ParseDecimal(normalized); err == nil {
			return d
		}
		var f float64
		if _, err := fmt.Sscanf(normalized, "%f", &f); err == nil {
			return f
		}
	}
	return s
}

func splitLogicalParts(s, op string) []string {
	var parts []string
	var cur strings.Builder
	inQuote := false
	quoteChar := byte(0)
	parenDepth := 0
	opLen := len(op)
	upper := strings.ToUpper(s)

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inQuote {
			if ch == quoteChar && (i == 0 || s[i-1] != '\\') {
				inQuote = false
			}
			cur.WriteByte(ch)
		} else {
			if ch == '\'' || ch == '"' {
				inQuote = true
				quoteChar = ch
				cur.WriteByte(ch)
			} else if ch == '(' {
				parenDepth++
				cur.WriteByte(ch)
			} else if ch == ')' {
				if parenDepth > 0 {
					parenDepth--
				}
				cur.WriteByte(ch)
			} else if parenDepth == 0 && i+opLen <= len(s) && upper[i:i+opLen] == op {
				parts = append(parts, strings.TrimSpace(cur.String()))
				cur.Reset()
				i += opLen - 1
			} else {
				cur.WriteByte(ch)
			}
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, strings.TrimSpace(cur.String()))
	}
	return parts
}

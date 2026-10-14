package execution

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"sqlion/pkg/proc"
	"sqlion/pkg/storage"
)

// FilterIterator evaluates a filter condition (WHERE or HAVING) over tuples streamed from a child iterator in row-by-row or vectorized chunk mode.
type FilterIterator struct {
	child     Iterator
	condition string
	parsed    proc.Expr
	hasParsed bool
	inChunk   Chunk
	outChunk  Chunk
	outIdx    int
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
		}
	}
	return f
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
				if f.evaluate(t) {
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
	if f.condition == "" || f.condition == "1=1" || f.condition == "1 = 1" || strings.EqualFold(f.condition, "NOT 1=0") || strings.EqualFold(f.condition, "true") {
		return true
	}
	if f.condition == "1=0" || f.condition == "1 = 0" || strings.EqualFold(f.condition, "NOT 1=1") || strings.EqualFold(f.condition, "false") {
		return false
	}

	if f.hasParsed && f.parsed != nil {
		return proc.EvalBoolExpr(f.parsed, t.ToEvalMap(), nil)
	}

	return EvalConditionTuple(t, f.condition)
}

// EvalConditionTuple evaluates a SQL condition string against a Tuple.
func EvalConditionTuple(t Tuple, cond string) bool {
	cond = strings.TrimSpace(cond)
	if cond == "" || cond == "1=1" || cond == "1 = 1" {
		return true
	}
	if cond == "1=0" || cond == "1 = 0" {
		return false
	}

	// Try AST expression parsing
	p := proc.NewParser(proc.NewLexer(cond))
	expr := p.ParseExpr()
	if expr != nil && len(p.Errors()) == 0 {
		return proc.EvalBoolExpr(expr, t.ToEvalMap(), nil)
	}

	orParts := splitLogicalParts(cond, " OR ")
	if len(orParts) > 1 {
		for _, part := range orParts {
			if EvalConditionTuple(t, part) {
				return true
			}
		}
		return false
	}

	andParts := splitLogicalParts(cond, " AND ")
	if len(andParts) > 1 {
		for _, part := range andParts {
			if !EvalConditionTuple(t, part) {
				return false
			}
		}
		return true
	}

	return evalSinglePredicate(t, cond)
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
	upper := strings.ToUpper(cond)
	if idx := strings.Index(upper, " IS NOT NULL"); idx != -1 {
		col := strings.TrimSpace(cond[:idx])
		val, ok := lookupVal(t, col)
		return ok && val != nil
	}
	if idx := strings.Index(upper, " IS NULL"); idx != -1 {
		col := strings.TrimSpace(cond[:idx])
		val, ok := lookupVal(t, col)
		return !ok || val == nil
	}

	// IN / NOT IN
	inRe := regexp.MustCompile(`(?is)^\s*([a-zA-Z0-9_\.]+)\s+(NOT\s+)?IN\s*\((.*?)\)\s*$`)
	if m := inRe.FindStringSubmatch(cond); len(m) == 4 {
		colName := m[1]
		isNot := strings.TrimSpace(m[2]) != ""
		valList := m[3]
		val, ok := lookupVal(t, colName)
		if !ok || val == nil {
			return false
		}
		strVal := fmt.Sprintf("%v", val)
		found := false
		for _, rawItem := range strings.Split(valList, ",") {
			item := strings.Trim(strings.TrimSpace(rawItem), "'\"")
			if strings.EqualFold(strVal, item) {
				found = true
				break
			}
		}
		if isNot {
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
			rightVal := strings.TrimSpace(cond[idx+len(op):])
			rightVal = strings.Trim(rightVal, "';\"")

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

			leftStr := fmt.Sprintf("%v", val)
			cmp := CompareValues(val, parseLiteralValue(rightVal))

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
				pattern := strings.ReplaceAll(regexp.QuoteMeta(rightVal), "%", ".*")
				pattern = strings.ReplaceAll(pattern, "_", ".")
				matched, _ := regexp.MatchString("(?i)^"+pattern+"$", leftStr)
				return matched
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
	if v, ok := t.GetOK(col); ok {
		return v, true
	}
	// Bare column if table prefix present (e.g. u.data -> data)
	if dotIdx := strings.Index(col, "."); dotIdx != -1 {
		bare := col[dotIdx+1:]
		if v, ok := t.GetOK(bare); ok {
			return v, true
		}
	}

	// If column contains operators (e.g. ->, ->>) or functions (e.g. JSON_VALUE)
	if strings.Contains(col, "->") || strings.Contains(col, "(") {
		p := proc.NewParser(proc.NewLexer(col))
		expr := p.ParseExpr()
		if expr != nil && len(p.Errors()) == 0 {
			res, err := proc.EvalRowExpr(expr, t.ToEvalMap(), nil)
			if err == nil {
				return res, true
			}
		}
	}
	return nil, false
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
	// Number parsing
	var n int64
	if _, err := fmt.Sscanf(s, "%d", &n); err == nil {
		return n
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err == nil {
		return f
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

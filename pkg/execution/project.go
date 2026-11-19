package execution

import (
	"context"
	"fmt"
	"io"
	"strings"

	"lionsql/pkg/planner"
	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
)

type projField struct {
	expr   string
	alias  string
	srcIdx int
	parsed proc.Expr
}

// ProjectionIterator evaluates projected expressions and renames columns in row-by-row or vectorized chunk mode.
type ProjectionIterator struct {
	child            Iterator
	fields           []projField
	isSelectAll      bool
	schema           []string
	colIndexMap      *ColumnIndexMap
	hasSubquery      bool
	subqueryExecutor func(sql string) ([]map[string]any, []string, error)
	sessionVars      map[string]any
	inChunk          Chunk
	outChunk         Chunk
	outIdx           int
}

// SetSubqueryExecutor sets an optional subquery executor for evaluating scalar or correlated subqueries in projections.
func (p *ProjectionIterator) SetSubqueryExecutor(exec func(sql string) ([]map[string]any, []string, error)) {
	p.subqueryExecutor = exec
}

// SetSessionVars sets session variables context for evaluating variable expressions (@var) in projections.
func (p *ProjectionIterator) SetSessionVars(vars map[string]any) {
	p.sessionVars = vars
}

// isWildcard reports whether a projection item is an unqualified or table-qualified wildcard (* or alias.*).
func isWildcard(item string) bool {
	item = strings.TrimSpace(item)
	return item == "*" || strings.HasSuffix(item, ".*")
}

// NewProjectionIterator creates a new projection operator.
func NewProjectionIterator(child Iterator, projections []string) *ProjectionIterator {
	p := &ProjectionIterator{
		child: child,
	}

	if len(projections) == 0 || (len(projections) == 1 && (projections[0] == "" || strings.TrimSpace(projections[0]) == "*")) {
		if child != nil {
			var hasHidden bool
			for _, col := range child.Schema() {
				if storage.IsSyntheticPKName(col) {
					hasHidden = true
					break
				}
			}
			if hasHidden {
				for i, col := range child.Schema() {
					if storage.IsSyntheticPKName(col) {
						continue
					}
					p.fields = append(p.fields, projField{expr: col, alias: col, srcIdx: i})
					p.schema = append(p.schema, col)
				}
				p.isSelectAll = false
				return p
			}
		}
		p.isSelectAll = true
		return p
	}

	addField := func(expr, alias string) {
		schemaAlias := alias
		for _, existing := range p.schema {
			if strings.EqualFold(existing, schemaAlias) {
				cleanExpr := sqlutil.CleanIdentifier(expr)
				if cleanExpr != "" && !strings.EqualFold(cleanExpr, schemaAlias) {
					schemaAlias = cleanExpr
				} else {
					schemaAlias = fmt.Sprintf("%s_%d", alias, len(p.schema)+1)
				}
				break
			}
		}
		f := projField{expr: expr, alias: alias, srcIdx: -1}
		parser := proc.NewParser(proc.NewLexer(expr))
		if ast := parser.ParseExpr(); ast != nil && len(parser.Errors()) == 0 {
			f.parsed = ast
			if proc.ExprHasSubquery(f.parsed) {
				p.hasSubquery = true
			}
		} else if sqlutil.ContainsKeyword(expr, "SELECT", true) {
			p.hasSubquery = true
		}
		p.fields = append(p.fields, f)
		p.schema = append(p.schema, schemaAlias)
	}

	for _, item := range projections {
		item = strings.TrimSpace(item)
		if strings.HasPrefix(strings.ToUpper(item), "ALL ") {
			item = strings.TrimSpace(item[4:])
		}
		if isWildcard(item) {
			prefix := ""
			if dot := strings.Index(item, "."); dot != -1 {
				prefix = strings.TrimSpace(item[:dot])
			}
			if prefix == "" || prefix == "*" {
				if child != nil {
					for i, col := range child.Schema() {
						if storage.IsSyntheticPKName(col) {
							continue
						}
						p.fields = append(p.fields, projField{expr: col, alias: col, srcIdx: i})
						p.schema = append(p.schema, col)
					}
				}
			} else {
				cleanPrefix := sqlutil.CleanIdentifier(prefix)
				var tblCols []string
				if child != nil && child.ColIndexMap() != nil {
					tblCols = child.ColIndexMap().TableColumns(cleanPrefix)
				}
				if len(tblCols) > 0 {
					for _, col := range tblCols {
						if storage.IsSyntheticPKName(col) {
							continue
						}
						addField(cleanPrefix+"."+col, col)
					}
				} else if child != nil {
					matchedAny := false
					for _, col := range child.Schema() {
						if storage.IsSyntheticPKName(col) {
							continue
						}
						if strings.HasPrefix(strings.ToLower(col), strings.ToLower(cleanPrefix)+".") {
							colName := col[len(cleanPrefix)+1:]
							addField(col, colName)
							matchedAny = true
						}
					}
					if !matchedAny {
						for _, col := range child.Schema() {
							if storage.IsSyntheticPKName(col) {
								continue
							}
							addField(col, col)
						}
					}
				}
			}
			continue
		}

		if w, ok := planner.ParseWindowDef(item); ok {
			alias := w.Alias
			if alias == "" {
				alias = strings.ToLower(w.Function)
			}
			p.fields = append(p.fields, projField{expr: w.RawExpr, alias: alias, srcIdx: -1})
			p.schema = append(p.schema, alias)
			continue
		}

		// Check for [AS] alias: e.g. "col AS alias" or "col alias"
		if expr, alias, hasAs := sqlutil.SplitAsAlias(item); hasAs {
			addField(expr, alias)
		} else {
			parts := sqlutil.SplitTopLevelTokens(item)
			if len(parts) == 2 && !strings.ContainsAny(parts[0], "()+-*/%") && !strings.ContainsAny(parts[1], "()+-*/%") {
				addField(parts[0], sqlutil.CleanIdentifier(parts[1]))
			} else {
				// Bare expression / column name
				alias := item
				if dotIdx := strings.LastIndex(alias, "."); dotIdx != -1 {
					alias = alias[dotIdx+1:]
				}
				alias = sqlutil.CleanIdentifier(alias)
				addField(item, alias)
			}
		}
	}

	return p
}

func (p *ProjectionIterator) Open(ctx context.Context) error {
	if p.child == nil {
		return fmt.Errorf("projection: child iterator is nil")
	}
	p.inChunk.Reset()
	p.outChunk.Reset()
	p.outIdx = 0
	if err := p.child.Open(ctx); err != nil {
		return err
	}
	p.colIndexMap = NewColumnIndexMapFromCols(p.Schema())
	return nil
}

func (p *ProjectionIterator) projectTuple(tuple Tuple) Tuple {
	if p.isSelectAll && len(p.fields) == 0 {
		tuple.Materialize()
		if p.child != nil {
			schemaLen := len(p.child.Schema())
			if len(tuple.Values) < schemaLen {
				padded := make([]any, schemaLen)
				copy(padded, tuple.Values)
				tuple.Values = padded
			}
		}
		return tuple
	}
	tuple.Materialize()
	vals := make([]any, len(p.fields))

	var subExec proc.SubqueryExecutor
	if p.hasSubquery && p.subqueryExecutor != nil {
		var evalMap map[string]any
		subExec = func(rawSubSQL string) ([]map[string]any, []string, error) {
			if evalMap == nil {
				evalMap = tuple.ToEvalMap()
			}
			paramSQL := sqlutil.ParameterizeSubquery(rawSubSQL, evalMap)
			return p.subqueryExecutor(paramSQL)
		}
	}

	for i, f := range p.fields {
		if f.srcIdx >= 0 {
			if f.srcIdx < len(tuple.Values) {
				vals[i] = tuple.Values[f.srcIdx]
			} else {
				vals[i] = nil
			}
			continue
		}
		if f.parsed != nil {
			if _, isLit := f.parsed.(*proc.LiteralExpr); isLit {
				if res, ok := EvalTupleExprWithContext(tuple, f.parsed, subExec, p.sessionVars); ok {
					vals[i] = res
					continue
				}
			}
		}
		// Prioritize qualified expression (e.g. "o.amount", "u.name") over unqualified alias
		// to prevent shadowing when multiple relations share identical column names.
		if val, ok := tuple.GetOK(f.expr); ok {
			vals[i] = val
			continue
		}
		if f.alias != "" {
			if val, ok := tuple.GetOK(f.alias); ok {
				vals[i] = val
				continue
			}
		}
		if f.parsed != nil {
			if res, ok := EvalTupleExprWithContext(tuple, f.parsed, subExec, p.sessionVars); ok {
				vals[i] = res
				continue
			}
		}
		val, found := lookupVal(tuple, f.expr)
		if !found {
			val, found = lookupVal(tuple, f.alias)
		}
		if !found && p.sessionVars != nil {
			if v, ok := p.sessionVars[f.expr]; ok {
				val = v
				found = true
			} else if v, ok := p.sessionVars[strings.TrimPrefix(f.expr, "@")]; ok {
				val = v
				found = true
			} else if v, ok := p.sessionVars[strings.ToLower(f.expr)]; ok {
				val = v
				found = true
			} else if v, ok := p.sessionVars[strings.ToLower(strings.TrimPrefix(f.expr, "@"))]; ok {
				val = v
				found = true
			}
		}
		vals[i] = val
	}

	return Tuple{
		Values: vals,
		Index:  p.colIndexMap,
	}
}

func (p *ProjectionIterator) NextChunk(ctx context.Context, dest *Chunk) error {
	dest.Reset()
	if p.child == nil {
		return io.EOF
	}

	err := PullChunk(ctx, p.child, &p.inChunk, DefaultChunkSize)
	if err != nil && err != io.EOF {
		return err
	}
	if p.inChunk.Len() == 0 {
		return io.EOF
	}

	if p.isSelectAll && len(p.fields) == 0 {
		dest.Tuples = append(dest.Tuples, p.inChunk.Tuples...)
		return nil
	}

	for _, tuple := range p.inChunk.Tuples {
		dest.Tuples = append(dest.Tuples, p.projectTuple(tuple))
	}
	return nil
}

func (p *ProjectionIterator) Next(ctx context.Context) (Tuple, error) {
	if p.outIdx < len(p.outChunk.Tuples) {
		t := p.outChunk.Tuples[p.outIdx]
		p.outIdx++
		return t, nil
	}
	p.outChunk.Reset()
	p.outIdx = 0
	if err := p.NextChunk(ctx, &p.outChunk); err != nil {
		return Tuple{}, err
	}
	if len(p.outChunk.Tuples) == 0 {
		return Tuple{}, io.EOF
	}
	t := p.outChunk.Tuples[0]
	p.outIdx = 1
	return t, nil
}

func (p *ProjectionIterator) Close() error {
	if p.child != nil {
		return p.child.Close()
	}
	return nil
}

func (p *ProjectionIterator) Schema() []string {
	if p.isSelectAll && len(p.fields) == 0 && p.child != nil {
		return p.child.Schema()
	}
	return p.schema
}

func (p *ProjectionIterator) ColIndexMap() *ColumnIndexMap {
	if p.isSelectAll && len(p.fields) == 0 && p.child != nil {
		return p.child.ColIndexMap()
	}
	if p.colIndexMap == nil {
		p.colIndexMap = NewColumnIndexMapFromCols(p.Schema())
	}
	return p.colIndexMap
}

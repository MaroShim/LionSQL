package execution

import (
	"context"
	"fmt"
	"io"
	"strings"

	"lionsql/pkg/planner"
	"lionsql/pkg/proc"
)

type projField struct {
	expr   string
	alias  string
	parsed proc.Expr
}

// ProjectionIterator evaluates projected expressions and renames columns in row-by-row or vectorized chunk mode.
type ProjectionIterator struct {
	child       Iterator
	fields      []projField
	isSelectAll bool
	schema      []string
	colIndexMap *ColumnIndexMap
	inChunk     Chunk
	outChunk    Chunk
	outIdx      int
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

	if len(projections) == 0 || (len(projections) == 1 && (projections[0] == "" || isWildcard(projections[0]))) {
		if child != nil {
			var visibleCols []string
			var hasHidden bool
			for _, col := range child.Schema() {
				if strings.EqualFold(col, "_row_id") || strings.EqualFold(col, "_dt_row_id") {
					hasHidden = true
					continue
				}
				visibleCols = append(visibleCols, col)
			}
			if hasHidden {
				for _, col := range visibleCols {
					p.fields = append(p.fields, projField{expr: col, alias: col})
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
		f := projField{expr: expr, alias: alias}
		if strings.ContainsAny(expr, " +-/*%()><=!|&,") || strings.Contains(expr, "->") {
			parser := proc.NewParser(proc.NewLexer(expr))
			if ast := parser.ParseExpr(); ast != nil && len(parser.Errors()) == 0 {
				f.parsed = ast
			}
		}
		p.fields = append(p.fields, f)
		p.schema = append(p.schema, alias)
	}

	for _, item := range projections {
		item = strings.TrimSpace(item)
		if isWildcard(item) {
			if child != nil {
				for _, col := range child.Schema() {
					addField(col, col)
				}
			}
			continue
		}

		if w, ok := planner.ParseWindowDef(item); ok {
			alias := w.Alias
			if alias == "" {
				alias = strings.ToLower(w.Function)
			}
			p.fields = append(p.fields, projField{expr: w.RawExpr, alias: alias})
			p.schema = append(p.schema, alias)
			continue
		}

		// Check for [AS] alias: e.g. "col AS alias" or "col alias"
		aliasIdx := strings.Index(strings.ToUpper(item), " AS ")
		if aliasIdx != -1 {
			expr := strings.TrimSpace(item[:aliasIdx])
			alias := strings.Trim(strings.TrimSpace(item[aliasIdx+4:]), "\"`[]")
			addField(expr, alias)
		} else {
			parts := strings.Fields(item)
			if len(parts) == 2 && !strings.Contains(parts[0], "(") && !strings.Contains(parts[0], "-") {
				addField(parts[0], strings.Trim(parts[1], "\"`[]"))
			} else {
				// Bare expression / column name
				alias := item
				if dotIdx := strings.LastIndex(alias, "."); dotIdx != -1 {
					alias = alias[dotIdx+1:]
				}
				alias = strings.Trim(alias, "\"`[]")
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
		return tuple
	}
	tuple.Materialize()
	vals := make([]any, len(p.fields))
	for i, f := range p.fields {
		// Prioritize qualified expression (e.g. "o.amount", "u.name") over unqualified alias
		// to prevent shadowing when multiple relations share identical column names.
		if val, ok := tuple.GetOK(f.expr); ok {
			vals[i] = val
			continue
		}
		if val, ok := tuple.GetOK(f.alias); ok {
			vals[i] = val
			continue
		}
		if f.parsed != nil {
			if res, err := proc.EvalRowExpr(f.parsed, tuple.ToEvalMap(), nil); err == nil {
				vals[i] = res
				continue
			}
		}
		val, found := lookupVal(tuple, f.expr)
		if !found {
			val, _ = lookupVal(tuple, f.alias)
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

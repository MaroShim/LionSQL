package execution

import (
	"context"
	"fmt"
	"strings"

	"sqlion/pkg/planner"
	"sqlion/pkg/proc"
)

type projField struct {
	expr   string
	alias  string
	parsed proc.Expr
}

// ProjectionIterator evaluates projected expressions and renames columns.
type ProjectionIterator struct {
	child       Iterator
	fields      []projField
	isSelectAll bool
	schema      []string
	colIndexMap *ColumnIndexMap
}

// NewProjectionIterator creates a new projection operator.
func NewProjectionIterator(child Iterator, projections []string) *ProjectionIterator {
	p := &ProjectionIterator{
		child: child,
	}

	if len(projections) == 0 || (len(projections) == 1 && (projections[0] == "*" || projections[0] == "")) {
		p.isSelectAll = true
		return p
	}

	addField := func(expr, alias string) {
		f := projField{expr: expr, alias: alias}
		if strings.Contains(expr, "->") || strings.Contains(expr, "(") {
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
		if item == "*" {
			p.isSelectAll = true
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
				if dotIdx := strings.Index(alias, "."); dotIdx != -1 {
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
	if err := p.child.Open(ctx); err != nil {
		return err
	}
	p.colIndexMap = NewColumnIndexMapFromCols(p.Schema())
	return nil
}

func (p *ProjectionIterator) Next(ctx context.Context) (Tuple, error) {
	tuple, err := p.child.Next(ctx)
	if err != nil {
		return Tuple{}, err
	}

	if p.isSelectAll && len(p.fields) == 0 {
		return tuple, nil
	}

	vals := make([]any, len(p.fields))
	for i, f := range p.fields {
		if val, ok := tuple.GetOK(f.alias); ok {
			vals[i] = val
			continue
		}
		if val, ok := tuple.GetOK(f.expr); ok {
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
	}, nil
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

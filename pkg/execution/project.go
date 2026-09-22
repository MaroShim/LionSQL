package execution

import (
	"context"
	"fmt"
	"strings"
)

type projField struct {
	expr  string
	alias string
}

// ProjectionIterator evaluates projected expressions and renames columns.
type ProjectionIterator struct {
	child       Iterator
	fields      []projField
	isSelectAll bool
	schema      []string
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

	for _, item := range projections {
		item = strings.TrimSpace(item)
		if item == "*" {
			p.isSelectAll = true
			continue
		}

		// Check for [AS] alias: e.g. "col AS alias" or "col alias"
		aliasIdx := strings.Index(strings.ToUpper(item), " AS ")
		if aliasIdx != -1 {
			expr := strings.TrimSpace(item[:aliasIdx])
			alias := strings.Trim(strings.TrimSpace(item[aliasIdx+4:]), "\"`[]")
			p.fields = append(p.fields, projField{expr: expr, alias: alias})
			p.schema = append(p.schema, alias)
		} else {
			parts := strings.Fields(item)
			if len(parts) == 2 && !strings.Contains(parts[0], "(") {
				p.fields = append(p.fields, projField{expr: parts[0], alias: strings.Trim(parts[1], "\"`[]")})
				p.schema = append(p.schema, strings.Trim(parts[1], "\"`[]"))
			} else {
				// Bare expression / column name
				alias := item
				if dotIdx := strings.Index(alias, "."); dotIdx != -1 {
					alias = alias[dotIdx+1:]
				}
				alias = strings.Trim(alias, "\"`[]")
				p.fields = append(p.fields, projField{expr: item, alias: alias})
				p.schema = append(p.schema, alias)
			}
		}
	}

	return p
}

func (p *ProjectionIterator) Open(ctx context.Context) error {
	if p.child == nil {
		return fmt.Errorf("projection: child iterator is nil")
	}
	return p.child.Open(ctx)
}

func (p *ProjectionIterator) Next(ctx context.Context) (Tuple, error) {
	tuple, err := p.child.Next(ctx)
	if err != nil {
		return nil, err
	}

	if p.isSelectAll && len(p.fields) == 0 {
		if p.child != nil {
			s := p.child.Schema()
			if len(s) > 0 {
				filtered := make(Tuple, len(s))
				for _, col := range s {
					if v, ok := lookupVal(tuple, col); ok {
						filtered[col] = v
					} else {
						filtered[col] = nil
					}
				}
				return filtered, nil
			}
		}
		return tuple, nil
	}

	out := make(Tuple, len(p.fields))
	for _, f := range p.fields {
		val, found := lookupVal(tuple, f.expr)
		if !found {
			val, found = lookupVal(tuple, f.alias)
		}
		if found {
			out[f.alias] = val
		} else {
			out[f.alias] = nil
		}
	}

	return out, nil
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

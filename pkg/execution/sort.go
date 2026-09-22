package execution

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
)

type sortItem struct {
	key        string
	descending bool
}

// SortIterator buffers incoming tuples, sorts them by one or more order keys, and streams them out.
type SortIterator struct {
	child  Iterator
	items  []sortItem
	tuples []Tuple
	idx    int
}

// NewSortIterator creates a new sort operator from an ORDER BY clause string (e.g. "age DESC, name ASC").
func NewSortIterator(child Iterator, orderByClause string) *SortIterator {
	s := &SortIterator{
		child: child,
	}

	for _, part := range strings.Split(orderByClause, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Fields(part)
		key := strings.Trim(fields[0], "\"`[]")
		desc := false
		if len(fields) > 1 && strings.EqualFold(fields[1], "DESC") {
			desc = true
		}
		s.items = append(s.items, sortItem{key: key, descending: desc})
	}

	return s
}

func (s *SortIterator) Open(ctx context.Context) error {
	if s.child == nil {
		return fmt.Errorf("sort: child iterator is nil")
	}
	if err := s.child.Open(ctx); err != nil {
		return err
	}

	s.tuples = nil
	s.idx = 0

	// Materialize all child tuples into memory
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		tuple, err := s.child.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		s.tuples = append(s.tuples, tuple)
	}

	// Stable sort by order items
	if len(s.items) > 0 {
		sort.SliceStable(s.tuples, func(i, j int) bool {
			for _, item := range s.items {
				vA, _ := lookupVal(s.tuples[i], item.key)
				vB, _ := lookupVal(s.tuples[j], item.key)
				cmp := CompareValues(vA, vB)
				if cmp != 0 {
					if item.descending {
						return cmp > 0
					}
					return cmp < 0
				}
			}
			return false
		})
	}

	return nil
}

func (s *SortIterator) Next(ctx context.Context) (Tuple, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if s.idx >= len(s.tuples) {
		return nil, io.EOF
	}

	t := s.tuples[s.idx]
	s.idx++
	return t, nil
}

func (s *SortIterator) Close() error {
	s.tuples = nil
	s.idx = 0
	if s.child != nil {
		return s.child.Close()
	}
	return nil
}

func (s *SortIterator) Schema() []string {
	if s.child != nil {
		return s.child.Schema()
	}
	return nil
}

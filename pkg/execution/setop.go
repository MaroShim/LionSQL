package execution

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"lionsql/pkg/storage"
)

// SetOpType defines the relational set operation variant.
type SetOpType int

const (
	SetOpUnion SetOpType = iota + 1
	SetOpUnionAll
	SetOpIntersect
	SetOpExcept
)

func (s SetOpType) String() string {
	switch s {
	case SetOpUnion:
		return "UNION"
	case SetOpUnionAll:
		return "UNION ALL"
	case SetOpIntersect:
		return "INTERSECT"
	case SetOpExcept:
		return "EXCEPT"
	default:
		return "UNKNOWN"
	}
}

// ParseSetOpType converts a SQL string (e.g. "UNION", "UNION ALL", "INTERSECT", "EXCEPT", "MINUS") to SetOpType.
func ParseSetOpType(raw string) (SetOpType, error) {
	upper := strings.TrimSpace(strings.ToUpper(raw))
	switch upper {
	case "UNION":
		return SetOpUnion, nil
	case "UNION ALL":
		return SetOpUnionAll, nil
	case "INTERSECT":
		return SetOpIntersect, nil
	case "EXCEPT", "MINUS":
		return SetOpExcept, nil
	default:
		return 0, fmt.Errorf("unrecognized set operator %q", raw)
	}
}

// SetOpIterator executes relational set operations (UNION, UNION ALL, INTERSECT, EXCEPT)
// over two child Volcano iterators following ANSI SQL semantics.
type SetOpIterator struct {
	left        Iterator
	right       Iterator
	op          SetOpType
	schema      []string
	colIndexMap *ColumnIndexMap

	leftDone bool
	seen     map[string]struct{}
	rightSet map[string]struct{}

	closeOnce sync.Once
	closeErr  error
}

// NewSetOpIterator creates a new SetOpIterator combining left and right iterators.
func NewSetOpIterator(left, right Iterator, op SetOpType) *SetOpIterator {
	var cols []string
	if left != nil {
		cols = left.Schema()
	}
	return &SetOpIterator{
		left:        left,
		right:       right,
		op:          op,
		schema:      cols,
		colIndexMap: NewColumnIndexMapFromCols(cols),
		seen:        make(map[string]struct{}),
		rightSet:    make(map[string]struct{}),
	}
}

func (s *SetOpIterator) Open(ctx context.Context) error {
	if s.left == nil || s.right == nil {
		return fmt.Errorf("set operator requires both left and right iterators")
	}

	if err := s.left.Open(ctx); err != nil {
		return fmt.Errorf("set operator left child open error: %w", err)
	}
	if err := s.right.Open(ctx); err != nil {
		_ = s.left.Close()
		return fmt.Errorf("set operator right child open error: %w", err)
	}

	leftSchema := s.left.Schema()
	rightSchema := s.right.Schema()

	if len(leftSchema) != len(rightSchema) {
		_ = s.left.Close()
		_ = s.right.Close()
		return fmt.Errorf("all queries combined using %s must have an equal number of expressions (%d != %d)",
			s.op.String(), len(leftSchema), len(rightSchema))
	}

	s.schema = make([]string, len(leftSchema))
	copy(s.schema, leftSchema)
	s.colIndexMap = NewColumnIndexMapFromCols(s.schema)
	s.leftDone = false
	s.seen = make(map[string]struct{})
	s.rightSet = make(map[string]struct{})

	// For INTERSECT and EXCEPT, materialize the right child into a hash set for membership testing
	if s.op == SetOpIntersect || s.op == SetOpExcept {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			t, err := s.right.Next(ctx)
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("set operator right child materialization error: %w", err)
			}
			key := encodeTupleKey(t)
			s.rightSet[key] = struct{}{}
		}
	}

	return nil
}

func (s *SetOpIterator) Next(ctx context.Context) (Tuple, error) {
	select {
	case <-ctx.Done():
		return Tuple{}, ctx.Err()
	default:
	}

	switch s.op {
	case SetOpUnionAll:
		return s.nextUnionAll(ctx)
	case SetOpUnion:
		return s.nextUnion(ctx)
	case SetOpIntersect:
		return s.nextIntersect(ctx)
	case SetOpExcept:
		return s.nextExcept(ctx)
	default:
		return Tuple{}, fmt.Errorf("unsupported set operator type: %v", s.op)
	}
}

func (s *SetOpIterator) nextUnionAll(ctx context.Context) (Tuple, error) {
	if !s.leftDone {
		t, err := s.left.Next(ctx)
		if err == nil {
			t.Index = s.colIndexMap
			return t, nil
		}
		if err != io.EOF {
			return Tuple{}, err
		}
		s.leftDone = true
	}

	t, err := s.right.Next(ctx)
	if err != nil {
		return Tuple{}, err
	}
	t.Index = s.colIndexMap
	return t, nil
}

func (s *SetOpIterator) nextUnion(ctx context.Context) (Tuple, error) {
	for {
		if !s.leftDone {
			t, err := s.left.Next(ctx)
			if err == nil {
				key := encodeTupleKey(t)
				if _, exists := s.seen[key]; !exists {
					s.seen[key] = struct{}{}
					t.Index = s.colIndexMap
					return t, nil
				}
				continue
			}
			if err != io.EOF {
				return Tuple{}, err
			}
			s.leftDone = true
		}

		t, err := s.right.Next(ctx)
		if err != nil {
			return Tuple{}, err
		}
		key := encodeTupleKey(t)
		if _, exists := s.seen[key]; !exists {
			s.seen[key] = struct{}{}
			t.Index = s.colIndexMap
			return t, nil
		}
	}
}

func (s *SetOpIterator) nextIntersect(ctx context.Context) (Tuple, error) {
	for {
		t, err := s.left.Next(ctx)
		if err != nil {
			return Tuple{}, err
		}
		key := encodeTupleKey(t)
		if _, inRight := s.rightSet[key]; inRight {
			if _, emitted := s.seen[key]; !emitted {
				s.seen[key] = struct{}{}
				t.Index = s.colIndexMap
				return t, nil
			}
		}
	}
}

func (s *SetOpIterator) nextExcept(ctx context.Context) (Tuple, error) {
	for {
		t, err := s.left.Next(ctx)
		if err != nil {
			return Tuple{}, err
		}
		key := encodeTupleKey(t)
		if _, inRight := s.rightSet[key]; !inRight {
			if _, emitted := s.seen[key]; !emitted {
				s.seen[key] = struct{}{}
				t.Index = s.colIndexMap
				return t, nil
			}
		}
	}
}

func (s *SetOpIterator) Close() error {
	s.closeOnce.Do(func() {
		var firstErr error
		if s.left != nil {
			if err := s.left.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if s.right != nil {
			if err := s.right.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		s.closeErr = firstErr
	})
	return s.closeErr
}

func (s *SetOpIterator) Schema() []string {
	return s.schema
}

func (s *SetOpIterator) ColIndexMap() *ColumnIndexMap {
	return s.colIndexMap
}

func encodeTupleKey(t Tuple) string {
	var sb strings.Builder
	for i, v := range t.Values {
		if i > 0 {
			sb.WriteByte('\x00')
		}
		if v == nil {
			sb.WriteString("N:")
			continue
		}
		switch val := v.(type) {
		case storage.Decimal:
			sb.WriteString("D:")
			sb.WriteString(val.String())
		case storage.Value:
			sb.WriteString("V:")
			sb.WriteString(val.String())
		case int64:
			sb.WriteString(fmt.Sprintf("I:%d", val))
		case int:
			sb.WriteString(fmt.Sprintf("I:%d", val))
		case float64:
			sb.WriteString(fmt.Sprintf("F:%v", val))
		case string:
			sb.WriteString("S:")
			sb.WriteString(val)
		case bool:
			sb.WriteString(fmt.Sprintf("B:%t", val))
		case time.Time:
			sb.WriteString("T:")
			sb.WriteString(val.Format(time.RFC3339Nano))
		default:
			sb.WriteString("?:")
			sb.WriteString(fmt.Sprintf("%v", val))
		}
	}
	return sb.String()
}

// SliceIterator yields tuples from an in-memory slice of rows.
type SliceIterator struct {
	tuples      []Tuple
	schema      []string
	colIndexMap *ColumnIndexMap
	idx         int
}

// NewSliceIterator creates a new SliceIterator from rows and column schema.
func NewSliceIterator(rows []map[string]any, cols []string) *SliceIterator {
	tuples := make([]Tuple, len(rows))
	colMap := NewColumnIndexMapFromCols(cols)
	for i, r := range rows {
		vals := make([]any, len(cols))
		for cIdx, c := range cols {
			if v, ok := r[c]; ok {
				vals[cIdx] = v
			} else if v, ok := r[strings.ToLower(c)]; ok {
				vals[cIdx] = v
			}
		}
		tuples[i] = Tuple{Values: vals, Index: colMap}
	}
	return &SliceIterator{
		tuples:      tuples,
		schema:      cols,
		colIndexMap: colMap,
	}
}

func (s *SliceIterator) Open(ctx context.Context) error {
	s.idx = 0
	return nil
}

func (s *SliceIterator) Next(ctx context.Context) (Tuple, error) {
	select {
	case <-ctx.Done():
		return Tuple{}, ctx.Err()
	default:
	}
	if s.idx >= len(s.tuples) {
		return Tuple{}, io.EOF
	}
	t := s.tuples[s.idx]
	s.idx++
	return t, nil
}

func (s *SliceIterator) Close() error {
	return nil
}

func (s *SliceIterator) Schema() []string {
	return s.schema
}

func (s *SliceIterator) ColIndexMap() *ColumnIndexMap {
	return s.colIndexMap
}


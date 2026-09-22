package execution

import (
	"context"
	"fmt"
	"io"
)

// LimitIterator applies LIMIT and OFFSET to an incoming tuple stream.
// It short-circuits execution as soon as the limit is reached, halting child operators.
type LimitIterator struct {
	child   Iterator
	limit   int64
	offset  int64
	emitted int64
	skipped int64
}

// NewLimitIterator creates a new limit and offset operator.
func NewLimitIterator(child Iterator, limit, offset int64) *LimitIterator {
	if offset < 0 {
		offset = 0
	}
	return &LimitIterator{
		child:  child,
		limit:  limit,
		offset: offset,
	}
}

func (l *LimitIterator) Open(ctx context.Context) error {
	if l.child == nil {
		return fmt.Errorf("limit: child iterator is nil")
	}
	l.emitted = 0
	l.skipped = 0
	return l.child.Open(ctx)
}

func (l *LimitIterator) Next(ctx context.Context) (Tuple, error) {
	// Skip rows up to offset
	for l.skipped < l.offset {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		_, err := l.child.Next(ctx)
		if err != nil {
			return nil, err
		}
		l.skipped++
	}

	// Short-circuit if limit reached
	if l.limit >= 0 && l.emitted >= l.limit {
		return nil, io.EOF
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	tuple, err := l.child.Next(ctx)
	if err != nil {
		return nil, err
	}

	l.emitted++
	return tuple, nil
}

func (l *LimitIterator) Close() error {
	if l.child != nil {
		return l.child.Close()
	}
	return nil
}

func (l *LimitIterator) Schema() []string {
	if l.child != nil {
		return l.child.Schema()
	}
	return nil
}

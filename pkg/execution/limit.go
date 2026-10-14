package execution

import (
	"context"
	"fmt"
	"io"
)

// LimitIterator applies LIMIT and OFFSET to an incoming tuple stream.
// It short-circuits execution as soon as the limit is reached, halting child operators.
type LimitIterator struct {
	child    Iterator
	limit    int64
	offset   int64
	emitted  int64
	skipped  int64
	inChunk  Chunk
	inIdx    int
	outChunk Chunk
	outIdx   int
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
	l.inChunk.Reset()
	l.inIdx = 0
	l.outChunk.Reset()
	l.outIdx = 0
	return l.child.Open(ctx)
}

func (l *LimitIterator) NextChunk(ctx context.Context, dest *Chunk) error {
	dest.Reset()
	if l.child == nil {
		return io.EOF
	}

	for dest.Len() < DefaultChunkSize {
		// Short-circuit if limit reached
		if l.limit >= 0 && l.emitted >= l.limit {
			if dest.Len() > 0 {
				return nil
			}
			return io.EOF
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// If inChunk is exhausted, pull next chunk from child
		if l.inIdx >= l.inChunk.Len() {
			l.inChunk.Reset()
			l.inIdx = 0
			fetchSize := DefaultChunkSize
			if l.limit >= 0 {
				needed := (l.limit - l.emitted) + (l.offset - l.skipped)
				if needed > 0 && int64(fetchSize) > needed {
					fetchSize = int(needed)
				}
			}
			err := PullChunk(ctx, l.child, &l.inChunk, fetchSize)
			if err != nil && err != io.EOF {
				return err
			}
			if l.inChunk.Len() == 0 && err == io.EOF {
				if dest.Len() > 0 {
					return nil
				}
				return io.EOF
			}
		}

		// Skip offset first
		if l.skipped < l.offset {
			l.inIdx++
			l.skipped++
			continue
		}

		// Take tuple
		dest.Tuples = append(dest.Tuples, l.inChunk.Tuples[l.inIdx])
		l.inIdx++
		l.emitted++
	}

	if dest.Len() == 0 {
		return io.EOF
	}
	return nil
}

func (l *LimitIterator) Next(ctx context.Context) (Tuple, error) {
	if l.outIdx < len(l.outChunk.Tuples) {
		t := l.outChunk.Tuples[l.outIdx]
		l.outIdx++
		return t, nil
	}
	l.outChunk.Reset()
	l.outIdx = 0
	if err := l.NextChunk(ctx, &l.outChunk); err != nil {
		return Tuple{}, err
	}
	if len(l.outChunk.Tuples) == 0 {
		return Tuple{}, io.EOF
	}
	t := l.outChunk.Tuples[0]
	l.outIdx = 1
	return t, nil
}

func (l *LimitIterator) Close() error {
	l.inChunk.Reset()
	l.inIdx = 0
	l.outChunk.Reset()
	l.outIdx = 0
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

func (l *LimitIterator) ColIndexMap() *ColumnIndexMap {
	if l.child != nil {
		return l.child.ColIndexMap()
	}
	return nil
}

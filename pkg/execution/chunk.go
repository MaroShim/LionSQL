package execution

import (
	"context"
	"fmt"
	"io"
	"sync"
)

const (
	// DefaultChunkSize is the canonical vector batch size (1,024 tuples) for execution pipeline streaming.
	DefaultChunkSize = 1024

	// MinChunkSize is the lower bound for vectorized processing chunks.
	MinChunkSize = 64
)

// Chunk represents a contiguous batch of Tuples flowing between vectorized operators.
type Chunk struct {
	Tuples []Tuple
}

var chunkPool = sync.Pool{
	New: func() any {
		return NewChunk(DefaultChunkSize)
	},
}

// GetChunk acquires an empty Chunk of DefaultChunkSize from the pool.
func GetChunk() *Chunk {
	c := chunkPool.Get().(*Chunk)
	c.Reset()
	return c
}

// PutChunk clears tuple references and returns the Chunk to the pool.
func PutChunk(c *Chunk) {
	if c == nil {
		return
	}
	// Defensive check: do not retain abnormally oversized chunks to bound memory (Rule 23)
	if cap(c.Tuples) > DefaultChunkSize*4 {
		return
	}
	for i := range c.Tuples {
		c.Tuples[i] = Tuple{}
	}
	c.Reset()
	chunkPool.Put(c)
}

// AcquireChunk is an alias for GetChunk.
func AcquireChunk() *Chunk {
	return GetChunk()
}

// ReleaseChunk is an alias for PutChunk.
func ReleaseChunk(c *Chunk) {
	PutChunk(c)
}

// NewChunk creates an initialized Chunk preallocated to the specified capacity.
func NewChunk(capacity int) *Chunk {
	if capacity <= 0 {
		capacity = DefaultChunkSize
	}
	return &Chunk{
		Tuples: make([]Tuple, 0, capacity),
	}
}

// Reset clears the chunk for buffer reuse without deallocating slice backing capacity.
func (c *Chunk) Reset() {
	if c != nil {
		c.Tuples = c.Tuples[:0]
	}
}

// Len returns the number of active tuples currently buffered in the chunk.
func (c *Chunk) Len() int {
	if c == nil {
		return 0
	}
	return len(c.Tuples)
}

// ChunkedIterator is an optional interface implemented by execution operators capable of batch streaming.
type ChunkedIterator interface {
	Iterator

	// NextChunk pulls the next batch of tuples into dest (up to DefaultChunkSize).
	// Returns nil on successful batch fetch, or io.EOF when no further tuples remain.
	NextChunk(ctx context.Context, dest *Chunk) error
}

// PullChunk reads up to maxChunkSize tuples into dest from it.
// If it implements ChunkedIterator, NextChunk is invoked directly for zero-virtual-dispatch streaming.
// Otherwise, it falls back to pulling tuples via Next() in a tight loop.
func PullChunk(ctx context.Context, it Iterator, dest *Chunk, maxChunkSize int) error {
	if dest == nil {
		return fmt.Errorf("pull chunk: nil destination chunk")
	}
	dest.Reset()
	if it == nil {
		return io.EOF
	}
	if maxChunkSize <= 0 {
		maxChunkSize = DefaultChunkSize
	}

	if ci, ok := it.(ChunkedIterator); ok {
		return ci.NextChunk(ctx, dest)
	}

	for dest.Len() < maxChunkSize {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		t, err := it.Next(ctx)
		if err != nil {
			if dest.Len() > 0 && err == io.EOF {
				return nil
			}
			return err
		}
		dest.Tuples = append(dest.Tuples, t)
	}
	return nil
}

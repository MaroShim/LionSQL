package execution

import (
	"context"
	"io"
	"time"

	"sqlion/pkg/planner"
)

// InstrumentedIterator wraps a Volcano Iterator and profiles runtime performance
// metrics (startup time, duration, produced rows, and execution loops) directly
// onto the associated planner.PlanNode during query execution (OBS-2).
type InstrumentedIterator struct {
	child         Iterator
	node          *planner.PlanNode
	openTime      time.Time
	firstProduced bool
	totalNanos    int64
}

// NewInstrumentedIterator creates an operator profiling wrapper.
func NewInstrumentedIterator(child Iterator, node *planner.PlanNode) *InstrumentedIterator {
	return &InstrumentedIterator{
		child: child,
		node:  node,
	}
}

// Open initializes the operator and records startup timestamp and loop count.
func (it *InstrumentedIterator) Open(ctx context.Context) error {
	it.openTime = time.Now()
	it.firstProduced = false
	it.totalNanos = 0
	if it.node != nil {
		it.node.ActualLoops++
	}
	if it.child == nil {
		return nil
	}
	return it.child.Open(ctx)
}

// Next pulls the next tuple while measuring wall-clock duration and row count.
func (it *InstrumentedIterator) Next(ctx context.Context) (Tuple, error) {
	if it.child == nil {
		return Tuple{}, io.EOF
	}

	start := time.Now()
	tuple, err := it.child.Next(ctx)
	durNanos := time.Since(start).Nanoseconds()
	it.totalNanos += durNanos

	if it.node != nil {
		us := it.totalNanos / 1000
		if it.totalNanos > 0 && us == 0 {
			us = 1
		}
		it.node.ActualDurationUS = us
		if !it.firstProduced {
			it.firstProduced = true
			startupNanos := time.Since(it.openTime).Nanoseconds()
			startupUS := startupNanos / 1000
			if startupNanos > 0 && startupUS == 0 {
				startupUS = 1
			}
			it.node.ActualStartupUS = startupUS
		}
		if err == nil && !tuple.IsNil() {
			it.node.ActualRows++
		}
	}

	return tuple, err
}

// NextChunk pulls a batch of tuples into dest while measuring batch execution time.
func (it *InstrumentedIterator) NextChunk(ctx context.Context, dest *Chunk) error {
	if it.child == nil {
		return io.EOF
	}

	start := time.Now()
	var err error
	if ci, ok := it.child.(ChunkedIterator); ok {
		err = ci.NextChunk(ctx, dest)
	} else {
		err = PullChunk(ctx, it.child, dest, DefaultChunkSize)
	}
	durNanos := time.Since(start).Nanoseconds()
	it.totalNanos += durNanos

	if it.node != nil {
		us := it.totalNanos / 1000
		if it.totalNanos > 0 && us == 0 {
			us = 1
		}
		it.node.ActualDurationUS = us
		if !it.firstProduced {
			it.firstProduced = true
			startupNanos := time.Since(it.openTime).Nanoseconds()
			startupUS := startupNanos / 1000
			if startupNanos > 0 && startupUS == 0 {
				startupUS = 1
			}
			it.node.ActualStartupUS = startupUS
		}
		if dest != nil {
			it.node.ActualRows += int64(dest.Len())
		}
	}

	return err
}

// Close releases resources allocated by the child operator.
func (it *InstrumentedIterator) Close() error {
	if it.child == nil {
		return nil
	}
	return it.child.Close()
}

// Schema returns the output column names for this operator.
func (it *InstrumentedIterator) Schema() []string {
	if it.child == nil {
		return nil
	}
	return it.child.Schema()
}

// ColIndexMap returns the column index map for O(1) resolution and join composition.
func (it *InstrumentedIterator) ColIndexMap() *ColumnIndexMap {
	if it.child == nil {
		return nil
	}
	return it.child.ColIndexMap()
}

// Underlying returns the wrapped child iterator.
func (it *InstrumentedIterator) Underlying() Iterator {
	return it.child
}

// PlanNode returns the profiled planner.PlanNode.
func (it *InstrumentedIterator) PlanNode() *planner.PlanNode {
	return it.node
}
